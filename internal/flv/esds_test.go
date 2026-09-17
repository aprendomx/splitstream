package flv_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/flv"
)

// Lo que Safari 26 entregó en decoderConfig.description durante el spike del
// 2026-09-17: un ES_Descriptor completo con el ASC (11 90 = AAC-LC, 48 kHz, estéreo)
// dentro, como DecoderSpecificInfo. Chrome, en cambio, entrega los 2 bytes desnudos.
var esdsSafari = []byte{
	0x03, 0x80, 0x80, 0x80, 0x22, 0x00, 0x00, 0x00,
	0x04, 0x80, 0x80, 0x80, 0x14, 0x40, 0x14, 0x00, 0x18, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x05, 0x80, 0x80, 0x80, 0x02, 0x11, 0x90,
	0x06, 0x80, 0x80, 0x80, 0x01, 0x02,
}

func TestAudioSpecificConfigPassesABareASCThrough(t *testing.T) {
	got, err := flv.AudioSpecificConfig([]byte{0x11, 0x90})
	if err != nil {
		t.Fatalf("AudioSpecificConfig: %v", err)
	}
	if !bytes.Equal(got, []byte{0x11, 0x90}) {
		t.Errorf("ASC = %x, quería 1190", got)
	}
}

func TestAudioSpecificConfigExtractsFromSafariESDS(t *testing.T) {
	got, err := flv.AudioSpecificConfig(esdsSafari)
	if err != nil {
		t.Fatalf("AudioSpecificConfig: %v", err)
	}
	if !bytes.Equal(got, []byte{0x11, 0x90}) {
		t.Errorf("ASC = %x, quería 1190", got)
	}
}

// La longitud «base 128» del esds puede venir en 1 byte (0x22) o en 4 (80 80 80 22): el
// parser tiene que aceptar las dos, porque cada muxer usa una.
func TestAudioSpecificConfigAcceptsShortLengths(t *testing.T) {
	corto := []byte{
		0x03, 0x19, 0x00, 0x00, 0x00,
		0x04, 0x11, 0x40, 0x14, 0x00, 0x18, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x05, 0x02, 0x11, 0x90,
		0x06, 0x01, 0x02,
	}
	got, err := flv.AudioSpecificConfig(corto)
	if err != nil {
		t.Fatalf("AudioSpecificConfig: %v", err)
	}
	if !bytes.Equal(got, []byte{0x11, 0x90}) {
		t.Errorf("ASC = %x, quería 1190", got)
	}
}

func TestAudioSpecificConfigRejectsTruncatedESDS(t *testing.T) {
	for corte := 1; corte < 32; corte++ {
		if _, err := flv.AudioSpecificConfig(esdsSafari[:corte]); !errors.Is(err, flv.ErrMalformedESDS) {
			t.Errorf("esds cortado en %d bytes: err = %v, quería ErrMalformedESDS", corte, err)
		}
	}
}

func TestAudioSpecificConfigRejectsEmpty(t *testing.T) {
	if _, err := flv.AudioSpecificConfig(nil); !errors.Is(err, flv.ErrEmptyPayload) {
		t.Errorf("err = %v, quería ErrEmptyPayload", err)
	}
}
