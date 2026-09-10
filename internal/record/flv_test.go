package record

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// tagLeido es lo que el parser mínimo del test saca de cada tag.
type tagLeido struct {
	Tipo byte
	TS   uint32
	Data []byte
}

// leerFLV parsea un archivo FLV completo: cabecera, PreviousTagSize0 y tags con su
// PreviousTagSize. Es deliberadamente estricto: cualquier byte fuera de sitio falla.
func leerFLV(t *testing.T, b []byte) []tagLeido {
	t.Helper()
	if len(b) < 13 || string(b[:3]) != "FLV" || b[3] != 1 {
		t.Fatalf("cabecera FLV inválida: % x", b[:min(13, len(b))])
	}
	if b[4]&0x05 != 0x05 {
		t.Errorf("flags = %#x, quería audio+vídeo (0x05)", b[4])
	}
	if binary.BigEndian.Uint32(b[5:9]) != 9 {
		t.Errorf("DataOffset = %d, quería 9", binary.BigEndian.Uint32(b[5:9]))
	}
	if binary.BigEndian.Uint32(b[9:13]) != 0 {
		t.Errorf("PreviousTagSize0 = %d, quería 0", binary.BigEndian.Uint32(b[9:13]))
	}
	var out []tagLeido
	p := 13
	for p < len(b) {
		if len(b)-p < 11 {
			t.Fatalf("tag truncado en %d", p)
		}
		n := int(b[p+1])<<16 | int(b[p+2])<<8 | int(b[p+3])
		ts := uint32(b[p+4])<<16 | uint32(b[p+5])<<8 | uint32(b[p+6]) | uint32(b[p+7])<<24
		if b[p+8]|b[p+9]|b[p+10] != 0 {
			t.Errorf("StreamID != 0 en %d", p)
		}
		fin := p + 11 + n
		if len(b) < fin+4 {
			t.Fatalf("datos truncados en %d", p)
		}
		if prev := binary.BigEndian.Uint32(b[fin : fin+4]); prev != uint32(11+n) {
			t.Errorf("PreviousTagSize = %d, quería %d", prev, 11+n)
		}
		out = append(out, tagLeido{Tipo: b[p], TS: ts, Data: b[p+11 : fin]})
		p = fin + 4
	}
	return out
}

func TestWriteHeaderAndTagsRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := WriteTag(&buf, TagScript, 0, []byte{0x02, 0x00, 0x0a, 'o', 'n', 'M', 'e', 't', 'a', 'D', 'a', 't', 'a'}); err != nil {
		t.Fatal(err)
	}
	if err := WriteTag(&buf, TagVideo, 0, []byte{0x17, 0x00, 0, 0, 0, 0x01}); err != nil {
		t.Fatal(err)
	}
	// Timestamp mayor de 24 bits: el byte extendido tiene que llevar los 8 altos.
	if err := WriteTag(&buf, TagAudio, 0x01_23_45_67, []byte{0xaf, 0x01, 0xff}); err != nil {
		t.Fatal(err)
	}

	tags := leerFLV(t, buf.Bytes())
	if len(tags) != 3 {
		t.Fatalf("tags = %d, quería 3", len(tags))
	}
	if tags[0].Tipo != TagScript || tags[1].Tipo != TagVideo || tags[2].Tipo != TagAudio {
		t.Errorf("tipos = %d %d %d", tags[0].Tipo, tags[1].Tipo, tags[2].Tipo)
	}
	if tags[2].TS != 0x01234567 {
		t.Errorf("timestamp extendido = %#x, quería 0x01234567", tags[2].TS)
	}
	if !bytes.Equal(tags[1].Data, []byte{0x17, 0x00, 0, 0, 0, 0x01}) {
		t.Errorf("datos del tag de vídeo alterados: % x", tags[1].Data)
	}
}

// Un tag de más de 16 MiB no cabe en 3 bytes de tamaño: se rechaza en vez de escribir
// un archivo corrupto.
func TestWriteTagRejectsOversizedData(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteTag(&buf, TagVideo, 0, make([]byte, 1<<24)); err == nil {
		t.Fatal("quería error por tag demasiado grande")
	}
}
