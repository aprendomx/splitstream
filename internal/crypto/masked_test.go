package crypto_test

import (
	"bytes"
	"encoding/json"
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

// encoding/json ignora fmt.Stringer: sin MarshalJSON, un Secret embebido en una
// respuesta de API saldría en claro por el cable. Este test convierte en invariante
// lo que hoy solo es un comentario en el tipo.
func TestSecretMasksWhenMarshaledToJSON(t *testing.T) {
	blob, err := json.Marshal(struct {
		Key crypto.Secret `json:"key"`
	}{"live_abcdefgh1234"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(blob), "abcdefgh") {
		t.Errorf("json.Marshal filtró el secreto: %s", blob)
	}
	if !strings.Contains(string(blob), "1234") {
		t.Errorf("json.Marshal debería emitir la máscara: %s", blob)
	}
}

func TestSecretStillUnmarshalsFromPlainJSON(t *testing.T) {
	var in struct {
		Key crypto.Secret `json:"key"`
	}
	if err := json.Unmarshal([]byte(`{"key":"live_abcdefgh1234"}`), &in); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if in.Key.Reveal() != "live_abcdefgh1234" {
		t.Errorf("Reveal = %q: la entrada debe seguir aceptando la clave en claro", in.Key.Reveal())
	}
}
