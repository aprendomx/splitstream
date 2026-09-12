package youtube_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/platforms/youtube"
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

// servidorGoogle simula oauth2.googleapis.com y www.googleapis.com/youtube/v3 en un solo
// httptest: OAuthBase y APIBase apuntan los dos a s.URL, las rutas no chocan. tokenRes,
// channelsRes y tokeninfoRes son inyectables; por defecto responden lo mismo que la vida
// real para el caso feliz.
type servidorGoogle struct {
	*httptest.Server
	tokenPolls   atomic.Int32
	channelCalls atomic.Int32

	tokenRes     func(r *http.Request) (int, []byte)
	channelsRes  func(r *http.Request) (int, []byte)
	tokeninfoRes func(r *http.Request) (int, []byte)

	deviceClientID     string
	deviceScope        string
	ultimaAuthChannels string
}

func nuevoServidor(t *testing.T) *servidorGoogle {
	t.Helper()
	s := &servidorGoogle{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /device/code", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		s.deviceClientID = r.Form.Get("client_id")
		s.deviceScope = r.Form.Get("scope")
		if s.deviceClientID == "" || s.deviceScope == "" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_request","error_description":"falta client_id o scope"}`))
			return
		}
		w.Write(fixture(t, "device.json"))
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		s.tokenPolls.Add(1)
		code, body := s.tokenRes(r)
		w.WriteHeader(code)
		w.Write(body)
	})
	mux.HandleFunc("GET /tokeninfo", func(w http.ResponseWriter, r *http.Request) {
		if s.tokeninfoRes != nil {
			code, body := s.tokeninfoRes(r)
			w.WriteHeader(code)
			w.Write(body)
			return
		}
		if r.URL.Query().Get("access_token") != "ya29.acceso-fixture" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error_description":"Invalid Value"}`))
			return
		}
		w.Write(fixture(t, "tokeninfo.json"))
	})
	mux.HandleFunc("GET /channels", func(w http.ResponseWriter, r *http.Request) {
		s.channelCalls.Add(1)
		s.ultimaAuthChannels = r.Header.Get("Authorization")
		if s.channelsRes != nil {
			code, body := s.channelsRes(r)
			w.WriteHeader(code)
			w.Write(body)
			return
		}
		if r.URL.Query().Get("mine") != "true" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"falta mine=true","errors":[{"reason":"invalidParameter"}]}}`))
			return
		}
		w.Write(fixture(t, "channels_mine.json"))
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func proveedor(s *servidorGoogle, quota func(int64) platforms.QuotaSink) *youtube.Provider {
	return youtube.New(youtube.Options{HTTPClient: s.Client(), OAuthBase: s.URL, APIBase: s.URL, Quota: quota})
}

func creds() platforms.Credentials {
	return platforms.Credentials{ClientID: "cid", ClientSecret: "csec"}
}

func TestDeviceFlowRequiresOwnCredentialsAndUsesTheYouTubeScope(t *testing.T) {
	s := nuevoServidor(t)
	p := proveedor(s, nil)
	ctx := context.Background()

	if _, err := p.BeginAuth(ctx, platforms.Credentials{}); !errors.Is(err, platforms.ErrNoClientID) {
		t.Fatalf("sin credenciales: err = %v, quería ErrNoClientID", err)
	}

	prompt, err := p.BeginAuth(ctx, creds())
	if err != nil {
		t.Fatal(err)
	}
	if s.deviceClientID != "cid" || s.deviceScope != youtube.Scope || strings.Contains(s.deviceScope, "force-ssl") {
		t.Errorf("device/code recibió client_id=%q scope=%q", s.deviceClientID, s.deviceScope)
	}
	if prompt.UserCode != "GQVQ-JKEC" || prompt.VerificationURI != "https://www.google.com/device" ||
		prompt.Interval != 5*time.Second || prompt.State == "" || prompt.DeviceCode == "" {
		t.Fatalf("prompt = %+v", prompt)
	}
}

func TestDeviceFlowPendingSlowDownDeniedAndDone(t *testing.T) {
	s := nuevoServidor(t)
	ctx := context.Background()
	sinks := map[int64]int{}
	quota := func(id int64) platforms.QuotaSink {
		return func(units int) { sinks[id] += units }
	}
	p := proveedor(s, quota)

	prompt, err := p.BeginAuth(ctx, creds())
	if err != nil {
		t.Fatal(err)
	}

	s.tokenRes = func(*http.Request) (int, []byte) { return 400, fixture(t, "pending.json") }
	if _, err := p.PollAuth(ctx, creds(), prompt); !errors.Is(err, platforms.ErrAuthPending) {
		t.Fatalf("pending: err = %v, quería ErrAuthPending", err)
	}

	antes := prompt
	s.tokenRes = func(*http.Request) (int, []byte) { return 400, fixture(t, "slow_down.json") }
	if _, err := p.PollAuth(ctx, creds(), prompt); !errors.Is(err, platforms.ErrAuthPending) {
		t.Fatalf("slow_down: err = %v, quería ErrAuthPending", err)
	}
	if prompt != antes {
		t.Errorf("slow_down cambió el prompt: antes %+v, ahora %+v", antes, prompt)
	}

	// access_denied va envuelto en el centinela: sin él, quien sondea no puede
	// distinguirlo de un fallo transitorio y sigue preguntando hasta que venza el código.
	s.tokenRes = func(*http.Request) (int, []byte) { return 400, fixture(t, "denied.json") }
	if _, err := p.PollAuth(ctx, creds(), prompt); !errors.Is(err, platforms.ErrAuthDenied) ||
		errors.Is(err, platforms.ErrAuthPending) || errors.Is(err, platforms.ErrAuthExpired) {
		t.Fatalf("access_denied: err = %v, quería ErrAuthDenied", err)
	}

	s.tokenRes = func(r *http.Request) (int, []byte) {
		if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "csec" ||
			r.Form.Get("device_code") != prompt.DeviceCode ||
			r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
			return 400, []byte(`{"error":"invalid_request"}`)
		}
		return 200, fixture(t, "token.json")
	}
	acct, err := p.PollAuth(ctx, creds(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Platform != store.PlatformYouTube || acct.ExternalID != "UCabc123" || acct.DisplayName != "Mi canal" ||
		len(acct.Scopes) != 1 || acct.Scopes[0] != youtube.Scope || !acct.OwnApp ||
		acct.Credentials.ClientID.Reveal() != "cid" || acct.Credentials.ClientSecret.Reveal() != "csec" ||
		acct.Tokens.Access.Reveal() != "ya29.acceso-fixture" || acct.Tokens.Refresh.Reveal() != "1//refresco-fixture" ||
		time.Until(acct.Tokens.ExpiresAt) < 3500*time.Second {
		t.Errorf("cuenta = %+v", acct)
	}
	if s.channelCalls.Load() != 1 || s.ultimaAuthChannels != "Bearer ya29.acceso-fixture" {
		t.Errorf("channels.list: llamadas=%d auth=%q", s.channelCalls.Load(), s.ultimaAuthChannels)
	}
	// Ruling (no está en el brief original): en PollAuth todavía no hay cuenta con id, así
	// que la unidad de channels.list para la identidad no se contabiliza en ningún
	// sumidero. Se ajusta el test del brief: no se exige la suma aquí.
	if len(sinks) != 0 {
		t.Errorf("PollAuth no debería gastar cuota todavía: sinks = %v", sinks)
	}
}

func TestPollExpires(t *testing.T) {
	s := nuevoServidor(t)
	ctx := context.Background()
	ahora := time.Now()
	p := youtube.New(youtube.Options{HTTPClient: s.Client(), OAuthBase: s.URL, APIBase: s.URL,
		Now: func() time.Time { return ahora }})

	prompt, err := p.BeginAuth(ctx, creds())
	if err != nil {
		t.Fatal(err)
	}
	ahora = ahora.Add(31 * time.Minute)
	if _, err := p.PollAuth(ctx, creds(), prompt); !errors.Is(err, platforms.ErrAuthExpired) {
		t.Fatalf("vencido por reloj: err = %v, quería ErrAuthExpired", err)
	}

	ahora = time.Now()
	prompt, err = p.BeginAuth(ctx, creds())
	if err != nil {
		t.Fatal(err)
	}
	s.tokenRes = func(*http.Request) (int, []byte) { return 400, []byte(`{"error":"invalid_grant"}`) }
	if _, err := p.PollAuth(ctx, creds(), prompt); !errors.Is(err, platforms.ErrAuthExpired) {
		t.Fatalf("invalid_grant: err = %v, quería ErrAuthExpired", err)
	}
}

func TestRefreshKeepsTheOldRefreshTokenWhenGoogleOmitsIt(t *testing.T) {
	s := nuevoServidor(t)
	ctx := context.Background()
	acct := store.Account{ID: 9, Platform: store.PlatformYouTube, ExternalID: "UCabc123"}
	p := proveedor(s, nil)

	s.tokenRes = func(r *http.Request) (int, []byte) {
		if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "csec" ||
			r.Form.Get("refresh_token") != "1//viejo" || r.Form.Get("grant_type") != "refresh_token" {
			return 400, []byte(`{"error":"invalid_request"}`)
		}
		return 200, fixture(t, "refresh.json")
	}
	tok, err := p.Refresh(ctx, acct, creds(), "1//viejo")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access.Reveal() != "ya29.acceso-fixture" || tok.Refresh.Reveal() != "1//viejo" ||
		time.Until(tok.ExpiresAt) < 3500*time.Second {
		t.Errorf("tokens = %+v", tok)
	}

	s.tokenRes = func(*http.Request) (int, []byte) { return 400, []byte(`{"error":"invalid_grant"}`) }
	if _, err := p.Refresh(ctx, acct, creds(), "1//viejo"); !errors.Is(err, platforms.ErrUnauthorized) {
		t.Errorf("invalid_grant: err = %v, quería ErrUnauthorized", err)
	}

	if _, err := p.Refresh(ctx, acct, platforms.Credentials{}, "1//viejo"); !errors.Is(err, platforms.ErrNoClientID) {
		t.Errorf("sin credenciales: err = %v, quería ErrNoClientID", err)
	}
}

func TestValidateMapsTokeninfo(t *testing.T) {
	s := nuevoServidor(t)
	ctx := context.Background()
	p := proveedor(s, nil)

	id, err := p.Validate(ctx, "ya29.acceso-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if id.ExpiresIn != 3400*time.Second || id.ExternalID != "" {
		t.Errorf("identity = %+v", id)
	}

	if _, err := p.Validate(ctx, "malo"); !errors.Is(err, platforms.ErrUnauthorized) {
		t.Errorf("token malo: err = %v, quería ErrUnauthorized", err)
	}
}

func TestErrorsCarryTheReasonNeverTheToken(t *testing.T) {
	s := nuevoServidor(t)
	ctx := context.Background()
	p := proveedor(s, nil)

	prompt, err := p.BeginAuth(ctx, creds())
	if err != nil {
		t.Fatal(err)
	}
	s.tokenRes = func(*http.Request) (int, []byte) { return 200, fixture(t, "token.json") }
	s.channelsRes = func(*http.Request) (int, []byte) { return 403, fixture(t, "error_quota.json") }

	_, err = p.PollAuth(ctx, creds(), prompt)
	if err == nil || !strings.Contains(err.Error(), "quotaExceeded") || strings.Contains(err.Error(), "ya29") {
		t.Fatalf("err = %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	// Ruling (diferible de Task 3): el invariante "ningún New de test sin hosts" es
	// literal, así que aquí también se inyectan hosts dummy que jamás se llaman.
	p := youtube.New(youtube.Options{OAuthBase: "http://127.0.0.1:0", APIBase: "http://127.0.0.1:0"})
	if p.ID() != platforms.YouTube {
		t.Errorf("ID() = %v", p.ID())
	}
	caps := p.Capabilities()
	if !caps.Title || !caps.Schedule || !caps.IngestKey || !caps.ChatRead || !caps.RequiresOwnApp || caps.Category {
		t.Errorf("capabilities = %+v", caps)
	}
	if !p.Configured() {
		t.Error("Configured() debería ser true: las credenciales van por cuenta, no por app incluida")
	}
}
