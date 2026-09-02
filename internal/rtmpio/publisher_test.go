package rtmpio

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
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
	time.Sleep(200 * time.Millisecond)

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
	time.Sleep(200 * time.Millisecond)

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
