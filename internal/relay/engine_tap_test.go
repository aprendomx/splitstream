package relay

import (
	"bytes"
	"testing"
	"time"
)

// La API necesita dos cosas del motor para la vista previa: el sequence header de vídeo
// (que lleva el avcC) y un tap. Las dos delegan en el hub; este test fija el contrato.
func TestEngineExposesVideoConfigAndTap(t *testing.T) {
	hub := NewHub(nil)
	e := NewEngine(EngineConfig{Hub: hub})

	if got := e.VideoConfig(); got != nil {
		t.Fatalf("sin sequence header, VideoConfig() = %x; quería nil", got)
	}

	seq := &Message{Kind: KindVideo, IsSeqHeader: true, IsKeyframe: true,
		Payload: []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x64, 0x00, 0x1f}}
	hub.Publish(seq)
	if got := e.VideoConfig(); !bytes.Equal(got, seq.Payload) {
		t.Fatalf("VideoConfig() = %x; quería el payload del sequence header %x", got, seq.Payload)
	}

	ch, release := e.Tap()
	defer release()
	hub.Publish(&Message{Kind: KindVideo, IsKeyframe: true, Payload: []byte{0x17, 0x01}})
	select {
	case m := <-ch:
		if !m.IsKeyframe {
			t.Fatalf("del tap salió %+v; quería el keyframe", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("el tap del motor no entregó el keyframe")
	}
}
