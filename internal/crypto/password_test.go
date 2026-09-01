package crypto_test

import (
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
)

func TestHashPasswordProducesPHCFormat(t *testing.T) {
	encoded, err := crypto.HashPassword("correcta-caballo-batería-grapa")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Errorf("formato inesperado: %s", encoded)
	}
	if strings.Contains(encoded, "caballo") {
		t.Errorf("el hash contiene la contraseña: %s", encoded)
	}
	if n := len(strings.Split(encoded, "$")); n != 6 {
		t.Errorf("quería 6 segmentos separados por $, hay %d: %s", n, encoded)
	}
}

func TestHashPasswordUsesFreshSalt(t *testing.T) {
	a, err := crypto.HashPassword("misma")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	b, err := crypto.HashPassword("misma")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if a == b {
		t.Fatal("dos hashes de la misma contraseña salieron idénticos: el salt no es aleatorio")
	}
}

func TestVerifyPasswordAcceptsCorrect(t *testing.T) {
	encoded, err := crypto.HashPassword("correcta")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := crypto.VerifyPassword(encoded, "correcta")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("VerifyPassword = false con la contraseña correcta")
	}
}

func TestVerifyPasswordRejectsIncorrect(t *testing.T) {
	encoded, err := crypto.HashPassword("correcta")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := crypto.VerifyPassword(encoded, "incorrecta")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("VerifyPassword = true con la contraseña incorrecta")
	}
}

func TestVerifyPasswordRejectsMalformedEncoding(t *testing.T) {
	for _, bad := range []string{
		"",
		"texto-plano",
		"$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",  // variante equivocada
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdA",        // faltan segmentos
		"$argon2id$v=19$m=abc,t=3,p=4$c2FsdA$aGFzaA",   // parámetro no numérico
	} {
		if _, err := crypto.VerifyPassword(bad, "x"); err == nil {
			t.Errorf("quería error con el codificado %q", bad)
		}
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := crypto.HashPassword(""); err == nil {
		t.Fatal("quería error con contraseña vacía")
	}
}
