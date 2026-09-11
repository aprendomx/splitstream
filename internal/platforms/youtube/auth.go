package youtube

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

const grantDevice = "urn:ietf:params:oauth:grant-type:device_code"

// BeginAuth pide un código de dispositivo. A diferencia de Twitch, YouTube es un cliente
// confidencial: exige client_id y client_secret propios (Configured() no sirve aquí,
// siempre es true, así que la comprobación es sobre las credenciales que llegan).
func (p *Provider) BeginAuth(ctx context.Context, creds platforms.Credentials) (platforms.AuthPrompt, error) {
	clientID := creds.ClientID.Reveal()
	if clientID == "" {
		return platforms.AuthPrompt{}, platforms.ErrNoClientID
	}
	form := url.Values{"client_id": {clientID}, "scope": {Scope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.oauthBase+"/device/code", strings.NewReader(form.Encode()))
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
		VerificationURL string `json:"verification_url"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.DeviceCode == "" {
		return platforms.AuthPrompt{}, errors.New("youtube: respuesta del código de dispositivo ilegible")
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return platforms.AuthPrompt{}, err
	}
	return platforms.AuthPrompt{
		State: hex.EncodeToString(b[:]), VerificationURI: out.VerificationURL, UserCode: out.UserCode,
		DeviceCode: out.DeviceCode, ExpiresAt: p.now().Add(time.Duration(out.ExpiresIn) * time.Second),
		Interval: time.Duration(out.Interval) * time.Second,
	}, nil
}

// PollAuth pregunta una vez si la persona ya autorizó. Google documenta
// authorization_pending y slow_down (ambos, esperar: quien llama es quien dobla el
// intervalo para slow_down, esta función no lo hace porque no puede mutar el prompt de
// quien llama), access_denied (canceló) y expired_token/invalid_grant (el código venció).
func (p *Provider) PollAuth(ctx context.Context, creds platforms.Credentials, prompt platforms.AuthPrompt) (store.NewAccount, error) {
	clientID, clientSecret := creds.ClientID.Reveal(), creds.ClientSecret.Reveal()
	if clientID == "" {
		return store.NewAccount{}, platforms.ErrNoClientID
	}
	if p.now().After(prompt.ExpiresAt) {
		return store.NewAccount{}, platforms.ErrAuthExpired
	}
	form := url.Values{"client_id": {clientID}, "client_secret": {clientSecret},
		"device_code": {prompt.DeviceCode}, "grant_type": {grantDevice}}
	tok, err := p.token(ctx, form)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			switch ae.reason {
			case "authorization_pending", "slow_down":
				return store.NewAccount{}, platforms.ErrAuthPending
			case "access_denied":
				return store.NewAccount{}, errors.New("la persona rechazó la autorización")
			case "expired_token", "invalid_grant":
				return store.NewAccount{}, platforms.ErrAuthExpired
			}
		}
		return store.NewAccount{}, err
	}
	// La identidad sale de channels.list (1 unidad de cuota). Ruling: aquí todavía no hay
	// cuenta (ni id), así que esta unidad NO se contabiliza en ningún sumidero — ver
	// gastar() en client.go. El manager solo puede sumar cuota una vez que la cuenta
	// existe, con las llamadas que vengan después de esta.
	req, err := p.api(ctx, http.MethodGet, "/channels?part=snippet&mine=true", tok.Access, nil)
	if err != nil {
		return store.NewAccount{}, err
	}
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return store.NewAccount{}, err
	}
	var out struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title string `json:"title"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Items) == 0 {
		return store.NewAccount{}, errors.New("youtube: no se encontró un canal para esta cuenta")
	}
	return store.NewAccount{
		Platform: store.PlatformYouTube, ExternalID: out.Items[0].ID, DisplayName: out.Items[0].Snippet.Title,
		Scopes: []string{Scope}, Tokens: tok, OwnApp: true, Credentials: creds,
	}, nil
}

// Refresh: cliente confidencial, con client_secret. Google no rota el refresh token: si
// la respuesta no trae uno (el caso normal), se conserva el que ya se tenía.
func (p *Provider) Refresh(ctx context.Context, _ store.Account, creds platforms.Credentials, refresh crypto.Secret) (store.Tokens, error) {
	clientID, clientSecret := creds.ClientID.Reveal(), creds.ClientSecret.Reveal()
	if clientID == "" {
		return store.Tokens{}, platforms.ErrNoClientID
	}
	form := url.Values{"client_id": {clientID}, "client_secret": {clientSecret},
		"refresh_token": {refresh.Reveal()}, "grant_type": {"refresh_token"}}
	tok, err := p.token(ctx, form)
	if err != nil {
		if errors.Is(err, platforms.ErrUnauthorized) {
			return store.Tokens{}, platforms.ErrUnauthorized
		}
		var ae *apiError
		if errors.As(err, &ae) && ae.code == http.StatusBadRequest {
			// "invalid_grant": el refresh token ya no vale (revocado o nunca existió).
			return store.Tokens{}, platforms.ErrUnauthorized
		}
		return store.Tokens{}, err
	}
	if tok.Refresh == "" {
		tok.Refresh = refresh
	}
	return tok, nil
}

func (p *Provider) token(ctx context.Context, form url.Values) (store.Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.oauthBase+"/token", strings.NewReader(form.Encode()))
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
		return store.Tokens{}, errors.New("youtube: respuesta de token ilegible")
	}
	return store.Tokens{Access: crypto.Secret(out.AccessToken), Refresh: crypto.Secret(out.RefreshToken),
		ExpiresAt: p.now().Add(time.Duration(out.ExpiresIn) * time.Second)}, nil
}

// Validate es tokeninfo: el único endpoint donde el token va en la query, porque así lo
// define Google (no hay forma de mandarlo por cabecera ahí). No da nombre: la identidad
// del flujo de dispositivo sale de channels.list en PollAuth, no de aquí.
func (p *Provider) Validate(ctx context.Context, access crypto.Secret) (platforms.Identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.oauthBase+"/tokeninfo?access_token="+url.QueryEscape(access.Reveal()), nil)
	if err != nil {
		return platforms.Identity{}, err
	}
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.code == http.StatusBadRequest {
			return platforms.Identity{}, platforms.ErrUnauthorized
		}
		return platforms.Identity{}, err
	}
	var out struct {
		ExpiresIn string `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return platforms.Identity{}, errors.New("youtube: respuesta de tokeninfo ilegible")
	}
	secs, err := strconv.Atoi(out.ExpiresIn)
	if err != nil {
		return platforms.Identity{}, errors.New("youtube: respuesta de tokeninfo ilegible")
	}
	return platforms.Identity{ExpiresIn: time.Duration(secs) * time.Second}, nil
}
