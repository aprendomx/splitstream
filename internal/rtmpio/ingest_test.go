package rtmpio

import (
	"errors"
	"sync"
	"testing"

	"github.com/aprendomx/splitstream/internal/relay"
)

// recorder captura lo que el servidor de ingesta entrega.
type recorder struct {
	mu       sync.Mutex
	msgs     []*relay.Message
	starts   int
	ends     int
	startErr error
}

func (r *recorder) OnPublishStart(app, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	return r.startErr
}

func (r *recorder) OnMessage(msg *relay.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
}

func (r *recorder) OnPublishEnd() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends++
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

func TestClassifyVideoKeyframe(t *testing.T) {
	msg, err := classifyVideo(100, []byte{0x17, 0x01, 0, 0, 0})
	if err != nil {
		t.Fatalf("classifyVideo: %v", err)
	}
	if msg.Kind != relay.KindVideo {
		t.Errorf("Kind = %v", msg.Kind)
	}
	if !msg.IsKeyframe {
		t.Error("IsKeyframe = false")
	}
	if msg.IsSeqHeader {
		t.Error("IsSeqHeader = true")
	}
	if msg.Timestamp != 100 {
		t.Errorf("Timestamp = %d", msg.Timestamp)
	}
}

func TestClassifyVideoSequenceHeader(t *testing.T) {
	msg, err := classifyVideo(0, []byte{0x17, 0x00, 0, 0, 0})
	if err != nil {
		t.Fatalf("classifyVideo: %v", err)
	}
	if !msg.IsSeqHeader {
		t.Error("IsSeqHeader = false, quería true")
	}
}

// Enhanced-RTMP (HEVC/AV1) se rechaza: no se puede transcodificar y Twitch no lo acepta.
func TestClassifyVideoRejectsEnhancedRTMP(t *testing.T) {
	_, err := classifyVideo(0, []byte{0x90, 'h', 'v', 'c', '1'})
	if !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("classifyVideo con enhanced-RTMP = %v, quería ErrUnsupportedCodec", err)
	}
}

func TestClassifyVideoRejectsNonAVC(t *testing.T) {
	// codecID 2 = Sorenson H.263
	if _, err := classifyVideo(0, []byte{0x12, 0x00}); !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("classifyVideo con H.263 = %v, quería ErrUnsupportedCodec", err)
	}
}

func TestClassifyAudioAAC(t *testing.T) {
	msg, err := classifyAudio(50, []byte{0xAF, 0x01, 0x21})
	if err != nil {
		t.Fatalf("classifyAudio: %v", err)
	}
	if msg.Kind != relay.KindAudio {
		t.Errorf("Kind = %v", msg.Kind)
	}
	if msg.IsSeqHeader {
		t.Error("un frame raw no es sequence header")
	}
	if msg.Timestamp != 50 {
		t.Errorf("Timestamp = %d", msg.Timestamp)
	}
}

func TestClassifyAudioSequenceHeader(t *testing.T) {
	msg, err := classifyAudio(0, []byte{0xAF, 0x00, 0x12, 0x10})
	if err != nil {
		t.Fatalf("classifyAudio: %v", err)
	}
	if !msg.IsSeqHeader {
		t.Error("IsSeqHeader = false, quería true")
	}
}

func TestClassifyAudioRejectsNonAAC(t *testing.T) {
	// soundFormat 2 = MP3
	if _, err := classifyAudio(0, []byte{0x2F, 0x00}); !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("classifyAudio con MP3 = %v, quería ErrUnsupportedCodec", err)
	}
}

// El payload que llega al relay debe ser una copia: go-rtmp reutiliza sus buffers.
func TestClassifyCopiesPayload(t *testing.T) {
	src := []byte{0x17, 0x01, 0xAA, 0xBB}
	msg, err := classifyVideo(0, src)
	if err != nil {
		t.Fatalf("classifyVideo: %v", err)
	}
	src[2] = 0xFF
	if msg.Payload[2] == 0xFF {
		t.Error("el mensaje comparte memoria con el buffer de origen: hay que copiar")
	}
}
