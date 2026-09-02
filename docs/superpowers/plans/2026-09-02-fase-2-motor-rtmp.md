# Splitstream — Plan de implementación, Fase 2

> **Para agentes:** SUB-SKILL REQUERIDA: usa `superpowers:subagent-driven-development`
> (recomendado) o `superpowers:executing-plans` para ejecutar este plan tarea por tarea.
> Los pasos usan sintaxis de checkbox (`- [ ]`) para seguimiento.

**Goal:** Que un stream de OBS entre por `:1935`, pase por el hub, y salga hacia un destino
RTMP o RTMPS real, con el vídeo reproducible en el otro extremo.

**Architecture:** El servidor `go-rtmp` acepta al publisher y valida su clave. Cada mensaje
de media se inspecciona (¿keyframe? ¿sequence header? ¿enhanced-RTMP?) y se convierte en un
`relay.Message` inmutable que el hub reparte a los sinks. Cada sink tiene su goroutine, su
propia base de timestamps anclada al keyframe de arranque, y un `Publisher` que habla RTMP
o RTMPS. `internal/relay` no importa `go-rtmp` ni `database/sql`: se testea entero con un
publisher en memoria.

**Tech Stack:** Go 1.25, `github.com/yutopp/go-rtmp` v0.0.7, `modernc.org/sqlite`,
`log/slog`. Para los tests de integración: Docker con `mediamtx`, y `ffmpeg`.

**Spec:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
Lee especialmente la **§16** (resultado del spike: tres hallazgos que condicionan el motor)
y la **§15** (deuda heredada de la fase 1; las tareas 1 y 2 la pagan).

## Global Constraints

- Módulo Go: `github.com/aprendomx/splitstream`. Piso `go 1.25.0` en `go.mod`.
- `CGO_ENABLED=0` en el **build**. Los **tests** corren con cgo, porque `-race` lo exige.
- Dependencias directas permitidas al terminar esta fase, y ninguna más:
  `modernc.org/sqlite`, `golang.org/x/crypto`, `github.com/yutopp/go-rtmp`. Las transitivas
  de go-rtmp (`go-amf0`, `logrus`, `pkg/errors`, `mapstructure`) se aceptan (spec §5).
- **NUNCA ejecutes `go mod tidy`.** Tarda varios minutos: recorre las dependencias de test
  de `modernc.org/sqlite`, que arrastra `libc → ccgo → gc/v2 → gc/v3`. Ha matado tres
  agentes. Usa `go get <paquete>` para añadir, que sí es rápido.
- `internal/relay` **no importa** `go-rtmp` ni `database/sql`. Es la frontera del spec §4.
- `internal/flv` no importa nada del proyecto.
- Códecs: solo H.264 + AAC. Enhanced-RTMP (HEVC/AV1 por FourCC) se rechaza en la ingesta con
  un error legible (spec §3.6).
- Los timestamps de salida usan **una sola base compartida por audio y vídeo**, anclada al
  keyframe de arranque. El audio anterior a la base se descarta (spec §3.2).
- El `onMetaData` se reenvía envuelto en `@setDataFrame` (spec §3.5).
- `TLSDial` exige el literal `"rtmps"` como argumento `protocol`; `Dial` exige `"rtmp"`
  (spec §16.1). Derívalo del esquema de la URL del destino.
- Ningún secreto en logs ni en errores. Las stream keys viajan como `crypto.Secret`.
- Comentarios y mensajes de error en español.
- Todo acceso a la base usa métodos `...Context`.

## Fuera de alcance de esta fase

Van a la fase 3, no las implementes aunque te tienten: múltiples destinos simultáneos con
política de descarte por GOP, cola acotada por bytes y duración, reconexión con backoff,
métricas de bitrate, estado `degraded`, y el parseo del SPS para la resolución de la sesión.
Esta fase deja **un** destino funcionando de punta a punta.

## Estructura de archivos de esta fase

| Archivo | Responsabilidad |
| --- | --- |
| `internal/store/db.go` (modificar) | Añadir `execer` e `InTx` (deuda §15.1) |
| `internal/store/events.go` (modificar) | `Session.Width/Height/BitrateBPS` a `*int` (deuda §15.2); añadir `SessionByID` |
| `internal/flv/inspect.go` | Inspección del primer byte de los tags: keyframe, sequence header, enhanced-RTMP |
| `internal/relay/message.go` | `Message`, `Kind` |
| `internal/relay/publisher.go` | La interfaz `Publisher` y su falso de test |
| `internal/relay/preamble.go` | Caché del `onMetaData` y los dos sequence headers |
| `internal/relay/timebase.go` | Rebase de timestamps con base compartida A/V |
| `internal/relay/sink.go` | Goroutine por destino: espera de keyframe y envío |
| `internal/relay/hub.go` | Fan-out y alta/baja de sinks |
| `internal/rtmpio/publisher.go` | `Publisher` sobre el cliente de go-rtmp (rtmp y rtmps) |
| `internal/rtmpio/ingest.go` | Servidor de ingesta en `:1935` con autenticación |
| `cmd/splitstream/main.go` (modificar) | Arrancar la ingesta, conectar el hub, apagar limpio |
| `test/integration/relay_test.go` | ffmpeg → ingesta → mediamtx, verificado con ffprobe |
| `deploy/test-compose.yml` | `mediamtx` para el test de integración |

---

### Task 1: `execer` e `InTx` en el store

Paga la deuda del spec §15.1. Hoy, abrir una transacción y llamar a cualquier método del
repositorio dentro se autobloquea, porque `SetMaxOpenConns(1)` deja una sola conexión y la
transacción la retiene. La fase 2 escribe eventos desde el motor y la fase 3 los querrá
atómicos junto al cambio de estado del destino, así que esto va **antes** del primer sink.

**Files:**
- Modify: `internal/store/db.go`
- Modify: `internal/store/settings.go`
- Modify: `internal/store/destinations.go`
- Modify: `internal/store/events.go`
- Test: `internal/store/tx_test.go`

**Interfaces:**
- Consumes: todo lo que la fase 1 dejó en `internal/store`.
- Produces: `func (d *DB) InTx(ctx context.Context, fn func(*DB) error) error`. El `*DB`
  que recibe `fn` enruta sus operaciones por la transacción, así que llamar a los
  repositorios dentro es seguro. Las firmas públicas existentes no cambian.

- [ ] **Step 1: Escribir el test que falla**

`internal/store/tx_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

// Sin InTx esto se autobloquea: la transacción retiene la única conexión.
func TestInTxAllowsNestedRepositoryCalls(t *testing.T) {
	db, c := bootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	err = db.InTx(ctx, func(tx *store.DB) error {
		if _, err := tx.LogEvent(ctx, store.Event{
			DestinationID: &dest.ID,
			Level:         store.LevelError,
			Kind:          "connect_failed",
			Message:       "connection refused",
		}); err != nil {
			return err
		}
		return tx.DeleteDestination(ctx, dest.ID)
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}

	events, err := db.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, quería 1", len(events))
	}
	list, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("el destino no se borró dentro de la transacción")
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	sentinel := errors.New("fallo del negocio")
	err = db.InTx(ctx, func(tx *store.DB) error {
		if err := tx.DeleteDestination(ctx, dest.ID); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx = %v, quería el error del callback", err)
	}

	list, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("el rollback no restauró el destino: %d destinos", len(list))
	}
}

// InTx anidado debe rechazarse en vez de autobloquearse.
func TestInTxRejectsNesting(t *testing.T) {
	db, _ := bootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := db.InTx(ctx, func(tx *store.DB) error {
		return tx.InTx(ctx, func(*store.DB) error { return nil })
	})
	if !errors.Is(err, store.ErrNestedTransaction) {
		t.Fatalf("InTx anidado = %v, quería ErrNestedTransaction", err)
	}
}
```

- [ ] **Step 2: Ejecutar el test y verificar que falla**

Run: `go test ./internal/store/ -run InTx -v -count=1`
Expected: FAIL con `undefined: (*store.DB).InTx`.

- [ ] **Step 3: Añadir `execer`, `InTx` y el centinela en `internal/store/db.go`**

Sustituye la definición del struct `DB` y sus accesores por esto (deja `Open`, `migrate` y
`loadMigrations` como están, salvo la línea de construcción que se indica):

```go
// execer abstrae *sql.DB y *sql.Tx: ambos exponen estos tres métodos. Permite que los
// repositorios se compongan dentro de una transacción sin autobloquear la única conexión
// que fija SetMaxOpenConns(1) (ver spec §15.1).
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ErrNestedTransaction se devuelve al llamar a InTx dentro de otro InTx. Con una sola
// conexión, anidar transacciones se bloquearía para siempre; es mejor un error claro.
var ErrNestedTransaction = errors.New("transacción anidada: InTx no se puede anidar")

// DB es la base de datos del servicio.
type DB struct {
	db *sql.DB // solo para abrir transacciones y cerrar
	ex execer  // por donde salen todas las consultas: *sql.DB o *sql.Tx
}

// SQL expone el *sql.DB subyacente. Solo para tests y para los repositorios de este
// paquete; el resto del programa usa los métodos tipados.
func (d *DB) SQL() *sql.DB { return d.db }

// Close cierra la base de datos.
func (d *DB) Close() error { return d.db.Close() }

// InTx ejecuta fn dentro de una transacción. El *DB que recibe fn enruta todas sus
// consultas por esa transacción, así que llamar a los repositorios dentro es seguro.
// Si fn devuelve error se hace rollback y se propaga; si no, se comitea.
func (d *DB) InTx(ctx context.Context, fn func(*DB) error) error {
	if _, ok := d.ex.(*sql.Tx); ok {
		return ErrNestedTransaction
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("abrir transacción: %w", err)
	}
	if err := fn(&DB{db: d.db, ex: tx}); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("comitear transacción: %w", err)
	}
	return nil
}
```

Añade `"errors"` a los imports de `db.go`. Y en `Open`, cambia la línea de construcción:

```go
	return &DB{db: sqlDB, ex: sqlDB}, nil
```

- [ ] **Step 4: Enrutar los repositorios por `d.ex`**

En `internal/store/settings.go`, `destinations.go` y `events.go`, sustituye **todas** las
apariciones de `d.db.ExecContext`, `d.db.QueryContext` y `d.db.QueryRowContext` por
`d.ex.ExecContext`, `d.ex.QueryContext` y `d.ex.QueryRowContext`.

En `destinations.go`, `ReorderDestinations` abre su propia transacción con
`d.db.BeginTx`. Reescríbela para que use `InTx` y así respete el nuevo contrato:

```go
// ReorderDestinations fija el orden a partir de la secuencia de ids recibida.
// Exige exactamente el conjunto completo de destinos existentes, sin repetidos y sin
// omisiones. Validar solo la longitud dejaría pasar una lista con un id duplicado, que
// dejaría sort_order empatados en silencio.
func (d *DB) ReorderDestinations(ctx context.Context, ids []int64) error {
	return d.InTx(ctx, func(tx *DB) error {
		existing, err := tx.destinationIDs(ctx)
		if err != nil {
			return err
		}

		seen := make(map[int64]bool, len(ids))
		for _, id := range ids {
			if seen[id] {
				return fmt.Errorf("reordenar: el id %d aparece más de una vez", id)
			}
			if !existing[id] {
				return fmt.Errorf("reordenar: %w (id %d)", ErrDestinationNotFound, id)
			}
			seen[id] = true
		}
		if len(seen) != len(existing) {
			return fmt.Errorf("reordenar exige los %d destinos, se recibieron %d", len(existing), len(seen))
		}

		for i, id := range ids {
			if _, err := tx.ex.ExecContext(ctx,
				`UPDATE destinations SET sort_order = ? WHERE id = ?`, i, id); err != nil {
				return fmt.Errorf("reordenar: %w", err)
			}
		}
		return nil
	})
}

// destinationIDs devuelve el conjunto de ids de destino existentes.
func (d *DB) destinationIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := d.ex.QueryContext(ctx, `SELECT id FROM destinations`)
	if err != nil {
		return nil, fmt.Errorf("reordenar: %w", err)
	}
	defer rows.Close()

	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("reordenar: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reordenar: %w", err)
	}
	return out, nil
}
```

Fíjate en que `destinationIDs` pasó de recibir un `*sql.Tx` a ser un método de `*DB` que
usa `d.ex`. Si quedaba la firma vieja `destinationIDs(ctx, tx)`, elimínala.

- [ ] **Step 5: Ejecutar todos los tests del paquete**

Run: `go test ./internal/store/ -race -count=1 -v 2>&1 | tail -20`
Expected: PASS en todo, incluidos los 3 tests nuevos de `InTx` y los que ya existían. Si
alguno se cuelga en vez de fallar, queda una consulta usando `d.db` en lugar de `d.ex`:
búscala con `grep -n 'd\.db\.' internal/store/*.go` — solo deben quedar `d.db.BeginTx`,
`d.db.Close` y el `return d.db` de `SQL()`.

- [ ] **Step 6: Commit**

```bash
git add internal/store/
git commit -m "refactor(store): interfaz execer e InTx para componer sin autobloqueo"
```

---

### Task 2: `Session` legible y `SessionByID`

Paga la deuda del spec §15.2. `Session.Width`, `Height` y `BitrateBPS` son `int`, pero las
columnas son nullable y `StartSession` las deja en NULL: escanear una sesión recién abierta
falla con `converting NULL to int is unsupported`.

**Por qué ahora y no en la fase 4, que es quien las leerá:** la fase 2 no lee sesiones, así
que estrictamente esto no le hace falta. Va aquí porque el struct hoy no puede representar
su propia fila —un defecto latente que ningún test detecta porque no hay camino de
lectura— y porque la fase 3 sí las va a leer para el bitrate y la resolución medidos. Es
más barato arreglarlo mientras se toca `events.go` que descubrirlo con el motor encima.

**Files:**
- Modify: `internal/store/events.go`
- Test: `internal/store/events_test.go`

**Interfaces:**
- Consumes: `store.DB`, `StartSession`, `FinishSession`.
- Produces: `Session` con `Width *int`, `Height *int`, `BitrateBPS *int`;
  `func (d *DB) SessionByID(ctx context.Context, id int64) (*Session, error)`, que devuelve
  `ErrSessionNotFound` si no existe.

- [ ] **Step 1: Escribir el test que falla**

Añade a `internal/store/events_test.go`:

```go
func TestSessionByIDReadsOpenSession(t *testing.T) {
	db, _ := bootstrapped(t)
	ctx := context.Background()

	id, err := db.StartSession(ctx)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Una sesión recién abierta tiene resolución y bitrate en NULL.
	s, err := db.SessionByID(ctx, id)
	if err != nil {
		t.Fatalf("SessionByID sobre una sesión abierta: %v", err)
	}
	if s.ID != id {
		t.Errorf("ID = %d, quería %d", s.ID, id)
	}
	if s.EndedAt != nil {
		t.Errorf("EndedAt = %v, quería nil en una sesión abierta", s.EndedAt)
	}
	if s.Width != nil || s.Height != nil || s.BitrateBPS != nil {
		t.Errorf("quería nil en Width/Height/BitrateBPS: %v %v %v", s.Width, s.Height, s.BitrateBPS)
	}
	if s.StartedAt.IsZero() {
		t.Error("StartedAt sin parsear")
	}
}

func TestSessionByIDReadsFinishedSession(t *testing.T) {
	db, _ := bootstrapped(t)
	ctx := context.Background()

	id, err := db.StartSession(ctx)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := db.FinishSession(ctx, id, 1920, 1080, 6_000_000); err != nil {
		t.Fatalf("FinishSession: %v", err)
	}

	s, err := db.SessionByID(ctx, id)
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if s.EndedAt == nil {
		t.Fatal("EndedAt = nil tras FinishSession")
	}
	if s.Width == nil || *s.Width != 1920 {
		t.Errorf("Width = %v, quería 1920", s.Width)
	}
	if s.Height == nil || *s.Height != 1080 {
		t.Errorf("Height = %v, quería 1080", s.Height)
	}
	if s.BitrateBPS == nil || *s.BitrateBPS != 6_000_000 {
		t.Errorf("BitrateBPS = %v, quería 6000000", s.BitrateBPS)
	}
}

func TestSessionByIDNotFound(t *testing.T) {
	db, _ := bootstrapped(t)
	if _, err := db.SessionByID(context.Background(), 9999); !errors.Is(err, store.ErrSessionNotFound) {
		t.Fatalf("SessionByID(9999) = %v, quería ErrSessionNotFound", err)
	}
}
```

Añade `"errors"` a los imports de `events_test.go` si no está.

- [ ] **Step 2: Ejecutar el test y verificar que falla**

Run: `go test ./internal/store/ -run SessionByID -v -count=1`
Expected: FAIL con `undefined: (*store.DB).SessionByID`.

- [ ] **Step 3: Cambiar el struct `Session` y añadir `SessionByID`**

En `internal/store/events.go`, sustituye el struct `Session`:

```go
// Session es una transmisión: desde que OBS conecta hasta que se va. Los campos de
// resolución y bitrate son punteros porque una sesión abierta los tiene en NULL, y
// porque "desconocido" y "cero" no deben ser indistinguibles.
type Session struct {
	ID         int64
	StartedAt  time.Time
	EndedAt    *time.Time
	Width      *int
	Height     *int
	BitrateBPS *int
}
```

Y añade, debajo de `FinishSession`:

```go
// SessionByID devuelve una sesión por su id.
func (d *DB) SessionByID(ctx context.Context, id int64) (*Session, error) {
	var (
		s         Session
		startedAt string
		endedAt   *string
	)
	err := d.ex.QueryRowContext(ctx,
		`SELECT id, started_at, ended_at, width, height, bitrate_bps FROM sessions WHERE id = ?`, id).
		Scan(&s.ID, &startedAt, &endedAt, &s.Width, &s.Height, &s.BitrateBPS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("leer sesión: %w", err)
	}

	if s.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt); err != nil {
		return nil, fmt.Errorf("started_at inválido: %w", err)
	}
	if endedAt != nil {
		t, err := time.Parse(time.RFC3339Nano, *endedAt)
		if err != nil {
			return nil, fmt.Errorf("ended_at inválido: %w", err)
		}
		s.EndedAt = &t
	}
	return &s, nil
}
```

Añade `"database/sql"` a los imports de `events.go` si no está.

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/store/ -race -count=1`
Expected: PASS. Si `TestStartAndFinishSession` fallara al compilar, es porque escaneaba a
variables locales; no lo toques salvo que el compilador lo exija.

- [ ] **Step 5: Commit**

```bash
git add internal/store/events.go internal/store/events_test.go
git commit -m "feat(store): Session legible con resolución nullable y SessionByID"
```

---

### Task 3: Inspección de tags FLV

El motor necesita saber, de cada mensaje de media y mirando solo su primer byte, si es un
keyframe, si es un sequence header, y si viene en enhanced-RTMP (que hay que rechazar).

**Files:**
- Create: `internal/flv/inspect.go`
- Test: `internal/flv/inspect_test.go`

**Interfaces:**
- Consumes: nada. `internal/flv` no importa nada del proyecto.
- Produces:
  ```go
  type VideoInfo struct {
      IsKeyframe       bool
      IsSequenceHeader bool
      IsEnhanced       bool
      CodecID          uint8
  }
  type AudioInfo struct {
      IsSequenceHeader bool
      SoundFormat      uint8
  }
  const (CodecIDAVC uint8 = 7; SoundFormatAAC uint8 = 10)
  var ErrEmptyPayload = errors.New("payload de tag vacío")
  func InspectVideo(payload []byte) (VideoInfo, error)
  func InspectAudio(payload []byte) (AudioInfo, error)
  ```

- [ ] **Step 1: Escribir el test que falla**

`internal/flv/inspect_test.go`:

```go
package flv_test

import (
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/flv"
)

func TestInspectVideoKeyframeAVC(t *testing.T) {
	// 0x17 = frameType 1 (keyframe) | codecID 7 (AVC); 0x01 = NALU (no seq header)
	got, err := flv.InspectVideo([]byte{0x17, 0x01, 0, 0, 0})
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if !got.IsKeyframe {
		t.Error("IsKeyframe = false, quería true")
	}
	if got.IsSequenceHeader {
		t.Error("IsSequenceHeader = true, quería false")
	}
	if got.IsEnhanced {
		t.Error("IsEnhanced = true, quería false")
	}
	if got.CodecID != flv.CodecIDAVC {
		t.Errorf("CodecID = %d, quería %d", got.CodecID, flv.CodecIDAVC)
	}
}

func TestInspectVideoInterFrame(t *testing.T) {
	// 0x27 = frameType 2 (inter frame) | codecID 7
	got, err := flv.InspectVideo([]byte{0x27, 0x01, 0, 0, 0})
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if got.IsKeyframe {
		t.Error("un inter frame no es keyframe")
	}
}

func TestInspectVideoSequenceHeader(t *testing.T) {
	// 0x17 keyframe|AVC, 0x00 = AVCPacketType 0 = sequence header
	got, err := flv.InspectVideo([]byte{0x17, 0x00, 0, 0, 0})
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if !got.IsSequenceHeader {
		t.Error("IsSequenceHeader = false, quería true")
	}
	// El AVC sequence header lleva el bit de keyframe puesto, pero no es un frame.
	if !got.IsKeyframe {
		t.Error("el sequence header trae el bit de keyframe; IsKeyframe debería reflejarlo")
	}
}

// Un sequence header necesita 2 bytes: si solo hay 1, no se puede decidir.
func TestInspectVideoSingleByteIsNotSequenceHeader(t *testing.T) {
	got, err := flv.InspectVideo([]byte{0x17})
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if got.IsSequenceHeader {
		t.Error("con un solo byte no se puede afirmar que sea sequence header")
	}
}

func TestInspectVideoDetectsEnhancedRTMP(t *testing.T) {
	// Bit alto (0x80) = isExVideoHeader de enhanced-RTMP. 0x90 | FourCC "hvc1".
	got, err := flv.InspectVideo([]byte{0x90, 'h', 'v', 'c', '1'})
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if !got.IsEnhanced {
		t.Error("IsEnhanced = false: no se detectó enhanced-RTMP")
	}
}

func TestInspectVideoRejectsEmpty(t *testing.T) {
	if _, err := flv.InspectVideo(nil); !errors.Is(err, flv.ErrEmptyPayload) {
		t.Fatalf("InspectVideo(nil) = %v, quería ErrEmptyPayload", err)
	}
}

func TestInspectAudioAACSequenceHeader(t *testing.T) {
	// 0xAF = soundFormat 10 (AAC) | 44kHz | 16bit | stereo; 0x00 = AACPacketType 0
	got, err := flv.InspectAudio([]byte{0xAF, 0x00, 0x12, 0x10})
	if err != nil {
		t.Fatalf("InspectAudio: %v", err)
	}
	if !got.IsSequenceHeader {
		t.Error("IsSequenceHeader = false, quería true")
	}
	if got.SoundFormat != flv.SoundFormatAAC {
		t.Errorf("SoundFormat = %d, quería %d", got.SoundFormat, flv.SoundFormatAAC)
	}
}

func TestInspectAudioAACRawFrame(t *testing.T) {
	got, err := flv.InspectAudio([]byte{0xAF, 0x01, 0x21, 0x00})
	if err != nil {
		t.Fatalf("InspectAudio: %v", err)
	}
	if got.IsSequenceHeader {
		t.Error("un frame raw no es sequence header")
	}
}

// Solo AAC usa AACPacketType. Con MP3 (soundFormat 2) el segundo byte es audio, no un tipo.
func TestInspectAudioNonAACIsNeverSequenceHeader(t *testing.T) {
	got, err := flv.InspectAudio([]byte{0x2F, 0x00, 0x00})
	if err != nil {
		t.Fatalf("InspectAudio: %v", err)
	}
	if got.IsSequenceHeader {
		t.Error("un tag no-AAC nunca es sequence header")
	}
	if got.SoundFormat != 2 {
		t.Errorf("SoundFormat = %d, quería 2 (MP3)", got.SoundFormat)
	}
}

func TestInspectAudioRejectsEmpty(t *testing.T) {
	if _, err := flv.InspectAudio([]byte{}); !errors.Is(err, flv.ErrEmptyPayload) {
		t.Fatalf("InspectAudio(vacío) = %v, quería ErrEmptyPayload", err)
	}
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/flv/ -v`
Expected: FAIL — el paquete `flv` no existe.

- [ ] **Step 3: Implementar `internal/flv/inspect.go`**

```go
// Package flv inspecciona el primer byte de los tags de media de FLV/RTMP, lo justo
// para que el relay decida qué hacer con cada mensaje sin decodificar nada.
package flv

import "errors"

// ErrEmptyPayload indica que el tag no trae ni el byte de cabecera.
var ErrEmptyPayload = errors.New("payload de tag vacío")

// Identificadores de códec que Splitstream acepta. El relay rechaza lo demás porque no
// puede transcodificar y porque no todas las plataformas aceptan lo mismo (spec §3.6).
const (
	CodecIDAVC     uint8 = 7  // H.264 en la ruta clásica de FLV
	SoundFormatAAC uint8 = 10 // AAC
)

// exVideoHeaderBit es el bit alto del primer byte de un tag de video. En enhanced-RTMP
// marca que lo que sigue es una cabecera con FourCC (HEVC, AV1, VP9) en vez del par
// frameType/codecID clásico.
const exVideoHeaderBit = 0x80

// VideoInfo es lo que se puede saber de un tag de video sin decodificarlo.
type VideoInfo struct {
	IsKeyframe       bool
	IsSequenceHeader bool
	IsEnhanced       bool
	CodecID          uint8
}

// AudioInfo es lo que se puede saber de un tag de audio sin decodificarlo.
type AudioInfo struct {
	IsSequenceHeader bool
	SoundFormat      uint8
}

// InspectVideo lee la cabecera de un tag de video.
//
// Formato clásico: el primer byte son 4 bits de frameType (1 = keyframe) y 4 de codecID
// (7 = AVC). Si el códec es AVC, el segundo byte es el AVCPacketType, y 0 significa
// sequence header (SPS/PPS).
func InspectVideo(payload []byte) (VideoInfo, error) {
	if len(payload) == 0 {
		return VideoInfo{}, ErrEmptyPayload
	}

	b := payload[0]
	info := VideoInfo{
		IsEnhanced: b&exVideoHeaderBit != 0,
		IsKeyframe: (b>>4)&0x07 == 1,
		CodecID:    b & 0x0f,
	}
	// El AVCPacketType solo existe en la ruta clásica de AVC, y necesita un segundo byte.
	if !info.IsEnhanced && info.CodecID == CodecIDAVC && len(payload) >= 2 {
		info.IsSequenceHeader = payload[1] == 0x00
	}
	return info, nil
}

// InspectAudio lee la cabecera de un tag de audio.
//
// El primer byte son 4 bits de soundFormat (10 = AAC) y 4 de tasa, tamaño y canales.
// Solo en AAC el segundo byte es el AACPacketType, y 0 significa sequence header.
func InspectAudio(payload []byte) (AudioInfo, error) {
	if len(payload) == 0 {
		return AudioInfo{}, ErrEmptyPayload
	}

	info := AudioInfo{SoundFormat: (payload[0] >> 4) & 0x0f}
	if info.SoundFormat == SoundFormatAAC && len(payload) >= 2 {
		info.IsSequenceHeader = payload[1] == 0x00
	}
	return info, nil
}
```

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/flv/ -race -count=1 -v`
Expected: PASS en los 10 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/flv/
git commit -m "feat(flv): inspección de tags de media y detección de enhanced-RTMP"
```

---

### Task 4: Tipos del relay, preámbulo y timebase

Las piezas puras del motor: el mensaje que circula, la interfaz del destino, la caché de lo
que un destino tardío necesita antes de cualquier frame, y la traducción de timestamps.

**Files:**
- Create: `internal/relay/message.go`
- Create: `internal/relay/publisher.go`
- Create: `internal/relay/preamble.go`
- Create: `internal/relay/timebase.go`
- Test: `internal/relay/preamble_test.go`
- Test: `internal/relay/timebase_test.go`

**Interfaces:**
- Consumes: nada del proyecto. `internal/relay` no importa `go-rtmp` ni `database/sql`.
- Produces:
  ```go
  type Kind uint8
  const (KindAudio Kind = iota; KindVideo; KindMeta)
  type Message struct {
      Kind        Kind
      Timestamp   uint32
      Payload     []byte
      IsKeyframe  bool
      IsSeqHeader bool
  }

  type Publisher interface {
      Connect(ctx context.Context) error
      WriteMeta(ts uint32, payload []byte) error
      WriteAudio(ts uint32, payload []byte) error
      WriteVideo(ts uint32, payload []byte) error
      Close() error
  }

  type Preamble struct{ ... }  // seguro para uso concurrente
  func (p *Preamble) Observe(msg *Message)
  func (p *Preamble) Snapshot() (meta, videoSeq, audioSeq *Message)
  func (p *Preamble) Reset()

  type timebase struct{ ... } // no exportado, lo usa sink.go
  ```

- [ ] **Step 1: Escribir los tests que fallan**

`internal/relay/preamble_test.go`:

```go
package relay

import "testing"

func msg(kind Kind, ts uint32, seq bool) *Message {
	return &Message{Kind: kind, Timestamp: ts, Payload: []byte{byte(ts)}, IsSeqHeader: seq}
}

func TestPreambleCachesTheThreeMessages(t *testing.T) {
	var p Preamble
	p.Observe(msg(KindMeta, 0, false))
	p.Observe(msg(KindVideo, 0, true))
	p.Observe(msg(KindAudio, 0, true))
	// Media normal: no debe reemplazar nada de lo anterior.
	p.Observe(msg(KindVideo, 33, false))
	p.Observe(msg(KindAudio, 23, false))

	meta, videoSeq, audioSeq := p.Snapshot()
	if meta == nil || meta.Kind != KindMeta {
		t.Errorf("meta = %v", meta)
	}
	if videoSeq == nil || !videoSeq.IsSeqHeader || videoSeq.Kind != KindVideo {
		t.Errorf("videoSeq = %v", videoSeq)
	}
	if audioSeq == nil || !audioSeq.IsSeqHeader || audioSeq.Kind != KindAudio {
		t.Errorf("audioSeq = %v", audioSeq)
	}
	if videoSeq.Timestamp != 0 {
		t.Errorf("el frame normal sobrescribió el sequence header de video")
	}
}

func TestPreambleEmptySnapshot(t *testing.T) {
	var p Preamble
	meta, videoSeq, audioSeq := p.Snapshot()
	if meta != nil || videoSeq != nil || audioSeq != nil {
		t.Error("un preámbulo vacío debe devolver tres nil")
	}
}

// Si el publisher renegocia (OBS cambia de códec a mitad), el nuevo header manda.
func TestPreambleLatestSequenceHeaderWins(t *testing.T) {
	var p Preamble
	p.Observe(&Message{Kind: KindVideo, Timestamp: 0, Payload: []byte{1}, IsSeqHeader: true})
	p.Observe(&Message{Kind: KindVideo, Timestamp: 500, Payload: []byte{2}, IsSeqHeader: true})

	_, videoSeq, _ := p.Snapshot()
	if videoSeq.Payload[0] != 2 {
		t.Errorf("payload = %v, quería el sequence header más reciente", videoSeq.Payload)
	}
}

func TestPreambleReset(t *testing.T) {
	var p Preamble
	p.Observe(msg(KindMeta, 0, false))
	p.Observe(msg(KindVideo, 0, true))
	p.Reset()

	meta, videoSeq, audioSeq := p.Snapshot()
	if meta != nil || videoSeq != nil || audioSeq != nil {
		t.Error("Reset debe vaciar el preámbulo")
	}
}
```

`internal/relay/timebase_test.go`:

```go
package relay

import "testing"

func TestTimebaseAnchorsToKeyframe(t *testing.T) {
	var tb timebase
	if tb.started() {
		t.Fatal("un timebase recién creado no está arrancado")
	}
	tb.start(5000)
	if !tb.started() {
		t.Fatal("start() debe marcarlo como arrancado")
	}

	out, ok := tb.translate(5000)
	if !ok || out != 0 {
		t.Errorf("translate(5000) = (%d, %v), quería (0, true)", out, ok)
	}
	out, ok = tb.translate(5033)
	if !ok || out != 33 {
		t.Errorf("translate(5033) = (%d, %v), quería (33, true)", out, ok)
	}
}

// Audio y video comparten base: la traducción no depende de la pista.
func TestTimebaseSharedAcrossTracks(t *testing.T) {
	var tb timebase
	tb.start(5000)

	video, okV := tb.translate(5100)
	audio, okA := tb.translate(5100)
	if !okV || !okA || video != audio {
		t.Errorf("video=%d audio=%d: la base debe ser común a las dos pistas", video, audio)
	}
}

// El audio anterior a la base se descarta en vez de emitirse negativo (spec §3.2).
func TestTimebaseDropsPreBaseAudio(t *testing.T) {
	var tb timebase
	tb.start(5000)

	if _, ok := tb.translate(4999); ok {
		t.Error("un timestamp anterior a la base debe descartarse")
	}
	if _, ok := tb.translate(4000); ok {
		t.Error("un timestamp muy anterior a la base debe descartarse")
	}
}

func TestTimebaseResetAllowsNewAnchor(t *testing.T) {
	var tb timebase
	tb.start(5000)
	tb.reset()
	if tb.started() {
		t.Fatal("reset debe desarmar el timebase")
	}
	tb.start(9000)
	out, ok := tb.translate(9500)
	if !ok || out != 500 {
		t.Errorf("tras reconectar, translate(9500) = (%d, %v), quería (500, true)", out, ok)
	}
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/relay/ -v`
Expected: FAIL — el paquete `relay` no existe.

- [ ] **Step 3: Implementar `internal/relay/message.go`**

```go
// Package relay es el motor de retransmisión: reparte los mensajes del publisher a los
// destinos. No importa go-rtmp ni database/sql, así que se testea entero en memoria.
package relay

// Kind distingue las tres clases de mensaje que circulan por el hub.
type Kind uint8

const (
	KindAudio Kind = iota
	KindVideo
	KindMeta
)

// String da un nombre legible para logs y errores.
func (k Kind) String() string {
	switch k {
	case KindAudio:
		return "audio"
	case KindVideo:
		return "video"
	case KindMeta:
		return "meta"
	default:
		return "desconocido"
	}
}

// Message es un mensaje de media listo para reenviar.
//
// Payload es inmutable y se comparte entre todos los sinks: nadie debe escribir en él.
// No se usa un pool ni refcount a propósito — a 8 Mbps son ~1 MB/s de asignaciones, que
// el GC no nota. Si algún día se vuelve medible, el pool va detrás de esta misma forma.
type Message struct {
	Kind        Kind
	Timestamp   uint32
	Payload     []byte
	IsKeyframe  bool
	IsSeqHeader bool
}
```

- [ ] **Step 4: Implementar `internal/relay/publisher.go`**

```go
package relay

import "context"

// Publisher es un destino de salida ya resuelto: sabe a dónde conectarse y con qué clave.
//
// Cada sink posee el suyo y lo usa desde una sola goroutine, así que las implementaciones
// NO necesitan ser seguras para uso concurrente. A cambio, deben tolerar que Close() se
// llame sin que Connect() haya tenido éxito.
type Publisher interface {
	// Connect abre la conexión y deja el stream listo para recibir media.
	Connect(ctx context.Context) error
	// WriteMeta envía el onMetaData. La implementación es responsable de envolverlo en
	// @setDataFrame (spec §3.5).
	WriteMeta(ts uint32, payload []byte) error
	WriteAudio(ts uint32, payload []byte) error
	WriteVideo(ts uint32, payload []byte) error
	// Close cierra ordenadamente. Es idempotente.
	Close() error
}
```

- [ ] **Step 5: Implementar `internal/relay/preamble.go`**

```go
package relay

import "sync"

// Preamble guarda los tres mensajes que todo destino necesita recibir antes que cualquier
// frame: el onMetaData, el AVC sequence header y el AAC sequence header (spec §6.2).
//
// Sin ellos, un destino que se conecte a mitad de transmisión no sabe decodificar nada.
// El valor cero está listo para usarse.
type Preamble struct {
	mu       sync.RWMutex
	meta     *Message
	videoSeq *Message
	audioSeq *Message
}

// Observe registra el mensaje si es uno de los tres del preámbulo. Los frames normales
// se ignoran. Un sequence header nuevo sustituye al anterior: si el publisher renegocia
// a mitad de transmisión, manda el último.
func (p *Preamble) Observe(msg *Message) {
	switch {
	case msg.Kind == KindMeta:
	case msg.Kind == KindVideo && msg.IsSeqHeader:
	case msg.Kind == KindAudio && msg.IsSeqHeader:
	default:
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	switch msg.Kind {
	case KindMeta:
		p.meta = msg
	case KindVideo:
		p.videoSeq = msg
	case KindAudio:
		p.audioSeq = msg
	}
}

// Snapshot devuelve los tres mensajes cacheados. Cualquiera puede ser nil si todavía no
// se ha visto. Los mensajes son inmutables, así que devolverlos es seguro.
func (p *Preamble) Snapshot() (meta, videoSeq, audioSeq *Message) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.meta, p.videoSeq, p.audioSeq
}

// Reset olvida los tres mensajes. Se llama al terminar una sesión: los headers de la
// transmisión anterior no valen para la siguiente.
func (p *Preamble) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.meta, p.videoSeq, p.audioSeq = nil, nil, nil
}
```

- [ ] **Step 6: Implementar `internal/relay/timebase.go`**

```go
package relay

// timebase traduce los timestamps del publisher a los que ve un destino concreto.
//
// La base es ÚNICA para audio y video y se ancla al keyframe con el que arranca el envío
// (spec §3.2). Anclar cada pista por separado desincronizaría el audio del video, que es
// el clásico "el audio va adelantado tras reconectar". El audio anterior a la base se
// descarta en vez de emitirse negativo.
//
// En cada reconexión se llama a reset() y luego a start() con el keyframe nuevo: es una
// sesión RTMP nueva y la plataforma espera un timeline que arranca en 0.
type timebase struct {
	armed bool
	base  uint32
}

// started indica si ya hay una base fijada.
func (t *timebase) started() bool { return t.armed }

// start fija la base en el timestamp del keyframe de arranque.
func (t *timebase) start(keyframeTimestamp uint32) {
	t.base = keyframeTimestamp
	t.armed = true
}

// reset desarma el timebase para que la próxima conexión fije una base nueva.
func (t *timebase) reset() {
	t.armed = false
	t.base = 0
}

// translate convierte un timestamp del publisher al del destino. Devuelve ok=false si el
// mensaje es anterior a la base y por tanto debe descartarse.
func (t *timebase) translate(ts uint32) (uint32, bool) {
	if !t.armed || ts < t.base {
		return 0, false
	}
	return ts - t.base, true
}
```

- [ ] **Step 7: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/relay/ -race -count=1 -v`
Expected: PASS en los 8 tests.

- [ ] **Step 8: Commit**

```bash
git add internal/relay/
git commit -m "feat(relay): mensaje, interfaz Publisher, preámbulo y rebase de timestamps"
```

---

### Task 5: Sink y hub

El sink es la goroutine que atiende a un destino: espera al primer keyframe, manda el
preámbulo, y a partir de ahí traduce y reenvía. El hub reparte a los sinks sin que un
destino lento afecte al publisher.

**Files:**
- Create: `internal/relay/sink.go`
- Create: `internal/relay/hub.go`
- Create: `internal/relay/fake_publisher_test.go`
- Test: `internal/relay/sink_test.go`
- Test: `internal/relay/hub_test.go`

**Interfaces:**
- Consumes: `Message`, `Kind`, `Publisher`, `Preamble`, `timebase` de la Task 4.
- Produces:
  ```go
  type State uint8
  const (StateIdle State = iota; StateConnecting; StateLive; StateError)
  func (s State) String() string

  type SinkConfig struct {
      ID       int64
      Name     string
      Pub      Publisher
      Queue    int           // capacidad; 0 usa DefaultQueueSize
      Logger   *slog.Logger  // nil usa slog.Default()
  }
  const DefaultQueueSize = 512

  func NewSink(cfg SinkConfig) *Sink
  func (s *Sink) Start(ctx context.Context, pre *Preamble)
  func (s *Sink) Enqueue(msg *Message)   // no bloqueante; descarta si está llena
  func (s *Sink) Stop()                  // idempotente; espera a que la goroutine termine
  func (s *Sink) State() State
  func (s *Sink) Dropped() uint64
  func (s *Sink) LastError() error

  func NewHub(logger *slog.Logger) *Hub
  func (h *Hub) Preamble() *Preamble
  func (h *Hub) Add(s *Sink)
  func (h *Hub) Remove(id int64)
  func (h *Hub) Publish(msg *Message)
  func (h *Hub) Close()
  ```

- [ ] **Step 1: Escribir el publisher falso**

`internal/relay/fake_publisher_test.go`:

```go
package relay

import (
	"context"
	"errors"
	"sync"
)

// writtenMsg es lo que el fake registra de cada escritura.
type writtenMsg struct {
	Kind Kind
	TS   uint32
	Data []byte
}

// fakePublisher es un Publisher en memoria. Permite testear el motor entero sin red.
type fakePublisher struct {
	mu          sync.Mutex
	written     []writtenMsg
	connects    int
	closes      int
	connectErr  error
	writeErr    error
	blockWrites chan struct{} // si no es nil, cada escritura espera aquí
}

func (f *fakePublisher) Connect(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connects++
	return f.connectErr
}

func (f *fakePublisher) write(kind Kind, ts uint32, payload []byte) error {
	if f.blockWrites != nil {
		<-f.blockWrites
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	f.written = append(f.written, writtenMsg{Kind: kind, TS: ts, Data: cp})
	return nil
}

func (f *fakePublisher) WriteMeta(ts uint32, p []byte) error  { return f.write(KindMeta, ts, p) }
func (f *fakePublisher) WriteAudio(ts uint32, p []byte) error { return f.write(KindAudio, ts, p) }
func (f *fakePublisher) WriteVideo(ts uint32, p []byte) error { return f.write(KindVideo, ts, p) }

func (f *fakePublisher) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return nil
}

func (f *fakePublisher) snapshot() []writtenMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]writtenMsg, len(f.written))
	copy(out, f.written)
	return out
}

var errFakeWrite = errors.New("fallo simulado de escritura")
```

- [ ] **Step 2: Escribir los tests que fallan**

`internal/relay/sink_test.go`:

```go
package relay

import (
	"context"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("tiempo agotado esperando: %s", msg)
}

func videoKey(ts uint32) *Message {
	return &Message{Kind: KindVideo, Timestamp: ts, Payload: []byte{0x17, 0x01}, IsKeyframe: true}
}
func videoInter(ts uint32) *Message {
	return &Message{Kind: KindVideo, Timestamp: ts, Payload: []byte{0x27, 0x01}}
}
func audioRaw(ts uint32) *Message {
	return &Message{Kind: KindAudio, Timestamp: ts, Payload: []byte{0xAF, 0x01}}
}

func preambleWith() *Preamble {
	p := &Preamble{}
	p.Observe(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	p.Observe(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	p.Observe(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})
	return p
}

// Antes del primer keyframe no se envía nada; el preámbulo sale justo antes de él.
func TestSinkWaitsForKeyframeThenSendsPreamble(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "YouTube", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	// Media anterior al keyframe: debe descartarse entera.
	s.Enqueue(videoInter(1000))
	s.Enqueue(audioRaw(1010))
	time.Sleep(50 * time.Millisecond)
	if got := pub.snapshot(); len(got) != 0 {
		t.Fatalf("se envió media antes del keyframe: %+v", got)
	}

	s.Enqueue(videoKey(2000))
	waitFor(t, func() bool { return len(pub.snapshot()) >= 4 }, "preámbulo + keyframe")

	got := pub.snapshot()
	if got[0].Kind != KindMeta {
		t.Errorf("el primer mensaje debe ser el onMetaData, fue %v", got[0].Kind)
	}
	if got[1].Kind != KindVideo || got[1].TS != 0 {
		t.Errorf("el segundo debe ser el AVC sequence header con ts=0, fue %+v", got[1])
	}
	if got[2].Kind != KindAudio || got[2].TS != 0 {
		t.Errorf("el tercero debe ser el AAC sequence header con ts=0, fue %+v", got[2])
	}
	if got[3].Kind != KindVideo || got[3].TS != 0 {
		t.Errorf("el keyframe debe salir con ts=0, fue %+v", got[3])
	}
}

// Tras el keyframe, los timestamps salen rebasados y con base común A/V.
func TestSinkRebasesTimestamps(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	s.Enqueue(videoKey(5000))
	s.Enqueue(audioRaw(5010))
	s.Enqueue(videoInter(5033))
	waitFor(t, func() bool { return len(pub.snapshot()) >= 6 }, "3 mensajes de preámbulo + 3 de media")

	got := pub.snapshot()[3:]
	want := []struct {
		kind Kind
		ts   uint32
	}{{KindVideo, 0}, {KindAudio, 10}, {KindVideo, 33}}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].TS != w.ts {
			t.Errorf("mensaje %d = (%v, %d), quería (%v, %d)", i, got[i].Kind, got[i].TS, w.kind, w.ts)
		}
	}
}

// El audio anterior a la base se descarta en vez de salir negativo.
func TestSinkDropsAudioOlderThanBase(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	s.Enqueue(videoKey(5000))
	s.Enqueue(audioRaw(4990)) // anterior a la base
	s.Enqueue(audioRaw(5020))
	waitFor(t, func() bool { return len(pub.snapshot()) >= 5 }, "preámbulo + keyframe + audio válido")

	for _, m := range pub.snapshot() {
		if m.Kind == KindAudio && m.TS > 1_000_000 {
			t.Fatalf("un timestamp de audio desbordó: %+v", m)
		}
	}
	media := pub.snapshot()[3:]
	if len(media) != 2 {
		t.Fatalf("se enviaron %d mensajes de media, quería 2 (el audio previo se descarta)", len(media))
	}
	if media[1].TS != 20 {
		t.Errorf("el audio válido salió con ts=%d, quería 20", media[1].TS)
	}
}

func TestSinkConnectFailureSetsErrorState(t *testing.T) {
	pub := &fakePublisher{connectErr: errFakeWrite}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	waitFor(t, func() bool { return s.State() == StateError }, "estado error")
	if s.LastError() == nil {
		t.Error("LastError = nil tras fallar Connect")
	}
}

func TestSinkWriteFailureSetsErrorState(t *testing.T) {
	pub := &fakePublisher{writeErr: errFakeWrite}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	s.Enqueue(videoKey(1000))
	waitFor(t, func() bool { return s.State() == StateError }, "estado error tras fallar el write")
}

// Un sink lento no debe bloquear a quien encola: se descarta y se cuenta.
func TestSinkEnqueueNeverBlocks(t *testing.T) {
	block := make(chan struct{})
	pub := &fakePublisher{blockWrites: block}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub, Queue: 4})
	s.Start(context.Background(), preambleWith())
	defer func() { close(block); s.Stop() }()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			s.Enqueue(videoKey(uint32(1000 + i)))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Enqueue se bloqueó: un destino lento no debe frenar al publisher")
	}
	waitFor(t, func() bool { return s.Dropped() > 0 }, "contador de descartes")
}

func TestSinkStopIsIdempotentAndClosesPublisher(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	s.Stop()
	s.Stop() // no debe entrar en pánico ni colgarse

	if s.State() != StateIdle {
		t.Errorf("State = %v tras Stop, quería idle", s.State())
	}
	pub.mu.Lock()
	closes := pub.closes
	pub.mu.Unlock()
	if closes == 0 {
		t.Error("Stop debe cerrar el Publisher")
	}
}
```

`internal/relay/hub_test.go`:

```go
package relay

import (
	"context"
	"testing"
)

func TestHubFansOutToAllSinks(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	pubs := make([]*fakePublisher, 3)
	for i := range pubs {
		pubs[i] = &fakePublisher{}
		s := NewSink(SinkConfig{ID: int64(i + 1), Name: "dest", Pub: pubs[i]})
		s.Start(context.Background(), h.Preamble())
		h.Add(s)
	}

	h.Publish(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	h.Publish(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	h.Publish(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})
	h.Publish(videoKey(1000))

	for i, p := range pubs {
		waitFor(t, func() bool { return len(p.snapshot()) >= 4 }, "el sink recibió el preámbulo y el keyframe")
		if got := len(p.snapshot()); got < 4 {
			t.Errorf("sink %d recibió %d mensajes", i, got)
		}
	}
}

// El hub observa el preámbulo, de modo que un sink que llega tarde lo recibe igual.
func TestHubLateSinkGetsPreamble(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	h.Publish(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	h.Publish(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	h.Publish(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})
	h.Publish(videoInter(500)) // el sink todavía no existe

	pub := &fakePublisher{}
	late := NewSink(SinkConfig{ID: 9, Name: "tardío", Pub: pub})
	late.Start(context.Background(), h.Preamble())
	h.Add(late)

	h.Publish(videoKey(1000))
	waitFor(t, func() bool { return len(pub.snapshot()) >= 4 }, "el sink tardío recibió el preámbulo")

	got := pub.snapshot()
	if got[0].Kind != KindMeta || got[1].Kind != KindVideo || got[2].Kind != KindAudio {
		t.Errorf("el preámbulo llegó desordenado: %v %v %v", got[0].Kind, got[1].Kind, got[2].Kind)
	}
}

func TestHubRemoveStopsSink(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 7, Name: "X", Pub: pub})
	s.Start(context.Background(), h.Preamble())
	h.Add(s)
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	h.Remove(7)
	waitFor(t, func() bool { return s.State() == StateIdle }, "el sink se detuvo al quitarlo")

	pub.mu.Lock()
	closes := pub.closes
	pub.mu.Unlock()
	if closes == 0 {
		t.Error("quitar un sink debe cerrar su Publisher")
	}
}

// Publicar sin sinks no debe entrar en pánico.
func TestHubPublishWithNoSinks(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()
	h.Publish(videoKey(1000))
}
```

- [ ] **Step 3: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/relay/ -run 'Sink|Hub' -v`
Expected: FAIL con `undefined: NewSink`.

- [ ] **Step 4: Implementar `internal/relay/sink.go`**

```go
package relay

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// DefaultQueueSize es la capacidad por defecto de la cola de un sink.
//
// La fase 3 sustituye esta cola por un deque acotado por bytes y duración, con descarte
// por GOP completo (spec §3.3 y §3.4). Mientras tanto, un canal con descarte simple basta
// para el objetivo de la fase 2: un destino de punta a punta.
const DefaultQueueSize = 512

// State es el estado de un destino. `degraded` es un atributo aparte y llega en la fase 3
// (spec §3.7); aquí solo están los estados de esta fase.
type State uint8

const (
	StateIdle State = iota
	StateConnecting
	StateLive
	StateError
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateConnecting:
		return "connecting"
	case StateLive:
		return "live"
	case StateError:
		return "error"
	default:
		return "desconocido"
	}
}

// SinkConfig son los datos para construir un sink.
type SinkConfig struct {
	ID     int64
	Name   string
	Pub    Publisher
	Queue  int          // capacidad de la cola; 0 usa DefaultQueueSize
	Logger *slog.Logger // nil usa slog.Default()
}

// Sink atiende a un destino desde su propia goroutine. Posee su Publisher, su timebase y
// su estado; nadie más los toca.
type Sink struct {
	id     int64
	name   string
	pub    Publisher
	log    *slog.Logger
	ch     chan *Message
	quit   chan struct{}
	done   chan struct{}
	once   sync.Once
	state  atomic.Uint32
	drops  atomic.Uint64
	errMu  sync.Mutex
	lastEr error
}

// NewSink construye un sink parado. Hay que llamar a Start para que atienda.
func NewSink(cfg SinkConfig) *Sink {
	size := cfg.Queue
	if size <= 0 {
		size = DefaultQueueSize
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Sink{
		id:   cfg.ID,
		name: cfg.Name,
		pub:  cfg.Pub,
		log:  log.With("destino_id", cfg.ID, "destino", cfg.Name),
		ch:   make(chan *Message, size),
		quit: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// ID devuelve el identificador del destino.
func (s *Sink) ID() int64 { return s.id }

// State devuelve el estado actual.
func (s *Sink) State() State { return State(s.state.Load()) }

// Dropped devuelve cuántos mensajes se han descartado por cola llena.
func (s *Sink) Dropped() uint64 { return s.drops.Load() }

// LastError devuelve el último error observado, o nil.
func (s *Sink) LastError() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.lastEr
}

func (s *Sink) setState(st State) { s.state.Store(uint32(st)) }

func (s *Sink) fail(err error) {
	s.errMu.Lock()
	s.lastEr = err
	s.errMu.Unlock()
	s.setState(StateError)
	s.log.Error("destino en error", "err", err)
}

// Start lanza la goroutine del sink. pre es el preámbulo de la sesión: el sink lo lee
// justo antes de mandar su primer keyframe.
func (s *Sink) Start(ctx context.Context, pre *Preamble) {
	go s.run(ctx, pre)
}

// Enqueue entrega un mensaje al sink sin bloquear nunca. Si la cola está llena, el
// mensaje se descarta y se cuenta: un destino lento no puede frenar al publisher ni a
// sus hermanos (spec §6.2).
func (s *Sink) Enqueue(msg *Message) {
	select {
	case s.ch <- msg:
	default:
		s.drops.Add(1)
	}
}

// Stop detiene el sink y espera a que su goroutine termine. Es idempotente.
func (s *Sink) Stop() {
	s.once.Do(func() { close(s.quit) })
	<-s.done
}

func (s *Sink) run(ctx context.Context, pre *Preamble) {
	defer close(s.done)
	defer s.pub.Close()

	s.setState(StateConnecting)
	if err := s.pub.Connect(ctx); err != nil {
		s.fail(err)
		<-s.quit
		s.setState(StateIdle)
		return
	}
	s.setState(StateLive)
	s.log.Info("destino conectado")

	var tb timebase
	for {
		select {
		case <-s.quit:
			s.setState(StateIdle)
			return
		case <-ctx.Done():
			s.setState(StateIdle)
			return
		case msg := <-s.ch:
			if err := s.handle(msg, pre, &tb); err != nil {
				s.fail(err)
				// La reconexión llega en la fase 3. Aquí el sink se queda en error
				// hasta que lo paren, sin consumir más mensajes.
				<-s.quit
				s.setState(StateIdle)
				return
			}
		}
	}
}

// handle procesa un mensaje. Antes del primer keyframe descarta todo; en el keyframe
// manda el preámbulo y ancla el timebase; después traduce y reenvía.
func (s *Sink) handle(msg *Message, pre *Preamble, tb *timebase) error {
	if !tb.started() {
		// Solo un keyframe de video real arranca el envío. Un sequence header trae el
		// bit de keyframe puesto pero no es un frame decodificable.
		if msg.Kind != KindVideo || !msg.IsKeyframe || msg.IsSeqHeader {
			return nil
		}
		if err := s.sendPreamble(pre); err != nil {
			return err
		}
		tb.start(msg.Timestamp)
	}

	ts, ok := tb.translate(msg.Timestamp)
	if !ok {
		return nil // anterior a la base: se descarta (spec §3.2)
	}

	switch msg.Kind {
	case KindVideo:
		return s.pub.WriteVideo(ts, msg.Payload)
	case KindAudio:
		return s.pub.WriteAudio(ts, msg.Payload)
	case KindMeta:
		return s.pub.WriteMeta(ts, msg.Payload)
	}
	return nil
}

// sendPreamble manda onMetaData, AVC sequence header y AAC sequence header, los tres con
// ts=0, antes de cualquier frame (spec §6.3).
func (s *Sink) sendPreamble(pre *Preamble) error {
	meta, videoSeq, audioSeq := pre.Snapshot()
	if meta != nil {
		if err := s.pub.WriteMeta(0, meta.Payload); err != nil {
			return err
		}
	}
	if videoSeq != nil {
		if err := s.pub.WriteVideo(0, videoSeq.Payload); err != nil {
			return err
		}
	}
	if audioSeq != nil {
		if err := s.pub.WriteAudio(0, audioSeq.Payload); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: Implementar `internal/relay/hub.go`**

```go
package relay

import (
	"log/slog"
	"sync"
)

// Hub reparte cada mensaje del publisher a todos los sinks registrados.
//
// Publish nunca bloquea: la entrega a cada sink es un envío no bloqueante a su cola, así
// que un destino lento no frena al publisher ni a sus hermanos (spec §6.2).
type Hub struct {
	log   *slog.Logger
	pre   Preamble
	mu    sync.RWMutex
	sinks map[int64]*Sink
}

// NewHub construye un hub vacío. logger nil usa slog.Default().
func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{log: logger, sinks: map[int64]*Sink{}}
}

// Preamble devuelve el preámbulo de la sesión, que los sinks leen al arrancar.
func (h *Hub) Preamble() *Preamble { return &h.pre }

// Add registra un sink ya arrancado.
func (h *Hub) Add(s *Sink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.sinks[s.ID()]; ok {
		go old.Stop()
	}
	h.sinks[s.ID()] = s
	h.log.Info("destino registrado en el hub", "destino_id", s.ID())
}

// Remove quita un sink y lo detiene. No hace nada si el id no está registrado.
func (h *Hub) Remove(id int64) {
	h.mu.Lock()
	s, ok := h.sinks[id]
	delete(h.sinks, id)
	h.mu.Unlock()

	if ok {
		s.Stop()
		h.log.Info("destino quitado del hub", "destino_id", id)
	}
}

// Publish entrega el mensaje a todos los sinks y actualiza el preámbulo de la sesión.
func (h *Hub) Publish(msg *Message) {
	h.pre.Observe(msg)

	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.sinks {
		s.Enqueue(msg)
	}
}

// Close detiene todos los sinks y olvida el preámbulo. El hub queda reutilizable.
func (h *Hub) Close() {
	h.mu.Lock()
	sinks := make([]*Sink, 0, len(h.sinks))
	for _, s := range h.sinks {
		sinks = append(sinks, s)
	}
	h.sinks = map[int64]*Sink{}
	h.mu.Unlock()

	for _, s := range sinks {
		s.Stop()
	}
	h.pre.Reset()
}
```

- [ ] **Step 6: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/relay/ -race -count=1 -v`
Expected: PASS en los 19 tests del paquete.

- [ ] **Step 7: Commit**

```bash
git add internal/relay/
git commit -m "feat(relay): sink con espera de keyframe y hub con fan-out no bloqueante"
```

---

### Task 6: Publisher sobre el cliente de go-rtmp

La implementación real de `relay.Publisher`, con lo que el spike de la §16 dejó verificado.

**Files:**
- Create: `internal/rtmpio/publisher.go`
- Test: `internal/rtmpio/publisher_test.go`
- Modify: `go.mod` (añade `github.com/yutopp/go-rtmp`)

**Interfaces:**
- Consumes: `relay.Publisher` (Task 4), `crypto.Secret`.
- Produces:
  ```go
  type PublisherConfig struct {
      URL       string        // rtmp://host[:puerto]/app  o  rtmps://...
      StreamKey crypto.Secret
      ChunkSize uint32        // 0 usa DefaultChunkSize
      Logger    *slog.Logger
  }
  const DefaultChunkSize = 4096
  func NewPublisher(cfg PublisherConfig) (*Publisher, error)  // valida la URL
  // *Publisher implementa relay.Publisher
  type target struct{ scheme, addr, app string }
  func parseTarget(rawURL string) (target, error)
  var ErrUnsupportedScheme = errors.New("esquema no soportado: usa rtmp:// o rtmps://")
  ```

- [ ] **Step 1: Añadir la dependencia**

```bash
go get github.com/yutopp/go-rtmp@v0.0.7
```

**No ejecutes `go mod tidy`.** Comprueba el resultado con `cat go.mod`: debe listar
`github.com/yutopp/go-rtmp v0.0.7` como directa, y `go-amf0`, `logrus`, `pkg/errors` y
`mapstructure` como indirectas. La directiva `go` debe seguir en `go 1.25.0`.

- [ ] **Step 2: Escribir el test que falla**

`internal/rtmpio/publisher_test.go`:

```go
package rtmpio

import (
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
)

func TestParseTargetRTMP(t *testing.T) {
	got, err := parseTarget("rtmp://a.rtmp.youtube.com/live2")
	if err != nil {
		t.Fatalf("parseTarget: %v", err)
	}
	// go-rtmp exige el literal "rtmp" para Dial (spec §16.1).
	if got.scheme != "rtmp" {
		t.Errorf("scheme = %q, quería \"rtmp\"", got.scheme)
	}
	if got.addr != "a.rtmp.youtube.com:1935" {
		t.Errorf("addr = %q, quería el puerto 1935 por defecto", got.addr)
	}
	if got.app != "live2" {
		t.Errorf("app = %q, quería \"live2\"", got.app)
	}
}

func TestParseTargetRTMPS(t *testing.T) {
	got, err := parseTarget("rtmps://live-api-s.facebook.com:443/rtmp/")
	if err != nil {
		t.Fatalf("parseTarget: %v", err)
	}
	// go-rtmp exige el literal "rtmps" para TLSDial (spec §16.1).
	if got.scheme != "rtmps" {
		t.Errorf("scheme = %q, quería \"rtmps\"", got.scheme)
	}
	if got.addr != "live-api-s.facebook.com:443" {
		t.Errorf("addr = %q", got.addr)
	}
	if got.app != "rtmp" {
		t.Errorf("app = %q, quería \"rtmp\" sin la barra final", got.app)
	}
}

func TestParseTargetRTMPSDefaultPort(t *testing.T) {
	got, err := parseTarget("rtmps://example.com/app")
	if err != nil {
		t.Fatalf("parseTarget: %v", err)
	}
	if got.addr != "example.com:443" {
		t.Errorf("addr = %q, quería el puerto 443 por defecto en rtmps", got.addr)
	}
}

func TestParseTargetNestedApp(t *testing.T) {
	got, err := parseTarget("rtmp://example.com/live/sub")
	if err != nil {
		t.Fatalf("parseTarget: %v", err)
	}
	if got.app != "live/sub" {
		t.Errorf("app = %q, quería \"live/sub\"", got.app)
	}
}

func TestParseTargetRejectsBadScheme(t *testing.T) {
	for _, raw := range []string{
		"http://example.com/live",
		"example.com/live",
		"",
	} {
		if _, err := parseTarget(raw); !errors.Is(err, ErrUnsupportedScheme) {
			t.Errorf("parseTarget(%q) = %v, quería ErrUnsupportedScheme", raw, err)
		}
	}
}

func TestParseTargetRejectsMissingHostOrApp(t *testing.T) {
	for _, raw := range []string{"rtmp:///live", "rtmp://example.com", "rtmp://example.com/"} {
		if _, err := parseTarget(raw); err == nil {
			t.Errorf("parseTarget(%q) = nil, quería error", raw)
		}
	}
}

func TestNewPublisherValidatesURL(t *testing.T) {
	if _, err := NewPublisher(PublisherConfig{
		URL:       "http://example.com/live",
		StreamKey: crypto.Secret("k"),
	}); !errors.Is(err, ErrUnsupportedScheme) {
		t.Error("NewPublisher debe rechazar una URL con esquema no soportado")
	}
}

// El error de NewPublisher no puede reproducir la stream key.
func TestNewPublisherErrorDoesNotLeakKey(t *testing.T) {
	_, err := NewPublisher(PublisherConfig{
		URL:       "http://example.com/live",
		StreamKey: crypto.Secret("clave-secreta-1234"),
	})
	if err == nil {
		t.Fatal("quería error")
	}
	if got := err.Error(); got != "" && contains(got, "clave-secreta") {
		t.Errorf("el error filtró la clave: %s", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// Close antes de Connect no debe entrar en pánico.
func TestPublisherCloseBeforeConnect(t *testing.T) {
	p, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://example.com/live",
		StreamKey: crypto.Secret("k"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close antes de Connect = %v, quería nil", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close es idempotente: segunda llamada = %v", err)
	}
}
```

- [ ] **Step 3: Ejecutar el test y verificar que falla**

Run: `go test ./internal/rtmpio/ -v`
Expected: FAIL — el paquete `rtmpio` no existe.

- [ ] **Step 4: Implementar `internal/rtmpio/publisher.go`**

```go
// Package rtmpio conecta el motor de relay con la red: el servidor de ingesta y el
// cliente que publica hacia las plataformas.
package rtmpio

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"

	"github.com/yutopp/go-rtmp"
	"github.com/yutopp/go-rtmp/message"

	"github.com/aprendomx/splitstream/internal/crypto"
)

// ErrUnsupportedScheme indica que la URL del destino no es rtmp:// ni rtmps://.
var ErrUnsupportedScheme = errors.New("esquema no soportado: usa rtmp:// o rtmps://")

// DefaultChunkSize es el tamaño de chunk que se negocia con el destino. Subirlo desde los
// 128 por defecto reduce el overhead de cabeceras; el spike lo verificó (spec §16.3).
const DefaultChunkSize = 4096

// Identificadores de chunk stream. Separar audio y video es la convención habitual.
const (
	csCommand = 3
	csAudio   = 4
	csVideo   = 5
)

// target es una URL de destino ya descompuesta en lo que necesita go-rtmp.
type target struct {
	scheme string // exactamente "rtmp" o "rtmps": go-rtmp compara con estos literales
	addr   string // host:puerto, con el puerto por defecto ya resuelto
	app    string // la app RTMP, sin barras al principio ni al final
}

// parseTarget descompone una URL de destino.
//
// El esquema decide si se usa Dial o TLSDial, y go-rtmp compara contra los literales
// "rtmp" y "rtmps" respectivamente: pasar el equivocado devuelve "Unknown protocol"
// (spec §16.1).
func parseTarget(rawURL string) (target, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return target{}, fmt.Errorf("%w: %s", ErrUnsupportedScheme, rawURL)
	}

	var defaultPort string
	switch u.Scheme {
	case "rtmp":
		defaultPort = "1935"
	case "rtmps":
		defaultPort = "443"
	default:
		return target{}, fmt.Errorf("%w: %q", ErrUnsupportedScheme, u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return target{}, errors.New("la URL del destino no tiene host")
	}
	port := u.Port()
	if port == "" {
		port = defaultPort
	}

	app := strings.Trim(u.Path, "/")
	if app == "" {
		return target{}, errors.New("la URL del destino no tiene app (la parte tras el host)")
	}

	return target{scheme: u.Scheme, addr: net.JoinHostPort(host, port), app: app}, nil
}

// PublisherConfig son los datos para construir un Publisher.
type PublisherConfig struct {
	URL       string
	StreamKey crypto.Secret
	ChunkSize uint32
	Logger    *slog.Logger
}

// Publisher publica hacia una plataforma. Implementa relay.Publisher.
//
// Lo usa una sola goroutine (la de su sink), así que no es seguro para uso concurrente,
// salvo Close, que sí puede llamarse desde otra.
type Publisher struct {
	tgt       target
	key       crypto.Secret
	chunkSize uint32
	log       *slog.Logger

	mu     sync.Mutex
	conn   *rtmp.ClientConn
	stream *rtmp.Stream
	closed bool
}

// NewPublisher valida la URL y construye el publisher sin conectar todavía.
func NewPublisher(cfg PublisherConfig) (*Publisher, error) {
	tgt, err := parseTarget(cfg.URL)
	if err != nil {
		return nil, err
	}
	size := cfg.ChunkSize
	if size == 0 {
		size = DefaultChunkSize
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Publisher{
		tgt:       tgt,
		key:       cfg.StreamKey,
		chunkSize: size,
		// La clave va como crypto.Secret y se loguea enmascarada.
		log: log.With("destino_url", cfg.URL, "clave", cfg.StreamKey),
	}, nil
}

// Connect abre la conexión y deja el stream listo para recibir media.
func (p *Publisher) Connect(ctx context.Context) error {
	var (
		conn *rtmp.ClientConn
		err  error
	)
	switch p.tgt.scheme {
	case "rtmps":
		conn, err = rtmp.TLSDial("rtmps", p.tgt.addr, &rtmp.ConnConfig{}, &tls.Config{
			ServerName: hostOf(p.tgt.addr),
			MinVersion: tls.VersionTLS12,
		})
	default:
		conn, err = rtmp.Dial("rtmp", p.tgt.addr, &rtmp.ConnConfig{})
	}
	if err != nil {
		return fmt.Errorf("conectar a %s: %w", p.tgt.addr, err)
	}

	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()

	tcURL := fmt.Sprintf("%s://%s/%s", p.tgt.scheme, p.tgt.addr, p.tgt.app)
	if err := conn.Connect(&message.NetConnectionConnect{
		Command: message.NetConnectionConnectCommand{
			App:      p.tgt.app,
			Type:     "nonprivate",
			FlashVer: "FMLE/3.0 (compatible; Splitstream)",
			TCURL:    tcURL,
		},
	}); err != nil {
		return fmt.Errorf("handshake connect con %s: %w", p.tgt.addr, err)
	}

	stream, err := conn.CreateStream(&message.NetConnectionCreateStream{}, p.chunkSize)
	if err != nil {
		return fmt.Errorf("createStream con %s: %w", p.tgt.addr, err)
	}

	// Algunas plataformas exigen releaseStream y FCPublish antes de publish. go-rtmp no
	// tiene helper para ellos, pero Stream.Write acepta un CommandMessage (spec §16).
	// Un rechazo aquí no es fatal: los destinos que no los esperan simplemente los ignoran.
	for _, cmd := range []string{"releaseStream", "FCPublish"} {
		if err := p.writeCommand(stream, cmd); err != nil {
			p.log.Debug("el destino no aceptó el comando previo", "comando", cmd, "err", err)
		}
	}

	if err := stream.Publish(&message.NetStreamPublish{
		PublishingName: p.key.Reveal(),
		PublishingType: "live",
	}); err != nil {
		return fmt.Errorf("publish en %s: %w", p.tgt.addr, err)
	}

	if err := stream.WriteSetChunkSize(p.chunkSize); err != nil {
		p.log.Debug("no se pudo fijar el chunk size", "err", err)
	}

	p.mu.Lock()
	p.stream = stream
	p.mu.Unlock()

	p.log.Info("publicando en el destino", "app", p.tgt.app)
	return nil
}

// writeCommand manda un comando AMF0 con objeto nulo y el nombre del stream, que es la
// forma de releaseStream y FCPublish.
func (p *Publisher) writeCommand(stream *rtmp.Stream, name string) error {
	buf := new(bytes.Buffer)
	enc := message.NewAMFEncoder(buf, message.EncodingTypeAMF0)
	if err := enc.Encode(nil); err != nil {
		return err
	}
	if err := enc.Encode(p.key.Reveal()); err != nil {
		return err
	}
	return stream.Write(csCommand, 0, &message.CommandMessage{
		CommandName:   name,
		TransactionID: 0,
		Encoding:      message.EncodingTypeAMF0,
		Body:          buf,
	})
}

// WriteMeta envía el onMetaData envuelto en @setDataFrame, sin el cual las plataformas lo
// ignoran y algunas rechazan el stream (spec §3.5).
func (p *Publisher) WriteMeta(ts uint32, payload []byte) error {
	stream, err := p.liveStream()
	if err != nil {
		return err
	}
	return stream.Write(csAudio, ts, &message.DataMessage{
		Name:     "@setDataFrame",
		Encoding: message.EncodingTypeAMF0,
		Body:     bytes.NewReader(payload),
	})
}

// WriteAudio envía un tag de audio tal cual.
func (p *Publisher) WriteAudio(ts uint32, payload []byte) error {
	stream, err := p.liveStream()
	if err != nil {
		return err
	}
	return stream.Write(csAudio, ts, &message.AudioMessage{Payload: bytes.NewReader(payload)})
}

// WriteVideo envía un tag de video tal cual.
func (p *Publisher) WriteVideo(ts uint32, payload []byte) error {
	stream, err := p.liveStream()
	if err != nil {
		return err
	}
	return stream.Write(csVideo, ts, &message.VideoMessage{Payload: bytes.NewReader(payload)})
}

func (p *Publisher) liveStream() (*rtmp.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("el publisher está cerrado")
	}
	if p.stream == nil {
		return nil, errors.New("el publisher no está conectado")
	}
	return p.stream, nil
}

// Close cierra el stream y la conexión. Es idempotente y tolera que Connect nunca se
// haya llamado o haya fallado a medias.
func (p *Publisher) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	conn, stream := p.conn, p.stream
	p.conn, p.stream = nil, nil
	p.mu.Unlock()

	if conn != nil && stream != nil {
		if err := conn.DeleteStream(&message.NetStreamDeleteStream{StreamID: stream.StreamID()}); err != nil {
			p.log.Debug("deleteStream falló al cerrar", "err", err)
		}
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
```

- [ ] **Step 5: Comprobar que satisface la interfaz**

Añade al final de `internal/rtmpio/publisher.go`:

```go
// Comprobación en tiempo de compilación de que *Publisher cumple el contrato del relay.
var _ relay.Publisher = (*Publisher)(nil)
```

Y añade `"github.com/aprendomx/splitstream/internal/relay"` a los imports.

- [ ] **Step 6: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/rtmpio/ -race -count=1 -v && go vet ./...`
Expected: PASS en los 9 tests. Si `var _ relay.Publisher` no compila, la firma de algún
método no coincide con la interfaz de la Task 4 — corrige el método, no la interfaz.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/rtmpio/
git commit -m "feat(rtmpio): publisher RTMP y RTMPS sobre el cliente de go-rtmp"
```

---

### Task 7: Servidor de ingesta

El servidor que recibe a OBS, valida su clave, rechaza códecs que no se pueden retransmitir,
y convierte cada mensaje en un `relay.Message`.

**Files:**
- Create: `internal/rtmpio/ingest.go`
- Test: `internal/rtmpio/ingest_test.go`

**Interfaces:**
- Consumes: `relay.Message`, `relay.Kind`, `flv.InspectVideo`, `flv.InspectAudio`,
  `crypto.Secret`.
- Produces:
  ```go
  type IngestHandler interface {
      OnPublishStart(app, streamKey string) error  // error rechaza la conexión
      OnMessage(msg *relay.Message)
      OnPublishEnd()
  }
  type IngestConfig struct {
      Addr    string
      Handler IngestHandler
      Logger  *slog.Logger
  }
  func NewIngest(cfg IngestConfig) *Ingest
  func (i *Ingest) Serve(ln net.Listener) error
  func (i *Ingest) ListenAndServe() error
  func (i *Ingest) Close() error
  var ErrUnsupportedCodec = errors.New("códec no soportado: configura H.264 + AAC en OBS")
  var ErrBadStreamKey = errors.New("app o clave de ingesta incorrectas")
  ```

- [ ] **Step 1: Escribir el test que falla**

`internal/rtmpio/ingest_test.go`:

```go
package rtmpio

import (
	"errors"
	"sync"
	"testing"

	"github.com/aprendomx/splitstream/internal/relay"
)

// recorder captura lo que el servidor de ingesta entrega.
type recorder struct {
	mu       sync.Mutex
	msgs     []*relay.Message
	starts   int
	ends     int
	startErr error
}

func (r *recorder) OnPublishStart(app, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	return r.startErr
}

func (r *recorder) OnMessage(msg *relay.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
}

func (r *recorder) OnPublishEnd() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends++
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

func TestClassifyVideoKeyframe(t *testing.T) {
	msg, err := classifyVideo(100, []byte{0x17, 0x01, 0, 0, 0})
	if err != nil {
		t.Fatalf("classifyVideo: %v", err)
	}
	if msg.Kind != relay.KindVideo {
		t.Errorf("Kind = %v", msg.Kind)
	}
	if !msg.IsKeyframe {
		t.Error("IsKeyframe = false")
	}
	if msg.IsSeqHeader {
		t.Error("IsSeqHeader = true")
	}
	if msg.Timestamp != 100 {
		t.Errorf("Timestamp = %d", msg.Timestamp)
	}
}

func TestClassifyVideoSequenceHeader(t *testing.T) {
	msg, err := classifyVideo(0, []byte{0x17, 0x00, 0, 0, 0})
	if err != nil {
		t.Fatalf("classifyVideo: %v", err)
	}
	if !msg.IsSeqHeader {
		t.Error("IsSeqHeader = false, quería true")
	}
}

// Enhanced-RTMP (HEVC/AV1) se rechaza: no se puede transcodificar y Twitch no lo acepta.
func TestClassifyVideoRejectsEnhancedRTMP(t *testing.T) {
	_, err := classifyVideo(0, []byte{0x90, 'h', 'v', 'c', '1'})
	if !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("classifyVideo con enhanced-RTMP = %v, quería ErrUnsupportedCodec", err)
	}
}

func TestClassifyVideoRejectsNonAVC(t *testing.T) {
	// codecID 2 = Sorenson H.263
	if _, err := classifyVideo(0, []byte{0x12, 0x00}); !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("classifyVideo con H.263 = %v, quería ErrUnsupportedCodec", err)
	}
}

func TestClassifyAudioAAC(t *testing.T) {
	msg, err := classifyAudio(50, []byte{0xAF, 0x01, 0x21})
	if err != nil {
		t.Fatalf("classifyAudio: %v", err)
	}
	if msg.Kind != relay.KindAudio {
		t.Errorf("Kind = %v", msg.Kind)
	}
	if msg.IsSeqHeader {
		t.Error("un frame raw no es sequence header")
	}
	if msg.Timestamp != 50 {
		t.Errorf("Timestamp = %d", msg.Timestamp)
	}
}

func TestClassifyAudioSequenceHeader(t *testing.T) {
	msg, err := classifyAudio(0, []byte{0xAF, 0x00, 0x12, 0x10})
	if err != nil {
		t.Fatalf("classifyAudio: %v", err)
	}
	if !msg.IsSeqHeader {
		t.Error("IsSeqHeader = false, quería true")
	}
}

func TestClassifyAudioRejectsNonAAC(t *testing.T) {
	// soundFormat 2 = MP3
	if _, err := classifyAudio(0, []byte{0x2F, 0x00}); !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("classifyAudio con MP3 = %v, quería ErrUnsupportedCodec", err)
	}
}

// El payload que llega al relay debe ser una copia: go-rtmp reutiliza sus buffers.
func TestClassifyCopiesPayload(t *testing.T) {
	src := []byte{0x17, 0x01, 0xAA, 0xBB}
	msg, err := classifyVideo(0, src)
	if err != nil {
		t.Fatalf("classifyVideo: %v", err)
	}
	src[2] = 0xFF
	if msg.Payload[2] == 0xFF {
		t.Error("el mensaje comparte memoria con el buffer de origen: hay que copiar")
	}
}
```

- [ ] **Step 2: Ejecutar el test y verificar que falla**

Run: `go test ./internal/rtmpio/ -run 'Classify' -v`
Expected: FAIL con `undefined: classifyVideo`.

- [ ] **Step 3: Implementar `internal/rtmpio/ingest.go`**

```go
package rtmpio

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"

	"github.com/aprendomx/splitstream/internal/flv"
	"github.com/aprendomx/splitstream/internal/relay"
)

// ErrUnsupportedCodec indica que el publisher manda algo que no se puede retransmitir.
//
// Un relay puro no transcodifica, y HEVC o AV1 por enhanced-RTMP no los acepta Twitch,
// así que el fan-out sería imposible aunque se parsearan (spec §3.6).
var ErrUnsupportedCodec = errors.New("códec no soportado: configura H.264 + AAC en OBS")

// ErrBadStreamKey indica que la app o la clave del publisher no coinciden.
var ErrBadStreamKey = errors.New("app o clave de ingesta incorrectas")

// IngestHandler recibe lo que ocurre en la ingesta. Sus métodos se llaman desde la
// goroutine de la conexión, en orden.
type IngestHandler interface {
	// OnPublishStart valida al publisher. Devolver error rechaza la conexión.
	OnPublishStart(app, streamKey string) error
	// OnMessage entrega un mensaje de media ya clasificado.
	OnMessage(msg *relay.Message)
	// OnPublishEnd avisa de que el publisher se fue.
	OnPublishEnd()
}

// IngestConfig son los datos para construir el servidor de ingesta.
type IngestConfig struct {
	Addr    string
	Handler IngestHandler
	Logger  *slog.Logger
}

// Ingest es el servidor RTMP que recibe a OBS.
type Ingest struct {
	addr    string
	handler IngestHandler
	log     *slog.Logger

	mu  sync.Mutex
	srv *rtmp.Server
	ln  net.Listener
}

// NewIngest construye el servidor sin escuchar todavía.
func NewIngest(cfg IngestConfig) *Ingest {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Ingest{addr: cfg.Addr, handler: cfg.Handler, log: log}
}

// ListenAndServe escucha en la dirección configurada y atiende hasta que se cierre.
func (i *Ingest) ListenAndServe() error {
	ln, err := net.Listen("tcp", i.addr)
	if err != nil {
		return fmt.Errorf("escuchar RTMP en %s: %w", i.addr, err)
	}
	return i.Serve(ln)
}

// Serve atiende sobre un listener ya abierto. Útil para tests con puerto efímero.
func (i *Ingest) Serve(ln net.Listener) error {
	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			return conn, &rtmp.ConnConfig{
				Handler: &ingestConn{handler: i.handler, log: i.log},
			}
		},
	})

	i.mu.Lock()
	i.srv, i.ln = srv, ln
	i.mu.Unlock()

	i.log.Info("ingesta RTMP escuchando", "addr", ln.Addr().String())
	return srv.Serve(ln)
}

// Close deja de aceptar conexiones y cierra las abiertas.
func (i *Ingest) Close() error {
	i.mu.Lock()
	srv := i.srv
	i.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Close()
}

// ingestConn atiende una conexión de publisher.
type ingestConn struct {
	rtmp.DefaultHandler
	handler IngestHandler
	log     *slog.Logger

	app       string
	publishing bool
}

func (c *ingestConn) OnConnect(timestamp uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	c.app = cmd.Command.App
	return nil
}

func (c *ingestConn) OnPublish(ctx *rtmp.StreamContext, timestamp uint32, cmd *rtmpmsg.NetStreamPublish) error {
	// El error no revela cuál de las dos partes falló, para no ayudar a adivinar.
	if err := c.handler.OnPublishStart(c.app, cmd.PublishingName); err != nil {
		c.log.Warn("publisher rechazado", "app", c.app, "err", err)
		return err
	}
	c.publishing = true
	c.log.Info("publisher aceptado", "app", c.app)
	return nil
}

func (c *ingestConn) OnAudio(timestamp uint32, payload io.Reader) error {
	data, err := io.ReadAll(payload)
	if err != nil {
		return fmt.Errorf("leer tag de audio: %w", err)
	}
	msg, err := classifyAudio(timestamp, data)
	if err != nil {
		return err
	}
	c.handler.OnMessage(msg)
	return nil
}

func (c *ingestConn) OnVideo(timestamp uint32, payload io.Reader) error {
	data, err := io.ReadAll(payload)
	if err != nil {
		return fmt.Errorf("leer tag de video: %w", err)
	}
	msg, err := classifyVideo(timestamp, data)
	if err != nil {
		return err
	}
	c.handler.OnMessage(msg)
	return nil
}

func (c *ingestConn) OnSetDataFrame(timestamp uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	payload := make([]byte, len(data.Payload))
	copy(payload, data.Payload)
	c.handler.OnMessage(&relay.Message{
		Kind:      relay.KindMeta,
		Timestamp: timestamp,
		Payload:   payload,
	})
	return nil
}

func (c *ingestConn) OnClose() {
	if c.publishing {
		c.publishing = false
		c.handler.OnPublishEnd()
	}
	c.log.Info("publisher desconectado", "app", c.app)
}

// classifyVideo convierte un tag de video en un relay.Message, rechazando lo que no se
// puede retransmitir. El payload se copia porque go-rtmp reutiliza sus buffers.
func classifyVideo(timestamp uint32, data []byte) (*relay.Message, error) {
	info, err := flv.InspectVideo(data)
	if err != nil {
		return nil, err
	}
	if info.IsEnhanced {
		return nil, fmt.Errorf("%w (enhanced-RTMP: HEVC o AV1)", ErrUnsupportedCodec)
	}
	if info.CodecID != flv.CodecIDAVC {
		return nil, fmt.Errorf("%w (codecID de video %d, se esperaba %d = H.264)",
			ErrUnsupportedCodec, info.CodecID, flv.CodecIDAVC)
	}

	payload := make([]byte, len(data))
	copy(payload, data)
	return &relay.Message{
		Kind:        relay.KindVideo,
		Timestamp:   timestamp,
		Payload:     payload,
		IsKeyframe:  info.IsKeyframe,
		IsSeqHeader: info.IsSequenceHeader,
	}, nil
}

// classifyAudio convierte un tag de audio en un relay.Message.
func classifyAudio(timestamp uint32, data []byte) (*relay.Message, error) {
	info, err := flv.InspectAudio(data)
	if err != nil {
		return nil, err
	}
	if info.SoundFormat != flv.SoundFormatAAC {
		return nil, fmt.Errorf("%w (soundFormat de audio %d, se esperaba %d = AAC)",
			ErrUnsupportedCodec, info.SoundFormat, flv.SoundFormatAAC)
	}

	payload := make([]byte, len(data))
	copy(payload, data)
	return &relay.Message{
		Kind:        relay.KindAudio,
		Timestamp:   timestamp,
		Payload:     payload,
		IsSeqHeader: info.IsSequenceHeader,
	}, nil
}
```

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/rtmpio/ -race -count=1 -v`
Expected: PASS en los 17 tests del paquete.

**Esta API ya está verificada contra go-rtmp v0.0.7**, así que no deberías necesitar
adaptarla. Confirmado leyendo el código de la librería:

- `rtmp.DefaultHandler` existe (`default_handler.go`) e implementa `Handler` entera, así
  que embeberlo basta para no escribir los métodos que no usamos.
- Las firmas coinciden una a una con `handler.go`: `OnConnect(uint32, *message.NetConnectionConnect) error`,
  `OnPublish(*StreamContext, uint32, *message.NetStreamPublish) error`,
  `OnAudio(uint32, io.Reader) error`, `OnVideo(uint32, io.Reader) error`,
  `OnSetDataFrame(uint32, *message.NetStreamSetDataFrame) error`, `OnClose()`.
- `message.NetStreamSetDataFrame` tiene el campo `Payload []byte`, que trae el AMF crudo
  posterior al nombre del comando — es decir, `"onMetaData"` más el array de datos. Por eso
  reenviarlo con `Name: "@setDataFrame"` en el Publisher produce exactamente lo que espera
  la plataforma.
- `ServerConfig.OnConnect` tiene la firma `func(net.Conn) (io.ReadWriteCloser, *ConnConfig)`
  y `ConnConfig.Handler` es de tipo `Handler`.

Si aun así algo no compila, adapta la firma del método afectado — pero **no cambies la
interfaz `IngestHandler`**, que es el contrato con la Task 8.

- [ ] **Step 5: Commit**

```bash
git add internal/rtmpio/
git commit -m "feat(rtmpio): servidor de ingesta con validación y rechazo de enhanced-RTMP"
```

---

### Task 8: Wiring y test de integración de punta a punta

Atar todo en el binario, y demostrar con ffmpeg y mediamtx que un stream entra y sale.

**Files:**
- Create: `internal/relay/engine.go`
- Create: `deploy/test-compose.yml`
- Create: `test/integration/relay_test.go`
- Create: `test/integration/doc.go`
- Modify: `cmd/splitstream/main.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: todo lo anterior.
- Produces:
  ```go
  // en internal/relay
  type Engine struct{ ... }
  type EngineConfig struct {
      Hub    *Hub
      Store  EngineStore
      Logger *slog.Logger
  }
  type EngineStore interface {
      StartSession(ctx context.Context) (int64, error)
      FinishSession(ctx context.Context, id int64, width, height, bitrateBPS int) error
      LogEvent(ctx context.Context, e EngineEvent) error
  }
  type EngineEvent struct {
      SessionID     *int64
      DestinationID *int64
      Level         string
      Kind          string
      Message       string
  }
  func NewEngine(cfg EngineConfig) *Engine
  func (e *Engine) OnPublishStart(app, streamKey string) error
  func (e *Engine) OnMessage(msg *Message)
  func (e *Engine) OnPublishEnd()
  func (e *Engine) SetValidator(fn func(app, key string) error)
  func (e *Engine) SessionID() int64
  ```
  `*Engine` satisface `rtmpio.IngestHandler` porque los tres métodos coinciden. La
  indirección `EngineStore` existe para que `internal/relay` no importe `internal/store`
  (frontera del spec §4).

- [ ] **Step 1: Escribir el test unitario del engine**

`internal/relay/engine_test.go`:

```go
package relay

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeStore struct {
	mu      sync.Mutex
	started int
	ended   int
	events  []EngineEvent
	nextID  int64
}

func (f *fakeStore) StartSession(ctx context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	f.nextID++
	return f.nextID, nil
}

func (f *fakeStore) FinishSession(ctx context.Context, id int64, w, h, b int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended++
	return nil
}

func (f *fakeStore) LogEvent(ctx context.Context, e EngineEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func TestEngineRejectsBadKey(t *testing.T) {
	st := &fakeStore{}
	e := NewEngine(EngineConfig{Hub: NewHub(nil), Store: st})
	bad := errors.New("clave incorrecta")
	e.SetValidator(func(app, key string) error {
		if app == "live" && key == "buena" {
			return nil
		}
		return bad
	})

	if err := e.OnPublishStart("live", "mala"); !errors.Is(err, bad) {
		t.Fatalf("OnPublishStart con clave mala = %v, quería el error del validador", err)
	}
	st.mu.Lock()
	started := st.started
	st.mu.Unlock()
	if started != 0 {
		t.Error("una clave rechazada no debe abrir sesión")
	}
}

func TestEngineOpensAndClosesSession(t *testing.T) {
	st := &fakeStore{}
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: st})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}
	if e.SessionID() == 0 {
		t.Error("SessionID = 0 tras aceptar al publisher")
	}
	e.OnPublishEnd()

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.started != 1 || st.ended != 1 {
		t.Errorf("sesiones: abiertas=%d cerradas=%d, quería 1 y 1", st.started, st.ended)
	}
}

func TestEngineForwardsMessagesToHub(t *testing.T) {
	st := &fakeStore{}
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: st})
	e.SetValidator(func(string, string) error { return nil })
	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}

	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), h.Preamble())
	h.Add(s)
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	e.OnMessage(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	e.OnMessage(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	e.OnMessage(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})
	e.OnMessage(videoKey(1000))

	waitFor(t, func() bool { return len(pub.snapshot()) >= 4 }, "el mensaje llegó al sink")
}

// El preámbulo de una transmisión no debe sobrevivir a la siguiente.
func TestEngineResetsPreambleBetweenSessions(t *testing.T) {
	st := &fakeStore{}
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: st})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}
	e.OnMessage(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	e.OnPublishEnd()

	meta, _, _ := h.Preamble().Snapshot()
	if meta != nil {
		t.Error("el preámbulo debe vaciarse al terminar la sesión")
	}
}
```

- [ ] **Step 2: Ejecutar el test y verificar que falla**

Run: `go test ./internal/relay/ -run Engine -v`
Expected: FAIL con `undefined: NewEngine`.

- [ ] **Step 3: Implementar `internal/relay/engine.go`**

```go
package relay

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// EngineStore es lo que el motor necesita de la persistencia. Es una interfaz para que
// internal/relay no importe internal/store y siga siendo testeable en memoria (spec §4).
type EngineStore interface {
	StartSession(ctx context.Context) (int64, error)
	FinishSession(ctx context.Context, id int64, width, height, bitrateBPS int) error
	LogEvent(ctx context.Context, e EngineEvent) error
}

// EngineEvent es una entrada del log persistente, en los términos del motor.
type EngineEvent struct {
	SessionID     *int64
	DestinationID *int64
	Level         string
	Kind          string
	Message       string
}

// EngineConfig son los datos para construir el motor.
type EngineConfig struct {
	Hub    *Hub
	Store  EngineStore
	Logger *slog.Logger
}

// Engine une la ingesta con el hub: valida al publisher, abre y cierra la sesión, y
// reparte los mensajes. Satisface rtmpio.IngestHandler.
type Engine struct {
	hub   *Hub
	store EngineStore
	log   *slog.Logger

	mu        sync.Mutex
	validate  func(app, key string) error
	sessionID int64
}

// NewEngine construye el motor. Hasta que se llame a SetValidator, rechaza a todo
// publisher: es más seguro que aceptar por defecto.
func NewEngine(cfg EngineConfig) *Engine {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		hub:      cfg.Hub,
		store:    cfg.Store,
		log:      log,
		validate: func(string, string) error { return errors.New("ingesta sin configurar") },
	}
}

// SetValidator fija la función que decide si un publisher puede publicar.
func (e *Engine) SetValidator(fn func(app, key string) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.validate = fn
}

// SessionID devuelve el id de la sesión en curso, o 0 si no hay ninguna.
func (e *Engine) SessionID() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessionID
}

// OnPublishStart valida al publisher y abre la sesión.
func (e *Engine) OnPublishStart(app, streamKey string) error {
	e.mu.Lock()
	validate := e.validate
	e.mu.Unlock()

	if err := validate(app, streamKey); err != nil {
		return err
	}

	ctx := context.Background()
	id, err := e.store.StartSession(ctx)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.sessionID = id
	e.mu.Unlock()

	e.logEvent(ctx, &id, nil, "info", "publisher_connected", "el publisher conectó")
	e.log.Info("sesión iniciada", "sesion_id", id, "app", app)
	return nil
}

// OnMessage reparte un mensaje de media a los destinos.
func (e *Engine) OnMessage(msg *Message) { e.hub.Publish(msg) }

// OnPublishEnd cierra la sesión y olvida el preámbulo: los sequence headers de esta
// transmisión no valen para la siguiente.
func (e *Engine) OnPublishEnd() {
	e.mu.Lock()
	id := e.sessionID
	e.sessionID = 0
	e.mu.Unlock()

	if id == 0 {
		return
	}

	ctx := context.Background()
	// La resolución y el bitrate medidos llegan en la fase 3; aquí se cierra con ceros.
	if err := e.store.FinishSession(ctx, id, 0, 0, 0); err != nil {
		e.log.Error("no se pudo cerrar la sesión", "sesion_id", id, "err", err)
	}
	e.logEvent(ctx, &id, nil, "info", "publisher_disconnected", "el publisher se desconectó")
	e.hub.Preamble().Reset()
	e.log.Info("sesión terminada", "sesion_id", id)
}

func (e *Engine) logEvent(ctx context.Context, sessionID, destID *int64, level, kind, msg string) {
	if err := e.store.LogEvent(ctx, EngineEvent{
		SessionID:     sessionID,
		DestinationID: destID,
		Level:         level,
		Kind:          kind,
		Message:       msg,
	}); err != nil {
		e.log.Error("no se pudo registrar el evento", "kind", kind, "err", err)
	}
}
```

- [ ] **Step 4: Ejecutar los tests del relay**

Run: `go test ./internal/relay/ -race -count=1`
Expected: PASS en los 23 tests.

- [ ] **Step 5: Conectar todo en `cmd/splitstream/main.go`**

Sustituye el cuerpo de `run` desde la línea de `settings, err := db.Settings(ctx)` hasta el
`return nil` final por esto, y añade los imports `sync`, `time`,
`github.com/aprendomx/splitstream/internal/relay` y
`github.com/aprendomx/splitstream/internal/rtmpio`:

```go
	settings, err := db.Settings(ctx)
	if err != nil {
		return err
	}

	hub := relay.NewHub(logger)
	engine := relay.NewEngine(relay.EngineConfig{
		Hub:    hub,
		Store:  storeAdapter{db: db},
		Logger: logger,
	})

	// La clave se compara descifrada y en tiempo constante no hace falta aquí: es un
	// servicio de un solo usuario y el rate limit vive en la API, no en RTMP.
	engine.SetValidator(func(app, key string) error {
		if app != settings.IngestApp {
			return rtmpio.ErrBadStreamKey
		}
		real, err := db.RevealIngestKey(ctx, cipher)
		if err != nil {
			return err
		}
		if key != real.Reveal() {
			return rtmpio.ErrBadStreamKey
		}
		return nil
	})

	// Fase 2: un solo destino, el primero habilitado. La fase 3 los gestiona todos.
	dests, err := db.ListDestinations(ctx)
	if err != nil {
		return err
	}
	var started int
	for _, d := range dests {
		if !d.Enabled {
			continue
		}
		key, err := db.RevealDestinationKey(ctx, cipher, d.ID)
		if err != nil {
			logger.Error("no se pudo leer la clave del destino", "destino", d.Name, "err", err)
			continue
		}
		pub, err := rtmpio.NewPublisher(rtmpio.PublisherConfig{
			URL:       d.RTMPURL,
			StreamKey: key,
			Logger:    logger,
		})
		if err != nil {
			logger.Error("destino mal configurado", "destino", d.Name, "err", err)
			continue
		}
		sink := relay.NewSink(relay.SinkConfig{ID: d.ID, Name: d.Name, Pub: pub, Logger: logger})
		sink.Start(ctx, hub.Preamble())
		hub.Add(sink)
		started++
		break // fase 2: uno solo
	}
	logger.Info("destinos activos", "n", started)

	ingest := rtmpio.NewIngest(rtmpio.IngestConfig{
		Addr:    cfg.RTMPAddr,
		Handler: engine,
		Logger:  logger,
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := ingest.ListenAndServe(); err != nil {
			// Al cerrar, Serve devuelve un error de listener cerrado: no es un fallo.
			logger.Info("la ingesta dejó de atender", "err", err)
		}
	}()

	logger.Info("splitstream arrancado", "config", cfg,
		"ingest_app", settings.IngestApp, "ingest_key", settings.IngestKeyMask)

	<-ctx.Done()
	logger.Info("apagando")

	if err := ingest.Close(); err != nil {
		logger.Error("cerrar la ingesta", "err", err)
	}
	hub.Close()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		logger.Warn("la ingesta no cerró en 3s; se sigue adelante")
	}
	return nil
}

// storeAdapter traduce el store al contrato EngineStore, para que internal/relay no
// tenga que importar internal/store.
type storeAdapter struct{ db *store.DB }

func (a storeAdapter) StartSession(ctx context.Context) (int64, error) {
	return a.db.StartSession(ctx)
}

func (a storeAdapter) FinishSession(ctx context.Context, id int64, w, h, b int) error {
	return a.db.FinishSession(ctx, id, w, h, b)
}

func (a storeAdapter) LogEvent(ctx context.Context, e relay.EngineEvent) error {
	_, err := a.db.LogEvent(ctx, store.Event{
		SessionID:     e.SessionID,
		DestinationID: e.DestinationID,
		Level:         store.Level(e.Level),
		Kind:          e.Kind,
		Message:       e.Message,
	})
	return err
}
```

- [ ] **Step 6: Crear `deploy/test-compose.yml`**

```yaml
# Sinks falsos para el test de integración. Se levanta con:
#   docker compose -f deploy/test-compose.yml up -d
services:
  sink-a:
    image: bluenviron/mediamtx:latest
    container_name: splitstream-test-sink-a
    ports:
      - "19351:1935"
      - "18554:8554"
    environment:
      MTX_LOGLEVEL: info
  sink-b:
    image: bluenviron/mediamtx:latest
    container_name: splitstream-test-sink-b
    ports:
      - "19352:1935"
      - "18555:8554"
    environment:
      MTX_LOGLEVEL: info
```

`sink-b` no lo usa la fase 2, pero la fase 3 lo necesita para el fan-out y el test de
reconexión; dejarlo aquí evita tocar este archivo después.

- [ ] **Step 7: Escribir el test de integración**

`test/integration/doc.go`:

```go
// Package integration contiene los tests de punta a punta, que necesitan Docker y ffmpeg.
// Se activan con la etiqueta de build `integration`:
//
//	go test -tags integration ./test/integration/ -v
package integration
```

`test/integration/relay_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/store"
)

const sinkA = "rtmp://localhost:19351/live"

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("hace falta %s en el PATH", name)
	}
}

func requireSink(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Skipf("no hay sink en %s: levanta deploy/test-compose.yml", addr)
	}
	conn.Close()
}

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 1)
	}
	c, err := crypto.NewCipher(k)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

// freePort reserva un puerto efímero para la ingesta del test.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("puerto libre: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// TestRelayEndToEnd publica un patrón de prueba con ffmpeg contra la ingesta y comprueba
// que el sink recibe un stream con video y audio decodificables.
func TestRelayEndToEnd(t *testing.T) {
	requireTool(t, "ffmpeg")
	requireTool(t, "ffprobe")
	requireSink(t, "localhost:19351")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	cipher := testCipher(t)
	if err := db.Bootstrap(ctx, cipher); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	ingestKey, err := db.RevealIngestKey(ctx, cipher)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}

	streamName := fmt.Sprintf("e2e%d", time.Now().UnixNano())

	hub := relay.NewHub(nil)
	defer hub.Close()

	pub, err := rtmpio.NewPublisher(rtmpio.PublisherConfig{
		URL:       sinkA,
		StreamKey: crypto.Secret(streamName),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	sink := relay.NewSink(relay.SinkConfig{ID: 1, Name: "sink-a", Pub: pub})
	sink.Start(ctx, hub.Preamble())
	hub.Add(sink)

	engine := relay.NewEngine(relay.EngineConfig{Hub: hub, Store: adapter{db}})
	engine.SetValidator(func(app, key string) error {
		if app == "live" && key == ingestKey.Reveal() {
			return nil
		}
		return rtmpio.ErrBadStreamKey
	})

	addr := freePort(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ingest := rtmpio.NewIngest(rtmpio.IngestConfig{Addr: addr, Handler: engine})
	go ingest.Serve(ln)
	defer ingest.Close()
	time.Sleep(300 * time.Millisecond)

	// OBS simulado: patrón de prueba con audio, en tiempo real.
	pubURL := fmt.Sprintf("rtmp://%s/live/%s", addr, ingestKey.Reveal())
	ff := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-re", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", "12", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-g", "30", "-b:v", "800k", "-c:a", "aac", "-b:a", "128k", "-ar", "44100",
		"-f", "flv", pubURL)
	ffOut, err := ff.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe: %v", err)
	}
	if err := ff.Start(); err != nil {
		t.Fatalf("arrancar ffmpeg: %v", err)
	}
	defer ff.Process.Kill()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ffOut.Read(buf)
			if n > 0 {
				t.Logf("ffmpeg: %s", strings.TrimSpace(string(buf[:n])))
			}
			if err != nil {
				return
			}
		}
	}()

	// Dar tiempo a que la sesión arranque y el sink mande el preámbulo.
	time.Sleep(4 * time.Second)

	if got := sink.State(); got != relay.StateLive {
		t.Fatalf("el sink está en %v, quería live. Último error: %v", got, sink.LastError())
	}

	// Leer del sink lo que salió del relay.
	out := filepath.Join(t.TempDir(), "out.flv")
	rec := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-rw_timeout", "15000000",
		"-i", fmt.Sprintf("%s/%s", sinkA, streamName),
		"-t", "4", "-c", "copy", "-y", out)
	if b, err := rec.CombinedOutput(); err != nil {
		t.Fatalf("no se pudo leer del sink: %v\n%s", err, b)
	}

	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 {
		t.Fatalf("el archivo grabado está vacío: %v", err)
	}

	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name,codec_type,width,height",
		"-of", "default=noprint_wrappers=1", out)
	b, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v\n%s", err, b)
	}
	got := string(b)
	t.Logf("ffprobe del stream retransmitido:\n%s", got)

	for _, want := range []string{"codec_name=h264", "codec_name=aac", "width=640", "height=360"} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en la salida del sink:\n%s", want, got)
		}
	}

	if d := sink.Dropped(); d > 0 {
		t.Logf("aviso: el sink descartó %d mensajes", d)
	}
}

// adapter conecta el store real con el contrato EngineStore.
type adapter struct{ db *store.DB }

func (a adapter) StartSession(ctx context.Context) (int64, error) { return a.db.StartSession(ctx) }
func (a adapter) FinishSession(ctx context.Context, id int64, w, h, b int) error {
	return a.db.FinishSession(ctx, id, w, h, b)
}
func (a adapter) LogEvent(ctx context.Context, e relay.EngineEvent) error {
	_, err := a.db.LogEvent(ctx, store.Event{
		SessionID:     e.SessionID,
		DestinationID: e.DestinationID,
		Level:         store.Level(e.Level),
		Kind:          e.Kind,
		Message:       e.Message,
	})
	return err
}
```

- [ ] **Step 8: Añadir los targets al Makefile**

Sustituye el bloque `.PHONY` y añade los targets nuevos:

```makefile
.PHONY: build test test-integration sinks-up sinks-down vet tidy run clean
```

Y al final del archivo:

```makefile
# Levanta los mediamtx que usan los tests de integración.
sinks-up:
	docker compose -f deploy/test-compose.yml up -d

sinks-down:
	docker compose -f deploy/test-compose.yml down

# Requiere sinks-up, ffmpeg y ffprobe.
test-integration:
	go test -tags integration ./test/integration/ -v -count=1 -timeout 5m
```

- [ ] **Step 9: Ejecutar el test de integración**

```bash
make sinks-up
sleep 5
make test-integration
```

Expected: `TestRelayEndToEnd` PASA, y en el log del test aparece la salida de `ffprobe`
con `codec_name=h264`, `codec_name=aac`, `width=640` y `height=360`.

Si falla con "el sink está en error", mira `sink.LastError()` en el mensaje: casi siempre
es que `mediamtx` no acepta el `publish` con ese nombre, o que el preámbulo no llegó.
Comprueba con `docker logs splitstream-test-sink-a` si el path se creó y si reconoció
"2 tracks".

Cuando termines: `make sinks-down`.

- [ ] **Step 10: Verificación completa y commit**

```bash
go vet ./...
go test ./... -race -count=1
CGO_ENABLED=0 go build -o splitstream ./cmd/splitstream
```
Expected: todo verde y el binario compila.

```bash
git add internal/relay/engine.go internal/relay/engine_test.go cmd/splitstream/main.go \
        deploy/test-compose.yml test/integration/ Makefile
git commit -m "feat: motor de retransmisión de punta a punta con un destino"
```

---

## Definición de terminado, fase 2

- [ ] `go test ./... -race -count=1` pasa entero.
- [ ] `go vet ./...` limpio.
- [ ] `CGO_ENABLED=0 go build ./cmd/splitstream` produce el binario.
- [ ] `go.mod` lista exactamente tres directas: `modernc.org/sqlite`, `golang.org/x/crypto`,
      `github.com/yutopp/go-rtmp`. La directiva `go` sigue en `go 1.25.0`.
- [ ] `make sinks-up && make test-integration` pasa: un patrón de ffmpeg entra por la
      ingesta y sale por el sink con h264 640x360 y aac decodificables.
- [ ] `internal/relay` no importa `go-rtmp` ni `database/sql` (compruébalo con
      `go list -deps ./internal/relay | grep -E 'go-rtmp|database/sql'`, que debe salir vacío).
- [ ] Un publisher con clave incorrecta se rechaza.
- [ ] Un publisher con HEVC o AV1 se rechaza con un mensaje que menciona H.264 + AAC.
- [ ] `InTx` permite llamar a los repositorios dentro de una transacción sin colgarse.

## Notas para la fase 3

- La cola del sink es un canal con descarte simple. La fase 3 la sustituye por el deque
  acotado por bytes y duración con descarte por GOP completo (spec §3.3 y §3.4).
- El sink no reconecta: al fallar se queda en `StateError` hasta que lo paren. La fase 3
  añade el backoff de 1 s a 30 s con jitter (spec §6.5) y el `reset()` del timebase, que ya
  existe y está testeado precisamente para eso.
- `main.go` arranca **un** destino y hace `break`. La fase 3 los arranca todos y reacciona
  a los cambios de la API.
- `FinishSession` se llama con ceros. La fase 3 parsea el SPS del AVC sequence header para
  la resolución y mide el bitrate real (spec §3.8 y §15.2).
- `Stream.Write` de go-rtmp bloquea hasta 5 s con un contexto que no controlamos
  (spec §16.2). La fase 3 debe tratar ese timeout como conexión perdida y disparar la
  reconexión, no reintentar sobre la misma conexión.
- El sink todavía no registra eventos por destino. La fase 3 los necesita para el panel de
  log en vivo, y ahora ya puede hacerlo de forma atómica gracias a `InTx`.
