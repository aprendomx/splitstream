package store_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

func testCipher(t *testing.T, fill byte) *crypto.Cipher {
	t.Helper()
	var k [32]byte
	for i := range k {
		k[i] = fill
	}
	c, err := crypto.NewCipher(k)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func bootstrapped(t *testing.T) (*store.DB, *crypto.Cipher) {
	t.Helper()
	db := openTemp(t)
	c := testCipher(t, 0xAB)
	if err := db.Bootstrap(context.Background(), c); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return db, c
}

func TestBootstrapCreatesSettings(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	s, err := db.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if s.IngestApp != "live" {
		t.Errorf("IngestApp = %q, quería \"live\"", s.IngestApp)
	}
	if s.PasswordHash != "" {
		t.Errorf("PasswordHash = %q, quería vacío en el arranque inicial", s.PasswordHash)
	}
	if !strings.HasPrefix(s.IngestKeyMask, "••••") {
		t.Errorf("IngestKeyMask no viene enmascarada: %q", s.IngestKeyMask)
	}

	revealed, err := db.RevealIngestKey(ctx, c)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}
	if len(revealed.Reveal()) < 16 {
		t.Errorf("la clave de ingesta es demasiado corta: %d caracteres", len(revealed.Reveal()))
	}
	if revealed.Mask() != s.IngestKeyMask {
		t.Errorf("máscara no coincide: revelada %q, listada %q", revealed.Mask(), s.IngestKeyMask)
	}
}

func TestBootstrapDoesNotStoreIngestKeyInClear(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	revealed, err := db.RevealIngestKey(ctx, c)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}

	var blob []byte
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT ingest_key_encrypted FROM settings WHERE id = 1`).Scan(&blob); err != nil {
		t.Fatalf("select: %v", err)
	}
	if strings.Contains(string(blob), revealed.Reveal()) {
		t.Error("la clave de ingesta está en claro en la columna cifrada")
	}
}

// TestBootstrapDoesNotStoreIngestKeyInClear solo mira una columna. Esta versión busca
// la clave en TODOS los archivos que deja la base, incluido el -wal: en modo WAL lo
// recién escrito vive ahí, no en el .db, así que mirar solo el .db no prueba nada.
func TestIngestKeyNeverAppearsInDatabaseFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	c := testCipher(t, 0xAB)
	if err := db.Bootstrap(ctx, c); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	key, err := db.RevealIngestKey(ctx, c)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}
	// Cerrar fuerza el checkpoint del WAL al archivo principal.
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	plaintext := []byte(key.Reveal())
	checked := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("leer %s: %v", e.Name(), err)
		}
		if bytes.Contains(content, plaintext) {
			t.Errorf("%s contiene la clave de ingesta en claro", e.Name())
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no se inspeccionó ningún archivo: el test no probó nada")
	}
}

func TestBootstrapIsIdempotent(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	before, err := db.RevealIngestKey(ctx, c)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}
	if err := db.Bootstrap(ctx, c); err != nil {
		t.Fatalf("segundo Bootstrap: %v", err)
	}
	after, err := db.RevealIngestKey(ctx, c)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}
	if before.Reveal() != after.Reveal() {
		t.Error("el segundo Bootstrap regeneró la clave de ingesta")
	}
}

func TestBootstrapDetectsWrongMasterKey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	first, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := first.Bootstrap(ctx, testCipher(t, 0xAB)); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	first.Close()

	second, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	defer second.Close()

	err = second.Bootstrap(ctx, testCipher(t, 0xCD))
	if !errors.Is(err, crypto.ErrWrongMasterKey) {
		t.Fatalf("Bootstrap con otra master key = %v, quería ErrWrongMasterKey", err)
	}
}

// RotateIngestKey y SetPasswordHash son inalcanzables en el flujo normal (CHECK (id = 1)
// y Bootstrap siempre antes), pero su modo de fallo hoy es éxito silencioso: el usuario
// recibiría una clave nueva por HTTP sin que la base se actualizara.
func TestSettingsWritesRequireBootstrap(t *testing.T) {
	db := openTemp(t) // sin Bootstrap a propósito
	c := testCipher(t, 0xAB)
	ctx := context.Background()

	if _, err := db.RotateIngestKey(ctx, c); !errors.Is(err, store.ErrSettingsNotInitialized) {
		t.Errorf("RotateIngestKey sin Bootstrap = %v, quería ErrSettingsNotInitialized", err)
	}
	if err := db.SetPasswordHash(ctx, "$argon2id$fake"); !errors.Is(err, store.ErrSettingsNotInitialized) {
		t.Errorf("SetPasswordHash sin Bootstrap = %v, quería ErrSettingsNotInitialized", err)
	}
}

func TestRotateIngestKey(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	before, err := db.RevealIngestKey(ctx, c)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}

	rotated, err := db.RotateIngestKey(ctx, c)
	if err != nil {
		t.Fatalf("RotateIngestKey: %v", err)
	}
	if rotated.Reveal() == before.Reveal() {
		t.Fatal("RotateIngestKey devolvió la misma clave")
	}

	persisted, err := db.RevealIngestKey(ctx, c)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}
	if persisted.Reveal() != rotated.Reveal() {
		t.Error("la clave rotada no se persistió")
	}

	s, err := db.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if s.IngestKeyMask != rotated.Mask() {
		t.Errorf("la máscara no se actualizó al rotar: %q", s.IngestKeyMask)
	}
}

func TestSetPasswordHash(t *testing.T) {
	db, _ := bootstrapped(t)
	ctx := context.Background()

	hash, err := crypto.HashPassword("una-contraseña")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := db.SetPasswordHash(ctx, hash); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}

	s, err := db.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if s.PasswordHash != hash {
		t.Errorf("PasswordHash no se persistió")
	}
}

func TestGenerateIngestKeyIsRandomAndURLSafe(t *testing.T) {
	a, err := store.GenerateIngestKey()
	if err != nil {
		t.Fatalf("GenerateIngestKey: %v", err)
	}
	b, err := store.GenerateIngestKey()
	if err != nil {
		t.Fatalf("GenerateIngestKey: %v", err)
	}
	if a.Reveal() == b.Reveal() {
		t.Fatal("GenerateIngestKey devolvió el mismo valor dos veces")
	}
	if strings.ContainsAny(a.Reveal(), "+/=") {
		t.Errorf("la clave debe ser segura para URL, es %q", a.Reveal())
	}
}
