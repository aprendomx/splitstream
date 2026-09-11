// Package youtube es el proveedor de YouTube: flujo de dispositivo con credenciales
// propias (cliente confidencial: cada persona trae su client_id/client_secret
// registrados en Google Cloud Console), identidad por channels.list, refresco y
// validación, y el contador de cuota por llamada. Los hosts son campos para que TODOS
// los tests vayan contra httptest.
package youtube

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
	"github.com/aprendomx/splitstream/internal/store"
)

// Scope es lo único que se pide en el flujo de dispositivo: youtube.force-ssl no está
// permitido ahí, y youtube basta para leer, crear y actualizar emisiones (spec §5).
const Scope = "https://www.googleapis.com/auth/youtube"

const (
	defaultOAuthBase = "https://oauth2.googleapis.com"
	defaultAPIBase   = "https://www.googleapis.com/youtube/v3"
)

// costos son las unidades de cuota por llamada de la Data API (spec §5/§6). Las cifras de
// los métodos live* son una estimación conservadora: la tabla pública de Google no los
// desglosa método por método, solo dice "escritura = 50 unidades" en general.
var costos = map[string]int{
	"list":       1,
	"insert":     50,
	"update":     50,
	"bind":       50,
	"transition": 50,
	"chat_list":  5,
}

type Options struct {
	HTTPClient *http.Client
	OAuthBase  string
	APIBase    string
	Now        func() time.Time
	Logger     *slog.Logger
	// Quota entrega el sumidero de cuota de una cuenta concreta. Puede quedar nil (por
	// ejemplo en los tests de este paquete que no ejercitan gastar()): entonces gastar no
	// hace nada, nunca revienta.
	Quota func(accountID int64) platforms.QuotaSink
}

type Provider struct {
	http      *http.Client
	oauthBase string
	apiBase   string
	now       func() time.Time
	logger    *slog.Logger
	quota     func(accountID int64) platforms.QuotaSink
}

// New construye el proveedor. Los tests SIEMPRE deben inyectar OAuthBase y APIBase (los
// de httptest): a diferencia de Twitch, aquí no hay client_id incluido que filtre nada,
// las credenciales van por cuenta (RequiresOwnApp) y Configured() es fijo.
func New(o Options) *Provider {
	p := &Provider{http: o.HTTPClient, oauthBase: o.OAuthBase, apiBase: o.APIBase,
		now: o.Now, logger: o.Logger, quota: o.Quota}
	if p.http == nil {
		p.http = &http.Client{Timeout: 15 * time.Second}
	}
	if p.oauthBase == "" {
		p.oauthBase = defaultOAuthBase
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

func (p *Provider) ID() platforms.ID { return platforms.YouTube }

func (p *Provider) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{Title: true, Schedule: true, IngestKey: true, ChatRead: true, RequiresOwnApp: true}
}

// Configured es siempre true: YouTube no trae una app incluida como Twitch, cada cuenta
// pone la suya. BeginAuth es quien exige las credenciales, con ErrNoClientID si faltan.
func (p *Provider) Configured() bool { return true }

// apiError es el sobre de error de Google, ya traducido a sus tres campos, sin importar
// cuál de las dos formas mandó (ver parseAPIError). Nunca lleva el token.
type apiError struct {
	code   int
	reason string
	msg    string
}

func (e *apiError) Error() string {
	switch {
	case e.reason != "" && e.msg != "":
		return fmt.Sprintf("youtube respondió %d: %s (%s)", e.code, e.msg, e.reason)
	case e.reason != "":
		return fmt.Sprintf("youtube respondió %d: %s", e.code, e.reason)
	default:
		return fmt.Sprintf("youtube respondió %d: %s", e.code, e.msg)
	}
}

// parseAPIError entiende las dos formas del sobre de error de Google: la Data API lo
// manda como objeto, {"error":{"code","message","errors":[{"reason"}]}}; el endpoint de
// tokens lo manda plano, {"error":"slow_down","error_description":"…"} — ahí "error" ES
// el motivo (una cadena), no un objeto. Se intenta primero la forma plana: si "error" no
// es una cadena, json.Unmarshal falla por el choque de tipos y se cae a la de objeto.
func parseAPIError(status int, body []byte) *apiError {
	var plano struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(body, &plano) == nil && plano.Error != "" {
		return &apiError{code: status, reason: plano.Error, msg: plano.Description}
	}
	var sobre struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &sobre) == nil && sobre.Error.Message != "" {
		reason := ""
		if len(sobre.Error.Errors) > 0 {
			reason = sobre.Error.Errors[0].Reason
		}
		return &apiError{code: status, reason: reason, msg: sobre.Error.Message}
	}
	return &apiError{code: status, msg: http.StatusText(status)}
}

// do hace la petición y devuelve el cuerpo si llega el código esperado. 401 → siempre
// ErrUnauthorized; 403 con reason quotaExceeded → ErrRateLimited legible (y la propia
// palabra "quotaExceeded" se conserva en el mensaje: es el motivo, no el token); 403 con
// reason liveStreamingNotEnabled → mensaje que explica cómo arreglarlo; cualquier otro
// código, el *apiError con su reason. El token va en la cabecera Authorization y do()
// nunca la toca, así que jamás aparece en un error.
func (p *Provider) do(req *http.Request, esperado int) ([]byte, error) {
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube: %w", sinQuery(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == esperado {
		return body, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, platforms.ErrUnauthorized
	}
	ae := parseAPIError(resp.StatusCode, body)
	if resp.StatusCode == http.StatusForbidden {
		switch ae.reason {
		case "quotaExceeded":
			return nil, fmt.Errorf("%w: cuota de YouTube agotada (quotaExceeded)", platforms.ErrRateLimited)
		case "liveStreamingNotEnabled":
			return nil, errors.New("este canal no tiene habilitadas las emisiones en directo (youtube.com/features)")
		}
	}
	return nil, ae
}

// sinQuery quita la query de un *url.Error. tokeninfo lleva el access_token en la query
// (así lo define Google, solo en ese endpoint); para cualquier otro error de transporte
// nunca se reproduce la URL entera tampoco, por si acaso algún día lleva algo sensible.
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

// api arma una petición contra la Data API con el token en la cabecera Authorization
// (nunca en la query, a diferencia de tokeninfo).
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

// gastar suma el costo de metodo a la cuota de la cuenta, si hay un sumidero inyectado.
// Ruling: nunca se llama durante PollAuth. Ahí channels.list identifica a quien se está
// conectando pero todavía no existe la cuenta (no tiene id), así que esa unidad no se
// contabiliza en ningún lado; el resto de llamadas de este proveedor sí tienen una cuenta
// real y gastan a través de este método.
func (p *Provider) gastar(acct store.Account, metodo string) {
	if p.quota == nil {
		return
	}
	p.quota(acct.ID)(costos[metodo])
}
