package relay

import (
	"context"
	"testing"
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
