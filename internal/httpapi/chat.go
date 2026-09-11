package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/aprendomx/splitstream/internal/chat"
	"github.com/aprendomx/splitstream/internal/store"
)

func chatDTO(m chat.Message) chatMessageDTO {
	acct := m.AccountID
	badges := m.Badges
	if badges == nil {
		badges = []string{}
	}
	return chatMessageDTO{SessionID: m.SessionID, Platform: string(m.Platform), AccountID: &acct, AuthorID: m.AuthorID,
		Author: m.Author, Text: m.Text, Color: m.Color, Badges: badges, MessageID: m.MessageID, At: m.At}
}

func storedChatDTO(m store.ChatMessage) chatMessageDTO {
	badges := m.Badges
	if badges == nil {
		badges = []string{}
	}
	return chatMessageDTO{ID: m.ID, SessionID: m.SessionID, Platform: m.Platform, AccountID: m.AccountID, AuthorID: m.AuthorID,
		Author: m.Author, Text: m.Text, Color: m.Color, Badges: badges, MessageID: m.MessageID, At: m.At}
}

// handleChatWS empuja el chat de la sesión: primero los últimos 50 y luego cada mensaje.
// Mismo plazo de escritura que el WS de estado; el cliente nunca manda nada.
func (s *Server) handleChatWS(w http.ResponseWriter, r *http.Request) {
	if s.chat == nil {
		writeError(w, http.StatusNotFound, codeNotFound, "el chat no está disponible")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.logger.Warn("no se pudo abrir el WebSocket del chat", "err", err)
		return
	}
	defer conn.CloseNow()
	// Suscribir ANTES de leer los recientes: un mensaje entre las dos lecturas llegaría por
	// el canal en vez de perderse.
	ch, release := s.chat.Subscribe(256)
	defer release()
	ctx := conn.CloseRead(r.Context())
	for _, m := range s.chat.Recent() {
		if !s.writeChat(ctx, conn, chatDTO(m)) {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			if !s.writeChat(ctx, conn, chatDTO(m)) {
				return
			}
		}
	}
}

func (s *Server) writeChat(ctx context.Context, conn *websocket.Conn, m chatMessageDTO) bool {
	escritura, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	if err := wsjson.Write(escritura, conn, m); err != nil {
		s.logger.Debug("se cerró el WebSocket del chat", "err", err)
		return false
	}
	return true
}

func (s *Server) handleSessionChat(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidInput, "limit inválido")
			return
		}
		limit = n
	}
	ms, err := s.db.ChatMessages(r.Context(), id, after, limit)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	out := make([]chatMessageDTO, 0, len(ms))
	for _, m := range ms {
		out = append(out, storedChatDTO(m))
	}
	writeJSON(w, http.StatusOK, out)
}
