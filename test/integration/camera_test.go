//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/httpapi"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/store"
)

// tagFLV es un tag de media leído de un archivo FLV: lo justo para hacer de navegador.
type tagFLV struct {
	tipo byte // 8 audio, 9 vídeo
	ts   uint32
	body []byte
}

// leerFLV recorre un archivo FLV (cabecera de 9 bytes, y por cada tag 11 bytes de cabecera,
// el cuerpo y 4 bytes de PreviousTagSize) y devuelve los tags de audio y vídeo.
func leerFLV(t *testing.T, path string) []tagFLV {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leer %s: %v", path, err)
	}
	if len(b) < 13 || string(b[:3]) != "FLV" {
		t.Fatalf("%s no es un FLV", path)
	}
	var tags []tagFLV
	for pos := 13; pos+11 <= len(b); {
		tipo := b[pos]
		size := int(b[pos+1])<<16 | int(b[pos+2])<<8 | int(b[pos+3])
		ts := uint32(b[pos+4])<<16 | uint32(b[pos+5])<<8 | uint32(b[pos+6]) | uint32(b[pos+7])<<24
		pos += 11
		if pos+size > len(b) {
			break
		}
		if tipo == 8 || tipo == 9 {
			tags = append(tags, tagFLV{tipo: tipo, ts: ts, body: b[pos : pos+size]})
		}
		pos += size + 4
	}
	return tags
}

// TestCameraEndToEnd hace de navegador: manda por /api/camera/ws lo que WebCodecs
// entregaría —avcC, NALUs AVCC, ASC y AAC crudo, sacados de un FLV que genera ffmpeg— y
// comprueba que un sink RTMP real recibe un stream decodificable con vídeo y audio.
//
// La reconstrucción byte a byte que describe el spec §8 ya está fijada por
// TestWrapRoundTripsThroughInspect y TestCameraWrapsFramesIntoRelayMessages; lo que añade
// este test es la propiedad más fuerte, y la única que importa de verdad: que un sink real
// decodifique h264 y aac de lo que sale por el otro extremo.
func TestCameraEndToEnd(t *testing.T) {
	requireTool(t, "ffmpeg")
	requireTool(t, "ffprobe")
	requireSink(t, "localhost:19351")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// El «navegador»: un FLV corto con H.264 baseline sin B-frames y AAC.
	src := filepath.Join(t.TempDir(), "src.flv")
	gen := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "8", "-c:v", "libx264", "-preset", "ultrafast", "-profile:v", "baseline", "-bf", "0",
		"-pix_fmt", "yuv420p", "-g", "30", "-b:v", "800k",
		"-c:a", "aac", "-b:a", "128k", "-ar", "48000", "-ac", "2", "-y", "-f", "flv", src)
	if b, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg no pudo generar el FLV: %v\n%s", err, b)
	}
	tags := leerFLV(t, src)
	if len(tags) == 0 {
		t.Fatal("el FLV generado no tiene tags de media")
	}

	// Servidor: base, motor, un sink al mediamtx de pruebas y la API con el endpoint.
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { time.Sleep(300 * time.Millisecond); db.Close() }()
	cipher := testCipher(t)
	if err := db.Bootstrap(ctx, cipher); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	hash, err := crypto.HashPassword("secreta-de-prueba")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := db.SetPasswordHash(ctx, hash); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}

	streamName := fmt.Sprintf("cam%d", time.Now().UnixNano())
	hub := relay.NewHub(nil)
	defer hub.Close()
	engine := relay.NewEngine(relay.EngineConfig{Hub: hub, Store: adapter{db}})
	engine.SetSinkProvider(func(int64) ([]*relay.Sink, error) {
		pub, err := rtmpio.NewPublisher(rtmpio.PublisherConfig{URL: sinkA, StreamKey: crypto.Secret(streamName)})
		if err != nil {
			return nil, err
		}
		return []*relay.Sink{relay.NewSink(relay.SinkConfig{ID: 1, Name: "sink-a", Pub: pub})}, nil
	})

	var master [32]byte
	copy(master[:], bytes.Repeat([]byte{7}, 32))
	api, err := httpapi.New(httpapi.Config{DB: db, Cipher: cipher, Engine: engine, MasterKey: master})
	if err != nil {
		t.Fatalf("httpapi.New: %v", err)
	}
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()

	// Sesión del panel: la cámara se autentica con la cookie, no con la clave RTMP.
	resp, err := http.Post(ts.URL+"/api/auth/login", "application/json", strings.NewReader(`{"password":"secreta-de-prueba"}`))
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login: %v (código %v)", err, resp)
	}
	h := http.Header{}
	var partes []string
	for _, c := range resp.Cookies() {
		partes = append(partes, c.Name+"="+c.Value)
	}
	h.Set("Cookie", strings.Join(partes, "; "))
	resp.Body.Close()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/camera/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	start, _ := json.Marshal(map[string]any{
		"width": 640, "height": 360, "framerate": 30, "video_bitrate": 800000,
		"audio_bitrate": 128000, "sample_rate": 48000, "channels": 2,
	})
	if err := conn.Write(ctx, websocket.MessageBinary, append([]byte{0x00}, start...)); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, data, err := conn.Read(ctx); err != nil || !strings.Contains(string(data), "session_id") {
		t.Fatalf("confirmación: %v %q", err, data)
	}

	// Reproducir los tags a su ritmo en una goroutine, quitándoles la cabecera FLV: lo
	// que queda es lo que WebCodecs entrega. Los fallos del emisor salen por el canal
	// (t.Fatalf no se puede llamar fuera de la goroutine del test).
	errEnvio := make(chan error, 1)
	go func() {
		inicio := time.Now()
		for _, tg := range tags {
			var msg []byte
			switch {
			case tg.tipo == 9 && tg.body[1] == 0x00:
				msg = append([]byte{0x01}, tg.body[5:]...)
			case tg.tipo == 9:
				flags := byte(0)
				if tg.body[0]>>4 == 1 {
					flags = 1
				}
				msg = append([]byte{0x02, flags, 0, 0, 0, 0}, tg.body[5:]...)
				binary.BigEndian.PutUint32(msg[2:6], tg.ts)
			case tg.tipo == 8 && tg.body[1] == 0x00:
				msg = append([]byte{0x03}, tg.body[2:]...)
			default:
				msg = append([]byte{0x04, 0, 0, 0, 0}, tg.body[2:]...)
				binary.BigEndian.PutUint32(msg[1:5], tg.ts)
			}
			if espera := time.Duration(tg.ts)*time.Millisecond - time.Since(inicio); espera > 0 {
				time.Sleep(espera)
			}
			if err := conn.Write(ctx, websocket.MessageBinary, msg); err != nil {
				errEnvio <- fmt.Errorf("enviar tag ts=%d: %w", tg.ts, err)
				return
			}
		}
		errEnvio <- nil
	}()

	// A los 4 s de emisión, leer del sink mientras sigue llegando media.
	time.Sleep(4 * time.Second)
	got := probeStream(t, ctx, fmt.Sprintf("%s/%s", sinkA, streamName), filepath.Join(t.TempDir(), "out.flv"))
	t.Logf("ffprobe del stream retransmitido:\n%s", got)
	for _, want := range []string{"codec_name=h264", "codec_name=aac"} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en la salida del sink:\n%s", want, got)
		}
	}
	if ses := engine.Session(); ses.ID == 0 || ses.Source != relay.SourceBrowser || ses.Width != 640 {
		t.Errorf("sesión = %+v, quería browser 640x360 en vivo", ses)
	}

	if err := <-errEnvio; err != nil {
		t.Fatal(err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(time.Second)
	if engine.SessionID() != 0 {
		t.Error("la sesión no se cerró al cerrar el WebSocket")
	}
}
