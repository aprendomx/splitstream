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
	out.Version = s.version

	settings, err := s.db.Settings(ctx)
	if err != nil {
		return out, err
	}
	out.Ingest = ingestDTO{
		URL:     s.ingestURL(r, settings.IngestApp),
		App:     settings.IngestApp,
		KeyMask: settings.IngestKeyMask,
	}

	// Los datos de la sesión salen del MOTOR, no de la fila de sessions: esa solo se
	// completa en FinishSession, así que durante la emisión tendría la resolución y el
	// bitrate en null — y el spec §10 pide enseñarlos en vivo.
	//
	// Width y Height llegan en 0 hasta el primer sequence header, que en la práctica es el
	// primer segundo. Se mandan como null para que la interfaz distinga "todavía no se
	// sabe" de un valor real, en vez de pintar "0x0".
	if s.engine != nil {
		if ses := s.engine.Session(); ses.ID != 0 {
			out.Session.Live = true
			out.Session.ID = ses.ID
			// En UTC: el motor lo tiene en hora local, y el resto de timestamps del
			// JSON —los que salen de la base— van en Z. Mezclar husos en el mismo
			// contrato es una fuente de confusión gratuita para el frontend.
			arranque := ses.StartedAt.UTC()
			out.Session.StartedAt = &arranque
			if ses.Width != 0 && ses.Height != 0 {
				w, h := ses.Width, ses.Height
				out.Session.Width, out.Session.Height = &w, &h
			}
			if ses.BitrateBPS != 0 {
				b := ses.BitrateBPS
				out.Session.BitrateBPS = &b
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
