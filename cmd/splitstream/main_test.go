package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
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

	// La fase 4 añadió el servidor HTTP al arranque. Ampliar lo que se loguea es
	// exactamente cuando esta propiedad se vuelve a romper: la fase 3 tuvo que arreglarla
	// dos veces (5e57f0b, 62a6dc2).
	if !strings.Contains(logged, "http_addr") {
		t.Errorf("el arranque no dice dónde escucha el HTTP: %s", logged)
	}
	// Nota: NO se prohíbe la palabra "contraseña". El aviso del primer arranque dice
	// legítimamente "elige tu contraseña", y prohibir la palabra confundía nombrar un
	// concepto con filtrar un secreto. Lo que se vigila son los secretos, arriba.
}

// TestFirstRunNoticeOnlyAppearsWhenUnconfigured: el código de configuración se imprime al
// arrancar sin contraseña, que es cuando sirve. Seguir imprimiéndolo en cada reinicio de un
// servicio ya configurado lo dejaría en el journal para siempre sin ninguna razón.
func TestFirstRunNoticeOnlyAppearsWhenUnconfigured(t *testing.T) {
	master, err := generateMasterKey()
	if err != nil {
		t.Fatalf("generateMasterKey: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "primero.db")
	t.Setenv("SPLITSTREAM_MASTER_KEY", master)
	t.Setenv("SPLITSTREAM_DB_PATH", dbPath)
	t.Setenv("SPLITSTREAM_RTMP_ADDR", "127.0.0.1:0")
	t.Setenv("SPLITSTREAM_HTTP_ADDR", "127.0.0.1:0")

	arranque := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		var out syncWriter
		if err := run(ctx, &out); err != nil {
			t.Fatalf("run: %v", err)
		}
		return out.String()
	}

	// Sin configurar: el aviso está.
	primero := arranque()
	if !strings.Contains(primero, "todavía no está configurado") {
		t.Fatalf("el primer arranque no avisó:\n%s", primero)
	}
	if !regexp.MustCompile(`[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}`).MatchString(primero) {
		t.Error("el primer arranque no imprimió ningún código")
	}

	// Se configura, y el aviso desaparece para siempre.
	if err := setPassword(context.Background(),
		strings.NewReader("una-contraseña-cualquiera\n"), io.Discard); err != nil {
		t.Fatalf("setPassword: %v", err)
	}

	segundo := arranque()
	if strings.Contains(segundo, "todavía no está configurado") {
		t.Errorf("un servicio ya configurado sigue avisando:\n%s", segundo)
	}
	if regexp.MustCompile(`[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}`).MatchString(segundo) {
		t.Error("un servicio ya configurado sigue imprimiendo un código de configuración")
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

// freeAddr reserva un puerto y lo suelta. Hace falta porque run() construye el servidor
// HTTP por dentro: con :0 el test no tendría forma de saber dónde acabó escuchando.
//
// Hay una ventana entre soltar el puerto y que run() lo tome; en un test local no se pisa
// nadie, y la alternativa —exponer el listener desde run()— ensuciaría la firma solo para
// el test.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("puerto libre: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// arrancaRun lanza run() en segundo plano con el entorno preparado, y devuelve la
// dirección HTTP, la función de cancelación y el canal con el resultado.
func arrancaRun(t *testing.T, out io.Writer) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	master, err := generateMasterKey()
	if err != nil {
		t.Fatalf("generateMasterKey: %v", err)
	}
	addr := freeAddr(t)
	t.Setenv("SPLITSTREAM_MASTER_KEY", master)
	t.Setenv("SPLITSTREAM_DB_PATH", filepath.Join(t.TempDir(), "run.db"))
	t.Setenv("SPLITSTREAM_RTMP_ADDR", "127.0.0.1:0")
	t.Setenv("SPLITSTREAM_HTTP_ADDR", addr)

	ctx, cancel := context.WithCancel(context.Background())
	hecho := make(chan error, 1)
	go func() { hecho <- run(ctx, out) }()
	return addr, cancel, hecho
}

// TestRunServesTheAPI: el binario levanta la API donde dice la configuración.
//
// Se comprueba contra /api/auth/login porque es el único endpoint público: un 409 —no hay
// contraseña configurada— prueba que el servidor está ahí y que es el nuestro, sin
// necesidad de autenticarse ni de tocar la base.
func TestRunServesTheAPI(t *testing.T) {
	addr, cancel, hecho := arrancaRun(t, io.Discard)
	defer cancel()

	var resp *http.Response
	var err error
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Post("http://"+addr+"/api/auth/login",
			"application/json", strings.NewReader(`{"password":"x"}`))
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("la API nunca respondió en %s: %v", addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("código = %d, quería 409 (sin contraseña configurada)", resp.StatusCode)
	}
	cuerpo, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(cuerpo), "setpassword") {
		t.Errorf("la respuesta no parece la nuestra: %s", cuerpo)
	}

	cancel()
	select {
	case err := <-hecho:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run no volvió tras cancelar")
	}
}

// TestRunShutsDownTheHTTPServerOnSignal: al cancelar el contexto, run debe volver y el
// puerto quedar libre. Si el servidor HTTP no se cierra, run se cuelga y el servicio no se
// puede reiniciar sin matarlo — que es exactamente el fallo que la fase 3 persiguió tres
// rondas en el lado de la ingesta.
func TestRunShutsDownTheHTTPServerOnSignal(t *testing.T) {
	addr, cancel, hecho := arrancaRun(t, io.Discard)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-hecho:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run no volvió tras cancelar: el servidor HTTP no se cerró")
	}

	// Y el puerto queda libre: se puede volver a escuchar en él.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("el puerto sigue ocupado tras el apagado: %v", err)
	}
	ln.Close()
}
