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

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
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

	stop := startIngestAndPublish(t, ctx, hub, name, 120)
	defer stop()

	// Tasa de referencia de A con todo sano: dos muestras separadas en el tiempo.
	rateBefore := measureRate(t, hub, 1, 2*time.Second)
	if rateBefore <= 0 {
		t.Fatalf("el destino A no estaba transmitiendo antes de la prueba (tasa %f B/s)", rateBefore)
	}
	t.Logf("tasa de A antes de la caída: %.0f B/s", rateBefore)

	// Tirar el sink B y DEJARLO caído: con docker restart vuelve en menos de un segundo,
	// y esa ventana es demasiado corta para distinguir "A siguió fluyendo" de "A estuvo
	// bloqueado un instante". Parado de verdad, el backoff encadena varios intentos y la
	// ventana es medible.
	t.Log("parando splitstream-test-sink-b")
	if b, err := exec.CommandContext(ctx, "docker", "stop", "-t", "0",
		"splitstream-test-sink-b").CombinedOutput(); err != nil {
		t.Fatalf("docker stop: %v\n%s", err, b)
	}

	// Si el test aborta a mitad, el contenedor no puede quedarse parado: rompería las
	// corridas siguientes.
	defer exec.Command("docker", "start", "splitstream-test-sink-b").Run()

	// Dar tiempo a que B note la caída.
	deadline := time.Now().Add(20 * time.Second)
	var sawDown bool
	for time.Now().Before(deadline) {
		if hub.Snapshot()[2].State != "live" {
			sawDown = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !sawDown {
		t.Fatal("el destino B nunca salió de live tras pararse su servidor")
	}

	// LA MEDICIÓN QUE IMPORTA: la tasa de A mientras B está caído. Si un fallo en B
	// bloqueara el fan-out, esta tasa se desplomaría.
	rateDuring := measureRate(t, hub, 1, 4*time.Second)
	t.Logf("tasa de A durante la caída de B: %.0f B/s", rateDuring)

	if rateDuring < rateBefore*0.5 {
		t.Errorf("la tasa del destino A cayó de %.0f a %.0f B/s mientras B estaba caído: "+
			"un destino está afectando a los demás", rateBefore, rateDuring)
	}

	// Comprobar de paso que el backoff llegó a encadenar más de un intento.
	if r := hub.Snapshot()[2]; r.State == "live" {
		t.Error("el destino B volvió a live con su servidor parado")
	}

	// Levantar B otra vez y comprobar que vuelve solo.
	t.Log("levantando splitstream-test-sink-b")
	if b, err := exec.CommandContext(ctx, "docker", "start",
		"splitstream-test-sink-b").CombinedOutput(); err != nil {
		t.Fatalf("docker start: %v\n%s", err, b)
	}

	deadline = time.Now().Add(90 * time.Second)
	var recovered bool
	for time.Now().Before(deadline) {
		if s := hub.Snapshot()[2]; s.State == "live" && s.Reconnections > 0 {
			recovered = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !recovered {
		t.Errorf("el destino B no se reconectó: %+v", hub.Snapshot()[2])
	}

	// Y A no se enteró de nada en todo el proceso.
	after := hub.Snapshot()
	if after[1].State != "live" {
		t.Errorf("el destino A quedó en %q: la caída de B lo afectó", after[1].State)
	}
	if after[1].Reconnections != 0 {
		t.Errorf("el destino A se reconectó %d veces sin motivo", after[1].Reconnections)
	}
	t.Logf("reconexiones de B: %d", after[2].Reconnections)
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

// measureRate devuelve los bytes por segundo que un destino envió durante la ventana dada.
//
// Medir la TASA y no el total es lo que permite detectar que un destino dejó de fluir
// durante un intervalo concreto: un total que crece no distingue "siguió transmitiendo"
// de "estuvo parado un rato y luego siguió".
func measureRate(t *testing.T, hub *relay.Hub, id int64, window time.Duration) float64 {
	t.Helper()

	start, ok := hub.Snapshot()[id]
	if !ok {
		t.Fatalf("no hay métricas para el destino %d", id)
	}
	time.Sleep(window)
	end, ok := hub.Snapshot()[id]
	if !ok {
		t.Fatalf("no hay métricas para el destino %d", id)
	}

	return float64(end.BytesSent-start.BytesSent) / window.Seconds()
}
