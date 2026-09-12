package kick

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

// ErrUseRedirect: Kick no admite el flujo de dispositivo, solo el redirect con PKCE de
// BeginRedirect/CompleteRedirect.
var ErrUseRedirect = errors.New("kick no admite el flujo de dispositivo; usa el redirect")

// redirectExpiry es cuánto dura el prompt del redirect antes de que haya que empezar de
// nuevo: 10 minutos, igual que el state que firma httpapi.
const redirectExpiry = 10 * time.Minute

// PKCE genera el par verifier/challenge S256 de PKCE (RFC 7636): 32 bytes aleatorios en
// base64url sin relleno como verifier (43 caracteres, dentro del rango 43-128 que exige
// el RFC), y el SHA-256 de esos 43 caracteres, también en base64url sin relleno, como
// challenge.
func PKCE() (verifier, challenge string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// BeginAuth y PollAuth existen solo para cumplir platforms.Provider: Kick no tiene
// flujo de dispositivo. Quien conecta una cuenta de Kick usa BeginRedirect/CompleteRedirect.
func (p *Provider) BeginAuth(_ context.Context, _ platforms.Credentials) (platforms.AuthPrompt, error) {
	return platforms.AuthPrompt{}, ErrUseRedirect
}

func (p *Provider) PollAuth(_ context.Context, _ platforms.Credentials, _ platforms.AuthPrompt) (store.NewAccount, error) {
	return store.NewAccount{}, ErrUseRedirect
}

// BeginRedirect arma la URL de autorización de Kick con PKCE. El state ya viene firmado
// por quien llama (httpapi): aquí solo se transporta. CodeVerifier y RedirectURI son
// secretos del flujo que no salen de la API: viajan en el AuthPrompt hasta
// CompleteRedirect, que los necesita para el intercambio.
func (p *Provider) BeginRedirect(_ context.Context, creds platforms.Credentials, redirectURI, state string) (platforms.AuthPrompt, error) {
	clientID := creds.ClientID.Reveal()
	if clientID == "" {
		return platforms.AuthPrompt{}, platforms.ErrNoClientID
	}
	verifier, challenge, err := PKCE()
	if err != nil {
		return platforms.AuthPrompt{}, err
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {Scopes},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	return platforms.AuthPrompt{
		State:        state,
		RedirectURL:  p.authBase + "/oauth/authorize?" + q.Encode(),
		CodeVerifier: verifier,
		RedirectURI:  redirectURI,
		ExpiresAt:    p.now().Add(redirectExpiry),
	}, nil
}

// CompleteRedirect intercambia el código por tokens (con el code_verifier y la
// redirect_uri que BeginRedirect guardó en el prompt) y arma la cuenta con la identidad
// de GET /public/v1/users.
func (p *Provider) CompleteRedirect(ctx context.Context, creds platforms.Credentials, prompt platforms.AuthPrompt, code string) (store.NewAccount, error) {
	clientID, clientSecret := creds.ClientID.Reveal(), creds.ClientSecret.Reveal()
	if clientID == "" {
		return store.NewAccount{}, platforms.ErrNoClientID
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"code_verifier": {prompt.CodeVerifier},
		"redirect_uri":  {prompt.RedirectURI},
	}
	tok, scopes, err := p.token(ctx, form)
	if err != nil {
		return store.NewAccount{}, err
	}
	id, err := p.Validate(ctx, tok.Access)
	if err != nil {
		return store.NewAccount{}, err
	}
	return store.NewAccount{
		Platform: store.PlatformKick, ExternalID: id.ExternalID, DisplayName: id.DisplayName,
		Scopes: scopes, Tokens: tok, OwnApp: true, Credentials: creds,
	}, nil
}

// Refresh: cliente confidencial, con client_secret. El refresh token rota (ventana
// deslizante de 30 días): el par nuevo se devuelve entero y quien llama lo guarda.
func (p *Provider) Refresh(ctx context.Context, _ store.Account, creds platforms.Credentials, refresh crypto.Secret) (store.Tokens, error) {
	clientID, clientSecret := creds.ClientID.Reveal(), creds.ClientSecret.Reveal()
	if clientID == "" {
		return store.Tokens{}, platforms.ErrNoClientID
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"refresh_token": {refresh.Reveal()},
	}
	tok, _, err := p.token(ctx, form)
	if err != nil {
		if errors.Is(err, platforms.ErrUnauthorized) {
			return store.Tokens{}, platforms.ErrUnauthorized
		}
		var he *httpError
		if errors.As(err, &he) && he.code == http.StatusBadRequest {
			// El refresh token ya no vale (rotó, caducó o se revocó).
			return store.Tokens{}, platforms.ErrUnauthorized
		}
		return store.Tokens{}, err
	}
	return tok, nil
}

// token hace POST /oauth/token y devuelve los tokens junto con los scopes concedidos
// (solo el intercambio inicial los usa; Refresh los descarta). El client_secret va en
// el cuerpo del form, nunca en la URL ni en un error.
func (p *Provider) token(ctx context.Context, form url.Values) (store.Tokens, []string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.authBase+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return store.Tokens{}, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return store.Tokens{}, nil, err
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		return store.Tokens{}, nil, errors.New("kick: respuesta de token ilegible")
	}
	tok := store.Tokens{Access: crypto.Secret(out.AccessToken), Refresh: crypto.Secret(out.RefreshToken),
		ExpiresAt: p.now().Add(time.Duration(out.ExpiresIn) * time.Second)}
	return tok, strings.Fields(out.Scope), nil
}

// Validate usa GET /public/v1/users: es lo único que Kick ofrece para comprobar un
// token y da la identidad (user_id, name) a la vez.
func (p *Provider) Validate(ctx context.Context, access crypto.Secret) (platforms.Identity, error) {
	req, err := p.api(ctx, http.MethodGet, "/public/v1/users", access, nil)
	if err != nil {
		return platforms.Identity{}, err
	}
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return platforms.Identity{}, err
	}
	var out struct {
		Data []struct {
			UserID int64  `json:"user_id"`
			Name   string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Data) == 0 {
		return platforms.Identity{}, errors.New("kick: respuesta de usuarios ilegible")
	}
	u := out.Data[0]
	return platforms.Identity{ExternalID: strconv.FormatInt(u.UserID, 10), DisplayName: u.Name}, nil
}
