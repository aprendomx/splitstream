package relay

import (
	"sync"
	"time"
)

const (
	// bitrateWindow es la ventana de la media móvil del bitrate (spec §6.6).
	bitrateWindow = 5 * time.Second

	// degradedWindow es cuánto se mantiene la marca de degradado tras el último
	// descarte (spec §6.4).
	degradedWindow = 10 * time.Second
)

// Metrics es la instantánea pública del estado de un destino. La fase 4 la serializa y la
// empuja por WebSocket.
type Metrics struct {
	State          string
	Degraded       bool
	BytesSent      uint64
	BitrateBPS     uint64
	DroppedFrames  uint64
	Uptime         time.Duration
	Reconnections  uint64
	LastError      string
	QueuedBytes    int
	QueuedMessages int
}

// sample es una medición de bytes enviados en un instante.
type sample struct {
	at    time.Time
	bytes int
}

// metrics acumula las medidas de un destino.
//
// Es seguro para uso concurrente: lo escribe la goroutine del sink y lo lee quien pida un
// snapshot, que en la fase 4 será el bucle del WebSocket.
type metrics struct {
	mu sync.Mutex

	now func() time.Time

	bytesSent     uint64
	reconnections uint64
	connectedAt   time.Time
	everConnected bool
	lastDrop      time.Time
	lastErr       string
	window        []sample
}

func newMetrics(now func() time.Time) *metrics {
	if now == nil {
		now = time.Now
	}
	return &metrics{now: now}
}

// connected marca el inicio de una conexión. A partir de la segunda cuenta como
// reconexión.
func (m *metrics) connected() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.everConnected {
		m.reconnections++
	}
	m.everConnected = true
	m.connectedAt = m.now()
}

// disconnected marca que la conexión se perdió: el uptime vuelve a cero.
func (m *metrics) disconnected() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectedAt = time.Time{}
}

// sent registra n bytes enviados.
func (m *metrics) sent(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.bytesSent += uint64(n)
	m.window = append(m.window, sample{at: m.now(), bytes: n})
	m.pruneLocked()
}

// markDegraded registra que hubo un descarte ahora.
func (m *metrics) markDegraded() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastDrop = m.now()
}

// setError guarda el último error. Un nil lo limpia.
func (m *metrics) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.lastErr = ""
		return
	}
	m.lastErr = err.Error()
}

// pruneLocked descarta las muestras que salieron de la ventana. Hay que tener el mutex.
func (m *metrics) pruneLocked() {
	cutoff := m.now().Add(-bitrateWindow)
	i := 0
	for i < len(m.window) && m.window[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		m.window = append(m.window[:0], m.window[i:]...)
	}
}

// snapshot compone la instantánea. Los datos que no vive aquí —el estado, los descartes y
// la ocupación de la cola— los aporta quien llama, que es el sink.
func (m *metrics) snapshot(state State, dropped uint64, queuedMsgs, queuedBytes int) Metrics {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneLocked()

	var total int
	for _, s := range m.window {
		total += s.bytes
	}
	// El divisor es siempre la ventana completa, no el tiempo entre la primera y la
	// última muestra: si no, un único envío daría un bitrate enorme.
	bitrate := uint64(float64(total*8) / bitrateWindow.Seconds())

	var uptime time.Duration
	if !m.connectedAt.IsZero() {
		uptime = m.now().Sub(m.connectedAt)
	}

	degraded := !m.lastDrop.IsZero() && m.now().Sub(m.lastDrop) < degradedWindow

	return Metrics{
		State:          state.String(),
		Degraded:       degraded,
		BytesSent:      m.bytesSent,
		BitrateBPS:     bitrate,
		DroppedFrames:  dropped,
		Uptime:         uptime,
		Reconnections:  m.reconnections,
		LastError:      m.lastErr,
		QueuedBytes:    queuedBytes,
		QueuedMessages: queuedMsgs,
	}
}
