package relay

import (
	"context"
	"testing"
	"time"
)

func TestHubFansOutToAllSinks(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	pubs := make([]*fakePublisher, 3)
	for i := range pubs {
		pubs[i] = &fakePublisher{}
		s := NewSink(SinkConfig{ID: int64(i + 1), Name: "dest", Pub: pubs[i]})
		s.Start(context.Background(), h.Preamble())
		h.Add(s)
	}

	h.Publish(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	h.Publish(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	h.Publish(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})
	h.Publish(videoKey(1000))

	for i, p := range pubs {
		waitFor(t, func() bool { return len(p.snapshot()) >= 4 }, "el sink recibió el preámbulo y el keyframe")
		if got := len(p.snapshot()); got < 4 {
			t.Errorf("sink %d recibió %d mensajes", i, got)
		}
	}
}

// El hub observa el preámbulo, de modo que un sink que llega tarde lo recibe igual.
func TestHubLateSinkGetsPreamble(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	h.Publish(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	h.Publish(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	h.Publish(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})
	h.Publish(videoInter(500)) // el sink todavía no existe

	pub := &fakePublisher{}
	late := NewSink(SinkConfig{ID: 9, Name: "tardío", Pub: pub})
	late.Start(context.Background(), h.Preamble())
	h.Add(late)

	h.Publish(videoKey(1000))
	waitFor(t, func() bool { return len(pub.snapshot()) >= 4 }, "el sink tardío recibió el preámbulo")

	got := pub.snapshot()
	if got[0].Kind != KindMeta || got[1].Kind != KindVideo || got[2].Kind != KindAudio {
		t.Errorf("el preámbulo llegó desordenado: %v %v %v", got[0].Kind, got[1].Kind, got[2].Kind)
	}
}

func TestHubRemoveStopsSink(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 7, Name: "X", Pub: pub})
	s.Start(context.Background(), h.Preamble())
	h.Add(s)
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	h.Remove(7)
	waitFor(t, func() bool { return s.State() == StateIdle }, "el sink se detuvo al quitarlo")

	pub.mu.Lock()
	closes := pub.closes
	pub.mu.Unlock()
	if closes == 0 {
		t.Error("quitar un sink debe cerrar su Publisher")
	}
}

// Publicar sin sinks no debe entrar en pánico.
func TestHubPublishWithNoSinks(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()
	h.Publish(videoKey(1000))
}

// Reemplazar un sink debe parar el viejo por completo antes de registrar el nuevo.
func TestHubAddReplacesWithoutOverlap(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	oldPub := &fakePublisher{}
	old := NewSink(SinkConfig{ID: 5, Name: "viejo", Pub: oldPub})
	old.Start(context.Background(), h.Preamble())
	h.Add(old)
	waitFor(t, func() bool { return old.State() == StateLive }, "el viejo está live")

	newPub := &fakePublisher{}
	fresh := NewSink(SinkConfig{ID: 5, Name: "nuevo", Pub: newPub})
	fresh.Start(context.Background(), h.Preamble())
	h.Add(fresh)

	// Al volver de Add, el viejo ya no puede estar vivo.
	if got := old.State(); got == StateLive {
		t.Errorf("el sink viejo sigue en %v tras reemplazarlo: ventana de escritura doble", got)
	}
	oldPub.mu.Lock()
	closes := oldPub.closes
	oldPub.mu.Unlock()
	if closes == 0 {
		t.Error("reemplazar un sink debe cerrar el Publisher del viejo")
	}

	// Y el nuevo debe seguir funcionando.
	h.Publish(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	h.Publish(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	h.Publish(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})
	h.Publish(videoKey(1000))
	waitFor(t, func() bool { return len(newPub.snapshot()) >= 4 }, "el sink nuevo recibe media")
}

func TestHubSnapshotHasEveryDestination(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	for i := 1; i <= 3; i++ {
		s := NewSink(SinkConfig{ID: int64(i), Name: "dest", Pub: &fakePublisher{}})
		s.Start(context.Background(), h.Preamble())
		h.Add(s)
	}
	waitFor(t, func() bool { return h.Len() == 3 }, "tres destinos registrados")

	snap := h.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("Snapshot tiene %d destinos, quería 3", len(snap))
	}
	for id := int64(1); id <= 3; id++ {
		if _, ok := snap[id]; !ok {
			t.Errorf("falta el destino %d en el snapshot", id)
		}
	}
}

// Añadir un sink que nunca se arrancó no debe colgar el hub.
func TestHubAddNeverStartedSinkDoesNotHang(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	old := NewSink(SinkConfig{ID: 1, Name: "sin arrancar", Pub: &fakePublisher{}})
	// A propósito: no se llama a old.Start.
	h.Add(old)

	done := make(chan struct{})
	go func() {
		defer close(done)
		fresh := NewSink(SinkConfig{ID: 1, Name: "nuevo", Pub: &fakePublisher{}})
		fresh.Start(context.Background(), h.Preamble())
		h.Add(fresh)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Add se colgó al reemplazar un sink que nunca se arrancó")
	}
}

// Un destino lento no debe frenar la entrega a los demás.
func TestHubSlowSinkDoesNotBlockOthers(t *testing.T) {
	h := NewHub(nil)
	block := make(chan struct{})
	defer func() { close(block); h.Close() }()

	slow := &fakePublisher{blockWrites: block}
	fast := &fakePublisher{}

	sSlow := NewSink(SinkConfig{ID: 1, Name: "lento", Pub: slow,
		Queue: queueConfig{MaxBytes: 1024, MaxSpan: 1_000_000}})
	sSlow.Start(context.Background(), h.Preamble())
	h.Add(sSlow)

	sFast := NewSink(SinkConfig{ID: 2, Name: "rápido", Pub: fast})
	sFast.Start(context.Background(), h.Preamble())
	h.Add(sFast)

	waitFor(t, func() bool { return sFast.State() == StateLive }, "el rápido está live")

	h.Publish(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	h.Publish(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	h.Publish(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			h.Publish(&Message{Kind: KindVideo, Timestamp: uint32(i * 33),
				Payload: make([]byte, 512), IsKeyframe: i%10 == 0})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish se bloqueó por culpa del destino lento")
	}

	waitFor(t, func() bool { return len(fast.snapshot()) > 10 }, "el destino rápido siguió recibiendo")
}
