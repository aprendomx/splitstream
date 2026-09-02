package rtmpio

import (
	"context"
	"errors"
	"net"
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

func TestParseTargetNestedApp(t *testing.T) {
	got, err := parseTarget("rtmp://example.com/live/sub")
	if err != nil {
		t.Fatalf("parseTarget: %v", err)
	}
	if got.app != "live/sub" {
		t.Errorf("app = %q, quería \"live/sub\"", got.app)
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
