package relay

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu      sync.Mutex
	started int
	ended   int
	events  []EngineEvent
	nextID  int64
}

func (f *fakeStore) StartSession(ctx context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	f.nextID++
	return f.nextID, nil
}

func (f *fakeStore) FinishSession(ctx context.Context, id int64, w, h, b int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended++
	return nil
}

func (f *fakeStore) LogEvent(ctx context.Context, e EngineEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func TestEngineRejectsBadKey(t *testing.T) {
	st := &fakeStore{}
	e := NewEngine(EngineConfig{Hub: NewHub(nil), Store: st})
	bad := errors.New("clave incorrecta")
	e.SetValidator(func(app, key string) error {
		if app == "live" && key == "buena" {
			return nil
		}
		return bad
	})

	if err := e.OnPublishStart("live", "mala"); !errors.Is(err, bad) {
		t.Fatalf("OnPublishStart con clave mala = %v, quería el error del validador", err)
	}
	st.mu.Lock()
	started := st.started
	st.mu.Unlock()
	if started != 0 {
		t.Error("una clave rechazada no debe abrir sesión")
	}
}

func TestEngineOpensAndClosesSession(t *testing.T) {
	st := &fakeStore{}
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: st})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}
	if e.SessionID() == 0 {
		t.Error("SessionID = 0 tras aceptar al publisher")
	}
	e.OnPublishEnd()

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.started != 1 || st.ended != 1 {
		t.Errorf("sesiones: abiertas=%d cerradas=%d, quería 1 y 1", st.started, st.ended)
	}
}

func TestEngineForwardsMessagesToHub(t *testing.T) {
	st := &fakeStore{}
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: st})
	e.SetValidator(func(string, string) error { return nil })
	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}

	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), h.Preamble())
	h.Add(s)
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	e.OnMessage(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	e.OnMessage(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	e.OnMessage(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})
	e.OnMessage(videoKey(1000))

	waitFor(t, func() bool { return len(pub.snapshot()) >= 4 }, "el mensaje llegó al sink")
}

// El preámbulo de una transmisión no debe sobrevivir a la siguiente.
func TestEngineResetsPreambleBetweenSessions(t *testing.T) {
	st := &fakeStore{}
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: st})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}
	e.OnMessage(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	e.OnPublishEnd()

	meta, _, _ := h.Preamble().Snapshot()
	if meta != nil {
		t.Error("el preámbulo debe vaciarse al terminar la sesión")
	}
}
func TestEngineWaitIdleReturnsImmediatelyWithNoSession(t *testing.T) {
	e := NewEngine(EngineConfig{Hub: NewHub(nil), Store: &fakeStore{}})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	if err := e.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle sin sesión = %v, quería nil", err)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("WaitIdle sin sesión tardó %v: debería retornar de inmediato", d)
	}
}

func TestEngineWaitIdleWaitsForSessionToClose(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: &fakeStore{}})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}

	// La sesión se cierra desde otra goroutine, como haría la de go-rtmp al cerrarse
	// el socket.
	go func() {
		time.Sleep(150 * time.Millisecond)
		e.OnPublishEnd()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	if err := e.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle = %v, quería nil", err)
	}
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Errorf("WaitIdle retornó en %v: no esperó a que la sesión cerrara", d)
	}
	if e.SessionID() != 0 {
		t.Error("WaitIdle retornó con una sesión todavía abierta")
	}
}

func TestEngineWaitIdleRespectsContext(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: &fakeStore{}})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}
	// La sesión NO se cierra: WaitIdle debe rendirse al vencer el contexto.

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := e.WaitIdle(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitIdle = %v, quería DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("WaitIdle tardó %v en rendirse", elapsed)
	}
}
