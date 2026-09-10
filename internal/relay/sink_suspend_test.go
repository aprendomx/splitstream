package relay

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Tras N conexiones fallidas seguidas el sink deja de reintentar y lo dice. Antes de la
// v0.8 reintentaba para siempre (spec base §6.5, enmendado): con una clave mal pegada eso
// era un bucle silencioso, y contra Facebook cada intento cuenta como emisión activa.
func TestSinkSuspendsAfterRepeatedConnectFailures(t *testing.T) {
	pub := &flakyPublisher{failFirst: 1000, inner: &fakePublisher{}}

	var mu sync.Mutex
	var eventos []EngineEvent
	s := NewSink(SinkConfig{
		ID: 1, Name: "X", Pub: pub,
		SuspendAfterAttempts: 3,
		OnEvent: func(e EngineEvent) {
			mu.Lock()
			eventos = append(eventos, e)
			mu.Unlock()
		},
	})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	// Tres intentos: el primero al instante, 1 s y 2 s de backoff después (±20 %).
	waitForDur(t, 15*time.Second, func() bool { return s.State() == StateSuspended }, "se suspendió")

	// Ventana en la que un cuarto intento se vería: el siguiente backoff sería de ~4 s.
	time.Sleep(5 * time.Second)
	if got := pub.attemptCount(); got != 3 {
		t.Errorf("intentos = %d, quería exactamente 3: un sink suspendido no reintenta", got)
	}
	if m := s.Metrics(); m.State != "suspended" {
		t.Errorf("Metrics().State = %q, quería suspended", m.State)
	}

	mu.Lock()
	defer mu.Unlock()
	var visto bool
	for _, e := range eventos {
		if e.Kind == "destination_suspended" && e.Level == "error" {
			visto = true
		}
	}
	if !visto {
		t.Errorf("no se emitió destination_suspended; eventos: %+v", eventos)
	}
}

// Un destino que conecta, transmite poco y corta —lo que hizo Facebook— también se
// suspende, tras M sesiones cortas seguidas.
func TestSinkSuspendsAfterRepeatedFlaps(t *testing.T) {
	pub := &flappingPublisher{permitidas: 6}
	s := NewSink(SinkConfig{ID: 1, Name: "aleteante", Pub: pub, SuspendAfterFlaps: 2})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	fin := make(chan struct{})
	defer close(fin)
	go func() {
		for {
			select {
			case <-fin:
				return
			default:
			}
			s.Enqueue(videoKey(1000))
			time.Sleep(2 * time.Millisecond)
		}
	}()

	waitForDur(t, 20*time.Second, func() bool { return s.State() == StateSuspended }, "se suspendió por aleteo")
	if got := pub.conexiones(); got != 2 {
		t.Errorf("conexiones = %d, quería 2", got)
	}
}

// Stop tiene que despertar a un sink suspendido: es el camino de Hub.Add al reemplazarlo
// tras «Reintentar», y del apagado.
func TestSinkStopWakesASuspendedSink(t *testing.T) {
	pub := &flakyPublisher{failFirst: 1000, inner: &fakePublisher{}}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub, SuspendAfterAttempts: 1})
	s.Start(context.Background(), preambleWith())

	waitForDur(t, 5*time.Second, func() bool { return s.State() == StateSuspended }, "se suspendió")

	inicio := time.Now()
	s.Stop()
	if d := time.Since(inicio); d > 2*time.Second {
		t.Errorf("Stop tardó %v sobre un sink suspendido", d)
	}
	if s.State() != StateIdle {
		t.Errorf("estado tras Stop = %v, quería idle", s.State())
	}
}

// La cola de un sink suspendido sigue recibiendo del hub. Su política de descarte se
// aplica en push, así que no crece sin límite.
func TestSuspendedSinkQueueDoesNotGrow(t *testing.T) {
	pub := &flakyPublisher{failFirst: 1000, inner: &fakePublisher{}}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub, SuspendAfterAttempts: 1})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()
	waitForDur(t, 5*time.Second, func() bool { return s.State() == StateSuspended }, "se suspendió")

	for i := 0; i < 3*DefaultMaxItems; i++ {
		s.Enqueue(audioRaw(uint32(i)))
	}
	if m := s.Metrics(); m.QueuedMessages > DefaultMaxItems {
		t.Errorf("la cola de un sink suspendido tiene %d mensajes, más que el tope %d",
			m.QueuedMessages, DefaultMaxItems)
	}
}

func TestStateStringSuspended(t *testing.T) {
	if got := StateSuspended.String(); got != "suspended" {
		t.Errorf("String() = %q", got)
	}
}
