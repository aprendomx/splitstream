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
	return n, true
}
