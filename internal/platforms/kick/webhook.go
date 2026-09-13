package kick

// El chat de Kick no se lee: llega. Kick firma cada webhook con RSA-SHA256 PKCS#1 v1.5
// sobre "message_id.timestamp.cuerpo" y publica la clave pública en un endpoint abierto.
// Aquí se verifica esa firma (con la clave cacheada), se rechaza lo viejo y lo repetido,
// y se traduce el evento a platforms.ChatMessage. Nada de esto toca la red en los tests:
// APIBase es un campo.

import (
	"bytes"
	"context"
	stdcrypto "crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// Cabeceras que Kick manda en cada webhook.
const (
	hdrID    = "Kick-Event-Message-Id"
	hdrTS    = "Kick-Event-Message-Timestamp"
	hdrTipo  = "Kick-Event-Type"
	hdrVer   = "Kick-Event-Version"
	hdrFirma = "Kick-Event-Signature"
)

const (
	// eventoChat y versionChat son lo único que este proveedor procesa; cualquier otro
	// evento se ignora sin rechazarlo (el handler responde 200 igual).
	eventoChat  = "chat.message.sent"
	versionChat = "1"
	// maxCuerpo: quien llama ya acota la lectura del cuerpo (el handler HTTP), pero
	// ParseWebhook lo vuelve a comprobar para que no dependa de que lo hagan.
	maxCuerpo = 256 << 10
	// rutaClave es el endpoint público de la clave: no lleva Authorization.
	rutaClave = "/public/v1/public-key"
	// rutaSubs es el alta, la lista y la baja de suscripciones a eventos.
	rutaSubs = "/public/v1/events/subscriptions"
)

const (
	defaultPublicKeyTTL = 24 * time.Hour
	defaultReplayWindow = 5 * time.Minute
	defaultSeenTTL      = 10 * time.Minute
	// edadMinRefresco: ante un fallo de firma la clave solo se vuelve a pedir si la
	// cacheada ya tiene más de un minuto. Sin esto, una ráfaga de firmas inválidas
	// (que cualquiera puede mandar: el endpoint es público) sería una petición a Kick
	// por webhook.
	edadMinRefresco = time.Minute
	// timeoutClave acota la petición de la clave: ParseWebhook no recibe contexto,
	// porque el handler ya respondió 200 antes de llamarla.
	timeoutClave = 10 * time.Second
)

// webhookState es el estado que el webhook del chat guarda entre llamadas: la clave
// pública de Kick cacheada y los message_id ya procesados. Vive dentro del Provider, que
// siempre se usa por puntero (por el mutex, nunca se copia).
type webhookState struct {
	// mu protege la clave cacheada y se mantiene tomado durante la petición a Kick: así
	// dos webhooks simultáneos en frío piden la clave una sola vez.
	mu        sync.Mutex
	pub       *rsa.PublicKey
	fetchedAt time.Time

	// seenMu protege seen, aparte de mu: marcar un mensaje no debe esperar a que
	// termine una petición de clave.
	seenMu sync.Mutex
	seen   map[string]time.Time

	claveTTL time.Duration
	ventana  time.Duration
	seenTTL  time.Duration
}

func (w *webhookState) init(o Options) {
	w.seen = make(map[string]time.Time)
	w.claveTTL, w.ventana, w.seenTTL = o.PublicKeyTTL, o.ReplayWindow, o.SeenTTL
	if w.claveTTL <= 0 {
		w.claveTTL = defaultPublicKeyTTL
	}
	if w.ventana <= 0 {
		w.ventana = defaultReplayWindow
	}
	if w.seenTTL <= 0 {
		w.seenTTL = defaultSeenTTL
	}
	// La ventana de repetición se mira en los dos sentidos, así que un mismo message_id
	// puede volver a entrar durante 2*ventana (desde `ahora-ventana` hasta
	// `ahora+ventana`). Con un SeenTTL más corto, el id se purgaría estando todavía dentro
	// de la ventana y el mensaje se aceptaría dos veces: el tope de abajo lo impide.
	if w.seenTTL < 2*w.ventana {
		w.seenTTL = 2 * w.ventana
	}
}

// mensajeDeKick saca el "message" del sobre de Kick, o "" si el cuerpo no lo trae (vacío,
// no es JSON, o es un objeto sin ese campo).
func mensajeDeKick(body []byte) string {
	var ae apiError
	if json.Unmarshal(body, &ae) != nil {
		return ""
	}
	return ae.Message
}

// rechazo arma el error de un webhook rechazado. Solo lleva el motivo: ni el cuerpo, ni
// la firma, ni la clave aparecen nunca en un error, porque quien los registra es un
// endpoint público al que puede llamar cualquiera.
func rechazo(motivo string) error {
	return fmt.Errorf("%w: %s", platforms.ErrWebhookRejected, motivo)
}

// ParseWebhook verifica un webhook de Kick y devuelve los mensajes de chat que trae.
// Un evento que no sea chat.message.sent v1 devuelve (nil, nil): no es un error, solo no
// interesa. AccountID queda en cero a propósito: quien llama resuelve la cuenta por
// BroadcasterID, que es lo único que Kick dice del canal.
//
// El orden de las comprobaciones importa: lo barato primero, y el tipo antes que la
// firma, para no gastar un RSA por cada evento que ni siquiera vamos a mirar.
func (p *Provider) ParseWebhook(hdr http.Header, body []byte) ([]platforms.ChatMessage, error) {
	if len(body) > maxCuerpo {
		return nil, rechazo("tamaño")
	}
	id, ts := hdr.Get(hdrID), hdr.Get(hdrTS)
	firma, tipo := hdr.Get(hdrFirma), hdr.Get(hdrTipo)
	if id == "" || ts == "" || firma == "" || tipo == "" {
		return nil, rechazo("cabeceras")
	}
	if tipo != eventoChat || hdr.Get(hdrVer) != versionChat {
		return nil, nil
	}
	// Una marca que ni siquiera es RFC 3339 no es un webhook viejo: es una cabecera mal
	// formada, y el motivo tiene que decir eso para que el registro del panel no haga
	// pensar en un reloj desajustado.
	momento, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return nil, rechazo("cabeceras")
	}
	// El desfase se mira en los dos sentidos: un webhook del futuro es tan sospechoso
	// como uno de hace media hora.
	if d := p.now().Sub(momento); d > p.wh.ventana || d < -p.wh.ventana {
		return nil, rechazo("antigüedad")
	}
	if err := p.verificar(id, ts, firma, body); err != nil {
		return nil, err
	}
	// La marca se pone DESPUÉS de verificar la firma: si se pusiera antes, cualquiera
	// podría quemar el id de un mensaje legítimo mandando basura con ese id.
	if !p.marcar(id) {
		return nil, rechazo("repetido")
	}
	msg, err := mensajeDesdeEvento(body, momento)
	if err != nil {
		return nil, err
	}
	return []platforms.ChatMessage{msg}, nil
}

// verificar comprueba la firma con la clave cacheada y, si falla, pide un refresco UNA
// vez antes de darla por mala. Quien decide si ese refresco se convierte en una petición
// a Kick es clavePublica, bajo su candado: la edad mínima no se puede mirar desde aquí
// porque entre mirarla y pedir la clave caben N goroutines más.
func (p *Provider) verificar(id, ts, firma string, body []byte) error {
	sig, err := base64.StdEncoding.DecodeString(firma)
	if err != nil {
		return rechazo("firma")
	}
	sum := sha256.Sum256([]byte(id + "." + ts + "." + string(body)))
	pub, err := p.clavePublica(false)
	if err != nil {
		// Sin clave no se puede verificar nada: se rechaza igual, pero el motivo del
		// fallo queda en el registro (no en el error, que va a un endpoint público).
		p.logger.Warn("kick: no se pudo obtener la clave pública del webhook", "err", err)
		return rechazo("firma")
	}
	if rsa.VerifyPKCS1v15(pub, stdcrypto.SHA256, sum[:], sig) == nil {
		return nil
	}
	refrescada, err := p.clavePublica(true)
	if err != nil {
		p.logger.Warn("kick: no se pudo refrescar la clave pública del webhook", "err", err)
		return rechazo("firma")
	}
	// Si clavePublica devolvió la misma copia (recién refrescada por otra goroutine, o
	// demasiado joven para volver a pedirla) no hay nada nuevo que probar.
	if refrescada == pub {
		return rechazo("firma")
	}
	if rsa.VerifyPKCS1v15(refrescada, stdcrypto.SHA256, sum[:], sig) != nil {
		return rechazo("firma")
	}
	p.logger.Info("kick: clave pública del webhook refrescada tras un fallo de firma")
	return nil
}

// marcar registra un message_id y dice si es nuevo. De paso purga lo que ya pasó de
// SeenTTL, que es lo que mantiene el mapa acotado sin ninguna goroutine de limpieza.
func (p *Provider) marcar(id string) bool {
	ahora := p.now()
	p.wh.seenMu.Lock()
	defer p.wh.seenMu.Unlock()
	for k, t := range p.wh.seen {
		if ahora.Sub(t) > p.wh.seenTTL {
			delete(p.wh.seen, k)
		}
	}
	if _, ya := p.wh.seen[id]; ya {
		return false
	}
	p.wh.seen[id] = ahora
	return true
}

// clavePublica devuelve la clave pública de Kick. Pide una nueva si no hay, si venció
// PublicKeyTTL o si forzar es true y la cacheada ya tiene más de edadMinRefresco. Un fallo
// de red con una clave ya cacheada no la tira: se sigue con la que había.
//
// Las dos condiciones se miran DENTRO del candado a propósito. El endpoint del webhook es
// público y quien quiera puede mandar firmas inválidas a placer: con la guardia fuera,
// N entregas simultáneas veían la clave envejecida a la vez y entraban las N a pedirla,
// serializadas con el candado tomado. Aquí, la primera la pide y las demás se encuentran
// con una copia recién traída y no piden nada.
func (p *Provider) clavePublica(forzar bool) (*rsa.PublicKey, error) {
	w := &p.wh
	w.mu.Lock()
	defer w.mu.Unlock()
	ahora := p.now()
	if w.pub != nil {
		edad := ahora.Sub(w.fetchedAt)
		if (!forzar && edad < w.claveTTL) || (forzar && edad < edadMinRefresco) {
			return w.pub, nil
		}
	}
	pub, err := p.pedirClave()
	if err != nil {
		if w.pub != nil {
			return w.pub, nil
		}
		return nil, err
	}
	w.pub, w.fetchedAt = pub, ahora
	return pub, nil
}

// pedirClave hace GET /public/v1/public-key. Es el único endpoint de Kick que no lleva
// Authorization, por eso arma la petición a mano en vez de usar api().
func (p *Provider) pedirClave() (*rsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeoutClave)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+rutaClave, nil)
	if err != nil {
		return nil, err
	}
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			PublicKey string `json:"public_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Data.PublicKey == "" {
		return nil, errors.New("kick: respuesta de clave pública ilegible")
	}
	blk, _ := pem.Decode([]byte(out.Data.PublicKey))
	if blk == nil {
		return nil, errors.New("kick: la clave pública no viene en PEM")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		// El error de x509 no se propaga: llevaría bytes de la clave al registro.
		return nil, errors.New("kick: la clave pública no es PKIX")
	}
	rsaPub, ok := k.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("kick: la clave pública no es RSA")
	}
	return rsaPub, nil
}

// eventoChatMensaje es el payload de chat.message.sent v1. Solo se declara lo que se usa.
type eventoChatMensaje struct {
	MessageID   string `json:"message_id"`
	Broadcaster struct {
		UserID int64 `json:"user_id"`
	} `json:"broadcaster"`
	Sender struct {
		UserID   int64  `json:"user_id"`
		Username string `json:"username"`
		Identity *struct {
			UsernameColor string `json:"username_color"`
			Badges        []struct {
				Type string `json:"type"`
			} `json:"badges"`
		} `json:"identity"`
	} `json:"sender"`
	Content string `json:"content"`
	// CreatedAt se lee como texto y se parsea a mano: si Kick cambiara el formato, un
	// mensaje válido no debería perderse por eso (se usa la marca de la cabecera, que
	// va firmada).
	CreatedAt string `json:"created_at"`
}

// mensajeDesdeEvento traduce el payload ya verificado a un ChatMessage. Los emotes se
// dejan como los manda Kick ("[emote:id:nombre]"): el panel los enseña como texto.
func mensajeDesdeEvento(body []byte, respaldo time.Time) (platforms.ChatMessage, error) {
	var ev eventoChatMensaje
	if err := json.Unmarshal(body, &ev); err != nil {
		return platforms.ChatMessage{}, rechazo("json")
	}
	at := respaldo
	if t, err := time.Parse(time.RFC3339, ev.CreatedAt); err == nil {
		at = t
	}
	msg := platforms.ChatMessage{
		Platform:      platforms.Kick,
		AuthorID:      strconv.FormatInt(ev.Sender.UserID, 10),
		Author:        ev.Sender.Username,
		Text:          ev.Content,
		MessageID:     ev.MessageID,
		At:            at,
		BroadcasterID: strconv.FormatInt(ev.Broadcaster.UserID, 10),
	}
	if ev.Sender.Identity != nil {
		msg.Color = ev.Sender.Identity.UsernameColor
		for _, b := range ev.Sender.Identity.Badges {
			if b.Type != "" {
				msg.Badges = append(msg.Badges, b.Type)
			}
		}
	}
	return msg, nil
}

// SubscribeChat da de alta chat.message.sent v1 para el canal de la cuenta. La URL de
// destino no viaja en la petición: Kick la toma de la app del portal, así que webhookURL
// solo se registra para que quede claro a dónde va a llamar.
func (p *Provider) SubscribeChat(ctx context.Context, acct store.Account, token crypto.Secret, webhookURL string) error {
	canal, err := strconv.ParseInt(acct.ExternalID, 10, 64)
	if err != nil {
		return fmt.Errorf("kick: la cuenta no tiene un id de canal numérico: %q", acct.ExternalID)
	}
	cuerpo, err := json.Marshal(map[string]any{
		"events":              []map[string]any{{"name": eventoChat, "version": 1}},
		"method":              "webhook",
		"broadcaster_user_id": canal,
	})
	if err != nil {
		return err
	}
	req, err := p.api(ctx, http.MethodPost, rutaSubs, token, bytes.NewReader(cuerpo))
	if err != nil {
		return err
	}
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return err
	}
	var out struct {
		Data []struct {
			Name           string `json:"name"`
			SubscriptionID string `json:"subscription_id"`
			Error          string `json:"error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Data) == 0 {
		return errors.New("kick: respuesta de suscripción ilegible")
	}
	for _, s := range out.Data {
		if s.Error != "" {
			return fmt.Errorf("kick no suscribió %s: %s", s.Name, s.Error)
		}
		p.logger.Info("chat de Kick suscrito", "canal", acct.ExternalID,
			"evento", s.Name, "suscripcion", s.SubscriptionID, "webhook", webhookURL)
	}
	return nil
}

// UnsubscribeChat lista las suscripciones de la app y borra las de chat.message.sent de
// este canal. La app es del usuario y puede tener varias cuentas conectadas: por eso se
// filtra por broadcaster_user_id cuando Kick lo trae. Que ya no exista (404) es el
// resultado que se buscaba, no un error.
func (p *Provider) UnsubscribeChat(ctx context.Context, acct store.Account, token crypto.Secret) error {
	req, err := p.api(ctx, http.MethodGet, rutaSubs, token, nil)
	if err != nil {
		return err
	}
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return err
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
			// Kick llama al evento "event" al listar y "name" al suscribir: se aceptan
			// los dos para no depender de cuál mande hoy.
			Event       string `json:"event"`
			Name        string `json:"name"`
			Broadcaster int64  `json:"broadcaster_user_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return errors.New("kick: respuesta de suscripciones ilegible")
	}
	var ids []string
	for _, s := range out.Data {
		evento := s.Event
		if evento == "" {
			evento = s.Name
		}
		if evento != eventoChat || s.ID == "" {
			continue
		}
		// Sin broadcaster_user_id en la lista no se puede distinguir de quién es: se
		// borran todas las del chat, que es lo que esta app suscribe.
		if s.Broadcaster != 0 && strconv.FormatInt(s.Broadcaster, 10) != acct.ExternalID {
			continue
		}
		ids = append(ids, s.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	q := url.Values{"id": ids}
	req, err = p.api(ctx, http.MethodDelete, rutaSubs+"?"+q.Encode(), token, nil)
	if err != nil {
		return err
	}
	// Kick documenta 204, pero devuelve 200 a veces. Un 200 con el cuerpo vacío es la baja
	// hecha; uno que trae {"message": "..."} es Kick contando por qué NO la hizo, y eso es
	// un error aunque el código sea de éxito. Un 404 sí es el resultado que se buscaba: la
	// suscripción ya no existe.
	if cuerpo, err := p.do(req, http.StatusNoContent); err != nil {
		var he *httpError
		switch {
		case !errors.As(err, &he):
			return err
		case he.code == http.StatusNotFound:
			return nil
		case he.code == http.StatusOK && mensajeDeKick(cuerpo) == "":
			return nil
		}
		return err
	}
	p.logger.Info("chat de Kick dado de baja", "canal", acct.ExternalID, "suscripciones", len(ids))
	return nil
}
