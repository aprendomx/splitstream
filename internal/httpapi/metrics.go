package httpapi

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
)

// Metric es una muestra en el formato de exposición de Prometheus. La exposición se escribe
// a mano —nombre, HELP, TYPE y valor, que es todo lo que el formato pide— porque el spec
// base §5 acota las dependencias y el cliente oficial de Prometheus arrastra bastante más
// de lo que este puñado de contadores necesita.
type Metric struct {
	Name   string
	Help   string
	Type   string // "gauge" | "counter"
	Labels map[string]string
	Value  float64
}

// ExtraMetrics permite a otros componentes —el bus de eventos, los webhooks— aportar sus
// contadores sin que este paquete los importe.
type ExtraMetrics func() []Metric

// requireSessionOrToken protege /metrics: cookie de sesión, o Bearer con el token
// configurado. Sin token configurado, solo cookie. La comparación es en tiempo constante.
func (s *Server) requireSessionOrToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			dado := strings.TrimPrefix(auth, "Bearer ")
			if s.metricsToken != "" && subtle.ConstantTimeCompare([]byte(dado), []byte(s.metricsToken)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "token de métricas inválido")
			return
		}
		s.requireSession(next).ServeHTTP(w, r)
	})
}

// estados son las series de destination_state: una por estado, 1 en el activo. Así en
// PromQL se pregunta `splitstream_destination_state{state="live"} == 1` sin parsear texto.
var estados = []relay.State{
	relay.StateIdle, relay.StateConnecting, relay.StateLive,
	relay.StateReconnecting, relay.StateError, relay.StateSuspended,
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var ms []Metric
	add := func(name, help, typ string, labels map[string]string, v float64) {
		ms = append(ms, Metric{Name: name, Help: help, Type: typ, Labels: labels, Value: v})
	}

	add("splitstream_build_info", "Versión del binario.", "gauge", map[string]string{"version": s.version}, 1)

	var live float64
	if s.engine != nil {
		if ses := s.engine.Session(); ses.ID != 0 {
			live = 1
			add("splitstream_session_bitrate_bps", "Bitrate medido de la ingesta.", "gauge", nil, float64(ses.BitrateBPS))
			add("splitstream_session_uptime_seconds", "Segundos desde que el publisher conectó.", "gauge", nil,
				time.Since(ses.StartedAt).Seconds())
		}
	}
	add("splitstream_session_live", "1 si hay un publisher emitiendo.", "gauge", nil, live)

	dests, err := s.db.ListDestinations(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	var snap map[int64]relay.Metrics
	if s.engine != nil {
		snap = s.engine.Snapshot()
	}
	for _, d := range dests {
		base := map[string]string{
			"destination": strconv.FormatInt(d.ID, 10), "name": d.Name, "platform": string(d.Platform),
		}
		m, ok := snap[d.ID]
		if !ok {
			m = relay.Metrics{State: relay.StateIdle.String()}
		}
		for _, st := range estados {
			l := clonar(base)
			l["state"] = st.String()
			var v float64
			if m.State == st.String() {
				v = 1
			}
			add("splitstream_destination_state", "Estado del destino: 1 en la serie activa.", "gauge", l, v)
		}
		add("splitstream_destination_degraded", "1 si descartó vídeo en los últimos 10 s.", "gauge", base, b2f(m.Degraded))
		add("splitstream_destination_bytes_sent_total", "Bytes enviados en esta sesión.", "counter", base, float64(m.BytesSent))
		add("splitstream_destination_bitrate_bps", "Bitrate de salida, media móvil de 5 s.", "gauge", base, float64(m.BitrateBPS))
		add("splitstream_destination_dropped_frames_total", "Mensajes descartados por la cola.", "counter", base, float64(m.DroppedFrames))
		add("splitstream_destination_reconnections_total", "Reconexiones en esta sesión.", "counter", base, float64(m.Reconnections))
		add("splitstream_destination_queued_bytes", "Bytes encolados hacia el destino.", "gauge", base, float64(m.QueuedBytes))
	}

	for _, extra := range s.extra {
		ms = append(ms, extra()...)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	escribirMetricas(w, ms)
}

// escribirMetricas agrupa por nombre para escribir HELP y TYPE una sola vez, en el orden
// de primera aparición.
func escribirMetricas(w http.ResponseWriter, ms []Metric) {
	var orden []string
	porNombre := map[string][]Metric{}
	for _, m := range ms {
		if _, visto := porNombre[m.Name]; !visto {
			orden = append(orden, m.Name)
		}
		porNombre[m.Name] = append(porNombre[m.Name], m)
	}
	for _, nombre := range orden {
		grupo := porNombre[nombre]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", nombre, grupo[0].Help, nombre, grupo[0].Type)
		for _, m := range grupo {
			fmt.Fprintf(w, "%s%s %s\n", nombre, etiquetas(m.Labels), strconv.FormatFloat(m.Value, 'f', -1, 64))
		}
	}
}

// etiquetas serializa {a="1",b="2"} con las claves ordenadas y los valores escapados:
// el nombre del destino lo escribe el usuario y puede llevar comillas o saltos de línea.
func etiquetas(l map[string]string) string {
	if len(l) == 0 {
		return ""
	}
	claves := make([]string, 0, len(l))
	for k := range l {
		claves = append(claves, k)
	}
	sort.Strings(claves)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range claves {
		if i > 0 {
			sb.WriteByte(',')
		}
		v := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(l[k])
		sb.WriteString(k + `="` + v + `"`)
	}
	sb.WriteByte('}')
	return sb.String()
}

func clonar(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
