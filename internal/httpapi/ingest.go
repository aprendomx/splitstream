package httpapi

import (
	"net"
	"net/http"

	"github.com/aprendomx/splitstream/internal/store"
)

// ingestURL compone la URL que el usuario pega en OBS.
//
// El host sale de la petición y no de la configuración: el panel se alcanza por algún
// nombre —una IP, un dominio, tailscale…— y la ingesta está en esa misma máquina, así que
// lo que el usuario tiene delante es lo que le va a funcionar. La configuración solo
// aporta el puerto.
//
// La clave NO va dentro de la URL: OBS la pide aparte, y meterla en la URL es lo que hace
// que la gente la pegue en sitios donde no debería estar.
func (s *Server) ingestURL(r *http.Request, app string) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		host = "localhost"
	}

	puerto := s.rtmpPort
	if puerto == "" {
		puerto = "1935"
	}
	return "rtmp://" + net.JoinHostPort(host, puerto) + "/" + app
}

func (s *Server) handleGetIngest(w http.ResponseWriter, r *http.Request) {
	settings, err := s.db.Settings(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, ingestDTO{
		URL:     s.ingestURL(r, settings.IngestApp),
		App:     settings.IngestApp,
		KeyMask: settings.IngestKeyMask,
	})
}

type rotateKeyRequest struct {
	// DisconnectNow por defecto false: rotar preventivamente no debe tumbar una
	// transmisión en curso sin haberlo pedido.
	DisconnectNow bool `json:"disconnect_now"`
}

type rotateKeyResponse struct {
	Key     string `json:"key"`
	KeyMask string `json:"key_mask"`
}

func (s *Server) handleRotateIngestKey(w http.ResponseWriter, r *http.Request) {
	var in rotateKeyRequest
	// Un cuerpo vacío es válido y significa disconnect_now:false. Solo un cuerpo presente
	// y mal formado es un 400.
	if r.ContentLength > 0 && !decodeBody(w, r, &in) {
		return
	}

	key, err := s.db.RotateIngestKey(r.Context(), s.cipher)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	// El evento dice QUE pasó, no qué clave quedó: el log se enseña entero en el panel
	// (spec §8).
	if _, err := s.db.LogEvent(r.Context(), store.Event{
		Level:   store.LevelWarn,
		Kind:    "ingest_key_rotated",
		Message: "se rotó la clave de ingesta",
	}); err != nil {
		s.logger.Error("no se pudo registrar la rotación de la clave", "err", err)
	}

	if in.DisconnectNow && s.ingest != nil {
		n := s.ingest.DisconnectPublisher()
		s.logger.Info("publicación cortada por rotación de clave", "conexiones", n)
		if n > 0 {
			if _, err := s.db.LogEvent(r.Context(), store.Event{
				Level:   store.LevelWarn,
				Kind:    "ingest_disconnected",
				Message: "se cortó la publicación en curso al rotar la clave",
			}); err != nil {
				s.logger.Error("no se pudo registrar el corte", "err", err)
			}
		}
	}

	// La clave nueva EN CLARO, y esta es la única vez que sale: si no se devolviera aquí,
	// rotar sería inútil porque el usuario no tendría qué pegar en OBS. Es la segunda y
	// última excepción a "las claves no salen", junto al revelado de un destino.
	writeJSON(w, http.StatusOK, rotateKeyResponse{Key: key.Reveal(), KeyMask: key.Mask()})
}
