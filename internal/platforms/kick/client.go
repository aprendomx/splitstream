// Package kick es el proveedor de Kick: OAuth 2.1 con redirect y PKCE (Kick no tiene
// flujo de dispositivo), API pública para identidad, título, categoría y clave de
// ingesta. Los hosts son campos para que TODOS los tests vayan contra httptest. El
// webhook del chat (ChatWebhook) es la Task 7, en otro archivo de este mismo paquete.
package kick

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
)

const (
	defaultAuthBase = "https://id.kick.com"
	defaultAPIBase  = "https://api.kick.com"
)

// Scopes es todo lo que se pide: identidad, leer y escribir el canal (título y
// categoría), la clave de ingesta y suscribirse a eventos (el chat llega por webhook,
// Task 7). No incluye chat:read: ese scope no existe en Kick.
const Scopes = "user:read channel:read channel:write streamkey:read events:subscribe"

type Options struct {
	HTTPClient *http.Client
	AuthBase   string
	APIBase    string
	Now        func() time.Time
	Logger     *slog.Logger
}

type Provider struct {
	http     *http.Client
	authBase string
	apiBase  string
	now      func() time.Time
	logger   *slog.Logger
}

// New construye el proveedor. Los tests SIEMPRE deben inyectar AuthBase y APIBase (los
// de httptest): a diferencia de Twitch, aquí no hay client_id incluido, las credenciales
// van por cuenta (RequiresOwnApp) y Configured() es fijo.
func New(o Options) *Provider {
	p := &Provider{http: o.HTTPClient, authBase: o.AuthBase, apiBase: o.APIBase, now: o.Now, logger: o.Logger}
	if p.http == nil {
		p.http = &http.Client{Timeout: 15 * time.Second}
	}
	if p.authBase == "" {
		p.authBase = defaultAuthBase
	}
	if p.apiBase == "" {
		p.apiBase = defaultAPIBase
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	return p
}

func (p *Provider) ID() platforms.ID { return platforms.Kick }

func (p *Provider) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{
		Title: true, Category: true, ChatRead: true, IngestKey: true,
		RequiresOwnApp: true, RequiresPublicURL: true,
	}
}

// Configured es siempre true: a diferencia de Twitch, Kick no trae una app incluida;
// cada cuenta pone la suya (RequiresOwnApp). BeginRedirect es quien exige las
// credenciales, con ErrNoClientID si faltan.
func (p *Provider) Configured() bool { return true }

// apiError es el sobre de error de Kick: {"message": "..."}. Nunca lleva tokens ni la
// clave de stream.
type apiError struct {
	Message string `json:"message"`
}

// do hace una petición y devuelve el cuerpo. Traduce 401 → ErrUnauthorized, 429 →
// ErrRateLimited y cualquier otro código no esperado a un error con el mensaje de Kick.
// El token va en la cabecera y no aparece en ningún error.
func (p *Provider) do(req *http.Request, esperado int) ([]byte, error) {
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kick: %w", sinQuery(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == esperado:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, platforms.ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: hasta %s", platforms.ErrRateLimited, resp.Header.Get("Retry-After"))
	}
	var ae apiError
	if json.Unmarshal(body, &ae) == nil && ae.Message != "" {
		return body, &httpError{code: resp.StatusCode, msg: ae.Message}
	}
	return body, &httpError{code: resp.StatusCode, msg: http.StatusText(resp.StatusCode)}
}

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return fmt.Sprintf("kick respondió %d: %s", e.code, e.msg) }

// sinQuery quita la query de un *url.Error: una petición de token lleva el
// client_secret o el refresh token en el cuerpo, no en la URL, pero por si acaso ningún
// error de transporte reproduce la URL entera.
func sinQuery(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if u, e := url.Parse(ue.URL); e == nil {
			u.RawQuery = ""
			return fmt.Errorf("%s %s: %w", ue.Op, u.String(), ue.Err)
		}
	}
	return err
}

// api arma una petición contra la API pública de Kick con el token en la cabecera
// Authorization.
func (p *Provider) api(ctx context.Context, method, path string, token crypto.Secret, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.apiBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token.Reveal())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// Aserciones de interfaz: si algún método deja de calzar, esto no compila.
var (
	_ platforms.Provider          = (*Provider)(nil)
	_ platforms.RedirectAuth      = (*Provider)(nil)
	_ platforms.TitleSetter       = (*Provider)(nil)
	_ platforms.CategorySetter    = (*Provider)(nil)
	_ platforms.IngestKeyProvider = (*Provider)(nil)
)
