package httpapi

import (
	"net/http"
	"strconv"
)

// handleSessions lista el historial de sesiones. Pagina por id (`before`), nunca por
// fecha.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryInt(w, r, "limit")
	if !ok {
		return
	}
	before, ok := queryInt(w, r, "before")
	if !ok {
		return
	}
	sesiones, err := s.db.ListSessions(r.Context(), limit, int64(before))
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	out := make([]sessionSummaryDTO, 0, len(sesiones))
	for _, ses := range sesiones {
		out = append(out, newSessionSummaryDTO(ses))
	}
	writeJSON(w, http.StatusOK, out)
}

// recordingsPorSesionLimit acota cuántas grabaciones trae la ficha de una sesión. Una
// sesión graba en segmentos de minutos: unos cientos cubren de sobra hasta la más larga.
const recordingsPorSesionLimit = 500

// handleSessionDetail es la ficha de una sesión: sus eventos, sus grabaciones y los
// contadores de su chat (spec v0.13 §3.4).
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	ses, err := s.db.SessionByID(ctx, id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	eventos, err := s.db.EventsBySession(ctx, id, 0)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	grabaciones, err := s.db.ListRecordings(ctx, id, recordingsPorSesionLimit, 0)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	total, porPlataforma, err := s.db.ChatCountBySession(ctx, id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newSessionDetailDTO(*ses, eventos, grabaciones, total, porPlataforma))
}

// queryInt lee un parámetro numérico opcional. Ausente vale 0; mal formado es 400.
func queryInt(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidInput, name+" debe ser un número")
		return 0, false
	}
	// Un negativo no es "sin valor": `before=-1` pediría una página que no existe y el
	// store lo interpretaría como un id. Se rechaza aquí, que es donde se lee.
	if n < 0 {
		writeError(w, http.StatusBadRequest, codeInvalidInput, name+" debe ser un número mayor o igual que 0")
		return 0, false
	}
	return n, true
}
