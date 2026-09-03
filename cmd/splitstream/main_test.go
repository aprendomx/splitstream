package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
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

// TestReadPasswordRejectsUnusableInput: una contraseña vacía o demasiado corta protege tan
// poco que aceptarla sería mentir sobre el estado del servicio.
func TestReadPasswordRejectsUnusableInput(t *testing.T) {
	casos := []struct{ nombre, in string }{
		{"vacía", "\n"},
		{"solo espacios", "        \n"},
		{"demasiado corta", "corta\n"},
		{"stdin cerrado", ""},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if _, err := readPassword(strings.NewReader(c.in)); err == nil {
				t.Error("se aceptó una contraseña inservible")
			}
		})
	}
}

// TestReadPasswordKeepsInteriorSpaces: una frase de paso lleva espacios y no hay que
// comérselos. Solo se quita el salto de línea final.
func TestReadPasswordKeepsInteriorSpaces(t *testing.T) {
	got, err := readPassword(strings.NewReader("correcto caballo batería grapa\n"))
	if err != nil {
		t.Fatalf("readPassword: %v", err)
	}
	if quiero := "correcto caballo batería grapa"; got != quiero {
		t.Errorf("got %q, quería %q", got, quiero)
	}
}

// TestReadPasswordHandlesCRLF: si alguien la pega desde Windows, el \r no es parte de la
// contraseña.
func TestReadPasswordHandlesCRLF(t *testing.T) {
	got, err := readPassword(strings.NewReader("contraseña-larga\r\n"))
	if err != nil {
		t.Fatalf("readPassword: %v", err)
	}
	if quiero := "contraseña-larga"; got != quiero {
		t.Errorf("got %q, quería %q", got, quiero)
	}
}

// setPasswordEnv prepara el entorno de una base temporal y devuelve su ruta.
func setPasswordEnv(t *testing.T) string {
	t.Helper()
	master, err := generateMasterKey()
	if err != nil {
		t.Fatalf("generateMasterKey: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "pw.db")
	t.Setenv("SPLITSTREAM_MASTER_KEY", master)
	t.Setenv("SPLITSTREAM_DB_PATH", dbPath)
	return dbPath
}

// settingsDe abre la base que dejó setPassword y devuelve sus settings.
func settingsDe(t *testing.T, dbPath string) *store.Settings {
	t.Helper()
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s, err := db.Settings(context.Background())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	return s
}

// TestSetPasswordNeverEchoesTheSecret es la propiedad del spec §8 aplicada aquí: lo que el
// comando imprime no puede contener la contraseña ni un trozo reconocible suyo.
func TestSetPasswordNeverEchoesTheSecret(t *testing.T) {
	const secreto = "una-contraseña-muy-secreta"
	setPasswordEnv(t)

	var out bytes.Buffer
	if err := setPassword(context.Background(), strings.NewReader(secreto+"\n"), &out); err != nil {
		t.Fatalf("setPassword: %v", err)
	}

	if strings.Contains(out.String(), secreto) {
		t.Errorf("la salida lleva la contraseña: %q", out.String())
	}
	if strings.Contains(out.String(), secreto[:8]) {
		t.Errorf("la salida lleva un prefijo de la contraseña: %q", out.String())
	}
}

// TestSetPasswordPersistsAVerifiableHash: el objetivo del comando es que el login pueda
// verificar después.
func TestSetPasswordPersistsAVerifiableHash(t *testing.T) {
	const secreto = "una-contraseña-muy-secreta"
	dbPath := setPasswordEnv(t)

	if err := setPassword(context.Background(), strings.NewReader(secreto+"\n"), io.Discard); err != nil {
		t.Fatalf("setPassword: %v", err)
	}

	s := settingsDe(t, dbPath)
	if s.PasswordHash == "" {
		t.Fatal("no se guardó ningún hash")
	}
	if strings.Contains(s.PasswordHash, secreto) {
		t.Fatal("el hash contiene la contraseña en claro")
	}

	ok, err := crypto.VerifyPassword(s.PasswordHash, secreto)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("el hash guardado no verifica contra la contraseña")
	}

	ok, err = crypto.VerifyPassword(s.PasswordHash, secreto+"x")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("el hash verifica contra una contraseña equivocada")
	}
}

// TestSetPasswordOverwritesTheOldOne: cambiar la contraseña es el mismo comando.
func TestSetPasswordOverwritesTheOldOne(t *testing.T) {
	dbPath := setPasswordEnv(t)
	ctx := context.Background()

	if err := setPassword(ctx, strings.NewReader("la-primera-contraseña\n"), io.Discard); err != nil {
		t.Fatalf("primera: %v", err)
	}
	if err := setPassword(ctx, strings.NewReader("la-segunda-contraseña\n"), io.Discard); err != nil {
		t.Fatalf("segunda: %v", err)
	}

	s := settingsDe(t, dbPath)
	if ok, _ := crypto.VerifyPassword(s.PasswordHash, "la-primera-contraseña"); ok {
		t.Error("la contraseña vieja sigue valiendo")
	}
	if ok, _ := crypto.VerifyPassword(s.PasswordHash, "la-segunda-contraseña"); !ok {
		t.Error("la contraseña nueva no vale")
	}
}

// TestSetPasswordDoesNotDisturbTheIngestKey: fijar la contraseña no puede rotar ni tocar
// la clave de ingesta. Sería una sorpresa desagradable a mitad de una transmisión.
func TestSetPasswordDoesNotDisturbTheIngestKey(t *testing.T) {
	dbPath := setPasswordEnv(t)
	ctx := context.Background()

	if err := setPassword(ctx, strings.NewReader("la-primera-contraseña\n"), io.Discard); err != nil {
		t.Fatalf("primera: %v", err)
	}
	antes := settingsDe(t, dbPath).IngestKeyMask

	if err := setPassword(ctx, strings.NewReader("la-segunda-contraseña\n"), io.Discard); err != nil {
		t.Fatalf("segunda: %v", err)
	}
	if got := settingsDe(t, dbPath).IngestKeyMask; got != antes {
		t.Errorf("la clave de ingesta cambió: %q → %q", antes, got)
	}
}
