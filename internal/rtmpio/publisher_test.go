package rtmpio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
)

func TestParseTargetRTMP(t *testing.T) {
	got, err := parseTarget("rtmp://a.rtmp.youtube.com/live2")
	if err != nil {
		t.Fatalf("parseTarget: %v", err)
	}
	// go-rtmp exige el literal "rtmp" para Dial (spec §16.1).
	if got.scheme != "rtmp" {
		t.Errorf("scheme = %q, quería \"rtmp\"", got.scheme)
	}
	if got.addr != "a.rtmp.youtube.com:1935" {
		t.Errorf("addr = %q, quería el puerto 1935 por defecto", got.addr)
	}
	if got.app != "live2" {
		t.Errorf("app = %q, quería \"live2\"", got.app)
	}
}

func TestParseTargetRTMPS(t *testing.T) {
	got, err := parseTarget("rtmps://live-api-s.facebook.com:443/rtmp/")
	if err != nil {
		t.Fatalf("parseTarget: %v", err)
	}
	// go-rtmp exige el literal "rtmps" para TLSDial (spec §16.1).
	if got.scheme != "rtmps" {
		t.Errorf("scheme = %q, quería \"rtmps\"", got.scheme)
	}
	if got.addr != "live-api-s.facebook.com:443" {
		t.Errorf("addr = %q", got.addr)
	}
	if got.app != "rtmp" {
		t.Errorf("app = %q, quería \"rtmp\" sin la barra final", got.app)
	}
}

func TestParseTargetRTMPSDefaultPort(t *testing.T) {
	got, err := parseTarget("rtmps://example.com/app")
	if err != nil {
		t.Fatalf("parseTarget: %v", err)
	}
	if got.addr != "example.com:443" {
		t.Errorf("addr = %q, quería el puerto 443 por defecto en rtmps", got.addr)
	}
}

// La app RTMP es el PATH ENTERO de la URL del servidor: con rtmp://host/a/b la app es
// "a/b". Recortarla al primer segmento rompería los destinos con app anidada, como un
// nginx-rtmp propio. Lo que se recorta es lo que se loguea, que es otro valor.
func TestParseTargetNestedApp(t *testing.T) {
	got, err := parseTarget("rtmp://example.com/live/sub")
	if err != nil {
		t.Fatalf("parseTarget: %v", err)
	}
	if got.app != "live/sub" {
		t.Errorf("app = %q, quería \"live/sub\"", got.app)
	}
	if got.logApp != "live" {
		t.Errorf("logApp = %q, quería \"live\": al log solo va el primer segmento", got.logApp)
	}
}

// El caso real que filtraba: una clave pegada al final de la URL del destino. Por el cable
// va tal cual —es lo que el usuario configuró— pero al log solo va el primer segmento.
func TestParseTargetKeepsPastedKeyOffTheLogApp(t *testing.T) {
	const key = "live_987654_SUPERSECRETSTREAMKEY"
	got, err := parseTarget("rtmp://a.rtmp.youtube.com/live2/" + key)
	if err != nil {
		t.Fatalf("parseTarget: %v", err)
	}
	if got.app != "live2/"+key {
		t.Errorf("app = %q: por el cable va el path entero, sin recortar", got.app)
	}
	if got.logApp != "live2" {
		t.Errorf("logApp = %q, quería \"live2\"", got.logApp)
	}
	if strings.Contains(got.logApp, "SUPERSECRETSTREAMKEY") {
		t.Errorf("lo que se loguea lleva la clave dentro: %q", got.logApp)
	}
}

// Un path de un solo segmento es AMBIGUO: puede ser una app normal (`/live2`) o una app
// con la clave pegada (`/live_987_CLAVE`), y desde el código no se distinguen. Como la §8
// no admite un "casi nunca", en ese caso no hay nada que loguear.
func TestParseTargetHasNoLogAppForFlatPaths(t *testing.T) {
	for _, tc := range []struct{ name, url, wantApp string }{
		{"app plana normal", "rtmp://a.rtmp.youtube.com/live2", "live2"},
		{"clave pegada sin barra", "rtmp://live.example.com/live_987_SUPERSECRETSTREAMKEY9x7", "live_987_SUPERSECRETSTREAMKEY9x7"},
		{"barra escapada", "rtmp://example.com/%2fSUPERSECRETSTREAMKEY9x7", "SUPERSECRETSTREAMKEY9x7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTarget(tc.url)
			if err != nil {
				t.Fatalf("parseTarget: %v", err)
			}
			// Lo que va por el cable no cambia: es lo que el usuario configuró.
			if got.app != tc.wantApp {
				t.Errorf("app = %q, quería %q: la app de red no se toca", got.app, tc.wantApp)
			}
			if got.logApp != "" {
				t.Errorf("logApp = %q: un path de un solo segmento no se puede loguear", got.logApp)
			}
		})
	}
}

func TestParseTargetRejectsBadScheme(t *testing.T) {
	for _, raw := range []string{
		"http://example.com/live",
		"example.com/live",
		"",
	} {
		if _, err := parseTarget(raw); !errors.Is(err, ErrUnsupportedScheme) {
			t.Errorf("parseTarget(%q) = %v, quería ErrUnsupportedScheme", raw, err)
		}
	}
}

func TestParseTargetRejectsMissingHostOrApp(t *testing.T) {
	for _, raw := range []string{"rtmp:///live", "rtmp://example.com", "rtmp://example.com/"} {
		if _, err := parseTarget(raw); err == nil {
			t.Errorf("parseTarget(%q) = nil, quería error", raw)
		}
	}
}

func TestNewPublisherValidatesURL(t *testing.T) {
	if _, err := NewPublisher(PublisherConfig{
		URL:       "http://example.com/live",
		StreamKey: crypto.Secret("k"),
	}); !errors.Is(err, ErrUnsupportedScheme) {
		t.Error("NewPublisher debe rechazar una URL con esquema no soportado")
	}
}

// El error de NewPublisher no puede reproducir la stream key.
func TestNewPublisherErrorDoesNotLeakKey(t *testing.T) {
	_, err := NewPublisher(PublisherConfig{
		URL:       "http://example.com/live",
		StreamKey: crypto.Secret("clave-secreta-1234"),
	})
	if err == nil {
		t.Fatal("quería error")
	}
	if got := err.Error(); got != "" && contains(got, "clave-secreta") {
		t.Errorf("el error filtró la clave: %s", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// Close antes de Connect no debe entrar en pánico.
func TestPublisherCloseBeforeConnect(t *testing.T) {
	p, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://example.com/live",
		StreamKey: crypto.Secret("k"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close antes de Connect = %v, quería nil", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close es idempotente: segunda llamada = %v", err)
	}
}

// Connect debe respetar la cancelación del contexto aunque go-rtmp no lo acepte.
func TestConnectHonoursContextCancellation(t *testing.T) {
	// Un listener que acepta la conexión y luego no dice nada: simula un destino
	// detrás de un firewall que descarta paquetes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Aceptar y callar. No cerrar: ese es justo el caso malo.
			_ = conn
		}
	}()

	p, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://" + ln.Addr().String() + "/live",
		StreamKey: crypto.Secret("k"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = p.Connect(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Connect debería fallar contra un peer que no responde")
	}
	if elapsed > 3*time.Second {
		t.Errorf("Connect tardó %v: no respetó la cancelación del contexto", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Logf("aviso: el error no envuelve DeadlineExceeded (%v); acceptable si el dialer falló antes", err)
	}
}

// Sin deadline en el contexto, Connect sigue acotado por connectTimeout y no cuelga
// indefinidamente. Aquí solo se comprueba que la constante existe y es razonable.
func TestConnectTimeoutIsBounded(t *testing.T) {
	if connectTimeout <= 0 || connectTimeout > 60*time.Second {
		t.Errorf("connectTimeout = %v: debe ser positivo y acotado", connectTimeout)
	}
}

// connectTimeout debe acotar Connect aunque quien llama pase un contexto sin deadline:
// es el caso real, porque main.go pasa el contexto de señales, que es cancelable pero
// no tiene plazo.
func TestConnectBoundedWithoutCallerDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("tarda connectTimeout")
	}

	// Un listener que acepta y se calla, sin cerrar: el caso malo.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c // retener la referencia para que no se cierre sola
		}
	}()

	p, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://" + ln.Addr().String() + "/live",
		StreamKey: crypto.Secret("k"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer p.Close()

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- p.Connect(context.Background()) }()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("Connect debería fallar contra un peer que no responde")
		}
		// Debe rondar connectTimeout, no colgarse.
		if elapsed > connectTimeout+5*time.Second {
			t.Errorf("Connect tardó %v, connectTimeout es %v", elapsed, connectTimeout)
		}
		t.Logf("Connect retornó en %v con: %v", elapsed, err)
	case <-time.After(connectTimeout + 10*time.Second):
		t.Fatalf("Connect no retornó en %v: connectTimeout no acota nada", connectTimeout+10*time.Second)
	}
}

// El cierre ordenado manda FCUnpublish antes de deleteStream (spec §6.5). Sin conexión,
// Close debe seguir siendo seguro.
func TestCloseSendsFCUnpublishWhenConnected(t *testing.T) {
	rec := &recorder{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := NewIngest(IngestConfig{Addr: ln.Addr().String(), Handler: rec})
	go ing.Serve(ln)
	defer ing.Close()
	esperaAceptando(t, ln.Addr().String())

	p, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://" + ln.Addr().String() + "/live",
		StreamKey: crypto.Secret("clave"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Close no debe fallar ni colgarse mandando FCUnpublish.
	done := make(chan error, 1)
	go func() { done <- p.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close = %v, quería nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close se colgó")
	}
}

// El error de parseTarget no puede reproducir la URL: la clave suele ir pegada dentro.
// Se comprueba en el texto del error y en la línea de log que main.go escribe con él.
func TestParseTargetErrorNeverLeaksURL(t *testing.T) {
	const key = "SUPERSECRETSTREAMKEY"
	malformed := []string{
		"rtmp://exa mple.com/live/" + key,   // carácter inválido en el host
		"rtmp://%zz/live/" + key,            // escape inválido
		"rtmp://host:puerto/live/" + key,    // puerto inválido
		"://host/live/" + key,               // sin esquema
		"rtmp://[::1/live/" + key,           // corchete sin cerrar
		"http://example.com/live/" + key,    // esquema no soportado
		"live2/live_" + key,                 // sin esquema ni host
		"rtmp://example.com/?stream=" + key, // la clave en la query, sin app
	}

	for _, raw := range malformed {
		_, err := parseTarget(raw)
		if err == nil {
			t.Errorf("parseTarget(<url malformada>) = nil, quería error")
			continue
		}
		if strings.Contains(err.Error(), key) {
			t.Errorf("el error filtró la clave: %s", err)
		}
		// Y tampoco puede filtrarla al escribirse en el log, que es lo que hace
		// cmd/splitstream/main.go con el error de NewPublisher.
		var buf bytes.Buffer
		slog.New(slog.NewTextHandler(&buf, nil)).Error("destino mal configurado", "err", err)
		if strings.Contains(buf.String(), key) {
			t.Errorf("la línea de log filtró la clave: %s", buf.String())
		}
	}
}

// Ninguna línea del publisher lleva la clave, ni en claro ni enmascarada (spec §8). El
// caso malo es el destino con la clave pegada en el path, que acababa en `destino_app`.
func TestPublisherLogsNeverContainTheKey(t *testing.T) {
	const key = "live_987654_SUPERSECRETSTREAMKEY"

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	p, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://a.rtmp.youtube.com/live2/" + key,
		StreamKey: crypto.Secret(key),
		Logger:    logger,
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer p.Close()

	// Los atributos fijos del publisher salen en todas sus líneas: basta con provocar una.
	p.log.Info("publicando en el destino")

	out := buf.String()
	if strings.Contains(out, "SUPERSECRETSTREAMKEY") {
		t.Errorf("el log filtró la clave: %s", out)
	}
	if strings.Contains(out, crypto.Secret(key).Mask()) || strings.Contains(out, crypto.Secret(key).Last4()) {
		t.Errorf("el log lleva la clave enmascarada, y el spec §8 no admite matices: %s", out)
	}
	if !strings.Contains(out, "destino_app=live2") {
		t.Errorf("el log debería identificar la app (el primer segmento): %s", out)
	}
}

// La app que viaja por el cable es el path entero de la URL del destino, no su primer
// segmento: es la URL de un servidor RTMP y la clave va en un campo aparte. Recortarla
// rompería un nginx-rtmp propio con la app anidada, que es el caso "RTMP genérico" del
// spec. Este es el test que faltaba cuando el recorte se coló.
func TestConnectUsesFullPathAsApp(t *testing.T) {
	rec := &recorder{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := NewIngest(IngestConfig{Addr: ln.Addr().String(), Handler: rec})
	// Se espera a que Serve retorne en vez de dejarlo suelto: con -count=N los tests
	// comparten proceso, y una goroutine de servidor que sobreviva al test se solapa con
	// la iteración siguiente.
	served := make(chan error, 1)
	go func() { served <- ing.Serve(ln) }()
	t.Cleanup(func() {
		_ = ing.Close()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
			t.Error("el servidor de ingesta no terminó tras Close: deja goroutines sueltas")
		}
	})
	esperaAceptando(t, ln.Addr().String())

	p, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://" + ln.Addr().String() + "/a/b",
		StreamKey: crypto.Secret("clave"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer p.Close()

	if p.tgt.app != "a/b" {
		t.Errorf("target.app = %q, quería \"a/b\"", p.tgt.app)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Stream.Publish no espera el onStatus, así que el servidor puede no haber procesado
	// todavía el comando cuando Connect retorna.
	deadline := time.Now().Add(5 * time.Second)
	for rec.lastApp() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// Lo que recibió el servidor es lo único que zanja la pregunta.
	if got := rec.lastApp(); got != "a/b" {
		t.Errorf("el servidor recibió la app %q, quería \"a/b\": el recorte volvió a colarse", got)
	}
}

// Con el path plano no se emite `destino_app` en ninguna línea, así que la clave pegada no
// puede salir por ahí. Es el hueco que dejaba `strings.Cut` sin separador.
func TestPublisherOmitsLogAppForFlatPaths(t *testing.T) {
	const key = "SUPERSECRETSTREAMKEY9x7"
	for _, tc := range []struct {
		name       string
		url        string
		wantApp    string // lo que va por el cable, que no puede cambiar
		wantLogApp string // "" = no se emite el atributo
	}{
		{"clave pegada sin barra", "rtmp://live.example.com/live_987_" + key, "live_987_" + key, ""},
		{"barra escapada", "rtmp://example.com/%2f" + key, key, ""},
		{"app anidada", "rtmp://example.com/live/sub", "live/sub", "live"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			p, err := NewPublisher(PublisherConfig{
				URL:       tc.url,
				StreamKey: crypto.Secret("otra-clave"),
				Logger:    logger,
			})
			if err != nil {
				t.Fatalf("NewPublisher: %v", err)
			}
			defer p.Close()

			if p.tgt.app != tc.wantApp {
				t.Errorf("la app de red cambió: %q, quería %q", p.tgt.app, tc.wantApp)
			}

			// Los atributos fijos salen en todas las líneas: basta con provocar una.
			p.log.Info("publicando en el destino")
			out := buf.String()

			if strings.Contains(out, key) {
				t.Errorf("el log filtró la clave pegada en la URL: %s", out)
			}
			if tc.wantLogApp == "" {
				if strings.Contains(out, "destino_app") {
					t.Errorf("con un path plano no se puede emitir destino_app: %s", out)
				}
			} else if !strings.Contains(out, "destino_app="+tc.wantLogApp) {
				t.Errorf("faltó destino_app=%s: %s", tc.wantLogApp, out)
			}
			if !strings.Contains(out, "destino_addr=") {
				t.Errorf("el log debería seguir identificando el destino por su addr: %s", out)
			}
		})
	}
}

// ingestaQueNoDrena es un IngestHandler que acepta al publisher y luego se queda clavado
// en el primer mensaje de media.
//
// go-rtmp llama al handler DESDE la goroutine que lee el socket de la conexión, así que
// bloquearse en OnMessage es exactamente «dejar de leer»: los búferes TCP se llenan y el
// Write del cliente acaba bloqueándose, que es el caso real de una plataforma que deja de
// consumir sin cerrar la conexión.
type ingestaQueNoDrena struct {
	bloqueo chan struct{} // se cierra al terminar el test para soltar la goroutine
}

func (h *ingestaQueNoDrena) OnPublishStart(app, streamKey string) error { return nil }
func (h *ingestaQueNoDrena) OnMessage(msg *relay.Message)               { <-h.bloqueo }
func (h *ingestaQueNoDrena) OnPublishEnd()                              {}

// servidorRTMPQueNoDrena levanta una ingesta que completa handshake, connect,
// createStream y publish y después deja de leer del socket. Devuelve su dirección y una
// función que vuelve a ponerla a drenar (idempotente).
func servidorRTMPQueNoDrena(t *testing.T) (addr string, liberar func()) {
	t.Helper()
	h := &ingestaQueNoDrena{bloqueo: make(chan struct{})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := NewIngest(IngestConfig{Addr: ln.Addr().String(), Handler: h})
	go ing.Serve(ln)
	liberar = sync.OnceFunc(func() { close(h.bloqueo) })
	// El orden importa: primero se suelta al handler y luego se cierra la ingesta. Al
	// revés, la goroutine de la conexión seguiría clavada en OnMessage.
	t.Cleanup(func() {
		liberar()
		ing.Close()
	})
	time.Sleep(200 * time.Millisecond)
	return ln.Addr().String(), liberar
}

// TestWriteToAStalledPeerFailsWithinThreeSeconds: escribir hacia un destino que aceptó la
// conexión y dejó de leer tiene que fallar en ≈3 s, no en los 5 s que go-rtmp v0.0.7 trae
// cableados en Stream.Write. El parche 1 hace ese plazo configurable y el Publisher lo
// fija en writeTimeout (spec v1.0 §3.1): cuanto antes falle la escritura, antes puede el
// sink tirar la conexión y reconectar.
//
// El plazo se mide sobre la llamada que falla, no desde el principio del bucle: llenar
// los búferes del socket lleva su tiempo y no es parte de lo que se está midiendo.
func TestWriteToAStalledPeerFailsWithinThreeSeconds(t *testing.T) {
	addr, liberar := servidorRTMPQueNoDrena(t)

	p, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://" + addr + "/live",
		StreamKey: crypto.Secret("clave"),
		ChunkSize: DefaultChunkSize,
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer p.Close()
	// Se ejecuta ANTES que p.Close() (los defer son LIFO): con el peer drenando otra vez,
	// el cierre ordenado no tiene que esperar a que expire ningún plazo.
	defer liberar()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Un tag de video válido (H.264, keyframe): si no lo fuera, la ingesta lo rechazaría
	// y cerraría la conexión, y el error vendría del cierre y no del plazo.
	frame := make([]byte, 64<<10)
	copy(frame, []byte{0x17, 0x01, 0x00, 0x00, 0x00})

	var (
		errEscritura error
		inicio       time.Time
		escritas     int
	)
	arranque := time.Now()
	for i := 0; i < 100000 && errEscritura == nil; i++ {
		inicio = time.Now()
		errEscritura = p.WriteVideo(uint32(i), frame)
		escritas = i
	}
	if errEscritura == nil {
		t.Fatal("el peer que no drena nunca produjo error")
	}
	d := time.Since(inicio)
	if d < writeTimeout-time.Second || d > writeTimeout+time.Second {
		t.Errorf("la escritura tardó %v en rendirse; quería ≈%v (5 s sería el valor cableado de upstream)", d, writeTimeout)
	}
	t.Logf("%d escrituras de 64 KiB llenaron el búfer en %v; la siguiente falló tras %v: %v",
		escritas, time.Since(arranque)-d, d, errEscritura)
}

// comandoRecibido es un comando de control que llegó a la ingesta, con el stream por el
// que llegó.
type comandoRecibido struct {
	nombre   string
	streamID uint32
}

// registroDeComandos es un IngestHandler que además anota, en orden, cada comando de
// control. Implementa el gancho opcional OnComando de ingest.go.
type registroDeComandos struct {
	recorder

	mu   sync.Mutex
	cmds []comandoRecibido
}

func (r *registroDeComandos) OnComando(nombre string, streamID uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, comandoRecibido{nombre: nombre, streamID: streamID})
}

func (r *registroDeComandos) registro() []comandoRecibido {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]comandoRecibido(nil), r.cmds...)
}

func (r *registroDeComandos) nombres() []string {
	out := []string{}
	for _, c := range r.registro() {
		out = append(out, c.nombre)
	}
	return out
}

// esperaComandos espera a que hayan llegado al menos n comandos. Los hooks se llaman
// desde la goroutine de la conexión, así que Connect puede volver antes de que el
// servidor haya terminado de procesar el último.
func (r *registroDeComandos) esperaComandos(t *testing.T, n int) {
	t.Helper()
	limite := time.Now().Add(5 * time.Second)
	for time.Now().Before(limite) {
		if len(r.registro()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("solo llegaron %v; quería al menos %d comandos", r.nombres(), n)
}

// TestPreCommandsGoThroughTheControlStreamBeforeCreateStream: con PreCommands, el
// publisher manda releaseStream y FCPublish por el stream de control ANTES de
// createStream, como FMLE/OBS (spec base §15.9, spec v1.0 §3.3); sin la opción —el valor
// por defecto— no manda ninguno de los dos.
//
// Sobre el stream id: go-rtmp no le pasa al handler el stream por el que llegó
// releaseStream o FCPublish, porque no hace falta: SOLO los despacha desde el handler del
// stream de control (server_control_connected_handler.go), nunca desde el de un stream de
// datos. Que el hook se dispare ya prueba que llegaron por el stream 0; para publish, en
// cambio, el StreamContext sí trae el id y ahí la comprobación de que NO es el 0 mide algo.
func TestPreCommandsGoThroughTheControlStreamBeforeCreateStream(t *testing.T) {
	for _, caso := range []struct {
		nombre string
		pre    bool
		want   []string
	}{
		{"con precomandos", true, []string{"connect", "releaseStream", "FCPublish", "createStream", "publish"}},
		{"sin precomandos", false, []string{"connect", "createStream", "publish"}},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			reg := &registroDeComandos{}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			ing := NewIngest(IngestConfig{Addr: ln.Addr().String(), Handler: reg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			go ing.Serve(ln)
			defer ing.Close()

			p, err := NewPublisher(PublisherConfig{
				URL:         "rtmp://" + ln.Addr().String() + "/live",
				StreamKey:   crypto.Secret("clave"),
				ChunkSize:   DefaultChunkSize,
				PreCommands: caso.pre,
				Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			if err != nil {
				t.Fatalf("NewPublisher: %v", err)
			}
			defer p.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := p.Connect(ctx); err != nil {
				t.Fatalf("Connect: %v", err)
			}
			// La foto se toma antes de Close a propósito: FCUnpublish llega después y no
			// es lo que esta prueba mide.
			reg.esperaComandos(t, len(caso.want))

			if got := reg.nombres(); !reflect.DeepEqual(got, caso.want) {
				t.Errorf("comandos = %v, quería %v", got, caso.want)
			}
			for _, c := range reg.registro() {
				switch c.nombre {
				case "releaseStream", "FCPublish":
					if c.streamID != 0 {
						t.Errorf("%s llegó por el stream %d, quería 0", c.nombre, c.streamID)
					}
				case "publish":
					if c.streamID == 0 {
						t.Error("publish llegó por el stream de control, quería uno de datos")
					}
				}
			}
		})
	}
}

// TestFCUnpublishGoesThroughTheSameStreamAsThePreCommands: con PreCommands, el cierre
// manda FCUnpublish por el stream de control (como FMLE); sin la opción sigue yendo por
// el stream de datos, que es el comportamiento de siempre.
func TestFCUnpublishGoesThroughTheSameStreamAsThePreCommands(t *testing.T) {
	for _, caso := range []struct {
		nombre string
		pre    bool
		// Con PreCommands, FCUnpublish llega por el stream de control y el handler lo ve
		// como OnFCUnpublish. Por el stream de datos, en cambio, go-rtmp no lo despacha:
		// el handler del stream que publica no conoce ese comando.
		wantFCUnpublish bool
	}{
		{"con precomandos", true, true},
		{"sin precomandos", false, false},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			reg := &registroDeComandos{}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			ing := NewIngest(IngestConfig{Addr: ln.Addr().String(), Handler: reg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			go ing.Serve(ln)
			defer ing.Close()

			p, err := NewPublisher(PublisherConfig{
				URL:         "rtmp://" + ln.Addr().String() + "/live",
				StreamKey:   crypto.Secret("clave"),
				PreCommands: caso.pre,
				Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			if err != nil {
				t.Fatalf("NewPublisher: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := p.Connect(ctx); err != nil {
				t.Fatalf("Connect: %v", err)
			}
			if err := p.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			vio := func() bool {
				for _, c := range reg.registro() {
					if c.nombre == "FCUnpublish" {
						return true
					}
				}
				return false
			}
			if caso.wantFCUnpublish {
				limite := time.Now().Add(5 * time.Second)
				for !vio() && time.Now().Before(limite) {
					time.Sleep(5 * time.Millisecond)
				}
			} else {
				// Un margen para que, si llegara, diera tiempo a registrarse.
				time.Sleep(200 * time.Millisecond)
			}
			if got := vio(); got != caso.wantFCUnpublish {
				t.Errorf("FCUnpublish por el stream de control = %v, quería %v (comandos: %v)", got, caso.wantFCUnpublish, reg.nombres())
			}
		})
	}
}

// esperaAceptando espera, con límite, a que el servidor de ingesta esté aceptando de
// verdad en addr. Sustituye al `time.Sleep(200ms)` que había antes: un plazo fijo se queda
// corto en un runner cargado —y entonces el test falla sin que nada esté roto— y sobra en
// todos los demás casos.
//
// La comprobación es abrir y cerrar una conexión. No molesta a la ingesta: go-rtmp la ve
// morir durante el handshake y la suelta, y ni OnPublishStart ni OnMessage llegan a
// dispararse.
func esperaAceptando(t *testing.T, addr string) {
	t.Helper()
	limite := time.Now().Add(5 * time.Second)
	for time.Now().Before(limite) {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("la ingesta en %s no aceptó conexiones en 5 s", addr)
}

// espia es un proxy TCP que se mete entre el publisher y la ingesta y guarda TODO lo que
// el publisher manda hacia el servidor.
//
// Hace falta porque el tamaño de chunk no se puede mirar de otra forma: ni go-rtmp ni la
// ingesta exponen el que acabó aplicándose, y a nivel de mensaje los dos tamaños dan el
// mismo resultado —el otro extremo reensambla igual—. La diferencia solo existe en el
// cable, y esto es el cable.
type espia struct {
	addr string

	mu  sync.Mutex
	buf []byte
}

// nuevoEspia levanta el proxy delante de destino y lo cierra al terminar el test.
func nuevoEspia(t *testing.T, destino string) *espia {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen del espía: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	e := &espia{addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go e.atiende(c, destino)
		}
	}()
	return e
}

func (e *espia) atiende(cliente net.Conn, destino string) {
	servidor, err := net.Dial("tcp", destino)
	if err != nil {
		cliente.Close()
		return
	}
	defer cliente.Close()
	defer servidor.Close()

	// La vuelta no se guarda: lo que se mide es lo que ESCRIBE el publisher.
	go io.Copy(cliente, servidor)

	buf := make([]byte, 32*1024)
	for {
		n, err := cliente.Read(buf)
		if n > 0 {
			e.mu.Lock()
			e.buf = append(e.buf, buf[:n]...)
			e.mu.Unlock()
			if _, werr := servidor.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// visto devuelve una copia de lo que el publisher lleva escrito.
func (e *espia) visto() []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]byte(nil), e.buf...)
}

// TestFirstVideoGoesOutWithTheNegotiatedChunkSize fija que el primer vídeo sale ya
// troceado al tamaño negociado con CreateStream, sin ningún `WriteSetChunkSize` extra
// detrás del publish.
//
// Ese WriteSetChunkSize existió mientras el tamaño lo aplicaba quien ENCOLABA el
// SetChunkSize: no se podía confiar en cuándo entraba en vigor. Desde el parche 4 de
// third_party/go-rtmp lo aplica la goroutine escritora justo después de poner el
// SetChunkSize en el hilo, así que el de CreateStream basta y el segundo era un mensaje
// repetido con el mismo valor. Esto es lo que lo comprueba en vez de suponerlo.
//
// Cómo se mide: el payload es más largo que los 128 bytes por defecto de RTMP y más corto
// que los 4096 que se negocian. Con el tamaño bueno el mensaje sale en UN chunk y sus
// bytes van seguidos en el cable; con 128 irían partidos, con una cabecera de continuación
// (fmt 3, chunk stream 5 → 0xC5) cada 128 bytes, y la comparación byte a byte falla.
func TestFirstVideoGoesOutWithTheNegotiatedChunkSize(t *testing.T) {
	rec := &recorder{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := NewIngest(IngestConfig{Addr: ln.Addr().String(), Handler: rec})
	go ing.Serve(ln)
	defer ing.Close()
	esperaAceptando(t, ln.Addr().String())

	esp := nuevoEspia(t, ln.Addr().String())

	p, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://" + esp.addr + "/live",
		StreamKey: crypto.Secret("clave"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Un tag de vídeo AVC válido (keyframe, NALU) con relleno detrás: la ingesta lo acepta
	// y el contenido, al ser distinto en cada byte, no puede confundirse con una cabecera.
	payload := make([]byte, 3000)
	for i := range payload {
		payload[i] = byte(i)
	}
	copy(payload, []byte{0x17, 0x01, 0x00, 0x00, 0x00})

	if err := p.WriteVideo(0, payload); err != nil {
		t.Fatalf("WriteVideo: %v", err)
	}

	// La cabecera del chunk: fmt 0 + chunk stream 5 (vídeo), timestamp 0, longitud del
	// mensaje en 3 bytes y tipo 9 (vídeo). Los 4 bytes del message stream id que siguen no
	// se comparan: el id lo decide CreateStream.
	const cabLen = 12
	cabecera := []byte{
		0x05,
		0x00, 0x00, 0x00,
		byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload)),
		0x09,
	}

	// La escritura es asíncrona: la saca la goroutine planificadora de go-rtmp.
	limite := time.Now().Add(5 * time.Second)
	for {
		b := esp.visto()
		if i := bytes.Index(b, cabecera); i >= 0 && len(b) >= i+cabLen+len(payload) {
			cuerpo := b[i+cabLen : i+cabLen+len(payload)]
			if !bytes.Equal(cuerpo, payload) {
				corte := bytes.IndexByte(cuerpo, 0xC5)
				t.Fatalf("el primer vídeo salió troceado: los %d bytes tras la cabecera no son el payload "+
					"(primer 0xC5 en el byte %d, con chunk de %d no debería haber ninguno)",
					len(payload), corte, DefaultChunkSize)
			}
			return
		}
		if !time.Now().Before(limite) {
			t.Fatalf("el vídeo no salió al cable en 5 s (%d bytes escritos)", len(b))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
