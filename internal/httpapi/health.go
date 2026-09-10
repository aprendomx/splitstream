package httpapi

import (
	"context"
	"net/http"
	"time"
)

type healthDTO struct {
	Status string `json:"status"`
	DB     string `json:"db"`
}

// handleHealthz es público (spec v0.8 §6): responde si el proceso atiende y la base
// contesta. No mira el motor —"sin sesión" no es "enfermo"— ni dice la versión.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		s.logger.Warn("healthz: la base no responde", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, healthDTO{Status: "degraded", DB: "error"})
		return
	}
	writeJSON(w, http.StatusOK, healthDTO{Status: "ok", DB: "ok"})
}
