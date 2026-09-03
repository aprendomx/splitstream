package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsServer levanta el servidor sobre httptest y devuelve la URL ws:// y las cookies de una
// sesión ya iniciada.
func wsServer(t *testing.T) (*Server, string, []*http.Cookie) {
	t.Helper()
	srv, _, _, _, cookies := newDestServer(t)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return srv, "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws", cookies
}

// dialWS conecta al WebSocket llevando las cookies de sesión.
func dialWS(ctx context.Context, url string, cookies []*http.Cookie) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	var partes []string
	for _, c := range cookies {
		partes = append(partes, c.Name+"="+c.Value)
	}
	if len(partes) > 0 {
		h.Set("Cookie", strings.Join(partes, "; "))
	}
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
}

// TestWebSocketRequiresASession: el handshake es una petición HTTP normal y lleva la
// cookie, así que el WS se protege igual que el resto. Sin ella, no hay upgrade.
func TestWebSocketRequiresASession(t *testing.T) {
	_, url, _ := wsServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, resp, err := dialWS(ctx, url, nil)
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("el WebSocket aceptó una conexión sin sesión")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("código = %d, quería 401", resp.StatusCode)
	}
}

// TestWebSocketPushesStatus: al conectar llega un statusDTO enseguida —la interfaz quiere
// pintar algo ya, no dentro de un segundo— y luego sigue llegando.
func TestWebSocketPushesStatus(t *testing.T) {
	_, url, cookies := wsServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	for i := 0; i < 2; i++ {
		leer, cancelLeer := context.WithTimeout(ctx, 4*time.Second)
		_, data, err := conn.Read(leer)
		cancelLeer()
		if err != nil {
			t.Fatalf("mensaje %d: %v", i+1, err)
		}

		var st statusDTO
		if err := json.Unmarshal(data, &st); err != nil {
			t.Fatalf("mensaje %d no es un statusDTO: %v — %s", i+1, err, data)
		}
		if st.Ingest.App == "" {
			t.Errorf("mensaje %d llegó sin la app de ingesta: %s", i+1, data)
		}
	}
}

// TestWebSocketPayloadMatchesTheRESTSnapshot es lo que sostiene la decisión de compartir
// tipo entre GET /api/status y el WS (spec §10): la interfaz arranca con el snapshot REST
// y sigue con el WS, y eso solo funciona si las dos formas son idénticas.
//
// Compara los deserializados, no los bytes: el orden de las claves de un JSON no es
// significativo y compararlo daría fallos falsos.
func TestWebSocketPayloadMatchesTheRESTSnapshot(t *testing.T) {
	srv, url, cookies := wsServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	leer, cancelLeer := context.WithTimeout(ctx, 4*time.Second)
	_, viaWS, err := conn.Read(leer)
	cancelLeer()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	rec := do(t, srv, cookies, http.MethodGet, "/api/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/status: %d", rec.Code)
	}

	var porWS, porREST map[string]any
	if err := json.Unmarshal(viaWS, &porWS); err != nil {
		t.Fatalf("WS: %v", err)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &porREST); err != nil {
		t.Fatalf("REST: %v", err)
	}

	for k := range porREST {
		if _, ok := porWS[k]; !ok {
			t.Errorf("el WS no manda la clave %q que sí trae GET /api/status", k)
		}
	}
	for k := range porWS {
		if _, ok := porREST[k]; !ok {
			t.Errorf("el WS manda la clave %q que GET /api/status no trae", k)
		}
	}
}

// TestWebSocketNeverPushesAKey: es el payload que más viaja de toda la API —uno por
// segundo mientras el panel esté abierto—, así que es donde más caro sale un descuido.
func TestWebSocketNeverPushesAKey(t *testing.T) {
	srv, url, cookies := wsServer(t)
	const clave = "clave-de-destino-inconfundible"
	crearDest(t, srv.db, srv, "yt", clave, true)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	leer, cancelLeer := context.WithTimeout(ctx, 4*time.Second)
	_, data, err := conn.Read(leer)
	cancelLeer()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if strings.Contains(string(data), clave) {
		t.Errorf("el WebSocket empujó la clave de un destino: %s", data)
	}
}

// TestWebSocketStopsWhenTheClientGoesAway: si el navegador cierra la pestaña, la goroutine
// del push tiene que terminar. Un WS mal escrito filtra una goroutine por pestaña cerrada,
// y en un proceso que retransmite durante horas eso se acumula.
func TestWebSocketStopsWhenTheClientGoesAway(t *testing.T) {
	_, url, cookies := wsServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Una conexión de calentamiento, para que lo que se crea una sola vez no cuente como
	// fuga en la medición.
	conn0, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial de calentamiento: %v", err)
	}
	leer, cancelLeer := context.WithTimeout(ctx, 4*time.Second)
	conn0.Read(leer)
	cancelLeer()
	conn0.Close(websocket.StatusNormalClosure, "")
	time.Sleep(500 * time.Millisecond)

	runtime.GC()
	antes := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		conn, _, err := dialWS(ctx, url, cookies)
		if err != nil {
			t.Fatalf("Dial %d: %v", i, err)
		}
		leer, cancelLeer := context.WithTimeout(ctx, 4*time.Second)
		if _, _, err := conn.Read(leer); err != nil {
			cancelLeer()
			t.Fatalf("Read %d: %v", i, err)
		}
		cancelLeer()
		conn.Close(websocket.StatusNormalClosure, "")
	}

	// Margen para que las goroutines se enteren y salgan.
	var despues int
	for i := 0; i < 25; i++ {
		time.Sleep(200 * time.Millisecond)
		runtime.GC()
		despues = runtime.NumGoroutine()
		if despues <= antes+2 {
			return
		}
	}
	t.Errorf("goroutines: %d antes, %d después de 5 conexiones cerradas — parece una fuga",
		antes, despues)
}

// TestWebSocketSurvivesASlowClient: un cliente que no lee llena el buffer del socket. Sin
// un plazo por escritura, el bucle del push se queda ahí para siempre y la goroutine no
// sale nunca, ni cerrando el navegador.
func TestWebSocketSurvivesASlowClient(t *testing.T) {
	if testing.Short() {
		t.Skip("tarda ~10 s; la CI no usa -short, así que allí sí corre")
	}

	_, url, cookies := wsServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	// No se lee NADA a propósito: este cliente está atascado.

	runtime.GC()
	antes := runtime.NumGoroutine()

	// Con el push a 1 s y el plazo de escritura a 2 s, en 10 s el servidor debe haber
	// desistido por su cuenta.
	time.Sleep(10 * time.Second)
	conn.Close(websocket.StatusNormalClosure, "")

	for i := 0; i < 25; i++ {
		time.Sleep(200 * time.Millisecond)
		runtime.GC()
		if runtime.NumGoroutine() <= antes+2 {
			return
		}
	}
	t.Errorf("un cliente que no lee dejó goroutines vivas: %d antes, %d después",
		antes, runtime.NumGoroutine())
}
