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

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
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

func TestPrintVersionEmitsOneLineWithTheVersion(t *testing.T) {
	old := version
	version = "v9.9.9-test"
	defer func() { version = old }()

	var buf bytes.Buffer
	printVersion(&buf)

	got := buf.String()
	if !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Errorf("quería exactamente una línea, obtuve %q", got)
	}
	if !strings.Contains(got, "v9.9.9-test") {
		t.Errorf("la salida no lleva la versión: %q", got)
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

// El arranque no puede escribir la clave de ingesta, ni en claro ni enmascarada: el spec
// §8 no admite matices, y la máscara con los últimos 4 es para la interfaz. `ingest_app` sí.
func TestRunStartupLogNeverContainsTheIngestKey(t *testing.T) {
	master, err := generateMasterKey()
	if err != nil {
		t.Fatalf("generateMasterKey: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "arranque.db")
	t.Setenv("SPLITSTREAM_MASTER_KEY", master)
	t.Setenv("SPLITSTREAM_DB_PATH", dbPath)
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

	// El valor real de la clave: la genera el bootstrap, así que se lee de la base
	// DESPUÉS de arrancar y se exige que esa cadena concreta no esté en la salida. Sin
	// esto el test solo vigilaba el nombre del atributo, y no habría visto la clave
	// volcada bajo otro nombre.
	ingestKey := revealIngestKey(t, dbPath, master)
	if len(ingestKey.Reveal()) < 8 {
		t.Fatalf("la clave de ingesta leída es sospechosamente corta (%d)", len(ingestKey.Reveal()))
	}
	if strings.Contains(logged, ingestKey.Reveal()) {
		t.Errorf("el arranque volcó la clave de ingesta en claro: %s", logged)
	}
	if strings.Contains(logged, ingestKey.Mask()) {
		t.Errorf("el arranque volcó la clave de ingesta enmascarada: %s", logged)
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

// revealIngestKey abre la base que dejó run() y descifra la clave de ingesta.
func revealIngestKey(t *testing.T, dbPath, masterB64 string) crypto.Secret {
	t.Helper()

	raw, err := base64.StdEncoding.DecodeString(masterB64)
	if err != nil {
		t.Fatalf("decodificar la master key: %v", err)
	}
	var master [32]byte
	copy(master[:], raw)

	cipher, err := crypto.NewCipher(master)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	ctx := context.Background()
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	key, err := db.RevealIngestKey(ctx, cipher)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}
	return key
}
