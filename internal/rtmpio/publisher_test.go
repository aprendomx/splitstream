package rtmpio

import (
	"errors"
	"testing"

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
