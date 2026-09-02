//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
)

const sinkB = "rtmp://localhost:19352/live"

// probeStream lee del sink y devuelve la salida de ffprobe.
func probeStream(t *testing.T, ctx context.Context, url, out string) string {
	t.Helper()
	rec := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-rw_timeout", "15000000", "-i", url, "-t", "3", "-c", "copy", "-y", out)
	if b, err := rec.CombinedOutput(); err != nil {
		t.Fatalf("no se pudo leer de %s: %v\n%s", url, err, b)
	}
	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name,codec_type", "-of", "default=noprint_wrappers=1", out)
	b, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe sobre %s: %v\n%s", out, err, b)
	}
	return string(b)
}

// TestFanOutToTwoSinks comprueba lo que pide el spec §11: un stream entra y sale hacia dos
// destinos a la vez, con vídeo y audio decodificables en ambos.
func TestFanOutToTwoSinks(t *testing.T) {
	requireTool(t, "ffmpeg")
	requireTool(t, "ffprobe")
	requireSink(t, "localhost:19351")
	requireSink(t, "localhost:19352")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	name := fmt.Sprintf("fan%d", time.Now().UnixNano())
	hub := relay.NewHub(nil)
	defer hub.Close()

	for i, base := range []string{sinkA, sinkB} {
		url, id := base, int64(i+1)
		s := relay.NewSink(relay.SinkConfig{
			ID: id, Name: fmt.Sprintf("sink-%d", id),
			NewPub: func() (relay.Publisher, error) {
				return rtmpio.NewPublisher(rtmpio.PublisherConfig{
					URL: url, StreamKey: crypto.Secret(name),
				})
			},
		})
		s.Start(ctx, hub.Preamble())
		hub.Add(s)
	}

	stop := startIngestAndPublish(t, ctx, hub, name, 25)
	defer stop()

	time.Sleep(6 * time.Second)

	dir := t.TempDir()
	for i, base := range []string{sinkA, sinkB} {
		got := probeStream(t, ctx, fmt.Sprintf("%s/%s", base, name),
			filepath.Join(dir, fmt.Sprintf("out%d.flv", i)))
		t.Logf("sink %d:\n%s", i+1, got)
		for _, want := range []string{"codec_name=h264", "codec_name=aac"} {
			if !strings.Contains(got, want) {
				t.Errorf("sink %d: falta %q en\n%s", i+1, want, got)
			}
		}
	}

	snap := hub.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("el hub reporta %d destinos, quería 2", len(snap))
	}
	for id, m := range snap {
		if m.State != "live" {
			t.Errorf("destino %d en estado %q, quería live", id, m.State)
		}
		if m.BytesSent == 0 {
			t.Errorf("destino %d no envió bytes", id)
		}
		if m.BitrateBPS == 0 {
			t.Errorf("destino %d reporta bitrate 0", id)
		}
	}
}

// TestReconnectAfterSinkDies mata un sink a media transmisión y comprueba que se reconecta
// sin tumbar al otro (spec §11).
func TestReconnectAfterSinkDies(t *testing.T) {
	requireTool(t, "ffmpeg")
	requireTool(t, "docker")
	requireSink(t, "localhost:19351")
	requireSink(t, "localhost:19352")

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	name := fmt.Sprintf("rec%d", time.Now().UnixNano())
	hub := relay.NewHub(nil)
	defer hub.Close()

	for i, base := range []string{sinkA, sinkB} {
		url, id := base, int64(i+1)
		s := relay.NewSink(relay.SinkConfig{
			ID: id, Name: fmt.Sprintf("sink-%d", id),
			NewPub: func() (relay.Publisher, error) {
				return rtmpio.NewPublisher(rtmpio.PublisherConfig{
					URL: url, StreamKey: crypto.Secret(name),
				})
			},
		})
		s.Start(ctx, hub.Preamble())
		hub.Add(s)
	}

	stop := startIngestAndPublish(t, ctx, hub, name, 90)
	defer stop()

	time.Sleep(8 * time.Second)
	before := hub.Snapshot()
	for id, m := range before {
		if m.State != "live" {
			t.Fatalf("antes de matar nada, el destino %d está en %q", id, m.State)
		}
	}
	survivorBytes := before[1].BytesSent
	t.Logf("antes de matar B: A=%+v B=%+v", before[1], before[2])

	// Matar el sink B.
	t.Log("reiniciando splitstream-test-sink-b")
	if b, err := exec.CommandContext(ctx, "docker", "restart", "-t", "0",
		"splitstream-test-sink-b").CombinedOutput(); err != nil {
		t.Fatalf("docker restart: %v\n%s", err, b)
	}

	// El destino B debe salir de live.
	deadline := time.Now().Add(30 * time.Second)
	var sawDown bool
	for time.Now().Before(deadline) {
		if s := hub.Snapshot(); s[2].State != "live" {
			sawDown = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !sawDown {
		t.Error("el destino B nunca salió de live tras reiniciarse su servidor")
	}

	// Y debe volver por sí solo.
	deadline = time.Now().Add(90 * time.Second)
	var recovered bool
	for time.Now().Before(deadline) {
		if s := hub.Snapshot(); s[2].State == "live" && s[2].Reconnections > 0 {
			recovered = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !recovered {
		t.Errorf("el destino B no se reconectó: %+v", hub.Snapshot()[2])
	}

	// Y el destino A no se enteró: siguió enviando todo el tiempo.
	after := hub.Snapshot()
	t.Logf("después de que B se recuperara: A=%+v B=%+v", after[1], after[2])
	if after[1].State != "live" {
		t.Errorf("el destino A quedó en %q: la caída de B lo afectó", after[1].State)
	}
	if after[1].BytesSent <= survivorBytes {
		t.Errorf("el destino A dejó de enviar durante la caída de B: %d → %d",
			survivorBytes, after[1].BytesSent)
	}
	if after[1].Reconnections != 0 {
		t.Errorf("el destino A se reconectó %d veces sin motivo", after[1].Reconnections)
	}
}

// startIngestAndPublish levanta la ingesta sobre un puerto efímero, arranca ffmpeg
// publicando contra ella durante `seconds`, y devuelve una función de parada.
func startIngestAndPublish(t *testing.T, ctx context.Context, hub *relay.Hub, key string, seconds int) func() {
	t.Helper()

	engine := relay.NewEngine(relay.EngineConfig{
		Hub: hub, Store: nopStore{}, BaseContext: ctx,
	})
	engine.SetValidator(func(app, k string) error {
		if app == "live" && k == key {
			return nil
		}
		return rtmpio.ErrBadStreamKey
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := rtmpio.NewIngest(rtmpio.IngestConfig{Addr: ln.Addr().String(), Handler: engine})
	go ing.Serve(ln)
	time.Sleep(300 * time.Millisecond)

	pubURL := fmt.Sprintf("rtmp://%s/live/%s", ln.Addr().String(), key)
	ff := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-re", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", fmt.Sprint(seconds), "-c:v", "libx264", "-preset", "ultrafast",
		"-pix_fmt", "yuv420p", "-g", "30", "-b:v", "800k",
		"-c:a", "aac", "-b:a", "128k", "-ar", "44100", "-f", "flv", pubURL)
	if err := ff.Start(); err != nil {
		t.Fatalf("arrancar ffmpeg: %v", err)
	}

	return func() {
		if ff.Process != nil {
			ff.Process.Kill()
		}
		ing.Close()
	}
}

// nopStore satisface relay.EngineStore sin persistir nada: estos tests miden el motor,
// no la base.
type nopStore struct{}

func (nopStore) StartSession(ctx context.Context) (int64, error)                { return 1, nil }
func (nopStore) FinishSession(ctx context.Context, id int64, w, h, b int) error { return nil }
func (nopStore) LogEvent(ctx context.Context, e relay.EngineEvent) error        { return nil }
