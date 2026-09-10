//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/sinks"
	"github.com/aprendomx/splitstream/internal/store"
)

// TestRecordingEndToEnd: ffmpeg publica 12 s contra la ingesta con la grabación activada
// y sin segmentar; al terminar hay un FLV que ffprobe reconoce con vídeo y audio de ~12 s,
// y la fila de recordings cuadra con él.
func TestRecordingEndToEnd(t *testing.T) {
	requireTool(t, "ffmpeg")
	requireTool(t, "ffprobe")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	var ingest *rtmpio.Ingest
	defer func() {
		if ingest != nil {
			ingest.Close()
		}
		time.Sleep(300 * time.Millisecond)
		db.Close()
	}()

	cipher := testCipher(t)
	if err := db.Bootstrap(ctx, cipher); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	ingestKey, err := db.RevealIngestKey(ctx, cipher)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}
	on, seg := true, 0
	if _, err := db.UpdateRecordingSettings(ctx, store.RecordingSettingsPatch{Enabled: &on, SegmentMin: &seg}); err != nil {
		t.Fatalf("UpdateRecordingSettings: %v", err)
	}

	recDir := t.TempDir()
	factory := sinks.NewFactory(db, cipher, nil)
	factory.SetRecordingsDir(recDir)

	hub := relay.NewHub(nil)
	defer hub.Close()
	engine := relay.NewEngine(relay.EngineConfig{Hub: hub, Store: adapter{db}})
	engine.SetValidator(func(app, key string) error {
		if app == "live" && key == ingestKey.Reveal() {
			return nil
		}
		return rtmpio.ErrBadStreamKey
	})
	engine.SetSinkProvider(func(sessionID int64) ([]*relay.Sink, error) {
		rec, err := factory.BuildRecorder(ctx, sessionID)
		if err != nil || rec == nil {
			t.Errorf("BuildRecorder: sink=%v err=%v", rec, err)
			return nil, err
		}
		return []*relay.Sink{rec}, nil
	})

	addr := freePort(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ingest = rtmpio.NewIngest(rtmpio.IngestConfig{Addr: addr, Handler: engine})
	go ingest.Serve(ln)
	time.Sleep(300 * time.Millisecond)

	pubURL := fmt.Sprintf("rtmp://%s/live/%s", addr, ingestKey.Reveal())
	ff := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-re", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", "12", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-g", "30", "-b:v", "800k", "-c:a", "aac", "-b:a", "128k", "-ar", "44100",
		"-f", "flv", pubURL)
	if out, err := ff.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}

	// La sesión se cierra cuando go-rtmp ve el socket cerrado; puede tardar un poco más
	// que ffmpeg en salir.
	fin := time.Now().Add(10 * time.Second)
	for engine.SessionID() != 0 && time.Now().Before(fin) {
		time.Sleep(100 * time.Millisecond)
	}
	if engine.SessionID() != 0 {
		t.Fatal("la sesión no se cerró tras salir ffmpeg")
	}

	grabaciones, err := db.ListRecordings(ctx, 0, 10, 0)
	if err != nil {
		t.Fatalf("ListRecordings: %v", err)
	}
	if len(grabaciones) != 1 {
		t.Fatalf("grabaciones = %d, quería 1 (sin segmentar)", len(grabaciones))
	}
	g := grabaciones[0]
	if g.EndedAt == nil || g.Bytes < 100_000 || g.DurationMS < 10_000 || g.DurationMS > 13_500 {
		t.Errorf("fila incoherente: %+v", g)
	}

	archivo := filepath.Join(recDir, filepath.FromSlash(g.Path))
	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name,codec_type,width,height:format=duration",
		"-of", "default=noprint_wrappers=1", archivo)
	b, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v\n%s", err, b)
	}
	got := string(b)
	t.Logf("ffprobe de la grabación:\n%s", got)
	for _, want := range []string{"codec_name=h264", "codec_name=aac", "width=640", "height=360"} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en la grabación:\n%s", want, got)
		}
	}
	for _, linea := range strings.Split(got, "\n") {
		if strings.HasPrefix(linea, "duration=") {
			d, _ := strconv.ParseFloat(strings.TrimPrefix(linea, "duration="), 64)
			if d < 10 || d > 13.5 {
				t.Errorf("duración según ffprobe = %.1f s, quería ~12", d)
			}
		}
	}
}
