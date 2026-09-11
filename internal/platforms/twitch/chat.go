package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// errRevocado: Twitch revocó la suscripción (la persona quitó el permiso). No se reintenta.
var errRevocado = errors.New("twitch revocó la suscripción al chat")

// keepalivePorDefecto es lo que promete EventSub si no se pide otra cosa; el welcome trae
// el valor de verdad y lo sustituye.
const keepalivePorDefecto = 10 * time.Second

// sobre es el envoltorio de todo mensaje de EventSub por WebSocket.
type sobre struct {
	Metadata struct {
		MessageType      string `json:"message_type"`
		SubscriptionType string `json:"subscription_type"`
	} `json:"metadata"`
	Payload json.RawMessage `json:"payload"`
}

type sesionPayload struct {
	Session struct {
		ID                      string `json:"id"`
		KeepaliveTimeoutSeconds int    `json:"keepalive_timeout_seconds"`
		ReconnectURL            string `json:"reconnect_url"`
	} `json:"session"`
}

type chatPayload struct {
	Event struct {
		ChatterUserID    string `json:"chatter_user_id"`
		ChatterUserLogin string `json:"chatter_user_login"`
		ChatterUserName  string `json:"chatter_user_name"`
		MessageID        string `json:"message_id"`
		Message          struct {
			Text string `json:"text"`
		} `json:"message"`
		Color  string `json:"color"`
		Badges []struct {
			SetID string `json:"set_id"`
			ID    string `json:"id"`
		} `json:"badges"`
	} `json:"event"`
}

// conexionChat es una conexión viva con lo que hay que recordar de ella: el keepalive que
// anunció su welcome y si todavía falta suscribirse (la conexión de un session_reconnect
// hereda las suscripciones de la vieja, así que no).
type conexionChat struct {
	conn      *websocket.Conn
	keepalive time.Duration
	suscribir bool
}

// ReadChat lee el chat del canal de la cuenta hasta que ctx termine. Reconecta por su
// cuenta con backoff; solo devuelve error si no puede ni empezar (sin token, sin
// client_id) o si Twitch revoca la suscripción. Nunca escribe en el socket: Twitch cierra
// con 4001 a quien lo haga.
func (p *Provider) ReadChat(ctx context.Context, acct store.Account, token platforms.TokenSource, out chan<- platforms.ChatMessage) error {
	if !p.Configured() {
		return platforms.ErrNoClientID
	}
	// Un token que no se puede conseguir ni al arrancar es un problema de la cuenta, no de
	// la red: se dice en vez de entrar al bucle de reconexión.
	if _, err := token(ctx); err != nil {
		return fmt.Errorf("chat de twitch: %w", err)
	}
	inicial := min(time.Second, p.chatBackoffMax)
	espera := inicial
	var c *conexionChat
	for {
		if c == nil {
			nueva, err := p.abrirChat(ctx, p.eventSubURL)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				p.logger.Warn("no se pudo conectar al chat de twitch; reintentando",
					"cuenta", acct.DisplayName, "err", err, "en", espera)
				if !p.dormir(ctx, espera) {
					return nil
				}
				espera = p.siguienteEspera(espera)
				continue
			}
			c = nueva
		}
		siguiente, err := p.sesionChat(ctx, acct, token, out, c)
		c = nil
		if ctx.Err() != nil {
			if siguiente != nil {
				siguiente.conn.CloseNow()
			}
			return nil
		}
		if errors.Is(err, errRevocado) {
			return err
		}
		if siguiente != nil {
			// session_reconnect: la conexión nueva ya está abierta y saludada, y la vieja
			// cerrada. Se sigue con ella sin volver a suscribirse ni esperar backoff.
			c, espera = siguiente, inicial
			continue
		}
		p.logger.Warn("el chat de twitch se cortó; reconectando", "cuenta", acct.DisplayName, "err", err, "en", espera)
		if !p.dormir(ctx, espera) {
			return nil
		}
		espera = p.siguienteEspera(espera)
	}
}

// dormir espera o se rinde si ctx termina. Falso = hay que salir.
func (p *Provider) dormir(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (p *Provider) siguienteEspera(d time.Duration) time.Duration {
	return min(d*2, p.chatBackoffMax)
}

// abrirChat conecta y deja la conexión lista para leer. No espera el welcome: de eso vive
// el bucle de sesionChat, que también tiene que suscribirse cuando llegue.
func (p *Provider) abrirChat(ctx context.Context, url string) (*conexionChat, error) {
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: p.http})
	if err != nil {
		return nil, sinQuery(err)
	}
	// Un mensaje de chat con emotes y badges no llega ni de lejos a 1 MiB; el límite evita
	// que un servidor raro nos haga reservar memoria sin tope.
	conn.SetReadLimit(1 << 20)
	return &conexionChat{conn: conn, keepalive: keepalivePorDefecto, suscribir: true}, nil
}

// sesionChat mantiene UNA conexión hasta que se corta. Devuelve la conexión siguiente si
// Twitch pidió reconectar (ya abierta y con su welcome leído cuando esto vuelve), o nil y
// el error. La conexión que recibe queda cerrada al volver, pase lo que pase.
func (p *Provider) sesionChat(ctx context.Context, acct store.Account, token platforms.TokenSource,
	out chan<- platforms.ChatMessage, c *conexionChat) (*conexionChat, error) {
	defer c.conn.CloseNow()

	for {
		// Leer con plazo es el temporizador del keepalive: cualquier mensaje lo reinicia.
		leerCtx, cancel := context.WithTimeout(ctx, c.keepalive+p.keepaliveGrace)
		_, data, err := c.conn.Read(leerCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil
			}
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				return nil, fmt.Errorf("twitch cerró el socket: %d %s (%s)", ce.Code, ce.Reason, motivoCierre(ce.Code))
			}
			if leerCtx.Err() != nil {
				return nil, fmt.Errorf("eventsub calló más de %s", c.keepalive+p.keepaliveGrace)
			}
			return nil, fmt.Errorf("leer de eventsub: %w", err)
		}
		var s sobre
		if err := json.Unmarshal(data, &s); err != nil {
			// Un mensaje ilegible no justifica tirar una conexión que va bien.
			continue
		}
		switch s.Metadata.MessageType {
		case "session_welcome":
			var sp sesionPayload
			if err := json.Unmarshal(s.Payload, &sp); err != nil || sp.Session.ID == "" {
				return nil, errors.New("welcome ilegible")
			}
			if sp.Session.KeepaliveTimeoutSeconds > 0 {
				c.keepalive = time.Duration(sp.Session.KeepaliveTimeoutSeconds) * time.Second
			}
			if c.suscribir {
				// Hay 10 s para suscribirse o Twitch cierra con 4003.
				if err := p.suscribirChat(ctx, acct, token, sp.Session.ID); err != nil {
					return nil, err
				}
				c.suscribir = false
			}
			p.logger.Info("chat de twitch conectado", "cuenta", acct.DisplayName)
		case "session_keepalive":
			// Nada: leer con plazo ya reinició el temporizador.
		case "session_reconnect":
			var sp sesionPayload
			if err := json.Unmarshal(s.Payload, &sp); err != nil || sp.Session.ReconnectURL == "" {
				return nil, errors.New("reconnect sin URL")
			}
			// Twitch da 30 s de gracia y sigue mandando por la vieja: se abre la nueva y se
			// espera SU welcome antes de soltar esta, que el defer cierra al volver. Las
			// suscripciones viajan con la sesión, así que la nueva no se vuelve a suscribir.
			nueva, err := p.seguirReconexion(ctx, sp.Session.ReconnectURL)
			if err != nil {
				return nil, fmt.Errorf("reconexión de eventsub: %w", err)
			}
			return nueva, nil
		case "revocation":
			return nil, errRevocado
		case "notification":
			if s.Metadata.SubscriptionType != "channel.chat.message" {
				continue
			}
			var cp chatPayload
			if err := json.Unmarshal(s.Payload, &cp); err != nil {
				continue
			}
			m := platforms.ChatMessage{
				Platform: platforms.Twitch, AccountID: acct.ID,
				AuthorID: cp.Event.ChatterUserID, Author: nombreOLogin(cp.Event.ChatterUserName, cp.Event.ChatterUserLogin),
				Text: cp.Event.Message.Text, Color: cp.Event.Color, MessageID: cp.Event.MessageID, At: p.now(),
			}
			for _, b := range cp.Event.Badges {
				m.Badges = append(m.Badges, b.SetID+"/"+b.ID)
			}
			select {
			case out <- m:
			case <-ctx.Done():
				return nil, nil
			default:
				// El agregador no lee: descartar antes que bloquear el socket (y que
				// Twitch lo cierre por no leer).
				p.logger.Debug("mensaje de chat descartado: el consumidor no lee")
			}
		}
	}
}

// seguirReconexion abre la conexión que pidió session_reconnect y espera su welcome: hasta
// que llega no se puede soltar la vieja sin perder mensajes. La nueva hereda las
// suscripciones, por eso vuelve con suscribir en falso.
func (p *Provider) seguirReconexion(ctx context.Context, url string) (*conexionChat, error) {
	nueva, err := p.abrirChat(ctx, url)
	if err != nil {
		return nil, err
	}
	// Twitch da 30 s antes de cerrar la vieja con 4004; si el welcome no llega mucho antes,
	// es mejor volver al bucle normal que quedarse con dos sockets mudos.
	leerCtx, cancel := context.WithTimeout(ctx, keepalivePorDefecto+p.keepaliveGrace)
	defer cancel()
	for {
		_, data, err := nueva.conn.Read(leerCtx)
		if err != nil {
			nueva.conn.CloseNow()
			return nil, err
		}
		var s sobre
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		if s.Metadata.MessageType != "session_welcome" {
			continue
		}
		var sp sesionPayload
		if err := json.Unmarshal(s.Payload, &sp); err != nil || sp.Session.ID == "" {
			nueva.conn.CloseNow()
			return nil, errors.New("welcome ilegible")
		}
		if sp.Session.KeepaliveTimeoutSeconds > 0 {
			nueva.keepalive = time.Duration(sp.Session.KeepaliveTimeoutSeconds) * time.Second
		}
		nueva.suscribir = false
		return nueva, nil
	}
}

func (p *Provider) suscribirChat(ctx context.Context, acct store.Account, token platforms.TokenSource, sessionID string) error {
	// El token se pide aquí y no al principio: si se refrescó a mitad de sesión, la
	// suscripción de la reconexión usa el nuevo.
	tok, err := token(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"type": "channel.chat.message", "version": "1",
		"condition": map[string]string{"broadcaster_user_id": acct.ExternalID, "user_id": acct.ExternalID},
		"transport": map[string]string{"method": "websocket", "session_id": sessionID},
	})
	req, err := p.helix(ctx, http.MethodPost, "/helix/eventsub/subscriptions", tok, bytes.NewReader(body))
	if err != nil {
		return err
	}
	_, err = p.do(req, http.StatusAccepted)
	return err
}

// nombreOLogin: Twitch manda los dos; el de pantalla es el que la gente reconoce, pero en
// cuentas con nombre no latino puede venir vacío.
func nombreOLogin(nombre, login string) string {
	if strings.TrimSpace(nombre) != "" {
		return nombre
	}
	return login
}

// motivoCierre traduce los códigos de EventSub, que no son los estándar.
func motivoCierre(code websocket.StatusCode) string {
	switch code {
	case 4000:
		return "error interno de twitch"
	case 4001:
		return "el cliente envió tráfico"
	case 4002:
		return "fallo de ping/pong"
	case 4003:
		return "sin suscripción en 10 s"
	case 4004:
		return "no se reconectó a tiempo"
	case 4005:
		return "timeout de red"
	case 4006:
		return "error de red"
	case 4007:
		return "URL de reconexión inválida"
	}
	return "cierre no documentado"
}
