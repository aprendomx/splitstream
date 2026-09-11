// Package twitch es el proveedor de Twitch: Device Code Grant con credenciales incluidas
// (cliente público, sin secret), Helix para título y categoría, y EventSub por WebSocket
// para el chat. Los hosts son campos para que TODOS los tests vayan contra httptest.
package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
)

// ClientID es el de la app de Splitstream registrada en dev.twitch.tv. Es público por
// diseño (cliente público: no hay secret que proteger); quien quiera su propia app pone
// SPLITSTREAM_TWITCH_CLIENT_ID. Vacío mientras no se registre la app: el proveedor lo
// dice en vez de fallar.
var ClientID = ""

// Scopes es todo lo que se pide: título y categoría del canal propio, y leer su chat. Con
// un token de usuario del propio broadcaster no hace falta user:bot ni channel:bot.
const Scopes = "channel:manage:broadcast user:read:chat"

const (
	defaultAuthBase    = "https://id.twitch.tv"
	defaultHelixBase   = "https://api.twitch.tv"
	defaultEventSubURL = "wss://eventsub.wss.twitch.tv/ws"
	boxArtSize         = "144x192"
)

// ResolveClientID: el entorno manda; vacío o espacios, el incluido.
func ResolveClientID(env string) string {
	if v := strings.TrimSpace(env); v != "" {
		return v
	}
	return ClientID
}

type Options struct {
	ClientID    string
	HTTPClient  *http.Client
	AuthBase    string
	HelixBase   string
	EventSubURL string
	Now         func() time.Time
	Logger      *slog.Logger
}

type Provider struct {
	clientID    string
	http        *http.Client
	authBase    string
	helixBase   string
	eventSubURL string
	now         func() time.Time
	logger      *slog.Logger
}

// New construye el proveedor. Los tests SIEMPRE deben inyectar AuthBase y HelixBase (los
// de httptest): sin eso, Options.ClientID vacío hace que Configured() sea falso y nadie
// llegue a marcar una petición real, porque no hay cuentas en la base que la disparen.
func New(o Options) *Provider {
	p := &Provider{clientID: o.ClientID, http: o.HTTPClient, authBase: o.AuthBase, helixBase: o.HelixBase,
		eventSubURL: o.EventSubURL, now: o.Now, logger: o.Logger}
	if p.http == nil {
		p.http = &http.Client{Timeout: 15 * time.Second}
	}
	if p.authBase == "" {
		p.authBase = defaultAuthBase
	}
	if p.helixBase == "" {
		p.helixBase = defaultHelixBase
	}
	if p.eventSubURL == "" {
		p.eventSubURL = defaultEventSubURL
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	return p
}

func (p *Provider) ID() platforms.ID { return platforms.Twitch }
func (p *Provider) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{Title: true, Category: true, ChatRead: true}
}
func (p *Provider) Configured() bool { return p.clientID != "" }

// apiError es el {status, message} de Twitch. Nunca lleva tokens.
type apiError struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

// do hace una petición y devuelve el cuerpo. Traduce 401 → ErrUnauthorized, 429 →
// ErrRateLimited y cualquier otro código no esperado a un error con el mensaje de Twitch.
// El token va en la cabecera y no aparece en ningún error.
func (p *Provider) do(req *http.Request, esperado int) ([]byte, error) {
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitch: %w", sinQuery(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == esperado:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, platforms.ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: hasta %s", platforms.ErrRateLimited, resp.Header.Get("Ratelimit-Reset"))
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

func (e *httpError) Error() string { return fmt.Sprintf("twitch respondió %d: %s", e.code, e.msg) }

// sinQuery quita la query de un *url.Error: una petición de token lleva el device_code o
// el refresh token en el cuerpo, no en la URL, pero por si acaso ningún error de
// transporte reproduce la URL entera.
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

func (p *Provider) helix(ctx context.Context, method, path string, token crypto.Secret, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.helixBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token.Reveal())
	req.Header.Set("Client-Id", p.clientID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}
