package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/aprendomx/splitstream/internal/probe"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// probeTimeout acota la sonda entera: dial (15 s en rtmpio) + gracia (3 s) + margen.
const probeTimeout = 30 * time.Second

type probeDTO struct {
	Outcome   string `json:"outcome"`
	Stage     string `json:"stage"`
	ElapsedMS int64  `json:"elapsed_ms"`
	// Message es para personas y NUNCA lleva la URL ni la clave: se compone aquí a partir
	// del veredicto y la etapa, no del texto del error.
	Message string `json:"message"`
}

func newProbeDTO(r probe.Result) probeDTO {
	return probeDTO{
		Outcome:   r.Outcome.String(),
		Stage:     r.Stage,
		ElapsedMS: r.Elapsed.Milliseconds(),
		Message:   probeMessage(r),
	}
}

// probeMessage traduce el veredicto a lo que el usuario puede hacer. Es el mismo criterio
// que diagnostico.js en el panel: el estado, no la traza.
func probeMessage(r probe.Result) string {
	switch r.Outcome {
	case probe.Plausible:
		return "La plataforma aceptó la conexión y la mantuvo abierta. La configuración es " +
			"plausible; solo emitir de verdad confirma la clave."
	case probe.ClosedEarly:
		return "La plataforma aceptó la conexión y la cerró enseguida. Casi siempre es la " +
			"clave, o una emisión que ya no está abierta en la plataforma."
	case probe.Rejected:
		if r.Stage == "url" {
			return "La URL del destino no vale: tiene que empezar por rtmp:// o rtmps:// y " +
				"llevar servidor y aplicación."
		}
		return fmt.Sprintf("La plataforma rechazó el handshake en «%s». Revisa la URL.", r.Stage)
	default:
		switch r.Stage {
		case "cancelled":
			return "La prueba se canceló antes de terminar. Vuelve a intentarlo."
		case "dns":
			return "No se resuelve el nombre del servidor. Revisa la URL."
		case "tls":
			return "El certificado del servidor no es válido. Revisa que la URL sea la de la " +
				"plataforma y no la de un intermediario."
		default:
			return "No se pudo conectar con el servidor. Revisa la URL, el puerto y tu red."
		}
	}
}

// testSkippedDTO es la respuesta de «probar destino» cuando no hay nada que probar.
type testSkippedDTO struct {
	Skipped bool   `json:"skipped"`
	Message string `json:"message"`
}

// handleTestDestination sondea un destino sin emitir (spec v0.8 §3).
func (s *Server) handleTestDestination(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	d, err := s.db.DestinationByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	// Con la clave traída por API no hay nada que probar: no hay clave mal pegada posible,
	// y abrir una conexión de prueba contra la emisión recién creada solo sirve para
	// gastar cuota. Se contesta ANTES de cualquier sonda.
	if d.KeyFromAPI {
		writeJSON(w, http.StatusOK, testSkippedDTO{Skipped: true,
			Message: traducir(idiomaDe(w), "la clave vino por API: no hay clave inválida que probar")})
		return
	}
	if s.tester == nil {
		writeError(w, http.StatusConflict, codeConflict, "probar destinos no está disponible en este arranque")
		return
	}
	// Con el destino emitiendo, una segunda publicación con la misma clave haría que la
	// plataforma expulsara a la que va en vivo. Apagado o suspendido sí se puede probar.
	if s.liveSession() {
		if m, ok := s.engine.Snapshot()[id]; ok && m.State == relay.StateLive.String() {
			writeError(w, http.StatusConflict, codeConflict,
				"el destino está emitiendo ahora mismo: probarlo abriría una segunda publicación "+
					"con la misma clave y la plataforma cortaría la que va en vivo")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	res, err := s.tester.Test(ctx, *d)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	dto := newProbeDTO(res)

	level := store.LevelWarn
	if res.Outcome == probe.Plausible {
		level = store.LevelInfo
	}
	// context.Background() y no el de la petición: la sonda dura hasta 30 s y quien la
	// lanzó puede haberse ido; la constancia de que se probó un destino no se pierde por eso.
	if _, err := s.db.LogEvent(context.Background(), store.Event{
		DestinationID: &id, Level: level, Kind: "destination_tested",
		Message: "se probó el destino: " + dto.Message,
	}); err != nil {
		s.logger.Error("no se pudo registrar la prueba del destino", "err", err)
	}
	// Se traduce DESPUÉS de registrar el evento: el diagnóstico que lee quien pide la
	// prueba va en su idioma, y el que queda en la base se queda en español, que es el
	// idioma del registro (spec v0.13 §3.3).
	dto.Message = traducir(idiomaDe(w), dto.Message)
	writeJSON(w, http.StatusOK, dto)
}
