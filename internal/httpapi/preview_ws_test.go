package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aprendomx/splitstream/internal/relay"
)

// previewServer levanta el servidor real sobre httptest y devuelve el fake del motor,
// la URL ws:// del endpoint y las cookies de una sesión iniciada.
func previewServer(t *testing.T) (*fakeEngine, string, []*http.Cookie) {
	t.Helper()
	srv, _, eng, _, cookies := newDestServer(t)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return eng, "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/preview/ws", cookies
}

func leerBinario(t *testing.T, ctx context.Context, conn *websocket.Conn) []byte {
	t.Helper()
	leer, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	tipo, data, err := conn.Read(leer)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tipo != websocket.MessageBinary {
		t.Fatalf("llegó un mensaje %v; el protocolo es binario", tipo)
	}
	return data
}

// El handshake lleva la cookie: sin sesión no hay upgrade, igual que en el WS de estado.
func TestPreviewRequiresASession(t *testing.T) {
	_, url, _ := previewServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, resp, err := dialWS(ctx, url, nil)
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("la vista previa aceptó una conexión sin sesión")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("código = %d, quería 401", resp.StatusCode)
	}
}

// Sin emisión no hay nada que enseñar: se acepta y se cierra en seguida con el código de
// aplicación, que cubre además la carrera de pulsar el botón justo cuando se corta.
func TestPreviewClosesWithoutSignal(t *testing.T) {
	_, url, cookies := previewServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	leer, cancelLeer := context.WithTimeout(ctx, 4*time.Second)
	_, _, err = conn.Read(leer)
	cancelLeer()
	if err == nil {
		t.Fatal("sin emisión llegó un mensaje; quería el cierre «sin señal»")
	}
	if got := websocket.CloseStatus(err); got != previewCloseNoSignal {
		t.Fatalf("código de cierre = %d; quería %d", got, previewCloseNoSignal)
	}
}

// Con emisión: lo primero es la config (0x01 + avcC) y después cada frame (0x02) con
// flags, timestamp big-endian y los NALUs, que son el payload FLV sin sus 5 bytes.
func TestPreviewSendsConfigThenFrames(t *testing.T) {
	eng, url, cookies := previewServer(t)
	avcc := []byte{0x01, 0x64, 0x00, 0x1f, 0xff}
	eng.setLive(7)
	eng.setVideoConfig(seqPayload(avcc...))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	config := leerBinario(t, ctx, conn)
	if want := append([]byte{previewMsgConfig}, avcc...); !bytes.Equal(config, want) {
		t.Fatalf("primer mensaje = %x; quería la config %x", config, want)
	}

	eng.emitir(&relay.Message{
		Kind: relay.KindVideo, Timestamp: 0x0102, IsKeyframe: true,
		Payload: framePayload(0xaa, 0xbb),
	})
	frame := leerBinario(t, ctx, conn)
	want := []byte{previewMsgFrame, 0x01, 0x00, 0x00, 0x01, 0x02, 0xaa, 0xbb}
	if !bytes.Equal(frame, want) {
		t.Fatalf("frame = %x; quería %x", frame, want)
	}
}

// Si el publisher renegocia a mitad, el sequence header nuevo atraviesa el tap y sale
// como una segunda config por el mismo WebSocket.
func TestPreviewResendsConfigOnRenegotiation(t *testing.T) {
	eng, url, cookies := previewServer(t)
	eng.setLive(7)
	eng.setVideoConfig(seqPayload(0x01, 0x64, 0x00, 0x1f))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	leerBinario(t, ctx, conn) // la config inicial

	avcc2 := []byte{0x01, 0x64, 0x00, 0x28}
	eng.emitir(&relay.Message{
		Kind: relay.KindVideo, IsSeqHeader: true, IsKeyframe: true,
		Payload: seqPayload(avcc2...),
	})
	got := leerBinario(t, ctx, conn)
	if want := append([]byte{previewMsgConfig}, avcc2...); !bytes.Equal(got, want) {
		t.Fatalf("tras renegociar llegó %x; quería la config nueva %x", got, want)
	}
}

// El fin de la emisión cierra el tap, y el cliente ve el código «la emisión terminó».
func TestPreviewClosesWhenTheSessionEnds(t *testing.T) {
	eng, url, cookies := previewServer(t)
	eng.setLive(7)
	eng.setVideoConfig(seqPayload(0x01, 0x64, 0x00, 0x1f))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	leerBinario(t, ctx, conn) // la config inicial

	eng.cerrarTap()

	leer, cancelLeer := context.WithTimeout(ctx, 4*time.Second)
	_, _, err = conn.Read(leer)
	cancelLeer()
	if got := websocket.CloseStatus(err); got != previewCloseEnded {
		t.Fatalf("código de cierre = %d (err %v); quería %d", got, err, previewCloseEnded)
	}
}

// Cuando el cliente se va, el tap se libera: sin esto cada vista abierta y abandonada
// dejaría un consumidor colgado del hub durante toda la emisión.
func TestPreviewReleasesTheTapWhenTheClientLeaves(t *testing.T) {
	eng, url, cookies := previewServer(t)
	eng.setLive(7)
	eng.setVideoConfig(seqPayload(0x01, 0x64, 0x00, 0x1f))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	leerBinario(t, ctx, conn) // la config: el handler ya está en su bucle
	conn.Close(websocket.StatusNormalClosure, "")

	for i := 0; i < 50; i++ {
		if eng.liberados() >= 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("el tap no se liberó tras cerrar el cliente: liberados = %d", eng.liberados())
}

// Un cliente que no lee no puede retener el tap para siempre: el plazo de escritura de
// 2 s corta y libera, la misma defensa que el WS de estado.
func TestPreviewSurvivesASlowClient(t *testing.T) {
	if testing.Short() {
		t.Skip("tarda varios segundos; la CI no usa -short, así que allí sí corre")
	}

	eng, url, cookies := previewServer(t)
	eng.setLive(7)
	eng.setVideoConfig(seqPayload(0x01, 0x64, 0x00, 0x1f))

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	// No se lee NADA a propósito: este cliente está atascado.

	// Frames grandes hasta llenar los buffers TCP: entonces la escritura se atasca, el
	// plazo vence y el handler libera el tap.
	grande := &relay.Message{
		Kind: relay.KindVideo, IsKeyframe: true,
		Payload: append([]byte{0x17, 0x01, 0x00, 0x00, 0x00}, make([]byte, 256*1024)...),
	}
	plazo := time.After(30 * time.Second)
	for eng.liberados() == 0 {
		select {
		case <-plazo:
			t.Fatal("el tap no se liberó: el cliente atascado retuvo la vista previa")
		default:
		}
		eng.emitir(grande)
		time.Sleep(20 * time.Millisecond)
	}
}
