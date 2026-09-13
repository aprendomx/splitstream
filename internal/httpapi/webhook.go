package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// maxWebhookBody acota lo que se lee de una entrega. Un mensaje de chat son unos cientos
// de bytes; 256 KiB dejan sitio de sobra a un lote y a las cabeceras del sobre, y evitan
// que quien llame a este endpoint —que es público— gaste memoria del proceso que está
// retransmitiendo.
const maxWebhookBody = 256 << 10

// maxMensajesPorEntrega acota cuántos mensajes se aceptan de UNA entrega: la plataforma
// decide cuántos manda, y sin tope un lote enorme llenaría el bus de golpe. Cien pasa de
// sobra cualquier entrega real de Kick.
const maxMensajesPorEntrega = 100

// rechazoCadencia acota el evento `webhook_rejected` a uno por minuto. Sin esto, un bucle
// de entregas mal firmadas llenaría el registro del panel y la tabla de eventos.
const rechazoCadencia = time.Minute

// handleKickWebhook recibe el chat que Kick nos manda (spec §8.2). Es PÚBLICA: quien llama
// es la plataforma, que no tiene cookie. Lo que la protege es la firma del payload, que
// verifica el proveedor en ParseWebhook.
//
// La respuesta nunca cuenta qué pasó con la entrega —si la cuenta existe, si el tipo de
// evento nos interesa, cuántos mensajes entraron—: es un endpoint abierto, y contarlo
// serviría para averiguar qué cuentas tiene este servidor. Solo hay tres respuestas: 401
// si la firma no vale, 413 si el cuerpo es enorme, y 200 para todo lo demás.
func (s *Server) handleKickWebhook(w http.ResponseWriter, r *http.Request) {
	var p platforms.Provider
	if s.platforms != nil {
		p, _ = s.platforms.Get(platforms.Kick)
	}
	cw, ok := p.(platforms.ChatWebhook)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "plataforma sin proveedor")
		return
	}

	// Se lee un byte de más para distinguir "justo el tope" de "se pasó".
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(body) > maxWebhookBody {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}

	msgs, err := cw.ParseWebhook(r.Header, body)
	switch {
	case errors.Is(err, platforms.ErrWebhookRejected):
		// El 401 es el único caso en el que se dice algo: la plataforma lo necesita para
		// saber que su firma no nos vale y dejar de reintentar esa entrega.
		s.registrarRechazo(r.Context(), err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	case err != nil:
		s.logger.Debug("no se pudo leer el webhook de Kick", "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	// 200 ANTES de tocar la base y el bus: la plataforma mide cuánto tardamos en aceptar
	// la entrega y reintenta si nos demoramos. Lo que queda es rápido —una lectura por
	// id de canal y un Ingest que no bloquea—, pero se hace con la entrega ya aceptada.
	//
	// El Content-Length explícito es lo que hace que el 200 sea de verdad: sin él, Go no
	// sabe cuánto cuerpo viene y manda la respuesta troceada, así que el cliente no la da
	// por terminada hasta el trozo final —que solo se escribe cuando este handler
	// retorna—. Con Content-Length: 0, el flush entrega una respuesta completa.
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	s.ingestarChat(r.Context(), msgs)
}

// ingestarChat resuelve a qué cuenta nuestra pertenece cada mensaje y lo mete en el bus.
// La plataforma solo dice de qué canal es (BroadcasterID), así que la cuenta se busca por
// ese id externo; un mensaje de un canal que no tenemos conectado se descarta en silencio.
func (s *Server) ingestarChat(ctx context.Context, msgs []platforms.ChatMessage) {
	if len(msgs) == 0 || s.chatIngest == nil {
		return
	}
	if len(msgs) > maxMensajesPorEntrega {
		msgs = msgs[:maxMensajesPorEntrega]
	}
	// Una entrega trae normalmente mensajes de un solo canal: el mapa evita repetir la
	// misma consulta por cada mensaje del lote. El 0 significa "ese canal no es nuestro".
	cuentas := make(map[string]int64, 1)
	out := make([]platforms.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		id, visto := cuentas[m.BroadcasterID]
		if !visto {
			if a, err := s.db.AccountByExternalID(ctx, store.PlatformKick, m.BroadcasterID); err == nil {
				id = a.ID
			}
			cuentas[m.BroadcasterID] = id
		}
		if id == 0 {
			continue
		}
		m.AccountID = id
		out = append(out, m)
	}
	if len(out) == 0 {
		return
	}
	s.chatIngest(out)
}

// registrarRechazo deja constancia de un webhook que no pasó la verificación, como mucho
// una vez por minuto. El motivo lo escribe el proveedor y pasa por textoSeguro: nunca
// lleva la firma recibida ni el cuerpo.
func (s *Server) registrarRechazo(ctx context.Context, err error) {
	s.rechazoMu.Lock()
	if !s.ultimoRechazo.IsZero() && s.now().Sub(s.ultimoRechazo) < rechazoCadencia {
		s.rechazoMu.Unlock()
		return
	}
	s.ultimoRechazo = s.now()
	s.rechazoMu.Unlock()

	s.db.LogEvent(context.WithoutCancel(ctx), store.Event{Level: store.LevelWarn, Kind: "webhook_rejected",
		Message: "webhook de Kick rechazado: " + textoSeguro(err.Error())})
}
