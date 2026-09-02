package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateMasterKeyIsDecodableAnd32Bytes(t *testing.T) {
	key, err := generateMasterKey()
	if err != nil {
		t.Fatalf("generateMasterKey: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		t.Fatalf("la clave generada no es base64 estándar: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("len = %d, quería 32", len(decoded))
	}
}

func TestGenerateMasterKeyIsRandom(t *testing.T) {
	a, err := generateMasterKey()
	if err != nil {
		t.Fatalf("generateMasterKey: %v", err)
	}
	b, err := generateMasterKey()
	if err != nil {
		t.Fatalf("generateMasterKey: %v", err)
	}
	if a == b {
		t.Fatal("generateMasterKey devolvió el mismo valor dos veces")
	}
	if strings.TrimSpace(a) != a {
		t.Errorf("la clave no debería llevar espacios: %q", a)
	}
}
