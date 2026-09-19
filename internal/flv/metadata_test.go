package flv_test

import (
	"bytes"
	"testing"

	"github.com/yutopp/go-amf0"

	"github.com/aprendomx/splitstream/internal/flv"
)

// El onMetaData es declarativo (spec base §3.8), pero las plataformas lo leen y algunas
// rechazan el stream si falta: se comprueba campo a campo decodificando con la misma
// librería AMF0 que usa go-rtmp.
func TestOnMetaDataEncodesWhatThePlatformsRead(t *testing.T) {
	payload, err := flv.OnMetaData(flv.Meta{
		Width: 1280, Height: 720, Framerate: 30,
		VideoBitrateKbps: 2500, AudioBitrateKbps: 128, AudioSampleRate: 48000,
		Stereo: true, Encoder: "splitstream-camera/test",
	})
	if err != nil {
		t.Fatalf("OnMetaData: %v", err)
	}

	dec := amf0.NewDecoder(bytes.NewReader(payload))
	var nombre string
	if err := dec.Decode(&nombre); err != nil {
		t.Fatalf("decodificar el nombre: %v", err)
	}
	if nombre != "onMetaData" {
		t.Fatalf("nombre = %q, quería onMetaData", nombre)
	}
	var campos amf0.ECMAArray
	if err := dec.Decode(&campos); err != nil {
		t.Fatalf("decodificar el ECMA array: %v", err)
	}

	quiere := map[string]interface{}{
		"width": float64(1280), "height": float64(720), "framerate": float64(30),
		"videocodecid": float64(7), "videodatarate": float64(2500),
		"audiocodecid": float64(10), "audiodatarate": float64(128),
		"audiosamplerate": float64(48000), "audiosamplesize": float64(16),
		"stereo": true, "encoder": "splitstream-camera/test",
	}
	for k, v := range quiere {
		if campos[k] != v {
			t.Errorf("%s = %v (%T), quería %v", k, campos[k], campos[k], v)
		}
	}
	if len(campos) != len(quiere) {
		t.Errorf("el array tiene %d campos, quería %d: %v", len(campos), len(quiere), campos)
	}
}
