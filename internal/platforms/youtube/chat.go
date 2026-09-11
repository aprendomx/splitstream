// ReadChat: a diferencia de Twitch (EventSub por WebSocket, empuja), YouTube no tiene
// push — hay que sondear liveChatMessages.list con el pageToken que devuelve cada
// respuesta y esperar pollingIntervalMillis (con un piso propio, MinPoll) antes del
// siguiente sondeo. Cada sondeo cuesta cuota (chat_list, 5 unidades): por eso hay un
// presupuesto inyectable (ChatBudget) que se consulta ANTES de cada llamada, para
// pausarse antes de que Google devuelva 403 quotaExceeded, no después.
package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

const (
	// defaultChatNoBroadcastWait/GiveUp son el ritmo con el que ReadChat busca una emisión
	// activa cuando todavía no hay ninguna: cada 30 s, hasta 10 min antes de rendirse.
	defaultChatNoBroadcastWait   = 30 * time.Second
	defaultChatNoBroadcastGiveUp = 10 * time.Minute
	// defaultMinPoll es el piso del intervalo de sondeo, sin importar lo que pida
	// pollingIntervalMillis: la Data API cobra cuota por sondeo, así que un valor bajo (o
	// 0, de una respuesta rara) no debe traducirse en martillar la API.
	defaultMinPoll = 2 * time.Second
	// chatBackoffMin/Max gobiernan el reintento ante errores no clasificados del sondeo
	// (red, 5xx, respuesta ilegible): ni la búsqueda de emisión ni el sondeo del chat
	// deben martillar la API cuando algo falla de forma transitoria.
	chatBackoffMin = 5 * time.Second
	chatBackoffMax = 60 * time.Second
)

// sleepReal es el Sleep por defecto: espera d o se rinde si ctx termina antes.
func sleepReal(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// dormir es el punto único por el que este archivo espera: usa p.sleep (inyectable en
// tests) y traduce su resultado a "seguir" (true) o "hay que salir" (false, sea porque
// ctx terminó o porque el Sleep de prueba así lo decidió).
func (p *Provider) dormir(ctx context.Context, d time.Duration) bool {
	return p.sleep(ctx, d) == nil
}

// ReadChat implementa platforms.ChatReader: bloquea hasta que ctx termine, alternando
// entre buscar la emisión activa de la cuenta y sondear su chat mientras siga viva. Si la
// emisión activa termina, vuelve a buscar otra sin devolver error (spec: no hay push que
// avise de un cambio de emisión, así que "se acabó" y "cambiaron de emisión" se ven
// igual). Solo devuelve error si no puede ni empezar (token, 401, presupuesto agotado, o
// se cumplió el plazo sin encontrar ninguna emisión activa).
func (p *Provider) ReadChat(ctx context.Context, acct store.Account, token platforms.TokenSource, out chan<- platforms.ChatMessage) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		liveChatID, err := p.buscarEmisionActiva(ctx, acct, token)
		if err != nil {
			return err
		}
		if liveChatID == "" {
			// ctx terminó durante la búsqueda: no es un error, es la señal de salir.
			return nil
		}
		if err := p.sondearChat(ctx, acct, token, liveChatID, out); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		// El sondeo volvió sin error y sin que ctx terminara: la emisión activa se acabó
		// (offlineAt/liveChatEnded). Se vuelve a buscar otra desde el principio.
	}
}

// buscarEmisionActiva pide liveBroadcasts?broadcastStatus=active hasta encontrar una con
// chat (gastar "list", vía findBroadcast) o hasta cumplirse ChatNoBroadcastGiveUp sin
// ninguna, momento en el que se rinde con un error. Un 401 o un 403 quotaExceeded
// interrumpen la espera de inmediato: no tiene sentido seguir reintentando cada 30 s si
// hace falta reautorizar o si la cuenta ya no tiene cuota. Cualquier otro error (red,
// 5xx) se reintenta con el backoff corto, no con ChatNoBroadcastWait.
func (p *Provider) buscarEmisionActiva(ctx context.Context, acct store.Account, token platforms.TokenSource) (string, error) {
	inicio := p.now()
	backoff := chatBackoffMin
	for {
		if ctx.Err() != nil {
			return "", nil
		}
		tok, err := token(ctx)
		if err != nil {
			return "", fmt.Errorf("chat de youtube: %w", err)
		}
		_, snip, err := p.findBroadcast(ctx, acct, tok, "active")
		if err != nil {
			if ctx.Err() != nil {
				return "", nil
			}
			if errors.Is(err, platforms.ErrUnauthorized) {
				return "", err
			}
			if errors.Is(err, platforms.ErrRateLimited) {
				p.avisarPausaPorCuota(acct)
				return "", err
			}
			p.logger.Warn("no se pudo buscar la emisión activa de youtube; reintentando",
				"cuenta", acct.DisplayName, "err", err, "en", backoff)
			if !p.dormir(ctx, backoff) {
				return "", nil
			}
			backoff = min(backoff*2, chatBackoffMax)
			continue
		}
		if snip.LiveChatID != "" {
			return snip.LiveChatID, nil
		}
		if p.now().Sub(inicio) >= p.chatNoBroadcastGiveUp {
			return "", errors.New("no hay emisión activa de youtube: se agotó la espera")
		}
		if !p.dormir(ctx, p.chatNoBroadcastWait) {
			return "", nil
		}
	}
}

// avisarPausaPorCuota llama OnChatPaused con used=budget=budget (spec: al toparse con
// quotaExceeded ya no importa el desglose, lo que importa es que no queda margen) cuando
// hay presupuesto configurado. Sin ChatBudget no hay budget que reportar, así que no
// llama a nada: el error que sigue ya basta para que quien orquesta se entere.
func (p *Provider) avisarPausaPorCuota(acct store.Account) {
	if p.chatBudget == nil || p.onChatPaused == nil {
		return
	}
	_, budget, _ := p.chatBudget(acct.ID)
	p.onChatPaused(acct, budget, budget)
}

// sondearChat sondea liveChatMessages.list de una emisión activa concreta hasta que ctx
// termine (nil), la emisión termine (offlineAt/liveChatEnded, nil: ReadChat busca otra),
// el presupuesto se agote (propio o quotaExceeded de Google), o llegue un 401.
func (p *Provider) sondearChat(ctx context.Context, acct store.Account, token platforms.TokenSource, liveChatID string, out chan<- platforms.ChatMessage) error {
	pageToken := ""
	backoff := chatBackoffMin
	for {
		if ctx.Err() != nil {
			return nil
		}
		if p.chatBudget != nil {
			used, budget, ok := p.chatBudget(acct.ID)
			if !ok {
				// El presupuesto es protección, no bloqueo: si no se puede leer, se sondea
				// igual y solo queda constancia del warning.
				p.logger.Warn("no se pudo leer el presupuesto de cuota de youtube; se sondea el chat de todos modos",
					"cuenta", acct.DisplayName)
			} else if used+costos["chat_list"] > budget {
				if p.onChatPaused != nil {
					p.onChatPaused(acct, used, budget)
				}
				return fmt.Errorf("el chat de youtube se pausó al llegar al presupuesto de %d unidades", budget)
			}
		}
		tok, err := token(ctx)
		if err != nil {
			return fmt.Errorf("chat de youtube: %w", err)
		}
		pagina, err := p.chatPage(ctx, acct, tok, liveChatID, pageToken)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, platforms.ErrUnauthorized) {
				return err
			}
			if errors.Is(err, platforms.ErrRateLimited) {
				p.avisarPausaPorCuota(acct)
				return err
			}
			p.logger.Warn("error sondeando el chat de youtube; reintentando",
				"cuenta", acct.DisplayName, "err", err, "en", backoff)
			if !p.dormir(ctx, backoff) {
				return nil
			}
			backoff = min(backoff*2, chatBackoffMax)
			continue
		}
		backoff = chatBackoffMin
		for _, m := range pagina.mensajes {
			select {
			case out <- m:
			default:
				// El agregador no lee: se descarta antes que bloquear el sondeo.
				p.logger.Debug("mensaje de chat de youtube descartado: el consumidor no lee")
			}
		}
		if pagina.terminado {
			return nil
		}
		pageToken = pagina.nextPageToken
		espera := time.Duration(pagina.pollingIntervalMillis) * time.Millisecond
		if espera < p.minPoll {
			espera = p.minPoll
		}
		if !p.dormir(ctx, espera) {
			return nil
		}
	}
}

// chatPagina es lo que chatPage extrae de una respuesta de liveChatMessages.list.
type chatPagina struct {
	mensajes              []platforms.ChatMessage
	nextPageToken         string
	pollingIntervalMillis int
	// terminado: la emisión dejó de estar en vivo (offlineAt en la respuesta, o 404/403
	// liveChatEnded). ReadChat vuelve a buscar otra emisión sin tratarlo como error.
	terminado bool
}

// chatPage pide una página de liveChatMessages.list y la convierte. Gasta cuota
// (chat_list) tanto si sale bien como si sale mal, igual que el resto del paquete: la
// llamada ya salió a la red y Google ya la cobró.
func (p *Provider) chatPage(ctx context.Context, acct store.Account, token crypto.Secret, liveChatID, pageToken string) (chatPagina, error) {
	q := "liveChatId=" + url.QueryEscape(liveChatID) + "&part=snippet,authorDetails"
	if pageToken != "" {
		q += "&pageToken=" + url.QueryEscape(pageToken)
	}
	httpReq, err := p.api(ctx, http.MethodGet, "/liveChatMessages?"+q, token, nil)
	if err != nil {
		return chatPagina{}, err
	}
	body, err := p.do(httpReq, http.StatusOK)
	p.gastar(acct, "chat_list")
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && (ae.code == http.StatusNotFound ||
			(ae.code == http.StatusForbidden && ae.reason == "liveChatEnded")) {
			return chatPagina{terminado: true}, nil
		}
		return chatPagina{}, err
	}
	var resp struct {
		OfflineAt             string `json:"offlineAt"`
		NextPageToken         string `json:"nextPageToken"`
		PollingIntervalMillis int    `json:"pollingIntervalMillis"`
		Items                 []struct {
			ID      string `json:"id"`
			Snippet struct {
				DisplayMessage string `json:"displayMessage"`
				PublishedAt    string `json:"publishedAt"`
			} `json:"snippet"`
			AuthorDetails struct {
				ChannelID       string `json:"channelId"`
				DisplayName     string `json:"displayName"`
				IsChatOwner     bool   `json:"isChatOwner"`
				IsChatModerator bool   `json:"isChatModerator"`
			} `json:"authorDetails"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return chatPagina{}, errors.New("youtube: respuesta de liveChatMessages.list ilegible")
	}
	pagina := chatPagina{
		nextPageToken:         resp.NextPageToken,
		pollingIntervalMillis: resp.PollingIntervalMillis,
		terminado:             resp.OfflineAt != "",
	}
	for _, it := range resp.Items {
		publicado, _ := time.Parse(time.RFC3339, it.Snippet.PublishedAt)
		m := platforms.ChatMessage{
			Platform:  platforms.YouTube,
			AccountID: acct.ID,
			AuthorID:  it.AuthorDetails.ChannelID,
			Author:    it.AuthorDetails.DisplayName,
			Text:      it.Snippet.DisplayMessage,
			MessageID: it.ID,
			At:        publicado,
		}
		if it.AuthorDetails.IsChatOwner {
			m.Badges = append(m.Badges, "owner")
		}
		if it.AuthorDetails.IsChatModerator {
			m.Badges = append(m.Badges, "moderator")
		}
		pagina.mensajes = append(pagina.mensajes, m)
	}
	return pagina, nil
}
