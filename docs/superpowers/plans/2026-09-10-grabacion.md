# v0.9 «Grabación» — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Grabar la sesión de ingesta a disco en FLV segmentado como un sink más del hub, con cuota de disco, retención y descarga desde el panel, sin que un disco lento pueda provocar un solo descarte en los destinos.

**Architecture:** `internal/record` aporta un `relay.Publisher` que escribe tags FLV en archivos (`FLVWriter`) y la cuota de disco (`Quota`, `FreeSpace`). El motor no cambia salvo dos cosas: la constante `relay.RecorderSinkID = -1` y que el `SinkProvider` recibe el id de sesión. `sinks.Factory.BuildRecorder` compone el sink con el store (tablas `recordings` y `recording_settings`, migración 0006) y traduce sus eventos. `httpapi` expone ajustes, listado, descarga y borrado, y añade `Recording` al `statusDTO`. El planificador de la v0.8 gana el job de retención. El panel gana el bloque de ajustes, un chip y la página de grabaciones.

**Tech Stack:** Go 1.25 stdlib + las cinco dependencias de siempre; Vue 3 + Quasar 2 + Pinia. Cero dependencias nuevas (`syscall.Statfs` en unix, `GetDiskFreeSpaceExW` vía `syscall.NewLazyDLL` en Windows).

**Spec:** `docs/superpowers/specs/2026-09-10-grabacion-design.md` (sobre el spec base y el plan maestro §4).

## Global Constraints

- **Regla innegociable (spec §1):** si el disco se atrasa se degrada la grabación, nunca el directo. Ningún camino de `record` bloquea fuera de la goroutine de su propio sink; nunca `Sync` en el camino caliente; el sink de grabación usa la misma cola y política de descarte que cualquier destino.
- **Fronteras:** `internal/relay` no importa go-rtmp, `database/sql`, `internal/store`, `internal/events` ni `internal/record`. `internal/record` importa `internal/relay` e `internal/flv` y stdlib, nada más. `internal/httpapi` no importa go-rtmp ni `internal/rtmpio` (puede importar `internal/record` por `FreeSpace`). La CI vigila las de siempre; la de `record` se añade en la Task 1.
- **Cero dependencias nuevas**; `go mod tidy` nunca se ejecuta en este repo.
- **Los eventos del recorder no llevan `destination_id`** (un `-1` violaría la clave ajena de `events`) y usan `kind` `recording_*`.
- **Migración 0006 sin `ALTER TABLE`** (los tests rebobinan `user_version`): `CREATE TABLE IF NOT EXISTS`, `INSERT OR IGNORE`, `SchemaVersion = 6`.
- **`statusDTO` idéntico en REST y WebSocket**; `TestWebSocketPayloadMatchesTheRESTSnapshot` lo vigila.
- **Errores de la API** `{"error":{"code","message"}}` con el conjunto cerrado de códigos; errores del store clasificados con `notFound`/`invalidInput`/`conflict`.
- **Tests con `-race`**, estables con `GOMAXPROCS=2`; los que necesiten `ffprobe` hacen `t.Skip` si no está en el PATH (la CI lo instala en el job de integración; el job rápido no lo tiene).
- **Comentarios, commits, copys y errores en español**; los comentarios explican el porqué.
- Rama `feat/grabacion` desde `main`. Commits con los trailers `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` y `Claude-Session: https://claude.ai/code/session_01NAkCcZLAMfx6yk4RCkTDhA`.

---

### Task 1: El motor: `RecorderSinkID` y `SinkProvider` con id de sesión

**Files:**
- Modify: `internal/relay/sink.go` (constante), `internal/relay/engine.go` (firma de `SinkProvider`, `NewEngine`, `OnPublishStart`)
- Modify: `internal/relay/engine_test.go` (los `SetSinkProvider` existentes)
- Modify: `cmd/splitstream/main.go` (el closure del provider)
- Modify: `.github/workflows/ci.yml` (frontera nueva)

**Interfaces:**
- Produces: `relay.RecorderSinkID int64 = -1`; `type SinkProvider func(sessionID int64) ([]*Sink, error)`.

- [ ] **Step 1: Crear la rama**

```bash
git checkout -b feat/grabacion main
```

- [ ] **Step 2: Test del id de sesión en el provider (rojo)**

En `internal/relay/engine_test.go`, añadir:

```go
// El provider recibe el id de la sesión que arranca: la grabación necesita saber a qué
// sesión pertenece cada archivo, y el motor es el único que lo sabe en ese momento.
func TestEngineSinkProviderReceivesTheSessionID(t *testing.T) {
	st := &fakeStore{}
	hub := NewHub(nil)
	defer hub.Close()
	e := NewEngine(EngineConfig{Hub: hub, Store: st})
	e.SetValidator(func(string, string) error { return nil })

	var recibido int64
	e.SetSinkProvider(func(sessionID int64) ([]*Sink, error) {
		recibido = sessionID
		return nil, nil
	})
	if err := e.OnPublishStart("live", "k"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}
	defer e.OnPublishEnd()

	if recibido == 0 || recibido != e.SessionID() {
		t.Errorf("el provider recibió %d, la sesión es %d", recibido, e.SessionID())
	}
}

func TestRecorderSinkIDIsNegative(t *testing.T) {
	if RecorderSinkID >= 0 {
		t.Fatalf("RecorderSinkID = %d: debe ser negativo para no chocar con AUTOINCREMENT", RecorderSinkID)
	}
}
```

- [ ] **Step 3: Correr en rojo**

```bash
go test ./internal/relay/ -run 'SinkProvider|RecorderSinkID' -count=1
```
Expected: FAIL de compilación (`cannot use func(sessionID int64)…`, `undefined: RecorderSinkID`).

- [ ] **Step 4: Implementar**

En `internal/relay/sink.go`, tras `flapThreshold`:

```go
// RecorderSinkID es el id del sink de grabación en el hub (spec v0.9 §2). Es negativo
// para no chocar nunca con un id de destino (AUTOINCREMENT arranca en 1). No corresponde
// a ninguna fila de destinations: sus eventos van sin destination_id.
const RecorderSinkID int64 = -1
```

En `internal/relay/engine.go`:

```go
// SinkProvider construye los sinks de una sesión. Recibe el id de la sesión que arranca
// porque la grabación necesita saber a qué sesión pertenece cada archivo. Se llama al
// aceptar a un publisher, no al arrancar el proceso: cada sesión abre su propia conexión
// con cada destino (spec §6.5).
type SinkProvider func(sessionID int64) ([]*Sink, error)
```

en `NewEngine`: `newSinks: func(int64) ([]*Sink, error) { return nil, nil },` y en `OnPublishStart`: `sinks, err := provider(id)`.

Actualizar los `SetSinkProvider(func() …)` existentes en `engine_test.go` a `func(int64) …` (buscar con `grep -n "SetSinkProvider" internal/relay/engine_test.go`).

En `cmd/splitstream/main.go`:

```go
	engine.SetSinkProvider(func(sessionID int64) ([]*relay.Sink, error) {
		return factory.BuildEnabled(ctx)
	})
```

(la Task 6 añade aquí el recorder).

En `.github/workflows/ci.yml`, en el paso «internal/relay sigue aislado», ampliar el grep a `'go-rtmp|database/sql|internal/store|internal/events|internal/record'` y el mensaje de error.

- [ ] **Step 5: Correr en verde y commitear**

```bash
go build ./... && go vet ./... && go test ./internal/relay/ ./cmd/splitstream/ -race -count=1
git add internal/relay/sink.go internal/relay/engine.go internal/relay/engine_test.go cmd/splitstream/main.go .github/workflows/ci.yml
git commit -m "feat(relay): el provider de sinks recibe la sesión y existe RecorderSinkID"
```

---

### Task 2: Tags FLV

**Files:**
- Create: `internal/record/flv.go`, `internal/record/flv_test.go`

**Interfaces:**
- Produces: `record.TagAudio = 8`, `record.TagVideo = 9`, `record.TagScript = 18`; `record.WriteHeader(w io.Writer) error`; `record.WriteTag(w io.Writer, typ byte, ts uint32, data []byte) error`; en el test, un parser mínimo `leerFLV(t, []byte) []tagLeido` que las tareas siguientes reutilizan.

- [ ] **Step 1: Test (rojo)**

Crear `internal/record/flv_test.go`:

```go
package record

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// tagLeido es lo que el parser mínimo del test saca de cada tag.
type tagLeido struct {
	Tipo byte
	TS   uint32
	Data []byte
}

// leerFLV parsea un archivo FLV completo: cabecera, PreviousTagSize0 y tags con su
// PreviousTagSize. Es deliberadamente estricto: cualquier byte fuera de sitio falla.
func leerFLV(t *testing.T, b []byte) []tagLeido {
	t.Helper()
	if len(b) < 13 || string(b[:3]) != "FLV" || b[3] != 1 {
		t.Fatalf("cabecera FLV inválida: % x", b[:min(13, len(b))])
	}
	if b[4]&0x05 != 0x05 {
		t.Errorf("flags = %#x, quería audio+vídeo (0x05)", b[4])
	}
	if binary.BigEndian.Uint32(b[5:9]) != 9 {
		t.Errorf("DataOffset = %d, quería 9", binary.BigEndian.Uint32(b[5:9]))
	}
	if binary.BigEndian.Uint32(b[9:13]) != 0 {
		t.Errorf("PreviousTagSize0 = %d, quería 0", binary.BigEndian.Uint32(b[9:13]))
	}
	var out []tagLeido
	p := 13
	for p < len(b) {
		if len(b)-p < 11 {
			t.Fatalf("tag truncado en %d", p)
		}
		n := int(b[p+1])<<16 | int(b[p+2])<<8 | int(b[p+3])
		ts := uint32(b[p+4])<<16 | uint32(b[p+5])<<8 | uint32(b[p+6]) | uint32(b[p+7])<<24
		if b[p+8]|b[p+9]|b[p+10] != 0 {
			t.Errorf("StreamID != 0 en %d", p)
		}
		fin := p + 11 + n
		if len(b) < fin+4 {
			t.Fatalf("datos truncados en %d", p)
		}
		if prev := binary.BigEndian.Uint32(b[fin : fin+4]); prev != uint32(11+n) {
			t.Errorf("PreviousTagSize = %d, quería %d", prev, 11+n)
		}
		out = append(out, tagLeido{Tipo: b[p], TS: ts, Data: b[p+11 : fin]})
		p = fin + 4
	}
	return out
}

func TestWriteHeaderAndTagsRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := WriteTag(&buf, TagScript, 0, []byte{0x02, 0x00, 0x0a, 'o', 'n', 'M', 'e', 't', 'a', 'D', 'a', 't', 'a'}); err != nil {
		t.Fatal(err)
	}
	if err := WriteTag(&buf, TagVideo, 0, []byte{0x17, 0x00, 0, 0, 0, 0x01}); err != nil {
		t.Fatal(err)
	}
	// Timestamp mayor de 24 bits: el byte extendido tiene que llevar los 8 altos.
	if err := WriteTag(&buf, TagAudio, 0x01_23_45_67, []byte{0xaf, 0x01, 0xff}); err != nil {
		t.Fatal(err)
	}

	tags := leerFLV(t, buf.Bytes())
	if len(tags) != 3 {
		t.Fatalf("tags = %d, quería 3", len(tags))
	}
	if tags[0].Tipo != TagScript || tags[1].Tipo != TagVideo || tags[2].Tipo != TagAudio {
		t.Errorf("tipos = %d %d %d", tags[0].Tipo, tags[1].Tipo, tags[2].Tipo)
	}
	if tags[2].TS != 0x01234567 {
		t.Errorf("timestamp extendido = %#x, quería 0x01234567", tags[2].TS)
	}
	if !bytes.Equal(tags[1].Data, []byte{0x17, 0x00, 0, 0, 0, 0x01}) {
		t.Errorf("datos del tag de vídeo alterados: % x", tags[1].Data)
	}
}

// Un tag de más de 16 MiB no cabe en 3 bytes de tamaño: se rechaza en vez de escribir
// un archivo corrupto.
func TestWriteTagRejectsOversizedData(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteTag(&buf, TagVideo, 0, make([]byte, 1<<24)); err == nil {
		t.Fatal("quería error por tag demasiado grande")
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/record/ -count=1
```
Expected: FAIL, paquete inexistente.

- [ ] **Step 3: Implementar**

Crear `internal/record/flv.go`:

```go
// Package record graba la sesión a disco como un sink más: un relay.Publisher que escribe
// tags FLV en archivos segmentados, con cuota de disco. Importa relay (por Publisher y
// Message) y flv (para reconocer keyframes y sequence headers); nada del motor lo importa.
package record

import (
	"encoding/binary"
	"errors"
	"io"
)

// Tipos de tag FLV.
const (
	TagAudio  byte = 8
	TagVideo  byte = 9
	TagScript byte = 18
)

// maxTagSize es lo que caben en los 3 bytes de DataSize. Un tag mayor no existe en la
// práctica (un keyframe 4K a bitrate alto son cientos de KB), pero escribirlo truncaría el
// tamaño y corrompería todo lo que siguiera.
const maxTagSize = 1<<24 - 1

var errTagTooBig = errors.New("tag FLV demasiado grande")

// flvHeader es la cabecera del archivo más el PreviousTagSize0: "FLV", versión 1, flags
// audio+vídeo, DataOffset 9, y los cuatro ceros del primer PreviousTagSize.
var flvHeader = []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}

// WriteHeader escribe la cabecera del archivo.
func WriteHeader(w io.Writer) error {
	_, err := w.Write(flvHeader)
	return err
}

// WriteTag escribe un tag y su PreviousTagSize. El timestamp va en 3 bytes más el byte
// extendido con los 8 altos, que es la disposición rara de FLV.
func WriteTag(w io.Writer, typ byte, ts uint32, data []byte) error {
	n := len(data)
	if n > maxTagSize {
		return errTagTooBig
	}
	var h [11]byte
	h[0] = typ
	h[1], h[2], h[3] = byte(n>>16), byte(n>>8), byte(n)
	h[4], h[5], h[6], h[7] = byte(ts>>16), byte(ts>>8), byte(ts), byte(ts>>24)
	// h[8..10]: StreamID, siempre 0.
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	var prev [4]byte
	binary.BigEndian.PutUint32(prev[:], uint32(11+n))
	_, err := w.Write(prev[:])
	return err
}
```

- [ ] **Step 4: Correr en verde y commitear**

```bash
go test ./internal/record/ -race -count=1
git add internal/record
git commit -m "feat(record): escritura de cabecera y tags FLV"
```

---

### Task 3: Espacio libre y cuota

**Files:**
- Create: `internal/record/disk_unix.go`, `internal/record/disk_windows.go`, `internal/record/quota.go`, `internal/record/quota_test.go`

**Interfaces:**
- Produces: `record.FreeSpace(dir string) (free, total int64, err error)`; `record.ErrDiskFull`; `record.DefaultMinFree = 512 << 20`; `record.DefaultWarnAt = 0.8`;
  ```go
  type Quota struct {
      MaxBytes  int64   // tope de la suma de grabaciones; 0 = sin tope
      UsedBytes int64   // lo que ya ocupan las grabaciones anteriores
      MinFree   int64   // espacio libre mínimo del sistema de archivos; 0 = DefaultMinFree
      WarnAt    float64 // fracción de MaxBytes a partir de la que se avisa; 0 = DefaultWarnAt
  }
  // Check comprueba la cuota con `extra` bytes escritos por el writer en curso. Devuelve
  // warn=true al cruzar WarnAt, y ErrDiskFull (envuelto) si no hay sitio.
  func (q Quota) Check(dir string, extra int64) (warn bool, err error)
  ```

- [ ] **Step 1: Tests (rojo)**

Crear `internal/record/quota_test.go`:

```go
package record

import (
	"errors"
	"testing"
)

func TestFreeSpaceReportsPositiveNumbersForATempDir(t *testing.T) {
	free, total, err := FreeSpace(t.TempDir())
	if err != nil {
		t.Fatalf("FreeSpace: %v", err)
	}
	if free <= 0 || total <= 0 || free > total {
		t.Errorf("free=%d total=%d: no tiene sentido", free, total)
	}
}

func TestFreeSpaceFailsOnAMissingDir(t *testing.T) {
	if _, _, err := FreeSpace(t.TempDir() + "/no-existe"); err == nil {
		t.Error("quería error para un directorio que no existe")
	}
}

func TestQuotaCheckRefusesWhenUsedReachesMax(t *testing.T) {
	q := Quota{MaxBytes: 1000, UsedBytes: 900}
	if _, err := q.Check("", 100); !errors.Is(err, ErrDiskFull) {
		t.Errorf("con 900+100 de 1000 → err = %v, quería ErrDiskFull", err)
	}
	if _, err := q.Check("", 99); err != nil {
		t.Errorf("con 900+99 de 1000 → err = %v, quería nil", err)
	}
}

func TestQuotaCheckWarnsAtEightyPercent(t *testing.T) {
	q := Quota{MaxBytes: 1000, UsedBytes: 700}
	if warn, _ := q.Check("", 50); warn {
		t.Error("avisó al 75 %")
	}
	if warn, _ := q.Check("", 100); !warn {
		t.Error("no avisó al 80 %")
	}
}

func TestQuotaWithoutMaxNeverRefusesByUsage(t *testing.T) {
	q := Quota{UsedBytes: 1 << 40}
	if warn, err := q.Check("", 1<<40); warn || err != nil {
		t.Errorf("sin MaxBytes: warn=%v err=%v", warn, err)
	}
}

// El espacio libre del sistema de archivos se comprueba de verdad: con un mínimo
// absurdo, cualquier disco real está «lleno».
func TestQuotaCheckRefusesWhenTheFilesystemIsAlmostFull(t *testing.T) {
	q := Quota{MinFree: 1 << 62}
	if _, err := q.Check(t.TempDir(), 0); !errors.Is(err, ErrDiskFull) {
		t.Errorf("err = %v, quería ErrDiskFull por espacio libre", err)
	}
}

// Un directorio que no se puede consultar no bloquea la grabación: es preferible grabar
// a ciegas que no grabar por un sistema de archivos exótico.
func TestQuotaCheckIgnoresAnUnreadableDir(t *testing.T) {
	q := Quota{MinFree: 1 << 62}
	if _, err := q.Check(t.TempDir()+"/no-existe", 0); err != nil {
		t.Errorf("err = %v, quería nil", err)
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/record/ -run 'FreeSpace|Quota' -count=1
```
Expected: FAIL de compilación, `undefined: FreeSpace`.

- [ ] **Step 3: Implementar**

Crear `internal/record/disk_unix.go`:

```go
//go:build !windows

package record

import "syscall"

// FreeSpace devuelve el espacio libre para el usuario y el total del sistema de archivos
// donde vive dir. Bavail y no Bfree: lo que root se reserva no cuenta.
func FreeSpace(dir string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	// Bsize es uint32 en darwin e int64 en linux: la conversión explícita vale en ambos.
	bs := int64(st.Bsize)
	return int64(st.Bavail) * bs, int64(st.Blocks) * bs, nil
}
```

Crear `internal/record/disk_windows.go`:

```go
//go:build windows

package record

import (
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// FreeSpace devuelve el espacio libre para el usuario y el total del volumen donde vive
// dir. Va por syscall y no por golang.org/x/sys/windows para no sumar una dependencia
// directa: el spec §5 las quiere deliberadamente pocas.
func FreeSpace(dir string) (free, total int64, err error) {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var libreUsuario, totalBytes, libreTotal uint64
	r, _, e := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&libreUsuario)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&libreTotal)))
	if r == 0 {
		return 0, 0, e
	}
	return int64(libreUsuario), int64(totalBytes), nil
}
```

Crear `internal/record/quota.go`:

```go
package record

import (
	"errors"
	"fmt"
)

// ErrDiskFull indica que no hay sitio para grabar: por la cuota de grabaciones o por el
// espacio libre del sistema de archivos. Es un centinela: el sink lo trata como cualquier
// fallo de conexión, y quien construye el sink decide si arrancar o no.
var ErrDiskFull = errors.New("no hay espacio para grabar")

const (
	// DefaultMinFree es el espacio libre mínimo del sistema de archivos. Llenar el disco
	// de un VPS tumba el relay entero, que es peor que no grabar (roadmap §6).
	DefaultMinFree int64 = 512 << 20
	// DefaultWarnAt es la fracción de la cuota a partir de la que se avisa.
	DefaultWarnAt = 0.8
)

// Quota es el presupuesto de disco de las grabaciones.
type Quota struct {
	MaxBytes  int64
	UsedBytes int64
	MinFree   int64
	WarnAt    float64
}

func (q Quota) minFree() int64 {
	if q.MinFree <= 0 {
		return DefaultMinFree
	}
	return q.MinFree
}

func (q Quota) warnAt() float64 {
	if q.WarnAt <= 0 {
		return DefaultWarnAt
	}
	return q.WarnAt
}

// Check comprueba la cuota con `extra` bytes escritos por el writer en curso. Un
// directorio que no se puede consultar no cuenta como lleno: grabar a ciegas es mejor
// que no grabar por un sistema de archivos exótico.
func (q Quota) Check(dir string, extra int64) (warn bool, err error) {
	used := q.UsedBytes + extra
	if q.MaxBytes > 0 && used >= q.MaxBytes {
		return false, fmt.Errorf("%w: %d de %d bytes usados", ErrDiskFull, used, q.MaxBytes)
	}
	if dir != "" {
		if free, _, ferr := FreeSpace(dir); ferr == nil && free < q.minFree() {
			return false, fmt.Errorf("%w: quedan %d bytes libres y el mínimo es %d", ErrDiskFull, free, q.minFree())
		}
	}
	warn = q.MaxBytes > 0 && float64(used) >= q.warnAt()*float64(q.MaxBytes)
	return warn, nil
}
```

- [ ] **Step 4: Correr en verde y commitear**

```bash
go test ./internal/record/ -race -count=1 && GOOS=windows go build ./internal/record/
git add internal/record/disk_unix.go internal/record/disk_windows.go internal/record/quota.go internal/record/quota_test.go
git commit -m "feat(record): espacio libre y cuota de disco"
```

---

### Task 4: `FLVWriter`, el `Publisher` que escribe a disco

**Files:**
- Create: `internal/record/writer.go`, `internal/record/writer_test.go`

**Interfaces:**
- Consumes: `relay.Publisher`, `flv.InspectVideo`, `flv.InspectAudio`, `WriteHeader`, `WriteTag`, `Quota` (Task 3), `leerFLV` (test de la Task 2).
- Produces:
  ```go
  type Segment struct{ Path string; Index int; StartedAt, EndedAt time.Time; Bytes int64; DurationMS uint32 }
  type Options struct {
      Dir            string        // se crea con 0o700
      SessionID      int64
      SegmentMinutes int           // 0 = sin segmentar
      Quota          Quota
      OnOpen         func(path string, index int, startedAt time.Time) // al abrir cada archivo
      OnSegment      func(Segment)                                     // al cerrar cada archivo
      OnDiskWarning  func(used, max int64)                             // una vez por writer, al cruzar WarnAt
      Now            func() time.Time
      Logger         *slog.Logger
  }
  func NewFLVWriter(o Options) *FLVWriter
  var _ relay.Publisher = (*FLVWriter)(nil)
  ```

Reglas: `Connect` crea el directorio, comprueba la cuota y abre el primer archivo `<Dir>/<AAAAMMDD-HHMMSS>-<nn>.flv` (`O_EXCL`, 0o600). Los tres escritores cachean el preámbulo (meta, AVC seq header, AAC seq header) y escriben tags con `ts - base`; `base` es 0 en el primer segmento (el sink ya rebasa) y el timestamp del keyframe de rotación en los siguientes. Rotación: en un keyframe con `ts - base >= SegmentMinutes*60000` se cierra el archivo (flush, sync, `OnSegment`), se vuelve a comprobar la cuota, se abre el siguiente y se reescribe el preámbulo con `ts=0`. Audio y vídeo con `ts < base` se descartan. `Flush` cada 2 s de media; `Sync` solo al cerrar. `Close` idempotente.

- [ ] **Step 1: Tests (rojo)**

Crear `internal/record/writer_test.go`:

```go
package record

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Payloads FLV mínimos: el primer byte lleva frameType|codecID (0x17 keyframe AVC, 0x27
// inter AVC), el segundo el AVCPacketType (0 seq header, 1 NALU). Audio: 0xAF = AAC,
// segundo byte 0 seq header, 1 raw.
var (
	pMeta     = []byte{0x02, 0x00, 0x0a, 'o', 'n', 'M', 'e', 't', 'a', 'D', 'a', 't', 'a', 0x08, 0, 0, 0, 0, 0, 0, 0x09}
	pVideoSeq = []byte{0x17, 0x00, 0, 0, 0, 0x01, 0x64, 0x00, 0x1f}
	pAudioSeq = []byte{0xAF, 0x00, 0x12, 0x10}
	pKey      = []byte{0x17, 0x01, 0, 0, 0, 0xaa}
	pInter    = []byte{0x27, 0x01, 0, 0, 0, 0xbb}
	pAudio    = []byte{0xAF, 0x01, 0xcc}
)

// segmentos captura las llamadas a OnOpen y OnSegment.
type segmentos struct {
	abiertos []string
	cerrados []Segment
}

func nuevoWriter(t *testing.T, o Options) (*FLVWriter, *segmentos) {
	t.Helper()
	segs := &segmentos{}
	if o.Dir == "" {
		o.Dir = filepath.Join(t.TempDir(), "sesion-1")
	}
	o.OnOpen = func(path string, _ int, _ time.Time) { segs.abiertos = append(segs.abiertos, path) }
	o.OnSegment = func(s Segment) { segs.cerrados = append(segs.cerrados, s) }
	return NewFLVWriter(o), segs
}

// preambulo manda lo que el sink manda al conectar: meta, seq headers y el keyframe de
// arranque, todo con ts=0 (spec base §6.3).
func preambulo(t *testing.T, w *FLVWriter) {
	t.Helper()
	for _, f := range []func() error{
		func() error { return w.WriteMeta(0, pMeta) },
		func() error { return w.WriteVideo(0, pVideoSeq) },
		func() error { return w.WriteAudio(0, pAudioSeq) },
		func() error { return w.WriteVideo(0, pKey) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
}

func leerArchivo(t *testing.T, path string) []tagLeido {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return leerFLV(t, b)
}

func TestWriterProducesAValidFLVWithThePreambleFirst(t *testing.T) {
	w, segs := nuevoWriter(t, Options{})
	if err := w.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	preambulo(t, w)
	if err := w.WriteAudio(10, pAudio); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteVideo(33, pInter); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(segs.abiertos) != 1 || len(segs.cerrados) != 1 {
		t.Fatalf("abiertos=%d cerrados=%d, quería 1 y 1", len(segs.abiertos), len(segs.cerrados))
	}
	seg := segs.cerrados[0]
	if seg.Index != 1 || seg.DurationMS != 33 || seg.Path != segs.abiertos[0] {
		t.Errorf("segmento = %+v", seg)
	}
	info, err := os.Stat(seg.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != seg.Bytes {
		t.Errorf("Bytes = %d, el archivo mide %d", seg.Bytes, info.Size())
	}
	if filepath.Ext(seg.Path) != ".flv" {
		t.Errorf("extensión de %q", seg.Path)
	}

	tags := leerArchivo(t, seg.Path)
	quiero := []byte{TagScript, TagVideo, TagAudio, TagVideo, TagAudio, TagVideo}
	if len(tags) != len(quiero) {
		t.Fatalf("tags = %d, quería %d", len(tags), len(quiero))
	}
	for i, q := range quiero {
		if tags[i].Tipo != q {
			t.Errorf("tag %d es tipo %d, quería %d", i, tags[i].Tipo, q)
		}
	}
	if tags[3].TS != 0 || tags[4].TS != 10 || tags[5].TS != 33 {
		t.Errorf("timestamps = %d %d %d, quería 0 10 33", tags[3].TS, tags[4].TS, tags[5].TS)
	}
	if !bytes.Equal(tags[0].Data, pMeta) {
		t.Error("el onMetaData no se escribió tal cual")
	}
}

// Cada segmento nuevo empieza por el preámbulo con ts=0 y rebasa los timestamps al
// keyframe de rotación: para el reproductor es un archivo completo por sí mismo.
func TestWriterSegmentsOnAKeyframeAfterTheInterval(t *testing.T) {
	w, segs := nuevoWriter(t, Options{SegmentMinutes: 1})
	if err := w.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	preambulo(t, w)
	// 150 s de media: keyframe cada 2 s, un inter entre medias, audio cada 500 ms.
	for ts := uint32(500); ts <= 150_000; ts += 500 {
		var err error
		switch {
		case ts%2000 == 0:
			err = w.WriteVideo(ts, pKey)
		case ts%1000 == 0:
			err = w.WriteVideo(ts, pInter)
		}
		if err == nil {
			err = w.WriteAudio(ts, pAudio)
		}
		if err != nil {
			t.Fatalf("ts=%d: %v", ts, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if len(segs.cerrados) != 3 {
		t.Fatalf("segmentos = %d, quería 3 (0-60 s, 60-120 s, 120-150 s)", len(segs.cerrados))
	}
	for i, seg := range segs.cerrados {
		if seg.Index != i+1 {
			t.Errorf("segmento %d tiene Index %d", i, seg.Index)
		}
		tags := leerArchivo(t, seg.Path)
		if len(tags) < 4 || tags[0].Tipo != TagScript || tags[1].Tipo != TagVideo || tags[2].Tipo != TagAudio || tags[3].Tipo != TagVideo {
			t.Fatalf("segmento %d no empieza por meta + seq headers + keyframe", i+1)
		}
		if tags[3].TS != 0 || !bytes.Equal(tags[3].Data, pKey) {
			t.Errorf("segmento %d: el primer frame es ts=%d, quería el keyframe con 0", i+1, tags[3].TS)
		}
		if tags[1].TS != 0 || tags[2].TS != 0 {
			t.Errorf("segmento %d: seq headers con ts %d/%d, quería 0", i+1, tags[1].TS, tags[2].TS)
		}
	}
	if d := segs.cerrados[0].DurationMS; d < 59_500 || d > 60_000 {
		t.Errorf("duración del primer segmento = %d ms, quería ~60000", d)
	}
	if d := segs.cerrados[2].DurationMS; d < 29_500 || d > 30_000 {
		t.Errorf("duración del último segmento = %d ms, quería ~30000", d)
	}
}

// Tras rotar, el audio anterior a la base del segmento se descarta en vez de emitirse con
// un timestamp negativo (spec base §3.2).
func TestWriterDropsAudioOlderThanTheSegmentBase(t *testing.T) {
	w, segs := nuevoWriter(t, Options{SegmentMinutes: 1})
	if err := w.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	preambulo(t, w)
	if err := w.WriteVideo(60_000, pKey); err != nil { // rota aquí
		t.Fatal(err)
	}
	if err := w.WriteAudio(59_900, pAudio); err != nil { // llega tarde: fuera
		t.Fatal(err)
	}
	if err := w.WriteAudio(60_100, pAudio); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	tags := leerArchivo(t, segs.cerrados[1].Path)
	var audios []uint32
	for _, tg := range tags[3:] {
		if tg.Tipo == TagAudio {
			audios = append(audios, tg.TS)
		}
	}
	if len(audios) != 1 || audios[0] != 100 {
		t.Errorf("audios del segundo segmento = %v, quería [100]", audios)
	}
}

func TestWriterConnectFailsWhenTheQuotaIsExhausted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	w, segs := nuevoWriter(t, Options{Dir: dir, Quota: Quota{MaxBytes: 100, UsedBytes: 100}})
	err := w.Connect(context.Background())
	if !errors.Is(err, ErrDiskFull) {
		t.Fatalf("Connect = %v, quería ErrDiskFull", err)
	}
	if len(segs.abiertos) != 0 {
		t.Error("se abrió un archivo con la cuota agotada")
	}
	if entradas, _ := os.ReadDir(dir); len(entradas) != 0 {
		t.Errorf("quedaron archivos: %v", entradas)
	}
}

// Al rotar se vuelve a comprobar la cuota: el segmento en curso se cierra bien y es el
// siguiente el que no arranca.
func TestWriterRotationFailsCleanlyWhenTheQuotaRunsOut(t *testing.T) {
	w, segs := nuevoWriter(t, Options{SegmentMinutes: 1, Quota: Quota{MaxBytes: 400}})
	if err := w.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	preambulo(t, w)
	// Rellenar por encima de 400 bytes antes de la rotación.
	for ts := uint32(500); ts < 60_000; ts += 500 {
		if err := w.WriteAudio(ts, pAudio); err != nil {
			t.Fatal(err)
		}
	}
	err := w.WriteVideo(60_000, pKey)
	if !errors.Is(err, ErrDiskFull) {
		t.Fatalf("la rotación devolvió %v, quería ErrDiskFull", err)
	}
	if len(segs.cerrados) != 1 || segs.cerrados[0].Index != 1 {
		t.Errorf("el primer segmento no se cerró limpiamente: %+v", segs.cerrados)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close tras el fallo = %v", err)
	}
}

func TestWriterWarnsOnceAtEightyPercent(t *testing.T) {
	avisos := 0
	w, _ := nuevoWriter(t, Options{SegmentMinutes: 1, Quota: Quota{MaxBytes: 1000, UsedBytes: 850}})
	w.o.OnDiskWarning = func(used, max int64) { avisos++ }
	if err := w.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	preambulo(t, w)
	if err := w.WriteVideo(60_000, pKey); err != nil { // rotación: segunda comprobación
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if avisos != 1 {
		t.Errorf("avisos = %d, quería exactamente 1", avisos)
	}
}

func TestWriterCloseIsIdempotentAndWriteAfterCloseFails(t *testing.T) {
	w, _ := nuevoWriter(t, Options{})
	if err := w.Close(); err != nil {
		t.Errorf("Close sin Connect = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("segundo Close = %v", err)
	}
	if err := w.WriteAudio(0, pAudio); err == nil {
		t.Error("escribir tras Close no dio error")
	}
}

// La prueba de verdad: tags de un FLV generado por ffmpeg pasan por el writer y ffprobe
// reconoce el resultado. Sin ffmpeg/ffprobe se salta; la CI los tiene en el job de
// integración.
func TestWriterOutputIsReadableByFFprobe(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("hace falta %s", tool)
		}
	}
	origen := filepath.Join(t.TempDir(), "origen.flv")
	gen := exec.Command("ffmpeg", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", "3", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "30",
		"-c:a", "aac", "-ar", "44100", "-f", "flv", origen)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	b, err := os.ReadFile(origen)
	if err != nil {
		t.Fatal(err)
	}

	w, segs := nuevoWriter(t, Options{})
	if err := w.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tg := range leerFLV(t, b) {
		var err error
		switch tg.Tipo {
		case TagScript:
			err = w.WriteMeta(tg.TS, tg.Data)
		case TagVideo:
			err = w.WriteVideo(tg.TS, tg.Data)
		case TagAudio:
			err = w.WriteAudio(tg.TS, tg.Data)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	probe := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name:format=duration",
		"-of", "default=noprint_wrappers=1", segs.cerrados[0].Path)
	out, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v\n%s", err, out)
	}
	for _, want := range []string{"codec_name=h264", "codec_name=aac", "duration="} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("falta %q en:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/record/ -run 'Writer' -count=1
```
Expected: FAIL de compilación, `undefined: NewFLVWriter`.

- [ ] **Step 3: Implementar**

Crear `internal/record/writer.go`:

```go
package record

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/aprendomx/splitstream/internal/flv"
	"github.com/aprendomx/splitstream/internal/relay"
)

const (
	// bufSize es el buffer de escritura. Un MiB son unos segundos de media a bitrates
	// normales: el disco recibe escrituras grandes y pocas.
	bufSize = 1 << 20
	// flushEveryMS es cada cuánto tiempo de media se vacía el buffer. Acota lo que un
	// kill -9 puede costar dentro del segmento en curso.
	flushEveryMS = 2000
)

// Segment describe un archivo cerrado.
type Segment struct {
	Path       string
	Index      int
	StartedAt  time.Time
	EndedAt    time.Time
	Bytes      int64
	DurationMS uint32
}

// Options son los datos para construir un FLVWriter.
type Options struct {
	Dir            string
	SessionID      int64
	SegmentMinutes int
	Quota          Quota
	OnOpen         func(path string, index int, startedAt time.Time)
	OnSegment      func(Segment)
	OnDiskWarning  func(used, max int64)
	Now            func() time.Time
	Logger         *slog.Logger
}

// FLVWriter es el Publisher de la grabación: escribe lo que el sink le manda en archivos
// FLV segmentados. Como todo Publisher, se usa desde una sola goroutine (la del sink), así
// que no lleva mutex. La regla innegociable del spec v0.9 §1 se cumple por construcción:
// este código solo corre en la goroutine del sink de grabación, y el hub le entrega
// mensajes sin bloquear.
type FLVWriter struct {
	o     Options
	now   func() time.Time
	log   *slog.Logger
	stamp string

	f           *os.File
	buf         *bufio.Writer
	path        string
	index       int
	segStart    time.Time
	segBytes    int64
	total       int64
	base        uint32
	lastTS      uint32
	lastFlushTS uint32

	meta, videoSeq, audioSeq []byte
	warned bool
	closed bool
}

func NewFLVWriter(o Options) *FLVWriter {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	return &FLVWriter{o: o, now: now, log: log.With("sesion_id", o.SessionID)}
}

var _ relay.Publisher = (*FLVWriter)(nil)

// Connect crea el directorio, comprueba la cuota y abre el primer segmento.
func (w *FLVWriter) Connect(ctx context.Context) error {
	if w.closed {
		return errors.New("el escritor está cerrado")
	}
	if err := os.MkdirAll(w.o.Dir, 0o700); err != nil {
		return fmt.Errorf("crear el directorio de grabación: %w", err)
	}
	if err := w.checkQuota(); err != nil {
		return err
	}
	w.stamp = w.now().Format("20060102-150405")
	return w.openSegment()
}

// checkQuota aplica la cuota con lo que este writer lleva escrito, y avisa una sola vez
// al cruzar el umbral.
func (w *FLVWriter) checkQuota() error {
	warn, err := w.o.Quota.Check(w.o.Dir, w.total)
	if err != nil {
		return err
	}
	if warn && !w.warned {
		w.warned = true
		if w.o.OnDiskWarning != nil {
			w.o.OnDiskWarning(w.o.Quota.UsedBytes+w.total, w.o.Quota.MaxBytes)
		}
	}
	return nil
}

func (w *FLVWriter) openSegment() error {
	w.index++
	w.path = filepath.Join(w.o.Dir, fmt.Sprintf("%s-%02d.flv", w.stamp, w.index))
	// O_EXCL: dos sesiones en el mismo segundo no pueden pisarse un archivo.
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("abrir el segmento: %w", err)
	}
	w.f = f
	w.buf = bufio.NewWriterSize(f, bufSize)
	w.segStart = w.now()
	w.segBytes = 0
	if err := WriteHeader(w.buf); err != nil {
		return err
	}
	w.segBytes += int64(len(flvHeader))
	w.total += int64(len(flvHeader))
	if w.o.OnOpen != nil {
		w.o.OnOpen(w.path, w.index, w.segStart)
	}
	return nil
}

// closeSegment vacía, sincroniza y cierra el archivo en curso, y avisa por OnSegment. Es
// el único sitio con Sync: una vez por segmento, nunca por frame.
func (w *FLVWriter) closeSegment() error {
	if w.f == nil {
		return nil
	}
	err := w.buf.Flush()
	if serr := w.f.Sync(); err == nil {
		err = serr
	}
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	seg := Segment{
		Path: w.path, Index: w.index, StartedAt: w.segStart, EndedAt: w.now(),
		Bytes: w.segBytes, DurationMS: w.lastTS - w.base,
	}
	w.f, w.buf = nil, nil
	if w.o.OnSegment != nil {
		w.o.OnSegment(seg)
	}
	return err
}

// rotate cierra el segmento en curso y abre el siguiente con base en el keyframe ts.
func (w *FLVWriter) rotate(ts uint32) error {
	if err := w.closeSegment(); err != nil {
		return err
	}
	if err := w.checkQuota(); err != nil {
		return err
	}
	if err := w.openSegment(); err != nil {
		return err
	}
	w.base = ts
	w.lastTS = ts
	w.lastFlushTS = ts
	return w.writePreamble()
}

// writePreamble reescribe lo que el sink mandó al conectar, con ts=0: cada segmento tiene
// que poder reproducirse solo.
func (w *FLVWriter) writePreamble() error {
	if w.meta != nil {
		if err := w.writeTag(TagScript, 0, w.meta); err != nil {
			return err
		}
	}
	if w.videoSeq != nil {
		if err := w.writeTag(TagVideo, 0, w.videoSeq); err != nil {
			return err
		}
	}
	if w.audioSeq != nil {
		if err := w.writeTag(TagAudio, 0, w.audioSeq); err != nil {
			return err
		}
	}
	return nil
}

// writeTag escribe un tag con timestamp YA relativo al segmento y lleva las cuentas.
func (w *FLVWriter) writeTag(typ byte, rel uint32, data []byte) error {
	if w.closed {
		return errors.New("el escritor está cerrado")
	}
	if w.f == nil {
		return errors.New("el escritor no está conectado")
	}
	if err := WriteTag(w.buf, typ, rel, data); err != nil {
		return fmt.Errorf("escribir en la grabación: %w", err)
	}
	n := int64(11 + len(data) + 4)
	w.segBytes += n
	w.total += n
	return nil
}

// media escribe un tag de audio o vídeo con timestamp del sink: lo rebasa al segmento,
// descarta lo anterior a la base y vacía el buffer cada flushEveryMS de media.
func (w *FLVWriter) media(typ byte, ts uint32, data []byte) error {
	if ts < w.base {
		return nil
	}
	if err := w.writeTag(typ, ts-w.base, data); err != nil {
		return err
	}
	if ts > w.lastTS {
		w.lastTS = ts
	}
	if ts-w.lastFlushTS >= flushEveryMS {
		w.lastFlushTS = ts
		if err := w.buf.Flush(); err != nil {
			return fmt.Errorf("escribir en la grabación: %w", err)
		}
	}
	return nil
}

func clonar(b []byte) []byte { return append([]byte(nil), b...) }

// WriteMeta escribe el onMetaData como script tag y lo cachea para los segmentos
// siguientes. El payload que circula por el hub ya es el cuerpo AMF0 completo.
func (w *FLVWriter) WriteMeta(ts uint32, payload []byte) error {
	w.meta = clonar(payload)
	return w.writeTag(TagScript, 0, payload)
}

// WriteVideo escribe un tag de vídeo. Los sequence headers se cachean; un keyframe que
// cruza el intervalo de segmentación rota el archivo antes de escribirse.
func (w *FLVWriter) WriteVideo(ts uint32, payload []byte) error {
	info, err := flv.InspectVideo(payload)
	if err != nil {
		return err
	}
	if info.IsSequenceHeader {
		w.videoSeq = clonar(payload)
		return w.writeTag(TagVideo, 0, payload)
	}
	if info.IsKeyframe && w.o.SegmentMinutes > 0 && w.f != nil &&
		ts-w.base >= uint32(w.o.SegmentMinutes)*60_000 {
		if err := w.rotate(ts); err != nil {
			return err
		}
	}
	return w.media(TagVideo, ts, payload)
}

// WriteAudio escribe un tag de audio; el sequence header se cachea.
func (w *FLVWriter) WriteAudio(ts uint32, payload []byte) error {
	info, err := flv.InspectAudio(payload)
	if err != nil {
		return err
	}
	if info.IsSequenceHeader {
		w.audioSeq = clonar(payload)
		return w.writeTag(TagAudio, 0, payload)
	}
	return w.media(TagAudio, ts, payload)
}

// Close cierra el segmento en curso. Es idempotente y tolera que Connect no se haya
// llamado o haya fallado.
func (w *FLVWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.closeSegment()
}
```

- [ ] **Step 4: Correr en verde y commitear**

```bash
go test ./internal/record/ -race -count=1
```
Expected: PASS (el test de ffprobe corre porque ffmpeg está instalado en esta máquina; en el job rápido de la CI se salta).

```bash
git add internal/record/writer.go internal/record/writer_test.go
git commit -m "feat(record): escritor FLV segmentado como Publisher"
```

---

### Task 5: Store: migración 0006, grabaciones, ajustes y poda

**Files:**
- Create: `internal/store/migrations/0006_recordings.sql`
- Modify: `internal/store/db.go` (`SchemaVersion = 6`), `internal/store/db_test.go` (lista de tablas)
- Create: `internal/store/recordings.go`, `internal/store/recordings_test.go`
- Create: `internal/store/recording_settings.go`, `internal/store/recording_settings_test.go`
- Modify: `internal/store/retention.go` (`PruneSessions` respeta las grabaciones), `internal/store/retention_test.go`

**Interfaces:**
- Produces:
  ```go
  type Recording struct {
      ID         int64
      SessionID  *int64
      Path       string     // RELATIVO al directorio de grabaciones
      Segment    int
      StartedAt  time.Time
      EndedAt    *time.Time // nil = en curso
      Bytes      int64
      DurationMS int
  }
  var ErrRecordingNotFound = notFound("grabación no encontrada")
  var ErrRecordingInProgress = conflict("la grabación está en curso")
  func (d *DB) OpenRecording(ctx, sessionID int64, path string, segment int, startedAt time.Time) (int64, error)
  func (d *DB) FinishRecording(ctx, path string, endedAt time.Time, bytes int64, durationMS int) error
  func (d *DB) ListRecordings(ctx, sessionID int64, limit int, before int64) ([]Recording, error) // sessionID 0 = todas; más recientes primero; pagina por id
  func (d *DB) RecordingByID(ctx, id int64) (*Recording, error)
  func (d *DB) DeleteRecording(ctx, id int64) error // ErrRecordingInProgress si sigue abierta
  func (d *DB) RecordingsTotalBytes(ctx) (int64, error)
  func (d *DB) CountSessionRecordings(ctx, sessionID int64) (int, error)
  func (d *DB) PruneRecordings(ctx, olderThan time.Time, maxBytes int64, remove func(path string) error) (deleted int, freed int64, err error)

  type RecordingSettings struct { Enabled bool; SegmentMin int; MaxGB float64; KeepDays int; UpdatedAt time.Time }
  type RecordingSettingsPatch struct { Enabled *bool; SegmentMin *int; MaxGB *float64; KeepDays *int }
  func (d *DB) RecordingSettings(ctx) (*RecordingSettings, error)
  func (d *DB) UpdateRecordingSettings(ctx, patch RecordingSettingsPatch) (*RecordingSettings, error)
  ```

- [ ] **Step 1: La migración**

Crear `internal/store/migrations/0006_recordings.sql`:

```sql
-- Grabación (spec v0.9 §5). Sin ALTER TABLE: los tests rebobinan user_version y reaplican
-- las migraciones, y SQLite no tiene ADD COLUMN IF NOT EXISTS. Los ajustes van en una tabla
-- propia de fila única, como settings, con INSERT OR IGNORE para que reaplicarla no falle.
CREATE TABLE IF NOT EXISTS recording_settings (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    enabled     INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    segment_min INTEGER NOT NULL DEFAULT 10 CHECK (segment_min BETWEEN 0 AND 240),
    max_gb      REAL    NOT NULL DEFAULT 20 CHECK (max_gb > 0),
    keep_days   INTEGER NOT NULL DEFAULT 30 CHECK (keep_days >= 0),
    updated_at  TEXT    NOT NULL
);
INSERT OR IGNORE INTO recording_settings (id, updated_at) VALUES (1, '1970-01-01T00:00:00.000000000Z');

-- path es RELATIVO al directorio de grabaciones: mover la carpeta entera (o cambiar la
-- variable de entorno) no rompe el listado. ended_at NULL = segmento en curso.
CREATE TABLE IF NOT EXISTS recordings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  INTEGER REFERENCES sessions (id) ON DELETE SET NULL,
    path        TEXT    NOT NULL UNIQUE,
    segment     INTEGER NOT NULL,
    started_at  TEXT    NOT NULL,
    ended_at    TEXT,
    bytes       INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_recordings_session ON recordings (session_id, segment);
```

En `internal/store/db.go`: `const SchemaVersion = 6`. En `internal/store/db_test.go`, `TestOpenCreatesSchema`: `want := []string{"destination_logos", "destinations", "events", "recording_settings", "recordings", "sessions", "settings", "webhooks"}`.

```bash
go test ./internal/store/ -race -count=1
```
Expected: PASS (incluidos los tests que rebobinan `user_version`).

- [ ] **Step 2: Tests de ajustes (rojo)**

Crear `internal/store/recording_settings_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestRecordingSettingsDefaults(t *testing.T) {
	db := openTemp(t)
	s, err := db.RecordingSettings(context.Background())
	if err != nil {
		t.Fatalf("RecordingSettings: %v", err)
	}
	if s.Enabled || s.SegmentMin != 10 || s.MaxGB != 20 || s.KeepDays != 30 {
		t.Errorf("defaults = %+v; quería apagada, 10 min, 20 GB, 30 días", s)
	}
}

func TestUpdateRecordingSettingsPatchesAndValidates(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	on, seg, gb := true, 5, 2.5
	s, err := db.UpdateRecordingSettings(ctx, store.RecordingSettingsPatch{Enabled: &on, SegmentMin: &seg, MaxGB: &gb})
	if err != nil {
		t.Fatalf("UpdateRecordingSettings: %v", err)
	}
	if !s.Enabled || s.SegmentMin != 5 || s.MaxGB != 2.5 || s.KeepDays != 30 {
		t.Errorf("patch mal aplicado: %+v", s)
	}
	if s.UpdatedAt.IsZero() || s.UpdatedAt.Year() < 2026 {
		t.Errorf("updated_at no se fijó: %v", s.UpdatedAt)
	}

	malos := []store.RecordingSettingsPatch{
		{SegmentMin: ptr(-1)},
		{SegmentMin: ptr(241)},
		{MaxGB: ptrF(0)},
		{KeepDays: ptr(-1)},
	}
	for i, p := range malos {
		if _, err := db.UpdateRecordingSettings(ctx, p); !errors.Is(err, store.ErrInvalidInput) {
			t.Errorf("patch %d aceptado (err = %v)", i, err)
		}
	}
	// Los rechazos no tocaron nada.
	s, _ = db.RecordingSettings(ctx)
	if s.SegmentMin != 5 || s.MaxGB != 2.5 {
		t.Errorf("un patch inválido modificó la fila: %+v", s)
	}
}

func ptr(n int) *int         { return &n }
func ptrF(f float64) *float64 { return &f }
```

- [ ] **Step 3: Tests de grabaciones (rojo)**

Crear `internal/store/recordings_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

func abrirYcerrar(t *testing.T, db *store.DB, sesion int64, path string, seg int, bytes int64, dur int) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := db.OpenRecording(ctx, sesion, path, seg, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("OpenRecording(%s): %v", path, err)
	}
	if err := db.FinishRecording(ctx, path, time.Now(), bytes, dur); err != nil {
		t.Fatalf("FinishRecording(%s): %v", path, err)
	}
	return id
}

func TestRecordingLifecycle(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	sesion, _ := db.StartSession(ctx)

	id, err := db.OpenRecording(ctx, sesion, "sesion-1/a-01.flv", 1, time.Now())
	if err != nil {
		t.Fatalf("OpenRecording: %v", err)
	}
	r, err := db.RecordingByID(ctx, id)
	if err != nil || r.EndedAt != nil || r.Segment != 1 || r.SessionID == nil || *r.SessionID != sesion {
		t.Fatalf("recién abierta = %+v, %v", r, err)
	}
	// En curso: no se borra.
	if err := db.DeleteRecording(ctx, id); !errors.Is(err, store.ErrRecordingInProgress) && !errors.Is(err, store.ErrConflict) {
		t.Errorf("borrar en curso = %v, quería ErrRecordingInProgress", err)
	}

	if err := db.FinishRecording(ctx, "sesion-1/a-01.flv", time.Now(), 1234, 5000); err != nil {
		t.Fatalf("FinishRecording: %v", err)
	}
	r, _ = db.RecordingByID(ctx, id)
	if r.EndedAt == nil || r.Bytes != 1234 || r.DurationMS != 5000 {
		t.Errorf("cerrada = %+v", r)
	}
	if n, _ := db.CountSessionRecordings(ctx, sesion); n != 1 {
		t.Errorf("CountSessionRecordings = %d", n)
	}
	if total, _ := db.RecordingsTotalBytes(ctx); total != 1234 {
		t.Errorf("RecordingsTotalBytes = %d", total)
	}
	if err := db.DeleteRecording(ctx, id); err != nil {
		t.Fatalf("DeleteRecording: %v", err)
	}
	if _, err := db.RecordingByID(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("tras borrar = %v, quería ErrNotFound", err)
	}
	if err := db.FinishRecording(ctx, "no-existe.flv", time.Now(), 1, 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FinishRecording de un path desconocido = %v", err)
	}
}

func TestListRecordingsFiltersBySessionAndPaginatesByID(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	s1, _ := db.StartSession(ctx)
	s2, _ := db.StartSession(ctx)
	for i := 1; i <= 3; i++ {
		abrirYcerrar(t, db, s1, "s1-"+string(rune('0'+i))+".flv", i, 10, 100)
	}
	abrirYcerrar(t, db, s2, "s2-1.flv", 1, 10, 100)

	todas, err := db.ListRecordings(ctx, 0, 10, 0)
	if err != nil || len(todas) != 4 || todas[0].Path != "s2-1.flv" {
		t.Fatalf("todas = %v, %v", todas, err)
	}
	deS1, _ := db.ListRecordings(ctx, s1, 10, 0)
	if len(deS1) != 3 || deS1[0].Segment != 3 {
		t.Errorf("de s1 = %v", deS1)
	}
	pag, _ := db.ListRecordings(ctx, s1, 2, deS1[0].ID)
	if len(pag) != 2 || pag[0].Segment != 2 {
		t.Errorf("página = %v", pag)
	}
}

// Poda: primero por fecha, después por gigas hasta bajar del tope, la más antigua primero.
// El archivo se borra por el callback antes que la fila; un archivo que ya no existe cuenta
// como borrado.
func TestPruneRecordingsByAgeThenBySize(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	sesion, _ := db.StartSession(ctx)
	vieja := time.Now().Add(-40 * 24 * time.Hour)
	for i, r := range []struct {
		path  string
		ended time.Time
		bytes int64
	}{
		{"vieja.flv", vieja, 100},
		{"a.flv", time.Now().Add(-3 * time.Hour), 400},
		{"b.flv", time.Now().Add(-2 * time.Hour), 400},
		{"c.flv", time.Now().Add(-1 * time.Hour), 400},
	} {
		if _, err := db.OpenRecording(ctx, sesion, r.path, i+1, r.ended.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := db.FinishRecording(ctx, r.path, r.ended, r.bytes, 1000); err != nil {
			t.Fatal(err)
		}
	}

	var borrados []string
	remove := func(p string) error {
		borrados = append(borrados, p)
		if p == "a.flv" {
			return os.ErrNotExist // ya no estaba: se da por borrado
		}
		return nil
	}
	// 30 días de retención y tope de 900 bytes: se va "vieja" por fecha (quedan 1200),
	// y luego "a" por tamaño (quedan 800 < 900).
	n, freed, err := db.PruneRecordings(ctx, time.Now().Add(-30*24*time.Hour), 900, remove)
	if err != nil {
		t.Fatalf("PruneRecordings: %v", err)
	}
	if n != 2 || freed != 500 {
		t.Errorf("borradas = %d, liberados = %d; quería 2 y 500", n, freed)
	}
	if len(borrados) != 2 || borrados[0] != "vieja.flv" || borrados[1] != "a.flv" {
		t.Errorf("orden de borrado = %v", borrados)
	}
	restantes, _ := db.ListRecordings(ctx, 0, 10, 0)
	if len(restantes) != 2 {
		t.Errorf("quedan %d, quería 2", len(restantes))
	}
}

// Un fallo real al borrar el archivo deja la fila: mejor una fila huérfana visible que
// un archivo huérfano invisible.
func TestPruneRecordingsKeepsTheRowWhenTheFileCannotBeRemoved(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	abrirYcerrar(t, db, 0, "x.flv", 1, 100, 1)
	_, _, err := db.PruneRecordings(ctx, time.Time{}, 50, func(string) error { return errors.New("permiso denegado") })
	if err == nil {
		t.Error("quería el error del borrado")
	}
	if restantes, _ := db.ListRecordings(ctx, 0, 10, 0); len(restantes) != 1 {
		t.Errorf("la fila desapareció aunque el archivo sigue")
	}
}

func TestPruneRecordingsNeverTouchesOpenSegments(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if _, err := db.OpenRecording(ctx, 0, "abierta.flv", 1, time.Now().Add(-100*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	n, _, err := db.PruneRecordings(ctx, time.Now(), 1, func(string) error { return nil })
	if err != nil || n != 0 {
		t.Errorf("podó un segmento en curso: n=%d err=%v", n, err)
	}
}

// PruneSessions (v0.8) no puede llevarse una sesión que todavía tiene grabaciones: la
// vista de historial las cuelga de la sesión.
func TestPruneSessionsKeepsSessionsWithRecordings(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	vieja := time.Now().Add(-200 * 24 * time.Hour).UTC().Format(anchoFijo)
	if _, err := db.SQL().ExecContext(ctx, `INSERT INTO sessions (id, started_at, ended_at) VALUES (5, ?, ?)`, vieja, vieja); err != nil {
		t.Fatal(err)
	}
	abrirYcerrar(t, db, 5, "s5.flv", 1, 10, 10)
	if n, err := db.PruneSessions(ctx, time.Now()); err != nil || n != 0 {
		t.Errorf("borró la sesión con grabaciones: n=%d err=%v", n, err)
	}
}
```

(`abrirYcerrar` con `sesion == 0` debe guardar `session_id` NULL; `anchoFijo` vive en `retention_test.go`.)

- [ ] **Step 4: Correr en rojo**

```bash
go test ./internal/store/ -run 'Recording' -count=1
```
Expected: FAIL de compilación.

- [ ] **Step 5: Implementar los ajustes**

Crear `internal/store/recording_settings.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"
)

// RecordingSettings es la fila única de recording_settings.
type RecordingSettings struct {
	Enabled    bool
	SegmentMin int
	MaxGB      float64
	KeepDays   int
	UpdatedAt  time.Time
}

// RecordingSettingsPatch es una modificación parcial: los campos nil no se tocan.
type RecordingSettingsPatch struct {
	Enabled    *bool
	SegmentMin *int
	MaxGB      *float64
	KeepDays   *int
}

// Límites. Duplican los CHECK del esquema para que el error llegue antes y legible.
const (
	maxSegmentMinutes = 240
)

func (d *DB) RecordingSettings(ctx context.Context) (*RecordingSettings, error) {
	var (
		s         RecordingSettings
		enabled   int
		updatedAt string
	)
	err := d.ex.QueryRowContext(ctx,
		`SELECT enabled, segment_min, max_gb, keep_days, updated_at FROM recording_settings WHERE id = 1`).
		Scan(&enabled, &s.SegmentMin, &s.MaxGB, &s.KeepDays, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("leer los ajustes de grabación: %w", err)
	}
	s.Enabled = enabled == 1
	if s.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &s, nil
}

// UpdateRecordingSettings aplica el patch tras validarlo entero: un valor inválido no
// modifica nada.
func (d *DB) UpdateRecordingSettings(ctx context.Context, p RecordingSettingsPatch) (*RecordingSettings, error) {
	actual, err := d.RecordingSettings(ctx)
	if err != nil {
		return nil, err
	}
	if p.Enabled != nil {
		actual.Enabled = *p.Enabled
	}
	if p.SegmentMin != nil {
		if *p.SegmentMin < 0 || *p.SegmentMin > maxSegmentMinutes {
			return nil, invalidInput(fmt.Sprintf("los minutos por segmento deben estar entre 0 y %d", maxSegmentMinutes))
		}
		actual.SegmentMin = *p.SegmentMin
	}
	if p.MaxGB != nil {
		if *p.MaxGB <= 0 {
			return nil, invalidInput("el tope en GB debe ser mayor que 0")
		}
		actual.MaxGB = *p.MaxGB
	}
	if p.KeepDays != nil {
		if *p.KeepDays < 0 {
			return nil, invalidInput("los días de retención no pueden ser negativos")
		}
		actual.KeepDays = *p.KeepDays
	}
	if _, err := d.ex.ExecContext(ctx,
		`UPDATE recording_settings SET enabled = ?, segment_min = ?, max_gb = ?, keep_days = ?, updated_at = ? WHERE id = 1`,
		boolToInt(actual.Enabled), actual.SegmentMin, actual.MaxGB, actual.KeepDays, nowRFC3339()); err != nil {
		return nil, fmt.Errorf("guardar los ajustes de grabación: %w", err)
	}
	return d.RecordingSettings(ctx)
}
```

- [ ] **Step 6: Implementar las grabaciones**

Crear `internal/store/recordings.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"
)

var (
	ErrRecordingNotFound   = notFound("grabación no encontrada")
	ErrRecordingInProgress = conflict("la grabación está en curso")
)

// Recording es un segmento grabado. Path es relativo al directorio de grabaciones.
type Recording struct {
	ID         int64
	SessionID  *int64
	Path       string
	Segment    int
	StartedAt  time.Time
	EndedAt    *time.Time
	Bytes      int64
	DurationMS int
}

// OpenRecording registra un segmento recién abierto. sessionID 0 se guarda como NULL.
func (d *DB) OpenRecording(ctx context.Context, sessionID int64, path string, segment int, startedAt time.Time) (int64, error) {
	var sid *int64
	if sessionID != 0 {
		sid = &sessionID
	}
	res, err := d.ex.ExecContext(ctx,
		`INSERT INTO recordings (session_id, path, segment, started_at) VALUES (?, ?, ?, ?)`,
		sid, path, segment, formatTime(startedAt))
	if err != nil {
		return 0, fmt.Errorf("registrar la grabación: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// FinishRecording cierra un segmento por su path.
func (d *DB) FinishRecording(ctx context.Context, path string, endedAt time.Time, bytes int64, durationMS int) error {
	res, err := d.ex.ExecContext(ctx,
		`UPDATE recordings SET ended_at = ?, bytes = ?, duration_ms = ? WHERE path = ?`,
		formatTime(endedAt), bytes, durationMS, path)
	if err != nil {
		return fmt.Errorf("cerrar la grabación: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRecordingNotFound
	}
	return nil
}

const (
	defaultRecordingLimit = 50
	maxRecordingLimit     = 500
)

// ListRecordings devuelve grabaciones de la más reciente a la más antigua. sessionID 0
// no filtra; before pagina por id (0 = desde la última).
func (d *DB) ListRecordings(ctx context.Context, sessionID int64, limit int, before int64) ([]Recording, error) {
	if limit <= 0 {
		limit = defaultRecordingLimit
	}
	if limit > maxRecordingLimit {
		limit = maxRecordingLimit
	}
	rows, err := d.ex.QueryContext(ctx,
		`SELECT id, session_id, path, segment, started_at, ended_at, bytes, duration_ms
		   FROM recordings
		  WHERE (? = 0 OR session_id = ?) AND (? = 0 OR id < ?)
		  ORDER BY id DESC LIMIT ?`, sessionID, sessionID, before, before, limit)
	if err != nil {
		return nil, fmt.Errorf("listar grabaciones: %w", err)
	}
	defer rows.Close()
	out := []Recording{}
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar grabaciones: %w", err)
	}
	return out, nil
}

func (d *DB) RecordingByID(ctx context.Context, id int64) (*Recording, error) {
	r, err := scanRecording(d.ex.QueryRowContext(ctx,
		`SELECT id, session_id, path, segment, started_at, ended_at, bytes, duration_ms FROM recordings WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRecordingNotFound
	}
	return r, err
}

// DeleteRecording borra la fila. NO borra el archivo: eso lo hace quien conoce el
// directorio raíz, antes de llamar aquí.
func (d *DB) DeleteRecording(ctx context.Context, id int64) error {
	r, err := d.RecordingByID(ctx, id)
	if err != nil {
		return err
	}
	if r.EndedAt == nil {
		return ErrRecordingInProgress
	}
	if _, err := d.ex.ExecContext(ctx, `DELETE FROM recordings WHERE id = ?`, id); err != nil {
		return fmt.Errorf("borrar la grabación: %w", err)
	}
	return nil
}

func (d *DB) RecordingsTotalBytes(ctx context.Context) (int64, error) {
	var total int64
	if err := d.ex.QueryRowContext(ctx, `SELECT coalesce(sum(bytes), 0) FROM recordings`).Scan(&total); err != nil {
		return 0, fmt.Errorf("sumar las grabaciones: %w", err)
	}
	return total, nil
}

func (d *DB) CountSessionRecordings(ctx context.Context, sessionID int64) (int, error) {
	var n int
	if err := d.ex.QueryRowContext(ctx, `SELECT count(*) FROM recordings WHERE session_id = ?`, sessionID).Scan(&n); err != nil {
		return 0, fmt.Errorf("contar las grabaciones: %w", err)
	}
	return n, nil
}

// PruneRecordings borra segmentos cerrados: primero los terminados antes de olderThan (si
// no es cero), después los más antiguos mientras la suma supere maxBytes (si es > 0): la
// de gigas manda (roadmap §6). remove borra el archivo por su path relativo; ErrNotExist
// cuenta como borrado. Un fallo real al borrar conserva la fila: mejor una fila huérfana
// visible que un archivo huérfano invisible.
func (d *DB) PruneRecordings(ctx context.Context, olderThan time.Time, maxBytes int64, remove func(path string) error) (deleted int, freed int64, err error) {
	borrar := func(r Recording) error {
		if err := remove(r.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("borrar %s: %w", r.Path, err)
		}
		if _, err := d.ex.ExecContext(ctx, `DELETE FROM recordings WHERE id = ?`, r.ID); err != nil {
			return fmt.Errorf("borrar la fila de %s: %w", r.Path, err)
		}
		deleted++
		freed += r.Bytes
		return nil
	}

	if !olderThan.IsZero() {
		viejas, err := d.recordingsWhere(ctx, `ended_at IS NOT NULL AND ended_at < ? ORDER BY id`, formatTime(olderThan))
		if err != nil {
			return deleted, freed, err
		}
		for _, r := range viejas {
			if err := borrar(r); err != nil {
				return deleted, freed, err
			}
		}
	}
	if maxBytes > 0 {
		total, err := d.RecordingsTotalBytes(ctx)
		if err != nil {
			return deleted, freed, err
		}
		if total > maxBytes {
			cerradas, err := d.recordingsWhere(ctx, `ended_at IS NOT NULL ORDER BY id`)
			if err != nil {
				return deleted, freed, err
			}
			for _, r := range cerradas {
				if total <= maxBytes {
					break
				}
				if err := borrar(r); err != nil {
					return deleted, freed, err
				}
				total -= r.Bytes
			}
		}
	}
	return deleted, freed, nil
}

func (d *DB) recordingsWhere(ctx context.Context, where string, args ...any) ([]Recording, error) {
	rows, err := d.ex.QueryContext(ctx,
		`SELECT id, session_id, path, segment, started_at, ended_at, bytes, duration_ms FROM recordings WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("leer grabaciones: %w", err)
	}
	defer rows.Close()
	var out []Recording
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func scanRecording(s scanner) (*Recording, error) {
	var (
		r         Recording
		startedAt string
		endedAt   *string
	)
	if err := s.Scan(&r.ID, &r.SessionID, &r.Path, &r.Segment, &startedAt, &endedAt, &r.Bytes, &r.DurationMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("leer grabación: %w", err)
	}
	var err error
	if r.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt); err != nil {
		return nil, fmt.Errorf("started_at inválido: %w", err)
	}
	if endedAt != nil {
		t, err := time.Parse(time.RFC3339Nano, *endedAt)
		if err != nil {
			return nil, fmt.Errorf("ended_at inválido: %w", err)
		}
		r.EndedAt = &t
	}
	return &r, nil
}
```

En `internal/store/retention.go`, `PruneSessions`: añadir a la condición

```sql
		    AND id NOT IN (SELECT session_id FROM recordings WHERE session_id IS NOT NULL)
```

y actualizar el comentario: una sesión con grabaciones se queda, porque la vista de historial cuelga los archivos de ella.

- [ ] **Step 7: Correr en verde y commitear**

```bash
go test ./internal/store/ -race -count=1
git add internal/store/migrations/0006_recordings.sql internal/store/db.go internal/store/db_test.go internal/store/recordings.go internal/store/recordings_test.go internal/store/recording_settings.go internal/store/recording_settings_test.go internal/store/retention.go internal/store/retention_test.go
git commit -m "feat(store): grabaciones, ajustes de grabación y poda por días y gigas"
```

---

### Task 6: Componer el sink de grabación: config, fábrica, cableado y retención

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go` (`RecordingsDir`)
- Modify: `internal/sinks/factory.go`, `internal/sinks/factory_test.go` (`SetRecordingsDir`, `BuildRecorder`, `PruneRecordings`)
- Modify: `cmd/splitstream/main.go` (provider con recorder, directorio, job de retención)

**Interfaces:**
- Consumes: `record.NewFLVWriter`, `record.Options`, `record.Quota`, `record.ErrDiskFull` (Tasks 3–4); `store.RecordingSettings`, `OpenRecording`, `FinishRecording`, `RecordingsTotalBytes`, `PruneRecordings` (Task 5); `relay.RecorderSinkID`, `SinkProvider(sessionID)` (Task 1).
- Produces: `config.Config.RecordingsDir` (`SPLITSTREAM_RECORDINGS_DIR`, por defecto `<dir de la base>/recordings`); `sinks.(*Factory).SetRecordingsDir(dir string)`; `sinks.(*Factory).BuildRecorder(ctx, sessionID int64) (*relay.Sink, error)` (nil, nil si está apagada o no hay sitio); `sinks.(*Factory).PruneRecordings(ctx) (deleted int, freed int64, err error)`; eventos `recording_started` (info), `recording_segment` (info), `recording_disk_warning` (warn), `recording_stopped` (warn), `recording_stopped_disk_full` (error, una vez por sesión), `recording_suspended` (error), `recording_skipped_quota` / `recording_skipped_disk` (warn), `recording_pruned` (info, desde el job).

- [ ] **Step 1: Config (rojo → verde)**

En `internal/config/config_test.go`:

```go
func TestRecordingsDirDefaultsNextToTheDatabase(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(), "SPLITSTREAM_DB_PATH": "/var/lib/splitstream/splitstream.db",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RecordingsDir != "/var/lib/splitstream/recordings" {
		t.Errorf("RecordingsDir = %q", cfg.RecordingsDir)
	}
	cfg, err = config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(), "SPLITSTREAM_RECORDINGS_DIR": "/mnt/grabaciones",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RecordingsDir != "/mnt/grabaciones" {
		t.Errorf("override = %q", cfg.RecordingsDir)
	}
}
```

En `internal/config/config.go`, campo en `Config` (tras `RetentionMaxEvents`):

```go
	// RecordingsDir es donde se escriben las grabaciones. Por defecto junto a la base,
	// por la misma razón que el archivo de clave: lo que hay que respaldar o mover va
	// junto.
	RecordingsDir string
```

en `LoadFrom`, tras leer `DBPath`: `cfg.RecordingsDir = get("SPLITSTREAM_RECORDINGS_DIR", filepath.Join(filepath.Dir(cfg.DBPath), "recordings"))` (`filepath` ya está importado), y `slog.String("recordings_dir", c.RecordingsDir)` en `LogValue`.

```bash
go test ./internal/config/ -race -count=1
```

- [ ] **Step 2: Tests de la fábrica (rojo)**

Añadir a `internal/sinks/factory_test.go` (imports adicionales: `"os"`, `"time"`, `"github.com/aprendomx/splitstream/internal/relay"` ya está):

```go
// encenderGrabacion activa la grabación con un tope en GB dado y devuelve el directorio.
func encenderGrabacion(t *testing.T, db *store.DB, f *sinks.Factory, maxGB float64) string {
	t.Helper()
	dir := t.TempDir()
	f.SetRecordingsDir(dir)
	on := true
	if _, err := db.UpdateRecordingSettings(context.Background(), store.RecordingSettingsPatch{Enabled: &on, MaxGB: &maxGB}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildRecorderIsNilWhenRecordingIsOff(t *testing.T) {
	_, _, f := setup(t)
	f.SetRecordingsDir(t.TempDir())
	s, err := f.BuildRecorder(context.Background(), 1)
	if err != nil || s != nil {
		t.Errorf("con la grabación apagada: sink=%v err=%v, quería nil, nil", s, err)
	}
}

// El sink de grabación es un sink normal con el id reservado. Arrancado contra un hub,
// conecta (abre el archivo), y sus eventos van SIN destination_id con kind recording_*.
func TestBuildRecorderProducesAWorkingSinkWithRecordingEvents(t *testing.T) {
	db, _, f := setup(t)
	ctx := context.Background()
	dir := encenderGrabacion(t, db, f, 1)
	sesion, _ := db.StartSession(ctx)

	s, err := f.BuildRecorder(ctx, sesion)
	if err != nil || s == nil {
		t.Fatalf("BuildRecorder: sink=%v err=%v", s, err)
	}
	if s.ID() != relay.RecorderSinkID {
		t.Errorf("id = %d, quería %d", s.ID(), relay.RecorderSinkID)
	}

	hub := relay.NewHub(nil)
	s.Start(ctx, hub.Preamble())
	esperar(t, 5*time.Second, func() bool { return s.State() == relay.StateLive }, "el recorder conectó")
	s.Stop()

	entradas, err := os.ReadDir(filepath.Join(dir, "sesion-"+strconv.FormatInt(sesion, 10)))
	if err != nil || len(entradas) != 1 {
		t.Fatalf("archivos de la sesión = %v, %v; quería 1", entradas, err)
	}
	grabaciones, _ := db.ListRecordings(ctx, sesion, 10, 0)
	if len(grabaciones) != 1 || grabaciones[0].EndedAt == nil || filepath.IsAbs(grabaciones[0].Path) {
		t.Errorf("fila de la grabación = %+v; quería cerrada y con path relativo", grabaciones)
	}

	eventos, _ := db.RecentEvents(ctx, 20)
	var visto bool
	for _, e := range eventos {
		if e.Kind == "recording_started" {
			visto = true
			if e.DestinationID != nil {
				t.Errorf("recording_started lleva destination_id=%d", *e.DestinationID)
			}
		}
		if strings.HasPrefix(e.Kind, "destination_") {
			t.Errorf("un evento del recorder salió como %s", e.Kind)
		}
	}
	if !visto {
		t.Errorf("no hubo recording_started; eventos: %+v", eventos)
	}
}

// Sin sitio, la grabación no arranca y lo dice; la sesión ni se entera.
func TestBuildRecorderSkipsWhenTheQuotaIsExhausted(t *testing.T) {
	db, _, f := setup(t)
	ctx := context.Background()
	encenderGrabacion(t, db, f, 0.000001) // ~1 KB
	// Un segmento EN CURSO ocupa más que la cuota: la poda no lo puede tocar.
	if _, err := db.OpenRecording(ctx, 0, "otra/abierta.flv", 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().ExecContext(ctx, `UPDATE recordings SET bytes = 100000`); err != nil {
		t.Fatal(err)
	}

	s, err := f.BuildRecorder(ctx, 7)
	if err != nil || s != nil {
		t.Fatalf("sink=%v err=%v, quería nil, nil", s, err)
	}
	eventos, _ := db.RecentEvents(ctx, 5)
	if len(eventos) == 0 || eventos[0].Kind != "recording_skipped_quota" || eventos[0].Level != store.LevelWarn {
		t.Errorf("evento = %+v, quería recording_skipped_quota warn", eventos)
	}
}

// Con la cuota superada por grabaciones VIEJAS, primero se poda y luego se arranca.
func TestBuildRecorderPrunesBeforeGivingUp(t *testing.T) {
	db, _, f := setup(t)
	ctx := context.Background()
	dir := encenderGrabacion(t, db, f, 0.000001)
	if err := os.WriteFile(filepath.Join(dir, "vieja.flv"), make([]byte, 100000), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.OpenRecording(ctx, 0, "vieja.flv", 1, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishRecording(ctx, "vieja.flv", time.Now().Add(-time.Hour), 100000, 1); err != nil {
		t.Fatal(err)
	}

	s, err := f.BuildRecorder(ctx, 8)
	if err != nil || s == nil {
		t.Fatalf("sink=%v err=%v, quería un sink tras podar", s, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vieja.flv")); !os.IsNotExist(err) {
		t.Error("la grabación vieja sigue en disco")
	}
}

func TestPruneRecordingsRemovesFilesAndRows(t *testing.T) {
	db, _, f := setup(t)
	ctx := context.Background()
	dir := encenderGrabacion(t, db, f, 20)
	ruta := filepath.Join(dir, "s", "a.flv")
	if err := os.MkdirAll(filepath.Dir(ruta), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ruta, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	vieja := time.Now().Add(-100 * 24 * time.Hour)
	if _, err := db.OpenRecording(ctx, 0, "s/a.flv", 1, vieja); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishRecording(ctx, "s/a.flv", vieja, 1, 1); err != nil {
		t.Fatal(err)
	}

	n, _, err := f.PruneRecordings(ctx)
	if err != nil || n != 1 {
		t.Fatalf("PruneRecordings: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(ruta); !os.IsNotExist(err) {
		t.Error("el archivo sigue")
	}
	if restantes, _ := db.ListRecordings(ctx, 0, 10, 0); len(restantes) != 0 {
		t.Error("la fila sigue")
	}
}

func esperar(t *testing.T, plazo time.Duration, cond func() bool, msg string) {
	t.Helper()
	fin := time.Now().Add(plazo)
	for time.Now().Before(fin) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tiempo agotado: %s", msg)
}
```

Añadir `"strconv"` y `"strings"` a los imports del test (`filepath` ya está).

- [ ] **Step 3: Correr en rojo**

```bash
go test ./internal/sinks/ -run 'Recorder|PruneRecordings' -count=1
```
Expected: FAIL de compilación, `f.SetRecordingsDir undefined`.

- [ ] **Step 4: Implementar en la fábrica**

En `internal/sinks/factory.go`, añadir a los imports `"fmt"`, `"os"`, `"path/filepath"`, `"strings"`, `"time"` y `"github.com/aprendomx/splitstream/internal/record"`; campo `recDir string` en `Factory`; y:

```go
// SetRecordingsDir fija el directorio raíz de las grabaciones. Sin él, BuildRecorder no
// construye nada: grabar sin saber dónde no es una opción.
func (f *Factory) SetRecordingsDir(dir string) { f.recDir = dir }

// logEvent deja un evento del sistema (sin sesión ni destino) con context.Background():
// son escrituras cortas que interesa que lleguen aunque quien llamó ya se haya ido.
func (f *Factory) logEvent(level store.Level, kind, msg string) {
	if _, err := f.db.LogEvent(context.Background(), store.Event{Level: level, Kind: kind, Message: msg}); err != nil {
		f.logger.Error("no se pudo registrar el evento de grabación", "kind", kind, "err", err)
	}
}

// recordingQuota compone la cuota con lo que ya ocupan las grabaciones.
func (f *Factory) recordingQuota(ctx context.Context, st *store.RecordingSettings) (record.Quota, error) {
	used, err := f.db.RecordingsTotalBytes(ctx)
	if err != nil {
		return record.Quota{}, err
	}
	return record.Quota{MaxBytes: int64(st.MaxGB * float64(1<<30)), UsedBytes: used}, nil
}

// relPath vuelve un path absoluto de una grabación relativo al directorio raíz, que es
// como se guarda: mover la carpeta entera no rompe el listado.
func (f *Factory) relPath(abs string) string {
	rel, err := filepath.Rel(f.recDir, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// PruneRecordings aplica la retención de grabaciones: por días y por gigas, la de gigas
// manda. Lo usa el job diario y BuildRecorder cuando no hay sitio.
func (f *Factory) PruneRecordings(ctx context.Context) (deleted int, freed int64, err error) {
	st, err := f.db.RecordingSettings(ctx)
	if err != nil {
		return 0, 0, err
	}
	var corte time.Time
	if st.KeepDays > 0 {
		corte = time.Now().Add(-time.Duration(st.KeepDays) * 24 * time.Hour)
	}
	remove := func(rel string) error {
		return os.Remove(filepath.Join(f.recDir, filepath.FromSlash(rel)))
	}
	return f.db.PruneRecordings(ctx, corte, int64(st.MaxGB*float64(1<<30)), remove)
}

// BuildRecorder construye el sink de grabación de una sesión (spec v0.9 §6): nil, nil si
// la grabación está apagada o no hay sitio. Sin sitio se intenta podar antes de rendirse,
// porque el planificador puede no haber corrido todavía hoy.
func (f *Factory) BuildRecorder(ctx context.Context, sessionID int64) (*relay.Sink, error) {
	if f.recDir == "" {
		return nil, nil
	}
	st, err := f.db.RecordingSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !st.Enabled {
		return nil, nil
	}
	if err := os.MkdirAll(f.recDir, 0o700); err != nil {
		return nil, fmt.Errorf("crear el directorio de grabaciones: %w", err)
	}

	q, err := f.recordingQuota(ctx, st)
	if err != nil {
		return nil, err
	}
	if _, err := q.Check(f.recDir, 0); err != nil {
		if _, _, perr := f.PruneRecordings(ctx); perr != nil {
			f.logger.Warn("no se pudo podar antes de grabar", "err", perr)
		}
		if q, err = f.recordingQuota(ctx, st); err != nil {
			return nil, err
		}
		if _, err = q.Check(f.recDir, 0); err != nil {
			kind := "recording_skipped_disk"
			if q.MaxBytes > 0 && q.UsedBytes >= q.MaxBytes {
				kind = "recording_skipped_quota"
			}
			f.logEvent(store.LevelWarn, kind, "grabación: no arranca, "+err.Error())
			return nil, nil
		}
	}

	dir := filepath.Join(f.recDir, fmt.Sprintf("sesion-%d", sessionID))
	bg := context.Background()
	// Una vez por sesión: el sink reintenta con backoff y cada intento volvería a decirlo.
	var discoLlenoAvisado bool

	newPub := func() (relay.Publisher, error) {
		q, err := f.recordingQuota(bg, st)
		if err != nil {
			return nil, err
		}
		return record.NewFLVWriter(record.Options{
			Dir: dir, SessionID: sessionID, SegmentMinutes: st.SegmentMin, Quota: q, Logger: f.logger,
			OnOpen: func(path string, index int, startedAt time.Time) {
				if _, err := f.db.OpenRecording(bg, sessionID, f.relPath(path), index, startedAt); err != nil {
					f.logger.Error("no se pudo registrar el segmento", "err", err)
				}
			},
			OnSegment: func(s record.Segment) {
				if err := f.db.FinishRecording(bg, f.relPath(s.Path), s.EndedAt, s.Bytes, int(s.DurationMS)); err != nil {
					f.logger.Error("no se pudo cerrar el segmento", "err", err)
				}
				f.logEvent(store.LevelInfo, "recording_segment", fmt.Sprintf(
					"grabación: segmento %d cerrado, %.1f MB y %s", s.Index,
					float64(s.Bytes)/(1<<20), (time.Duration(s.DurationMS) * time.Millisecond).Round(time.Second)))
			},
			OnDiskWarning: func(used, max int64) {
				f.logEvent(store.LevelWarn, "recording_disk_warning", fmt.Sprintf(
					"grabación: las grabaciones ocupan el %d %% del tope; se borrarán las más antiguas al llegar", used*100/max))
			},
		}), nil
	}

	return relay.NewSink(relay.SinkConfig{
		ID: relay.RecorderSinkID, Name: "grabación", NewPub: newPub, Logger: f.logger,
		// Los eventos del sink se traducen: sin destination_id (-1 violaría la clave
		// ajena) y con kind recording_*. Los de sospecha y aleteo no se pasan: para un
		// archivo no significan nada y solo harían ruido.
		OnEvent: func(ev relay.EngineEvent) {
			level := store.Level(ev.Level)
			var kind string
			switch ev.Kind {
			case "destination_connected":
				kind, ev.Message = "recording_started", "empezó a escribir"
			case "destination_disconnected":
				kind = "recording_stopped"
				if strings.Contains(ev.Message, record.ErrDiskFull.Error()) {
					if discoLlenoAvisado {
						return
					}
					discoLlenoAvisado = true
					kind, level = "recording_stopped_disk_full", store.LevelError
				}
			case "destination_suspended":
				kind = "recording_suspended"
			default:
				return
			}
			f.logEvent(level, kind, "grabación: "+ev.Message)
		},
	}), nil
}
```

- [ ] **Step 5: Correr en verde y commitear la fábrica**

```bash
go test ./internal/sinks/ ./internal/config/ -race -count=1
git add internal/config internal/sinks
git commit -m "feat(sinks): el sink de grabación se compone en la fábrica con su cuota y sus eventos"
```

- [ ] **Step 6: Cablear en `main.go`**

En `cmd/splitstream/main.go`, tras `factory := sinks.NewFactory(db, cipher, logger)`:

```go
	factory.SetRecordingsDir(cfg.RecordingsDir)
	// Los destinos y, si está encendida, la grabación: un sink más de la misma sesión.
	// Un fallo construyendo la grabación no puede impedir la sesión: se registra y se
	// sigue sin grabar.
	engine.SetSinkProvider(func(sessionID int64) ([]*relay.Sink, error) {
		sinks, err := factory.BuildEnabled(ctx)
		if err != nil {
			return nil, err
		}
		rec, err := factory.BuildRecorder(ctx, sessionID)
		if err != nil {
			logger.Error("no se pudo construir la grabación", "err", err)
			return sinks, nil
		}
		if rec != nil {
			sinks = append(sinks, rec)
		}
		return sinks, nil
	})
```

(sustituye al `SetSinkProvider` de la Task 1). En la lista `Jobs` del planificador, añadir tras `sesiones`:

```go
			{Name: "grabaciones", Run: func(ctx context.Context) (string, error) {
				n, freed, err := factory.PruneRecordings(ctx)
				return fmt.Sprintf("grabaciones: %d borradas, %.1f MB liberados", n, float64(freed)/(1<<20)), err
			}},
```

```bash
go build ./... && go vet ./... && go test ./cmd/splitstream/ -race -count=1
git add cmd/splitstream/main.go
git commit -m "feat: la grabación entra en cada sesión y en la retención diaria"
```

---

### Task 7: La API: ajustes, listado, descarga, borrado, estado y métricas

**Files:**
- Modify: `internal/httpapi/server.go` (`RecorderBuilder`, `Config.Recorder`, `Config.RecordingsDir`, rutas)
- Modify: `internal/httpapi/dto.go` (`recordingStatusDTO`, `recordingSettingsDTO`, `recordingDTO`, `statusDTO.Recording`)
- Modify: `internal/httpapi/status.go` (`recordingStatus`)
- Create: `internal/httpapi/recording.go`, `internal/httpapi/recording_test.go`
- Modify: `internal/httpapi/metrics.go` (cuatro métricas de grabación)
- Modify: `cmd/splitstream/main.go` (`Recorder: factory`, `RecordingsDir: cfg.RecordingsDir`)

**Interfaces:**
- Consumes: `store.RecordingSettings/UpdateRecordingSettings/ListRecordings/RecordingByID/DeleteRecording/RecordingsTotalBytes/CountSessionRecordings`, `store.ErrRecordingInProgress` (Task 5); `record.FreeSpace` (Task 3); `relay.RecorderSinkID` (Task 1); `sinks.(*Factory).BuildRecorder` (Task 6).
- Produces:
  ```go
  type RecorderBuilder interface {
      BuildRecorder(ctx context.Context, sessionID int64) (*relay.Sink, error)
  }
  ```
  `GET/PATCH /api/recording/settings`, `GET /api/recordings?session_id=&limit=&before=`, `GET /api/recordings/{id}/download`, `DELETE /api/recordings/{id}`; `statusDTO.Recording` = `{enabled, active, state, degraded, bytes, dropped_frames, segments, used_bytes, max_bytes, free_bytes, dir}`; métricas `splitstream_recording_active`, `splitstream_recording_bytes_total`, `splitstream_recording_dropped_frames_total`, `splitstream_recording_free_bytes`.

Reglas: encender la grabación con sesión viva construye el sink y lo añade (`AddSink`); apagarla lo quita (`RemoveSink(RecorderSinkID)`); los demás ajustes se aplican a la siguiente sesión. Descargar o borrar un segmento en curso es 409. El archivo se borra antes que la fila; un archivo que ya no existe no impide borrar la fila.

- [ ] **Step 1: Tests (rojo)**

Crear `internal/httpapi/recording_test.go`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// fakeRecorder construye un sink con el id reservado que no escribe a ninguna parte, y
// apunta las sesiones para las que se le pidió.
type fakeRecorder struct {
	llamadas []int64
	nilSink  bool
}

func (f *fakeRecorder) BuildRecorder(ctx context.Context, sessionID int64) (*relay.Sink, error) {
	f.llamadas = append(f.llamadas, sessionID)
	if f.nilSink {
		return nil, nil
	}
	return relay.NewSink(relay.SinkConfig{ID: relay.RecorderSinkID, Name: "grabación"}), nil
}

func decodeRecSettings(t *testing.T, body []byte) recordingSettingsDTO {
	t.Helper()
	var out recordingSettingsDTO
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decodificar: %v — %s", err, body)
	}
	return out
}

func TestRecordingSettingsGetAndPatch(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()

	rec := do(t, srv, cookies, http.MethodGet, "/api/recording/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d — %s", rec.Code, rec.Body.String())
	}
	got := decodeRecSettings(t, rec.Body.Bytes())
	if got.Enabled || got.SegmentMin != 10 || got.MaxGB != 20 || got.KeepDays != 30 || got.Dir != srv.recDir {
		t.Errorf("defaults = %+v", got)
	}
	if got.FreeBytes <= 0 {
		t.Errorf("free_bytes = %d, quería > 0 para un directorio real", got.FreeBytes)
	}

	rec = do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"segment_min":5,"max_gb":2.5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d — %s", rec.Code, rec.Body.String())
	}
	if got = decodeRecSettings(t, rec.Body.Bytes()); got.SegmentMin != 5 || got.MaxGB != 2.5 {
		t.Errorf("patch mal aplicado: %+v", got)
	}

	rec = do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"segment_min":-1}`)
	if rec.Code != http.StatusBadRequest || errorCodeDe(t, rec) != codeInvalidInput {
		t.Errorf("segment_min=-1: %d %s", rec.Code, rec.Body.String())
	}
}

// Encender con sesión viva arranca la grabación al momento; apagar la para. Los demás
// ajustes esperan a la siguiente sesión.
func TestPatchEnablingRecordingWhileLiveAddsAndRemovesTheSink(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	fr := &fakeRecorder{}
	srv.recorder = fr
	eng.setLive(7)

	if rec := do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("encender: %d — %s", rec.Code, rec.Body.String())
	}
	added, _ := eng.snapshotSinks()
	if len(fr.llamadas) != 1 || fr.llamadas[0] != 7 || len(added) != 1 || added[0] != relay.RecorderSinkID {
		t.Errorf("llamadas=%v added=%v", fr.llamadas, added)
	}

	// Cambiar otro ajuste con la grabación ya encendida NO reconstruye el sink.
	if rec := do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"segment_min":3}`); rec.Code != http.StatusOK {
		t.Fatalf("segment_min: %d", rec.Code)
	}
	if added, _ = eng.snapshotSinks(); len(added) != 1 {
		t.Errorf("un ajuste sin cambio de enabled reconstruyó el sink: %v", added)
	}

	if rec := do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("apagar: %d", rec.Code)
	}
	if _, removed := eng.snapshotSinks(); len(removed) != 1 || removed[0] != relay.RecorderSinkID {
		t.Errorf("removed = %v", removed)
	}
}

func TestPatchRecordingWithoutASessionDoesNotTouchTheEngine(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	fr := &fakeRecorder{}
	srv.recorder = fr
	if rec := do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d", rec.Code)
	}
	if added, _ := eng.snapshotSinks(); len(added) != 0 || len(fr.llamadas) != 0 {
		t.Errorf("sin sesión se tocó el motor: added=%v llamadas=%v", added, fr.llamadas)
	}
}

func TestStatusCarriesTheRecordingBlock(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()
	on := true
	if _, err := db.UpdateRecordingSettings(context.Background(), store.RecordingSettingsPatch{Enabled: &on}); err != nil {
		t.Fatal(err)
	}

	st := decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if !st.Recording.Enabled || st.Recording.Active || st.Recording.Dir != srv.recDir {
		t.Errorf("sin sesión: %+v", st.Recording)
	}

	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{relay.RecorderSinkID: {State: "live", BytesSent: 500, Degraded: true, DroppedFrames: 3}})
	st = decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if !st.Recording.Active || st.Recording.State != "live" || st.Recording.Bytes != 500 || !st.Recording.Degraded || st.Recording.DroppedFrames != 3 {
		t.Errorf("con sesión: %+v", st.Recording)
	}
	if st.Recording.MaxBytes != 20<<30 || st.Recording.FreeBytes <= 0 {
		t.Errorf("cuota: %+v", st.Recording)
	}
}

func crearGrabacion(t *testing.T, srv *Server, db *store.DB, rel string, contenido []byte, cerrar bool) int64 {
	t.Helper()
	ctx := context.Background()
	abs := filepath.Join(srv.recDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, contenido, 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := db.OpenRecording(ctx, 0, rel, 1, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if cerrar {
		if err := db.FinishRecording(ctx, rel, time.Now(), int64(len(contenido)), 1000); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestListDownloadAndDeleteRecordings(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()
	contenido := []byte("FLV\x01\x05\x00\x00\x00\x09\x00\x00\x00\x00")
	id := crearGrabacion(t, srv, db, "sesion-1/20260910-120000-01.flv", contenido, true)

	rec := do(t, srv, cookies, http.MethodGet, "/api/recordings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("listar: %d — %s", rec.Code, rec.Body.String())
	}
	var lista []recordingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &lista); err != nil {
		t.Fatal(err)
	}
	if len(lista) != 1 || lista[0].ID != id || lista[0].InProgress || lista[0].Bytes != int64(len(contenido)) {
		t.Errorf("lista = %+v", lista)
	}

	rec = do(t, srv, cookies, http.MethodGet, "/api/recordings/"+itoa(id)+"/download", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("descargar: %d — %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="20260910-120000-01.flv"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if !strings.HasPrefix(rec.Body.String(), "FLV") || rec.Body.Len() != len(contenido) {
		t.Errorf("cuerpo = %q", rec.Body.String())
	}

	if rec = do(t, srv, cookies, http.MethodDelete, "/api/recordings/"+itoa(id), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("borrar: %d — %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(srv.recDir, "sesion-1", "20260910-120000-01.flv")); !os.IsNotExist(err) {
		t.Error("el archivo sigue en disco")
	}
	if rec = do(t, srv, cookies, http.MethodGet, "/api/recordings/"+itoa(id)+"/download", ""); rec.Code != http.StatusNotFound {
		t.Errorf("descargar tras borrar: %d, quería 404", rec.Code)
	}
}

func TestDownloadAndDeleteRefuseAnOpenSegment(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()
	id := crearGrabacion(t, srv, db, "s/abierta.flv", []byte("FLV"), false)

	if rec := do(t, srv, cookies, http.MethodGet, "/api/recordings/"+itoa(id)+"/download", ""); rec.Code != http.StatusConflict {
		t.Errorf("descargar en curso: %d, quería 409", rec.Code)
	}
	if rec := do(t, srv, cookies, http.MethodDelete, "/api/recordings/"+itoa(id), ""); rec.Code != http.StatusConflict {
		t.Errorf("borrar en curso: %d, quería 409", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(srv.recDir, "s", "abierta.flv")); err != nil {
		t.Error("el archivo en curso desapareció")
	}
}

// Una fila cuyo archivo ya no está (borrado a mano) se puede borrar igual: mejor limpiar
// que quedarse con una fila que no se puede quitar.
func TestDeleteRecordingWhoseFileIsGone(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()
	id := crearGrabacion(t, srv, db, "s/b.flv", []byte("FLV"), true)
	if err := os.Remove(filepath.Join(srv.recDir, "s", "b.flv")); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, srv, cookies, http.MethodDelete, "/api/recordings/"+itoa(id), ""); rec.Code != http.StatusNoContent {
		t.Errorf("borrar sin archivo: %d — %s", rec.Code, rec.Body.String())
	}
}

func TestMetricsIncludeRecording(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()
	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{relay.RecorderSinkID: {State: "live", BytesSent: 42, DroppedFrames: 2}})
	m := parseMetrics(t, getMetrics(t, srv, cookies, "").Body.String())
	if m["splitstream_recording_active"] != "1" || m["splitstream_recording_bytes_total"] != "42" || m["splitstream_recording_dropped_frames_total"] != "2" {
		t.Errorf("métricas de grabación: %v", claves(m))
	}
	if m["splitstream_recording_free_bytes"] == "" || m["splitstream_recording_free_bytes"] == "0" {
		t.Errorf("free_bytes = %q", m["splitstream_recording_free_bytes"])
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/httpapi/ -run 'Recording|Recordings' -count=1
```
Expected: FAIL de compilación.

- [ ] **Step 3: Implementar**

En `internal/httpapi/server.go`: tras `DestinationTester`:

```go
// RecorderBuilder construye el sink de grabación de una sesión (nil si está apagada o no
// hay sitio). Lo cumple *sinks.Factory. La API lo usa al encender la grabación en caliente.
type RecorderBuilder interface {
	BuildRecorder(ctx context.Context, sessionID int64) (*relay.Sink, error)
}
```

`Config` gana `Recorder RecorderBuilder` y `RecordingsDir string`; `Server` gana `recorder RecorderBuilder` y `recDir string`; `New` los copia. Rutas (tras las de `/api/backup`):

```go
	protegida("GET /api/recording/settings", s.handleGetRecordingSettings)
	protegida("PATCH /api/recording/settings", s.handlePatchRecordingSettings)
	protegida("GET /api/recordings", s.handleListRecordings)
	protegida("GET /api/recordings/{id}/download", s.handleDownloadRecording)
	protegida("DELETE /api/recordings/{id}", s.handleDeleteRecording)
```

En `internal/httpapi/dto.go`, en `statusDTO` tras `RecentEvents`:

```go
	// Recording es el estado de la grabación de la sesión (spec v0.9 §6).
	Recording recordingStatusDTO `json:"recording"`
```

y al final:

```go
type recordingStatusDTO struct {
	Enabled       bool   `json:"enabled"`
	Active        bool   `json:"active"`
	State         string `json:"state"`
	Degraded      bool   `json:"degraded"`
	Bytes         uint64 `json:"bytes"`
	DroppedFrames uint64 `json:"dropped_frames"`
	Segments      int    `json:"segments"`
	UsedBytes     int64  `json:"used_bytes"`
	MaxBytes      int64  `json:"max_bytes"`
	FreeBytes     int64  `json:"free_bytes"`
	Dir           string `json:"dir"`
}

type recordingSettingsDTO struct {
	Enabled    bool    `json:"enabled"`
	SegmentMin int     `json:"segment_min"`
	MaxGB      float64 `json:"max_gb"`
	KeepDays   int     `json:"keep_days"`
	Dir        string  `json:"dir"`
	UsedBytes  int64   `json:"used_bytes"`
	FreeBytes  int64   `json:"free_bytes"`
}

type recordingDTO struct {
	ID         int64      `json:"id"`
	SessionID  *int64     `json:"session_id"`
	Segment    int        `json:"segment"`
	Path       string     `json:"path"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at"`
	Bytes      int64      `json:"bytes"`
	DurationMS int        `json:"duration_ms"`
	InProgress bool       `json:"in_progress"`
}

func newRecordingDTO(r store.Recording) recordingDTO {
	dto := recordingDTO{
		ID: r.ID, SessionID: r.SessionID, Segment: r.Segment, Path: r.Path,
		StartedAt: r.StartedAt.UTC(), Bytes: r.Bytes, DurationMS: r.DurationMS, InProgress: r.EndedAt == nil,
	}
	if r.EndedAt != nil {
		e := r.EndedAt.UTC()
		dto.EndedAt = &e
	}
	return dto
}
```

Crear `internal/httpapi/recording.go`:

```go
package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/aprendomx/splitstream/internal/record"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// freeBytes devuelve el espacio libre del directorio de grabaciones, o 0 si no se puede
// saber (sin directorio configurado, o todavía sin crear).
func (s *Server) freeBytes() int64 {
	if s.recDir == "" {
		return 0
	}
	free, _, err := record.FreeSpace(s.recDir)
	if err != nil {
		return 0
	}
	return free
}

// recordingStatus compone el bloque de grabación del estado. Lo que se sabe del sink sale
// del motor (Snapshot con el id reservado); el resto, del store y del disco.
func (s *Server) recordingStatus(ctx context.Context) (recordingStatusDTO, error) {
	st, err := s.db.RecordingSettings(ctx)
	if err != nil {
		return recordingStatusDTO{}, err
	}
	used, err := s.db.RecordingsTotalBytes(ctx)
	if err != nil {
		return recordingStatusDTO{}, err
	}
	out := recordingStatusDTO{
		Enabled: st.Enabled, MaxBytes: int64(st.MaxGB * float64(1<<30)), UsedBytes: used,
		FreeBytes: s.freeBytes(), Dir: s.recDir,
	}
	if s.engine == nil {
		return out, nil
	}
	if ses := s.engine.Session(); ses.ID != 0 {
		if m, ok := s.engine.Snapshot()[relay.RecorderSinkID]; ok {
			out.Active = m.State == relay.StateLive.String()
			out.State, out.Degraded, out.Bytes, out.DroppedFrames = m.State, m.Degraded, m.BytesSent, m.DroppedFrames
		}
		if n, err := s.db.CountSessionRecordings(ctx, ses.ID); err == nil {
			out.Segments = n
		}
	}
	return out, nil
}

func (s *Server) recordingSettingsDTO(ctx context.Context, st *store.RecordingSettings) (recordingSettingsDTO, error) {
	used, err := s.db.RecordingsTotalBytes(ctx)
	if err != nil {
		return recordingSettingsDTO{}, err
	}
	return recordingSettingsDTO{
		Enabled: st.Enabled, SegmentMin: st.SegmentMin, MaxGB: st.MaxGB, KeepDays: st.KeepDays,
		Dir: s.recDir, UsedBytes: used, FreeBytes: s.freeBytes(),
	}, nil
}

func (s *Server) handleGetRecordingSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.db.RecordingSettings(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	dto, err := s.recordingSettingsDTO(r.Context(), st)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

type recordingSettingsPatch struct {
	Enabled    *bool    `json:"enabled"`
	SegmentMin *int     `json:"segment_min"`
	MaxGB      *float64 `json:"max_gb"`
	KeepDays   *int     `json:"keep_days"`
}

// handlePatchRecordingSettings guarda los ajustes y, si hay sesión viva y cambió
// `enabled`, arranca o para la grabación al momento. Los demás ajustes esperan a la
// siguiente sesión: cambiar el tamaño de segmento a mitad de archivo no vale la pena.
func (s *Server) handlePatchRecordingSettings(w http.ResponseWriter, r *http.Request) {
	var in recordingSettingsPatch
	if !decodeBody(w, r, &in) {
		return
	}
	ctx := r.Context()
	antes, err := s.db.RecordingSettings(ctx)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	st, err := s.db.UpdateRecordingSettings(ctx, store.RecordingSettingsPatch{
		Enabled: in.Enabled, SegmentMin: in.SegmentMin, MaxGB: in.MaxGB, KeepDays: in.KeepDays,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	if s.liveSession() && antes.Enabled != st.Enabled {
		switch {
		case st.Enabled && s.recorder != nil:
			sink, err := s.recorder.BuildRecorder(ctx, s.engine.Session().ID)
			if err != nil {
				s.logger.Error("no se pudo arrancar la grabación en caliente", "err", err)
			} else if sink != nil {
				s.engine.AddSink(sink)
			}
		case !st.Enabled:
			s.engine.RemoveSink(relay.RecorderSinkID)
		}
	}

	dto, err := s.recordingSettingsDTO(ctx, st)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := queryInt(w, r, "session_id")
	if !ok {
		return
	}
	limit, ok := queryInt(w, r, "limit")
	if !ok {
		return
	}
	before, ok := queryInt(w, r, "before")
	if !ok {
		return
	}
	lista, err := s.db.ListRecordings(r.Context(), int64(sessionID), limit, int64(before))
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	out := make([]recordingDTO, 0, len(lista))
	for _, rec := range lista {
		out = append(out, newRecordingDTO(rec))
	}
	writeJSON(w, http.StatusOK, out)
}

// recordingPath resuelve el archivo de una grabación. El path guardado es relativo y no
// puede salir del directorio raíz: filepath.Join lo limpia, y se comprueba el prefijo por
// si una fila editada a mano trajera "..".
func (s *Server) recordingPath(rel string) (string, bool) {
	abs := filepath.Join(s.recDir, filepath.FromSlash(rel))
	root := filepath.Clean(s.recDir) + string(filepath.Separator)
	return abs, s.recDir != "" && len(abs) > len(root) && abs[:len(root)] == root
}

func (s *Server) handleDownloadRecording(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	rec, err := s.db.RecordingByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if rec.EndedAt == nil {
		s.writeStoreError(w, store.ErrRecordingInProgress)
		return
	}
	abs, ok := s.recordingPath(rec.Path)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "la grabación no está en el directorio de grabaciones")
		return
	}
	f, err := os.Open(abs)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, codeNotFound, "el archivo de la grabación ya no está en disco")
		return
	}
	if err != nil {
		s.logger.Error("no se pudo abrir la grabación", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(abs)+`"`)
	http.ServeContent(w, r, filepath.Base(abs), info.ModTime(), f)
}

// handleDeleteRecording borra el archivo y después la fila. Un archivo que ya no existe
// no impide borrar la fila: mejor limpiar que dejar una fila que nadie puede quitar.
func (s *Server) handleDeleteRecording(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	rec, err := s.db.RecordingByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if rec.EndedAt == nil {
		s.writeStoreError(w, store.ErrRecordingInProgress)
		return
	}
	if abs, ok := s.recordingPath(rec.Path); ok {
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.logger.Error("no se pudo borrar el archivo de la grabación", "err", err)
			writeError(w, http.StatusInternalServerError, codeInternal, "no se pudo borrar el archivo")
			return
		}
	}
	if err := s.db.DeleteRecording(r.Context(), id); err != nil {
		s.writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

En `internal/httpapi/status.go`, en `status()` antes del `return out, nil`:

```go
	if out.Recording, err = s.recordingStatus(ctx); err != nil {
		return out, err
	}
```

En `internal/httpapi/metrics.go`, en `handleMetrics` tras el bucle de destinos:

```go
	// Grabación: el sink con el id reservado, si está en la sesión, más el disco.
	var recActiva, recBytes, recDrops float64
	if m, ok := snap[relay.RecorderSinkID]; ok {
		if m.State == relay.StateLive.String() {
			recActiva = 1
		}
		recBytes, recDrops = float64(m.BytesSent), float64(m.DroppedFrames)
	}
	add("splitstream_recording_active", "1 si la sesión se está grabando.", "gauge", nil, recActiva)
	add("splitstream_recording_bytes_total", "Bytes escritos por la grabación en esta sesión.", "counter", nil, recBytes)
	add("splitstream_recording_dropped_frames_total", "Mensajes que la grabación descartó por disco lento.", "counter", nil, recDrops)
	add("splitstream_recording_free_bytes", "Espacio libre en el directorio de grabaciones.", "gauge", nil, float64(s.freeBytes()))
```

En `cmd/splitstream/main.go`, en `httpapi.Config`: `Recorder: factory,` y `RecordingsDir: cfg.RecordingsDir,`.

- [ ] **Step 4: Correr en verde y commitear**

```bash
go build ./... && go vet ./... && go test ./internal/httpapi/ -race -count=1
go list -deps ./internal/httpapi | grep -E 'go-rtmp|internal/rtmpio'; echo "exit=$?"
```
Expected: PASS y `exit=1`.

```bash
git add internal/httpapi/server.go internal/httpapi/dto.go internal/httpapi/status.go internal/httpapi/recording.go internal/httpapi/recording_test.go internal/httpapi/metrics.go cmd/splitstream/main.go
git commit -m "feat(api): ajustes, listado, descarga y borrado de grabaciones; estado y métricas"
```

---

### Task 8: Panel: ajustes de grabación, chip «Grabando» y página de grabaciones

**Files:**
- Modify: `web/src/api.js`, `web/src/iconos.js`, `web/src/stores/panel.js`, `web/src/router.js`, `web/src/App.vue`, `web/src/pages/Panel.vue`, `web/src/pages/Ajustes.vue`
- Create: `web/src/pages/Grabaciones.vue`

**Interfaces:**
- Consumes: `statusDTO.recording`, `/api/recording/settings`, `/api/recordings*` (Task 7); `bytesLegibles`, `duracionLegible` de `web/src/diagnostico.js`.
- Produces: `api.ajustesGrabacion()`, `api.editarAjustesGrabacion(patch)`, `api.grabaciones(sessionId, limit, before)`, `api.borrarGrabacion(id)`, `api.urlDescargaGrabacion(id)`; getter `panel.grabacion`; ruta `/grabaciones`.

- [ ] **Step 1: Cliente, iconos y store**

En `web/src/api.js`, dentro de `export const api = {`, al final:

```js
  ajustesGrabacion: () => pedir('GET', '/api/recording/settings'),
  editarAjustesGrabacion: (patch) => pedir('PATCH', '/api/recording/settings', patch),
  grabaciones: (sessionId = 0, limit = 50, before = 0) =>
    pedir('GET', `/api/recordings?session_id=${sessionId}&limit=${limit}&before=${before}`),
  borrarGrabacion: (id) => pedir('DELETE', `/api/recordings/${id}`),
  // La descarga es un enlace normal: el navegador manda la cookie y el backend responde
  // con attachment. No hace falta pasar por un blob como en el respaldo.
  urlDescargaGrabacion: (id) => `/api/recordings/${id}/download`,
```

En `web/src/iconos.js`, añadir a la exportación: `mdiRecordRec as iGrabar,`, `mdiFolderPlay as iGrabaciones,`, `mdiHarddisk as iDisco,`.

En `web/src/stores/panel.js`, en `getters`: `grabacion: (s) => s.estado?.recording ?? null,`.

- [ ] **Step 2: El chip en el panel**

En `web/src/pages/Panel.vue`:

- Importar `iGrabar` de `@/iconos` y `bytesLegibles` ya está importado de `@/diagnostico`.
- Añadir en `<script setup>`:

```js
// Porcentaje del tope de grabaciones que ya está ocupado; es lo que decide cuándo la
// retención empezará a borrar.
const porcentajeGrabacion = computed(() => {
  const g = panel.grabacion
  if (!g || !g.max_bytes) return 0
  return Math.min(100, Math.round((100 * g.used_bytes) / g.max_bytes))
})
```

- En la tarjeta de señal, justo antes del botón «Vista previa»:

```html
        <q-chip
          v-if="panel.grabacion?.active"
          dense square :icon="iGrabar" text-color="white"
          :color="panel.grabacion.degraded ? 'warning' : 'negative'"
        >
          Grabando · {{ bytesLegibles(panel.grabacion.bytes) }} · disco {{ porcentajeGrabacion }} %
          <q-tooltip>
            {{ panel.grabacion.degraded
              ? 'El disco no da abasto: la grabación descarta vídeo, el directo no.'
              : `Segmento ${panel.grabacion.segments} · ${bytesLegibles(panel.grabacion.free_bytes)} libres` }}
          </q-tooltip>
        </q-chip>
```

- [ ] **Step 3: El bloque «Grabación» en Ajustes**

En `web/src/pages/Ajustes.vue`:

- Importar `iGrabaciones`, `iDisco` de `@/iconos` y `bytesLegibles` de `@/diagnostico`; `import { usePanel } from '@/stores/panel'` y `const panel = usePanel()`.
- En `<script setup>`:

```js
// Ajustes de grabación. El interruptor se aplica al momento (con sesión viva arranca o
// para la grabación); los demás campos se guardan con el botón y valen para la siguiente
// emisión.
const grabacion = ref(null)
const formGrab = ref({ segment_min: 10, max_gb: 20, keep_days: 30 })
const guardandoGrab = ref(false)

async function cargarGrabacion() {
  try {
    grabacion.value = await api.ajustesGrabacion()
    formGrab.value = {
      segment_min: grabacion.value.segment_min,
      max_gb: grabacion.value.max_gb,
      keep_days: grabacion.value.keep_days,
    }
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  }
}

async function aplicarGrabacion(patch) {
  guardandoGrab.value = true
  try {
    grabacion.value = await api.editarAjustesGrabacion(patch)
    $q.notify({ type: 'positive', message: 'Ajustes de grabación guardados' })
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    guardandoGrab.value = false
  }
}

function alternarGrabacion(encender) {
  // Apagar a mitad de emisión cierra el archivo en curso: se avisa. Encender no destruye
  // nada.
  if (!encender && panel.haySesion && panel.grabacion?.active) {
    $q.dialog({
      title: 'Parar la grabación',
      message: 'Estás emitiendo. Se cerrará el segmento en curso y no se grabará el resto.',
      cancel: { flat: true, noCaps: true, label: 'Cancelar' },
      ok: { color: 'negative', unelevated: true, noCaps: true, label: 'Parar' },
      persistent: true,
    }).onOk(() => aplicarGrabacion({ enabled: false }))
    return
  }
  aplicarGrabacion({ enabled: encender })
}

function guardarGrabacion() {
  aplicarGrabacion({
    segment_min: Number(formGrab.value.segment_min),
    max_gb: Number(formGrab.value.max_gb),
    keep_days: Number(formGrab.value.keep_days),
  })
}

const usoGrabacion = computed(() => {
  const g = grabacion.value
  if (!g) return ''
  const tope = g.max_gb * 2 ** 30
  return `${bytesLegibles(g.used_bytes)} de ${g.max_gb} GB · ${bytesLegibles(g.free_bytes)} libres en el disco`
})
```

  Añadir `computed` al import de `vue` y, en `onMounted`, llamar también a `cargarGrabacion()` (`onMounted(() => { cargar(); cargarGrabacion() })`).

- En el template, antes del `<div class="text-h6 q-mt-xl q-mb-sm">Respaldo</div>`:

```html
      <div class="text-h6 q-mt-xl q-mb-sm">Grabación</div>
      <q-card flat bordered>
        <q-card-section v-if="grabacion" class="q-gutter-y-md">
          <p class="text-body2 text-grey-5 q-mb-none">
            Guarda una copia de cada emisión en el servidor, en FLV y por segmentos, sin
            transcodificar. Si el disco no da abasto, se degrada la grabación, nunca el directo.
          </p>
          <q-toggle
            :model-value="grabacion.enabled" label="Grabar las emisiones"
            :disable="guardandoGrab" @update:model-value="alternarGrabacion"
          />
          <div class="row q-col-gutter-md">
            <q-input v-model.number="formGrab.segment_min" type="number" min="0" max="240" outlined dense
                     label="Minutos por segmento" hint="0 = un solo archivo" class="col-12 col-sm-4" />
            <q-input v-model.number="formGrab.max_gb" type="number" min="0.1" step="0.5" outlined dense
                     label="Tope en GB" hint="Al llegar, se borran las más antiguas" class="col-12 col-sm-4" />
            <q-input v-model.number="formGrab.keep_days" type="number" min="0" outlined dense
                     label="Días de retención" hint="0 = solo manda el tope en GB" class="col-12 col-sm-4" />
          </div>
          <div class="row items-center q-gutter-sm">
            <q-btn unelevated no-caps color="primary" label="Guardar" :loading="guardandoGrab" @click="guardarGrabacion" />
            <q-btn flat no-caps :icon="iGrabaciones" label="Ver grabaciones" :to="{ name: 'grabaciones' }" />
          </div>
          <div class="text-caption text-grey-5">
            <q-icon :name="iDisco" size="14px" class="q-mr-xs" />{{ usoGrabacion }}
            <br />Carpeta: <span class="mono">{{ grabacion.dir }}</span>
          </div>
        </q-card-section>
      </q-card>
```

  y en `<style scoped>`: `.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }`.

- [ ] **Step 4: La página de grabaciones**

Crear `web/src/pages/Grabaciones.vue`:

```vue
<script setup>
import { ref, onMounted } from 'vue'
import { useQuasar } from 'quasar'
import { iDescargar, iBorrar, iGrabaciones } from '@/iconos'
import { api } from '@/api'
import { bytesLegibles, duracionLegible } from '@/diagnostico'

// Lista de segmentos grabados, del más reciente al más antiguo, con descarga y borrado.
// La descarga es un enlace: el navegador manda la cookie y el backend responde con
// attachment, así que no hay que pasar por un blob.
const $q = useQuasar()
const lista = ref([])
const cargando = ref(false)
const hayMas = ref(false)
const PAGINA = 50

async function cargar(before = 0) {
  cargando.value = true
  try {
    const nuevas = await api.grabaciones(0, PAGINA, before)
    lista.value = before ? [...lista.value, ...nuevas] : nuevas
    hayMas.value = nuevas.length === PAGINA
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    cargando.value = false
  }
}
onMounted(cargar)

function cargarMas() {
  const ultima = lista.value.at(-1)
  if (ultima) cargar(ultima.id)
}

function borrar(g) {
  $q.dialog({
    title: 'Eliminar grabación',
    message: `Se borrará «${nombre(g)}» del disco. No se puede deshacer.`,
    cancel: { flat: true, noCaps: true, label: 'Cancelar' },
    ok: { color: 'negative', unelevated: true, noCaps: true, label: 'Eliminar' },
    persistent: true,
  }).onOk(async () => {
    try {
      await api.borrarGrabacion(g.id)
      lista.value = lista.value.filter((x) => x.id !== g.id)
      $q.notify({ type: 'positive', message: 'Grabación eliminada' })
    } catch (e) {
      $q.notify({ type: 'negative', message: e.message })
    }
  })
}

const nombre = (g) => g.path.split('/').at(-1)
const fecha = (iso) => new Date(iso).toLocaleString('es', { dateStyle: 'medium', timeStyle: 'short' })
</script>

<template>
  <q-page class="q-pa-md q-pb-xl">
    <div class="contenido">
      <div class="row items-center q-mb-sm">
        <div class="text-h6">Grabaciones</div>
        <q-space />
        <q-btn flat no-caps label="Ajustes" :to="{ name: 'ajustes' }" />
      </div>

      <q-card v-if="!lista.length && !cargando" flat bordered class="q-pa-lg text-center">
        <q-icon :name="iGrabaciones" size="36px" class="text-grey-7" />
        <div class="text-body2 text-grey-5 q-mt-sm">Todavía no hay grabaciones. Actívalas en Ajustes.</div>
      </q-card>

      <q-list v-else bordered separator class="rounded-borders">
        <q-item v-for="g in lista" :key="g.id">
          <q-item-section>
            <q-item-label>
              {{ nombre(g) }}
              <q-badge v-if="g.in_progress" color="negative" label="en curso" class="q-ml-xs" />
            </q-item-label>
            <q-item-label caption>
              {{ fecha(g.started_at) }} · sesión {{ g.session_id ?? '—' }} · segmento {{ g.segment }}
              · {{ duracionLegible(g.duration_ms / 1000) }} · {{ bytesLegibles(g.bytes) }}
            </q-item-label>
          </q-item-section>
          <q-item-section side>
            <div class="row items-center no-wrap q-gutter-xs">
              <q-btn flat round dense :icon="iDescargar" size="sm" aria-label="Descargar" :disable="g.in_progress"
                     type="a" :href="api.urlDescargaGrabacion(g.id)" />
              <q-btn flat round dense :icon="iBorrar" size="sm" class="text-negative" aria-label="Eliminar"
                     :disable="g.in_progress" @click="borrar(g)" />
            </div>
          </q-item-section>
        </q-item>
      </q-list>

      <div v-if="hayMas" class="text-center q-mt-md">
        <q-btn flat no-caps label="Cargar más" :loading="cargando" @click="cargarMas" />
      </div>

      <p class="text-caption text-grey-6 q-mt-lg">
        Los archivos son FLV, que cualquier reproductor abre. Para pasar uno a MP4 sin recodificar:
        <code>ffmpeg -i grabacion.flv -c copy grabacion.mp4</code>
      </p>
    </div>
  </q-page>
</template>

<style scoped>
.contenido { max-width: 760px; margin: 0 auto; }
</style>
```

En `web/src/router.js`, antes del comodín: `{ path: '/grabaciones', name: 'grabaciones', component: () => import('@/pages/Grabaciones.vue') },`.

En `web/src/App.vue`, antes del botón de ajustes: `<q-btn v-if="panel.autenticado" flat round dense :icon="iGrabaciones" aria-label="Grabaciones" :to="{ name: 'grabaciones' }" />` e importar `iGrabaciones`.

- [ ] **Step 5: Compilar y commitear**

```bash
cd web && npm run build && cd .. && go build ./cmd/splitstream
git add web/src
git commit -m "feat(panel): ajustes de grabación, chip «Grabando» y página de grabaciones"
```

---

### Task 9: Integración de punta a punta, documentación, enmiendas al spec base y cierre

**Files:**
- Create: `test/integration/recording_test.go`
- Modify: `.github/workflows/ci.yml` (el job de integración espera cuatro tests)
- Modify: `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (§1, §6.2, §6.5, §7, §9, §12)
- Modify: `README.md`, `docs/manual-de-usuario.md`, `docs/lanzamiento.md`

- [ ] **Step 1: El test de integración**

Crear `test/integration/recording_test.go` (reutiliza `requireTool`, `testCipher`, `freePort`, `adapter` de `relay_test.go`; no necesita `mediamtx` ni Docker):

```go
//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/sinks"
	"github.com/aprendomx/splitstream/internal/store"
)

// TestRecordingEndToEnd: ffmpeg publica 12 s contra la ingesta con la grabación activada
// y sin segmentar; al terminar hay un FLV que ffprobe reconoce con vídeo y audio de ~12 s,
// y la fila de recordings cuadra con él.
func TestRecordingEndToEnd(t *testing.T) {
	requireTool(t, "ffmpeg")
	requireTool(t, "ffprobe")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	var ingest *rtmpio.Ingest
	defer func() {
		if ingest != nil {
			ingest.Close()
		}
		time.Sleep(300 * time.Millisecond)
		db.Close()
	}()

	cipher := testCipher(t)
	if err := db.Bootstrap(ctx, cipher); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	ingestKey, err := db.RevealIngestKey(ctx, cipher)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}
	on, seg := true, 0
	if _, err := db.UpdateRecordingSettings(ctx, store.RecordingSettingsPatch{Enabled: &on, SegmentMin: &seg}); err != nil {
		t.Fatalf("UpdateRecordingSettings: %v", err)
	}

	recDir := t.TempDir()
	factory := sinks.NewFactory(db, cipher, nil)
	factory.SetRecordingsDir(recDir)

	hub := relay.NewHub(nil)
	defer hub.Close()
	engine := relay.NewEngine(relay.EngineConfig{Hub: hub, Store: adapter{db}})
	engine.SetValidator(func(app, key string) error {
		if app == "live" && key == ingestKey.Reveal() {
			return nil
		}
		return rtmpio.ErrBadStreamKey
	})
	engine.SetSinkProvider(func(sessionID int64) ([]*relay.Sink, error) {
		rec, err := factory.BuildRecorder(ctx, sessionID)
		if err != nil || rec == nil {
			t.Errorf("BuildRecorder: sink=%v err=%v", rec, err)
			return nil, err
		}
		return []*relay.Sink{rec}, nil
	})

	addr := freePort(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ingest = rtmpio.NewIngest(rtmpio.IngestConfig{Addr: addr, Handler: engine})
	go ingest.Serve(ln)
	time.Sleep(300 * time.Millisecond)

	pubURL := fmt.Sprintf("rtmp://%s/live/%s", addr, ingestKey.Reveal())
	ff := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-re", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", "12", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-g", "30", "-b:v", "800k", "-c:a", "aac", "-b:a", "128k", "-ar", "44100",
		"-f", "flv", pubURL)
	if out, err := ff.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}

	// La sesión se cierra cuando go-rtmp ve el socket cerrado; puede tardar un poco más
	// que ffmpeg en salir.
	fin := time.Now().Add(10 * time.Second)
	for engine.SessionID() != 0 && time.Now().Before(fin) {
		time.Sleep(100 * time.Millisecond)
	}
	if engine.SessionID() != 0 {
		t.Fatal("la sesión no se cerró tras salir ffmpeg")
	}

	grabaciones, err := db.ListRecordings(ctx, 0, 10, 0)
	if err != nil {
		t.Fatalf("ListRecordings: %v", err)
	}
	if len(grabaciones) != 1 {
		t.Fatalf("grabaciones = %d, quería 1 (sin segmentar)", len(grabaciones))
	}
	g := grabaciones[0]
	if g.EndedAt == nil || g.Bytes < 100_000 || g.DurationMS < 10_000 || g.DurationMS > 13_500 {
		t.Errorf("fila incoherente: %+v", g)
	}

	archivo := filepath.Join(recDir, filepath.FromSlash(g.Path))
	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name,codec_type,width,height:format=duration",
		"-of", "default=noprint_wrappers=1", archivo)
	b, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v\n%s", err, b)
	}
	got := string(b)
	t.Logf("ffprobe de la grabación:\n%s", got)
	for _, want := range []string{"codec_name=h264", "codec_name=aac", "width=640", "height=360"} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en la grabación:\n%s", want, got)
		}
	}
	for _, linea := range strings.Split(got, "\n") {
		if strings.HasPrefix(linea, "duration=") {
			d, _ := strconv.ParseFloat(strings.TrimPrefix(linea, "duration="), 64)
			if d < 10 || d > 13.5 {
				t.Errorf("duración según ffprobe = %.1f s, quería ~12", d)
			}
		}
	}
}
```

En `.github/workflows/ci.yml`, en el paso «ningún test se saltó», cambiar `if [ "$passed" -lt 3 ]` por `-lt 4` y el mensaje a «se esperaban 4 tests de integración».

Correr en local (ffmpeg y ffprobe están instalados; los otros tres tests se saltan sin `mediamtx`, y eso está bien aquí):

```bash
go test -tags integration ./test/integration/ -run TestRecordingEndToEnd -v -count=1 -timeout 5m
```
Expected: PASS con el `ffprobe` de la grabación en el log.

```bash
git add test/integration/recording_test.go .github/workflows/ci.yml
git commit -m "test(integration): grabación de punta a punta con ffmpeg"
```

- [ ] **Step 2: Enmiendas al spec base**

En `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`:

- §1, tras «Fuera de alcance de forma explícita y permanente: …»: añadir el párrafo

```markdown
> **Enmienda 2026-09-10 (v0.9):** grabar a disco deja de estar fuera de alcance. Grabar
> sin transcodificar es muxear: entra como un `Publisher` más detrás de la misma cola y
> la misma política de descarte que cualquier destino, con la regla de que si el disco
> se atrasa se degrada la grabación, nunca el directo. Siguen fuera: transcodificar,
> ABR, chat de escritura, multi-tenant.
```

- §6.2, al final: «*(v0.9)* El hub admite un sink con `ID = relay.RecorderSinkID (-1)` que no corresponde a ninguna fila de `destinations`; sus eventos van sin `destination_id` y con `kind` `recording_*`.»
- §6.5, al final: «*(v0.9)* El `SinkProvider` recibe el id de la sesión: `func(sessionID int64) ([]*Sink, error)`.»
- §7: viñetas nuevas para `recordings` y `recording_settings` (migración 0006).
- §9: las cinco rutas de grabación anotadas `(v0.9)`.
- §12: `SPLITSTREAM_RECORDINGS_DIR` (por defecto `recordings/` junto a la base).

- [ ] **Step 3: README, manual y entrada de lanzamiento**

`README.md`: fila `SPLITSTREAM_RECORDINGS_DIR` en la tabla de configuración; sección nueva «Grabar las emisiones» tras «Vigilarlo desde fuera»: cómo se activa (Ajustes → Grabación), dónde quedan los archivos, segmentos, tope y retención, la regla del disco lento, y `ffmpeg -i x.flv -c copy x.mp4`. En «Alcance»: quitar «sin grabación» y decir que graba en FLV sin transcodificar.

`docs/manual-de-usuario.md`: sección nueva «Grabar» (antes de «Avisos»): activar, qué son los segmentos y por qué existen (un corte de luz no cuesta más de un segmento), el tope y la retención («la de gigas manda»), qué significa «Grabando con pérdidas» (el disco no da abasto; los canales no se ven afectados), descargar y borrar desde «Grabaciones», y pasar a MP4. Actualizar la numeración de las secciones siguientes y comprobar `grep -n '§\|sección' docs/manual-de-usuario.md README.md`.

`docs/lanzamiento.md`: en «Qué no hace», sustituir «No graba.» por «Graba en FLV, sin transcodificar, con segmentos y tope de disco.» y una frase en «Lo que aprendimos» sobre la regla del disco lento.

```bash
git add docs/superpowers/specs/2026-09-01-rtmp-relay-design.md README.md docs/manual-de-usuario.md docs/lanzamiento.md
git commit -m "docs: v0.9 en el spec base, el README y el manual"
```

- [ ] **Step 4: La suite completa y la CI**

```bash
make vet && make test && cd web && npm run build && cd .. && make build-go
go list -deps ./internal/relay | grep -E 'go-rtmp|database/sql|internal/store|internal/events|internal/record'; echo "exit=$?"
go list -deps ./internal/httpapi | grep -E 'go-rtmp|internal/rtmpio'; echo "exit=$?"
```
Expected: todo en verde, `exit=1` los dos.

- [ ] **Step 5: Puerta de salida (usuario), fusión y etiqueta**

Con OBS delante: una emisión de 1 h con segmentos de 10 min; `kill -9` a mitad; todos los segmentos previos reproducibles con `ffplay`; y **cero descartes en los destinos** mientras se graba en un disco artificialmente lento (por ejemplo `dd if=/dev/zero of=<dir>/lastre bs=1M` en paralelo, o el directorio de grabaciones sobre un USB lento). Anotar en el ledger. Fusionar y etiquetar `v0.9.0`.

---

## Autorrevisión

**Cobertura del spec de la entrega:** §1 (FLV segmentado, cuota, retención, tablas, API, panel) → Tasks 2–8; regla innegociable → el sink de grabación es un `relay.Sink` sin cambios (Task 6) y `record` nunca hace `Sync` en caliente (Task 4); §2 enmiendas → Tasks 1 (`RecorderSinkID`, provider) y 9 (spec base); §3 (archivos, tags, buffer, segmentación, rebase, errores como conexión perdida, `Close`) → Task 4; §4 (cuota antes de arrancar, en rotación, aviso 80 %, `ErrDiskFull`, retención días→gigas) → Tasks 3, 5, 6; §5 (0006 sin `ALTER`, `path` relativo, `PruneSessions`) → Task 5; §6 (`BuildRecorder`, eventos sin `destination_id`, provider con sesión, `statusDTO.Recording`, endpoints, panel, métricas) → Tasks 6, 7, 8; §7 (pruebas) → cada tarea; integración y puerta → Task 9; §8 fuera → nada lo implementa.

**Marcadores:** sin «TBD». Lo único que el plan deja a la ejecución es el texto exacto del manual (Task 9 Step 3), como en la v0.8.

**Consistencia de nombres:** `record.Options{OnOpen, OnSegment, OnDiskWarning}` (Task 4) es lo que rellena `BuildRecorder` (Task 6); `record.Quota.Check(dir, extra)` (Task 3) es lo que llaman el writer (Task 4) y la fábrica (Task 6); `store.OpenRecording/FinishRecording(path…)` (Task 5) reciben el path relativo que produce `Factory.relPath` (Task 6) y que `handleDownloadRecording` resuelve con `recordingPath` (Task 7); `relay.RecorderSinkID` (Task 1) es lo que compara `recordingStatus` (Task 7), lo que devuelve `fakeRecorder` (Task 7) y lo que quita `RemoveSink` al apagar; `store.ErrRecordingInProgress` es un `conflict` (409) y lo usan `DeleteRecording` y los dos handlers; `statusDTO.recording` (Task 7) es lo que lee `panel.grabacion` (Task 8); `sinks.(*Factory).PruneRecordings` (Task 6) es lo que llama el job `grabaciones` de `main.go` y `BuildRecorder` antes de rendirse.
