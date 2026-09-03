package httpapi

import (
	"context"
	"net/http"
	"strconv"
)

// status compone el estado completo del servicio.
//
// Lo usan GET /api/status y el WebSocket, y es el MISMO método a propósito: el spec §10
// dice que la interfaz arranca con el snapshot REST y sigue con el WS, así que dos
// compositores acabarían divergiendo y la UI vería saltar el estado al conectar.
func (s *Server) status(ctx context.Context, r *http.Request) (statusDTO, error) {
	var out statusDTO

	settings, err := s.db.Settings(ctx)
	if err != nil {
		return out, err
	}
	out.Ingest = ingestDTO{
		URL:     s.ingestURL(r, settings.IngestApp),
		App:     settings.IngestApp,
		KeyMask: settings.IngestKeyMask,
	}

	if s.engine != nil {
		if id := s.engine.SessionID(); id != 0 {
			out.Session.Live = true
			out.Session.ID = id
			// La fila puede no estar todavía si la sesión acaba de abrirse: no es un
			// error, simplemente se devuelve lo que se sabe.
			if ses, err := s.db.SessionByID(ctx, id); err == nil {
				out.Session.StartedAt = &ses.StartedAt
				out.Session.Width = ses.Width
				out.Session.Height = ses.Height
				out.Session.BitrateBPS = ses.BitrateBPS
			}
		}
	}

	dests, err := s.db.ListDestinations(ctx)
	if err != nil {
		return out, err
	}
	out.Destinations = make([]destinationDTO, 0, len(dests))
	for _, d := range dests {
		out.Destinations = append(out.Destinations, newDestinationDTO(d, s.metricsFor(d.ID)))
	}
	return out, nil
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.status(r.Context(), r)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			// Esto sí es una petición mal formada. Un límite fuera de rango no lo es:
			// RecentEvents ya lo acota por arriba y por abajo, y pedir 0 o un millón es
			// descuido del cliente, no una petición inválida.
			writeError(w, http.StatusBadRequest, codeInvalidInput, "limit debe ser un número")
			return
		}
		limit = n
	}

	eventos, err := s.db.RecentEvents(r.Context(), limit)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	out := make([]eventDTO, 0, len(eventos))
	for _, e := range eventos {
		out = append(out, newEventDTO(e))
	}
	writeJSON(w, http.StatusOK, out)
}
