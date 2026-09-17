package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/yutopp/go-amf0"

	"github.com/aprendomx/splitstream/internal/relay"
)

func cameraServer(t *testing.T) (*fakeEngine, string, []*http.Cookie) {
	t.Helper()
	srv, _, eng, _, cookies := newDestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return eng, "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/camera/ws", cookies
}

const startValido = `{"width":1280,"height":720,"framerate":30,"video_bitrate":2500000,"audio_bitrate":128000,"sample_rate":48000,"channels":2}`

func enviarBinario(t *testing.T, ctx context.Context, conn *websocket.Conn, tipo byte, cuerpo []byte) {
	t.Helper()
	escritura, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if err := conn.Write(escritura, websocket.MessageBinary, append([]byte{tipo}, cuerpo...)); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

// arrancar manda start y espera la confirmación con el id de sesión.
func arrancar(t *testing.T, ctx context.Context, conn *websocket.Conn) int64 {
	t.Helper()
	enviarBinario(t, ctx, conn, cameraMsgStart, []byte(startValido))
	leer, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	tipo, data, err := conn.Read(leer)
	if err != nil {
		t.Fatalf("Read de la confirmación: %v", err)
	}
	if tipo != websocket.MessageText {
		t.Fatalf("la confirmación llegó como %v, quería texto", tipo)
	}
	var conf struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(data, &conf); err != nil {
		t.Fatalf("confirmación ilegible %q: %v", data, err)
	}
	return conf.SessionID
}

func esperarCierre(t *testing.T, ctx context.Context, conn *websocket.Conn) websocket.CloseError {
	t.Helper()
	leer, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	_, _, err := conn.Read(leer)
	var cierre websocket.CloseError
	if !errors.As(err, &cierre) {
		t.Fatalf("se esperaba un cierre con código, llegó %v", err)
	}
	return cierre
}

func esperarTerminadas(t *testing.T, eng *fakeEngine, n int) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if eng.terminadas() >= n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("OnPublishEnd se llamó %d veces, quería %d", eng.terminadas(), n)
}

// El handshake lleva la cookie: sin sesión del panel no hay upgrade.
func TestCameraRequiresASession(t *testing.T) {
	_, url, _ := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, resp, err := dialWS(ctx, url, nil)
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("la cámara aceptó una conexión sin sesión")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("código = %d, quería 401", resp.StatusCode)
	}
}

// start es siempre lo primero: un frame antes de start no abre sesión y cierra con 4003.
func TestCameraRejectsWhenStartIsNotFirst(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	enviarBinario(t, ctx, conn, cameraMsgVideoConfig, []byte{0x01, 0x42, 0x00, 0x1f, 0xff, 0xe1, 0x00})
	if got := esperarCierre(t, ctx, conn); got.Code != cameraCloseProtocol {
		t.Errorf("código de cierre = %d, quería %d", got.Code, cameraCloseProtocol)
	}
	if eng.sesionesLocales() != 0 {
		t.Error("se abrió una sesión sin start")
	}
}

// Con OBS en el aire, la cámara no entra: 4002 con motivo en el idioma del panel.
func TestCameraClosesBusyWhenASessionIsLive(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	eng.setStartErr(relay.ErrSessionInProgress)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialPreview(ctx, url, cookies, "en")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	enviarBinario(t, ctx, conn, cameraMsgStart, []byte(startValido))
	got := esperarCierre(t, ctx, conn)
	if got.Code != cameraCloseBusy {
		t.Errorf("código de cierre = %d, quería %d", got.Code, cameraCloseBusy)
	}
	if got.Reason != "a broadcast is already in progress" {
		t.Errorf("motivo = %q, quería el texto en inglés", got.Reason)
	}
	if eng.terminadas() != 0 {
		t.Error("OnPublishEnd se llamó sin que hubiera sesión que cerrar")
	}
}

// start abre la sesión, publica el onMetaData construido con lo que declaró el cliente y
// confirma con el id.
func TestCameraStartsSessionAndPublishesMeta(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	if id := arrancar(t, ctx, conn); id != 42 {
		t.Errorf("session_id = %d, quería 42 (el del fake)", id)
	}
	if eng.sesionesLocales() != 1 {
		t.Fatalf("sesiones locales = %d, quería 1", eng.sesionesLocales())
	}
	msgs := eng.mensajes()
	if len(msgs) != 1 || msgs[0].Kind != relay.KindMeta {
		t.Fatalf("mensajes = %+v, quería solo el onMetaData", msgs)
	}
	dec := amf0.NewDecoder(bytes.NewReader(msgs[0].Payload))
	var nombre string
	var campos amf0.ECMAArray
	if err := dec.Decode(&nombre); err != nil || nombre != "onMetaData" {
		t.Fatalf("el meta no empieza por onMetaData: %q, %v", nombre, err)
	}
	if err := dec.Decode(&campos); err != nil {
		t.Fatalf("decodificar campos: %v", err)
	}
	if campos["width"] != float64(1280) || campos["audiosamplerate"] != float64(48000) || campos["stereo"] != true {
		t.Errorf("campos = %v", campos)
	}
}

// Cada mensaje binario se convierte en el relay.Message que el hub espera: mismos
// flags que Inspect* sacaría del tag equivalente de OBS, timestamps intactos, y el ASC
// extraído aunque llegue dentro de un esds.
func TestCameraWrapsFramesIntoRelayMessages(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	arrancar(t, ctx, conn)

	avcc := []byte{0x01, 0x42, 0x00, 0x1f, 0xff, 0xe1, 0x00}
	esds := []byte{
		0x03, 0x80, 0x80, 0x80, 0x22, 0x00, 0x00, 0x00,
		0x04, 0x80, 0x80, 0x80, 0x14, 0x40, 0x14, 0x00, 0x18, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x05, 0x80, 0x80, 0x80, 0x02, 0x11, 0x90,
		0x06, 0x80, 0x80, 0x80, 0x01, 0x02,
	}
	enviarBinario(t, ctx, conn, cameraMsgVideoConfig, avcc)
	enviarBinario(t, ctx, conn, cameraMsgVideoFrame, []byte{0x01, 0x00, 0x00, 0x01, 0x02, 0xaa, 0xbb})
	enviarBinario(t, ctx, conn, cameraMsgVideoFrame, []byte{0x00, 0x00, 0x00, 0x01, 0x23, 0xcc})
	enviarBinario(t, ctx, conn, cameraMsgAudioConfig, esds)
	enviarBinario(t, ctx, conn, cameraMsgAudioFrame, []byte{0x00, 0x00, 0x01, 0x10, 0x21, 0x20})

	var msgs []*relay.Message
	for i := 0; i < 50; i++ {
		if msgs = eng.mensajes(); len(msgs) >= 6 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(msgs) != 6 {
		t.Fatalf("llegaron %d mensajes, quería 6 (meta + 5)", len(msgs))
	}
	casos := []struct {
		nombre string
		got    *relay.Message
		want   relay.Message
	}{
		{"video config", msgs[1], relay.Message{Kind: relay.KindVideo, IsSeqHeader: true, IsKeyframe: true,
			Payload: append([]byte{0x17, 0x00, 0, 0, 0}, avcc...)}},
		{"keyframe", msgs[2], relay.Message{Kind: relay.KindVideo, Timestamp: 0x0102, IsKeyframe: true,
			Payload: []byte{0x17, 0x01, 0, 0, 0, 0xaa, 0xbb}}},
		{"inter", msgs[3], relay.Message{Kind: relay.KindVideo, Timestamp: 0x0123,
			Payload: []byte{0x27, 0x01, 0, 0, 0, 0xcc}}},
		{"audio config", msgs[4], relay.Message{Kind: relay.KindAudio, IsSeqHeader: true,
			Payload: []byte{0xaf, 0x00, 0x11, 0x90}}},
		{"audio frame", msgs[5], relay.Message{Kind: relay.KindAudio, Timestamp: 0x0110,
			Payload: []byte{0xaf, 0x01, 0x21, 0x20}}},
	}
	for _, c := range casos {
		g, w := c.got, c.want
		if g.Kind != w.Kind || g.Timestamp != w.Timestamp || g.IsKeyframe != w.IsKeyframe ||
			g.IsSeqHeader != w.IsSeqHeader || !bytes.Equal(g.Payload, w.Payload) {
			t.Errorf("%s = %+v (payload %x), quería %+v (payload %x)", c.nombre, g, g.Payload, w, w.Payload)
		}
	}
}

// Un mensaje que no se puede envolver cierra con 4003 y termina la sesión: seguir
// aceptando tras un frame corrupto mandaría basura a las plataformas.
func TestCameraClosesOnMalformedMessage(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	arrancar(t, ctx, conn)

	enviarBinario(t, ctx, conn, cameraMsgVideoFrame, []byte{0x01, 0x00}) // sin timestamp completo
	if got := esperarCierre(t, ctx, conn); got.Code != cameraCloseProtocol {
		t.Errorf("código de cierre = %d, quería %d", got.Code, cameraCloseProtocol)
	}
	esperarTerminadas(t, eng, 1)
}

// Cuando el cliente se va —con cierre limpio o sin él— la sesión se cierra en el motor:
// es lo que apaga los sinks y lo que WaitIdle necesita para el apagado limpio.
func TestCameraEndsTheSessionWhenTheClientLeaves(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	arrancar(t, ctx, conn)
	conn.Close(websocket.StatusNormalClosure, "")
	esperarTerminadas(t, eng, 1)

	conn2, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial 2: %v", err)
	}
	arrancar(t, ctx, conn2)
	conn2.CloseNow() // sin trama de cierre: el socket muere sin más
	esperarTerminadas(t, eng, 2)
}

// Un keyframe 1080p pasa de los 32 KiB que la librería acepta por defecto: el límite
// tiene que ser mayor. Y uno mayor que el límite cierra la sesión en vez de colgarla.
func TestCameraAcceptsBigFramesAndClosesOnHugeOnes(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	arrancar(t, ctx, conn)

	grande := append([]byte{0x01, 0, 0, 0, 0}, make([]byte, 512*1024)...)
	enviarBinario(t, ctx, conn, cameraMsgVideoFrame, grande)
	for i := 0; i < 50 && len(eng.mensajes()) < 2; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	// El payload envuelto mide lo mismo que el mensaje sin su byte de tipo: 5 de cabecera
	// FLV en lugar de los 5 de flags + timestamp.
	if msgs := eng.mensajes(); len(msgs) != 2 || len(msgs[1].Payload) != len(grande) {
		t.Fatalf("un frame de 512 KiB no llegó entero: %d mensajes", len(msgs))
	}

	enorme := append([]byte{0x01, 0, 0, 0, 0}, make([]byte, cameraReadLimit+1)...)
	escritura, cancelEscritura := context.WithTimeout(ctx, 8*time.Second)
	_ = conn.Write(escritura, websocket.MessageBinary, append([]byte{cameraMsgVideoFrame}, enorme...))
	cancelEscritura()
	esperarTerminadas(t, eng, 1)
}
