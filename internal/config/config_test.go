package config_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/config"
)

// 32 bytes de 0x01..0x20, codificados en base64 estándar.
func testKeyB64() string {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(k[:])
}

func lookup(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

func TestLoadFromAppliesDefaults(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(),
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, quería \":8080\"", cfg.HTTPAddr)
	}
	if cfg.RTMPAddr != ":1935" {
		t.Errorf("RTMPAddr = %q, quería \":1935\"", cfg.RTMPAddr)
	}
	if cfg.DBPath != "splitstream.db" {
		t.Errorf("DBPath = %q, quería \"splitstream.db\"", cfg.DBPath)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, quería info", cfg.LogLevel)
	}
	if cfg.MasterKey[0] != 1 || cfg.MasterKey[31] != 32 {
		t.Errorf("MasterKey mal decodificada: %v", cfg.MasterKey)
	}
}

func TestLoadFromOverridesDefaults(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(),
		"SPLITSTREAM_HTTP_ADDR":  "127.0.0.1:9000",
		"SPLITSTREAM_RTMP_ADDR":  "0.0.0.0:1936",
		"SPLITSTREAM_DB_PATH":    "/var/lib/splitstream/db.sqlite",
		"SPLITSTREAM_LOG_LEVEL":  "debug",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.RTMPAddr != "0.0.0.0:1936" {
		t.Errorf("RTMPAddr = %q", cfg.RTMPAddr)
	}
	if cfg.DBPath != "/var/lib/splitstream/db.sqlite" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, quería debug", cfg.LogLevel)
	}
}

// Faltar SPLITSTREAM_MASTER_KEY ya NO es un error: se crea un archivo de clave junto a la
// base. Ese cambio existe para que el programa se pueda abrir con doble clic desde el
// Finder o el Explorador, donde no hay variables de entorno.
//
// Lo que sí sigue siendo un error es una variable puesta con un valor que no sirve: ahí
// hubo intención, y adivinar por el usuario sería peor que decírselo.
func TestLoadFromWithoutMasterKeyCreatesOne(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_DB_PATH": filepath.Join(dir, "datos.db"),
	}))
	if err != nil {
		t.Fatalf("LoadFrom sin la variable debería funcionar: %v", err)
	}
	if !cfg.MasterKeyAutogenerada {
		t.Error("debería haber generado la clave")
	}
}

func TestLoadFromRejectsAnUnusableMasterKey(t *testing.T) {
	casos := map[string]string{
		"no es base64":      "esto no es base64 válido !!!",
		"base64 pero corta": "YWJj",
		"base64 pero larga": strings.Repeat("QUJD", 40),
	}
	for nombre, valor := range casos {
		t.Run(nombre, func(t *testing.T) {
			_, err := config.LoadFrom(lookup(map[string]string{
				"SPLITSTREAM_MASTER_KEY": valor,
				"SPLITSTREAM_DB_PATH":    filepath.Join(t.TempDir(), "datos.db"),
			}))
			if err == nil {
				t.Fatal("quería error con una clave inservible")
			}
			if !strings.Contains(err.Error(), "SPLITSTREAM_MASTER_KEY") {
				t.Errorf("el error debería nombrar la variable: %v", err)
			}
			if strings.Contains(err.Error(), valor) {
				t.Error("el error incluye el valor de la clave")
			}
		})
	}
}

func TestLoadFromRejectsWrongKeyLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": short,
	}))
	if err == nil {
		t.Fatal("quería error con una clave de 16 bytes")
	}
}

func TestLoadFromRejectsInvalidBase64(t *testing.T) {
	_, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": "no-es-base64-!!!",
	}))
	if err == nil {
		t.Fatal("quería error con base64 inválido")
	}
}

func TestLoadFromRejectsUnknownLogLevel(t *testing.T) {
	_, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(),
		"SPLITSTREAM_LOG_LEVEL":  "verboso",
	}))
	if err == nil {
		t.Fatal("quería error con un nivel de log desconocido")
	}
}

// El error de una master key inválida no puede reproducir su valor.
func TestLoadFromErrorDoesNotLeakKey(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": secret,
	}))
	if err == nil {
		t.Fatal("quería error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("el error filtró la clave: %v", err)
	}
}

func TestConfigLogValueOmitsMasterKey(t *testing.T) {
	key := testKeyB64()
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": key,
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("arranque", "config", cfg)

	out := buf.String()
	if strings.Contains(out, key) {
		t.Errorf("el log filtró la master key en base64: %s", out)
	}
	if strings.Contains(out, "AQIDBA") { // prefijo base64 de 0x01020304
		t.Errorf("el log filtró bytes de la master key: %s", out)
	}
	if !strings.Contains(out, ":8080") {
		t.Errorf("el log debería incluir los campos no secretos: %s", out)
	}
}

// LogValue con receptor puntero no está en el method set de Config por valor: si algo
// loguea un Config (no un *Config), slog no encuentra slog.LogValuer y vuelca el struct
// entero, incluida la master key. Verificado por ejecución en la revisión final.
func TestConfigLogValueOmitsMasterKeyWhenLoggedByValue(t *testing.T) {
	key := testKeyB64()
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": key,
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("arranque", "config", *cfg)

	out := buf.String()
	if strings.Contains(out, "MasterKey") {
		t.Errorf("el log filtró la master key al loguear Config por valor: %s", out)
	}
	if !strings.Contains(out, ":8080") {
		t.Errorf("el log debería incluir los campos no secretos: %s", out)
	}
}

// json.Marshal no puede volcar la master key, ni desde un Config ni desde un *Config.
// El receptor importa: en la fase 1, con LogValue declarado sobre puntero, loguear un
// Config por valor volcaba los 32 bytes.
func TestConfigMarshalJSONMasksMasterKey(t *testing.T) {
	var cfg config.Config
	cfg.HTTPAddr = ":8080"
	cfg.RTMPAddr = ":1935"
	cfg.DBPath = "splitstream.db"
	cfg.LogLevel = slog.LevelInfo
	for i := range cfg.MasterKey {
		cfg.MasterKey[i] = byte(i + 1)
	}

	// Las dos formas en que la clave podría aparecer: el array de bytes que emite
	// encoding/json y el base64 con el que se configura.
	asNumbers := "1,2,3,4,5,6,7,8"
	asBase64 := base64.StdEncoding.EncodeToString(cfg.MasterKey[:])

	for _, tc := range []struct {
		name string
		v    any
	}{
		{"por valor", cfg},
		{"por puntero", &cfg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			out := string(raw)
			if strings.Contains(out, asNumbers) {
				t.Errorf("el JSON lleva los bytes de la master key: %s", out)
			}
			if strings.Contains(out, asBase64) {
				t.Errorf("el JSON lleva la master key en base64: %s", out)
			}
			if strings.Contains(out, "MasterKey") || strings.Contains(out, "master_key") {
				t.Errorf("el JSON menciona la master key: %s", out)
			}
			// Y sigue sirviendo para lo que se serializa un Config.
			if !strings.Contains(out, `"db_path":"splitstream.db"`) {
				t.Errorf("el JSON perdió el resto de la configuración: %s", out)
			}
		})
	}
}

// TestMasterKeyIsCreatedWhenThereIsNoEnv: el caso de abrir el programa con doble clic
// desde el Finder o el Explorador, donde no hay variables de entorno. Antes moría con
// "falta SPLITSTREAM_MASTER_KEY" y no había forma de arrancarlo sin usar la terminal.
func TestMasterKeyIsCreatedWhenThereIsNoEnv(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "datos.db")

	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_DB_PATH": dbPath,
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !cfg.MasterKeyAutogenerada {
		t.Error("debería avisar de que acaba de generar la clave")
	}

	rutaClave := config.KeyPathFor(dbPath)
	if cfg.MasterKeyPath != rutaClave {
		t.Errorf("MasterKeyPath = %q, quería %q", cfg.MasterKeyPath, rutaClave)
	}

	info, err := os.Stat(rutaClave)
	if err != nil {
		t.Fatalf("no se creó el archivo de clave: %v", err)
	}
	// Solo el dueño. Si otro usuario del equipo puede leerla, el cifrado de la base no
	// protege de nada.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("permisos = %o, quería 600", info.Mode().Perm())
	}
}

// TestMasterKeyIsStableAcrossRuns: si cambiara en cada arranque, la base quedaría ilegible
// y el usuario perdería las claves de todos sus destinos.
func TestMasterKeyIsStableAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "datos.db")
	entorno := lookup(map[string]string{"SPLITSTREAM_DB_PATH": dbPath})

	primera, err := config.LoadFrom(entorno)
	if err != nil {
		t.Fatalf("primera: %v", err)
	}
	segunda, err := config.LoadFrom(entorno)
	if err != nil {
		t.Fatalf("segunda: %v", err)
	}

	if primera.MasterKey != segunda.MasterKey {
		t.Fatal("la clave cambió entre arranques: la base quedaría ilegible")
	}
	if segunda.MasterKeyAutogenerada {
		t.Error("el segundo arranque no debería decir que la generó")
	}
}

// TestEnvMasterKeyWinsOverTheFile: el camino del servidor no cambia. Si alguien pone la
// variable, es la que manda, aunque haya un archivo al lado.
func TestEnvMasterKeyWinsOverTheFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "datos.db")

	// Primero se crea un archivo de clave.
	delArchivo, err := config.LoadFrom(lookup(map[string]string{"SPLITSTREAM_DB_PATH": dbPath}))
	if err != nil {
		t.Fatalf("crear el archivo: %v", err)
	}

	// Y ahora se arranca con una variable distinta.
	otra := testKeyB64()
	conEntorno, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_DB_PATH":    dbPath,
		"SPLITSTREAM_MASTER_KEY": otra,
	}))
	if err != nil {
		t.Fatalf("con entorno: %v", err)
	}

	if conEntorno.MasterKey == delArchivo.MasterKey {
		t.Error("el archivo ganó a la variable de entorno")
	}
	if conEntorno.MasterKeyPath != "" {
		t.Errorf("MasterKeyPath = %q; con la variable puesta no se usa archivo", conEntorno.MasterKeyPath)
	}
	if conEntorno.MasterKeyAutogenerada {
		t.Error("con la variable puesta no se genera nada")
	}
}

// TestEmptyKeyFileIsAnErrorNotANewKey: un archivo vacío suele ser una escritura a medias o
// un respaldo mal hecho. Generar una clave nueva encima dejaría la base ilegible en
// silencio, que es la peor forma de perder datos.
func TestEmptyKeyFileIsAnErrorNotANewKey(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "datos.db")
	if err := os.WriteFile(config.KeyPathFor(dbPath), []byte("  \n"), 0o600); err != nil {
		t.Fatalf("preparar: %v", err)
	}

	if _, err := config.LoadFrom(lookup(map[string]string{"SPLITSTREAM_DB_PATH": dbPath})); err == nil {
		t.Error("un archivo de clave vacío debería ser un error")
	}
}

// TestKeyPathSitsNextToTheDatabase: los dos archivos se respaldan y se mueven juntos.
func TestKeyPathSitsNextToTheDatabase(t *testing.T) {
	casos := map[string]string{
		"splitstream.db":            "splitstream.key",
		"/var/lib/ss/datos.db":      "/var/lib/ss/datos.key",
		"/data/splitstream.sqlite3": "/data/splitstream.key",
		"sin-extension":             "sin-extension.key",
	}
	for db, quiero := range casos {
		if got := config.KeyPathFor(db); got != quiero {
			t.Errorf("KeyPathFor(%q) = %q, quería %q", db, got, quiero)
		}
	}
}
