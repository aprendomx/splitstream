package rtmpio

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
)

// recorder captura lo que el servidor de ingesta entrega.
type recorder struct {
	mu       sync.Mutex
	msgs     []*relay.Message
	starts   int
	ends     int
	startErr error
	apps     []string
}

func (r *recorder) OnPublishStart(app, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	r.apps = append(r.apps, app)
	return r.startErr
}

// lastApp es la app con la que conectó el último publisher, o "" si no hubo ninguno.
func (r *recorder) lastApp() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.apps) == 0 {
		return ""
	}
	return r.apps[len(r.apps)-1]
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

// Close debe cortar de verdad las publicaciones en curso, no solo dejar de aceptar
// conexiones nuevas: en un SIGTERM, un OBS conectado no puede quedarse publicando
// contra un proceso que ya se cree cerrado.
func TestCloseTerminatesActivePublish(t *testing.T) {
	rec := &recorder{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := NewIngest(IngestConfig{Addr: ln.Addr().String(), Handler: rec})
	go ing.Serve(ln)
	defer ing.Close()
	time.Sleep(200 * time.Millisecond)

	pub, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://" + ln.Addr().String() + "/live",
		StreamKey: crypto.Secret("clave"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pub.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Confirmar que la publicación está viva antes de cerrar.
	if err := pub.WriteVideo(0, []byte{0x17, 0x01, 0x00}); err != nil {
		t.Fatalf("WriteVideo antes de Close: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		starts := rec.starts
		rec.mu.Unlock()
		if starts > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := ing.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Tras Close, el handler debe recibir su OnPublishEnd sin que el publisher haga nada.
	endDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(endDeadline) {
		rec.mu.Lock()
		ends := rec.ends
		rec.mu.Unlock()
		if ends > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("OnPublishEnd no se llamó tras Close: la conexión activa sobrevivió al cierre")
}

// Close sigue siendo seguro antes de Serve y llamado dos veces.
func TestCloseIsSafeBeforeServeAndIdempotent(t *testing.T) {
	ing := NewIngest(IngestConfig{Addr: "127.0.0.1:0", Handler: &recorder{}})
	if err := ing.Close(); err != nil {
		t.Errorf("Close antes de Serve = %v, quería nil", err)
	}
	if err := ing.Close(); err != nil {
		t.Errorf("segundo Close = %v, quería nil", err)
	}
}
