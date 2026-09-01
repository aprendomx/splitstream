package crypto_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
)

func TestSecretMasksWhenFormatted(t *testing.T) {
	s := crypto.Secret("live_abcdefgh1234")

	for _, verb := range []string{"%s", "%v", "%q"} {
		out := fmt.Sprintf(verb, s)
		if strings.Contains(out, "abcdefgh") {
			t.Errorf("fmt %s filtró el secreto: %s", verb, out)
		}
		if !strings.Contains(out, "••••1234") {
			t.Errorf("fmt %s = %s, quería que contuviera ••••1234", verb, out)
		}
	}
}

func TestSecretMaskShortValue(t *testing.T) {
	if got := crypto.Secret("ab").Mask(); got != "••••" {
		t.Errorf("Mask = %q, quería \"••••\"", got)
	}
	if got := crypto.Secret("").Mask(); got != "••••" {
		t.Errorf("Mask de vacío = %q, quería \"••••\"", got)
	}
	if got := crypto.Secret("abcd").Mask(); got != "••••abcd" {
		t.Errorf("Mask = %q, quería \"••••abcd\"", got)
	}
}

func TestSecretLast4(t *testing.T) {
	if got := crypto.Secret("live_abcdefgh1234").Last4(); got != "1234" {
		t.Errorf("Last4 = %q, quería \"1234\"", got)
	}
	if got := crypto.Secret("ab").Last4(); got != "" {
		t.Errorf("Last4 de un valor corto = %q, quería \"\"", got)
	}
}

func TestSecretLogValueMasks(t *testing.T) {
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).
		Info("destino", "key", crypto.Secret("live_abcdefgh1234"))

	out := buf.String()
	if strings.Contains(out, "abcdefgh") {
		t.Errorf("slog filtró el secreto: %s", out)
	}
	if !strings.Contains(out, "1234") {
		t.Errorf("slog debería mostrar la máscara: %s", out)
	}
}

func TestSecretRevealReturnsPlaintext(t *testing.T) {
	if got := crypto.Secret("live_abcdefgh1234").Reveal(); got != "live_abcdefgh1234" {
		t.Errorf("Reveal = %q", got)
	}
}
