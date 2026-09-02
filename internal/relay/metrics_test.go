package relay

import (
	"errors"
	"testing"
	"time"
)

// fakeClock permite avanzar el tiempo sin dormir.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestMetricsCountsBytes(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)
	m.connected()

	m.sent(100)
	m.sent(250)

	got := m.snapshot(StateLive, 0, 0, 0)
	if got.BytesSent != 350 {
		t.Errorf("BytesSent = %d, quería 350", got.BytesSent)
	}
}

// El bitrate es una media móvil de 5 s (spec §6.6).
func TestMetricsBitrateOverFiveSecondWindow(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)
	m.connected()

	// 125 000 bytes por segundo durante los 5 s de la ventana = 1 Mbps.
	// Han de ser 5 muestras: el divisor es la ventana completa, no el tiempo entre la
	// primera y la última. Con 4 saldrían 800 kbps, que también sería correcto pero
	// confundiría a quien lea el test.
	for i := 0; i < 5; i++ {
		m.sent(125_000)
		c.advance(time.Second)
	}

	got := m.snapshot(StateLive, 0, 0, 0)
	if got.BitrateBPS < 900_000 || got.BitrateBPS > 1_100_000 {
		t.Errorf("BitrateBPS = %d, quería ~1000000", got.BitrateBPS)
	}
}

// Lo que sale de la ventana deja de contar.
func TestMetricsBitrateDropsWhenIdle(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)
	m.connected()

	m.sent(1_000_000)
	c.advance(10 * time.Second) // muy fuera de la ventana de 5 s

	got := m.snapshot(StateLive, 0, 0, 0)
	if got.BitrateBPS != 0 {
		t.Errorf("BitrateBPS = %d tras 10 s sin enviar, quería 0", got.BitrateBPS)
	}
}

func TestMetricsUptimeAndReconnections(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)

	m.connected()
	c.advance(30 * time.Second)
	if got := m.snapshot(StateLive, 0, 0, 0); got.Uptime != 30*time.Second {
		t.Errorf("Uptime = %v, quería 30s", got.Uptime)
	}
	if got := m.snapshot(StateLive, 0, 0, 0); got.Reconnections != 0 {
		t.Errorf("la primera conexión no es una reconexión")
	}

	m.disconnected()
	if got := m.snapshot(StateReconnecting, 0, 0, 0); got.Uptime != 0 {
		t.Errorf("Uptime = %v estando desconectado, quería 0", got.Uptime)
	}

	m.connected()
	c.advance(5 * time.Second)
	got := m.snapshot(StateLive, 0, 0, 0)
	if got.Reconnections != 1 {
		t.Errorf("Reconnections = %d, quería 1", got.Reconnections)
	}
	if got.Uptime != 5*time.Second {
		t.Errorf("Uptime = %v, quería 5s: cuenta desde la reconexión", got.Uptime)
	}
}

// degraded se apaga solo si no hay descartes recientes (spec §6.4).
func TestMetricsDegradedExpires(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)
	m.connected()

	m.markDegraded()
	if !m.snapshot(StateLive, 1, 0, 0).Degraded {
		t.Error("debería estar degradado justo tras un descarte")
	}

	c.advance(5 * time.Second)
	if !m.snapshot(StateLive, 1, 0, 0).Degraded {
		t.Error("a los 5 s todavía debería estar degradado")
	}

	c.advance(6 * time.Second) // 11 s en total
	if m.snapshot(StateLive, 1, 0, 0).Degraded {
		t.Error("pasados 10 s sin descartes debería dejar de estar degradado")
	}
}

func TestMetricsLastError(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)

	if got := m.snapshot(StateIdle, 0, 0, 0); got.LastError != "" {
		t.Errorf("LastError inicial = %q, quería vacío", got.LastError)
	}

	m.setError(errors.New("connection refused"))
	if got := m.snapshot(StateError, 0, 0, 0); got.LastError != "connection refused" {
		t.Errorf("LastError = %q", got.LastError)
	}
}

func TestMetricsSnapshotCarriesQueueAndState(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)

	got := m.snapshot(StateReconnecting, 42, 7, 1234)
	if got.State != "reconnecting" {
		t.Errorf("State = %q", got.State)
	}
	if got.DroppedFrames != 42 {
		t.Errorf("DroppedFrames = %d", got.DroppedFrames)
	}
	if got.QueuedMessages != 7 || got.QueuedBytes != 1234 {
		t.Errorf("cola = %d msgs / %d bytes", got.QueuedMessages, got.QueuedBytes)
	}
}
