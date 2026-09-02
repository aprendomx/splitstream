package relay

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// waitFor, videoKey y videoInter viven en sink_test.go.

type fakeStore struct {
	mu      sync.Mutex
	started int
	ended   int
	events  []EngineEvent
	nextID  int64

	lastWidth   int
	lastHeight  int
	lastBitrate int
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
	f.lastWidth, f.lastHeight, f.lastBitrate = w, h, b
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

// Dos sesiones seguidas deben usar sinks distintos, con su propio timebase: si el sink
// sobreviviera, la segunda transmisión saldría con el timeline de la primera y su
// sequence header nunca llegaría al destino.
func TestEngineRestartsSinksBetweenSessions(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: &fakeStore{}, BaseContext: context.Background()})
	e.SetValidator(func(string, string) error { return nil })

	var pubs []*fakePublisher
	var mu sync.Mutex
	e.SetSinkProvider(func() ([]*Sink, error) {
		p := &fakePublisher{}
		mu.Lock()
		pubs = append(pubs, p)
		mu.Unlock()
		return []*Sink{NewSink(SinkConfig{ID: 1, Name: "dest", Pub: p})}, nil
	})

	publish := func(baseTS uint32) {
		if err := e.OnPublishStart("live", "ok"); err != nil {
			t.Fatalf("OnPublishStart: %v", err)
		}
		// El preámbulo lleva el timestamp del momento en que se manda, como haría un
		// encoder real, y no 0: con la cola de la fase 3 (Task 1), que acota por span de
		// timestamps encolados (spec §3.4), un preámbulo en ts=0 seguido del keyframe en
		// baseTS=600000 parece 600 s de cola acumulada y el keyframe se descarta antes de
		// que el sink llegue a verlo.
		e.OnMessage(&Message{Kind: KindMeta, Timestamp: baseTS, Payload: []byte{0xFF}})
		e.OnMessage(&Message{Kind: KindVideo, Timestamp: baseTS, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
		e.OnMessage(&Message{Kind: KindAudio, Timestamp: baseTS, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})
		e.OnMessage(videoKey(baseTS))
		e.OnMessage(videoInter(baseTS + 33))
	}

	// Primera sesión con timestamps altos, como una transmisión que lleva rato.
	publish(600000)
	mu.Lock()
	first := pubs[0]
	mu.Unlock()
	waitFor(t, func() bool { return len(first.snapshot()) >= 5 }, "la sesión 1 escribió")
	e.OnPublishEnd()

	// Segunda sesión: timestamps que arrancan de nuevo, como hace OBS.
	publish(0)
	mu.Lock()
	if len(pubs) != 2 {
		mu.Unlock()
		t.Fatalf("se crearon %d publishers, quería 2: el sink no se reinició entre sesiones", len(pubs))
	}
	second := pubs[1]
	mu.Unlock()

	waitFor(t, func() bool { return len(second.snapshot()) >= 5 }, "la sesión 2 escribió")

	got := second.snapshot()
	// El preámbulo debe reenviarse entero en la sesión nueva.
	if got[0].Kind != KindMeta || got[1].Kind != KindVideo || got[2].Kind != KindAudio {
		t.Errorf("la sesión 2 no reenvió el preámbulo: %v %v %v", got[0].Kind, got[1].Kind, got[2].Kind)
	}
	// Y el timeline debe arrancar en 0, no continuar el de la sesión anterior.
	if got[3].TS != 0 {
		t.Errorf("el primer frame de la sesión 2 salió con ts=%d, quería 0", got[3].TS)
	}
	e.OnPublishEnd()
}

func TestEngineRejectsSecondPublisher(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()
	st := &fakeStore{}
	e := NewEngine(EngineConfig{Hub: h, Store: st, BaseContext: context.Background()})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("primer publisher: %v", err)
	}
	if err := e.OnPublishStart("live", "ok"); !errors.Is(err, ErrSessionInProgress) {
		t.Fatalf("segundo publisher = %v, quería ErrSessionInProgress", err)
	}

	st.mu.Lock()
	started := st.started
	st.mu.Unlock()
	if started != 1 {
		t.Errorf("se abrieron %d sesiones, quería 1", started)
	}

	// Tras cerrar, se acepta uno nuevo.
	e.OnPublishEnd()
	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Errorf("tras cerrar la sesión debería aceptarse otro publisher: %v", err)
	}
	e.OnPublishEnd()
}

// La resolución sale del SPS del AVC sequence header, no del onMetaData (spec §3.8).
func TestEngineRecordsResolutionFromSPS(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()
	st := &fakeStore{}
	e := NewEngine(EngineConfig{Hub: h, Store: st, BaseContext: context.Background()})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}

	// AVC sequence header real de 640x360.
	seq := mustAVCSeqHeader()
	e.OnMessage(&Message{Kind: KindVideo, Payload: seq, IsSeqHeader: true, IsKeyframe: true})
	e.OnMessage(&Message{Kind: KindVideo, Timestamp: 0, Payload: make([]byte, 1000), IsKeyframe: true})
	e.OnMessage(&Message{Kind: KindVideo, Timestamp: 1000, Payload: make([]byte, 1000)})
	e.OnPublishEnd()

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.lastWidth != 640 || st.lastHeight != 360 {
		t.Errorf("resolución = %dx%d, quería 640x360", st.lastWidth, st.lastHeight)
	}
	if st.lastBitrate <= 0 {
		t.Errorf("bitrate = %d, quería un valor medido positivo", st.lastBitrate)
	}
}

// Sin sequence header no se inventa una resolución: se cierra con ceros.
func TestEngineRecordsZeroResolutionWithoutSPS(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()
	st := &fakeStore{}
	e := NewEngine(EngineConfig{Hub: h, Store: st, BaseContext: context.Background()})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}
	e.OnMessage(&Message{Kind: KindVideo, Timestamp: 0, Payload: []byte{0x17, 0x01}, IsKeyframe: true})
	e.OnPublishEnd()

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.lastWidth != 0 || st.lastHeight != 0 {
		t.Errorf("resolución = %dx%d, quería 0x0 sin sequence header", st.lastWidth, st.lastHeight)
	}
}

// mustAVCSeqHeader devuelve un AVC sequence header real de 640x360.
func mustAVCSeqHeader() []byte {
	sps := []byte{
		0x67, 0x64, 0x00, 0x1e, 0xac, 0xd9, 0x40, 0xa0, 0x2f, 0xf9, 0x61, 0x00,
		0x00, 0x03, 0x00, 0x01, 0x00, 0x00, 0x03, 0x00, 0x3c, 0x8f, 0x16, 0x2d, 0x96,
	}
	out := []byte{0x17, 0x00, 0, 0, 0, 0x01, sps[1], sps[2], sps[3], 0xFF, 0xE1}
	out = append(out, byte(len(sps)>>8), byte(len(sps)))
	return append(out, sps...)
}
