//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/store"
)

const sinkA = "rtmp://localhost:19351/live"

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("hace falta %s en el PATH", name)
	}
}

func requireSink(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Skipf("no hay sink en %s: levanta deploy/test-compose.yml", addr)
	}
	conn.Close()
}

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 1)
	}
	c, err := crypto.NewCipher(k)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

// freePort reserva un puerto efímero para la ingesta del test.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("puerto libre: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// TestRelayEndToEnd publica un patrón de prueba con ffmpeg contra la ingesta y comprueba
// que el sink recibe un stream con video y audio decodificables.
func TestRelayEndToEnd(t *testing.T) {
	requireTool(t, "ffmpeg")
	requireTool(t, "ffprobe")
	requireSink(t, "localhost:19351")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	cipher := testCipher(t)
	if err := db.Bootstrap(ctx, cipher); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	ingestKey, err := db.RevealIngestKey(ctx, cipher)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}

	streamName := fmt.Sprintf("e2e%d", time.Now().UnixNano())

	hub := relay.NewHub(nil)
	defer hub.Close()

	pub, err := rtmpio.NewPublisher(rtmpio.PublisherConfig{
		URL:       sinkA,
		StreamKey: crypto.Secret(streamName),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	sink := relay.NewSink(relay.SinkConfig{ID: 1, Name: "sink-a", Pub: pub})
	sink.Start(ctx, hub.Preamble())
	hub.Add(sink)

	engine := relay.NewEngine(relay.EngineConfig{Hub: hub, Store: adapter{db}})
	engine.SetValidator(func(app, key string) error {
		if app == "live" && key == ingestKey.Reveal() {
			return nil
		}
		return rtmpio.ErrBadStreamKey
	})

	addr := freePort(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ingest := rtmpio.NewIngest(rtmpio.IngestConfig{Addr: addr, Handler: engine})
	go ingest.Serve(ln)
	defer ingest.Close()
	time.Sleep(300 * time.Millisecond)

	// OBS simulado: patrón de prueba con audio, en tiempo real.
	pubURL := fmt.Sprintf("rtmp://%s/live/%s", addr, ingestKey.Reveal())
	ff := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-re", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", "12", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-g", "30", "-b:v", "800k", "-c:a", "aac", "-b:a", "128k", "-ar", "44100",
		"-f", "flv", pubURL)
	ffOut, err := ff.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe: %v", err)
	}
	if err := ff.Start(); err != nil {
		t.Fatalf("arrancar ffmpeg: %v", err)
	}
	defer ff.Process.Kill()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ffOut.Read(buf)
			if n > 0 {
				t.Logf("ffmpeg: %s", strings.TrimSpace(string(buf[:n])))
			}
			if err != nil {
				return
			}
		}
	}()

	// Dar tiempo a que la sesión arranque y el sink mande el preámbulo.
	time.Sleep(4 * time.Second)

	if got := sink.State(); got != relay.StateLive {
		t.Fatalf("el sink está en %v, quería live. Último error: %v", got, sink.LastError())
	}

	// Leer del sink lo que salió del relay.
	out := filepath.Join(t.TempDir(), "out.flv")
	rec := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-rw_timeout", "15000000",
		"-i", fmt.Sprintf("%s/%s", sinkA, streamName),
		"-t", "4", "-c", "copy", "-y", out)
	if b, err := rec.CombinedOutput(); err != nil {
		t.Fatalf("no se pudo leer del sink: %v\n%s", err, b)
	}

	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 {
		t.Fatalf("el archivo grabado está vacío: %v", err)
	}

	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name,codec_type,width,height",
		"-of", "default=noprint_wrappers=1", out)
	b, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v\n%s", err, b)
	}
	got := string(b)
	t.Logf("ffprobe del stream retransmitido:\n%s", got)

	for _, want := range []string{"codec_name=h264", "codec_name=aac", "width=640", "height=360"} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en la salida del sink:\n%s", want, got)
		}
	}

	if d := sink.Dropped(); d > 0 {
		t.Logf("aviso: el sink descartó %d mensajes", d)
	}
}

// adapter conecta el store real con el contrato EngineStore.
type adapter struct{ db *store.DB }

func (a adapter) StartSession(ctx context.Context) (int64, error) { return a.db.StartSession(ctx) }
func (a adapter) FinishSession(ctx context.Context, id int64, w, h, b int) error {
	return a.db.FinishSession(ctx, id, w, h, b)
}
func (a adapter) LogEvent(ctx context.Context, e relay.EngineEvent) error {
	_, err := a.db.LogEvent(ctx, store.Event{
		SessionID:     e.SessionID,
		DestinationID: e.DestinationID,
		Level:         store.Level(e.Level),
		Kind:          e.Kind,
		Message:       e.Message,
	})
	return err
}
