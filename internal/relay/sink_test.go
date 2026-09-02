package relay

import (
	"context"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("tiempo agotado esperando: %s", msg)
}

func videoKey(ts uint32) *Message {
	return &Message{Kind: KindVideo, Timestamp: ts, Payload: []byte{0x17, 0x01}, IsKeyframe: true}
}
func videoInter(ts uint32) *Message {
	return &Message{Kind: KindVideo, Timestamp: ts, Payload: []byte{0x27, 0x01}}
}
func audioRaw(ts uint32) *Message {
	return &Message{Kind: KindAudio, Timestamp: ts, Payload: []byte{0xAF, 0x01}}
}

func preambleWith() *Preamble {
	p := &Preamble{}
	p.Observe(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	p.Observe(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	p.Observe(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})
	return p
}

// Antes del primer keyframe no se envía nada; el preámbulo sale justo antes de él.
func TestSinkWaitsForKeyframeThenSendsPreamble(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "YouTube", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	// Media anterior al keyframe: debe descartarse entera.
	s.Enqueue(videoInter(1000))
	s.Enqueue(audioRaw(1010))
	time.Sleep(50 * time.Millisecond)
	if got := pub.snapshot(); len(got) != 0 {
		t.Fatalf("se envió media antes del keyframe: %+v", got)
	}

	s.Enqueue(videoKey(2000))
	waitFor(t, func() bool { return len(pub.snapshot()) >= 4 }, "preámbulo + keyframe")

	got := pub.snapshot()
	if got[0].Kind != KindMeta {
		t.Errorf("el primer mensaje debe ser el onMetaData, fue %v", got[0].Kind)
	}
	if got[1].Kind != KindVideo || got[1].TS != 0 {
		t.Errorf("el segundo debe ser el AVC sequence header con ts=0, fue %+v", got[1])
	}
	if got[2].Kind != KindAudio || got[2].TS != 0 {
		t.Errorf("el tercero debe ser el AAC sequence header con ts=0, fue %+v", got[2])
	}
	if got[3].Kind != KindVideo || got[3].TS != 0 {
		t.Errorf("el keyframe debe salir con ts=0, fue %+v", got[3])
	}
}

// Tras el keyframe, los timestamps salen rebasados y con base común A/V.
func TestSinkRebasesTimestamps(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	s.Enqueue(videoKey(5000))
	s.Enqueue(audioRaw(5010))
	s.Enqueue(videoInter(5033))
	waitFor(t, func() bool { return len(pub.snapshot()) >= 6 }, "3 mensajes de preámbulo + 3 de media")

	got := pub.snapshot()[3:]
	want := []struct {
		kind Kind
		ts   uint32
	}{{KindVideo, 0}, {KindAudio, 10}, {KindVideo, 33}}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].TS != w.ts {
			t.Errorf("mensaje %d = (%v, %d), quería (%v, %d)", i, got[i].Kind, got[i].TS, w.kind, w.ts)
		}
	}
}

// El audio anterior a la base se descarta en vez de salir negativo.
func TestSinkDropsAudioOlderThanBase(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	s.Enqueue(videoKey(5000))
	s.Enqueue(audioRaw(4990)) // anterior a la base
	s.Enqueue(audioRaw(5020))
	waitFor(t, func() bool { return len(pub.snapshot()) >= 5 }, "preámbulo + keyframe + audio válido")

	for _, m := range pub.snapshot() {
		if m.Kind == KindAudio && m.TS > 1_000_000 {
			t.Fatalf("un timestamp de audio desbordó: %+v", m)
		}
	}
	media := pub.snapshot()[3:]
	if len(media) != 2 {
		t.Fatalf("se enviaron %d mensajes de media, quería 2 (el audio previo se descarta)", len(media))
	}
	if media[1].TS != 20 {
		t.Errorf("el audio válido salió con ts=%d, quería 20", media[1].TS)
	}
}

// Fase 3: un Connect fallido ya no deja el sink en StateError para siempre, sino que pasa
// a StateReconnecting y reintenta con backoff (spec §6.5). Adaptado desde la fase 2, donde
// este test esperaba StateError como estado final.
func TestSinkConnectFailureSetsErrorState(t *testing.T) {
	pub := &fakePublisher{connectErr: errFakeWrite}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	waitFor(t, func() bool { return s.State() == StateReconnecting }, "estado reconnecting")
	if s.LastError() == nil {
		t.Error("LastError = nil tras fallar Connect")
	}
}

// Fase 3: igual que arriba pero para un fallo de escritura sobre una conexión ya viva: la
// conexión se pierde y el sink pasa a reconectar, no se queda en StateError.
func TestSinkWriteFailureSetsErrorState(t *testing.T) {
	pub := &fakePublisher{writeErr: errFakeWrite}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	s.Enqueue(videoKey(1000))
	waitFor(t, func() bool { return s.State() == StateReconnecting }, "estado reconnecting tras fallar el write")
	if s.LastError() == nil {
		t.Error("LastError = nil tras fallar el write")
	}
}

// Un sink lento no debe bloquear a quien encola: se descarta y se cuenta.
func TestSinkEnqueueNeverBlocks(t *testing.T) {
	block := make(chan struct{})
	pub := &fakePublisher{blockWrites: block}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub, Queue: queueConfig{MaxItems: 4}})
	s.Start(context.Background(), preambleWith())
	defer func() { close(block); s.Stop() }()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			s.Enqueue(videoKey(uint32(1000 + i)))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Enqueue se bloqueó: un destino lento no debe frenar al publisher")
	}
	waitFor(t, func() bool { return s.Dropped() > 0 }, "contador de descartes")
}

func TestSinkStopIsIdempotentAndClosesPublisher(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	s.Stop()
	s.Stop() // no debe entrar en pánico ni colgarse

	if s.State() != StateIdle {
		t.Errorf("State = %v tras Stop, quería idle", s.State())
	}
	pub.mu.Lock()
	closes := pub.closes
	pub.mu.Unlock()
	if closes == 0 {
		t.Error("Stop debe cerrar el Publisher")
	}
}

// Fase 3: TestSinkConnectFailureReleasesGoroutine y TestSinkWriteFailureReleasesGoroutine
// de la fase 2 se eliminan aquí. Su premisa era que un Connect o un Write fallidos dejaban
// la goroutine terminando sola, sin Stop, para no dejarla viva para siempre. Eso ya no es
// cierto por diseño (decisión 3 de esta tarea): el sink reintenta indefinidamente en vez de
// rendirse (spec §6.5), así que la goroutine sigue viva hasta que alguien llama a Stop.
// La propiedad que a esos tests les interesaba de verdad —que la goroutine no queda huérfana
// para siempre— la cubre ahora TestSinkStopInterruptsBackoff en
// sink_reconnect_test.go: Stop corta la espera del backoff en vez de esperar a que venza.

// Un sink parado cierra el Publisher, tanto si estaba conectado como si fallaba en bucle.
func TestSinkStopClosesPublisherAfterConnectFailure(t *testing.T) {
	pub := &fakePublisher{connectErr: errFakeWrite}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())

	waitFor(t, func() bool { return s.State() == StateReconnecting }, "estado reconnecting")

	s.Stop()
	if s.LastError() == nil {
		t.Error("LastError se perdió")
	}

	pub.mu.Lock()
	closes := pub.closes
	pub.mu.Unlock()
	if closes == 0 {
		t.Error("el Publisher debe cerrarse aunque Connect falle")
	}
}

// Un destino lento descarta GOPs y se marca degradado, sin bloquear a quien encola.
func TestSinkDegradesUnderBackpressure(t *testing.T) {
	block := make(chan struct{})
	pub := &fakePublisher{blockWrites: block}

	s := NewSink(SinkConfig{
		ID: 1, Name: "X", Pub: pub,
		Queue: queueConfig{MaxBytes: 4096, MaxSpan: 1_000_000},
	})
	s.Start(context.Background(), preambleWith())
	defer func() { close(block); s.Stop() }()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 400; i++ {
			if i%10 == 0 {
				s.Enqueue(&Message{Kind: KindVideo, Timestamp: uint32(i * 33),
					Payload: make([]byte, 256), IsKeyframe: true})
			} else {
				s.Enqueue(&Message{Kind: KindVideo, Timestamp: uint32(i * 33),
					Payload: make([]byte, 256)})
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Enqueue se bloqueó bajo presión")
	}

	waitFor(t, func() bool { return s.Dropped() > 0 }, "hubo descartes")
	waitFor(t, func() bool { return s.Metrics().Degraded }, "se marcó degradado")
	if s.State() != StateLive {
		t.Errorf("State = %v: degraded es un atributo, no un estado (spec §3.7)", s.State())
	}
}

func TestSinkMetricsCountBytes(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	s.Enqueue(videoKey(1000))
	waitFor(t, func() bool { return s.Metrics().BytesSent > 0 }, "se contaron bytes")
}

// Start es idempotente. Sin eso, la segunda goroutine cerraba el mismo canal `done` y se
// llevaba por delante el proceso entero con "close of closed channel".
func TestSinkStartIsIdempotent(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "dos veces", Pub: pub})

	s.Start(context.Background(), preambleWith())
	s.Start(context.Background(), preambleWith()) // no debe entrar en pánico
	defer s.Stop()

	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	// Y sigue habiendo un solo sink: una sola conexión, no dos.
	s.Enqueue(videoKey(1000))
	waitFor(t, func() bool { return len(pub.snapshot()) >= 4 }, "preámbulo + keyframe")

	pub.mu.Lock()
	connects := pub.connects
	pub.mu.Unlock()
	if connects != 1 {
		t.Errorf("connects = %d: la segunda llamada a Start abrió otra conexión", connects)
	}
}

// Y arrancar un sink ya parado no lanza nada: ni goroutine, ni conexión, ni pánico. Sin
// el guardia, Start dejaría una goroutine que nadie espera y que además abre una conexión
// con el destino justo después de haber pedido cerrarlo.
func TestSinkStartAfterStopDoesNothing(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "parado", Pub: pub})

	s.Stop()
	s.Start(context.Background(), preambleWith())
	s.Stop() // no debe colgarse esperando a una goroutine que no existe

	if s.started.Load() {
		t.Error("Start marcó el sink como arrancado después de pararlo: falta el guardia")
	}

	// Margen para que una goroutine indebida hubiera llegado a conectar.
	time.Sleep(50 * time.Millisecond)
	pub.mu.Lock()
	connects := pub.connects
	pub.mu.Unlock()
	if connects != 0 {
		t.Errorf("connects = %d: arrancar un sink ya parado abrió una conexión", connects)
	}
	if s.State() != StateIdle {
		t.Errorf("estado = %v, quería idle", s.State())
	}
}
