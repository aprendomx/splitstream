package relay

import (
	"context"
	"sync"
	"testing"
	"time"
)

// flakyPublisher falla las primeras n conexiones y luego funciona.
type flakyPublisher struct {
	mu        sync.Mutex
	failFirst int
	attempts  int
	inner     *fakePublisher
}

func (f *flakyPublisher) Connect(ctx context.Context) error {
	f.mu.Lock()
	f.attempts++
	fail := f.attempts <= f.failFirst
	f.mu.Unlock()
	if fail {
		return errFakeWrite
	}
	return f.inner.Connect(ctx)
}

func (f *flakyPublisher) WriteMeta(ts uint32, p []byte) error  { return f.inner.WriteMeta(ts, p) }
func (f *flakyPublisher) WriteAudio(ts uint32, p []byte) error { return f.inner.WriteAudio(ts, p) }
func (f *flakyPublisher) WriteVideo(ts uint32, p []byte) error { return f.inner.WriteVideo(ts, p) }
func (f *flakyPublisher) Close() error                         { return f.inner.Close() }

func (f *flakyPublisher) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// Un destino que falla al conectar debe reintentar, no rendirse.
func TestSinkReconnectsAfterConnectFailure(t *testing.T) {
	inner := &fakePublisher{}
	pub := &flakyPublisher{failFirst: 2, inner: inner}

	s := NewSink(SinkConfig{
		ID: 1, Name: "X", Pub: pub,
		NewPub: func() (Publisher, error) { return pub, nil },
	})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	// El backoff arranca en 1 s, así que dos fallos son ~3 s.
	waitForDur(t, 20*time.Second, func() bool { return s.State() == StateLive }, "llegó a live tras reintentar")

	if got := pub.attemptCount(); got < 3 {
		t.Errorf("se intentó conectar %d veces, quería al menos 3", got)
	}
	// Ojo: Reconnections sigue en 0. La primera conexión que tiene éxito no es una
	// reconexión, por muchos intentos fallidos que la precedan. El contador se verifica en
	// TestSinkResendsPreambleAfterReconnect, donde sí se cae una conexión ya establecida.
}

// Tras reconectar, el preámbulo se reenvía y el timeline arranca de nuevo en 0.
func TestSinkResendsPreambleAfterReconnect(t *testing.T) {
	var mu sync.Mutex
	var pubs []*fakePublisher

	s := NewSink(SinkConfig{
		ID: 1, Name: "X",
		NewPub: func() (Publisher, error) {
			p := &fakePublisher{}
			mu.Lock()
			pubs = append(pubs, p)
			mu.Unlock()
			return p, nil
		},
	})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	waitFor(t, func() bool { return s.State() == StateLive }, "primera conexión")

	s.Enqueue(videoKey(500000))
	s.Enqueue(videoInter(500033))
	mu.Lock()
	first := pubs[0]
	mu.Unlock()
	waitFor(t, func() bool { return len(first.snapshot()) >= 5 }, "la primera conexión escribió")

	// Forzar el fallo de escritura para disparar la reconexión.
	first.mu.Lock()
	first.writeErr = errFakeWrite
	first.mu.Unlock()
	s.Enqueue(videoInter(500066))

	waitForDur(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(pubs) >= 2
	}, "se creó un publisher nuevo")
	waitForDur(t, 20*time.Second, func() bool { return s.State() == StateLive }, "volvió a live")

	mu.Lock()
	second := pubs[1]
	mu.Unlock()

	// Timestamps altos de nuevo: la base debe reanclarse, no continuar la anterior.
	s.Enqueue(videoKey(900000))
	s.Enqueue(videoInter(900033))
	waitForDur(t, 10*time.Second, func() bool { return len(second.snapshot()) >= 5 }, "la segunda conexión escribió")

	got := second.snapshot()
	if got[0].Kind != KindMeta || got[1].Kind != KindVideo || got[2].Kind != KindAudio {
		t.Errorf("la reconexión no reenvió el preámbulo: %v %v %v", got[0].Kind, got[1].Kind, got[2].Kind)
	}
	if got[3].TS != 0 {
		t.Errorf("el primer frame tras reconectar salió con ts=%d, quería 0", got[3].TS)
	}
	if got[4].TS != 33 {
		t.Errorf("el segundo frame salió con ts=%d, quería 33", got[4].TS)
	}
	// Aquí sí: se cayó una conexión que estaba establecida.
	if m := s.Metrics(); m.Reconnections == 0 {
		t.Error("el contador de reconexiones no subió tras caerse una conexión viva")
	}
}

// waitForDur es waitFor con un plazo propio, para las esperas que dependen del backoff.
func waitForDur(t *testing.T, limit time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tiempo agotado esperando: %s", msg)
}

// Stop debe cortar la espera del backoff, no esperar a que venza.
func TestSinkStopInterruptsBackoff(t *testing.T) {
	pub := &flakyPublisher{failFirst: 100, inner: &fakePublisher{}}
	s := NewSink(SinkConfig{
		ID: 1, Name: "X", Pub: pub,
		NewPub: func() (Publisher, error) { return pub, nil },
	})
	s.Start(context.Background(), preambleWith())

	waitForDur(t, 5*time.Second, func() bool { return s.State() == StateReconnecting }, "entró en reconnecting")

	start := time.Now()
	s.Stop()
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Stop tardó %v: debería cortar la espera del backoff", d)
	}
}
