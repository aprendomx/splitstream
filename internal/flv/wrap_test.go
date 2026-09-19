package flv_test

import (
	"bytes"
	"testing"

	"github.com/aprendomx/splitstream/internal/flv"
)

// Las cabeceras son bytes exactos: son lo que leen las plataformas y el grabador, y
// cualquier desviación se ve como «stream corrupto» sin más pista.
func TestWrapProducesExactHeaders(t *testing.T) {
	casos := []struct {
		nombre string
		got    []byte
		want   []byte
	}{
		{"keyframe", flv.WrapVideo([]byte{0xaa, 0xbb}, true), []byte{0x17, 0x01, 0, 0, 0, 0xaa, 0xbb}},
		{"inter", flv.WrapVideo([]byte{0xaa}, false), []byte{0x27, 0x01, 0, 0, 0, 0xaa}},
		{"avc seq header", flv.WrapVideoSeqHeader([]byte{0x01, 0x42}), []byte{0x17, 0x00, 0, 0, 0, 0x01, 0x42}},
		{"aac frame", flv.WrapAudio([]byte{0x21, 0x20}), []byte{0xaf, 0x01, 0x21, 0x20}},
		{"aac seq header", flv.WrapAudioSeqHeader([]byte{0x11, 0x90}), []byte{0xaf, 0x00, 0x11, 0x90}},
	}
	for _, c := range casos {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s = %x, quería %x", c.nombre, c.got, c.want)
		}
	}
}

// Lo que Wrap envuelve, Inspect lo desmonta con los mismos campos que el relay usa para
// decidir: el círculo se cierra.
func TestWrapRoundTripsThroughInspect(t *testing.T) {
	v, err := flv.InspectVideo(flv.WrapVideo([]byte{0x00}, true))
	if err != nil || !v.IsKeyframe || v.IsSequenceHeader || v.CodecID != flv.CodecIDAVC || v.IsEnhanced {
		t.Errorf("keyframe envuelto se inspecciona como %+v (err %v)", v, err)
	}
	v, err = flv.InspectVideo(flv.WrapVideo([]byte{0x00}, false))
	if err != nil || v.IsKeyframe || v.IsSequenceHeader {
		t.Errorf("inter envuelto se inspecciona como %+v (err %v)", v, err)
	}
	v, err = flv.InspectVideo(flv.WrapVideoSeqHeader([]byte{0x01}))
	if err != nil || !v.IsSequenceHeader || !v.IsKeyframe {
		t.Errorf("seq header envuelto se inspecciona como %+v (err %v)", v, err)
	}
	a, err := flv.InspectAudio(flv.WrapAudio([]byte{0x21}))
	if err != nil || a.IsSequenceHeader || a.SoundFormat != flv.SoundFormatAAC {
		t.Errorf("frame AAC envuelto se inspecciona como %+v (err %v)", a, err)
	}
	a, err = flv.InspectAudio(flv.WrapAudioSeqHeader([]byte{0x11, 0x90}))
	if err != nil || !a.IsSequenceHeader {
		t.Errorf("seq header AAC envuelto se inspecciona como %+v (err %v)", a, err)
	}
}

// El avcC que entrega VideoEncoder lleva el SPS dentro, y el motor saca de ahí la
// resolución que enseña el panel: un sequence header envuelto tiene que servirle a
// ParseResolution tal cual.
func TestWrapVideoSeqHeaderKeepsTheResolutionReadable(t *testing.T) {
	sps := mustHex(t, "6742c01fda014016ec0440000003004000000f03c60ca8")
	avcC := []byte{0x01, sps[1], sps[2], sps[3], 0xFF, 0xE1, byte(len(sps) >> 8), byte(len(sps))}
	avcC = append(avcC, sps...)
	w, h, err := flv.ParseResolution(flv.WrapVideoSeqHeader(avcC))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if w != 1280 || h != 720 {
		t.Errorf("resolución = %dx%d, quería 1280x720", w, h)
	}
}

// El resultado no comparte memoria con la entrada: el payload de relay.Message es
// inmutable y se reparte entre todos los sinks, y el buffer de lectura del WebSocket
// puede reutilizarse.
func TestWrapCopiesItsInput(t *testing.T) {
	in := []byte{0xaa, 0xbb}
	out := flv.WrapVideo(in, true)
	in[0] = 0x00
	if out[5] != 0xaa {
		t.Error("WrapVideo devolvió un slice que comparte memoria con la entrada")
	}
}
