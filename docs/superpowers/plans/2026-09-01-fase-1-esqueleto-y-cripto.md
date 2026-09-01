# Splitstream — Plan de implementación, Fase 1

> **Para agentes:** SUB-SKILL REQUERIDA: usa `superpowers:subagent-driven-development`
> (recomendado) o `superpowers:executing-plans` para ejecutar este plan tarea por tarea.
> Los pasos usan sintaxis de checkbox (`- [ ]`) para seguimiento.

**Goal:** Dejar el proyecto compilando y con sus cimientos verificados: configuración por
entorno, cifrado de secretos con AES-256-GCM, hash de contraseñas con argon2id, SQLite con
migraciones versionadas, y los repositorios de las cuatro tablas.

**Architecture:** Un módulo Go sin CGO. `internal/config` traduce el entorno a un struct.
`internal/crypto` aporta dos primitivas independientes (cifrado simétrico de secretos y
hash de contraseñas) más un tipo `Secret` cuyo `String()` devuelve la máscara, de modo que
una clave en claro no puede llegar a un log ni por accidente. `internal/store` abre SQLite,
aplica migraciones embebidas y expone repositorios tipados. El binario de la fase 1 arranca,
valida la master key contra un *key check value* y espera a SIGTERM: todavía no hay ni RTMP
ni HTTP.

**Tech Stack:** Go 1.27 (piso 1.23), `modernc.org/sqlite`, `golang.org/x/crypto/argon2`,
`log/slog`, `embed`. Sin CGO, sin frameworks.

**Spec:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`

## Global Constraints

- Módulo Go: `github.com/aprendomx/splitstream`. Binario: `cmd/splitstream`.
- `CGO_ENABLED=0` en el **build**: el binario final debe ser estático. Los **tests**
  corren con cgo habilitado, porque `-race` lo exige en darwin/arm64 y linux/amd64.
  `modernc.org/sqlite` es puro Go y funciona igual en ambos modos.
- Go 1.23 como piso en `go.mod`, aunque la máquina tenga 1.27.
- Dependencias permitidas en toda la fase 1, y ninguna más: `modernc.org/sqlite`,
  `golang.org/x/crypto`. Sin testify, sin router HTTP, sin librería de migraciones.
- Variables de entorno, con estos nombres exactos: `SPLITSTREAM_MASTER_KEY` (obligatoria),
  `SPLITSTREAM_HTTP_ADDR` (default `:8080`), `SPLITSTREAM_RTMP_ADDR` (default `:1935`),
  `SPLITSTREAM_DB_PATH` (default `splitstream.db`), `SPLITSTREAM_LOG_LEVEL` (default `info`).
- Las claves en claro **nunca** aparecen en logs ni en mensajes de error (spec §8). Todo
  secreto viaja como `crypto.Secret`, cuyo `String()` y `LogValue()` devuelven `••••1234`.
- El enmascarado es exactamente cuatro puntos medios `••••` seguidos de los últimos 4
  caracteres. Si el secreto tiene menos de 4 caracteres, solo `••••`.
- Enum de plataformas, exactamente estos seis valores en minúscula:
  `youtube`, `twitch`, `facebook`, `kick`, `x`, `custom`.
- Timestamps en SQLite como TEXT en RFC3339 con nanosegundos, en UTC.
- Todo `*sql.DB` se usa con métodos `...Context`. Nada de `db.Query` sin contexto.

## Estructura de archivos de esta fase

| Archivo | Responsabilidad |
| --- | --- |
| `go.mod`, `Makefile`, `.gitignore` | Módulo y tareas de build/test |
| `internal/config/config.go` | Entorno → `Config`, con defaults y validación |
| `internal/crypto/secret.go` | `Cipher` (AES-256-GCM) y el key check value |
| `internal/crypto/masked.go` | Tipo `Secret`: enmascarado en `String`/`LogValue` |
| `internal/crypto/password.go` | argon2id: `HashPassword` / `VerifyPassword` |
| `internal/store/db.go` | `Open`, pragmas de SQLite, runner de migraciones |
| `internal/store/migrations/0001_initial.sql` | Las cuatro tablas |
| `internal/store/settings.go` | Fila única: ingesta, contraseña, KCV |
| `internal/store/destinations.go` | CRUD, orden, cifrado y revelado de claves |
| `internal/store/events.go` | Sesiones y log de eventos |
| `cmd/splitstream/main.go` | Wiring, `-genkey`, arranque, SIGTERM |

**Frontera que hay que respetar:** `internal/store` importa `internal/crypto`, nunca al
revés. `internal/crypto` no importa nada del proyecto.

---

### Task 1: Esqueleto del módulo y configuración por entorno

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nada.
- Produces: `config.Config` con campos `HTTPAddr string`, `RTMPAddr string`, `DBPath string`,
  `LogLevel slog.Level`, `MasterKey [32]byte`.
  `config.Load() (*Config, error)` y `config.LoadFrom(lookup func(string) (string, bool)) (*Config, error)`.
  `Config` implementa `slog.LogValuer`.

- [ ] **Step 1: Inicializar el módulo**

```bash
cd /Users/jadrians/aprendo/open-restream
go mod init github.com/aprendomx/splitstream
go mod edit -go=1.23
```

- [ ] **Step 2: Crear el Makefile**

```makefile
.PHONY: build test vet tidy run clean

# CGO_ENABLED=0 solo aquí: el binario de producción debe ser estático.
build:
	CGO_ENABLED=0 go build -o splitstream ./cmd/splitstream

# El detector de carreras necesita cgo, así que este target no lo desactiva.
test:
	go test ./... -race -count=1

vet:
	go vet ./...

tidy:
	go mod tidy

run: build
	./splitstream

clean:
	rm -f splitstream
```

Nota: `-race` requiere cgo, por eso el target `test` no fija `CGO_ENABLED=0` y `build` sí.
Verifica los dos caminos con `make test && make build` antes de dar la tarea por hecha.

- [ ] **Step 3: Escribir el test que falla**

`internal/config/config_test.go`:

```go
package config_test

import (
	"bytes"
	"encoding/base64"
	"log/slog"
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

func TestLoadFromRequiresMasterKey(t *testing.T) {
	_, err := config.LoadFrom(lookup(map[string]string{}))
	if err == nil {
		t.Fatal("quería error cuando falta SPLITSTREAM_MASTER_KEY")
	}
	if !strings.Contains(err.Error(), "SPLITSTREAM_MASTER_KEY") {
		t.Errorf("el error debería nombrar la variable: %v", err)
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
```

- [ ] **Step 4: Ejecutar el test y verificar que falla**

Run: `go test ./internal/config/ -v`
Expected: FAIL — el paquete `config` no existe.

- [ ] **Step 5: Implementar `internal/config/config.go`**

```go
// Package config traduce el entorno del proceso a una configuración validada.
package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
)

// MasterKeyLen es el tamaño exacto, en bytes, de la master key de AES-256.
const MasterKeyLen = 32

// Config es la configuración del proceso. Implementa slog.LogValuer para que
// MasterKey no pueda escaparse a un log por accidente.
type Config struct {
	HTTPAddr  string
	RTMPAddr  string
	DBPath    string
	LogLevel  slog.Level
	MasterKey [MasterKeyLen]byte
}

// LogValue implementa slog.LogValuer. Omite MasterKey deliberadamente.
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("http_addr", c.HTTPAddr),
		slog.String("rtmp_addr", c.RTMPAddr),
		slog.String("db_path", c.DBPath),
		slog.String("log_level", c.LogLevel.String()),
	)
}

// Load lee la configuración del entorno del proceso.
func Load() (*Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom lee la configuración de una función de consulta arbitraria, para poder
// testear sin tocar el entorno del proceso.
func LoadFrom(lookup func(string) (string, bool)) (*Config, error) {
	get := func(name, def string) string {
		if v, ok := lookup(name); ok && v != "" {
			return v
		}
		return def
	}

	cfg := &Config{
		HTTPAddr: get("SPLITSTREAM_HTTP_ADDR", ":8080"),
		RTMPAddr: get("SPLITSTREAM_RTMP_ADDR", ":1935"),
		DBPath:   get("SPLITSTREAM_DB_PATH", "splitstream.db"),
	}

	level, err := parseLevel(get("SPLITSTREAM_LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}
	cfg.LogLevel = level

	raw, ok := lookup("SPLITSTREAM_MASTER_KEY")
	if !ok || raw == "" {
		return nil, fmt.Errorf("falta SPLITSTREAM_MASTER_KEY: genera una con `splitstream -genkey`")
	}
	// Los mensajes de error de aquí abajo nunca incluyen `raw` ni los bytes decodificados.
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("SPLITSTREAM_MASTER_KEY no es base64 estándar válido")
	}
	if len(decoded) != MasterKeyLen {
		return nil, fmt.Errorf("SPLITSTREAM_MASTER_KEY debe decodificar a %d bytes, decodificó a %d", MasterKeyLen, len(decoded))
	}
	copy(cfg.MasterKey[:], decoded)

	return cfg, nil
}

func parseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("SPLITSTREAM_LOG_LEVEL inválido %q: usa debug, info, warn o error", s)
	}
}
```

- [ ] **Step 6: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/config/ -v && go vet ./... && CGO_ENABLED=0 go build ./...`
Expected: PASS en los 8 tests.

- [ ] **Step 7: Commit**

```bash
git add go.mod Makefile internal/config/
git commit -m "feat(config): configuración por entorno con master key validada"
```

---

### Task 2: Cifrado de secretos y tipo enmascarado

**Files:**
- Create: `internal/crypto/secret.go`
- Create: `internal/crypto/masked.go`
- Test: `internal/crypto/secret_test.go`
- Test: `internal/crypto/masked_test.go`

**Interfaces:**
- Consumes: nada (`internal/crypto` no importa nada del proyecto).
- Produces:
  - `crypto.Cipher` con `NewCipher(key [32]byte) (*Cipher, error)`,
    `(*Cipher).Encrypt(plaintext []byte) ([]byte, error)`,
    `(*Cipher).Decrypt(blob []byte) ([]byte, error)`,
    `(*Cipher).NewCheckValue() ([]byte, error)`,
    `(*Cipher).VerifyCheckValue(kcv []byte) error`.
  - `crypto.ErrWrongMasterKey` (variable de error centinela).
  - `crypto.Secret` (string) con `Reveal() string`, `Mask() string`, `String() string`,
    `LogValue() slog.Value`, `Last4() string`.

- [ ] **Step 1: Escribir el test que falla, del cifrado**

`internal/crypto/secret_test.go`:

```go
package crypto_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
)

func key(fill byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = fill
	}
	return k
}

func newCipher(t *testing.T, fill byte) *crypto.Cipher {
	t.Helper()
	c, err := crypto.NewCipher(key(fill))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c := newCipher(t, 0xAB)
	plain := []byte("live_123456789_abcdefghijklmnop")

	blob, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("el ciphertext contiene el plaintext en claro")
	}

	got, err := c.Decrypt(blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("Decrypt = %q, quería %q", got, plain)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	c := newCipher(t, 0xAB)
	plain := []byte("mismo texto")

	a, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	b, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("dos cifrados del mismo texto salieron idénticos: el nonce no es aleatorio")
	}
}

func TestDecryptRejectsTamperedBlob(t *testing.T) {
	c := newCipher(t, 0xAB)
	blob, err := c.Encrypt([]byte("secreto"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	blob[len(blob)-1] ^= 0x01

	if _, err := c.Decrypt(blob); err == nil {
		t.Fatal("quería error al descifrar un blob manipulado")
	}
}

func TestDecryptRejectsShortBlob(t *testing.T) {
	c := newCipher(t, 0xAB)
	if _, err := c.Decrypt([]byte{1, 2, 3}); err == nil {
		t.Fatal("quería error con un blob más corto que el nonce")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	blob, err := newCipher(t, 0xAB).Encrypt([]byte("secreto"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := newCipher(t, 0xCD).Decrypt(blob); err == nil {
		t.Fatal("quería error al descifrar con otra clave")
	}
}

func TestCheckValueRoundTrip(t *testing.T) {
	c := newCipher(t, 0xAB)
	kcv, err := c.NewCheckValue()
	if err != nil {
		t.Fatalf("NewCheckValue: %v", err)
	}
	if err := c.VerifyCheckValue(kcv); err != nil {
		t.Errorf("VerifyCheckValue con la clave correcta: %v", err)
	}
}

func TestVerifyCheckValueDetectsWrongKey(t *testing.T) {
	kcv, err := newCipher(t, 0xAB).NewCheckValue()
	if err != nil {
		t.Fatalf("NewCheckValue: %v", err)
	}

	err = newCipher(t, 0xCD).VerifyCheckValue(kcv)
	if !errors.Is(err, crypto.ErrWrongMasterKey) {
		t.Fatalf("VerifyCheckValue = %v, quería ErrWrongMasterKey", err)
	}
}
```

- [ ] **Step 2: Escribir el test que falla, del tipo `Secret`**

`internal/crypto/masked_test.go`:

```go
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
```

- [ ] **Step 3: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/crypto/ -v`
Expected: FAIL — el paquete `crypto` no existe.

- [ ] **Step 4: Implementar `internal/crypto/secret.go`**

```go
// Package crypto agrupa las primitivas de secreto de Splitstream: cifrado simétrico
// de credenciales en reposo y hash de contraseñas. No importa nada del proyecto.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
)

// ErrWrongMasterKey indica que la master key configurada no es la que cifró esta
// base de datos. Es un centinela para poder abortar el arranque con un mensaje claro.
var ErrWrongMasterKey = errors.New("la master key no corresponde a esta base de datos")

// checkValuePlaintext es la constante conocida que se cifra para formar el key check
// value. No es un secreto: su valor es público y está aquí a propósito.
const checkValuePlaintext = "splitstream-kcv-v1"

// Cipher cifra y descifra secretos con AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher construye un Cipher a partir de una master key de 32 bytes.
func NewCipher(key [32]byte) (*Cipher, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt devuelve nonce || ciphertext || tag. El nonce es aleatorio en cada llamada.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	// Seal añade al final de nonce, así que el nonce queda como prefijo del resultado.
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt deshace Encrypt. El error nunca incluye material del texto.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(blob) < n {
		return nil, errors.New("blob cifrado demasiado corto")
	}
	plaintext, err := c.aead.Open(nil, blob[:n], blob[n:], nil)
	if err != nil {
		return nil, errors.New("no se pudo descifrar: clave incorrecta o dato alterado")
	}
	return plaintext, nil
}

// NewCheckValue cifra una constante conocida. Guardarlo permite detectar al arrancar
// que la master key cambió, en vez de devolver basura descifrada más tarde.
func (c *Cipher) NewCheckValue() ([]byte, error) {
	return c.Encrypt([]byte(checkValuePlaintext))
}

// VerifyCheckValue comprueba un key check value creado por NewCheckValue.
func (c *Cipher) VerifyCheckValue(kcv []byte) error {
	plaintext, err := c.Decrypt(kcv)
	if err != nil {
		return ErrWrongMasterKey
	}
	if subtle.ConstantTimeCompare(plaintext, []byte(checkValuePlaintext)) != 1 {
		return ErrWrongMasterKey
	}
	return nil
}
```

- [ ] **Step 5: Implementar `internal/crypto/masked.go`**

```go
package crypto

import "log/slog"

// maskPrefix son los cuatro puntos medios que preceden a los últimos 4 caracteres.
const maskPrefix = "••••"

// Secret es una credencial en claro que se enmascara al formatearse o loguearse.
// Para obtener el valor real hay que llamar a Reveal() de forma explícita, lo que
// hace que una fuga accidental requiera escribirla a propósito.
type Secret string

// Reveal devuelve la credencial en claro. Úsalo solo al enviarla a su destino real.
func (s Secret) Reveal() string { return string(s) }

// Last4 devuelve los últimos 4 caracteres, o "" si el secreto es más corto.
// Se persiste junto al ciphertext para poder enmascarar sin la master key.
func (s Secret) Last4() string {
	r := []rune(string(s))
	if len(r) < 4 {
		return ""
	}
	return string(r[len(r)-4:])
}

// Mask devuelve la representación pública: "••••" más los últimos 4 caracteres.
func (s Secret) Mask() string { return maskPrefix + s.Last4() }

// String implementa fmt.Stringer con la máscara, para que %s, %v y %q no filtren.
func (s Secret) String() string { return s.Mask() }

// LogValue implementa slog.LogValuer con la máscara.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.Mask()) }
```

- [ ] **Step 6: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/crypto/ -v -count=1`
Expected: PASS en los 12 tests.

- [ ] **Step 7: Commit**

```bash
git add internal/crypto/secret.go internal/crypto/masked.go internal/crypto/secret_test.go internal/crypto/masked_test.go
git commit -m "feat(crypto): AES-256-GCM con key check value y tipo Secret enmascarado"
```

---

### Task 3: Hash de contraseñas con argon2id

**Files:**
- Create: `internal/crypto/password.go`
- Test: `internal/crypto/password_test.go`
- Modify: `go.mod` (añade `golang.org/x/crypto`)

**Interfaces:**
- Consumes: nada.
- Produces: `crypto.HashPassword(password string) (string, error)` y
  `crypto.VerifyPassword(encoded, password string) (bool, error)`.
  El formato codificado es el estándar de PHC:
  `$argon2id$v=19$m=65536,t=3,p=4$<salt-b64>$<hash-b64>` con base64 raw-std sin padding.

- [ ] **Step 1: Añadir la dependencia**

```bash
go get golang.org/x/crypto@latest
```

- [ ] **Step 2: Escribir el test que falla**

`internal/crypto/password_test.go`:

```go
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
```

- [ ] **Step 3: Ejecutar el test y verificar que falla**

Run: `go test ./internal/crypto/ -run Password -v`
Expected: FAIL — `undefined: crypto.HashPassword`.

- [ ] **Step 4: Implementar `internal/crypto/password.go`**

```go
package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parámetros de argon2id. Cambiarlos invalida los hashes existentes: si alguna vez
// hay que subirlos, habrá que rehashear en el siguiente login correcto.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

var b64 = base64.RawStdEncoding

// HashPassword devuelve la contraseña hasheada en formato PHC.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("la contraseña no puede estar vacía")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(hash),
	), nil
}

// VerifyPassword comprueba una contraseña contra un codificado de HashPassword.
// Devuelve error solo si el codificado está malformado; una contraseña equivocada
// es (false, nil).
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errors.New("hash de contraseña malformado")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, errors.New("hash de contraseña malformado: versión")
	}
	if version != argon2.Version {
		return false, fmt.Errorf("versión de argon2 no soportada: %d", version)
	}

	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, errors.New("hash de contraseña malformado: parámetros")
	}

	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("hash de contraseña malformado: salt")
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, errors.New("hash de contraseña malformado: hash")
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
```

- [ ] **Step 5: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/crypto/ -v -count=1 && go mod tidy && go vet ./...`
Expected: PASS en los 18 tests del paquete.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/crypto/password.go internal/crypto/password_test.go
git commit -m "feat(crypto): hash de contraseñas con argon2id en formato PHC"
```

---

### Task 4: SQLite, migraciones versionadas y esquema inicial

**Files:**
- Create: `internal/store/db.go`
- Create: `internal/store/migrations/0001_initial.sql`
- Test: `internal/store/db_test.go`
- Modify: `go.mod` (añade `modernc.org/sqlite`)

**Interfaces:**
- Consumes: nada.
- Produces: `store.DB` (struct con un `*sql.DB` embebido no exportado),
  `store.Open(ctx context.Context, path string) (*DB, error)`,
  `(*DB).Close() error`, `(*DB).SQL() *sql.DB` (solo para tests).
  Constante `store.SchemaVersion = 1`.

- [ ] **Step 1: Añadir la dependencia**

```bash
go get modernc.org/sqlite@latest
```

- [ ] **Step 2: Escribir la migración `internal/store/migrations/0001_initial.sql`**

```sql
-- Fila única (id = 1) con la configuración persistente del servicio.
CREATE TABLE settings (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    ingest_app           TEXT    NOT NULL DEFAULT 'live',
    ingest_key_encrypted BLOB    NOT NULL,
    ingest_key_last4     TEXT    NOT NULL,
    password_hash        TEXT    NOT NULL DEFAULT '',
    master_key_check     BLOB    NOT NULL,
    created_at           TEXT    NOT NULL,
    updated_at           TEXT    NOT NULL
);

-- stream_key_last4 está desnormalizado a propósito: permite enmascarar el listado
-- sin descifrar nada, de forma que la master key solo se usa al revelar.
CREATE TABLE destinations (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    name                 TEXT    NOT NULL,
    platform             TEXT    NOT NULL
                                 CHECK (platform IN ('youtube','twitch','facebook','kick','x','custom')),
    rtmp_url             TEXT    NOT NULL,
    stream_key_encrypted BLOB    NOT NULL,
    stream_key_last4     TEXT    NOT NULL,
    enabled              INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    sort_order           INTEGER NOT NULL,
    created_at           TEXT    NOT NULL,
    updated_at           TEXT    NOT NULL
);

CREATE INDEX idx_destinations_sort ON destinations (sort_order);

CREATE TABLE sessions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at  TEXT    NOT NULL,
    ended_at    TEXT,
    width       INTEGER,
    height      INTEGER,
    bitrate_bps INTEGER
);

CREATE INDEX idx_sessions_started ON sessions (started_at DESC);

-- Las referencias son ON DELETE SET NULL: borrar un destino no debe borrar la
-- evidencia de lo que pasó con él.
CREATE TABLE events (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id     INTEGER REFERENCES sessions (id) ON DELETE SET NULL,
    destination_id INTEGER REFERENCES destinations (id) ON DELETE SET NULL,
    level          TEXT    NOT NULL CHECK (level IN ('info', 'warn', 'error')),
    kind           TEXT    NOT NULL,
    message        TEXT    NOT NULL,
    created_at     TEXT    NOT NULL
);

CREATE INDEX idx_events_created ON events (created_at DESC);
```

- [ ] **Step 3: Escribir el test que falla**

`internal/store/db_test.go`:

```go
package store_test

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func openTemp(t *testing.T) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesSchema(t *testing.T) {
	db := openTemp(t)

	rows, err := db.SQL().QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	sort.Strings(got)

	want := []string{"destinations", "events", "sessions", "settings"}
	if len(got) != len(want) {
		t.Fatalf("tablas = %v, quería %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tablas = %v, quería %v", got, want)
		}
	}
}

func TestOpenSetsSchemaVersion(t *testing.T) {
	db := openTemp(t)

	var version int
	if err := db.SQL().QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version != store.SchemaVersion {
		t.Errorf("user_version = %d, quería %d", version, store.SchemaVersion)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	first, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("primer Open: %v", err)
	}
	if _, err := first.SQL().ExecContext(ctx,
		`INSERT INTO sessions (started_at) VALUES ('2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	first.Close()

	second, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("segundo Open: %v", err)
	}
	defer second.Close()

	var n int
	if err := second.SQL().QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("reabrir la base perdió datos: %d filas, quería 1", n)
	}
}

func TestOpenEnablesWALAndForeignKeys(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	var mode string
	if err := db.SQL().QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, quería \"wal\"", mode)
	}

	var fk int
	if err := db.SQL().QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, quería 1", fk)
	}
}

func TestSchemaRejectsUnknownPlatform(t *testing.T) {
	db := openTemp(t)
	_, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO destinations
		 (name, platform, rtmp_url, stream_key_encrypted, stream_key_last4, sort_order, created_at, updated_at)
		 VALUES ('x', 'vimeo', 'rtmp://x', X'00', '', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("quería que el CHECK rechazara la plataforma 'vimeo'")
	}
}
```

- [ ] **Step 4: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/store/ -v`
Expected: FAIL — el paquete `store` no existe.

- [ ] **Step 5: Implementar `internal/store/db.go`**

```go
// Package store envuelve la base SQLite: apertura, migraciones y repositorios.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // driver "sqlite", puro Go
)

// SchemaVersion es la última migración incluida en el binario.
const SchemaVersion = 1

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB es la base de datos del servicio.
type DB struct {
	db *sql.DB
}

// SQL expone el *sql.DB subyacente. Solo para tests y para los repositorios
// de este paquete; el resto del programa usa los métodos tipados.
func (d *DB) SQL() *sql.DB { return d.db }

// Close cierra la base de datos.
func (d *DB) Close() error { return d.db.Close() }

// Open abre (o crea) la base en path, aplica los pragmas y corre las migraciones
// pendientes. Es idempotente: reabrir una base ya migrada no toca los datos.
func Open(ctx context.Context, dbPath string) (*DB, error) {
	dsn := "file:" + dbPath + "?" + url.Values{
		"_pragma": {
			"journal_mode(WAL)",
			"busy_timeout(5000)",
			"foreign_keys(1)",
			"synchronous(NORMAL)",
		},
	}.Encode()

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrir %s: %w", dbPath, err)
	}
	// SQLite tolera mal la escritura concurrente; una sola conexión evita
	// SQLITE_BUSY sin tener que reintentar en cada consulta.
	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("conectar a %s: %w", dbPath, err)
	}

	if err := migrate(ctx, sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}

	return &DB{db: sqlDB}, nil
}

type migration struct {
	version int
	name    string
	sql     string
}

// migrate aplica en orden las migraciones cuya versión supere PRAGMA user_version.
func migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	var current int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("leer user_version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migración %d: begin: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migración %d (%s): %w", m.version, m.name, err)
		}
		// PRAGMA no admite parámetros vinculados; m.version viene de strconv.Atoi
		// sobre el nombre del archivo embebido, así que es un entero de confianza.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migración %d: fijar user_version: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migración %d: commit: %w", m.version, err)
		}
	}
	return nil
}

// loadMigrations lee migrations/*.sql y las ordena por versión. Los nombres deben
// tener la forma NNNN_descripcion.sql.
func loadMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("leer migraciones: %w", err)
	}

	out := make([]migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migración %q: falta el prefijo NNNN_", e.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migración %q: prefijo no numérico: %w", e.Name(), err)
		}
		body, err := migrationsFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("leer %q: %w", e.Name(), err)
		}
		out = append(out, migration{version: version, name: e.Name(), sql: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })

	if len(out) > 0 && out[len(out)-1].version != SchemaVersion {
		return nil, fmt.Errorf(
			"SchemaVersion es %d pero la última migración es %d: actualiza la constante",
			SchemaVersion, out[len(out)-1].version)
	}
	return out, nil
}
```

- [ ] **Step 6: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/store/ -v -count=1`
Expected: PASS en los 5 tests.

Si `TestOpenEnablesWALAndForeignKeys` falla porque `journal_mode` devuelve `delete`,
comprueba que el DSN llega bien codificado: `url.Values.Encode()` produce
`_pragma=journal_mode%28WAL%29&...`, que el driver acepta. Imprime el DSN para
verificarlo antes de cambiar otra cosa.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/store/
git commit -m "feat(store): SQLite con migraciones versionadas y esquema inicial"
```

---

### Task 5: Repositorio de settings y verificación de la master key

**Files:**
- Create: `internal/store/settings.go`
- Test: `internal/store/settings_test.go`

**Interfaces:**
- Consumes: `store.Open`, `store.DB.SQL()`; `crypto.Cipher`, `crypto.Secret`,
  `crypto.ErrWrongMasterKey`.
- Produces:
  ```go
  type Settings struct {
      IngestApp     string
      IngestKeyMask string // "••••1234"; el struct nunca lleva la clave
      PasswordHash  string
      UpdatedAt     time.Time
  }
  func (d *DB) Bootstrap(ctx context.Context, c *crypto.Cipher) error
  func (d *DB) Settings(ctx context.Context) (*Settings, error)
  func (d *DB) RevealIngestKey(ctx context.Context, c *crypto.Cipher) (crypto.Secret, error)
  func (d *DB) RotateIngestKey(ctx context.Context, c *crypto.Cipher) (crypto.Secret, error)
  func (d *DB) SetPasswordHash(ctx context.Context, hash string) error
  func GenerateKey() (crypto.Secret, error)
  ```
  `Settings.IngestKeyMask` es un `string` ya enmascarado, igual que `Destination.KeyMask`
  de la Task 6. Para la clave real hay que llamar a `RevealIngestKey`. Es deliberado que
  no sea un `crypto.Secret`: un `Secret` que solo contiene los últimos 4 haría que
  `Reveal()` devolviera medio secreto, que es peor que no tener el método.

- [ ] **Step 1: Escribir el test que falla**

`internal/store/settings_test.go`:

```go
package store_test

import (
	"context"
	"errors"
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

func TestGenerateKeyIsRandomAndURLSafe(t *testing.T) {
	a, err := store.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	b, err := store.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if a.Reveal() == b.Reveal() {
		t.Fatal("GenerateKey devolvió el mismo valor dos veces")
	}
	if strings.ContainsAny(a.Reveal(), "+/=") {
		t.Errorf("la clave debe ser segura para URL, es %q", a.Reveal())
	}
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/store/ -run 'Bootstrap|Rotate|Password|GenerateKey' -v`
Expected: FAIL — `undefined: (*store.DB).Bootstrap`.

- [ ] **Step 3: Implementar `internal/store/settings.go`**

```go
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
)

// ingestKeyBytes es la entropía de la clave de ingesta: 24 bytes → 32 caracteres
// en base64 seguro para URL, que es lo que se pega en OBS.
const ingestKeyBytes = 24

// Settings es la configuración persistente. IngestKeyMask ya viene enmascarada: para
// obtener la clave real hay que llamar a RevealIngestKey de forma explícita.
type Settings struct {
	IngestApp     string
	IngestKeyMask string
	PasswordHash  string
	UpdatedAt     time.Time
}

// GenerateKey produce una credencial aleatoria segura para usar en una URL.
func GenerateKey() (crypto.Secret, error) {
	buf := make([]byte, ingestKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generar clave: %w", err)
	}
	return crypto.Secret(base64.RawURLEncoding.EncodeToString(buf)), nil
}

// Bootstrap deja la base lista para operar. Si no hay fila de settings, la crea con
// una clave de ingesta nueva y el key check value de c. Si ya existe, verifica que c
// sea la misma master key que la cifró y devuelve crypto.ErrWrongMasterKey si no.
func (d *DB) Bootstrap(ctx context.Context, c *crypto.Cipher) error {
	var kcv []byte
	err := d.db.QueryRowContext(ctx, `SELECT master_key_check FROM settings WHERE id = 1`).Scan(&kcv)
	switch {
	case err == nil:
		return c.VerifyCheckValue(kcv)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("leer settings: %w", err)
	}

	key, err := GenerateKey()
	if err != nil {
		return err
	}
	encrypted, err := c.Encrypt([]byte(key.Reveal()))
	if err != nil {
		return fmt.Errorf("cifrar clave de ingesta: %w", err)
	}
	newKCV, err := c.NewCheckValue()
	if err != nil {
		return fmt.Errorf("key check value: %w", err)
	}

	now := nowRFC3339()
	_, err = d.db.ExecContext(ctx,
		`INSERT INTO settings
		   (id, ingest_app, ingest_key_encrypted, ingest_key_last4, password_hash, master_key_check, created_at, updated_at)
		 VALUES (1, 'live', ?, ?, '', ?, ?, ?)`,
		encrypted, key.Last4(), newKCV, now, now)
	if err != nil {
		return fmt.Errorf("crear settings: %w", err)
	}
	return nil
}

// Settings devuelve la configuración persistente, con la clave de ingesta enmascarada.
func (d *DB) Settings(ctx context.Context) (*Settings, error) {
	var (
		s         Settings
		last4     string
		updatedAt string
	)
	err := d.db.QueryRowContext(ctx,
		`SELECT ingest_app, ingest_key_last4, password_hash, updated_at FROM settings WHERE id = 1`).
		Scan(&s.IngestApp, &last4, &s.PasswordHash, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("leer settings: %w", err)
	}
	// last4 viene ya truncado de la base, así que aquí no hay nada que filtrar.
	s.IngestKeyMask = maskFromLast4(last4)
	s.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &s, nil
}

// RevealIngestKey descifra y devuelve la clave de ingesta en claro.
func (d *DB) RevealIngestKey(ctx context.Context, c *crypto.Cipher) (crypto.Secret, error) {
	var blob []byte
	if err := d.db.QueryRowContext(ctx,
		`SELECT ingest_key_encrypted FROM settings WHERE id = 1`).Scan(&blob); err != nil {
		return "", fmt.Errorf("leer clave de ingesta: %w", err)
	}
	plain, err := c.Decrypt(blob)
	if err != nil {
		return "", fmt.Errorf("descifrar clave de ingesta: %w", err)
	}
	return crypto.Secret(plain), nil
}

// RotateIngestKey genera una clave nueva, la persiste y la devuelve en claro.
func (d *DB) RotateIngestKey(ctx context.Context, c *crypto.Cipher) (crypto.Secret, error) {
	key, err := GenerateKey()
	if err != nil {
		return "", err
	}
	encrypted, err := c.Encrypt([]byte(key.Reveal()))
	if err != nil {
		return "", fmt.Errorf("cifrar clave de ingesta: %w", err)
	}
	if _, err := d.db.ExecContext(ctx,
		`UPDATE settings SET ingest_key_encrypted = ?, ingest_key_last4 = ?, updated_at = ? WHERE id = 1`,
		encrypted, key.Last4(), nowRFC3339()); err != nil {
		return "", fmt.Errorf("rotar clave de ingesta: %w", err)
	}
	return key, nil
}

// SetPasswordHash guarda el hash argon2id de la contraseña del panel.
func (d *DB) SetPasswordHash(ctx context.Context, hash string) error {
	if _, err := d.db.ExecContext(ctx,
		`UPDATE settings SET password_hash = ?, updated_at = ? WHERE id = 1`,
		hash, nowRFC3339()); err != nil {
		return fmt.Errorf("guardar contraseña: %w", err)
	}
	return nil
}

// maskFromLast4 compone la máscara pública a partir de los 4 caracteres guardados,
// usando el mismo formato que crypto.Secret.Mask().
func maskFromLast4(last4 string) string { return crypto.Secret(last4).Mask() }

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }
```

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/store/ -v -count=1`
Expected: PASS en los 12 tests del paquete.

- [ ] **Step 5: Commit**

```bash
git add internal/store/settings.go internal/store/settings_test.go
git commit -m "feat(store): settings con clave de ingesta cifrada y verificación de master key"
```

---

### Task 6: Repositorio de destinos

**Files:**
- Create: `internal/store/destinations.go`
- Test: `internal/store/destinations_test.go`

**Interfaces:**
- Consumes: `store.DB`, `crypto.Cipher`, `crypto.Secret`.
- Produces:
  ```go
  type Platform string
  const (
      PlatformYouTube  Platform = "youtube"
      PlatformTwitch   Platform = "twitch"
      PlatformFacebook Platform = "facebook"
      PlatformKick     Platform = "kick"
      PlatformX        Platform = "x"
      PlatformCustom   Platform = "custom"
  )
  func (p Platform) Valid() bool

  type Destination struct {
      ID        int64
      Name      string
      Platform  Platform
      RTMPURL   string
      KeyMask   string // "••••1234"; el struct nunca lleva la clave
      Enabled   bool
      SortOrder int
      CreatedAt time.Time
      UpdatedAt time.Time
  }

  type NewDestination struct {
      Name     string
      Platform Platform
      RTMPURL  string
      Key      crypto.Secret
      Enabled  bool
  }

  type DestinationPatch struct {
      Name     *string
      Platform *Platform
      RTMPURL  *string
      Key      *crypto.Secret
      Enabled  *bool
  }

  var ErrDestinationNotFound = errors.New("destino no encontrado")

  func (d *DB) ListDestinations(ctx context.Context) ([]Destination, error)
  func (d *DB) CreateDestination(ctx context.Context, c *crypto.Cipher, in NewDestination) (*Destination, error)
  func (d *DB) UpdateDestination(ctx context.Context, c *crypto.Cipher, id int64, patch DestinationPatch) (*Destination, error)
  func (d *DB) DeleteDestination(ctx context.Context, id int64) error
  func (d *DB) ReorderDestinations(ctx context.Context, ids []int64) error
  func (d *DB) RevealDestinationKey(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error)
  ```
  **Invariante que hay que preservar:** `Destination` no tiene campo para la clave.
  Obtenerla exige llamar a `RevealDestinationKey`, así que ninguna serialización
  accidental del struct puede filtrarla (spec §8).

- [ ] **Step 1: Escribir el test que falla**

`internal/store/destinations_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

func newDest(name string) store.NewDestination {
	return store.NewDestination{
		Name:     name,
		Platform: store.PlatformYouTube,
		RTMPURL:  "rtmp://a.rtmp.youtube.com/live2",
		Key:      crypto.Secret("abcd-efgh-ijkl-8765"),
		Enabled:  true,
	}
}

func TestCreateDestinationStoresKeyEncrypted(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	got, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	if got.ID == 0 {
		t.Error("CreateDestination no asignó ID")
	}
	if got.KeyMask != "••••8765" {
		t.Errorf("KeyMask = %q, quería \"••••8765\"", got.KeyMask)
	}

	var blob []byte
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT stream_key_encrypted FROM destinations WHERE id = ?`, got.ID).Scan(&blob); err != nil {
		t.Fatalf("select: %v", err)
	}
	if strings.Contains(string(blob), "abcd-efgh") {
		t.Error("la clave del destino está en claro en la base")
	}
}

func TestRevealDestinationKey(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	created, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	key, err := db.RevealDestinationKey(ctx, c, created.ID)
	if err != nil {
		t.Fatalf("RevealDestinationKey: %v", err)
	}
	if key.Reveal() != "abcd-efgh-ijkl-8765" {
		t.Errorf("clave revelada = %q", key.Reveal())
	}
}

func TestRevealDestinationKeyNotFound(t *testing.T) {
	db, c := bootstrapped(t)
	_, err := db.RevealDestinationKey(context.Background(), c, 999)
	if !errors.Is(err, store.ErrDestinationNotFound) {
		t.Fatalf("err = %v, quería ErrDestinationNotFound", err)
	}
}

func TestCreateDestinationRejectsUnknownPlatform(t *testing.T) {
	db, c := bootstrapped(t)
	in := newDest("Raro")
	in.Platform = "vimeo"
	if _, err := db.CreateDestination(context.Background(), c, in); err == nil {
		t.Fatal("quería error con una plataforma desconocida")
	}
}

func TestCreateDestinationAssignsIncreasingSortOrder(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	first, err := db.CreateDestination(ctx, c, newDest("A"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	second, err := db.CreateDestination(ctx, c, newDest("B"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	if second.SortOrder <= first.SortOrder {
		t.Errorf("sort_order = %d y %d, el segundo debería ser mayor", first.SortOrder, second.SortOrder)
	}
}

func TestListDestinationsOrderedBySortOrder(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	for _, name := range []string{"A", "B", "C"} {
		if _, err := db.CreateDestination(ctx, c, newDest(name)); err != nil {
			t.Fatalf("CreateDestination %s: %v", name, err)
		}
	}

	list, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d, quería 3", len(list))
	}
	for i, want := range []string{"A", "B", "C"} {
		if list[i].Name != want {
			t.Errorf("posición %d = %q, quería %q", i, list[i].Name, want)
		}
		if list[i].KeyMask != "••••8765" {
			t.Errorf("posición %d: KeyMask = %q", i, list[i].KeyMask)
		}
	}
}

func TestReorderDestinations(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	var ids []int64
	for _, name := range []string{"A", "B", "C"} {
		d, err := db.CreateDestination(ctx, c, newDest(name))
		if err != nil {
			t.Fatalf("CreateDestination: %v", err)
		}
		ids = append(ids, d.ID)
	}

	// C, A, B
	if err := db.ReorderDestinations(ctx, []int64{ids[2], ids[0], ids[1]}); err != nil {
		t.Fatalf("ReorderDestinations: %v", err)
	}

	list, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	for i, want := range []string{"C", "A", "B"} {
		if list[i].Name != want {
			t.Errorf("posición %d = %q, quería %q", i, list[i].Name, want)
		}
	}
}

func TestReorderDestinationsRejectsIncompleteList(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	a, err := db.CreateDestination(ctx, c, newDest("A"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	if _, err := db.CreateDestination(ctx, c, newDest("B")); err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	if err := db.ReorderDestinations(ctx, []int64{a.ID}); err == nil {
		t.Fatal("quería error: la lista no incluye todos los destinos")
	}
}

func TestUpdateDestinationPatchesOnlyGivenFields(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	created, err := db.CreateDestination(ctx, c, newDest("Antes"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	name := "Después"
	updated, err := db.UpdateDestination(ctx, c, created.ID, store.DestinationPatch{Name: &name})
	if err != nil {
		t.Fatalf("UpdateDestination: %v", err)
	}
	if updated.Name != "Después" {
		t.Errorf("Name = %q", updated.Name)
	}
	if updated.RTMPURL != created.RTMPURL {
		t.Errorf("RTMPURL cambió sin pedirlo: %q", updated.RTMPURL)
	}

	key, err := db.RevealDestinationKey(ctx, c, created.ID)
	if err != nil {
		t.Fatalf("RevealDestinationKey: %v", err)
	}
	if key.Reveal() != "abcd-efgh-ijkl-8765" {
		t.Error("la clave cambió sin pedirlo")
	}
}

func TestUpdateDestinationReplacesKey(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	created, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	nueva := crypto.Secret("zzzz-yyyy-xxxx-4321")
	updated, err := db.UpdateDestination(ctx, c, created.ID, store.DestinationPatch{Key: &nueva})
	if err != nil {
		t.Fatalf("UpdateDestination: %v", err)
	}
	if updated.KeyMask != "••••4321" {
		t.Errorf("KeyMask = %q, quería \"••••4321\"", updated.KeyMask)
	}

	key, err := db.RevealDestinationKey(ctx, c, created.ID)
	if err != nil {
		t.Fatalf("RevealDestinationKey: %v", err)
	}
	if key.Reveal() != "zzzz-yyyy-xxxx-4321" {
		t.Errorf("clave = %q", key.Reveal())
	}
}

func TestUpdateDestinationNotFound(t *testing.T) {
	db, c := bootstrapped(t)
	name := "x"
	_, err := db.UpdateDestination(context.Background(), c, 999, store.DestinationPatch{Name: &name})
	if !errors.Is(err, store.ErrDestinationNotFound) {
		t.Fatalf("err = %v, quería ErrDestinationNotFound", err)
	}
}

func TestDeleteDestination(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	created, err := db.CreateDestination(ctx, c, newDest("A"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	if err := db.DeleteDestination(ctx, created.ID); err != nil {
		t.Fatalf("DeleteDestination: %v", err)
	}

	list, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("quedaron %d destinos", len(list))
	}

	if err := db.DeleteDestination(ctx, created.ID); !errors.Is(err, store.ErrDestinationNotFound) {
		t.Fatalf("segundo delete = %v, quería ErrDestinationNotFound", err)
	}
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/store/ -run Destination -v`
Expected: FAIL — `undefined: store.NewDestination`.

- [ ] **Step 3: Implementar `internal/store/destinations.go`**

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
)

// ErrDestinationNotFound se devuelve cuando el id no existe.
var ErrDestinationNotFound = errors.New("destino no encontrado")

// Platform es el conjunto cerrado de plataformas soportadas. Duplica el CHECK del
// esquema a propósito: así el error llega antes y con un mensaje legible.
type Platform string

const (
	PlatformYouTube  Platform = "youtube"
	PlatformTwitch   Platform = "twitch"
	PlatformFacebook Platform = "facebook"
	PlatformKick     Platform = "kick"
	PlatformX        Platform = "x"
	PlatformCustom   Platform = "custom"
)

// Valid indica si p es una de las plataformas soportadas.
func (p Platform) Valid() bool {
	switch p {
	case PlatformYouTube, PlatformTwitch, PlatformFacebook, PlatformKick, PlatformX, PlatformCustom:
		return true
	}
	return false
}

// Destination es un destino de retransmisión. No tiene campo para la clave: para
// obtenerla hay que llamar a RevealDestinationKey, de modo que serializar este
// struct nunca puede filtrarla.
type Destination struct {
	ID        int64
	Name      string
	Platform  Platform
	RTMPURL   string
	KeyMask   string
	Enabled   bool
	SortOrder int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewDestination son los datos para crear un destino.
type NewDestination struct {
	Name     string
	Platform Platform
	RTMPURL  string
	Key      crypto.Secret
	Enabled  bool
}

// DestinationPatch es una modificación parcial: los campos nil no se tocan.
type DestinationPatch struct {
	Name     *string
	Platform *Platform
	RTMPURL  *string
	Key      *crypto.Secret
	Enabled  *bool
}

// ListDestinations devuelve todos los destinos ordenados por sort_order.
func (d *DB) ListDestinations(ctx context.Context) ([]Destination, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, name, platform, rtmp_url, stream_key_last4, enabled, sort_order, created_at, updated_at
		 FROM destinations ORDER BY sort_order, id`)
	if err != nil {
		return nil, fmt.Errorf("listar destinos: %w", err)
	}
	defer rows.Close()

	out := []Destination{}
	for rows.Next() {
		dest, err := scanDestination(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *dest)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar destinos: %w", err)
	}
	return out, nil
}

// CreateDestination cifra la clave y añade el destino al final del orden.
func (d *DB) CreateDestination(ctx context.Context, c *crypto.Cipher, in NewDestination) (*Destination, error) {
	if !in.Platform.Valid() {
		return nil, fmt.Errorf("plataforma desconocida %q", in.Platform)
	}
	if in.Name == "" {
		return nil, errors.New("el nombre no puede estar vacío")
	}
	if in.RTMPURL == "" {
		return nil, errors.New("la URL RTMP no puede estar vacía")
	}

	encrypted, err := c.Encrypt([]byte(in.Key.Reveal()))
	if err != nil {
		return nil, fmt.Errorf("cifrar la clave del destino: %w", err)
	}

	var next int
	if err := d.db.QueryRowContext(ctx,
		`SELECT coalesce(max(sort_order), -1) + 1 FROM destinations`).Scan(&next); err != nil {
		return nil, fmt.Errorf("calcular sort_order: %w", err)
	}

	now := nowRFC3339()
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO destinations
		   (name, platform, rtmp_url, stream_key_encrypted, stream_key_last4, enabled, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Name, string(in.Platform), in.RTMPURL, encrypted, in.Key.Last4(), boolToInt(in.Enabled), next, now, now)
	if err != nil {
		return nil, fmt.Errorf("crear destino: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("crear destino: %w", err)
	}
	return d.destination(ctx, id)
}

// UpdateDestination aplica una modificación parcial. Los campos nil del patch
// se dejan como están.
func (d *DB) UpdateDestination(ctx context.Context, c *crypto.Cipher, id int64, patch DestinationPatch) (*Destination, error) {
	if _, err := d.destination(ctx, id); err != nil {
		return nil, err
	}

	sets := []string{}
	args := []any{}

	if patch.Name != nil {
		if *patch.Name == "" {
			return nil, errors.New("el nombre no puede estar vacío")
		}
		sets = append(sets, "name = ?")
		args = append(args, *patch.Name)
	}
	if patch.Platform != nil {
		if !patch.Platform.Valid() {
			return nil, fmt.Errorf("plataforma desconocida %q", *patch.Platform)
		}
		sets = append(sets, "platform = ?")
		args = append(args, string(*patch.Platform))
	}
	if patch.RTMPURL != nil {
		if *patch.RTMPURL == "" {
			return nil, errors.New("la URL RTMP no puede estar vacía")
		}
		sets = append(sets, "rtmp_url = ?")
		args = append(args, *patch.RTMPURL)
	}
	if patch.Key != nil {
		encrypted, err := c.Encrypt([]byte(patch.Key.Reveal()))
		if err != nil {
			return nil, fmt.Errorf("cifrar la clave del destino: %w", err)
		}
		sets = append(sets, "stream_key_encrypted = ?", "stream_key_last4 = ?")
		args = append(args, encrypted, patch.Key.Last4())
	}
	if patch.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, boolToInt(*patch.Enabled))
	}

	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, nowRFC3339(), id)
		query := "UPDATE destinations SET " + joinComma(sets) + " WHERE id = ?"
		if _, err := d.db.ExecContext(ctx, query, args...); err != nil {
			return nil, fmt.Errorf("actualizar destino: %w", err)
		}
	}
	return d.destination(ctx, id)
}

// DeleteDestination borra el destino. Los eventos asociados sobreviven con
// destination_id a NULL.
func (d *DB) DeleteDestination(ctx context.Context, id int64) error {
	res, err := d.db.ExecContext(ctx, `DELETE FROM destinations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("borrar destino: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("borrar destino: %w", err)
	}
	if n == 0 {
		return ErrDestinationNotFound
	}
	return nil
}

// ReorderDestinations fija el orden a partir de la secuencia de ids recibida.
// Exige la lista completa: un reorden parcial dejaría huecos silenciosos.
func (d *DB) ReorderDestinations(ctx context.Context, ids []int64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reordenar: %w", err)
	}
	defer tx.Rollback()

	var total int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM destinations`).Scan(&total); err != nil {
		return fmt.Errorf("reordenar: %w", err)
	}
	if total != len(ids) {
		return fmt.Errorf("reordenar exige los %d destinos, se recibieron %d", total, len(ids))
	}

	for i, id := range ids {
		res, err := tx.ExecContext(ctx, `UPDATE destinations SET sort_order = ? WHERE id = ?`, i, id)
		if err != nil {
			return fmt.Errorf("reordenar: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("reordenar: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("reordenar: %w (id %d)", ErrDestinationNotFound, id)
		}
	}
	return tx.Commit()
}

// RevealDestinationKey descifra y devuelve la clave del destino en claro.
// Es el único camino para obtenerla; quien lo llame debe registrar un evento.
func (d *DB) RevealDestinationKey(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error) {
	var blob []byte
	err := d.db.QueryRowContext(ctx,
		`SELECT stream_key_encrypted FROM destinations WHERE id = ?`, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDestinationNotFound
	}
	if err != nil {
		return "", fmt.Errorf("leer la clave del destino: %w", err)
	}
	plain, err := c.Decrypt(blob)
	if err != nil {
		return "", fmt.Errorf("descifrar la clave del destino: %w", err)
	}
	return crypto.Secret(plain), nil
}

func (d *DB) destination(ctx context.Context, id int64) (*Destination, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, name, platform, rtmp_url, stream_key_last4, enabled, sort_order, created_at, updated_at
		 FROM destinations WHERE id = ?`, id)
	dest, err := scanDestination(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDestinationNotFound
	}
	return dest, err
}

// scanner abstrae *sql.Row y *sql.Rows, que comparten la firma de Scan.
type scanner interface{ Scan(dest ...any) error }

func scanDestination(s scanner) (*Destination, error) {
	var (
		dest      Destination
		platform  string
		last4     string
		enabled   int
		createdAt string
		updatedAt string
	)
	if err := s.Scan(&dest.ID, &dest.Name, &platform, &dest.RTMPURL, &last4,
		&enabled, &dest.SortOrder, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("leer destino: %w", err)
	}

	dest.Platform = Platform(platform)
	dest.KeyMask = crypto.Secret(last4).Mask()
	dest.Enabled = enabled == 1

	var err error
	if dest.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return nil, fmt.Errorf("created_at inválido: %w", err)
	}
	if dest.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &dest, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
```

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/store/ -v -count=1`
Expected: PASS en los 24 tests del paquete.

- [ ] **Step 5: Commit**

```bash
git add internal/store/destinations.go internal/store/destinations_test.go
git commit -m "feat(store): CRUD de destinos con claves cifradas y orden persistente"
```

---

### Task 7: Repositorio de sesiones y eventos

**Files:**
- Create: `internal/store/events.go`
- Test: `internal/store/events_test.go`

**Interfaces:**
- Consumes: `store.DB`.
- Produces:
  ```go
  type Level string
  const (LevelInfo Level = "info"; LevelWarn Level = "warn"; LevelError Level = "error")

  type Session struct {
      ID         int64
      StartedAt  time.Time
      EndedAt    *time.Time
      Width      int
      Height     int
      BitrateBPS int
  }

  type Event struct {
      ID            int64
      SessionID     *int64
      DestinationID *int64
      Level         Level
      Kind          string
      Message       string
      CreatedAt     time.Time
  }

  func (d *DB) StartSession(ctx context.Context) (int64, error)
  func (d *DB) FinishSession(ctx context.Context, id int64, width, height, bitrateBPS int) error
  func (d *DB) LogEvent(ctx context.Context, e Event) (int64, error)
  func (d *DB) RecentEvents(ctx context.Context, limit int) ([]Event, error)
  ```
  `RecentEvents` devuelve del más reciente al más antiguo y acota `limit` al rango
  1..1000 (0 o negativo → 100).

- [ ] **Step 1: Escribir el test que falla**

`internal/store/events_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestStartAndFinishSession(t *testing.T) {
	db, _ := bootstrapped(t)
	ctx := context.Background()

	id, err := db.StartSession(ctx)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if id == 0 {
		t.Fatal("StartSession no devolvió id")
	}
	if err := db.FinishSession(ctx, id, 1920, 1080, 6_000_000); err != nil {
		t.Fatalf("FinishSession: %v", err)
	}

	var (
		ended   *string
		width   int
		height  int
		bitrate int
	)
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT ended_at, width, height, bitrate_bps FROM sessions WHERE id = ?`, id).
		Scan(&ended, &width, &height, &bitrate); err != nil {
		t.Fatalf("select: %v", err)
	}
	if ended == nil {
		t.Error("ended_at sigue en NULL tras FinishSession")
	}
	if width != 1920 || height != 1080 || bitrate != 6_000_000 {
		t.Errorf("got %dx%d @ %d", width, height, bitrate)
	}
}

func TestLogEventAndRecentEventsAreNewestFirst(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	sessionID, err := db.StartSession(ctx)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	for _, kind := range []string{"primero", "segundo", "tercero"} {
		if _, err := db.LogEvent(ctx, store.Event{
			SessionID:     &sessionID,
			DestinationID: &dest.ID,
			Level:         store.LevelInfo,
			Kind:          kind,
			Message:       "mensaje de " + kind,
		}); err != nil {
			t.Fatalf("LogEvent %s: %v", kind, err)
		}
	}

	events, err := db.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len = %d, quería 3", len(events))
	}
	if events[0].Kind != "tercero" {
		t.Errorf("el primero debería ser el más reciente, es %q", events[0].Kind)
	}
	if events[0].SessionID == nil || *events[0].SessionID != sessionID {
		t.Error("session_id no se persistió")
	}
	if events[0].DestinationID == nil || *events[0].DestinationID != dest.ID {
		t.Error("destination_id no se persistió")
	}
}

func TestRecentEventsRespectsLimit(t *testing.T) {
	db, _ := bootstrapped(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := db.LogEvent(ctx, store.Event{
			Level:   store.LevelWarn,
			Kind:    "prueba",
			Message: "x",
		}); err != nil {
			t.Fatalf("LogEvent: %v", err)
		}
	}

	events, err := db.RecentEvents(ctx, 2)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("len = %d, quería 2", len(events))
	}
}

func TestLogEventRejectsUnknownLevel(t *testing.T) {
	db, _ := bootstrapped(t)
	_, err := db.LogEvent(context.Background(), store.Event{
		Level:   "crítico",
		Kind:    "prueba",
		Message: "x",
	})
	if err == nil {
		t.Fatal("quería error con un nivel desconocido")
	}
}

// Borrar un destino no debe borrar la evidencia de lo que le pasó.
func TestDeleteDestinationKeepsItsEvents(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	if _, err := db.LogEvent(ctx, store.Event{
		DestinationID: &dest.ID,
		Level:         store.LevelError,
		Kind:          "connect_failed",
		Message:       "connection refused",
	}); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}

	if err := db.DeleteDestination(ctx, dest.ID); err != nil {
		t.Fatalf("DeleteDestination: %v", err)
	}

	events, err := db.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len = %d, quería 1: el evento debe sobrevivir al destino", len(events))
	}
	if events[0].DestinationID != nil {
		t.Errorf("destination_id = %v, quería NULL tras ON DELETE SET NULL", *events[0].DestinationID)
	}
	if events[0].Message != "connection refused" {
		t.Errorf("el mensaje se perdió: %q", events[0].Message)
	}
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/store/ -run 'Session|Event' -v`
Expected: FAIL — `undefined: (*store.DB).StartSession`.

- [ ] **Step 3: Implementar `internal/store/events.go`**

```go
package store

import (
	"context"
	"fmt"
	"time"
)

// Level es la severidad de un evento.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Valid indica si l es una severidad soportada.
func (l Level) Valid() bool {
	switch l {
	case LevelInfo, LevelWarn, LevelError:
		return true
	}
	return false
}

const (
	defaultEventLimit = 100
	maxEventLimit     = 1000
)

// Session es una transmisión: desde que OBS conecta hasta que se va.
type Session struct {
	ID         int64
	StartedAt  time.Time
	EndedAt    *time.Time
	Width      int
	Height     int
	BitrateBPS int
}

// Event es una entrada del log persistente. SessionID y DestinationID son
// opcionales: un evento del sistema no tiene ninguno de los dos.
type Event struct {
	ID            int64
	SessionID     *int64
	DestinationID *int64
	Level         Level
	Kind          string
	Message       string
	CreatedAt     time.Time
}

// StartSession abre una sesión y devuelve su id.
func (d *DB) StartSession(ctx context.Context) (int64, error) {
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO sessions (started_at) VALUES (?)`, nowRFC3339())
	if err != nil {
		return 0, fmt.Errorf("iniciar sesión: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("iniciar sesión: %w", err)
	}
	return id, nil
}

// FinishSession cierra la sesión y guarda lo que se midió del ingest.
func (d *DB) FinishSession(ctx context.Context, id int64, width, height, bitrateBPS int) error {
	if _, err := d.db.ExecContext(ctx,
		`UPDATE sessions SET ended_at = ?, width = ?, height = ?, bitrate_bps = ? WHERE id = ?`,
		nowRFC3339(), width, height, bitrateBPS, id); err != nil {
		return fmt.Errorf("cerrar sesión: %w", err)
	}
	return nil
}

// LogEvent persiste un evento y devuelve su id. Ignora e.ID y e.CreatedAt.
func (d *DB) LogEvent(ctx context.Context, e Event) (int64, error) {
	if !e.Level.Valid() {
		return 0, fmt.Errorf("nivel de evento desconocido %q", e.Level)
	}
	if e.Kind == "" {
		return 0, fmt.Errorf("el evento necesita un kind")
	}
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO events (session_id, destination_id, level, kind, message, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.SessionID, e.DestinationID, string(e.Level), e.Kind, e.Message, nowRFC3339())
	if err != nil {
		return 0, fmt.Errorf("registrar evento: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("registrar evento: %w", err)
	}
	return id, nil
}

// RecentEvents devuelve los eventos del más reciente al más antiguo.
func (d *DB) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = defaultEventLimit
	}
	if limit > maxEventLimit {
		limit = maxEventLimit
	}

	rows, err := d.db.QueryContext(ctx,
		`SELECT id, session_id, destination_id, level, kind, message, created_at
		 FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("leer eventos: %w", err)
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		var (
			e         Event
			level     string
			createdAt string
		)
		if err := rows.Scan(&e.ID, &e.SessionID, &e.DestinationID, &level,
			&e.Kind, &e.Message, &createdAt); err != nil {
			return nil, fmt.Errorf("leer eventos: %w", err)
		}
		e.Level = Level(level)
		if e.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, fmt.Errorf("created_at inválido: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("leer eventos: %w", err)
	}
	return out, nil
}
```

Nota sobre `ORDER BY id DESC`: es más fiable que `ORDER BY created_at DESC` porque
varios eventos del mismo milisegundo empatarían y el orden quedaría indefinido. El
índice `idx_events_created` sigue siendo útil para los filtros por fecha de la fase 4.

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/store/ -v -count=1`
Expected: PASS en los 29 tests del paquete.

- [ ] **Step 5: Commit**

```bash
git add internal/store/events.go internal/store/events_test.go
git commit -m "feat(store): sesiones y log persistente de eventos"
```

---

### Task 8: Binario, arranque y apagado limpio

**Files:**
- Create: `cmd/splitstream/main.go`
- Test: `cmd/splitstream/main_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `config.Load`, `crypto.NewCipher`, `crypto.ErrWrongMasterKey`,
  `store.Open`, `(*store.DB).Bootstrap`, `(*store.DB).Settings`, `(*store.DB).Close`.
- Produces: el binario `splitstream`. Bandera `-genkey`, que imprime una master key
  nueva en base64 y sale con código 0. Función interna
  `run(ctx context.Context, out io.Writer) error` para poder testear el arranque.

- [ ] **Step 1: Escribir el test que falla**

`cmd/splitstream/main_test.go`:

```go
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
```

- [ ] **Step 2: Ejecutar el test y verificar que falla**

Run: `go test ./cmd/splitstream/ -v`
Expected: FAIL — `undefined: generateMasterKey`.

- [ ] **Step 3: Implementar `cmd/splitstream/main.go`**

```go
// Command splitstream es el servicio de retransmisión RTMP.
//
// Fase 1: arranca, valida la configuración y la master key, migra la base de datos
// y espera a SIGTERM. Todavía no hay servidor RTMP ni API HTTP.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/aprendomx/splitstream/internal/config"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

func main() {
	genkey := flag.Bool("genkey", false, "imprime una SPLITSTREAM_MASTER_KEY nueva y sale")
	flag.Parse()

	if *genkey {
		key, err := generateMasterKey()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println(key)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Stdout); err != nil {
		// El error puede venir de config o de la master key; ninguno de los dos
		// incluye material secreto en su mensaje.
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// generateMasterKey produce una master key de 32 bytes en base64 estándar.
func generateMasterKey() (string, error) {
	buf := make([]byte, config.MasterKeyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generar master key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func run(ctx context.Context, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	cipher, err := crypto.NewCipher(cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("inicializar el cifrado: %w", err)
	}

	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Bootstrap(ctx, cipher); err != nil {
		if errors.Is(err, crypto.ErrWrongMasterKey) {
			return fmt.Errorf(
				"%w: %s fue cifrada con otra SPLITSTREAM_MASTER_KEY. "+
					"Restaura la clave original o empieza con una base de datos nueva",
				err, cfg.DBPath)
		}
		return err
	}

	settings, err := db.Settings(ctx)
	if err != nil {
		return err
	}

	logger.Info("splitstream arrancado", "config", cfg,
		"ingest_app", settings.IngestApp, "ingest_key", settings.IngestKeyMask)

	<-ctx.Done()
	logger.Info("apagando")
	return nil
}
```

- [ ] **Step 4: Ejecutar todos los tests y verificar que pasan**

```bash
go test ./... -count=1
go vet ./...
go build -o splitstream ./cmd/splitstream
```
Expected: PASS en todo, y el binario compila.

- [ ] **Step 5: Verificación manual de punta a punta**

```bash
export SPLITSTREAM_MASTER_KEY=$(./splitstream -genkey)
export SPLITSTREAM_DB_PATH=/tmp/splitstream-smoke.db
rm -f /tmp/splitstream-smoke.db*

# Arranca, imprime la configuración con la clave enmascarada, y sale con Ctrl-C.
./splitstream
```
Expected: una línea con `ingest_key=••••XXXX`, sin la clave completa y sin la master
key. `Ctrl-C` imprime `apagando` y termina con código 0.

Ahora comprueba que una master key equivocada aborta el arranque:

```bash
SPLITSTREAM_MASTER_KEY=$(./splitstream -genkey) ./splitstream; echo "exit=$?"
```
Expected: `error: la master key no corresponde a esta base de datos: ...` y `exit=1`.

Y que la clave de ingesta no está en claro en el archivo:

```bash
strings /tmp/splitstream-smoke.db | grep -c '••••' ; rm -f /tmp/splitstream-smoke.db*
```
Expected: `0`. (Si `strings` no está disponible, usa `grep -a`.)

- [ ] **Step 6: Actualizar el README**

Sustituye la sección `## Alcance` del README por:

```markdown
## Estado

Fase 1 completa: configuración, cifrado, base de datos y esqueleto del binario.
Todavía no hay servidor RTMP ni panel web.

## Desarrollo

```bash
make test    # tests con -race
make build   # binario en ./splitstream
make vet
```

## Configuración

| Variable | Default | Descripción |
| --- | --- | --- |
| `SPLITSTREAM_MASTER_KEY` | — | **Obligatoria.** 32 bytes en base64. Genérala con `splitstream -genkey`. |
| `SPLITSTREAM_HTTP_ADDR` | `:8080` | Dirección del panel y la API |
| `SPLITSTREAM_RTMP_ADDR` | `:1935` | Dirección del servidor RTMP de ingesta |
| `SPLITSTREAM_DB_PATH` | `splitstream.db` | Ruta del archivo SQLite |
| `SPLITSTREAM_LOG_LEVEL` | `info` | `debug`, `info`, `warn` o `error` |

> **Respalda `SPLITSTREAM_MASTER_KEY` aparte de la base de datos.** Cifra las claves
> de tus destinos: si la pierdes, son irrecuperables y hay que volver a pegarlas todas.

## Alcance

Solo retransmisión. Sin transcodificación, sin grabación, sin chat unificado y
sin multi-tenant.
```

- [ ] **Step 7: Commit**

```bash
git add cmd/splitstream/ README.md
git commit -m "feat(cmd): arranque con validación de master key y apagado limpio"
```

---

## Definición de terminado, fase 1

- [ ] `go test ./... -race -count=1` pasa entero.
- [ ] `go vet ./...` limpio.
- [ ] `CGO_ENABLED=0 go build ./cmd/splitstream` produce el binario.
- [ ] `go mod tidy` no deja diferencias, y `go.mod` solo lista `modernc.org/sqlite` y
      `golang.org/x/crypto`.
- [ ] Arrancar con una master key equivocada aborta con `ErrWrongMasterKey` y código 1.
- [ ] Ninguna clave en claro aparece en la salida del arranque ni en el archivo `.db`.
- [ ] El README documenta las cinco variables de entorno y la advertencia sobre la
      master key.

## Notas para la fase 2

- El spike de `go-rtmp` (spec §14) va **antes** de escribir el motor: verificar
  `TLSDial` y si las plataformas exigen `releaseStream`/`FCPublish`.
- `store.Settings.IngestApp` ya existe y por defecto es `live`; la validación del
  publisher de la fase 2 comparará contra ese valor y contra `RevealIngestKey`.
- `LogEvent` ya acepta `SessionID` y `DestinationID` nulos: el motor puede registrar
  eventos de sistema desde el arranque sin tocar el esquema.
