package flv_test

import (
	"encoding/hex"
	"errors"
	"math/rand"
	"testing"

	"github.com/aprendomx/splitstream/internal/flv"
)

// avcSeqHeader construye un tag de vídeo con un AVCDecoderConfigurationRecord que
// contiene el SPS dado.
//
// Formato del tag: [0]=0x17 (keyframe|AVC), [1]=0x00 (sequence header), [2..4]=composition
// time. Luego el AVCDecoderConfigurationRecord: version, profile, compat, level,
// 0xFF (lengthSizeMinusOne), 0xE1 (numOfSPS=1), 2 bytes de longitud del SPS, y el SPS.
func avcSeqHeader(sps []byte) []byte {
	out := []byte{0x17, 0x00, 0, 0, 0, 0x01, sps[1], sps[2], sps[3], 0xFF, 0xE1}
	out = append(out, byte(len(sps)>>8), byte(len(sps)))
	return append(out, sps...)
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex: %v", err)
	}
	return b
}

// SPS real de 640x360, generado con:
//
//	ffmpeg -y -loglevel error -f lavfi -i testsrc2=size=640x360:rate=30 -t 1 \
//	  -c:v libx264 -preset ultrafast -pix_fmt yuv420p -f flv probe640.flv
//
// y extraído directamente del AVCDecoderConfigurationRecord del primer tag de vídeo
// (frameType|codecID=0x17, AVCPacketType=0). Verificado contra
// `ffprobe -show_entries stream=width,height` sobre el mismo archivo: 640x360.
func TestParseResolution640x360(t *testing.T) {
	sps := mustHex(t, "6742c01eda0280bfe5c044000003000400000300f03c58ba80")
	w, h, err := flv.ParseResolution(avcSeqHeader(sps))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if w != 640 || h != 360 {
		t.Errorf("resolución = %dx%d, quería 640x360", w, h)
	}
}

// SPS real de 1920x1080, generado y verificado igual que el anterior. Este SPS
// contiene bytes de emulation prevention (0x03 tras 0x00 0x00) y ejercita el
// recorte de frame_cropping, porque 1080 no es múltiplo de 16 (se codifica como
// 1088 y se recorta 8 filas).
func TestParseResolution1920x1080(t *testing.T) {
	sps := mustHex(t, "6742c028da01e0089f970110000003001000000303c0f1832a")
	w, h, err := flv.ParseResolution(avcSeqHeader(sps))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if w != 1920 || h != 1080 {
		t.Errorf("resolución = %dx%d, quería 1920x1080", w, h)
	}
}

// SPS real de 1280x720, generado y verificado igual que los anteriores. 720 sí es
// múltiplo de 16, así que este caso no ejercita el recorte, pero añade una tercera
// resolución real independiente para dar más confianza al parser.
func TestParseResolution1280x720(t *testing.T) {
	sps := mustHex(t, "6742c01fda014016ec0440000003004000000f03c60ca8")
	w, h, err := flv.ParseResolution(avcSeqHeader(sps))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if w != 1280 || h != 720 {
		t.Errorf("resolución = %dx%d, quería 1280x720", w, h)
	}
}

func TestParseResolutionRejectsNonSequenceHeader(t *testing.T) {
	// AVCPacketType 1 = NALU, no sequence header.
	if _, _, err := flv.ParseResolution([]byte{0x17, 0x01, 0, 0, 0}); !errors.Is(err, flv.ErrNotAVCSequenceHeader) {
		t.Fatalf("err = %v, quería ErrNotAVCSequenceHeader", err)
	}
}

func TestParseResolutionRejectsNonAVC(t *testing.T) {
	// codecID 2 = Sorenson H.263.
	if _, _, err := flv.ParseResolution([]byte{0x12, 0x00, 0, 0, 0}); !errors.Is(err, flv.ErrNotAVCSequenceHeader) {
		t.Fatalf("err = %v, quería ErrNotAVCSequenceHeader", err)
	}
}

func TestParseResolutionRejectsTruncated(t *testing.T) {
	for _, bad := range [][]byte{
		{},
		{0x17},
		{0x17, 0x00},
		{0x17, 0x00, 0, 0, 0},             // sin AVCDecoderConfigurationRecord
		{0x17, 0x00, 0, 0, 0, 1, 0, 0, 0}, // record truncado
	} {
		if _, _, err := flv.ParseResolution(bad); err == nil {
			t.Errorf("ParseResolution(%v) = nil, quería error", bad)
		}
	}
}

// Un SPS cuyo contenido no se puede decodificar debe dar error, no una resolución absurda.
func TestParseResolutionRejectsGarbageSPS(t *testing.T) {
	garbage := []byte{0x67, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	_, _, err := flv.ParseResolution(avcSeqHeader(garbage))
	if err == nil {
		t.Fatal("un SPS ilegible debe dar error")
	}
}

// La basura no debe producir resoluciones inventadas: una resolución falsa sin error es
// peor que un fallo, porque se muestra como buena.
func TestParseResolutionRejectsMostGarbage(t *testing.T) {
	// Semilla fija: el test tiene que ser reproducible.
	rnd := rand.New(rand.NewSource(1))

	const samples = 5000
	accepted := 0
	for i := 0; i < samples; i++ {
		sps := make([]byte, 25)
		rnd.Read(sps)
		sps[0] = 0x67 // cabecera NAL de SPS, para pasar el filtro trivial

		if _, _, err := flv.ParseResolution(avcSeqHeader(sps)); err == nil {
			accepted++
		}
	}

	// Con profile_idc, level_idc y reserved_zero_2bits validados debe quedar por debajo
	// del 1%. Antes de esas comprobaciones pasaba el 21%.
	if rate := float64(accepted) / samples; rate > 0.01 {
		t.Errorf("el %.1f%% de la basura aleatoria produjo una resolución sin error, quería <1%%",
			rate*100)
	}
}
