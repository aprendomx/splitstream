package twitch_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/platforms/twitch"
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

// servidorTwitch simula id.twitch.tv y api.twitch.tv en un solo httptest con rutas.
type servidorTwitch struct {
	*httptest.Server
	tokenPolls atomic.Int32
	// respuestas por ruta: las cambian los tests.
	tokenRes   func(r *http.Request) (int, []byte)
	patches    []map[string]any
	ultimaAuth string
}

func nuevoServidor(t *testing.T) *servidorTwitch {
	t.Helper()
	s := &servidorTwitch{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth2/device", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("client_id") != "cid" || !strings.Contains(r.Form.Get("scopes"), "user:read:chat") {
			w.WriteHeader(400)
			return
		}
		w.Write(fixture(t, "device.json"))
	})
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		s.tokenPolls.Add(1)
		code, body := s.tokenRes(r)
		w.WriteHeader(code)
		w.Write(body)
	})
	mux.HandleFunc("GET /oauth2/validate", func(w http.ResponseWriter, r *http.Request) {
		s.ultimaAuth = r.Header.Get("Authorization")
		if s.ultimaAuth != "OAuth tok-acceso-fixture" {
			w.WriteHeader(401)
			w.Write([]byte(`{"status":401,"message":"invalid access token"}`))
			return
		}
		w.Write(fixture(t, "validate.json"))
	})
	mux.HandleFunc("PATCH /helix/channels", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-Id") != "cid" || r.Header.Get("Authorization") != "Bearer tok-acceso-fixture" {
			w.WriteHeader(401)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		body["broadcaster_id"] = r.URL.Query().Get("broadcaster_id")
		s.patches = append(s.patches, body)
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /helix/search/categories", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "" {
			w.WriteHeader(400)
			return
		}
		w.Write(fixture(t, "categories.json"))
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func proveedor(t *testing.T, s *servidorTwitch) *twitch.Provider {
	return twitch.New(twitch.Options{ClientID: "cid", HTTPClient: s.Client(), AuthBase: s.URL, HelixBase: s.URL,
		EventSubURL: "ws" + strings.TrimPrefix(s.URL, "http") + "/ws"})
}

func cuenta() store.Account {
	return store.Account{ID: 7, Platform: store.PlatformTwitch, ExternalID: "141981764", DisplayName: "twitchdev"}
}

func TestDeviceFlowPendingThenDone(t *testing.T) {
	s := nuevoServidor(t)
	s.tokenRes = func(r *http.Request) (int, []byte) {
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.Form.Get("device_code") != "ike3GM8QIdYZs43KdrWPIO36LofILoCyFEzjlQ91" {
			return 400, []byte(`{"status":400,"message":"invalid device code"}`)
		}
		if s.tokenPolls.Load() < 3 {
			return 400, fixture(t, "pending.json")
		}
		return 200, fixture(t, "token.json")
	}
	p := proveedor(t, s)
	ctx := context.Background()
	prompt, err := p.BeginAuth(ctx, platforms.Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	if prompt.UserCode != "ABCDEFGH" || !strings.HasPrefix(prompt.VerificationURI, "https://www.twitch.tv/activate") ||
		prompt.Interval != 5*time.Second || prompt.State == "" || prompt.DeviceCode == "" {
		t.Fatalf("prompt = %+v", prompt)
	}
	for i := 0; i < 2; i++ {
		if _, err := p.PollAuth(ctx, platforms.Credentials{}, prompt); !errors.Is(err, platforms.ErrAuthPending) {
			t.Fatalf("poll %d: err = %v, quería ErrAuthPending", i, err)
		}
	}
	acct, err := p.PollAuth(ctx, platforms.Credentials{}, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Platform != store.PlatformTwitch || acct.ExternalID != "141981764" || acct.DisplayName != "twitchdev" ||
		acct.Tokens.Access.Reveal() != "tok-acceso-fixture" || acct.Tokens.Refresh.Reveal() != "tok-refresco-fixture" ||
		len(acct.Scopes) != 2 || time.Until(acct.Tokens.ExpiresAt) < 3*time.Hour {
		t.Errorf("cuenta = %+v", acct)
	}
}

func TestDeviceFlowExpiresAndSlowsDown(t *testing.T) {
	s := nuevoServidor(t)
	s.tokenRes = func(*http.Request) (int, []byte) { return 400, []byte(`{"status":400,"message":"slow_down"}`) }
	ahora := time.Now()
	p := twitch.New(twitch.Options{ClientID: "cid", HTTPClient: s.Client(), AuthBase: s.URL, HelixBase: s.URL,
		Now: func() time.Time { return ahora }})
	prompt, _ := p.BeginAuth(context.Background(), platforms.Credentials{})
	_, err := p.PollAuth(context.Background(), platforms.Credentials{}, prompt)
	if !errors.Is(err, platforms.ErrAuthPending) {
		t.Fatalf("slow_down: err = %v", err)
	}
	// slow_down no está documentado por Twitch; si llega, se pide esperar el doble.
	if prompt.Interval != 5*time.Second {
		t.Errorf("el prompt original no cambia: %v", prompt.Interval)
	}
	ahora = ahora.Add(31 * time.Minute)
	if _, err := p.PollAuth(context.Background(), platforms.Credentials{}, prompt); !errors.Is(err, platforms.ErrAuthExpired) {
		t.Errorf("vencido: err = %v", err)
	}
}

func TestRefreshRotatesWithoutClientSecret(t *testing.T) {
	s := nuevoServidor(t)
	s.tokenRes = func(r *http.Request) (int, []byte) {
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "viejo" || r.Form.Has("client_secret") {
			return 400, []byte(`{"status":400,"message":"Invalid refresh token"}`)
		}
		return 200, fixture(t, "refresh.json")
	}
	p := proveedor(t, s)
	tok, err := p.Refresh(context.Background(), cuenta(), platforms.Credentials{}, "viejo")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access.Reveal() != "1ssjqsqfy6bads1ws7m03gras79zfr" || tok.Refresh.Reveal() == "" || time.Until(tok.ExpiresAt) < 4*time.Hour {
		t.Errorf("tokens = %+v", tok)
	}
	if _, err := p.Refresh(context.Background(), cuenta(), platforms.Credentials{}, "otro"); !errors.Is(err, platforms.ErrUnauthorized) {
		t.Errorf("refresh inválido: err = %v, quería ErrUnauthorized", err)
	}
}

func TestValidateUsesOAuthHeaderAndMapsIdentity(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(t, s)
	id, err := p.Validate(context.Background(), "tok-acceso-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if id.ExternalID != "141981764" || id.DisplayName != "twitchdev" || len(id.Scopes) != 2 || id.ExpiresIn == 0 {
		t.Errorf("identity = %+v", id)
	}
	if _, err := p.Validate(context.Background(), "malo"); !errors.Is(err, platforms.ErrUnauthorized) {
		t.Errorf("token malo: err = %v", err)
	}
}

func TestSetTitleTrimsTo140AndSetCategoryUsesGameID(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(t, s)
	largo := strings.Repeat("a", 150)
	if err := p.SetTitle(context.Background(), cuenta(), "tok-acceso-fixture", largo); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCategory(context.Background(), cuenta(), "tok-acceso-fixture", "509670"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCategory(context.Background(), cuenta(), "tok-acceso-fixture", ""); err != nil {
		t.Fatal(err)
	}
	if len(s.patches) != 3 || s.patches[0]["broadcaster_id"] != "141981764" || len(s.patches[0]["title"].(string)) != 140 ||
		s.patches[1]["game_id"] != "509670" || s.patches[2]["game_id"] != "0" {
		t.Errorf("patches = %v", s.patches)
	}
	if err := p.SetTitle(context.Background(), cuenta(), "malo", "x"); !errors.Is(err, platforms.ErrUnauthorized) {
		t.Errorf("401: err = %v", err)
	}
	if err := p.SetTitle(context.Background(), cuenta(), "tok-acceso-fixture", "   "); err == nil {
		t.Error("título vacío debería rechazarse antes de llamar")
	}
}

func TestSearchCategoriesFillsBoxArtSize(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(t, s)
	cats, err := p.SearchCategories(context.Background(), "tok-acceso-fixture", "science")
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 1 || cats[0].ID != "509670" || cats[0].Name != "Science & Technology" ||
		strings.Contains(cats[0].BoxArtURL, "{width}") || !strings.Contains(cats[0].BoxArtURL, "144x192") {
		t.Errorf("cats = %+v", cats)
	}
}

func TestRateLimitBecomesErrRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Ratelimit-Reset", "1781653392")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	p := twitch.New(twitch.Options{ClientID: "cid", HTTPClient: srv.Client(), AuthBase: srv.URL, HelixBase: srv.URL})
	err := p.SetTitle(context.Background(), cuenta(), "t", "hola")
	if !errors.Is(err, platforms.ErrRateLimited) {
		t.Errorf("err = %v", err)
	}
}

func TestWithoutClientIDNothingWorks(t *testing.T) {
	p := twitch.New(twitch.Options{})
	if p.Configured() {
		t.Error("sin client_id no está configurado")
	}
	if _, err := p.BeginAuth(context.Background(), platforms.Credentials{}); !errors.Is(err, platforms.ErrNoClientID) {
		t.Errorf("err = %v", err)
	}
	if _, err := p.SearchCategories(context.Background(), "tok", "science"); !errors.Is(err, platforms.ErrNoClientID) {
		t.Errorf("buscar categorías sin client_id: err = %v", err)
	}
	if twitch.ResolveClientID("  ") != twitch.ClientID || twitch.ResolveClientID("propio") != "propio" {
		t.Error("ResolveClientID: el entorno manda; vacío es el incluido")
	}
}

func TestNoTokenEverAppearsInErrors(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(t, s)
	err := p.SetTitle(context.Background(), cuenta(), "tok-secreto-xyz", "hola")
	if err == nil || strings.Contains(err.Error(), "tok-secreto-xyz") {
		t.Errorf("el error lleva el token: %v", err)
	}
}
