package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

// syncWriter serializa lo que escriben las goroutines de run: el logger lo usan la
// ingesta y los sinks a la vez.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// El arranque no puede escribir la clave de ingesta, ni siquiera enmascarada: el spec §8
// no admite matices, y la máscara con los últimos 4 es para la interfaz. `ingest_app` sí.
func TestRunStartupLogNeverContainsTheIngestKey(t *testing.T) {
	key, err := generateMasterKey()
	if err != nil {
		t.Fatalf("generateMasterKey: %v", err)
	}
	t.Setenv("SPLITSTREAM_MASTER_KEY", key)
	t.Setenv("SPLITSTREAM_DB_PATH", filepath.Join(t.TempDir(), "arranque.db"))
	t.Setenv("SPLITSTREAM_RTMP_ADDR", "127.0.0.1:0")
	t.Setenv("SPLITSTREAM_HTTP_ADDR", "127.0.0.1:0")

	// run() se queda esperando a que venza el contexto; con el plazo justo para haber
	// escrito ya la línea de arranque.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var out syncWriter
	if err := run(ctx, &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	logged := out.String()
	if !strings.Contains(logged, "splitstream arrancado") {
		t.Fatalf("no se llegó a la línea de arranque: %s", logged)
	}
	if strings.Contains(logged, "ingest_key") {
		t.Errorf("el arranque loguea la clave de ingesta: %s", logged)
	}
	if strings.Contains(logged, "••••") {
		t.Errorf("el arranque loguea una clave enmascarada, y el spec §8 no admite matices: %s", logged)
	}
	if !strings.Contains(logged, "ingest_app") {
		t.Errorf("el arranque debería seguir diciendo la app de ingesta: %s", logged)
	}
}
