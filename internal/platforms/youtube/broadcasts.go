// Crear la emisión, salir al aire, terminar y poner el título: spec §5. La clave de
// ingesta de YouTube solo existe una vez creada la emisión (a diferencia de Kick, cuya
// clave es del canal): IngestKey siempre devuelve ErrNoBroadcast, el handler HTTP usa
// CreateBroadcast en su lugar.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

const (
	defaultTransitionRetry    = 5 * time.Second
	defaultTransitionDeadline = 60 * time.Second
)

// privacidadesVálidas son los tres valores que acepta status.privacyStatus. Vacío no está
// aquí a propósito: se resuelve a "unlisted" antes de validar, nunca es en sí un valor
// inválido.
var privacidadesVálidas = map[string]bool{"public": true, "unlisted": true, "private": true}

// CreateBroadcast son tres llamadas: liveBroadcasts.insert, liveStreams.insert y
// liveBroadcasts.bind. Devuelve la URL de ingesta y la clave por separado (Broadcast.
// IngestURL/Key), nunca concatenadas: quien arma la URL RTMP completa es quien consume
// esta respuesta, no este paquete.
func (p *Provider) CreateBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, req platforms.BroadcastRequest) (platforms.Broadcast, error) {
	if req.Title == "" {
		return platforms.Broadcast{}, errors.New("youtube: el título de la emisión no puede estar vacío")
	}
	privacidad := req.Privacy
	if privacidad == "" {
		privacidad = "unlisted"
	}
	if !privacidadesVálidas[privacidad] {
		return platforms.Broadcast{}, errors.New("youtube: privacidad inválida: " + req.Privacy)
	}
	inicio := req.ScheduledAt
	if inicio.IsZero() {
		inicio = p.now()
	}

	bcastID, liveChatID, err := p.insertBroadcast(ctx, acct, token, req.Title, privacidad, inicio)
	if err != nil {
		return platforms.Broadcast{}, err
	}
	streamID, ingestURL, key, err := p.insertStream(ctx, acct, token, req.Title)
	if err != nil {
		return platforms.Broadcast{}, err
	}
	if err := p.bindStream(ctx, acct, token, bcastID, streamID); err != nil {
		return platforms.Broadcast{}, err
	}

	return platforms.Broadcast{
		Ref:        bcastID,
		StreamRef:  streamID,
		LiveChatID: liveChatID,
		IngestURL:  ingestURL,
		Key:        crypto.Secret(key),
		WatchURL:   "https://www.youtube.com/watch?v=" + bcastID,
	}, nil
}

// insertBroadcast crea el liveBroadcast: título, hora de inicio (RFC 3339 UTC),
// privacidad, y las banderas fijas que pide el spec §5 (autostart/autostop, sin marcar
// para niños, latencia normal).
func (p *Provider) insertBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, title, privacidad string, inicio time.Time) (id, liveChatID string, err error) {
	var cuerpo struct {
		Snippet struct {
			Title              string `json:"title"`
			ScheduledStartTime string `json:"scheduledStartTime"`
		} `json:"snippet"`
		Status struct {
			PrivacyStatus           string `json:"privacyStatus"`
			SelfDeclaredMadeForKids bool   `json:"selfDeclaredMadeForKids"`
		} `json:"status"`
		ContentDetails struct {
			EnableAutoStart   bool   `json:"enableAutoStart"`
			EnableAutoStop    bool   `json:"enableAutoStop"`
			LatencyPreference string `json:"latencyPreference"`
		} `json:"contentDetails"`
	}
	cuerpo.Snippet.Title = title
	cuerpo.Snippet.ScheduledStartTime = inicio.UTC().Format(time.RFC3339)
	cuerpo.Status.PrivacyStatus = privacidad
	cuerpo.Status.SelfDeclaredMadeForKids = false
	cuerpo.ContentDetails.EnableAutoStart = true
	cuerpo.ContentDetails.EnableAutoStop = true
	cuerpo.ContentDetails.LatencyPreference = "normal"
	b, err := json.Marshal(cuerpo)
	if err != nil {
		return "", "", err
	}
	httpReq, err := p.api(ctx, http.MethodPost, "/liveBroadcasts?part=snippet,status,contentDetails", token, bytes.NewReader(b))
	if err != nil {
		return "", "", err
	}
	body, err := p.do(httpReq, http.StatusOK)
	p.gastar(acct, "insert")
	if err != nil {
		return "", "", err
	}
	var out struct {
		ID      string `json:"id"`
		Snippet struct {
			LiveChatID string `json:"liveChatId"`
		} `json:"snippet"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" {
		return "", "", errors.New("youtube: respuesta de liveBroadcasts.insert ilegible")
	}
	return out.ID, out.Snippet.LiveChatID, nil
}

// insertStream crea el liveStream RTMP variable; ingestionAddress y streamName son la URL
// y la clave que el destino necesita para emitir.
func (p *Provider) insertStream(ctx context.Context, acct store.Account, token crypto.Secret, title string) (id, ingestURL, key string, err error) {
	var cuerpo struct {
		Snippet struct {
			Title string `json:"title"`
		} `json:"snippet"`
		CDN struct {
			IngestionType string `json:"ingestionType"`
			Resolution    string `json:"resolution"`
			FrameRate     string `json:"frameRate"`
		} `json:"cdn"`
	}
	cuerpo.Snippet.Title = title
	cuerpo.CDN.IngestionType = "rtmp"
	cuerpo.CDN.Resolution = "variable"
	cuerpo.CDN.FrameRate = "variable"
	b, err := json.Marshal(cuerpo)
	if err != nil {
		return "", "", "", err
	}
	httpReq, err := p.api(ctx, http.MethodPost, "/liveStreams?part=snippet,cdn", token, bytes.NewReader(b))
	if err != nil {
		return "", "", "", err
	}
	body, err := p.do(httpReq, http.StatusOK)
	p.gastar(acct, "insert")
	if err != nil {
		return "", "", "", err
	}
	var out struct {
		ID  string `json:"id"`
		CDN struct {
			IngestionInfo struct {
				StreamName       string `json:"streamName"`
				IngestionAddress string `json:"ingestionAddress"`
			} `json:"ingestionInfo"`
		} `json:"cdn"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" {
		return "", "", "", errors.New("youtube: respuesta de liveStreams.insert ilegible")
	}
	return out.ID, out.CDN.IngestionInfo.IngestionAddress, out.CDN.IngestionInfo.StreamName, nil
}

// bindStream une el broadcast con el stream: sin esto, transition a "live" no tendría
// señal RTMP que emitir.
func (p *Provider) bindStream(ctx context.Context, acct store.Account, token crypto.Secret, bcastID, streamID string) error {
	q := "id=" + url.QueryEscape(bcastID) + "&streamId=" + url.QueryEscape(streamID) + "&part=id,contentDetails"
	httpReq, err := p.api(ctx, http.MethodPost, "/liveBroadcasts/bind?"+q, token, nil)
	if err != nil {
		return err
	}
	_, err = p.do(httpReq, http.StatusOK)
	p.gastar(acct, "bind")
	return err
}

// transitionRetry y transitionDeadline son los valores efectivos: Options.TransitionRetry/
// Deadline si vienen, si no los valores por defecto del spec §5 (5 s / 60 s).
func (p *Provider) transitionRetry() time.Duration {
	if p.retry > 0 {
		return p.retry
	}
	return defaultTransitionRetry
}

func (p *Provider) transitionDeadline() time.Duration {
	if p.deadline > 0 {
		return p.deadline
	}
	return defaultTransitionDeadline
}

// StartBroadcast pide "live" y reintenta mientras YouTube diga errorStreamInactive: la
// señal RTMP de OBS tarda en llegar tras crear/bind, así que el primer intento (o los
// primeros) puede fallar sin que sea un error real. Cualquier otro motivo de error se
// devuelve de inmediato, sin reintentar. Nunca time.Sleep a secas: el reintento respeta
// ctx.Done() con select.
func (p *Provider) StartBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, ref string) error {
	retry, deadline := p.transitionRetry(), p.transitionDeadline()
	limite := time.Now().Add(deadline)
	for {
		err := p.transition(ctx, acct, token, ref, "live")
		if err == nil {
			return nil
		}
		var ae *apiError
		if !errors.As(err, &ae) || ae.reason != "errorStreamInactive" {
			return err
		}
		if !time.Now().Before(limite) {
			return errors.New("YouTube no recibe la señal todavía: arranca OBS y vuelve a intentarlo")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retry):
		}
	}
}

// EndBroadcast pide "complete". redundantTransition significa que ya estaba terminada (o
// nunca llegó a estar en vivo): se trata como éxito, no como error, para que "Terminar"
// nunca falle por haberse llamado dos veces.
func (p *Provider) EndBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, ref string) error {
	err := p.transition(ctx, acct, token, ref, "complete")
	if err == nil {
		return nil
	}
	var ae *apiError
	if errors.As(err, &ae) && ae.reason == "redundantTransition" {
		return nil
	}
	return err
}

func (p *Provider) transition(ctx context.Context, acct store.Account, token crypto.Secret, ref, estado string) error {
	q := "broadcastStatus=" + estado + "&id=" + url.QueryEscape(ref) + "&part=id,status"
	httpReq, err := p.api(ctx, http.MethodPost, "/liveBroadcasts/transition?"+q, token, nil)
	if err != nil {
		return err
	}
	_, err = p.do(httpReq, http.StatusOK)
	p.gastar(acct, "transition")
	return err
}

// emisionSnippet es la parte de snippet que sí importa conservar en un update: Google
// trata liveBroadcasts.update con part=snippet como un REEMPLAZO completo del snippet, no
// un parche. Si solo mandamos el título, scheduledStartTime se pierde (o Google devuelve
// 400 en emisiones programadas): por eso cada update relee estos campos y los reenvía tal
// cual, con el título nuevo encima.
type emisionSnippet struct {
	Title              string `json:"title"`
	Description        string `json:"description"`
	ScheduledStartTime string `json:"scheduledStartTime"`
}

// SetTitle no recibe la emisión (la interfaz platforms.TitleSetter es la misma para
// Twitch/Kick, donde el título es del canal): busca la próxima emisión del canal
// (broadcastStatus=upcoming) y si no hay ninguna, la activa (broadcastStatus=active). Sin
// ninguna de las dos, ErrNoBroadcast — el resultado por destino le dice a la persona que
// cree la emisión primero. findBroadcast ya pide part=id,snippet: el snippet que trae ese
// list es el mismo que hay que reenviar en el update, así que aquí NO hace falta un GET
// aparte (a diferencia de SetBroadcastTitle, que solo tiene el id).
func (p *Provider) SetTitle(ctx context.Context, acct store.Account, token crypto.Secret, title string) error {
	ref, snip, err := p.findBroadcast(ctx, acct, token, "upcoming")
	if err != nil {
		return err
	}
	if ref == "" {
		ref, snip, err = p.findBroadcast(ctx, acct, token, "active")
		if err != nil {
			return err
		}
	}
	if ref == "" {
		return platforms.ErrNoBroadcast
	}
	snip.Title = title
	return p.updateTitle(ctx, acct, token, ref, snip)
}

// SetBroadcastTitle actualiza directamente la emisión que ya se conoce (BroadcastRef del
// destino): se salta el list por broadcastStatus que hace SetTitle cuando no hace falta
// adivinar cuál es. Pero como solo tiene el id, sí necesita leer el snippet actual antes
// de reemplazarlo (ver emisionSnippet) — sin emisión con ese id, ErrNoBroadcast.
func (p *Provider) SetBroadcastTitle(ctx context.Context, acct store.Account, token crypto.Secret, ref, title string) error {
	snip, err := p.getSnippet(ctx, acct, token, ref)
	if err != nil {
		return err
	}
	snip.Title = title
	return p.updateTitle(ctx, acct, token, ref, snip)
}

func (p *Provider) findBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, estado string) (string, emisionSnippet, error) {
	httpReq, err := p.api(ctx, http.MethodGet, "/liveBroadcasts?part=id,snippet&mine=true&broadcastStatus="+estado, token, nil)
	if err != nil {
		return "", emisionSnippet{}, err
	}
	body, err := p.do(httpReq, http.StatusOK)
	p.gastar(acct, "list")
	if err != nil {
		return "", emisionSnippet{}, err
	}
	var out struct {
		Items []struct {
			ID      string         `json:"id"`
			Snippet emisionSnippet `json:"snippet"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", emisionSnippet{}, errors.New("youtube: respuesta de liveBroadcasts.list ilegible")
	}
	if len(out.Items) == 0 {
		return "", emisionSnippet{}, nil
	}
	return out.Items[0].ID, out.Items[0].Snippet, nil
}

// getSnippet lee el snippet actual de una emisión por id (1 unidad, "list"): lo que
// SetBroadcastTitle necesita releer porque solo le llega el ref, no el snippet completo
// que sí trae findBroadcast.
func (p *Provider) getSnippet(ctx context.Context, acct store.Account, token crypto.Secret, ref string) (emisionSnippet, error) {
	httpReq, err := p.api(ctx, http.MethodGet, "/liveBroadcasts?part=snippet&id="+url.QueryEscape(ref), token, nil)
	if err != nil {
		return emisionSnippet{}, err
	}
	body, err := p.do(httpReq, http.StatusOK)
	p.gastar(acct, "list")
	if err != nil {
		return emisionSnippet{}, err
	}
	var out struct {
		Items []struct {
			Snippet emisionSnippet `json:"snippet"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return emisionSnippet{}, errors.New("youtube: respuesta de liveBroadcasts.list ilegible")
	}
	if len(out.Items) == 0 {
		return emisionSnippet{}, platforms.ErrNoBroadcast
	}
	return out.Items[0].Snippet, nil
}

// updateTitle reemplaza el snippet completo (ver emisionSnippet): snip ya trae el título
// nuevo puesto por quien llama, y scheduledStartTime/description tal como se leyeron, para
// que el update no se los borre.
func (p *Provider) updateTitle(ctx context.Context, acct store.Account, token crypto.Secret, ref string, snip emisionSnippet) error {
	var cuerpo struct {
		ID      string         `json:"id"`
		Snippet emisionSnippet `json:"snippet"`
	}
	cuerpo.ID = ref
	cuerpo.Snippet = snip
	b, err := json.Marshal(cuerpo)
	if err != nil {
		return err
	}
	httpReq, err := p.api(ctx, http.MethodPut, "/liveBroadcasts?part=snippet", token, bytes.NewReader(b))
	if err != nil {
		return err
	}
	_, err = p.do(httpReq, http.StatusOK)
	p.gastar(acct, "update")
	return err
}

// IngestKey: YouTube no tiene clave sin emisión (a diferencia de Kick, cuya clave es del
// canal). El handler HTTP usa CreateBroadcast para obtenerla.
func (p *Provider) IngestKey(ctx context.Context, acct store.Account, token crypto.Secret) (string, crypto.Secret, error) {
	return "", "", platforms.ErrNoBroadcast
}
