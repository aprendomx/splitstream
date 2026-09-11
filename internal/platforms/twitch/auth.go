package twitch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

const grantDevice = "urn:ietf:params:oauth:grant-type:device_code"

// BeginAuth pide un código de dispositivo. State es aleatorio y es lo único que la API
// enseña para sondear; el device_code se queda en el servidor.
func (p *Provider) BeginAuth(ctx context.Context) (platforms.AuthPrompt, error) {
	if !p.Configured() {
		return platforms.AuthPrompt{}, platforms.ErrNoClientID
	}
	form := url.Values{"client_id": {p.clientID}, "scopes": {Scopes}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.authBase+"/oauth2/device", strings.NewReader(form.Encode()))
	if err != nil {
		return platforms.AuthPrompt{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return platforms.AuthPrompt{}, err
	}
	var out struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.DeviceCode == "" {
		return platforms.AuthPrompt{}, errors.New("twitch: respuesta del código de dispositivo ilegible")
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return platforms.AuthPrompt{}, err
	}
	return platforms.AuthPrompt{
		State: hex.EncodeToString(b[:]), VerificationURI: out.VerificationURI, UserCode: out.UserCode,
		DeviceCode: out.DeviceCode, ExpiresAt: p.now().Add(time.Duration(out.ExpiresIn) * time.Second),
		Interval: time.Duration(out.Interval) * time.Second,
	}, nil
}

// PollAuth pregunta una vez si la persona ya autorizó. Twitch solo documenta
// `authorization_pending`; `slow_down` no está documentado y, si llega, se trata como
// pendiente (quien llama ya espera Interval; el spec pide doblar la espera). Cualquier
// otro 400 tras vencer el código es ErrAuthExpired.
func (p *Provider) PollAuth(ctx context.Context, prompt platforms.AuthPrompt) (store.NewAccount, error) {
	if p.now().After(prompt.ExpiresAt) {
		return store.NewAccount{}, platforms.ErrAuthExpired
	}
	form := url.Values{"client_id": {p.clientID}, "scopes": {Scopes}, "device_code": {prompt.DeviceCode}, "grant_type": {grantDevice}}
	tok, err := p.token(ctx, form)
	if err != nil {
		var he *httpError
		if errors.As(err, &he) && he.code == http.StatusBadRequest {
			switch he.msg {
			case "authorization_pending", "slow_down":
				return store.NewAccount{}, platforms.ErrAuthPending
			}
			return store.NewAccount{}, fmt.Errorf("%w: %s", platforms.ErrAuthExpired, he.msg)
		}
		return store.NewAccount{}, err
	}
	// La identidad sale de /validate: user_id, login y scopes sin llamar a Helix.
	id, err := p.Validate(ctx, tok.Access)
	if err != nil {
		return store.NewAccount{}, err
	}
	return store.NewAccount{
		Platform: store.PlatformTwitch, ExternalID: id.ExternalID, DisplayName: id.DisplayName,
		Scopes: id.Scopes, Tokens: tok,
	}, nil
}

// Refresh: cliente público, sin client_secret. El refresh token rota: el par nuevo se
// devuelve entero y quien llama lo guarda antes de usarlo.
func (p *Provider) Refresh(ctx context.Context, refresh crypto.Secret) (store.Tokens, error) {
	if !p.Configured() {
		return store.Tokens{}, platforms.ErrNoClientID
	}
	form := url.Values{"client_id": {p.clientID}, "grant_type": {"refresh_token"}, "refresh_token": {refresh.Reveal()}}
	tok, err := p.token(ctx, form)
	var he *httpError
	if errors.As(err, &he) && he.code == http.StatusBadRequest {
		// "Invalid refresh token": ya no vale (rotó, caducó a los 30 días o se revocó).
		return store.Tokens{}, platforms.ErrUnauthorized
	}
	return tok, err
}

func (p *Provider) token(ctx context.Context, form url.Values) (store.Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.authBase+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return store.Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return store.Tokens{}, err
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		return store.Tokens{}, errors.New("twitch: respuesta de token ilegible")
	}
	return store.Tokens{Access: crypto.Secret(out.AccessToken), Refresh: crypto.Secret(out.RefreshToken),
		ExpiresAt: p.now().Add(time.Duration(out.ExpiresIn) * time.Second)}, nil
}

// Validate es la comprobación horaria que Twitch exige; también da la identidad tras el
// flujo de dispositivo. La cabecera es `OAuth`, no `Bearer`: así lo pide ese endpoint.
func (p *Provider) Validate(ctx context.Context, access crypto.Secret) (platforms.Identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.authBase+"/oauth2/validate", nil)
	if err != nil {
		return platforms.Identity{}, err
	}
	req.Header.Set("Authorization", "OAuth "+access.Reveal())
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return platforms.Identity{}, err
	}
	var out struct {
		Login     string   `json:"login"`
		Scopes    []string `json:"scopes"`
		UserID    string   `json:"user_id"`
		ExpiresIn int      `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.UserID == "" {
		return platforms.Identity{}, errors.New("twitch: respuesta de validación ilegible")
	}
	return platforms.Identity{ExternalID: out.UserID, DisplayName: out.Login, Scopes: out.Scopes,
		ExpiresIn: time.Duration(out.ExpiresIn) * time.Second}, nil
}
