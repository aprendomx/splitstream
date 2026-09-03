# Splitstream — Plan de implementación, Fase 4

> **Para agentes:** SUB-SKILL REQUERIDA: usa `superpowers:subagent-driven-development`
> (recomendado) o `superpowers:executing-plans` para ejecutar este plan tarea por tarea.
> Los pasos usan sintaxis de checkbox (`- [ ]`) para seguimiento.

**Goal:** Que todo lo que hoy solo se puede tocar escribiendo en el SQLite se pueda hacer
por HTTP: dar de alta destinos, encenderlos y apagarlos, rotar la clave de ingesta, ver el
estado en vivo y recibirlo empujado por WebSocket.

**Architecture:** Un paquete nuevo `internal/httpapi` que depende de `store`, `crypto` y
`relay`, y al que ninguno de los tres importa. Enrutado con `net/http.ServeMux` a secas —
los patrones con método y wildcard de Go 1.22 cubren los catorce endpoints del spec §9 sin
router externo. La sesión es una cookie firmada con HMAC, sin estado en el servidor. El
motor sigue sin saber que existe JSON: la conversión a DTO vive en la capa HTTP, y un test
por reflexión impide que la duplicación se desincronice. Un paquete nuevo `internal/sinks`
recoge la construcción de sinks desde la base de datos, que hoy está copiada dentro de
`main.go`, para que la API pueda aplicar cambios de destino en caliente.

**Tech Stack:** Go 1.25, `net/http`, `crypto/hkdf` y `crypto/hmac` de la stdlib,
`github.com/coder/websocket`, `golang.org/x/time/rate`, SQLite vía `modernc.org/sqlite`.

**Spec:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` — §9 (API), §8
(seguridad), §15.2 a §15.5 y §15.8 (deuda que esta fase paga). Es la autoridad vinculante:
si este plan y el spec se contradicen, gana el spec y hay que decirlo en el ledger.

## Global Constraints

Copiados del spec. Valen para **todas** las tareas.

- **Go 1.25+.** `go.mod` dice `go 1.25.0` y no se toca.
- **Dependencias directas permitidas** (spec §5, verbatim): `github.com/yutopp/go-rtmp`,
  `modernc.org/sqlite`, `golang.org/x/crypto`, `golang.org/x/time`,
  `github.com/coder/websocket`. Ninguna más, y las dos últimas las añade esta fase.
- **NUNCA ejecutar `go mod tidy`.** Lección de la fase 2, registrada en su ledger: recorre
  las dependencias de test de `modernc/sqlite` (libc→ccgo→gc/v2→gc/v3) y mató a tres
  agentes. Para añadir una dependencia se usa `go get <módulo>@<versión>` y nada más.
- **Sin router HTTP, sin testify, sin librería de migraciones** (spec §5).
- **Logging con `log/slog`.**
- **Las claves jamás aparecen en un log ni en un mensaje de error** (spec §8), tampoco
  enmascaradas. La única salida en claro de una clave es el cuerpo de
  `GET /api/destinations/:id/key`.
- **Forma de los errores de la API** (spec §9, verbatim):
  `{"error": {"code": "...", "message": "..."}}`.
- **El binario de producción es estático:** `CGO_ENABLED=0`. Los tests van con `-race`,
  que sí necesita cgo.
- **Aislamiento de capas.** `internal/relay` no importa `go-rtmp` ni `database/sql` — la CI
  lo verifica. Esta fase añade la simétrica: `internal/httpapi` no importa `go-rtmp`.

---

## Decisiones tomadas antes de escribir el plan

Las tres primeras las decidió el usuario; las demás salen del spec o de leer el código.

1. **La primera contraseña se fija con `splitstream -setpassword`, leyendo de stdin.** No
   pasa por el entorno ni por los logs, y no deja ventana en la que quien llegue primero al
   panel se lo quede.
2. **La sesión es una cookie firmada con HMAC, sin estado.** Sobrevive a los reinicios: no
   te echa del panel a mitad de transmisión. Revocar todas las sesiones = rotar la master
   key.
3. **`relay.Metrics` no lleva tags `json`.** La conversión a DTO vive en `internal/httpapi`,
   con un test por reflexión que falla si alguien añade un campo al motor sin decidir su
   nombre público.
4. **Los cambios de destino se aplican en caliente si hay sesión viva.** `Hub.Add` reemplaza
   sin ventana de solape y `Hub.Remove` para el sink; la fase 2 hizo ese trabajo
   precisamente para esto. Si no hay sesión, el cambio se aplica en la siguiente. La
   alternativa —persistir y esperar a la próxima transmisión— haría que el toggle de la UI
   mintiera durante todo el directo.
5. **Revelar una clave y auditarla dejan de ser dos cosas.** El método que hoy usa el motor
   para construir sinks pasa a llamarse por lo que hace (`DestinationKeyForRelay`), y
   `RevealDestinationKey` —el único que la API puede alcanzar— escribe el evento dentro de
   su propia transacción. Así la auditoría deja de ser una convención (spec §15.5).

---

## Fuera de alcance

Va a las fases 5 y 6: el frontend Quasar, el `go:embed` que lo sirve, Docker, systemd y la
documentación de operación. La fase 4 termina con la API probada por `httptest`, el
WebSocket empujando JSON y el binario sirviendo ambos junto al RTMP.

---

## Estructura de archivos de esta fase

| Archivo | Responsabilidad |
| --- | --- |
| `internal/store/migrations/0002_fixed_width_timestamps.sql` | Reescribe los timestamps a ancho fijo |
| `internal/store/errors.go` (crear) | Los tres centinelas transversales del store |
| `internal/store/settings.go` (modificar) | `nowRFC3339` de ancho fijo; `GenerateIngestKey` |
| `internal/store/destinations.go` (modificar) | Centinelas envueltos, validación de nombre y plataforma, `DestinationKeyForRelay` y `RevealDestinationKey` auditado |
| `internal/sinks/factory.go` (crear) | Construye `*relay.Sink` desde un `store.Destination` |
| `internal/rtmpio/ingest.go` (modificar) | `DisconnectPublisher()`: corta la publicación sin cerrar el listener |
| `internal/httpapi/server.go` (crear) | `Server`, mux, middlewares, apagado |
| `internal/httpapi/errors.go` (crear) | Mapeo de centinelas a HTTP y la forma JSON del error |
| `internal/httpapi/auth.go` (crear) | Cookie HMAC, login, logout, rate limit |
| `internal/httpapi/dto.go` (crear) | DTOs y conversión desde `relay.Metrics` y `store.*` |
| `internal/httpapi/destinations.go` (crear) | Los siete endpoints de destinos |
| `internal/httpapi/ingest.go` (crear) | `GET /api/ingest` y `POST /api/ingest/rotate-key` |
| `internal/httpapi/status.go` (crear) | `GET /api/status` y `GET /api/events` |
| `internal/httpapi/ws.go` (crear) | `GET /ws` |
| `cmd/splitstream/main.go` (modificar) | `-setpassword`, arranque del HTTP, apagado ordenado |

---

### Task 1: Timestamps de ancho fijo (spec §15.4)

Los timestamps se guardan como TEXT con `time.RFC3339Nano`, que **recorta los ceros
finales** de la fracción. Eso rompe el orden: `"2026-09-02T10:00:00.5Z"` y
`"2026-09-02T10:00:00Z"` se comparan carácter a carácter hasta el `.` (0x2E) contra la `Z`
(0x5A), así que el primero va **antes** como texto y **después** en el tiempo. Los índices
`idx_events_created` e `idx_sessions_started` invitan exactamente a esa consulta, y
`GET /api/events?limit=100` es su primer cliente.

**Files:**
- Create: `internal/store/migrations/0002_fixed_width_timestamps.sql`
- Modify: `internal/store/settings.go` (la función `nowRFC3339`, línea 162)
- Modify: `internal/store/db.go` (la constante `SchemaVersion`, línea 20)
- Test: `internal/store/timestamps_internal_test.go` y `internal/store/timestamps_test.go` (crear)

**Interfaces:**
- Consumes: nada de tareas anteriores.
- Produces: `store.SchemaVersion = 2`. El orden lexicográfico de `created_at`,
  `updated_at`, `started_at` y `ended_at` pasa a coincidir con el cronológico, que es de lo
  que dependen `RecentEvents` (Task 9) y cualquier `ORDER BY` sobre esas columnas.

- [ ] **Step 1: Escribir los tests que fallan**

Los tests de `internal/store` son **todos externos** (`package store_test`): el paquete se
prueba por su API pública. Esta tarea es la excepción justificada y necesita **dos**
archivos, porque el formato en sí es una función privada y probarlo por fuera solo daría
una comprobación probabilística.

Primero el interno. Crea `internal/store/timestamps_internal_test.go`:

```go
package store

import (
	"testing"
	"time"
)

// TestFormatTimeSortsLexicographically es el test que define el arreglo: dos instantes
// cuyo orden cronológico se conoce deben ordenarse igual como texto.
//
// Con time.RFC3339Nano esto falla, porque recorta los ceros finales de la fracción: el
// instante sin fracción produce una 'Z' donde el otro tiene un '.', y '.' (0x2E) es menor
// que 'Z' (0x5A), así que el posterior queda antes.
//
// Va en un test interno —el resto del paquete se prueba desde fuera— porque formatTime es
// privada y comprobar esta propiedad desde el exterior obligaría a insertar muchas filas
// y confiar en que alguna caiga con nanosegundos acabados en cero. Eso no es un test, es
// una apuesta.
func TestFormatTimeSortsLexicographically(t *testing.T) {
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

	casos := []struct {
		nombre         string
		antes, despues time.Time
	}{
		{"sin fracción contra media fracción", base, base.Add(500 * time.Millisecond)},
		{"media fracción contra un segundo", base.Add(500 * time.Millisecond), base.Add(time.Second)},
		{"un nanosegundo cuenta", base, base.Add(time.Nanosecond)},
		{"cruce de minuto", base.Add(59 * time.Second), base.Add(time.Minute)},
		{"cruce de día", base.Add(13*time.Hour + 59*time.Minute), base.Add(14 * time.Hour)},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			a, b := formatTime(c.antes), formatTime(c.despues)
			if !(a < b) {
				t.Errorf("orden de texto invertido:\n  antes   = %q\n  después = %q", a, b)
			}
		})
	}
}

// TestFormatTimeIsAlwaysTheSameWidth deja explícito el porqué: si todas las cadenas miden
// lo mismo, la comparación de texto no puede desalinearse.
func TestFormatTimeIsAlwaysTheSameWidth(t *testing.T) {
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	quiero := len(formatTime(base))

	for _, d := range []time.Duration{0, time.Nanosecond, time.Microsecond, time.Millisecond, 500 * time.Millisecond, time.Second} {
		if got := formatTime(base.Add(d)); len(got) != quiero {
			t.Errorf("%q mide %d, quería %d", got, len(got), quiero)
		}
	}
}

// TestFormatTimeNormalisesToUTC: el orden de texto solo coincide con el cronológico si
// todas las filas comparten huso. Un time.Time con zona debe salir en UTC.
func TestFormatTimeNormalisesToUTC(t *testing.T) {
	zona := time.FixedZone("CDT", -5*3600)
	conZona := time.Date(2026, 9, 2, 5, 0, 0, 0, zona)

	got := formatTime(conZona)
	if quiero := "2026-09-02T10:00:00.000000000Z"; got != quiero {
		t.Errorf("got %q, quería %q", got, quiero)
	}
}

// TestFormatTimeRoundTrips comprueba que lo escrito se vuelve a leer sin perder
// precisión: el parseo sigue usando RFC3339Nano, que acepta cualquier número de decimales.
func TestFormatTimeRoundTrips(t *testing.T) {
	quiero := time.Date(2026, 9, 2, 10, 0, 0, 123456789, time.UTC)

	got, err := time.Parse(time.RFC3339Nano, formatTime(quiero))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.Equal(quiero) {
		t.Errorf("got %v, quería %v", got, quiero)
	}
}
```

Ahora el externo, que prueba la migración de datos por el camino real. Crea
`internal/store/timestamps_test.go`:

```go
package store_test

import (
	"context"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

// tsPattern es el formato persistente: RFC3339 en UTC con nueve dígitos de fracción.
var tsPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}Z$`)

// TestMigration0002RewritesExistingRows prueba la migración de datos sin tocar nada
// privado: escribe filas con el formato viejo, retrasa PRAGMA user_version a 1, cierra y
// vuelve a abrir. Open aplica la 0002 de verdad, que es el camino que corre en producción
// cuando alguien actualiza el binario sobre una base existente.
func TestMigration0002RewritesExistingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Las tres formas que time.RFC3339Nano podía producir: sin fracción, con fracción
	// recortada y con fracción completa. Cronológicamente van 1 < 2 < 3.
	viejos := []struct{ ts, msg string }{
		{"2026-09-02T10:00:00Z", "evento-1"},
		{"2026-09-02T10:00:00.5Z", "evento-2"},
		{"2026-09-02T10:00:00.987654321Z", "evento-3"},
	}
	for _, v := range viejos {
		if _, err := db.SQL().ExecContext(ctx,
			`INSERT INTO events (level, kind, message, created_at) VALUES ('info', 'test', ?, ?)`,
			v.msg, v.ts); err != nil {
			t.Fatalf("insertar %s: %v", v.msg, err)
		}
	}

	// Antes de migrar, el orden de texto está mal: es el bug que se está arreglando.
	if got := primerEvento(t, db); got != "evento-2" {
		t.Fatalf("el test no reproduce el bug: el primero por texto es %q, esperaba evento-2", got)
	}

	if _, err := db.SQL().ExecContext(ctx, `PRAGMA user_version = 1`); err != nil {
		t.Fatalf("retrasar user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	t.Cleanup(func() { db2.Close() })

	if got := primerEvento(t, db2); got != "evento-1" {
		t.Errorf("tras migrar, el primero por texto es %q; quería evento-1", got)
	}

	rows, err := db2.SQL().QueryContext(ctx, `SELECT message, created_at FROM events ORDER BY created_at`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var orden []string
	for rows.Next() {
		var msg, ts string
		if err := rows.Scan(&msg, &ts); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !tsPattern.MatchString(ts) {
			t.Errorf("%s quedó con created_at = %q, que no es de ancho fijo", msg, ts)
		}
		orden = append(orden, msg)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	quiero := []string{"evento-1", "evento-2", "evento-3"}
	if len(orden) != len(quiero) {
		t.Fatalf("orden = %v, quería %v", orden, quiero)
	}
	for i := range quiero {
		if orden[i] != quiero[i] {
			t.Errorf("orden = %v, quería %v", orden, quiero)
			break
		}
	}
}

// TestNewRowsAreWrittenWithFixedWidth comprueba que lo que escribe el código de hoy ya
// tiene la forma nueva, no solo lo que migró.
func TestNewRowsAreWrittenWithFixedWidth(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)

	if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelInfo, Kind: "test", Message: "nuevo"}); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}

	var ts string
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT created_at FROM events WHERE message = 'nuevo'`).Scan(&ts); err != nil {
		t.Fatalf("leer created_at: %v", err)
	}
	if !tsPattern.MatchString(ts) {
		t.Errorf("created_at = %q, que no es de ancho fijo", ts)
	}
}

func primerEvento(t *testing.T, db *store.DB) string {
	t.Helper()
	var msg string
	if err := db.SQL().QueryRowContext(context.Background(),
		`SELECT message FROM events ORDER BY created_at LIMIT 1`).Scan(&msg); err != nil {
		t.Fatalf("consulta de orden: %v", err)
	}
	return msg
}
```

`openTemp(t)` ya existe en `internal/store/db_test.go:12` y devuelve un `*store.DB` con
`Cleanup` puesto: úsalo tal cual.

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/store/ -run 'FormatTime|Migration0002|FixedWidth' -v`
Expected: los cuatro internos fallan a compilar por `undefined: formatTime`. Para ver el
fallo *de verdad* —no el de compilación— añade primero `func formatTime(t time.Time)
string { return t.UTC().Format(time.RFC3339Nano) }` y vuelve a correr:
`TestFormatTimeSortsLexicographically/sin_fracción_contra_media_fracción` debe fallar, y
`TestFormatTimeIsAlwaysTheSameWidth` también. Ese fallo es la prueba de que los tests
detectan el bug. `TestMigration0002RewritesExistingRows` fallará en su `t.Fatalf` de
reproducción si el bug no está presente, así que también sirve de comprobación cruzada.

- [ ] **Step 3: Implementar el formato de ancho fijo**

En `internal/store/settings.go`, sustituye la línea 162 por:

```go
// timeLayout es RFC3339 con la fracción SIEMPRE de nueve dígitos.
//
// No se usa time.RFC3339Nano porque recorta los ceros finales, y entonces el orden de
// texto deja de ser el cronológico: "10:00:00.5Z" va antes que "10:00:00Z" al comparar
// carácter a carácter, porque '.' (0x2E) < 'Z' (0x5A). Los índices idx_events_created e
// idx_sessions_started invitan justo a esa consulta (spec §15.4).
//
// Al PARSEAR se sigue usando time.RFC3339Nano, que acepta cualquier número de decimales:
// así las filas escritas antes de la migración 0002 se leen igual de bien.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// formatTime rinde un instante en el formato persistente, siempre en UTC: el orden de
// texto solo coincide con el cronológico si todas las filas comparten huso.
func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func nowRFC3339() string { return formatTime(time.Now()) }
```

- [ ] **Step 4: Escribir la migración de datos**

El runner (`internal/store/db.go:128`) pasa el archivo entero a un solo `ExecContext`, y la
`0001_initial.sql` ya lleva varias sentencias, así que **se admiten varias por archivo**.
Crea `internal/store/migrations/0002_fixed_width_timestamps.sql`:

```sql
-- Reescribe los timestamps al formato de ancho fijo del spec §15.4.
--
-- Las filas viejas se escribieron con time.RFC3339Nano, que recorta los ceros finales de
-- la fracción, así que en la base hay tres formas: sin fracción ('...:00Z'), con fracción
-- recortada ('...:00.5Z') y con fracción completa ('...:00.123456789Z'). Esta expresión
-- normaliza las tres a nueve dígitos.
--
-- Todas estas columnas se escribieron en UTC, así que terminan en 'Z' y sus primeros 19
-- caracteres son 'YYYY-MM-DDTHH:MM:SS'. La fracción, si la hay, va de la posición 21 a la
-- 'Z' final.

UPDATE settings SET
    created_at = substr(created_at, 1, 19) || '.' || substr(
        CASE WHEN instr(created_at, '.') = 0 THEN '000000000'
             ELSE substr(created_at, 21, length(created_at) - 21) || '000000000' END, 1, 9) || 'Z',
    updated_at = substr(updated_at, 1, 19) || '.' || substr(
        CASE WHEN instr(updated_at, '.') = 0 THEN '000000000'
             ELSE substr(updated_at, 21, length(updated_at) - 21) || '000000000' END, 1, 9) || 'Z';

UPDATE destinations SET
    created_at = substr(created_at, 1, 19) || '.' || substr(
        CASE WHEN instr(created_at, '.') = 0 THEN '000000000'
             ELSE substr(created_at, 21, length(created_at) - 21) || '000000000' END, 1, 9) || 'Z',
    updated_at = substr(updated_at, 1, 19) || '.' || substr(
        CASE WHEN instr(updated_at, '.') = 0 THEN '000000000'
             ELSE substr(updated_at, 21, length(updated_at) - 21) || '000000000' END, 1, 9) || 'Z';

UPDATE sessions SET
    started_at = substr(started_at, 1, 19) || '.' || substr(
        CASE WHEN instr(started_at, '.') = 0 THEN '000000000'
             ELSE substr(started_at, 21, length(started_at) - 21) || '000000000' END, 1, 9) || 'Z';

UPDATE sessions SET
    ended_at = substr(ended_at, 1, 19) || '.' || substr(
        CASE WHEN instr(ended_at, '.') = 0 THEN '000000000'
             ELSE substr(ended_at, 21, length(ended_at) - 21) || '000000000' END, 1, 9) || 'Z'
WHERE ended_at IS NOT NULL;

UPDATE events SET
    created_at = substr(created_at, 1, 19) || '.' || substr(
        CASE WHEN instr(created_at, '.') = 0 THEN '000000000'
             ELSE substr(created_at, 21, length(created_at) - 21) || '000000000' END, 1, 9) || 'Z';
```

Y sube la constante en `internal/store/db.go:20`:

```go
const SchemaVersion = 2
```

Esa constante tiene un guardián: `loadMigrations` falla si la última migración embebida no
coincide con ella (`internal/store/db.go:178`). Si te olvidas de subirla, los tests del
paquete se caen enteros con un mensaje que lo explica.

- [ ] **Step 5: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/store/ -race -count=1`
Expected: PASS. Comprueba en particular `TestOpenSetsSchemaVersion`
(`internal/store/db_test.go:57`): lee `PRAGMA user_version` y ahora debe valer 2. Si
compara contra la constante ya está; si asertaba un 1 literal, actualízalo.

- [ ] **Step 6: Commit**

```bash
git add internal/store/
git commit -m "fix(store): timestamps de ancho fijo, para que el orden de texto sea el cronológico"
```

---

### Task 2: Taxonomía de errores del store (spec §15.3 y §15.8)

Hoy las validaciones devuelven `errors.New` pelados, así que la API no podría distinguir un
400 de un 500 sin comparar cadenas. Esta tarea introduce tres centinelas transversales y
hace que los cuatro que ya existen los envuelvan, de modo que el código viejo siga
funcionando y el nuevo pueda preguntar por la clase. De paso paga §15.8.

**Files:**
- Create: `internal/store/errors.go`
- Modify: `internal/store/destinations.go` (los centinelas de las líneas 16 y 19; la
  validación de `CreateDestination` y `UpdateDestination`)
- Modify: `internal/store/events.go` (el centinela de la línea 12)
- Modify: `internal/store/settings.go` (el centinela de la línea 20; `GenerateKey` → `GenerateIngestKey`)
- Test: `internal/store/errors_test.go` (crear)

**Interfaces:**
- Consumes: nada de la Task 1.
- Produces: `store.ErrNotFound`, `store.ErrInvalidInput`, `store.ErrConflict`. Todos los
  errores de validación y de "no existe" del store satisfacen `errors.Is` contra uno de los
  tres. `store.GenerateIngestKey() (crypto.Secret, error)` sustituye a `GenerateKey`. La
  Task 6 los mapea a códigos HTTP.

- [ ] **Step 1: Escribir el test que falla**

Crea `internal/store/errors_test.go`:

```go
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
)

// TestSentinelsCarryTheirClass fija el contrato: cada centinela concreto pertenece a una
// de las tres clases transversales, y la API puede preguntar por la clase sin conocer el
// centinela.
func TestSentinelsCarryTheirClass(t *testing.T) {
	casos := []struct {
		nombre string
		err    error
		clase  error
	}{
		{"destino no encontrado", ErrDestinationNotFound, ErrNotFound},
		{"sesión no encontrada", ErrSessionNotFound, ErrNotFound},
		{"URL inválida", ErrInvalidDestinationURL, ErrInvalidInput},
		{"settings sin inicializar", ErrSettingsNotInitialized, ErrConflict},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if !errors.Is(c.err, c.clase) {
				t.Errorf("%v no pertenece a la clase %v", c.err, c.clase)
			}
		})
	}
}

// TestClassesAreDistinct evita el error tonto de que los tres centinelas acaben siendo el
// mismo valor por copiar y pegar.
func TestClassesAreDistinct(t *testing.T) {
	if errors.Is(ErrNotFound, ErrInvalidInput) || errors.Is(ErrInvalidInput, ErrConflict) ||
		errors.Is(ErrConflict, ErrNotFound) {
		t.Error("las clases se confunden entre sí")
	}
}

// TestWrappedErrorsStillMatchTheirOwnSentinel comprueba que envolver no rompe a quien ya
// preguntaba por el centinela concreto: es código que existe hoy y no debe cambiar.
func TestWrappedErrorsStillMatchTheirOwnSentinel(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)

	_, err := db.UpdateDestination(ctx, testCipher(t, 1), 9999, DestinationPatch{})
	if !errors.Is(err, ErrDestinationNotFound) {
		t.Errorf("err = %v, quería ErrDestinationNotFound", err)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, quería que también fuera ErrNotFound", err)
	}
}

// TestCreateDestinationValidatesNameAndPlatform cierra el hueco que la API destaparía:
// hoy un nombre vacío o una plataforma inventada solo los para el CHECK de SQLite, que
// devuelve un error de driver indistinguible de un fallo de disco.
func TestCreateDestinationValidatesNameAndPlatform(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	c := testCipher(t, 1)

	casos := []struct {
		nombre string
		in     NewDestination
	}{
		{"nombre vacío", NewDestination{Name: "", Platform: PlatformCustom, RTMPURL: "rtmp://x/live", Key: crypto.Secret("k")}},
		{"nombre solo espacios", NewDestination{Name: "   ", Platform: PlatformCustom, RTMPURL: "rtmp://x/live", Key: crypto.Secret("k")}},
		{"plataforma inventada", NewDestination{Name: "n", Platform: Platform("myspace"), RTMPURL: "rtmp://x/live", Key: crypto.Secret("k")}},
		{"clave vacía", NewDestination{Name: "n", Platform: PlatformCustom, RTMPURL: "rtmp://x/live", Key: crypto.Secret("")}},
	}

	for _, cas := range casos {
		t.Run(cas.nombre, func(t *testing.T) {
			_, err := db.CreateDestination(ctx, c, cas.in)
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, quería que fuera ErrInvalidInput", err)
			}
		})
	}
}
```

`openTemp(t)` (`internal/store/db_test.go:12`) y `testCipher(t, fill byte)`
(`internal/store/settings_test.go:16`) ya existen en el paquete de test: el segundo recibe
un byte de relleno para la clave, de ahí el `1`. Las constantes de `Platform` están en
`internal/store/destinations.go:48-53`.

- [ ] **Step 2: Ejecutar el test y verificar que falla**

Run: `go test ./internal/store/ -run 'Sentinel|Classes|Wrapped|ValidatesName' -v`
Expected: FAIL por `undefined: ErrNotFound`, `ErrInvalidInput` y `ErrConflict`.

- [ ] **Step 3: Crear los centinelas**

Crea `internal/store/errors.go`:

```go
package store

import "errors"

// Las tres clases transversales de error del store.
//
// Existen para que la capa HTTP pueda decidir el código de respuesta sin conocer cada
// centinela concreto ni comparar cadenas (spec §15.3). Los centinelas específicos siguen
// existiendo y siguen siendo lo que usa el código del motor: estas clases se añaden por
// debajo, envolviéndolos, así que errors.Is sigue funcionando en ambos sentidos.
var (
	// ErrNotFound: la fila pedida no existe. La API responde 404.
	ErrNotFound = errors.New("no encontrado")

	// ErrInvalidInput: lo que llega no es aceptable, y reintentarlo igual tampoco lo
	// sería. La API responde 400.
	ErrInvalidInput = errors.New("entrada inválida")

	// ErrConflict: la entrada es válida pero choca con el estado actual. La API responde
	// 409.
	ErrConflict = errors.New("conflicto con el estado actual")
)
```

- [ ] **Step 4: Envolver los centinelas existentes**

En `internal/store/destinations.go`, sustituye las declaraciones de las líneas 16 y 19:

```go
// ErrDestinationNotFound se devuelve cuando el id no existe.
var ErrDestinationNotFound = fmt.Errorf("%w: destino", ErrNotFound)

// ErrInvalidDestinationURL indica que la URL del destino no sirve para retransmitir.
var ErrInvalidDestinationURL = fmt.Errorf("%w: URL de destino", ErrInvalidInput)
```

En `internal/store/events.go`, línea 12:

```go
// ErrSessionNotFound se devuelve cuando el id de sesión no existe.
var ErrSessionNotFound = fmt.Errorf("%w: sesión", ErrNotFound)
```

En `internal/store/settings.go`, línea 20:

```go
// ErrSettingsNotInitialized indica que falta Bootstrap. Es un conflicto de estado, no una
// entrada inválida: la petición es correcta, el servicio aún no está listo.
var ErrSettingsNotInitialized = fmt.Errorf("%w: settings sin inicializar, falta Bootstrap", ErrConflict)
```

Comprueba que `fmt` esté importado en los tres archivos; `destinations.go` ya lo importa.

- [ ] **Step 5: Validar nombre, plataforma y clave**

En `internal/store/destinations.go`, añade junto a `validateRTMPURL`:

```go
// validateName rechaza el nombre vacío o de solo espacios. El esquema no lo impide y sin
// esto el destino aparecería en la UI como una fila sin etiqueta.
func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: el nombre no puede estar vacío", ErrInvalidInput)
	}
	return nil
}

// validatePlatform duplica a propósito el CHECK del esquema: así el error llega con un
// mensaje legible en vez de como un fallo de constraint del driver, indistinguible de un
// error de disco.
func validatePlatform(p Platform) error {
	switch p {
	case PlatformYouTube, PlatformTwitch, PlatformFacebook, PlatformKick, PlatformX, PlatformCustom:
		return nil
	}
	return fmt.Errorf("%w: plataforma %q no soportada", ErrInvalidInput, p)
}

// validateKey rechaza la clave vacía. Un destino sin clave conecta y es rechazado por la
// plataforma en la primera escritura, que es el modo de fallo más confuso que tenemos.
func validateKey(k crypto.Secret) error {
	if k.Reveal() == "" {
		return fmt.Errorf("%w: la clave no puede estar vacía", ErrInvalidInput)
	}
	return nil
}
```

Usa los nombres reales de las constantes de `Platform` que haya en el archivo. Llama a las
tres desde `CreateDestination`, junto a la llamada a `validateRTMPURL` que ya está ahí, y
desde `UpdateDestination` **solo para los campos no nil** del patch.

- [ ] **Step 6: Renombrar `GenerateKey` (spec §15.8)**

En `internal/store/settings.go:32`, renombra la función a `GenerateIngestKey` y ajusta su
comentario para que diga qué genera. Busca los usos y actualízalos:

```bash
grep -rn "GenerateKey" --include="*.go" .
```

- [ ] **Step 7: Ejecutar los tests y verificar que pasan**

Run: `go test ./... -race -count=1`
Expected: PASS entero. Los cambios de mensaje pueden romper tests que asertaban texto: si
alguno falla por eso, **arregla el test, no el mensaje** — salvo que el mensaje nuevo sea
peor que el viejo, en cuyo caso arregla el mensaje.

- [ ] **Step 8: Commit**

```bash
git add internal/store/
git commit -m "feat(store): clases de error transversales y validación de la entrada"
```

---

### Task 3: Auditoría obligatoria del revelado (spec §15.5)

El comentario de `RevealDestinationKey` dice hoy, literalmente: «quien lo llame debe
registrar un evento». Eso es una convención, y §15.5 pide que sea un invariante. El
obstáculo es que el motor usa ese mismo método para construir sus sinks en cada sesión: si
auditara siempre, cada arranque de transmisión escribiría un evento de revelado por
destino, y la auditoría se ahogaría en ruido justo cuando importa.

La salida es separar los dos usos por el nombre. `DestinationKeyForRelay` es lo que usa el
motor: no es una divulgación a una persona, es una lectura interna. `RevealDestinationKey`
es lo que la API expone, y escribe su evento dentro de la misma transacción, de modo que no
existe camino que revele sin dejar rastro.

**Files:**
- Modify: `internal/store/destinations.go:290-307`
- Test: `internal/store/destinations_test.go` (añadir)

**Interfaces:**
- Consumes: `store.ErrNotFound` y `store.ErrInvalidInput` de la Task 2.
- Produces:
  - `func (d *DB) DestinationKeyForRelay(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error)`
    — sin auditoría. Lo usa `internal/sinks` (Task 8).
  - `func (d *DB) RevealDestinationKey(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error)`
    — audita siempre. Lo usa `GET /api/destinations/:id/key` (Task 9).
  - El evento auditado tiene `Kind == "key_revealed"`, `Level == LevelWarn` y
    `DestinationID` puesto.

- [ ] **Step 1: Escribir el test que falla**

Añade a `internal/store/destinations_test.go`:

```go
// TestRevealDestinationKeyAlwaysAudits fija el invariante del spec §15.5: no hay forma de
// revelar una clave por la API sin dejar rastro, porque el evento se escribe en la misma
// transacción que el descifrado.
func TestRevealDestinationKeyAlwaysAudits(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	c := testCipher(t, 1)

	dest, err := db.CreateDestination(ctx, c, store.NewDestination{
		Name: "yt", Platform: store.PlatformYouTube,
		RTMPURL: "rtmp://a.rtmp.youtube.com/live2", Key: crypto.Secret("clave-secreta"),
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	got, err := db.RevealDestinationKey(ctx, c, dest.ID)
	if err != nil {
		t.Fatalf("RevealDestinationKey: %v", err)
	}
	if got.Reveal() != "clave-secreta" {
		t.Errorf("la clave revelada no es la guardada")
	}

	eventos, err := db.RecentEvents(ctx, 50)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}

	var revelados []store.Event
	for _, e := range eventos {
		if e.Kind == "key_revealed" {
			revelados = append(revelados, e)
		}
	}
	if len(revelados) != 1 {
		t.Fatalf("eventos key_revealed = %d, quería 1", len(revelados))
	}
	ev := revelados[0]
	if ev.DestinationID == nil || *ev.DestinationID != dest.ID {
		t.Errorf("el evento no apunta al destino: %+v", ev)
	}
	if ev.Level != store.LevelWarn {
		t.Errorf("level = %q, quería warn: revelar una clave no es rutina", ev.Level)
	}
	if strings.Contains(ev.Message, "clave-secreta") {
		t.Error("el evento de auditoría lleva la clave en claro dentro")
	}
}

// TestDestinationKeyForRelayDoesNotAudit: el motor lee la clave en cada sesión y por cada
// destino. Si eso auditara, el log se llenaría de ruido y la auditoría dejaría de servir.
func TestDestinationKeyForRelayDoesNotAudit(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	c := testCipher(t, 1)

	dest, err := db.CreateDestination(ctx, c, store.NewDestination{
		Name: "yt", Platform: store.PlatformYouTube,
		RTMPURL: "rtmp://a.rtmp.youtube.com/live2", Key: crypto.Secret("clave-secreta"),
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := db.DestinationKeyForRelay(ctx, c, dest.ID); err != nil {
			t.Fatalf("DestinationKeyForRelay: %v", err)
		}
	}

	eventos, err := db.RecentEvents(ctx, 50)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	for _, e := range eventos {
		if e.Kind == "key_revealed" {
			t.Error("la lectura del motor generó un evento de auditoría")
		}
	}
}

// TestRevealDestinationKeyMissingIDDoesNotAudit: un id que no existe no debe dejar un
// evento de revelado, porque no se reveló nada.
func TestRevealDestinationKeyMissingIDDoesNotAudit(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	c := testCipher(t, 1)

	_, err := db.RevealDestinationKey(ctx, c, 9999)
	if !errors.Is(err, store.ErrDestinationNotFound) {
		t.Fatalf("err = %v, quería ErrDestinationNotFound", err)
	}

	eventos, err := db.RecentEvents(ctx, 50)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	for _, e := range eventos {
		if e.Kind == "key_revealed" {
			t.Error("se auditó un revelado que no ocurrió")
		}
	}
}
```

Añade `errors` y `strings` a los imports del archivo de test si no están.

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/store/ -run 'Reveal|ForRelay' -v`
Expected: FAIL por `undefined: DestinationKeyForRelay`, y `TestRevealDestinationKeyAlwaysAudits`
por 0 eventos donde quería 1.

- [ ] **Step 3: Implementar la separación**

Sustituye el bloque de `internal/store/destinations.go:290-307` por:

```go
// DestinationKeyForRelay descifra la clave del destino para que el motor pueda construir
// su sink. NO audita: el motor la lee en cada sesión y por cada destino, así que auditar
// aquí llenaría el log de ruido y taparía justo lo que la auditoría quiere hacer visible.
//
// No es una divulgación a una persona; la clave no sale del proceso. El camino que sí lo
// es —y que sí audita— es RevealDestinationKey.
func (d *DB) DestinationKeyForRelay(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error) {
	var blob []byte
	err := d.ex.QueryRowContext(ctx,
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

// RevealDestinationKey descifra la clave y deja constancia de que alguien la pidió.
//
// El evento se escribe en la MISMA transacción que la lectura, así que no existe un camino
// que revele sin auditar: o pasan las dos cosas o no pasa ninguna (spec §15.5). Antes esto
// era un comentario pidiendo al llamante que registrara el evento, y un comentario no es
// un invariante.
func (d *DB) RevealDestinationKey(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error) {
	var key crypto.Secret
	err := d.InTx(ctx, func(tx *DB) error {
		k, err := tx.DestinationKeyForRelay(ctx, c, id)
		if err != nil {
			return err
		}
		// El mensaje NO lleva la clave, ni siquiera enmascarada (spec §8). El destino se
		// identifica por su id, que es lo que hace falta para investigar.
		if _, err := tx.LogEvent(ctx, Event{
			DestinationID: &id,
			Level:         LevelWarn,
			Kind:          "key_revealed",
			Message:       "se reveló la clave del destino",
		}); err != nil {
			return err
		}
		key = k
		return nil
	})
	if err != nil {
		return "", err
	}
	return key, nil
}
```

`InTx` no se puede anidar (`ErrNestedTransaction`, fase 2). Comprueba que ningún llamador
de `RevealDestinationKey` esté ya dentro de un `InTx`; hoy solo lo llama `main.go`, que no
lo está — y ese llamador pasa a la variante `ForRelay` en el Step 4.

- [ ] **Step 4: Mover al motor a la variante sin auditoría**

En `cmd/splitstream/main.go`, dentro de `engine.SetSinkProvider`, cambia
`db.RevealDestinationKey(ctx, cipher, d.ID)` por `db.DestinationKeyForRelay(ctx, cipher, d.ID)`.
Comprueba que no queden más usos:

```bash
grep -rn "RevealDestinationKey" --include="*.go" .
```

Tras este paso, el único uso de `RevealDestinationKey` debe ser el de los tests, hasta que
la Task 9 añada el endpoint.

- [ ] **Step 5: Ejecutar los tests y verificar que pasan**

Run: `go test ./... -race -count=1`
Expected: PASS entero.

- [ ] **Step 6: Commit**

```bash
git add internal/store/ cmd/splitstream/main.go
git commit -m "feat(store): auditar el revelado de claves como invariante, no como convención"
```

---

### Task 4: `-setpassword`

`password_hash` arranca vacío y no hay forma de fijarlo. Sin esto, la API de la Task 6 no
tiene contra qué autenticar.

Sobre la entrada: el spec limita las dependencias a cinco y `golang.org/x/term` no está
entre ellas, así que no se puede suprimir el eco del terminal. La contraseña se lee de
stdin tal cual, y la forma recomendada de invocarlo —documentada en el mensaje de ayuda y
en el README de la fase 6— es dejar que el shell la lea sin eco:

```bash
read -rs PW && printf '%s' "$PW" | splitstream -setpassword && unset PW
```

`read -rs` no la muestra ni la deja en el historial, y la tubería no crea una línea de
comando con la contraseña dentro.

**Files:**
- Modify: `cmd/splitstream/main.go`
- Test: `cmd/splitstream/main_test.go` (añadir)

**Interfaces:**
- Consumes: `crypto.HashPassword`, `db.SetPasswordHash`, `store.Open`, `config.Load`.
- Produces: `func setPassword(ctx context.Context, in io.Reader, out io.Writer) error` y el
  flag `-setpassword`. La Task 6 asume que `settings.PasswordHash` puede estar vacío y que
  eso significa «aún no configurada».

- [ ] **Step 1: Escribir el test que falla**

Añade a `cmd/splitstream/main_test.go`:

```go
// TestReadPasswordRejectsUnusableInput: una contraseña vacía o demasiado corta protege
// tan poco que aceptarla sería mentir sobre el estado del servicio.
func TestReadPasswordRejectsUnusableInput(t *testing.T) {
	casos := []struct{ nombre, in string }{
		{"vacía", "\n"},
		{"solo espacios", "        \n"},
		{"demasiado corta", "corta\n"},
		{"stdin cerrado", ""},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			_, err := readPassword(strings.NewReader(c.in))
			if err == nil {
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

// TestSetPasswordNeverEchoesTheSecret es la propiedad del spec §8 aplicada aquí: lo que
// el comando imprime no puede contener la contraseña ni un trozo suyo.
func TestSetPasswordNeverEchoesTheSecret(t *testing.T) {
	ctx := context.Background()
	const secreto = "una-contraseña-muy-secreta"

	dir := t.TempDir()
	t.Setenv("SPLITSTREAM_MASTER_KEY", testKeyB64())
	t.Setenv("SPLITSTREAM_DB_PATH", filepath.Join(dir, "test.db"))

	var out bytes.Buffer
	if err := setPassword(ctx, strings.NewReader(secreto+"\n"), &out); err != nil {
		t.Fatalf("setPassword: %v", err)
	}

	if strings.Contains(out.String(), secreto) {
		t.Errorf("la salida lleva la contraseña: %q", out.String())
	}
	// Ni siquiera un prefijo reconocible.
	if strings.Contains(out.String(), secreto[:8]) {
		t.Errorf("la salida lleva un prefijo de la contraseña: %q", out.String())
	}
}

// TestSetPasswordPersistsAVerifiableHash: el objetivo del comando es que el login de la
// Task 6 pueda verificar después.
func TestSetPasswordPersistsAVerifiableHash(t *testing.T) {
	ctx := context.Background()
	const secreto = "una-contraseña-muy-secreta"

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	t.Setenv("SPLITSTREAM_MASTER_KEY", testKeyB64())
	t.Setenv("SPLITSTREAM_DB_PATH", dbPath)

	if err := setPassword(ctx, strings.NewReader(secreto+"\n"), io.Discard); err != nil {
		t.Fatalf("setPassword: %v", err)
	}

	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	s, err := db.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
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
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	t.Setenv("SPLITSTREAM_MASTER_KEY", testKeyB64())
	t.Setenv("SPLITSTREAM_DB_PATH", dbPath)

	if err := setPassword(ctx, strings.NewReader("la-primera-contraseña\n"), io.Discard); err != nil {
		t.Fatalf("primera: %v", err)
	}
	if err := setPassword(ctx, strings.NewReader("la-segunda-contraseña\n"), io.Discard); err != nil {
		t.Fatalf("segunda: %v", err)
	}

	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	s, err := db.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}

	if ok, _ := crypto.VerifyPassword(s.PasswordHash, "la-primera-contraseña"); ok {
		t.Error("la contraseña vieja sigue valiendo")
	}
	if ok, _ := crypto.VerifyPassword(s.PasswordHash, "la-segunda-contraseña"); !ok {
		t.Error("la contraseña nueva no vale")
	}
}
```

`testKeyB64()` ya existe en `cmd/splitstream/main_test.go`. Añade a los imports lo que
falte: `io`, `path/filepath`, y los paquetes `crypto` y `store` del proyecto.

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./cmd/splitstream/ -run 'ReadPassword|SetPassword' -v`
Expected: FAIL por `undefined: readPassword` y `undefined: setPassword`.

- [ ] **Step 3: Implementar**

En `cmd/splitstream/main.go`, añade:

```go
// minPasswordLen es el mínimo aceptable. No es una política de seguridad seria, es un
// filtro contra el descuido: el panel queda expuesto a internet y una contraseña de tres
// letras no es una contraseña.
const minPasswordLen = 8

// readPassword lee una contraseña de una línea de r.
//
// Quita solo el salto de línea final —y el retorno de carro, por si viene pegada desde
// Windows—: los espacios interiores son parte de la contraseña, porque una frase de paso
// los lleva.
func readPassword(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("leer la contraseña: %w", err)
	}
	pw := strings.TrimRight(line, "\r\n")

	if strings.TrimSpace(pw) == "" {
		return "", errors.New("la contraseña no puede estar vacía")
	}
	if len(pw) < minPasswordLen {
		return "", fmt.Errorf("la contraseña necesita al menos %d caracteres", minPasswordLen)
	}
	return pw, nil
}

// setPassword fija la contraseña del panel. No imprime nada que dependa de ella: ni la
// contraseña, ni su longitud, ni un prefijo (spec §8).
func setPassword(ctx context.Context, in io.Reader, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	pw, err := readPassword(in)
	if err != nil {
		return err
	}

	hash, err := crypto.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("hashear la contraseña: %w", err)
	}

	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	cipher, err := crypto.NewCipher(cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("inicializar el cifrado: %w", err)
	}
	// Bootstrap deja la fila de settings creada; sin él, SetPasswordHash no tiene dónde
	// escribir en una base recién hecha.
	if err := db.Bootstrap(ctx, cipher); err != nil {
		return err
	}

	if err := db.SetPasswordHash(ctx, hash); err != nil {
		return err
	}

	fmt.Fprintln(out, "contraseña del panel actualizada")
	return nil
}
```

Y en `main()`, junto a los flags que ya hay:

```go
	setpw := flag.Bool("setpassword", false,
		"lee una contraseña de stdin y la fija como la del panel; usa `read -rs PW && printf '%s' \"$PW\" | splitstream -setpassword`")
```

con su rama, después de la de `-version` y antes de la de `-genkey`:

```go
	if *setpw {
		if err := setPassword(context.Background(), os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
```

Añade `bufio` a los imports.

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./cmd/splitstream/ -race -count=1 -v`
Expected: PASS, los seis nuevos incluidos.

- [ ] **Step 5: Commit**

```bash
git add cmd/splitstream/
git commit -m "feat(main): -setpassword para fijar la contraseña del panel"
```

---

### Task 5: La cookie de sesión firmada

El spec §9 pide «cookie de sesión httpOnly» y no dice qué lleva dentro. La decisión (nº 2
de la lista de arriba) es firmarla con HMAC y no guardar nada en el servidor: así reiniciar
el servicio no te echa del panel a mitad de transmisión, y no hace falta ni tabla nueva ni
limpieza de sesiones caducadas.

La clave de firma se deriva de la master key con HKDF y una etiqueta propia. No se usa la
master key directamente: si la firma de la cookie y el cifrado de las claves de destino
compartieran material, un fallo en cualquiera de los dos ayudaría a atacar el otro.
`crypto/hkdf` está en la stdlib desde Go 1.24, así que esto no añade dependencias.

**Files:**
- Create: `internal/httpapi/auth.go`
- Test: `internal/httpapi/auth_test.go`

**Interfaces:**
- Consumes: `config.MasterKeyLen` (vale 32).
- Produces:
  - `func newSessionSigner(master [32]byte) (*sessionSigner, error)`
  - `func (s *sessionSigner) issue(now time.Time) string`
  - `func (s *sessionSigner) verify(value string, now time.Time) error`
  - `const sessionCookieName = "splitstream_session"`, `const sessionTTL = 30 * 24 * time.Hour`
  - Errores: `errCookieMalformed`, `errCookieBadSignature`, `errCookieExpired`.
  La Task 6 usa `issue` al hacer login y `verify` en el middleware.

- [ ] **Step 1: Escribir el test que falla**

Crea `internal/httpapi/auth_test.go`:

```go
package httpapi

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testMaster(fill byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = fill
	}
	return k
}

func testSigner(t *testing.T, fill byte) *sessionSigner {
	t.Helper()
	s, err := newSessionSigner(testMaster(fill))
	if err != nil {
		t.Fatalf("newSessionSigner: %v", err)
	}
	return s
}

// TestSessionCookieRoundTrip: lo que se emite se verifica.
func TestSessionCookieRoundTrip(t *testing.T) {
	s := testSigner(t, 1)
	ahora := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	if err := s.verify(s.issue(ahora), ahora.Add(time.Hour)); err != nil {
		t.Errorf("una cookie recién emitida no verifica: %v", err)
	}
}

// TestSessionCookieRejectsTampering es el punto entero del HMAC: cambiar un solo byte,
// en el payload o en la firma, invalida la cookie.
func TestSessionCookieRejectsTampering(t *testing.T) {
	s := testSigner(t, 1)
	ahora := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	bueno := s.issue(ahora)

	partes := strings.Split(bueno, ".")
	if len(partes) != 3 {
		t.Fatalf("formato inesperado de la cookie: %q", bueno)
	}

	casos := []struct{ nombre, valor string }{
		{"caducidad estirada", partes[0] + "." + "99999999999" + "." + partes[2]},
		{"firma cambiada", partes[0] + "." + partes[1] + "." + flipLast(partes[2])},
		{"versión cambiada", "v2." + partes[1] + "." + partes[2]},
		{"sin firma", partes[0] + "." + partes[1]},
		{"vacía", ""},
		{"basura", "no-es-una-cookie"},
		{"solo puntos", ".."},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if err := s.verify(c.valor, ahora); err == nil {
				t.Errorf("se aceptó una cookie manipulada: %q", c.valor)
			}
		})
	}
}

// TestSessionCookieExpires: pasada la vida útil, deja de valer aunque la firma sea buena.
func TestSessionCookieExpires(t *testing.T) {
	s := testSigner(t, 1)
	ahora := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	cookie := s.issue(ahora)

	if err := s.verify(cookie, ahora.Add(sessionTTL-time.Minute)); err != nil {
		t.Errorf("caducó antes de tiempo: %v", err)
	}
	err := s.verify(cookie, ahora.Add(sessionTTL+time.Minute))
	if !errors.Is(err, errCookieExpired) {
		t.Errorf("err = %v, quería errCookieExpired", err)
	}
}

// TestSessionCookieIsTiedToTheMasterKey: rotar la master key invalida todas las sesiones.
// Es el mecanismo de revocación global, así que tiene que funcionar.
func TestSessionCookieIsTiedToTheMasterKey(t *testing.T) {
	ahora := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	cookie := testSigner(t, 1).issue(ahora)

	if err := testSigner(t, 2).verify(cookie, ahora); err == nil {
		t.Error("una cookie firmada con otra master key fue aceptada")
	}
}

// TestSessionKeyIsNotTheMasterKey: la clave de firma se deriva, no se reutiliza. Si un día
// alguien "simplifica" esto, el test lo para.
func TestSessionKeyIsNotTheMasterKey(t *testing.T) {
	master := testMaster(7)
	s := testSigner(t, 7)

	if string(s.key) == string(master[:]) {
		t.Error("la clave de firma ES la master key; debe derivarse con HKDF")
	}
}

// flipLast cambia el último carácter por otro distinto, para corromper una firma sin
// cambiar su longitud.
func flipLast(s string) string {
	if s == "" {
		return "x"
	}
	ultimo := s[len(s)-1]
	nuevo := byte('A')
	if ultimo == 'A' {
		nuevo = 'B'
	}
	return s[:len(s)-1] + string(nuevo)
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/httpapi/ -v`
Expected: FAIL a compilar — el paquete no existe todavía.

- [ ] **Step 3: Implementar**

Crea `internal/httpapi/auth.go` con la parte de la cookie (el login y el middleware llegan
en la Task 6):

```go
// Package httpapi sirve la API HTTP y el WebSocket del panel (spec §9).
//
// Depende de store, crypto y relay, y ninguno de los tres depende de él: la serialización
// a JSON vive aquí y no se filtra hacia el motor.
package httpapi

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// sessionCookieName es el nombre de la cookie de sesión.
	sessionCookieName = "splitstream_session"

	// sessionTTL es lo que dura una sesión. Treinta días: es un servicio personal de un
	// solo usuario, y volver a escribir la contraseña a mitad de un directo es peor que
	// el riesgo de una sesión larga en un navegador que ya es de confianza.
	sessionTTL = 30 * 24 * time.Hour

	// sessionInfo es la etiqueta de HKDF. Aísla la clave de firma de la cookie del
	// material que cifra las claves de destino: un fallo en una no debe ayudar con la
	// otra. Si algún día cambia el formato de la cookie, se sube el /v y todas las
	// sesiones vivas quedan invalidadas de golpe.
	sessionInfo = "splitstream/session-cookie/v1"

	// cookieVersion prefija el valor para poder cambiar el formato sin ambigüedad.
	cookieVersion = "v1"
)

var (
	errCookieMalformed    = errors.New("cookie de sesión con formato inválido")
	errCookieBadSignature = errors.New("firma de la cookie de sesión inválida")
	errCookieExpired      = errors.New("sesión caducada")
)

// sessionSigner emite y verifica cookies de sesión firmadas.
//
// La sesión no tiene estado en el servidor: la cookie lleva su propia caducidad y va
// firmada, así que reiniciar el proceso no cierra la sesión de nadie. La contrapartida es
// que no se puede revocar UNA sesión: revocar es rotar la master key, que las tumba todas.
type sessionSigner struct{ key []byte }

func newSessionSigner(master [32]byte) (*sessionSigner, error) {
	key, err := hkdf.Key(sha256.New, master[:], nil, sessionInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("derivar la clave de sesión: %w", err)
	}
	return &sessionSigner{key: key}, nil
}

// issue emite el valor de una cookie válida hasta now+sessionTTL. El formato es
// "v1.<caducidad unix>.<hmac en base64url>".
func (s *sessionSigner) issue(now time.Time) string {
	payload := cookieVersion + "." + strconv.FormatInt(now.Add(sessionTTL).Unix(), 10)
	return payload + "." + s.sign(payload)
}

// verify comprueba firma y caducidad, en ese orden: sin firma válida, la caducidad que
// venga en la cookie no significa nada porque la escribió quien quiso.
func (s *sessionSigner) verify(value string, now time.Time) error {
	partes := strings.Split(value, ".")
	if len(partes) != 3 || partes[0] != cookieVersion {
		return errCookieMalformed
	}
	payload := partes[0] + "." + partes[1]

	// hmac.Equal compara en tiempo constante: comparar con == filtraría por temporización
	// cuántos bytes iniciales acertó quien lo intenta.
	if !hmac.Equal([]byte(partes[2]), []byte(s.sign(payload))) {
		return errCookieBadSignature
	}

	exp, err := strconv.ParseInt(partes[1], 10, 64)
	if err != nil {
		return errCookieMalformed
	}
	if now.After(time.Unix(exp, 0)) {
		return errCookieExpired
	}
	return nil
}

func (s *sessionSigner) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
```

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/httpapi/ -race -count=1 -v`
Expected: PASS los seis.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): cookie de sesión firmada con HMAC, sin estado en el servidor"
```

---

### Task 6: Servidor, errores JSON, login y logout

El esqueleto: el mux con los patrones de método de Go 1.22, la forma de error del spec §9,
el mapeo de las clases de error de la Task 2 a códigos HTTP, el middleware que exige
sesión, y los dos endpoints de autenticación con su limitador de intentos.

**Files:**
- Create: `internal/httpapi/server.go`, `internal/httpapi/errors.go`
- Modify: `internal/httpapi/auth.go` (añadir login, logout y el middleware)
- Test: `internal/httpapi/errors_test.go`, `internal/httpapi/login_test.go`
- Modify: `go.mod` (añadir `golang.org/x/time`)

**Interfaces:**
- Consumes: `sessionSigner` (Task 5), `store.ErrNotFound`/`ErrInvalidInput`/`ErrConflict`
  (Task 2), `crypto.VerifyPassword`.
- Produces:
  - `type Config struct { DB *store.DB; Cipher *crypto.Cipher; Engine *relay.Engine; Ingest Disconnecter; Sinks SinkBuilder; MasterKey [32]byte; Logger *slog.Logger; SecureCookies bool }`
  - `func New(cfg Config) (*Server, error)` y `func (s *Server) Handler() http.Handler`
  - `func writeJSON(w http.ResponseWriter, status int, v any)`
  - `func writeError(w http.ResponseWriter, status int, code, msg string)`
  - `func (s *Server) writeStoreError(w http.ResponseWriter, err error)`
  - `func (s *Server) requireSession(next http.Handler) http.Handler`
  Las tasks 8, 9 y 10 cuelgan sus handlers de este mux y usan estos tres ayudantes.
  `Disconnecter` y `SinkBuilder` son interfaces declaradas aquí y satisfechas por
  `rtmpio.Ingest` (Task 9) e `internal/sinks` (Task 8); hasta entonces, los tests las
  cumplen con dobles.

- [ ] **Step 1: Añadir la dependencia del limitador**

```bash
go get golang.org/x/time@latest
```

**No ejecutes `go mod tidy`** (Global Constraints). Comprueba que `golang.org/x/time` quedó
en el bloque de directas de `go.mod` y que no entró nada más:

```bash
git diff go.mod
```

- [ ] **Step 2: Escribir el test de la forma de los errores**

Crea `internal/httpapi/errors_test.go`:

```go
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

// TestErrorResponseShape fija el contrato del spec §9: TODO error de la API tiene esta
// forma, y el frontend de la fase 5 va a depender de ella.
func TestErrorResponseShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "invalid_input", "el nombre no puede estar vacío")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("código = %d, quería 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, quería application/json", ct)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("el cuerpo no es el JSON esperado: %v — %s", err, rec.Body.String())
	}
	if body.Error.Code != "invalid_input" {
		t.Errorf("code = %q", body.Error.Code)
	}
	if body.Error.Message != "el nombre no puede estar vacío" {
		t.Errorf("message = %q", body.Error.Message)
	}
}

// TestStoreErrorsMapToStatusCodes es la razón de ser de la Task 2: la API decide el código
// preguntando por la clase del error, no comparando cadenas.
func TestStoreErrorsMapToStatusCodes(t *testing.T) {
	casos := []struct {
		nombre string
		err    error
		status int
		code   string
	}{
		{"no encontrado", store.ErrDestinationNotFound, http.StatusNotFound, "not_found"},
		{"sesión no encontrada", store.ErrSessionNotFound, http.StatusNotFound, "not_found"},
		{"entrada inválida", store.ErrInvalidDestinationURL, http.StatusBadRequest, "invalid_input"},
		{"conflicto", store.ErrSettingsNotInitialized, http.StatusConflict, "conflict"},
		{"envuelto en contexto", fmt.Errorf("crear destino: %w", store.ErrInvalidDestinationURL), http.StatusBadRequest, "invalid_input"},
		{"desconocido", errors.New("se rompió el disco"), http.StatusInternalServerError, "internal"},
	}

	s := &Server{logger: discardLogger()}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.writeStoreError(rec, c.err)

			if rec.Code != c.status {
				t.Errorf("código = %d, quería %d", rec.Code, c.status)
			}
			var body struct {
				Error struct{ Code, Message string } `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("cuerpo: %v", err)
			}
			if body.Error.Code != c.code {
				t.Errorf("code = %q, quería %q", body.Error.Code, c.code)
			}
		})
	}
}

// TestInternalErrorsDoNotLeakDetails: un 500 no cuenta al cliente qué se rompió por
// dentro. El detalle va al log, que es donde lo lee quien opera el servicio.
func TestInternalErrorsDoNotLeakDetails(t *testing.T) {
	s := &Server{logger: discardLogger()}
	rec := httptest.NewRecorder()

	s.writeStoreError(rec, errors.New("no such file: /home/jadrian/secreto.db"))

	if body := rec.Body.String(); strings.Contains(body, "secreto.db") {
		t.Errorf("el 500 filtra detalles internos: %s", body)
	}
}
```

Añade `strings`, `io` y `log/slog` a los imports, y define en ESTE archivo el ayudante que
comparten los tests del paquete:

```go
func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
```

Va aquí y no en `login_test.go` porque este archivo se escribe antes: si lo definieras allí,
el paquete no compilaría entre el Step 2 y el Step 5.

- [ ] **Step 3: Ejecutar el test y verificar que falla**

Run: `go test ./internal/httpapi/ -run 'Error' -v`
Expected: FAIL por `undefined: writeError`, `undefined: Server`.

- [ ] **Step 4: Implementar los errores**

Crea `internal/httpapi/errors.go`:

```go
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/aprendomx/splitstream/internal/store"
)

// errorBody es la forma que el spec §9 fija para TODOS los errores de la API.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Los códigos que el frontend puede encontrarse. Son un conjunto cerrado a propósito: el
// cliente decide qué hacer mirando el code, no el texto, que es para humanos.
const (
	codeInvalidInput = "invalid_input"
	codeNotFound     = "not_found"
	codeConflict     = "conflict"
	codeUnauthorized = "unauthorized"
	codeRateLimited  = "rate_limited"
	codeInternal     = "internal"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		// Si la codificación falla a mitad, la cabecera ya salió: no hay forma de
		// convertirlo en un error HTTP. Se registra y se corta.
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: msg}})
}

// writeStoreError traduce un error del store a una respuesta HTTP preguntando por su
// CLASE, no por su identidad ni por su texto (spec §15.3).
//
// El caso por defecto es 500 y un mensaje genérico: un error que no sabemos clasificar es
// un fallo nuestro, y su detalle puede llevar rutas o estado interno, así que va al log y
// no al cliente.
func (s *Server) writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, codeInvalidInput, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, codeConflict, err.Error())
	default:
		s.logger.Error("fallo no clasificado de la API", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
	}
}
```

Los mensajes de `ErrNotFound`, `ErrInvalidInput` y `ErrConflict` sí van al cliente: son
textos que escribimos nosotros para explicar qué tiene de malo la petición, y no llevan
estado interno. El del 500 no.

- [ ] **Step 5: Escribir el test del login**

Crea `internal/httpapi/login_test.go`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

// discardLogger está en errors_test.go, del mismo paquete.

// newTestServer levanta un Server contra una base temporal, con la contraseña ya fijada.
func newTestServer(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	master := testMaster(1)
	cipher, err := crypto.NewCipher(master)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	if err := db.Bootstrap(ctx, cipher); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	hash, err := crypto.HashPassword("la-contraseña-de-prueba")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := db.SetPasswordHash(ctx, hash); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}

	srv, err := New(Config{DB: db, Cipher: cipher, MasterKey: master, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, db
}

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestLoginSetsAnHttpOnlyCookie: el spec §9 pide httpOnly explícitamente, porque es lo que
// impide que un XSS en el panel se lleve la sesión.
func TestLoginSetsAnHttpOnlyCookie(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"la-contraseña-de-prueba"}`)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("código = %d, quería 204: %s", rec.Code, rec.Body.String())
	}

	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no se puso la cookie de sesión")
	}
	if !cookie.HttpOnly {
		t.Error("la cookie no es httpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Error("la cookie no es SameSite=Lax")
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, quería /", cookie.Path)
	}
}

// TestLoginRejectsTheWrongPassword, y sin decir en qué falló: un mensaje que distinga
// "no hay contraseña puesta" de "esta no es" ayuda a quien lo intenta.
func TestLoginRejectsTheWrongPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"la-equivocada"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("código = %d, quería 401", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("se puso una cookie pese al fallo")
	}
}

// TestLoginNeverEchoesThePassword (spec §8).
func TestLoginNeverEchoesThePassword(t *testing.T) {
	srv, _ := newTestServer(t)
	const pw = "la-contraseña-de-prueba"

	for _, body := range []string{`{"password":"` + pw + `"}`, `{"password":"la-equivocada"}`} {
		rec := postJSON(t, srv.Handler(), "/api/auth/login", body)
		if strings.Contains(rec.Body.String(), pw) || strings.Contains(rec.Body.String(), "la-equivocada") {
			t.Errorf("la respuesta lleva la contraseña: %s", rec.Body.String())
		}
	}
}

// TestProtectedEndpointsNeedASession recorre TODOS los endpoints protegidos: sin cookie,
// 401. Es la lista que hay que ampliar cuando las tasks 8, 9 y 10 añadan los suyos.
func TestProtectedEndpointsNeedASession(t *testing.T) {
	srv, _ := newTestServer(t)

	protegidos := []struct{ metodo, path string }{
		{http.MethodGet, "/api/ingest"},
		{http.MethodPost, "/api/ingest/rotate-key"},
		{http.MethodGet, "/api/destinations"},
		{http.MethodPost, "/api/destinations"},
		{http.MethodPatch, "/api/destinations/1"},
		{http.MethodDelete, "/api/destinations/1"},
		{http.MethodPost, "/api/destinations/1/toggle"},
		{http.MethodPost, "/api/destinations/reorder"},
		{http.MethodGet, "/api/destinations/1/key"},
		{http.MethodGet, "/api/status"},
		{http.MethodGet, "/api/events"},
		{http.MethodGet, "/ws"},
	}

	for _, p := range protegidos {
		t.Run(p.metodo+" "+p.path, func(t *testing.T) {
			req := httptest.NewRequest(p.metodo, p.path, strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("código = %d, quería 401", rec.Code)
			}
		})
	}
}

// TestSessionCookieOpensTheDoor: el camino feliz completo, login y después una petición
// autenticada.
func TestSessionCookieOpensTheDoor(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	rec := postJSON(t, h, "/api/auth/login", `{"password":"la-contraseña-de-prueba"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/destinations", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)

	if rec2.Code != http.StatusOK {
		t.Errorf("código = %d, quería 200: %s", rec2.Code, rec2.Body.String())
	}
}

// TestLogoutInvalidatesTheCookie: cerrar sesión tiene que dejarte fuera de verdad.
func TestLogoutInvalidatesTheCookie(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	login := postJSON(t, h, "/api/auth/login", `{"password":"la-contraseña-de-prueba"}`)
	cookies := login.Result().Cookies()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}

	var borrada *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			borrada = c
		}
	}
	if borrada == nil || borrada.MaxAge >= 0 {
		t.Error("el logout no manda al navegador borrar la cookie")
	}
}

// TestLoginIsRateLimited: sin esto, la contraseña del panel se puede probar a fuerza bruta
// tan rápido como aguante el argon2id.
func TestLoginIsRateLimited(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	var limitado bool
	for i := 0; i < 30; i++ {
		rec := postJSON(t, h, "/api/auth/login", `{"password":"la-equivocada"}`)
		if rec.Code == http.StatusTooManyRequests {
			limitado = true
			break
		}
	}
	if !limitado {
		t.Error("treinta intentos fallidos seguidos y ninguno fue limitado")
	}
}

// TestRateLimitDoesNotBlockTheRightPassword: el limitador no debe dejarte fuera de tu
// propio panel si te equivocas un par de veces al escribir.
func TestRateLimitDoesNotBlockTheRightPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	for i := 0; i < 2; i++ {
		postJSON(t, h, "/api/auth/login", `{"password":"la-equivocada"}`)
	}
	rec := postJSON(t, h, "/api/auth/login", `{"password":"la-contraseña-de-prueba"}`)
	if rec.Code != http.StatusNoContent {
		t.Errorf("código = %d, quería 204 tras dos fallos", rec.Code)
	}
}

// TestLoginWithoutAPasswordConfigured: si nadie ejecutó -setpassword, el panel no está
// listo. 409, no 401: la petición es correcta, el servicio no está configurado.
func TestLoginWithoutAPasswordConfigured(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	master := testMaster(1)
	cipher, _ := crypto.NewCipher(master)
	if err := db.Bootstrap(ctx, cipher); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	srv, err := New(Config{DB: db, Cipher: cipher, MasterKey: master, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"lo-que-sea"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("código = %d, quería 409: %s", rec.Code, rec.Body.String())
	}
	var body errorBody
	json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body.Error.Message, "setpassword") {
		t.Errorf("el mensaje no dice cómo arreglarlo: %q", body.Error.Message)
	}
}
```

`TestProtectedEndpointsNeedASession` y `TestSessionCookieOpensTheDoor` exigen que las rutas
existan ya. Regístralas todas en el mux desde esta tarea; las que aún no tienen handler
propio se enganchan a uno que devuelve `501 Not Implemented`, y cada task posterior lo
sustituye por el suyo. Así la lista de rutas y la de permisos se escriben una sola vez.

- [ ] **Step 6: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/httpapi/ -v`
Expected: FAIL por `undefined: New`, `undefined: Config`, `undefined: Server`.

- [ ] **Step 7: Implementar el servidor**

Crea `internal/httpapi/server.go`:

```go
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// Disconnecter corta la publicación de ingesta en curso sin dejar de escuchar. Lo cumple
// *rtmpio.Ingest (Task 9). Es una interfaz y no el tipo concreto para que este paquete no
// importe go-rtmp ni siquiera de forma transitiva.
type Disconnecter interface {
	DisconnectPublisher() int
}

// SinkBuilder construye el sink de un destino para aplicar cambios en caliente. Lo cumple
// *sinks.Factory (Task 8).
type SinkBuilder interface {
	Build(ctx context.Context, d store.Destination) (*relay.Sink, error)
}

// Config son las dependencias del servidor. Todas obligatorias salvo Logger, Ingest y
// Sinks, que pueden ser nil en los tests que no los ejercitan.
type Config struct {
	DB     *store.DB
	Cipher *crypto.Cipher
	Engine *relay.Engine
	Ingest Disconnecter
	Sinks  SinkBuilder
	// MasterKey solo se usa para derivar la clave de firma de la cookie. No se guarda.
	MasterKey [32]byte
	Logger    *slog.Logger
	// SecureCookies marca la cookie como Secure. Va aquí y no se deduce de la petición
	// porque en producción el TLS lo termina un proxy y el servidor solo ve HTTP.
	SecureCookies bool
}

// Server sirve la API del spec §9.
type Server struct {
	db      *store.DB
	cipher  *crypto.Cipher
	engine  *relay.Engine
	ingest  Disconnecter
	sinks   SinkBuilder
	signer  *sessionSigner
	limiter *loginLimiter
	logger  *slog.Logger
	secure  bool
	mux     *http.ServeMux
}

func New(cfg Config) (*Server, error) {
	if cfg.DB == nil {
		return nil, errors.New("httpapi: falta DB")
	}
	if cfg.Cipher == nil {
		return nil, errors.New("httpapi: falta Cipher")
	}
	signer, err := newSessionSigner(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		db: cfg.DB, cipher: cfg.Cipher, engine: cfg.Engine,
		ingest: cfg.Ingest, sinks: cfg.Sinks,
		signer: signer, limiter: newLoginLimiter(), logger: logger,
		secure: cfg.SecureCookies, mux: http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

// routes registra las rutas del spec §9. Los patrones con método son de Go 1.22, así que
// no hace falta router externo.
//
// Las rutas se declaran TODAS aquí, incluidas las que aún no tienen handler: así la lista
// de qué existe y qué necesita sesión se escribe en un solo sitio, y no se puede añadir un
// endpoint olvidándose de protegerlo.
func (s *Server) routes() {
	// Públicas: son el camino para conseguir una sesión.
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)

	protegida := func(pattern string, h http.HandlerFunc) {
		s.mux.Handle(pattern, s.requireSession(h))
	}

	protegida("GET /api/ingest", s.notImplemented)
	protegida("POST /api/ingest/rotate-key", s.notImplemented)
	protegida("GET /api/destinations", s.notImplemented)
	protegida("POST /api/destinations", s.notImplemented)
	protegida("PATCH /api/destinations/{id}", s.notImplemented)
	protegida("DELETE /api/destinations/{id}", s.notImplemented)
	protegida("POST /api/destinations/{id}/toggle", s.notImplemented)
	protegida("POST /api/destinations/reorder", s.notImplemented)
	protegida("GET /api/destinations/{id}/key", s.notImplemented)
	protegida("GET /api/status", s.notImplemented)
	protegida("GET /api/events", s.notImplemented)
	protegida("GET /ws", s.notImplemented)
}

// notImplemented es el andamio de las tasks 8 a 10. Cada una sustituye las suyas.
func (s *Server) notImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, codeInternal, "todavía no implementado")
}

// clientIP saca la IP para el limitador. Sin confiar en X-Forwarded-For: quien llega
// directo puede inventárselo, y en el despliegue del spec §12 el proxy es local.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
```

`POST /api/destinations/reorder` y `PATCH /api/destinations/{id}` conviven en el mux de Go
1.22 sin ambigüedad: los patrones literales ganan a los que tienen wildcard. Compruébalo —
si `reorder` acabara entrando por el handler de `{id}`, el test de la Task 8 lo cazará.

- [ ] **Step 8: Implementar login, logout y el middleware**

Añade a `internal/httpapi/auth.go`:

```go
// loginLimiter acota los intentos de login por IP.
//
// Sin esto, la contraseña del panel se puede probar a fuerza bruta tan rápido como aguante
// el argon2id, que es lento pero no infinitamente. El límite es generoso a propósito: debe
// molestar a un script, no a alguien que se equivoca dos veces al escribir.
type loginLimiter struct {
	mu  sync.Mutex
	por map[string]*rate.Limiter
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{por: make(map[string]*rate.Limiter)}
}

// allow consume un intento. Ráfaga de 5 y reposición de uno cada 10 s.
func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	lim, ok := l.por[ip]
	if !ok {
		lim = rate.NewLimiter(rate.Every(10*time.Second), 5)
		l.por[ip] = lim
	}
	return lim.Allow()
}

type loginRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, codeRateLimited,
			"demasiados intentos; espera un momento")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidInput, "cuerpo JSON inválido")
		return
	}

	settings, err := s.db.Settings(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if settings.PasswordHash == "" {
		writeError(w, http.StatusConflict, codeConflict,
			"no hay contraseña configurada: ejecuta `splitstream -setpassword`")
		return
	}

	ok, err := crypto.VerifyPassword(settings.PasswordHash, req.Password)
	if err != nil {
		s.logger.Error("no se pudo verificar la contraseña", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	if !ok {
		// Sin decir qué falló, y sin registrar la contraseña probada (spec §8).
		s.logger.Warn("intento de login fallido", "ip", clientIP(r))
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "contraseña incorrecta")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.signer.issue(time.Now()),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// requireSession exige una cookie de sesión válida.
//
// Todas las respuestas de fallo son el mismo 401 sin detalle: distinguir "no hay cookie"
// de "la firma no cuadra" de "caducó" solo le sirve a quien está probando.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "sesión requerida")
			return
		}
		if err := s.signer.verify(c.Value, time.Now()); err != nil {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "sesión requerida")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

Añade a los imports de `auth.go`: `encoding/json`, `net/http`, `sync`,
`golang.org/x/time/rate` y el paquete `crypto` del proyecto.

- [ ] **Step 9: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/httpapi/ -race -count=1 -v`
Expected: PASS todos menos `TestSessionCookieOpensTheDoor`, que espera 200 de
`GET /api/destinations` y de momento recibe 501. Ajusta ese test para aceptar 501 **con un
comentario que diga que la Task 8 lo sube a 200**, y súbelo cuando llegues ahí. No lo
borres: es el único test del camino feliz de la sesión.

- [ ] **Step 10: Commit**

```bash
git add internal/httpapi/ go.mod go.sum
git commit -m "feat(httpapi): servidor, errores JSON del spec §9, login y logout"
```

---

### Task 7: DTOs (spec §15.2)

`relay.Metrics` no lleva tags `json` y no se los vamos a poner: el motor no debe saber que
existe la API, igual que hoy no sabe de `go-rtmp` ni de `database/sql`. La conversión vive
aquí, y el precio —duplicar diez nombres de campo— se paga con un test que impide que la
copia se desincronice en silencio.

**Files:**
- Create: `internal/httpapi/dto.go`, `internal/httpapi/dto_test.go`

**Interfaces:**
- Consumes: `relay.Metrics`, `store.Destination`, `store.Event`, `store.Session`.
- Produces: `metricsDTO`, `destinationDTO`, `eventDTO`, `sessionDTO`, `statusDTO`, y las
  funciones `newMetricsDTO`, `newDestinationDTO`, `newEventDTO`. Las tasks 8, 9 y 10 las
  usan; el WebSocket empuja exactamente el mismo `statusDTO` que `GET /api/status`.

- [ ] **Step 1: Escribir el test que falla**

Crea `internal/httpapi/dto_test.go`:

```go
package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// TestMetricsDTOCoversEveryEngineField es el test que sostiene la decisión del spec §15.2.
//
// Duplicar los campos del motor en un DTO solo es seguro si alguien vigila la copia. Este
// test recorre relay.Metrics por reflexión y falla si aparece un campo que nadie mapeó:
// añadir una métrica al motor obliga entonces a decidir su nombre público, en vez de que
// desaparezca en silencio del WebSocket.
func TestMetricsDTOCoversEveryEngineField(t *testing.T) {
	mapeados := map[string]bool{
		"State":          true,
		"Degraded":       true,
		"BytesSent":      true,
		"BitrateBPS":     true,
		"DroppedFrames":  true,
		"Uptime":         true,
		"Reconnections":  true,
		"LastError":      true,
		"QueuedBytes":    true,
		"QueuedMessages": true,
	}

	rt := reflect.TypeOf(relay.Metrics{})
	for i := 0; i < rt.NumField(); i++ {
		nombre := rt.Field(i).Name
		if !mapeados[nombre] {
			t.Errorf("relay.Metrics.%s no está mapeado en metricsDTO.\n"+
				"Si has añadido un campo al motor, decide su nombre público en dto.go "+
				"y añádelo a este mapa. Si de verdad no debe salir por la API, añádelo "+
				"igualmente aquí con un comentario que diga por qué.", nombre)
		}
	}

	// Y al revés: un nombre en el mapa que ya no exista en el motor es basura acumulada.
	for nombre := range mapeados {
		if _, ok := rt.FieldByName(nombre); !ok {
			t.Errorf("%q está en el mapa pero ya no existe en relay.Metrics", nombre)
		}
	}
}

// TestNewMetricsDTOCopiesTheValues: que los campos estén mapeados no significa que se
// copien bien. Valores distintos en cada campo para que un cruce se note.
func TestNewMetricsDTOCopiesTheValues(t *testing.T) {
	m := relay.Metrics{
		State: "live", Degraded: true,
		BytesSent: 1_000, BitrateBPS: 2_000, DroppedFrames: 3,
		Uptime: 90 * time.Second, Reconnections: 4,
		LastError: "se cayó la red", QueuedBytes: 5, QueuedMessages: 6,
	}

	got := newMetricsDTO(m)

	if got.State != "live" || !got.Degraded {
		t.Errorf("estado mal copiado: %+v", got)
	}
	if got.BytesSent != 1000 || got.BitrateBPS != 2000 || got.DroppedFrames != 3 {
		t.Errorf("contadores mal copiados: %+v", got)
	}
	if got.Reconnections != 4 || got.LastError != "se cayó la red" {
		t.Errorf("reconexión mal copiada: %+v", got)
	}
	if got.QueuedBytes != 5 || got.QueuedMessages != 6 {
		t.Errorf("cola mal copiada: %+v", got)
	}
	// Uptime sale en segundos: time.Duration serializa como nanosegundos en un int64, que
	// es un número enorme y sin unidad para el frontend.
	if got.UptimeSeconds != 90 {
		t.Errorf("UptimeSeconds = %d, quería 90", got.UptimeSeconds)
	}
}

// TestDestinationDTONeverCarriesThePlainKey es la propiedad del spec §8 en la frontera de
// serialización: el listado enseña la máscara, nunca la clave.
func TestDestinationDTONeverCarriesThePlainKey(t *testing.T) {
	d := store.Destination{
		ID: 1, Name: "yt", Platform: store.PlatformYouTube,
		RTMPURL: "rtmp://a.rtmp.youtube.com/live2",
		KeyMask: "••••abcd", Enabled: true, SortOrder: 0,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	blob, err := json.Marshal(newDestinationDTO(d, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var suelto map[string]any
	if err := json.Unmarshal(blob, &suelto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, prohibido := range []string{"key", "stream_key", "stream_key_encrypted"} {
		if _, ok := suelto[prohibido]; ok {
			t.Errorf("el DTO expone el campo %q", prohibido)
		}
	}
	if suelto["key_mask"] != "••••abcd" {
		t.Errorf("key_mask = %v", suelto["key_mask"])
	}
}

// TestDTOFieldNamesAreSnakeCase: el frontend de la fase 5 va a depender de estos nombres,
// así que conviene que sean consistentes desde el principio.
func TestDTOFieldNamesAreSnakeCase(t *testing.T) {
	tipos := []any{metricsDTO{}, destinationDTO{}, eventDTO{}, sessionDTO{}, ingestDTO{}, statusDTO{}}

	for _, v := range tipos {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			tag := rt.Field(i).Tag.Get("json")
			if tag == "" {
				t.Errorf("%s.%s no tiene tag json", rt.Name(), rt.Field(i).Name)
				continue
			}
			nombre, _, _ := strings.Cut(tag, ",")
			if nombre != strings.ToLower(nombre) {
				t.Errorf("%s.%s usa %q; los nombres van en snake_case", rt.Name(), rt.Field(i).Name, nombre)
			}
		}
	}
}
```

Añade `strings` a los imports.

- [ ] **Step 2: Ejecutar el test y verificar que falla**

Run: `go test ./internal/httpapi/ -run DTO -v`
Expected: FAIL por `undefined: metricsDTO`, `newMetricsDTO`, etc.

- [ ] **Step 3: Implementar**

Crea `internal/httpapi/dto.go`:

```go
package httpapi

import (
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// Los DTO son el contrato público de la API. Existen aparte de los tipos del motor y del
// store a propósito (spec §15.2): así renombrar un campo interno no rompe al frontend, y
// el motor sigue sin saber que existe JSON. El precio es una copia, y lo cobra
// TestMetricsDTOCoversEveryEngineField, que falla si el motor gana un campo que nadie mapeó.

type metricsDTO struct {
	State          string `json:"state"`
	Degraded       bool   `json:"degraded"`
	BytesSent      uint64 `json:"bytes_sent"`
	BitrateBPS     uint64 `json:"bitrate_bps"`
	DroppedFrames  uint64 `json:"dropped_frames"`
	UptimeSeconds  int64  `json:"uptime_seconds"`
	Reconnections  uint64 `json:"reconnections"`
	LastError      string `json:"last_error"`
	QueuedBytes    int    `json:"queued_bytes"`
	QueuedMessages int    `json:"queued_messages"`
}

// newMetricsDTO copia las métricas del motor.
//
// Uptime pasa a segundos: un time.Duration serializa como nanosegundos en un int64, que
// para el frontend es un número enorme sin unidad ninguna.
func newMetricsDTO(m relay.Metrics) metricsDTO {
	return metricsDTO{
		State:          m.State,
		Degraded:       m.Degraded,
		BytesSent:      m.BytesSent,
		BitrateBPS:     m.BitrateBPS,
		DroppedFrames:  m.DroppedFrames,
		UptimeSeconds:  int64(m.Uptime / time.Second),
		Reconnections:  m.Reconnections,
		LastError:      m.LastError,
		QueuedBytes:    m.QueuedBytes,
		QueuedMessages: m.QueuedMessages,
	}
}

type destinationDTO struct {
	ID        int64       `json:"id"`
	Name      string      `json:"name"`
	Platform  string      `json:"platform"`
	RTMPURL   string      `json:"rtmp_url"`
	KeyMask   string      `json:"key_mask"`
	Enabled   bool        `json:"enabled"`
	SortOrder int         `json:"sort_order"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Metrics   *metricsDTO `json:"metrics"`
}

// newDestinationDTO. m es nil cuando no hay sesión viva o el destino está apagado: el
// frontend distingue "sin métricas" de "métricas en cero" por el null.
//
// La clave NO aparece, ni cifrada ni en claro: solo la máscara que el store ya guarda
// desnormalizada (spec §8).
func newDestinationDTO(d store.Destination, m *relay.Metrics) destinationDTO {
	dto := destinationDTO{
		ID: d.ID, Name: d.Name, Platform: string(d.Platform),
		RTMPURL: d.RTMPURL, KeyMask: d.KeyMask, Enabled: d.Enabled,
		SortOrder: d.SortOrder, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
	if m != nil {
		x := newMetricsDTO(*m)
		dto.Metrics = &x
	}
	return dto
}

type eventDTO struct {
	ID            int64     `json:"id"`
	SessionID     *int64    `json:"session_id"`
	DestinationID *int64    `json:"destination_id"`
	Level         string    `json:"level"`
	Kind          string    `json:"kind"`
	Message       string    `json:"message"`
	CreatedAt     time.Time `json:"created_at"`
}

func newEventDTO(e store.Event) eventDTO {
	return eventDTO{
		ID: e.ID, SessionID: e.SessionID, DestinationID: e.DestinationID,
		Level: string(e.Level), Kind: e.Kind, Message: e.Message, CreatedAt: e.CreatedAt,
	}
}

// sessionDTO describe la sesión de ingesta en curso. Live en false significa que no hay
// nadie publicando, y entonces el resto de los campos no significan nada.
type sessionDTO struct {
	Live       bool       `json:"live"`
	ID         int64      `json:"id"`
	StartedAt  *time.Time `json:"started_at"`
	Width      *int       `json:"width"`
	Height     *int       `json:"height"`
	BitrateBPS *int       `json:"bitrate_bps"`
}

type ingestDTO struct {
	URL     string `json:"url"`
	App     string `json:"app"`
	KeyMask string `json:"key_mask"`
}

// statusDTO es lo que devuelve GET /api/status y lo que el WebSocket empuja cada segundo.
// Es el MISMO tipo a propósito: el spec §10 dice que el snapshot inicial de la UI viene
// del GET para no depender de que el WS conecte primero, y eso solo funciona si las dos
// fuentes tienen exactamente la misma forma.
type statusDTO struct {
	Ingest       ingestDTO        `json:"ingest"`
	Session      sessionDTO       `json:"session"`
	Destinations []destinationDTO `json:"destinations"`
}
```

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/httpapi/ -race -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): DTOs con el test que impide que se desincronicen del motor"
```

---

### Task 8: Fábrica de sinks y endpoints de destinos

Dos cosas que van juntas porque la segunda necesita la primera. `main.go` construye hoy los
sinks dentro de un closure; la API necesita hacer lo mismo para aplicar un alta o una
edición en caliente, y copiar ese código sería garantizar que las dos copias divergan.

**Files:**
- Create: `internal/sinks/factory.go`, `internal/sinks/factory_test.go`
- Create: `internal/httpapi/destinations.go`, `internal/httpapi/destinations_test.go`
- Modify: `internal/httpapi/server.go` (sustituir los `notImplemented` de destinos)
- Modify: `cmd/splitstream/main.go` (usar la fábrica en el `SinkProvider`)

**Interfaces:**
- Consumes: `store.DestinationKeyForRelay` (Task 3), `writeJSON`/`writeError`/
  `writeStoreError` (Task 6), `newDestinationDTO` (Task 7).
- Produces:
  - `func sinks.NewFactory(db *store.DB, c *crypto.Cipher, logger *slog.Logger) *sinks.Factory`
  - `func (f *Factory) Build(ctx context.Context, d store.Destination) (*relay.Sink, error)`
  - `func (f *Factory) BuildEnabled(ctx context.Context) ([]*relay.Sink, error)`
  `*sinks.Factory` satisface `httpapi.SinkBuilder`.

- [ ] **Step 1: Extraer la fábrica**

Crea `internal/sinks/factory.go` moviendo, **sin cambiar la lógica**, el cuerpo del closure
que hoy está en `engine.SetSinkProvider` dentro de `cmd/splitstream/main.go`. Léelo primero
entero: incluye el descifrado de la clave, la validación temprana con `rtmpio.NewPublisher`,
el `NewPub` que construye un publisher nuevo por reconexión, y el `OnEvent` que persiste.
Todo eso se conserva tal cual; lo único que cambia es dónde vive.

```go
// Package sinks construye los sinks de retransmisión a partir de lo que hay en la base de
// datos.
//
// Existe como paquete propio porque lo necesitan dos sitios: el motor, que arma los sinks
// de cada sesión, y la API, que aplica en caliente el alta o la edición de un destino. La
// alternativa —un closure en main.go y una copia en httpapi— garantizaba que las dos
// versiones divergieran.
//
// Es la capa de composición: importa store, crypto, rtmpio y relay. Por eso no puede vivir
// dentro de relay, que no debe conocer ni la base de datos ni la librería RTMP.
package sinks
```

La fábrica guarda `db`, `cipher` y `logger`, y `Build` usa
`db.DestinationKeyForRelay(ctx, cipher, d.ID)` — la variante que **no** audita (Task 3).
`BuildEnabled` lista los destinos, se salta los deshabilitados y llama a `Build` por cada
uno, registrando y continuando si uno falla, exactamente como hace hoy `main.go`.

- [ ] **Step 2: Escribir el test de la fábrica**

Crea `internal/sinks/factory_test.go` con, al menos:

```go
// TestBuildRejectsAMisconfiguredDestination: validar al construir evita crear un sink que
// no podría conectar jamás y que se pasaría la vida reintentando.
func TestBuildRejectsAMisconfiguredDestination(t *testing.T) { /* URL http:// → error */ }

// TestBuildEnabledSkipsDisabledDestinations
func TestBuildEnabledSkipsDisabledDestinations(t *testing.T) { /* 3 destinos, 1 apagado → 2 sinks */ }

// TestBuildEnabledSurvivesOneBadDestination: un destino roto no puede impedir que los
// demás salgan al aire.
func TestBuildEnabledSurvivesOneBadDestination(t *testing.T) { /* 1 malo + 2 buenos → 2 sinks, sin error */ }

// TestBuildDoesNotAudit: construir sinks no es revelar una clave (spec §15.5, Task 3).
func TestBuildDoesNotAudit(t *testing.T) { /* tras BuildEnabled, 0 eventos key_revealed */ }
```

Escríbelos completos siguiendo el estilo de `internal/relay/sink_test.go`: tabla de casos,
mensajes de error que digan qué se quería. `TestBuildDoesNotAudit` es el que impide que
alguien "simplifique" la Task 3 volviendo a llamar a `RevealDestinationKey` desde aquí.

- [ ] **Step 3: Cambiar `main.go` para usar la fábrica**

Sustituye el closure de `engine.SetSinkProvider` por:

```go
	factory := sinks.NewFactory(db, cipher, logger)
	engine.SetSinkProvider(func() ([]*relay.Sink, error) {
		return factory.BuildEnabled(ctx)
	})
```

Run: `go test ./... -race -count=1` — la suite entera, incluida la de integración si tienes
los sinks levantados. Este paso no debe cambiar ningún comportamiento observable: si algo
se pone rojo, el movimiento no fue mecánico y hay que revisarlo.

- [ ] **Step 4: Commit del refactor, separado**

```bash
git add internal/sinks/ cmd/splitstream/main.go
git commit -m "refactor: extraer la construcción de sinks de main.go a internal/sinks"
```

Va en su propio commit a propósito: un refactor sin cambio de comportamiento y una
funcionalidad nueva no deben compartir commit, porque entonces `git bisect` no puede
distinguirlos.

- [ ] **Step 5: Escribir los tests de los endpoints de destinos**

Crea `internal/httpapi/destinations_test.go`. Los casos que hay que cubrir, cada uno como
su propio `func Test...`:

- `TestListDestinationsReturnsThemInSortOrder` — tres destinos, orden por `sort_order`.
- `TestListDestinationsNeverIncludesAKey` — recorre el JSON crudo y comprueba que la clave
  que se guardó no aparece en ninguna parte del cuerpo.
- `TestCreateDestinationPersistsAndReturns201` — con `Location` apuntando al recurso.
- `TestCreateDestinationRejectsBadInput` — tabla: URL `http://`, sin nombre, plataforma
  inventada, clave vacía, JSON malformado. Todos 400 con `code: "invalid_input"`.
- `TestPatchDestinationLeavesUnsetFieldsAlone` — manda solo `name`; comprueba que la URL,
  la plataforma y la clave no cambiaron.
- `TestPatchDestinationCanReplaceTheKey` — manda `key`; comprueba que `key_mask` cambió y
  que la respuesta no lleva la clave nueva.
- `TestDeleteDestinationReturns204AndIsGone`.
- `TestToggleFlipsEnabled` — dos llamadas, vuelve al estado inicial.
- `TestReorderPersistsTheWholeOrder` — manda los ids al revés, comprueba el listado.
- `TestReorderRejectsUnknownIDs` — 400, y el orden anterior intacto.
- `TestReorderRouteIsNotSwallowedByThePatchWildcard` — `POST /api/destinations/reorder`
  debe llegar al handler de reorder y no al de `{id}` con id="reorder". Este test existe
  porque es el fallo más probable del enrutado y daría un 400 confuso.
- `TestRevealKeyReturnsTheKeyAndAudits` — el ÚNICO endpoint que devuelve una clave en
  claro; y deja el evento `key_revealed` (Task 3).
- `TestNotFoundForUnknownID` — 404 con `code: "not_found"` en PATCH, DELETE, toggle y key.

Para el efecto en caliente:

```go
// TestCreateDestinationAppliesToTheLiveSessionImmediately: si el usuario añade un destino
// mientras transmite, tiene que empezar a salir por ahí sin cortar la transmisión. Si no,
// el toggle de la UI mentiría durante todo el directo (decisión nº 4 del plan).
func TestCreateDestinationAppliesToTheLiveSessionImmediately(t *testing.T) {
	// Con un hub de verdad y un doble de SinkBuilder que registre las llamadas:
	// Hub.Len() debe crecer en 1 tras el POST, y solo si hay sesión viva.
}

// TestCreateDestinationDoesNothingHotWhenThereIsNoSession: sin sesión, el destino se
// persiste y ya está; no se conecta nada hasta la próxima transmisión (spec §6.5).
func TestCreateDestinationDoesNothingHotWhenThereIsNoSession(t *testing.T) {}

// TestDeleteDestinationStopsItsSinkWhenLive
func TestDeleteDestinationStopsItsSinkWhenLive(t *testing.T) {}

// TestToggleOffStopsTheSinkAndToggleOnStartsIt
func TestToggleOffStopsTheSinkAndToggleOnStartsIt(t *testing.T) {}
```

- [ ] **Step 6: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/httpapi/ -run Destination -v`
Expected: FAIL — los endpoints devuelven 501.

- [ ] **Step 7: Implementar los handlers**

Crea `internal/httpapi/destinations.go`. Las decisiones que el código debe respetar:

1. **El id se saca con `r.PathValue("id")`** y se parsea con `strconv.ParseInt`. Un id no
   numérico es `400 invalid_input`, no 404: la ruta existe, lo que mandaron no vale.
2. **El cuerpo se lee con `http.MaxBytesReader`** — 64 KiB bastan de sobra— para que un
   cuerpo enorme no consuma memoria del proceso que está retransmitiendo.
3. **La clave entra como `string` en el JSON y se convierte a `crypto.Secret` en cuanto se
   lee.** Cuanto menos viva como `string`, menos sitios puede acabar impresa. Nunca se
   devuelve, salvo en el endpoint de revelado.
4. **En PATCH, un campo ausente y un campo vacío no son lo mismo.** Decodifica en un struct
   de punteros para poder distinguirlos, y pásalos tal cual a `store.DestinationPatch`, que
   ya usa punteros con esa semántica.
5. **El efecto en caliente va DESPUÉS de que la escritura en la base haya ido bien**, nunca
   antes: si falla el hub, el estado persistido sigue siendo correcto y el destino entrará
   en la siguiente sesión. Al revés, un fallo de la base dejaría un sink conectado que no
   corresponde a ninguna fila.
6. **El efecto en caliente solo se intenta si hay sesión viva**, que se consulta con
   `s.engine.SessionID() != 0`. Si `s.engine` o `s.sinks` son nil (tests), se salta.
7. **Un fallo del efecto en caliente se registra y NO convierte la respuesta en un error**:
   la petición hizo lo que pedía —persistir el cambio—, y decir 500 haría que el usuario lo
   repitiera y creara un destino duplicado.

- [ ] **Step 8: Sustituir los `notImplemented` de destinos en `routes()`**

Y sube `TestSessionCookieOpensTheDoor` (Task 6, Step 9) de 501 a 200, que era el objetivo.

- [ ] **Step 9: Ejecutar los tests y verificar que pasan**

Run: `go test ./... -race -count=1`
Expected: PASS entero.

- [ ] **Step 10: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): endpoints de destinos, con efecto en caliente sobre la sesión viva"
```

---

### Task 9: Ingesta, estado y eventos

Los cinco endpoints que quedan sin WebSocket. El único con enjundia es la rotación de la
clave con `disconnect_now`, que necesita algo que `rtmpio.Ingest` todavía no sabe hacer:
cortar al publisher sin dejar de escuchar. `Ingest.Close()` cierra también el listener, así
que no sirve.

**Files:**
- Modify: `internal/rtmpio/ingest.go` (añadir `DisconnectPublisher`)
- Create: `internal/httpapi/ingest.go`, `internal/httpapi/status.go` y sus tests
- Modify: `internal/httpapi/server.go` (sustituir los `notImplemented` restantes salvo `/ws`)
- Test: `internal/rtmpio/ingest_test.go` (añadir)

**Interfaces:**
- Consumes: `store.RotateIngestKey`, `store.Settings`, `store.RecentEvents`,
  `store.SessionByID`, `relay.Engine.SessionID`, `relay.Engine.Snapshot`, los DTO (Task 7).
- Produces: `func (i *Ingest) DisconnectPublisher() int` — cierra las conexiones activas y
  devuelve cuántas cerró; el listener sigue escuchando. Satisface `httpapi.Disconnecter`.

- [ ] **Step 1: Escribir el test de `DisconnectPublisher`**

Añade a `internal/rtmpio/ingest_test.go`:

```go
// TestDisconnectPublisherCutsTheStreamButKeepsListening: rotar la clave con
// disconnect_now debe echar a quien está publicando con la clave vieja, y dejar el
// servidor listo para que vuelva a entrar con la nueva. Close() no sirve: cierra también
// el listener y haría falta reiniciar el proceso.
func TestDisconnectPublisherCutsTheStreamButKeepsListening(t *testing.T) {
	// 1. Levanta la ingesta sobre un listener temporal.
	// 2. Conecta un publisher y publica un frame: debe funcionar.
	// 3. Llama a DisconnectPublisher(): debe devolver 1 y dispararse OnPublishEnd.
	// 4. Conecta un publisher NUEVO: debe poder conectar y publicar.
	// 5. Y solo entonces, Close() cierra todo.
}

// TestDisconnectPublisherWithNobodyConnected: no debe fallar ni colgarse; devuelve 0.
func TestDisconnectPublisherWithNobodyConnected(t *testing.T) {}

// TestDisconnectPublisherIsSafeConcurrently: la API puede llamarlo mientras el publisher
// se desconecta solo. Con -race y varias goroutines.
func TestDisconnectPublisherIsSafeConcurrently(t *testing.T) {}
```

Copia la forma de levantar la ingesta de los tests que ya existen en ese archivo; la fase 2
dejó ahí el andamiaje del seguimiento de conexiones (`track`/`untrack`) que esta tarea
reutiliza.

- [ ] **Step 2: Implementar `DisconnectPublisher`**

`Ingest` ya lleva un mapa de conexiones activas desde la fase 2 —se añadió porque
`rtmp.Server.Close()` de go-rtmp v0.0.7 solo cierra el listener y no rastrea las conexiones
aceptadas—. `DisconnectPublisher` cierra esas conexiones y vacía el mapa, **sin** tocar el
listener ni el `rtmp.Server`. Reutiliza el mismo mutex; no añadas otro.

- [ ] **Step 3: Los endpoints de ingesta**

`GET /api/ingest` devuelve `ingestDTO` con la URL de ingesta, la app y la máscara de la
clave. La URL se compone de la dirección RTMP configurada y la app; **la clave no va en la
URL**, va aparte y enmascarada.

`POST /api/ingest/rotate-key` con cuerpo `{"disconnect_now": bool}`:
1. `db.RotateIngestKey` (ya existe).
2. Registra un evento `ingest_key_rotated`, sin la clave dentro.
3. Si `disconnect_now`, llama a `s.ingest.DisconnectPublisher()` y registra cuántas
   conexiones cortó.
4. Devuelve la clave **nueva en claro** — es el único momento en que el usuario puede
   copiarla— junto a su máscara. Documéntalo en el código: es la segunda y última excepción
   a "las claves no salen", y existe porque sin ella la rotación sería inútil.

Los tests que hacen falta:

```go
// TestRotateKeyChangesTheKeyAndAudits
// TestRotateKeyWithoutDisconnectLeavesTheSessionAlone
// TestRotateKeyWithDisconnectCutsThePublisher   (con un doble de Disconnecter)
// TestRotateKeyEventDoesNotContainTheKey        (spec §8)
// TestGetIngestNeverReturnsThePlainKey          (solo la máscara)
```

- [ ] **Step 4: `GET /api/status` y `GET /api/events`**

`status` compone: `ingestDTO` desde settings; `sessionDTO` desde
`engine.SessionID()` y, si no es 0, `db.SessionByID`; y la lista de `destinationDTO`
cruzando `db.ListDestinations` con `engine.Snapshot()`, que devuelve
`map[int64]relay.Metrics` indexado por id de destino.

`events` acepta `?limit=100`; `RecentEvents` ya acota el límite por arriba y por abajo, así
que un `limit` fuera de rango no es un error: se ajusta. Un `limit` no numérico sí es 400.

```go
// TestStatusWithoutASessionSaysNotLive
// TestStatusIncludesMetricsForLiveDestinationsOnly   (nil en los que no transmiten)
// TestStatusNeverIncludesAKey
// TestEventsRespectsTheLimit
// TestEventsRejectsANonNumericLimit
// TestEventsComeNewestFirst    ← este depende de la Task 1: sin el arreglo del orden
//                                lexicográfico, falla en cuanto dos eventos caen en el
//                                mismo segundo con fracciones de distinta longitud.
```

- [ ] **Step 5: Ejecutar los tests y verificar que pasan**

Run: `go test ./... -race -count=1`

- [ ] **Step 6: Commit**

```bash
git add internal/rtmpio/ internal/httpapi/
git commit -m "feat(httpapi): ingesta, rotación de clave con corte, estado y eventos"
```

---

### Task 10: WebSocket

`GET /ws` empuja el mismo `statusDTO` cada segundo. Que sea el mismo tipo que devuelve
`GET /api/status` no es casualidad: el spec §10 dice que la UI arranca con el snapshot del
GET para no depender de que el WS conecte primero, y eso solo funciona si las dos fuentes
tienen exactamente la misma forma.

**Files:**
- Create: `internal/httpapi/ws.go`, `internal/httpapi/ws_test.go`
- Modify: `internal/httpapi/server.go` (el último `notImplemented`), `go.mod`

**Interfaces:**
- Consumes: `statusDTO` y el mismo compositor de estado que usa `GET /api/status` (Task 9)
  — extráelo a un método `func (s *Server) status(ctx context.Context) (statusDTO, error)`
  y llámalo desde los dos sitios. Dos compositores acabarían divergiendo.
- Produces: nada que consuman otras tasks.

- [ ] **Step 1: Añadir la dependencia**

```bash
go get github.com/coder/websocket@v1.8.15
```

Verificado antes de escribir el plan: su `go.mod` no tiene ni un `require`, así que no
arrastra nada. **No ejecutes `go mod tidy`.** Comprueba con `git diff go.mod` que solo entró
esa línea.

- [ ] **Step 2: Escribir los tests**

Crea `internal/httpapi/ws_test.go` usando `httptest.NewServer` y el cliente del propio
paquete `websocket`:

```go
// TestWebSocketRequiresASession: el handshake lleva la cookie, así que el WS se protege
// igual que el resto. Sin cookie, el upgrade se rechaza.
func TestWebSocketRequiresASession(t *testing.T) {}

// TestWebSocketPushesStatus: al conectar llega un statusDTO, y luego otro. Con un límite
// de tiempo generoso pero finito, para que un fallo salga como fallo y no como cuelgue.
func TestWebSocketPushesStatus(t *testing.T) {}

// TestWebSocketPayloadMatchesTheRESTSnapshot es lo que sostiene la decisión de compartir
// tipo: el JSON del WS y el de GET /api/status deben tener las mismas claves. Compara los
// dos deserializados a map[string]any, no los bytes, que pueden diferir en el orden.
func TestWebSocketPayloadMatchesTheRESTSnapshot(t *testing.T) {}

// TestWebSocketStopsWhenTheClientGoesAway: si el navegador cierra la pestaña, la goroutine
// del push tiene que terminar. Comprueba runtime.NumGoroutine antes y después, con margen.
func TestWebSocketStopsWhenTheClientGoesAway(t *testing.T) {}

// TestWebSocketSurvivesASlowClient: un cliente que no lee no puede bloquear el bucle para
// siempre. Cada escritura va con su propio plazo; al vencer, se cierra la conexión.
func TestWebSocketSurvivesASlowClient(t *testing.T) {}
```

Los dos últimos son los que importan: un WS mal escrito filtra una goroutine por pestaña
cerrada, y eso en un proceso que retransmite durante horas se acumula.

- [ ] **Step 3: Implementar**

Puntos que el código debe respetar:

1. **El `Accept` va con `InsecureSkipVerify: false`** y la comprobación de origen por
   defecto de la librería, que exige que `Origin` coincida con el `Host`. Es la defensa
   contra que otra página abra un WS contra el panel del usuario.
2. **Cada escritura lleva su propio contexto con plazo** (2 s basta): un cliente que no lee
   llena el buffer del socket y, sin plazo, el bucle se queda ahí para siempre.
3. **El bucle sale cuando `r.Context()` se cancela**, que es lo que pasa cuando el cliente
   se va. Nada de `for {}` sin `select`.
4. **El ticker se para con `defer`.**
5. **Un error de escritura cierra la conexión y termina la goroutine**: no se reintenta. El
   cliente reconecta solo, que es lo que hará el frontend de la fase 5.
6. **Un fallo al componer el estado se registra y NO cierra la conexión**: un error puntual
   de la base no debe tirar el panel de quien está transmitiendo.

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/httpapi/ -race -count=5`
Con `-count=5` a propósito: los tests de goroutines y de plazos son los que fallan de forma
intermitente, y una sola pasada verde no dice nada.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/ go.mod go.sum
git commit -m "feat(httpapi): WebSocket que empuja el estado cada segundo"
```

---

### Task 11: Cablear el servidor HTTP en el binario

Todo lo anterior está probado con `httptest` y no lo arranca nadie. Esta tarea lo mete en
`main.go` junto al servidor RTMP, con el mismo cuidado en el apagado que la fase 3 puso en
los sinks.

**Files:**
- Modify: `cmd/splitstream/main.go`
- Test: `cmd/splitstream/main_test.go` (añadir)

**Interfaces:**
- Consumes: `httpapi.New`, `httpapi.Config`, `sinks.NewFactory` (Task 8),
  `Ingest.DisconnectPublisher` (Task 9).
- Produces: el binario sirve la API en `cfg.HTTPAddr`.

- [ ] **Step 1: Escribir el test que falla**

```go
// TestRunServesTheAPI: el binario levanta la API donde dice la configuración, y responde.
// Se comprueba contra /api/auth/login porque es el único endpoint público: un 401 o un 409
// prueban que el servidor está ahí, sin necesidad de autenticarse.
func TestRunServesTheAPI(t *testing.T) {}

// TestRunShutsDownTheHTTPServerOnSignal: al cancelar el contexto, run debe volver, y el
// puerto quedar libre. Si el servidor HTTP no se cierra, run se queda colgado y el
// servicio no se puede reiniciar sin matarlo.
func TestRunShutsDownTheHTTPServerOnSignal(t *testing.T) {}

// TestStartupLogStillHasNoKeys: la fase 3 arregló esto dos veces (5e57f0b, 62a6dc2).
// Ampliar el arranque es exactamente cuando se vuelve a romper.
func TestStartupLogStillHasNoKeys(t *testing.T) {}
```

El tercero probablemente ya exista como `TestRunStartupLogNeverContainsTheIngestKey`
(`cmd/splitstream/main_test.go:69`): amplíalo en vez de duplicarlo, para que cubra también
lo que loguea el arranque del HTTP.

- [ ] **Step 2: Implementar el cableado**

Dentro de `run`, después de construir el motor y antes de arrancar la ingesta:

```go
	api, err := httpapi.New(httpapi.Config{
		DB: db, Cipher: cipher, Engine: engine,
		Ingest: ingest, Sinks: factory,
		MasterKey: cfg.MasterKey, Logger: logger,
		SecureCookies: cfg.SecureCookies,
	})
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: api.Handler(),
		// Sin WriteTimeout: lo mataría el WebSocket, que por definición escribe durante
		// horas. Los plazos de escritura van por mensaje, dentro del handler del WS.
		ReadHeaderTimeout: 10 * time.Second,
	}
```

Arráncalo en su goroutine con el `wg` que ya existe, y ciérralo en el apagado **antes** de
esperar a la ingesta, con `httpSrv.Shutdown(ctx)` y un plazo propio. El orden importa:
cerrar primero el HTTP evita que llegue una petición que toque la base mientras se está
cerrando la sesión de ingesta.

Un `ListenAndServe` que devuelve `http.ErrServerClosed` **no es un fallo**: es lo que
devuelve siempre tras un `Shutdown`. Trátalo como la ingesta trata su error de listener
cerrado.

- [ ] **Step 3: Añadir `SecureCookies` a la configuración**

En `internal/config/config.go`, añade el campo con su variable de entorno
`SPLITSTREAM_SECURE_COOKIES`, por defecto `false`. Va en la configuración y no se deduce de
la petición porque en el despliegue del spec §12 el TLS lo termina un proxy y el binario
solo ve HTTP: intentar adivinarlo daría una cookie sin `Secure` justo en producción.
Actualiza también la tabla del README y el test de defaults de `config`.

- [ ] **Step 4: Ejecutar la suite entera**

Run: `go test ./... -race -count=1` y después
`make sinks-up && make test-integration`.
Expected: PASS los dos. La integración importa aquí: es lo que prueba que añadir el
servidor HTTP no rompió el apagado ordenado que la fase 3 costó tres rondas de arreglos.

- [ ] **Step 5: Commit**

```bash
git add cmd/splitstream/ internal/config/ README.md
git commit -m "feat: el binario sirve la API HTTP junto al servidor RTMP"
```

---

## Definición de terminado, fase 4

- [ ] `go test ./... -race -count=1` pasa entero.
- [ ] `go vet ./...` limpio.
- [ ] `CGO_ENABLED=0 go build ./cmd/splitstream` produce el binario.
- [ ] `go.mod` tiene exactamente **cinco** directas, las del spec §5, y `go 1.25.0`.
- [ ] `go list -deps ./internal/relay | grep -E 'go-rtmp|database/sql'` sigue vacío.
- [ ] `go list -deps ./internal/httpapi | grep go-rtmp` vacío.
- [ ] `make sinks-up && make test-integration` sigue pasando los tres tests.
- [ ] La CI (los dos jobs) está verde.
- [ ] Los catorce endpoints del spec §9 existen, responden y están cubiertos por tests.
- [ ] Ninguno de los catorce, salvo `GET /api/destinations/:id/key` y la respuesta de
      `POST /api/ingest/rotate-key`, devuelve una clave en claro. Verificado por un test que
      recorre los cuerpos, no por lectura.
- [ ] Sin cookie de sesión válida, los doce endpoints protegidos responden 401.
- [ ] El WebSocket empuja `statusDTO` cada segundo y su JSON tiene las mismas claves que
      `GET /api/status`.
- [ ] Cerrar el cliente del WebSocket no deja goroutines vivas.
- [ ] `splitstream -setpassword` fija una contraseña verificable y no la imprime.
- [ ] Revelar una clave por la API deja siempre un evento `key_revealed`.
- [ ] El orden de `GET /api/events` es el cronológico, con eventos del mismo segundo.
- [ ] El apagado con SIGTERM sigue cerrando la sesión de ingesta con `ended_at` no nulo.

## Notas para la fase 5

- El frontend consume `statusDTO`. Los nombres de sus campos son contrato desde que esta
  fase se fusione: cambiarlos después rompe la UI.
- `destinationDTO.Metrics` es `null` cuando el destino no está transmitiendo. La UI debe
  distinguir eso de un cero, o enseñará "0 kbps" para un destino apagado.
- La rotación de clave devuelve la clave nueva en claro **una sola vez**, en la respuesta.
  Si la UI no la enseña ahí, el usuario la pierde y tiene que rotar otra vez.
- El WS no reintenta: la reconexión con backoff es del cliente (spec §10).
- Queda sin pagar de la deuda del spec §15: nada. Esta fase cierra §15.2, §15.3, §15.4,
  §15.5 y §15.8, que eran las cinco que quedaban.
