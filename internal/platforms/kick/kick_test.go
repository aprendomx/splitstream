package kick_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/platforms/kick"
	"github.com/aprendomx/splitstream/internal/store"
)

func fixture(t *testing.T, nombre string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + nombre)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// s256 calcula el code_challenge tal como lo exige PKCE: SHA-256 del verifier en
// base64url sin relleno.
func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// servidorKick simula id.kick.com y api.kick.com en un solo httptest con rutas.
type servidorKick struct {
	*httptest.Server
	// challenge es el code_challenge que el test guarda desde la URL de authorize, para
	// comprobar que el code_verifier del intercambio corresponde.
	challenge     string
	patches       []map[string]any
	categoryCalls atomic.Int32
	// tokenRes, cuando no es nil, sustituye la lógica normal de /oauth/token (para
	// forzar un 401 en el refresh, por ejemplo).
	tokenRes func(r *http.Request) (int, []byte)
}

func nuevoServidor(t *testing.T) *servidorKick {
	t.Helper()
	s := &servidorKick{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if s.tokenRes != nil {
			code, body := s.tokenRes(r)
			w.WriteHeader(code)
			w.Write(body)
			return
		}
		if r.Form.Get("client_id") != "kid" || r.Form.Get("client_secret") != "ksec" || r.Form.Get("grant_type") == "" {
			w.WriteHeader(400)
			w.Write([]byte(`{"message":"invalid_client"}`))
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			verifier := r.Form.Get("code_verifier")
			if r.Form.Get("code") != "codigo" || r.Form.Get("redirect_uri") == "" || s256(verifier) != s.challenge {
				w.WriteHeader(400)
				w.Write([]byte(`{"message":"invalid_grant"}`))
				return
			}
			w.Write(fixture(t, "token.json"))
		case "refresh_token":
			if r.Form.Get("refresh_token") != "kick-refresco-fixture" {
				w.WriteHeader(400)
				w.Write([]byte(`{"message":"invalid_grant"}`))
				return
			}
			w.Write(fixture(t, "refresh.json"))
		default:
			w.WriteHeader(400)
			w.Write([]byte(`{"message":"unsupported_grant_type"}`))
		}
	})
	mux.HandleFunc("GET /public/v1/users", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer kick-acceso-fixture", "Bearer kick-acceso-2":
			w.Write(fixture(t, "users.json"))
		default:
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"unauthorized"}`))
		}
	})
	mux.HandleFunc("GET /public/v1/channels", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("broadcaster_user_id") != "123" {
			w.WriteHeader(400)
			w.Write([]byte(`{"message":"broadcaster_user_id requerido"}`))
			return
		}
		w.Write(fixture(t, "channels.json"))
	})
	mux.HandleFunc("PATCH /public/v1/channels", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer kick-acceso-fixture" {
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"unauthorized"}`))
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		s.patches = append(s.patches, body)
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /public/v1/categories", func(w http.ResponseWriter, r *http.Request) {
		s.categoryCalls.Add(1)
		if r.URL.Query().Get("q") == "" {
			w.WriteHeader(400)
			w.Write([]byte(`{"message":"q requerido"}`))
			return
		}
		w.Write(fixture(t, "categories.json"))
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func proveedor(t *testing.T, s *servidorKick) *kick.Provider {
	t.Helper()
	return kick.New(kick.Options{HTTPClient: s.Client(), AuthBase: s.URL, APIBase: s.URL})
}

func creds() platforms.Credentials {
	return platforms.Credentials{ClientID: "kid", ClientSecret: "ksec"}
}

func cuenta() store.Account {
	return store.Account{ID: 9, Platform: store.PlatformKick, ExternalID: "123", DisplayName: "John Doe"}
}

func TestBeginRedirectBuildsThePKCEAuthorizeURL(t *testing.T) {
	s := nuevoServidor(t)
	ahora := time.Now()
	p := kick.New(kick.Options{HTTPClient: s.Client(), AuthBase: s.URL, APIBase: s.URL, Now: func() time.Time { return ahora }})

	prompt, err := p.BeginRedirect(context.Background(), creds(), "http://localhost:8080/api/platforms/kick/callback", "estado.firmado")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(prompt.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(s.URL)
	if u.Scheme != base.Scheme || u.Host != base.Host || u.Path != "/oauth/authorize" {
		t.Fatalf("redirect url = %s, quería host/path de %s + /oauth/authorize", prompt.RedirectURL, s.URL)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != "kid" ||
		q.Get("redirect_uri") != "http://localhost:8080/api/platforms/kick/callback" ||
		q.Get("scope") != kick.Scopes || q.Get("code_challenge_method") != "S256" || q.Get("state") != "estado.firmado" {
		t.Fatalf("query = %v", q)
	}
	if n := len(prompt.CodeVerifier); n < 43 || n > 128 {
		t.Errorf("CodeVerifier longitud = %d, quería 43-128", n)
	}
	if got := s256(prompt.CodeVerifier); got != q.Get("code_challenge") {
		t.Errorf("code_challenge = %s, no corresponde a S256(verifier) = %s", q.Get("code_challenge"), got)
	}
	if d := prompt.ExpiresAt.Sub(ahora); d < 9*time.Minute || d > 11*time.Minute {
		t.Errorf("ExpiresAt - ahora = %v, quería ~10min", d)
	}
	if prompt.RedirectURI != "http://localhost:8080/api/platforms/kick/callback" {
		t.Errorf("RedirectURI = %q", prompt.RedirectURI)
	}

	if _, err := p.BeginRedirect(context.Background(), platforms.Credentials{}, "http://x", "s"); !errors.Is(err, platforms.ErrNoClientID) {
		t.Errorf("sin credenciales: err = %v, quería ErrNoClientID", err)
	}
	if _, err := p.BeginAuth(context.Background(), creds()); !errors.Is(err, kick.ErrUseRedirect) {
		t.Errorf("BeginAuth: err = %v, quería ErrUseRedirect", err)
	}
	if _, err := p.PollAuth(context.Background(), creds(), platforms.AuthPrompt{}); !errors.Is(err, kick.ErrUseRedirect) {
		t.Errorf("PollAuth: err = %v, quería ErrUseRedirect", err)
	}
}

func TestCompleteRedirectExchangesTheCodeAndBuildsTheAccount(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(t, s)

	prompt, err := p.BeginRedirect(context.Background(), creds(), "http://localhost:8080/api/platforms/kick/callback", "estado.firmado")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(prompt.RedirectURL)
	s.challenge = u.Query().Get("code_challenge")

	acct, err := p.CompleteRedirect(context.Background(), creds(), prompt, "codigo")
	if err != nil {
		t.Fatal(err)
	}
	if acct.Platform != store.PlatformKick || acct.ExternalID != "123" || acct.DisplayName != "John Doe" ||
		len(acct.Scopes) != 5 || !acct.OwnApp || acct.Credentials.ClientID.Reveal() != "kid" ||
		acct.Tokens.Access.Reveal() != "kick-acceso-fixture" || acct.Tokens.Refresh.Reveal() != "kick-refresco-fixture" ||
		time.Until(acct.Tokens.ExpiresAt) < 7000*time.Second {
		t.Errorf("cuenta = %+v", acct)
	}

	// Código rechazado por el servidor.
	if _, err := p.CompleteRedirect(context.Background(), creds(), prompt, "codigo-malo"); err == nil {
		t.Error("código inválido debería fallar")
	}

	// Sin client_secret: el servidor lo rechaza y el proveedor devuelve el error.
	sinSecret := platforms.Credentials{ClientID: "kid"}
	if _, err := p.CompleteRedirect(context.Background(), sinSecret, prompt, "codigo"); err == nil {
		t.Error("sin client_secret debería fallar")
	}
}

func TestRefreshRotates(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(t, s)

	tok, err := p.Refresh(context.Background(), cuenta(), creds(), "kick-refresco-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access.Reveal() != "kick-acceso-2" || tok.Refresh.Reveal() != "kick-refresco-2" || time.Until(tok.ExpiresAt) < 7000*time.Second {
		t.Errorf("tokens = %+v", tok)
	}

	// 400: refresh token que ya no vale.
	if _, err := p.Refresh(context.Background(), cuenta(), creds(), "refresh-malo"); !errors.Is(err, platforms.ErrUnauthorized) {
		t.Errorf("400: err = %v, quería ErrUnauthorized", err)
	}

	// 401 directo del servidor.
	s.tokenRes = func(*http.Request) (int, []byte) { return 401, []byte(`{"message":"unauthorized"}`) }
	if _, err := p.Refresh(context.Background(), cuenta(), creds(), "kick-refresco-fixture"); !errors.Is(err, platforms.ErrUnauthorized) {
		t.Errorf("401: err = %v, quería ErrUnauthorized", err)
	}
	s.tokenRes = nil

	if _, err := p.Refresh(context.Background(), cuenta(), platforms.Credentials{}, "x"); !errors.Is(err, platforms.ErrNoClientID) {
		t.Errorf("sin client_id: err = %v, quería ErrNoClientID", err)
	}
}

func TestValidateUsesUsers(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(t, s)

	id, err := p.Validate(context.Background(), "kick-acceso-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if id.ExternalID != "123" || id.DisplayName != "John Doe" {
		t.Errorf("identity = %+v", id)
	}
	if _, err := p.Validate(context.Background(), "malo"); !errors.Is(err, platforms.ErrUnauthorized) {
		t.Errorf("token malo: err = %v, quería ErrUnauthorized", err)
	}
}

func TestSetTitleAndCategoryPatchChannels(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(t, s)

	if err := p.SetTitle(context.Background(), cuenta(), "kick-acceso-fixture", "Nuevo título"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetTitle(context.Background(), cuenta(), "kick-acceso-fixture", "   "); err == nil {
		t.Error("título vacío debería rechazarse antes de llamar")
	}
	if err := p.SetCategory(context.Background(), cuenta(), "kick-acceso-fixture", "101"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCategory(context.Background(), cuenta(), "kick-acceso-fixture", ""); err == nil {
		t.Error("id vacío debería fallar: Kick no permite quitar la categoría")
	}
	if err := p.SetCategory(context.Background(), cuenta(), "kick-acceso-fixture", "abc"); err == nil {
		t.Error("id no numérico debería fallar sin llamar")
	}
	if len(s.patches) != 2 {
		t.Fatalf("patches = %v, quería 2 (título y categoría; los rechazos no llaman)", s.patches)
	}
	if s.patches[0]["stream_title"] != "Nuevo título" {
		t.Errorf("patch de título = %v", s.patches[0])
	}
	catID, ok := s.patches[1]["category_id"].(float64)
	if !ok || catID != 101 {
		t.Errorf("patch de categoría = %v, category_id debería ser el entero 101", s.patches[1])
	}

	cats, err := p.SearchCategories(context.Background(), "kick-acceso-fixture", "rune")
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 1 || cats[0].ID != "101" || cats[0].Name != "Old School Runescape" ||
		!strings.Contains(cats[0].BoxArtURL, "old-school-runescape") {
		t.Errorf("cats = %+v", cats)
	}

	antes := s.categoryCalls.Load()
	vacio, err := p.SearchCategories(context.Background(), "kick-acceso-fixture", "")
	if err != nil || len(vacio) != 0 {
		t.Errorf("q vacía: cats=%v err=%v, quería []", vacio, err)
	}
	if s.categoryCalls.Load() != antes {
		t.Error("q vacía no debería llamar a la API")
	}
}

func TestIngestKeyReadsTheChannel(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(t, s)

	urlIngesta, key, err := p.IngestKey(context.Background(), cuenta(), "kick-acceso-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if urlIngesta != "rtmps://stream.kick.com/1234567890" || key.Reveal() != "super-secret-stream-key" {
		t.Errorf("ingest = %s / %s", urlIngesta, key.Reveal())
	}

	sinClave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":"","data":[{"broadcaster_user_id":123,"stream":{"url":"rtmps://stream.kick.com/1234567890"}}]}`))
	}))
	defer sinClave.Close()
	p2 := kick.New(kick.Options{HTTPClient: sinClave.Client(), AuthBase: sinClave.URL, APIBase: sinClave.URL})
	_, _, err = p2.IngestKey(context.Background(), cuenta(), "tok")
	if err == nil || !strings.Contains(err.Error(), "streamkey:read") {
		t.Errorf("sin clave: err = %v, quería que mencionara streamkey:read", err)
	}
	if err != nil && strings.Contains(err.Error(), "super-secret-stream-key") {
		t.Error("el error no debe llevar la clave de stream")
	}
}

func TestCapabilities(t *testing.T) {
	p := kick.New(kick.Options{})
	c := p.Capabilities()
	if !c.Title || !c.Category || !c.ChatRead || !c.IngestKey || !c.RequiresOwnApp || !c.RequiresPublicURL || c.Schedule {
		t.Errorf("capabilities = %+v", c)
	}
	if !p.Configured() {
		t.Error("Configured debería ser true: las credenciales de Kick van por cuenta")
	}
}

func TestNoTokenEverAppearsInErrors(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(t, s)
	err := p.SetTitle(context.Background(), cuenta(), "tok-secreto-xyz", "hola")
	if err == nil || strings.Contains(err.Error(), "tok-secreto-xyz") {
		t.Errorf("el error lleva el token: %v", err)
	}
	if _, err := p.Validate(context.Background(), "tok-secreto-xyz"); err == nil || strings.Contains(err.Error(), "tok-secreto-xyz") {
		t.Errorf("el error de Validate lleva el token: %v", err)
	}
}
