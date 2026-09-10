# v0.8 «Confianza en producción» — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Que Splitstream se pueda dejar corriendo en un VPS sin mirarlo: probar un destino sin emitir, avisar por el panel y por webhook cuando algo falla, exponer `/healthz` y `/metrics`, podar y respaldar la base, y dejar de reintentar contra un destino que nunca transmite.

**Architecture:** Nada toca el reparto del `Hub` ni la cola. Se añaden: un estado `suspended` al `Sink`; una sonda `rtmpio.Probe` con sus tipos en `internal/probe`; un hook en `store.LogEvent` que alimenta `events.Bus`; un consumidor del bus (`alerts.WebhookDispatcher`); dos endpoints de operación; un planificador diario (`internal/maintenance`); y en el panel el registro de eventos, los avisos y una página de ajustes. Las fronteras que vigila la CI se mantienen y se añade una: `httpapi` no importa `rtmpio`.

**Tech Stack:** Go 1.25 stdlib + las cinco dependencias del spec base §5. Vue 3 + Quasar 2 + Pinia. Cero dependencias nuevas.

**Spec:** `docs/superpowers/specs/2026-09-09-confianza-produccion-design.md` (sobre el spec base `2026-09-01-rtmp-relay-design.md` y el plan maestro `2026-09-09-roadmap-ejecucion.md` §3).

## Global Constraints

- **Cero dependencias nuevas**, ni Go ni npm. `go mod tidy` no se ejecuta en este repo.
- **`internal/relay` no importa go-rtmp, `database/sql`, `internal/store` ni `internal/events`.** `internal/httpapi` no importa go-rtmp ni `internal/rtmpio` (la CI lo comprueba: ver Task 6).
- **Un solo mecanismo de secretos:** `crypto.Cipher` para cifrar en reposo, `crypto.Secret` en memoria. Ningún secreto ni clave en logs, eventos ni mensajes de error, tampoco enmascarado.
- **Errores de la API** con la forma `{"error":{"code","message"}}` y solo los códigos de `internal/httpapi/errors.go`. Errores del store clasificados con `notFound` / `invalidInput` / `conflict`.
- **`statusDTO` es el mismo tipo para `GET /api/status` y el WebSocket.** `TestWebSocketPayloadMatchesTheRESTSnapshot` lo vigila.
- **Migraciones** `NNNN_nombre.sql` con `CREATE TABLE IF NOT EXISTS`, y `SchemaVersion` al día.
- **Tests con `-race`** (`make test`), estables con `GOMAXPROCS=2`. Las esperas por backoff usan `waitForDur` con plazo holgado, como en `sink_reconnect_test.go`.
- **Comentarios, commits, copys y errores en español.** Los comentarios explican el porqué.
- **Rutas** registradas todas en `routes()` de `internal/httpapi/server.go`, las protegidas vía `protegida(...)`.
- Rama de trabajo: `feat/confianza-produccion` desde `main`. Un commit por paso verde; PR al final; etiqueta `v0.8.0` tras fusionar.

---

### Task 1: Rama y deriva de versiones en la documentación

**Files:**
- Modify: `README.md` (línea del `xattr` con `splitstream-v0.5.0-…`)
- Modify: `docs/lanzamiento.md` (cabecera «Escrito para la v0.6.0»)
- Modify: `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` (nota al final del §7)

- [ ] **Step 1: Crear la rama**

```bash
git checkout -b feat/confianza-produccion main
```

- [ ] **Step 2: Corregir el ejemplo del README**

En `README.md`, sustituir la línea:

```
xattr -dr com.apple.quarantine splitstream-v0.5.0-macos-apple-silicon
```

por:

```
xattr -dr com.apple.quarantine splitstream-v0.8.0-macos-apple-silicon
```

- [ ] **Step 3: Corregir la cabecera de la entrada de lanzamiento**

En `docs/lanzamiento.md`, sustituir «Escrito para la v0.6.0.» por «Escrito para la v0.8.0, la primera con `/metrics`, avisos y «probar destino»; si cambian las plataformas soportadas o lo que la herramienta no hace, hay que revisarlo.» manteniendo el resto del párrafo.

- [ ] **Step 4: Anotar la renumeración en el roadmap del repo**

Añadir al final del §7 de `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md`:

```markdown
> **Renumeración (2026-09-09):** `v0.6.0` y `v0.7.0` ya existían como etiquetas cuando se
> escribió este roadmap. Las entregas se ejecutan como v0.8 (Confianza), v0.9 (Grabación),
> v0.10 (Instalación), v0.11 (Twitch), v0.12 (YouTube y Kick), v0.13 (Internacional) y
> v1.0. Ver `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §0.
```

- [ ] **Step 5: Verificar y commitear**

```bash
grep -rn "v0\.[567]\.0" README.md docs/lanzamiento.md
```
Expected: sin resultados.

```bash
git add README.md docs/lanzamiento.md docs/superpowers/specs/2026-09-09-roadmap-mejoras.md
git commit -m "docs: corregir la deriva de versiones y anotar la renumeración del roadmap"
```

---

### Task 2: Hook de eventos en el store y `events.Bus`

**Files:**
- Modify: `internal/store/db.go` (campo `hook` en `DB`, `InTx` lo propaga)
- Create: `internal/store/hook.go`
- Modify: `internal/store/events.go` (`LogEvent` llama al hook)
- Create: `internal/store/hook_test.go`
- Create: `internal/events/bus.go`, `internal/events/bus_test.go`
- Modify: `cmd/splitstream/main.go` (cablear el bus)

**Interfaces:**
- Produces: `store.EventHook func(Event)`; `(*DB).SetEventHook(EventHook)`.
- Produces: `events.NewBus() *Bus`; `(*Bus).Subscribe(buffer int) (<-chan store.Event, func())`; `(*Bus).Publish(store.Event)`; `(*Bus).Dropped() uint64`.

- [ ] **Step 1: Escribir los tests del hook (rojo)**

Crear `internal/store/hook_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

// El hook recibe el evento tal como quedó en la base: con id y fecha. Es lo que permite
// que alertas y webhooks no tengan que releer nada.
func TestLogEventCallsTheHookWithTheStoredEvent(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	var got []store.Event
	db.SetEventHook(func(e store.Event) { got = append(got, e) })

	id, err := db.LogEvent(ctx, store.Event{Level: store.LevelWarn, Kind: "prueba", Message: "hola"})
	if err != nil {
		t.Fatalf("LogEvent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("el hook se llamó %d veces, quería 1", len(got))
	}
	if got[0].ID != id || got[0].Kind != "prueba" || got[0].Level != store.LevelWarn {
		t.Errorf("el hook recibió %+v, quería id=%d kind=prueba level=warn", got[0], id)
	}
	if got[0].CreatedAt.IsZero() {
		t.Error("el hook recibió un evento sin CreatedAt")
	}
}

// El camino de RevealDestinationKey escribe su evento dentro de InTx: el hook tiene que
// llegar también por ahí, o la auditoría de las claves no avisaría a nadie.
func TestHookIsCalledInsideATransaction(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	llamadas := 0
	db.SetEventHook(func(store.Event) { llamadas++ })

	err := db.InTx(ctx, func(tx *store.DB) error {
		_, err := tx.LogEvent(ctx, store.Event{Level: store.LevelInfo, Kind: "en_tx", Message: "x"})
		return err
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if llamadas != 1 {
		t.Errorf("el hook se llamó %d veces dentro de la transacción, quería 1", llamadas)
	}
}

// Un evento que no se escribió no se anuncia.
func TestHookIsNotCalledWhenTheInsertFails(t *testing.T) {
	db := openTemp(t)

	llamadas := 0
	db.SetEventHook(func(store.Event) { llamadas++ })

	if _, err := db.LogEvent(context.Background(), store.Event{Level: "fatal", Kind: "x"}); err == nil {
		t.Fatal("quería error por nivel desconocido")
	}
	if llamadas != 0 {
		t.Errorf("el hook se llamó %d veces tras un INSERT fallido", llamadas)
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/store/ -run 'Hook' -count=1
```
Expected: FAIL, `db.SetEventHook undefined`.

- [ ] **Step 3: Implementar el hook**

Crear `internal/store/hook.go`:

```go
package store

// EventHook recibe cada evento que LogEvent persistió, con su ID y su CreatedAt ya
// asignados. Es el único punto por el que pasan TODOS los eventos —los del motor, los de
// los sinks, los de la API y la auditoría de claves— porque todos escriben por LogEvent.
//
// Se llama en la goroutine que escribió, después de que el INSERT tuviera éxito. Dentro de
// una transacción se llama antes del commit; se acepta porque el único camino transaccional
// (RevealDestinationKey) no puede fallar después del LogEvent.
//
// El hook NO debe bloquear: LogEvent lo llaman las goroutines de los sinks a mitad de
// transmisión. events.Bus cumple esa regla con entregas no bloqueantes.
type EventHook func(Event)

// SetEventHook fija el hook. nil lo quita. No es seguro llamarlo en concurrencia con
// LogEvent: se cablea una vez en el arranque.
func (d *DB) SetEventHook(h EventHook) { d.hook = h }
```

En `internal/store/db.go`, añadir el campo al struct y propagarlo en `InTx`:

```go
type DB struct {
	db   *sql.DB // solo para abrir transacciones y cerrar
	ex   execer  // por donde salen todas las consultas: *sql.DB o *sql.Tx
	hook EventHook
}
```

y en `InTx`:

```go
	if err := fn(&DB{db: d.db, ex: tx, hook: d.hook}); err != nil {
```

En `internal/store/events.go`, reescribir `LogEvent`:

```go
// LogEvent persiste un evento y devuelve su id. Ignora e.ID y e.CreatedAt. Si hay hook, lo
// llama con el evento completo tras el INSERT.
func (d *DB) LogEvent(ctx context.Context, e Event) (int64, error) {
	if !e.Level.Valid() {
		return 0, fmt.Errorf("nivel de evento desconocido %q", e.Level)
	}
	if e.Kind == "" {
		return 0, fmt.Errorf("el evento necesita un kind")
	}
	now := time.Now()
	res, err := d.ex.ExecContext(ctx,
		`INSERT INTO events (session_id, destination_id, level, kind, message, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.SessionID, e.DestinationID, string(e.Level), e.Kind, e.Message, formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("registrar evento: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("registrar evento: %w", err)
	}
	if d.hook != nil {
		e.ID = id
		e.CreatedAt = now.UTC()
		d.hook(e)
	}
	return id, nil
}
```

- [ ] **Step 4: Correr en verde**

```bash
go test ./internal/store/ -race -count=1
```
Expected: PASS (toda la suite del store, no solo los tres nuevos).

- [ ] **Step 5: Commit**

```bash
git add internal/store/hook.go internal/store/hook_test.go internal/store/db.go internal/store/events.go
git commit -m "feat(store): hook a la salida de LogEvent para alertas y webhooks"
```

- [ ] **Step 6: Escribir los tests del bus (rojo)**

Crear `internal/events/bus_test.go`:

```go
package events_test

import (
	"sync"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/events"
	"github.com/aprendomx/splitstream/internal/store"
)

func TestEverySubscriberReceivesTheEvent(t *testing.T) {
	bus := events.NewBus()
	a, releaseA := bus.Subscribe(4)
	defer releaseA()
	b, releaseB := bus.Subscribe(4)
	defer releaseB()

	bus.Publish(store.Event{ID: 1, Kind: "x"})

	for nombre, ch := range map[string]<-chan store.Event{"a": a, "b": b} {
		select {
		case ev := <-ch:
			if ev.ID != 1 {
				t.Errorf("%s recibió el evento %d, quería 1", nombre, ev.ID)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s no recibió nada", nombre)
		}
	}
}

// Es la propiedad que justifica el bus: LogEvent lo llaman las goroutines de los sinks, y
// un consumidor lento —un webhook contra un servidor caído— no puede frenarlas.
func TestPublishNeverBlocksOnASlowSubscriber(t *testing.T) {
	bus := events.NewBus()
	_, release := bus.Subscribe(2) // nadie lee
	defer release()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			bus.Publish(store.Event{ID: int64(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish se bloqueó con un suscriptor que no lee")
	}
	// 2 cupieron en el buffer; los otros 98 se perdieron y se contaron.
	if got := bus.Dropped(); got != 98 {
		t.Errorf("Dropped = %d, quería 98", got)
	}
}

func TestReleaseIsIdempotentAndClosesTheChannel(t *testing.T) {
	bus := events.NewBus()
	ch, release := bus.Subscribe(1)
	release()
	release()
	if _, ok := <-ch; ok {
		t.Error("el canal siguió abierto tras release")
	}
	bus.Publish(store.Event{ID: 1}) // no debe entrar en pánico escribiendo en un canal cerrado
}

func TestPublishAndReleaseAreSafeConcurrently(t *testing.T) {
	bus := events.NewBus()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, release := bus.Subscribe(1)
			release()
		}()
		go func() {
			defer wg.Done()
			bus.Publish(store.Event{ID: 1})
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 7: Correr en rojo**

```bash
go test ./internal/events/ -count=1
```
Expected: FAIL, `no Go files` o `undefined: events.NewBus`.

- [ ] **Step 8: Implementar el bus**

Crear `internal/events/bus.go`:

```go
// Package events reparte los eventos ya persistidos a quien quiera reaccionar a ellos:
// alertas, webhooks y, más adelante, el chat. Importa store solo por el tipo Event.
package events

import (
	"sync"
	"sync/atomic"

	"github.com/aprendomx/splitstream/internal/store"
)

// Bus es un fan-out de eventos con entrega NO bloqueante. Un suscriptor que no lee pierde
// eventos —se cuentan en Dropped— pero jamás frena a quien publica, que es la goroutine
// de un sink a mitad de transmisión. Es la misma disciplina que el tap de la vista previa.
type Bus struct {
	mu      sync.RWMutex
	subs    map[*subscriber]struct{}
	dropped atomic.Uint64
}

type subscriber struct {
	ch   chan store.Event
	once sync.Once
}

func NewBus() *Bus {
	return &Bus{subs: map[*subscriber]struct{}{}}
}

// Subscribe registra un consumidor con un canal de `buffer` posiciones y devuelve el canal
// y una función release idempotente que lo da de baja y lo cierra.
func (b *Bus) Subscribe(buffer int) (<-chan store.Event, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	s := &subscriber{ch: make(chan store.Event, buffer)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()

	release := func() {
		b.mu.Lock()
		delete(b.subs, s)
		b.mu.Unlock()
		// Fuera del lock y solo tras quitarlo del mapa: ningún Publish en vuelo puede
		// escribir ya en este canal, porque el Lock de arriba esperó a que soltaran el
		// RLock, y desde entonces el suscriptor no está en el mapa.
		s.once.Do(func() { close(s.ch) })
	}
	return s.ch, release
}

// Publish entrega el evento a todos los suscriptores sin bloquear.
func (b *Bus) Publish(ev store.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		select {
		case s.ch <- ev:
		default:
			b.dropped.Add(1)
		}
	}
}

// Dropped devuelve cuántas entregas se perdieron por suscriptores que no leían. Sale por
// /metrics.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }
```

- [ ] **Step 9: Correr en verde**

```bash
go test ./internal/events/ -race -count=1
```
Expected: PASS.

- [ ] **Step 10: Cablear el bus en `main.go`**

En `cmd/splitstream/main.go`, importar `"github.com/aprendomx/splitstream/internal/events"` y, en `run()`, justo después de `settings, err := db.Settings(ctx)`:

```go
	// El bus de eventos: todo lo que se persiste en `events` sale también por aquí, para
	// las alertas del panel y los webhooks. Se cablea antes que el motor y la API para que
	// ningún evento del arranque se quede sin anunciar.
	bus := events.NewBus()
	db.SetEventHook(bus.Publish)
```

`bus` se usa en Tasks 8 y 11; hasta entonces, para que compile, añadir `_ = bus` justo debajo con el comentario `// se consume en las tareas siguientes`.

- [ ] **Step 11: Verificar y commitear**

```bash
go build ./... && go vet ./... && go test ./... -race -count=1
```
Expected: todo en verde.

```bash
git add internal/events cmd/splitstream/main.go
git commit -m "feat(events): bus de eventos con entrega no bloqueante"
```

---

### Task 3: Suspender tras N fallos (motor)

**Files:**
- Modify: `internal/relay/sink.go`
- Create: `internal/relay/sink_suspend_test.go`

**Interfaces:**
- Produces: `relay.StateSuspended` (String `"suspended"`); constantes `relay.DefaultSuspendAfterAttempts = 10`, `relay.DefaultSuspendAfterFlaps = 5`; campos `SinkConfig.SuspendAfterAttempts`, `SinkConfig.SuspendAfterFlaps` (0 = por defecto); evento `destination_suspended` (error).

- [ ] **Step 1: Escribir los tests (rojo)**

Crear `internal/relay/sink_suspend_test.go`:

```go
package relay

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Tras N conexiones fallidas seguidas el sink deja de reintentar y lo dice. Antes de la
// v0.8 reintentaba para siempre (spec base §6.5, enmendado): con una clave mal pegada eso
// era un bucle silencioso, y contra Facebook cada intento cuenta como emisión activa.
func TestSinkSuspendsAfterRepeatedConnectFailures(t *testing.T) {
	pub := &flakyPublisher{failFirst: 1000, inner: &fakePublisher{}}

	var mu sync.Mutex
	var eventos []EngineEvent
	s := NewSink(SinkConfig{
		ID: 1, Name: "X", Pub: pub,
		SuspendAfterAttempts: 3,
		OnEvent: func(e EngineEvent) {
			mu.Lock()
			eventos = append(eventos, e)
			mu.Unlock()
		},
	})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	// Tres intentos: el primero al instante, 1 s y 2 s de backoff después (±20 %).
	waitForDur(t, 15*time.Second, func() bool { return s.State() == StateSuspended }, "se suspendió")

	// Ventana en la que un cuarto intento se vería: el siguiente backoff sería de ~4 s.
	time.Sleep(5 * time.Second)
	if got := pub.attemptCount(); got != 3 {
		t.Errorf("intentos = %d, quería exactamente 3: un sink suspendido no reintenta", got)
	}
	if m := s.Metrics(); m.State != "suspended" {
		t.Errorf("Metrics().State = %q, quería suspended", m.State)
	}

	mu.Lock()
	defer mu.Unlock()
	var visto bool
	for _, e := range eventos {
		if e.Kind == "destination_suspended" && e.Level == "error" {
			visto = true
		}
	}
	if !visto {
		t.Errorf("no se emitió destination_suspended; eventos: %+v", eventos)
	}
}

// Un destino que conecta, transmite poco y corta —lo que hizo Facebook— también se
// suspende, tras M sesiones cortas seguidas.
func TestSinkSuspendsAfterRepeatedFlaps(t *testing.T) {
	pub := &flappingPublisher{permitidas: 6}
	s := NewSink(SinkConfig{ID: 1, Name: "aleteante", Pub: pub, SuspendAfterFlaps: 2})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	fin := make(chan struct{})
	defer close(fin)
	go func() {
		for {
			select {
			case <-fin:
				return
			default:
			}
			s.Enqueue(videoKey(1000))
			time.Sleep(2 * time.Millisecond)
		}
	}()

	waitForDur(t, 20*time.Second, func() bool { return s.State() == StateSuspended }, "se suspendió por aleteo")
	if got := pub.conexiones(); got != 2 {
		t.Errorf("conexiones = %d, quería 2", got)
	}
}

// Stop tiene que despertar a un sink suspendido: es el camino de Hub.Add al reemplazarlo
// tras «Reintentar», y del apagado.
func TestSinkStopWakesASuspendedSink(t *testing.T) {
	pub := &flakyPublisher{failFirst: 1000, inner: &fakePublisher{}}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub, SuspendAfterAttempts: 1})
	s.Start(context.Background(), preambleWith())

	waitForDur(t, 5*time.Second, func() bool { return s.State() == StateSuspended }, "se suspendió")

	inicio := time.Now()
	s.Stop()
	if d := time.Since(inicio); d > 2*time.Second {
		t.Errorf("Stop tardó %v sobre un sink suspendido", d)
	}
	if s.State() != StateIdle {
		t.Errorf("estado tras Stop = %v, quería idle", s.State())
	}
}

// La cola de un sink suspendido sigue recibiendo del hub. Su política de descarte se
// aplica en push, así que no crece sin límite.
func TestSuspendedSinkQueueDoesNotGrow(t *testing.T) {
	pub := &flakyPublisher{failFirst: 1000, inner: &fakePublisher{}}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub, SuspendAfterAttempts: 1})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()
	waitForDur(t, 5*time.Second, func() bool { return s.State() == StateSuspended }, "se suspendió")

	for i := 0; i < 3*DefaultMaxItems; i++ {
		s.Enqueue(audioRaw(uint32(i)))
	}
	if m := s.Metrics(); m.QueuedMessages > DefaultMaxItems {
		t.Errorf("la cola de un sink suspendido tiene %d mensajes, más que el tope %d",
			m.QueuedMessages, DefaultMaxItems)
	}
}

func TestStateStringSuspended(t *testing.T) {
	if got := StateSuspended.String(); got != "suspended" {
		t.Errorf("String() = %q", got)
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/relay/ -run 'Suspend' -count=1
```
Expected: FAIL de compilación, `undefined: StateSuspended`.

- [ ] **Step 3: Implementar la suspensión**

En `internal/relay/sink.go`:

1. Añadir `"fmt"` a los imports.
2. En la enumeración de estados, después de `StateError`:

```go
	// StateSuspended: el sink dejó de reintentar dentro de esta sesión tras
	// SuspendAfterAttempts intentos sin transmitir o SuspendAfterFlaps cortes seguidos
	// (spec base §6.5, enmendado en la v0.8). Sale de aquí al reconstruirlo («Reintentar»)
	// o al empezar otra sesión.
	StateSuspended
```

y en `String()`:

```go
	case StateSuspended:
		return "suspended"
```

3. Debajo de `flapThreshold`:

```go
// Umbrales de suspensión (spec base §6.5, enmendado). Antes los reintentos eran
// indefinidos; con una clave mal pegada eso era un bucle silencioso, y contra una
// plataforma que cuenta cada intento como emisión activa, un cupo agotado.
//
// Diez intentos con el backoff topado a 30 s son unos dos minutos: de sobra para una
// caída puntual de red, y un tope claro para una configuración que nunca va a funcionar.
const (
	DefaultSuspendAfterAttempts = 10
	DefaultSuspendAfterFlaps    = 5
)
```

4. En `SinkConfig`, tras `OnEvent`:

```go
	// SuspendAfterAttempts y SuspendAfterFlaps sobreescriben los umbrales de suspensión.
	// 0 usa el valor por defecto.
	SuspendAfterAttempts int
	SuspendAfterFlaps    int
```

5. En `Sink`, tras `onEvent`:

```go
	suspendAttempts int
	suspendFlaps    int
```

6. En `NewSink`, antes del `return`:

```go
	suspendAttempts := cfg.SuspendAfterAttempts
	if suspendAttempts <= 0 {
		suspendAttempts = DefaultSuspendAfterAttempts
	}
	suspendFlaps := cfg.SuspendAfterFlaps
	if suspendFlaps <= 0 {
		suspendFlaps = DefaultSuspendAfterFlaps
	}
```

y en el literal `&Sink{…}`: `suspendAttempts: suspendAttempts, suspendFlaps: suspendFlaps,`.

7. En `run()`, entre el `switch` de fallos y `wait := s.bo.next()`:

```go
		// Suspensión: tras N intentos seguidos sin transmitir, o M sesiones cortas
		// seguidas, se deja de reintentar. bo.attempts() cuenta los next() ya hechos, así
		// que el intento que acaba de fallar es attempts()+1.
		intentos := s.bo.attempts() + 1
		if (!transmitted && intentos >= s.suspendAttempts) || flaps >= s.suspendFlaps {
			s.suspend(ctx, intentos, flaps)
			return
		}
```

8. Añadir el método, después de `run`:

```go
// suspend deja el sink parado dentro de la sesión, sin cerrar su cola: el hub sigue
// entregándole mensajes y la política de descarte de la cola —que se aplica en push— evita
// que crezca. Se sale solo por Stop (que llegará con «Reintentar» o con el fin de sesión)
// o por el contexto del proceso.
func (s *Sink) suspend(ctx context.Context, intentos, flaps int) {
	s.setState(StateSuspended)
	s.emit("error", "destination_suspended", fmt.Sprintf(
		"el destino queda suspendido en esta sesión tras %d intentos sin transmitir y %d cortes "+
			"seguidos; revisa la URL y la clave y pulsa «Reintentar»", intentos, flaps))
	s.log.Error("destino suspendido", "intentos", intentos, "cortes", flaps)

	select {
	case <-s.quit:
	case <-ctx.Done():
	}
	s.setState(StateIdle)
}
```

- [ ] **Step 4: Correr en verde**

```bash
go test ./internal/relay/ -race -count=1
GOMAXPROCS=2 go test ./internal/relay/ -race -count=1 -run 'Suspend'
```
Expected: PASS las dos. La suite de `relay` tarda ~1 min por las esperas de backoff que ya tenía.

- [ ] **Step 5: Commit**

```bash
git add internal/relay/sink.go internal/relay/sink_suspend_test.go
git commit -m "feat(relay): suspender un destino tras N intentos sin transmitir o M cortes"
```

---

### Task 4: «Reintentar»: endpoint y panel

**Files:**
- Modify: `internal/httpapi/destinations.go` (`handleRetryDestination`)
- Modify: `internal/httpapi/server.go` (ruta)
- Modify: `internal/httpapi/destinations_test.go` (tests al final)
- Modify: `web/src/api.js`, `web/src/diagnostico.js`, `web/src/components/TarjetaDestino.vue`, `web/src/pages/Panel.vue`

**Interfaces:**
- Consumes: `relay.StateSuspended.String()` (Task 3); `s.applyHot` y `s.liveSession()` existentes.
- Produces: `POST /api/destinations/{id}/retry` → 200 con `destinationDTO`; 409 si no hay sesión o el destino no está `suspended`; 404 si no existe. Evento `destination_retry` (info).

- [ ] **Step 1: Tests (rojo)**

Añadir al final de `internal/httpapi/destinations_test.go`:

```go
// --- reintentar ---

// Reintentar reconstruye el sink de un destino suspendido: es la única salida de ese
// estado sin esperar a la siguiente sesión.
func TestRetryRebuildsASuspendedDestination(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k1", true)
	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{d.ID: {State: "suspended"}})

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/retry", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	added, _ := eng.snapshotSinks()
	if len(added) != 1 || added[0] != d.ID {
		t.Errorf("AddSink recibió %v, quería [%d]", added, d.ID)
	}

	eventos, err := db.RecentEvents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventos) == 0 || eventos[0].Kind != "destination_retry" {
		t.Errorf("no quedó evento destination_retry: %+v", eventos)
	}
}

// Reintentar un destino que está emitiendo lo reconstruiría y cortaría la transmisión.
func TestRetryRefusesADestinationThatIsNotSuspended(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k1", true)
	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{d.ID: {State: "live"}})

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/retry", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("código = %d, quería 409: %s", rec.Code, rec.Body.String())
	}
	if added, _ := eng.snapshotSinks(); len(added) != 0 {
		t.Errorf("se reconstruyó un destino que estaba en vivo: %v", added)
	}
}

func TestRetryWithoutASessionIsAConflict(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k1", true)

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/retry", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("código = %d, quería 409: %s", rec.Code, rec.Body.String())
	}
}

func TestRetryUnknownDestinationIs404(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{9999: {State: "suspended"}})

	rec := do(t, srv, cookies, http.MethodPost, destPath(9999)+"/retry", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("código = %d, quería 404: %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/httpapi/ -run 'Retry' -count=1
```
Expected: FAIL con 404/405 (la ruta no existe).

- [ ] **Step 3: Implementar el handler**

En `internal/httpapi/destinations.go`, al final:

```go
// handleRetryDestination reconstruye el sink de un destino suspendido (spec v0.8 §2.1).
//
// Solo tiene sentido con sesión viva y con el destino en `suspended`: sobre uno en vivo,
// reconstruirlo cortaría la transmisión, y sin sesión el destino conectará solo al empezar
// la siguiente. La reconstrucción es el mismo camino que una edición en caliente: Build +
// AddSink, que reemplaza al sink anterior sin ventana de escritura doble.
func (s *Server) handleRetryDestination(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if !s.liveSession() {
		writeError(w, http.StatusConflict, codeConflict,
			"no hay emisión en curso: el destino conectará solo al empezar la siguiente")
		return
	}

	d, err := s.db.DestinationByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if m, ok := s.engine.Snapshot()[id]; !ok || m.State != relay.StateSuspended.String() {
		writeError(w, http.StatusConflict, codeConflict, "el destino no está suspendido")
		return
	}
	if !d.Enabled {
		writeError(w, http.StatusConflict, codeConflict, "el destino está apagado: enciéndelo")
		return
	}

	if _, err := s.db.LogEvent(r.Context(), store.Event{
		DestinationID: &id, Level: store.LevelInfo, Kind: "destination_retry",
		Message: "se reintenta el destino a petición del usuario",
	}); err != nil {
		s.logger.Error("no se pudo registrar el reintento", "err", err)
	}

	s.applyHot(r, *d)
	writeJSON(w, http.StatusOK, newDestinationDTO(*d, s.metricsFor(d.ID), s.logoETag(r.Context(), d.ID)))
}
```

`DestinationByID` no existe todavía: en `internal/store/destinations.go`, junto a `destination`:

```go
// DestinationByID devuelve un destino por su id, sin la clave.
func (d *DB) DestinationByID(ctx context.Context, id int64) (*Destination, error) {
	return d.destination(ctx, id)
}
```

En `internal/httpapi/server.go`, en `routes()`, tras la línea de `/key`:

```go
	protegida("POST /api/destinations/{id}/retry", s.handleRetryDestination)
```

- [ ] **Step 4: Correr en verde**

```bash
go test ./internal/httpapi/ ./internal/store/ -race -count=1
```
Expected: PASS.

- [ ] **Step 5: Commit del backend**

```bash
git add internal/httpapi/destinations.go internal/httpapi/destinations_test.go internal/httpapi/server.go internal/store/destinations.go
git commit -m "feat(api): reintentar un destino suspendido"
```

- [ ] **Step 6: Panel — diagnóstico, botón y llamada**

En `web/src/api.js`, dentro de `export const api = {`, tras `revelarClave`:

```js
  reintentarDestino: (id) => pedir('POST', `/api/destinations/${id}/retry`),
```

En `web/src/diagnostico.js`, antes de `if (m.state === 'error') {`:

```js
  if (m.state === 'suspended') {
    return {
      tono: 'fallo',
      titulo: 'Suspendido',
      detalle: `Tras ${m.reconnections} intentos sin conseguirlo, dejó de reintentar en esta emisión.`,
      consejo: 'Revisa la URL y la clave y pulsa «Reintentar». Si no haces nada, volverá a intentarlo en la próxima emisión.',
    }
  }
```

En `web/src/components/TarjetaDestino.vue`:

- En `defineEmits`, añadir `'reintentar'`: `defineEmits(['editar', 'alternar', 'borrar', 'revelar', 'reintentar'])`.
- Añadir en `<script setup>`: `const suspendido = computed(() => m.value?.state === 'suspended')`.
- Tras el bloque `<div v-if="diag.consejo" …>…</div>`, añadir:

```html
    <div v-if="suspendido" class="q-px-md q-pt-sm">
      <q-btn dense no-caps unelevated color="primary" size="sm" :icon="iRotar"
             label="Reintentar" @click="$emit('reintentar')" />
    </div>
```

y añadir `iRotar` al import de `@/iconos` de ese componente.

En `web/src/pages/Panel.vue`:

- En el uso de `<TarjetaDestino …>` añadir `@reintentar="reintentar(element)"`.
- Junto a `alternar(d)`:

```js
async function reintentar(d) {
  try {
    await api.reintentarDestino(d.id)
    await panel.cargar()
    $q.notify({ type: 'info', message: `Reintentando ${d.name}` })
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  }
}
```

- [ ] **Step 7: Compilar el panel y commitear**

```bash
cd web && npm run build && cd ..
```
Expected: build sin errores ni avisos de variables sin importar.

```bash
git add web/src
git commit -m "feat(panel): estado suspendido con botón de reintentar"
```

---

### Task 5: La sonda `rtmpio.Probe` y el paquete `probe`

**Files:**
- Create: `internal/probe/probe.go`
- Modify: `internal/rtmpio/publisher.go` (errores con etapa; `lastError`)
- Create: `internal/rtmpio/probe.go`, `internal/rtmpio/probe_test.go`

**Interfaces:**
- Produces: `probe.Outcome` (`Unreachable | Rejected | ClosedEarly | Plausible`, `String()` → `unreachable|rejected|closed_early|plausible`); `probe.Result{Outcome, Stage string, Elapsed time.Duration, Err error}`.
- Produces: `rtmpio.Probe(ctx context.Context, cfg PublisherConfig, grace time.Duration) probe.Result`.

- [ ] **Step 1: El paquete de tipos**

Crear `internal/probe/probe.go`:

```go
// Package probe define el resultado de probar un destino sin emitir. Es un paquete de
// tipos sin dependencias para que la capa HTTP pueda hablar de sondas sin importar rtmpio
// —y con él go-rtmp—, que es una de las fronteras que vigila la CI.
package probe

import "time"

// Outcome es el veredicto de una sonda.
type Outcome uint8

const (
	// Unreachable: no se llegó a hablar RTMP (DNS, TCP o TLS).
	Unreachable Outcome = iota
	// Rejected: la URL no vale o la plataforma rechazó el handshake.
	Rejected
	// ClosedEarly: aceptó publish y cerró dentro de la gracia. Casi siempre es la clave.
	ClosedEarly
	// Plausible: aceptó publish y seguía abierta al terminar la gracia. No es "correcta":
	// solo emitir de verdad confirma la clave.
	Plausible
)

func (o Outcome) String() string {
	switch o {
	case Unreachable:
		return "unreachable"
	case Rejected:
		return "rejected"
	case ClosedEarly:
		return "closed_early"
	case Plausible:
		return "plausible"
	default:
		return "desconocido"
	}
}

// Result es lo que devuelve una sonda. Stage es la etapa en la que se decidió: url, dns,
// tcp, tls, connect, createStream, publish o grace. Err nunca contiene la URL ni la clave.
type Result struct {
	Outcome Outcome
	Stage   string
	Elapsed time.Duration
	Err     error
}
```

- [ ] **Step 2: Tests de la sonda (rojo)**

Crear `internal/rtmpio/probe_test.go`:

```go
package rtmpio

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/probe"
)

// sondaHandler es un servidor RTMP cuyo comportamiento decide cada test.
type sondaHandler struct {
	rtmp.DefaultHandler
	conn       net.Conn
	rechazar   bool          // OnConnect devuelve error
	cerrarTras time.Duration // >0: tras aceptar publish, cierra el socket
}

func (h *sondaHandler) OnConnect(ts uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	if h.rechazar {
		return errors.New("rechazado")
	}
	return nil
}

func (h *sondaHandler) OnPublish(_ *rtmp.StreamContext, ts uint32, cmd *rtmpmsg.NetStreamPublish) error {
	if h.cerrarTras > 0 {
		time.AfterFunc(h.cerrarTras, func() { h.conn.Close() })
	}
	return nil
}

// servidorDeSonda levanta el servidor sobre un puerto efímero y devuelve host:puerto.
func servidorDeSonda(t *testing.T, ajustar func(*sondaHandler)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(c net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			h := &sondaHandler{conn: c}
			if ajustar != nil {
				ajustar(h)
			}
			return c, &rtmp.ConnConfig{Handler: h}
		},
	})
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func sondear(t *testing.T, url string, grace time.Duration) probe.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return Probe(ctx, PublisherConfig{URL: url, StreamKey: crypto.Secret("clave-de-prueba")}, grace)
}

func TestProbePlausibleAgainstAServerThatKeepsTheStream(t *testing.T) {
	addr := servidorDeSonda(t, nil)
	res := sondear(t, "rtmp://"+addr+"/live", 300*time.Millisecond)
	if res.Outcome != probe.Plausible {
		t.Fatalf("outcome = %v (etapa %s, err %v), quería plausible", res.Outcome, res.Stage, res.Err)
	}
	if res.Stage != "grace" {
		t.Errorf("stage = %q, quería grace", res.Stage)
	}
}

// Lo que hace Twitch con una clave mala: acepta publish y corta. La sonda lo ve porque el
// bucle de lectura de go-rtmp muere y ClientConn.LastError deja de ser nil.
func TestProbeClosedEarlyWhenTheServerHangsUp(t *testing.T) {
	addr := servidorDeSonda(t, func(h *sondaHandler) { h.cerrarTras = 100 * time.Millisecond })
	res := sondear(t, "rtmp://"+addr+"/live", 1500*time.Millisecond)
	if res.Outcome != probe.ClosedEarly {
		t.Fatalf("outcome = %v (etapa %s, err %v), quería closed_early", res.Outcome, res.Stage, res.Err)
	}
}

// Un connect rechazado llega al cliente como ConnectRejectedError: la sonda lo distingue
// de un cierre.
func TestProbeRejectedWhenConnectIsRefused(t *testing.T) {
	addr := servidorDeSonda(t, func(h *sondaHandler) { h.rechazar = true })
	res := sondear(t, "rtmp://"+addr+"/live", 300*time.Millisecond)
	if res.Outcome != probe.Rejected {
		t.Fatalf("outcome = %v (etapa %s, err %v), quería rejected", res.Outcome, res.Stage, res.Err)
	}
	if res.Stage != "connect" {
		t.Errorf("stage = %q, quería connect", res.Stage)
	}
}

func TestProbeUnreachableOnAClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // el puerto queda cerrado

	res := sondear(t, "rtmp://"+addr+"/live", 300*time.Millisecond)
	if res.Outcome != probe.Unreachable || res.Stage != "tcp" {
		t.Fatalf("outcome = %v etapa %q, quería unreachable/tcp (err %v)", res.Outcome, res.Stage, res.Err)
	}
}

// Un certificado que no verifica es la etapa tls, y la sonda no lo acepta: la
// verificación por defecto se mantiene (spec base §16).
func TestProbeUnreachableOnABadCertificate(t *testing.T) {
	cert := certificadoAutofirmado(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(io.Discard, c); c.Close() }()
		}
	}()

	res := sondear(t, "rtmps://"+ln.Addr().String()+"/live", 300*time.Millisecond)
	if res.Outcome != probe.Unreachable || res.Stage != "tls" {
		t.Fatalf("outcome = %v etapa %q, quería unreachable/tls (err %v)", res.Outcome, res.Stage, res.Err)
	}
}

func TestProbeRejectsABadURLWithoutLeakingIt(t *testing.T) {
	const key = "CLAVESECRETA"
	res := sondear(t, "http://example.com/live/"+key, 300*time.Millisecond)
	if res.Outcome != probe.Rejected || res.Stage != "url" {
		t.Fatalf("outcome = %v etapa %q, quería rejected/url", res.Outcome, res.Stage)
	}
	if res.Err != nil && strings.Contains(res.Err.Error(), key) {
		t.Errorf("el error de la sonda lleva la clave: %v", res.Err)
	}
}

func certificadoAutofirmado(t *testing.T) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}
```

- [ ] **Step 3: Correr en rojo**

```bash
go test ./internal/rtmpio/ -run 'Probe' -count=1
```
Expected: FAIL de compilación, `undefined: Probe`.

- [ ] **Step 4: Etapas en `Connect` y `lastError`**

En `internal/rtmpio/publisher.go`:

1. Añadir tras `connectTimeout`:

```go
// stageError dice en qué paso falló Connect. Lo usa Probe para explicar al usuario si el
// problema fue la red, el TLS o el handshake. El texto del error no cambia: Error()
// delega en el error envuelto.
type stageError struct {
	stage string
	err   error
}

func (e *stageError) Error() string { return e.err.Error() }
func (e *stageError) Unwrap() error { return e.err }

// dialStage clasifica un fallo del dial. Un certificado que no verifica es "tls", un
// nombre que no resuelve es "dns", y lo demás —conexión rechazada, timeout— es "tcp".
func dialStage(err error) string {
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "tls"
	}
	var recErr tls.RecordHeaderError
	if errors.As(err, &recErr) {
		return "tls"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	return "tcp"
}
```

2. En `Connect`, envolver cada `return fmt.Errorf(...)`:

```go
	case <-ctx.Done():
		go func() { … }()
		return &stageError{stage: "tcp", err: fmt.Errorf("conectar a %s: %w", p.tgt.addr, ctx.Err())}
	case r := <-results:
		if r.err != nil {
			return &stageError{stage: dialStage(r.err), err: fmt.Errorf("conectar a %s: %w", p.tgt.addr, r.err)}
		}
```

```go
	}); err != nil {
		return &stageError{stage: "connect", err: fmt.Errorf("handshake connect con %s: %w", p.tgt.addr, err)}
	}
```

```go
	if err != nil {
		return &stageError{stage: "createStream", err: fmt.Errorf("createStream con %s: %w", p.tgt.addr, err)}
	}
```

```go
	}); err != nil {
		return &stageError{stage: "publish", err: fmt.Errorf("publish en %s: %w", p.tgt.addr, err)}
	}
```

3. Añadir tras `liveStream`:

```go
// lastError devuelve el error con el que murió el bucle de lectura de go-rtmp, o nil si la
// conexión sigue viva. Es la única señal de que el peer colgó: go-rtmp no avisa de otra
// forma, y Publish no espera el onStatus.
func (p *Publisher) lastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return errors.New("sin conexión")
	}
	return p.conn.LastError()
}
```

- [ ] **Step 5: Implementar `Probe`**

Crear `internal/rtmpio/probe.go`:

```go
package rtmpio

import (
	"context"
	"errors"
	"time"

	"github.com/aprendomx/splitstream/internal/probe"
)

// Probe comprueba un destino SIN emitir: conecta, hace connect, createStream y publish,
// espera `grace` y cierra con FCUnpublish (spec v0.8 §3).
//
// Lo que no puede prometer: Stream.Publish de go-rtmp no espera el onStatus, así que una
// clave mala solo se ve si la plataforma cierra el socket dentro de la gracia. Por eso el
// resultado bueno es "plausible" y no "correcta".
//
// Ningún error reproduce la URL ni la clave: se heredan las reglas de parseTarget.
func Probe(ctx context.Context, cfg PublisherConfig, grace time.Duration) probe.Result {
	inicio := time.Now()
	done := func(o probe.Outcome, stage string, err error) probe.Result {
		return probe.Result{Outcome: o, Stage: stage, Elapsed: time.Since(inicio), Err: err}
	}

	p, err := NewPublisher(cfg)
	if err != nil {
		return done(probe.Rejected, "url", err)
	}
	defer p.Close()

	if err := p.Connect(ctx); err != nil {
		stage := "connect"
		var se *stageError
		if errors.As(err, &se) {
			stage = se.stage
		}
		switch stage {
		case "dns", "tcp", "tls":
			return done(probe.Unreachable, stage, err)
		default:
			return done(probe.Rejected, stage, err)
		}
	}

	select {
	case <-ctx.Done():
		return done(probe.Rejected, "grace", ctx.Err())
	case <-time.After(grace):
	}

	if err := p.lastError(); err != nil {
		return done(probe.ClosedEarly, "grace", err)
	}
	return done(probe.Plausible, "grace", nil)
}
```

- [ ] **Step 6: Correr en verde**

```bash
go test ./internal/rtmpio/ ./internal/probe/ -race -count=1
```
Expected: PASS, incluidos los tests anteriores de `publisher_test.go` (los textos de error no cambiaron).

- [ ] **Step 7: Commit**

```bash
git add internal/probe internal/rtmpio/probe.go internal/rtmpio/probe_test.go internal/rtmpio/publisher.go
git commit -m "feat(rtmpio): sonda de destino sin emitir"
```

---

### Task 6: Probar destino: fábrica, endpoint, panel y frontera en la CI

**Files:**
- Modify: `internal/sinks/factory.go` (`Test`), `internal/sinks/factory_test.go`
- Create: `internal/httpapi/test_destination.go`, `internal/httpapi/test_destination_test.go`
- Modify: `internal/httpapi/server.go` (`Config.Tester`, campo, ruta)
- Modify: `cmd/splitstream/main.go` (`Tester: factory`)
- Modify: `.github/workflows/ci.yml` (nueva frontera)
- Modify: `web/src/api.js`, `web/src/components/TarjetaDestino.vue`, `web/src/pages/Panel.vue`

**Interfaces:**
- Consumes: `rtmpio.Probe`, `probe.Result` (Task 5); `store.DestinationKeyForRelay`.
- Produces: `sinks.(*Factory).Test(ctx, d store.Destination) (probe.Result, error)`; `httpapi.DestinationTester` interface; `POST /api/destinations/{id}/test` → `{"outcome","stage","elapsed_ms","message"}`; evento `destination_tested`.

- [ ] **Step 1: Test de la fábrica (rojo)**

Añadir al final de `internal/sinks/factory_test.go`:

```go
// Test descifra la clave con DestinationKeyForRelay —no es una divulgación, no se
// audita— y sondea. Contra un puerto cerrado el resultado es "unreachable" y no un error:
// el error es para "no pude ni intentarlo".
func TestTestProbesTheDestination(t *testing.T) {
	db, c, f := setup(t)
	ctx := context.Background()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	d, err := db.CreateDestination(ctx, c, store.NewDestination{
		Name: "cerrado", Platform: store.PlatformCustom, RTMPURL: "rtmp://" + addr + "/live",
		Key: crypto.Secret("k"), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := f.Test(ctx, *d)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if res.Outcome != probe.Unreachable {
		t.Errorf("outcome = %v, quería unreachable", res.Outcome)
	}

	eventos, err := db.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range eventos {
		if e.Kind == "key_revealed" {
			t.Error("Test auditó como si hubiera revelado la clave a una persona")
		}
	}
}

func TestTestMissingDestination(t *testing.T) {
	_, _, f := setup(t)
	if _, err := f.Test(context.Background(), store.Destination{ID: 9999}); err == nil {
		t.Error("quería error para un destino que no existe")
	}
}
```

Añadir `"github.com/aprendomx/splitstream/internal/probe"` a los imports del test.

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/sinks/ -run 'TestTest' -count=1
```
Expected: FAIL, `f.Test undefined`.

- [ ] **Step 3: Implementar `Factory.Test`**

En `internal/sinks/factory.go`, añadir `"time"` y `"github.com/aprendomx/splitstream/internal/probe"` a los imports, y al final:

```go
// probeGrace es lo que se espera tras publish antes de dar la configuración por
// plausible. Twitch corta una clave mala en menos de un segundo; tres da margen a
// plataformas más lentas sin que el botón parezca colgado.
const probeGrace = 3 * time.Second

// Test sondea un destino sin emitir (spec v0.8 §3). Como Build, lee la clave con
// DestinationKeyForRelay: no es una divulgación y no se audita como tal.
func (f *Factory) Test(ctx context.Context, d store.Destination) (probe.Result, error) {
	key, err := f.db.DestinationKeyForRelay(ctx, f.cipher, d.ID)
	if err != nil {
		return probe.Result{}, err
	}
	return rtmpio.Probe(ctx, rtmpio.PublisherConfig{
		URL: d.RTMPURL, StreamKey: key, Logger: f.logger,
	}, probeGrace), nil
}
```

- [ ] **Step 4: Correr en verde y commitear**

```bash
go test ./internal/sinks/ -race -count=1
git add internal/sinks
git commit -m "feat(sinks): sondear un destino desde la fábrica"
```

- [ ] **Step 5: Tests del endpoint (rojo)**

Crear `internal/httpapi/test_destination_test.go`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/probe"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// fakeTester devuelve un resultado fijo y apunta a quién sondeó.
type fakeTester struct {
	res      probe.Result
	err      error
	sondeado []int64
}

func (f *fakeTester) Test(ctx context.Context, d store.Destination) (probe.Result, error) {
	f.sondeado = append(f.sondeado, d.ID)
	return f.res, f.err
}

func decodeProbe(t *testing.T, body []byte) probeDTO {
	t.Helper()
	var out probeDTO
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decodificar: %v — %s", err, body)
	}
	return out
}

func TestTestDestinationReportsTheOutcome(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "clave-inconfundible", true)
	ft := &fakeTester{res: probe.Result{Outcome: probe.Plausible, Stage: "grace", Elapsed: 3100 * time.Millisecond}}
	srv.tester = ft

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodeProbe(t, rec.Body.Bytes())
	if got.Outcome != "plausible" || got.Stage != "grace" || got.ElapsedMS != 3100 {
		t.Errorf("dto = %+v", got)
	}
	if got.Message == "" {
		t.Error("falta el mensaje para personas")
	}
	if len(ft.sondeado) != 1 || ft.sondeado[0] != d.ID {
		t.Errorf("se sondeó %v, quería [%d]", ft.sondeado, d.ID)
	}

	eventos, err := db.RecentEvents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventos) == 0 || eventos[0].Kind != "destination_tested" || eventos[0].Level != store.LevelInfo {
		t.Errorf("evento = %+v, quería destination_tested info", eventos)
	}
}

// Un resultado malo queda como warn, y el mensaje explica qué revisar.
func TestTestDestinationClosedEarlyIsAWarning(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "tw", "k", true)
	srv.tester = &fakeTester{res: probe.Result{Outcome: probe.ClosedEarly, Stage: "grace", Err: errors.New("EOF")}}

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", "")
	got := decodeProbe(t, rec.Body.Bytes())
	if got.Outcome != "closed_early" || !strings.Contains(got.Message, "clave") {
		t.Errorf("dto = %+v", got)
	}
	eventos, _ := db.RecentEvents(context.Background(), 1)
	if eventos[0].Level != store.LevelWarn {
		t.Errorf("nivel = %s, quería warn", eventos[0].Level)
	}
}

// Probar un destino que está emitiendo abriría una segunda publicación con la misma
// clave, y la plataforma cortaría la que va en vivo.
func TestTestDestinationRefusesALiveDestination(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)
	ft := &fakeTester{res: probe.Result{Outcome: probe.Plausible}}
	srv.tester = ft
	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{d.ID: {State: "live"}})

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("código = %d, quería 409: %s", rec.Code, rec.Body.String())
	}
	if len(ft.sondeado) != 0 {
		t.Error("se sondeó un destino en vivo")
	}
}

// Con sesión viva pero el destino apagado o suspendido, sí se puede probar.
func TestTestDestinationAllowedWhenNotLive(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)
	srv.tester = &fakeTester{res: probe.Result{Outcome: probe.Plausible}}
	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{d.ID: {State: "suspended"}})

	if rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", ""); rec.Code != http.StatusOK {
		t.Fatalf("código = %d, quería 200: %s", rec.Code, rec.Body.String())
	}
}

func TestTestDestinationUnknownIs404(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.tester = &fakeTester{}
	if rec := do(t, srv, cookies, http.MethodPost, destPath(9999)+"/test", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("código = %d, quería 404", rec.Code)
	}
}

func TestTestDestinationWithoutATesterIsAConflict(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)
	srv.tester = nil
	if rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", ""); rec.Code != http.StatusConflict {
		t.Fatalf("código = %d, quería 409", rec.Code)
	}
}

// El evento y la respuesta no pueden llevar la URL ni la clave: el error de la sonda
// tampoco las lleva, pero esto lo comprueba en la frontera HTTP.
func TestTestDestinationNeverLeaksTheKey(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	const clave = "clave-inconfundible-9x7"
	d := crearDest(t, db, srv, "yt", clave, true)
	srv.tester = &fakeTester{res: probe.Result{Outcome: probe.Rejected, Stage: "publish", Err: errors.New("publish en host: rechazado")}}

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", "")
	if strings.Contains(rec.Body.String(), clave) {
		t.Error("la respuesta lleva la clave")
	}
	eventos, _ := db.RecentEvents(context.Background(), 1)
	if strings.Contains(eventos[0].Message, clave) {
		t.Error("el evento lleva la clave")
	}
}
```

- [ ] **Step 6: Correr en rojo**

```bash
go test ./internal/httpapi/ -run 'TestTestDestination' -count=1
```
Expected: FAIL de compilación, `srv.tester undefined`.

- [ ] **Step 7: Implementar el endpoint**

En `internal/httpapi/server.go`:

1. Añadir `"github.com/aprendomx/splitstream/internal/probe"` a los imports.
2. Tras `SinkBuilder`:

```go
// DestinationTester sondea un destino sin emitir. Lo cumple *sinks.Factory. Devuelve
// probe.Result y no un tipo de rtmpio para que este paquete no importe go-rtmp ni de
// forma transitiva.
type DestinationTester interface {
	Test(ctx context.Context, d store.Destination) (probe.Result, error)
}
```

3. En `Config`, tras `Sinks`: `Tester DestinationTester`. En `Server`, tras `sinks`: `tester DestinationTester`. En `New`: `tester: cfg.Tester,`.
4. En `routes()`, tras la de `/retry`: `protegida("POST /api/destinations/{id}/test", s.handleTestDestination)`.

Crear `internal/httpapi/test_destination.go`:

```go
package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/aprendomx/splitstream/internal/probe"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// probeTimeout acota la sonda entera: dial (15 s en rtmpio) + gracia (3 s) + margen.
const probeTimeout = 30 * time.Second

type probeDTO struct {
	Outcome   string `json:"outcome"`
	Stage     string `json:"stage"`
	ElapsedMS int64  `json:"elapsed_ms"`
	// Message es para personas y NUNCA lleva la URL ni la clave: se compone aquí a partir
	// del veredicto y la etapa, no del texto del error.
	Message string `json:"message"`
}

func newProbeDTO(r probe.Result) probeDTO {
	return probeDTO{
		Outcome:   r.Outcome.String(),
		Stage:     r.Stage,
		ElapsedMS: r.Elapsed.Milliseconds(),
		Message:   probeMessage(r),
	}
}

// probeMessage traduce el veredicto a lo que el usuario puede hacer. Es el mismo criterio
// que diagnostico.js en el panel: el estado, no la traza.
func probeMessage(r probe.Result) string {
	switch r.Outcome {
	case probe.Plausible:
		return "La plataforma aceptó la conexión y la mantuvo abierta. La configuración es " +
			"plausible; solo emitir de verdad confirma la clave."
	case probe.ClosedEarly:
		return "La plataforma aceptó la conexión y la cerró enseguida. Casi siempre es la " +
			"clave, o una emisión que ya no está abierta en la plataforma."
	case probe.Rejected:
		if r.Stage == "url" {
			return "La URL del destino no vale: tiene que empezar por rtmp:// o rtmps:// y " +
				"llevar servidor y aplicación."
		}
		return fmt.Sprintf("La plataforma rechazó el handshake en «%s». Revisa la URL.", r.Stage)
	default:
		switch r.Stage {
		case "dns":
			return "No se resuelve el nombre del servidor. Revisa la URL."
		case "tls":
			return "El certificado del servidor no es válido. Revisa que la URL sea la de la " +
				"plataforma y no la de un intermediario."
		default:
			return "No se pudo conectar con el servidor. Revisa la URL, el puerto y tu red."
		}
	}
}

// handleTestDestination sondea un destino sin emitir (spec v0.8 §3).
func (s *Server) handleTestDestination(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	d, err := s.db.DestinationByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if s.tester == nil {
		writeError(w, http.StatusConflict, codeConflict, "probar destinos no está disponible en este arranque")
		return
	}
	// Con el destino emitiendo, una segunda publicación con la misma clave haría que la
	// plataforma expulsara a la que va en vivo. Apagado o suspendido sí se puede probar.
	if s.liveSession() {
		if m, ok := s.engine.Snapshot()[id]; ok && m.State == relay.StateLive.String() {
			writeError(w, http.StatusConflict, codeConflict,
				"el destino está emitiendo ahora mismo: probarlo abriría una segunda publicación "+
					"con la misma clave y la plataforma cortaría la que va en vivo")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	res, err := s.tester.Test(ctx, *d)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	dto := newProbeDTO(res)

	level := store.LevelWarn
	if res.Outcome == probe.Plausible {
		level = store.LevelInfo
	}
	if _, err := s.db.LogEvent(r.Context(), store.Event{
		DestinationID: &id, Level: level, Kind: "destination_tested",
		Message: "se probó el destino: " + dto.Message,
	}); err != nil {
		s.logger.Error("no se pudo registrar la prueba del destino", "err", err)
	}
	writeJSON(w, http.StatusOK, dto)
}
```

En `cmd/splitstream/main.go`, en el literal de `httpapi.Config`, tras `Sinks: factory,`: `Tester: factory,`.

- [ ] **Step 8: Correr en verde**

```bash
go test ./internal/httpapi/ -race -count=1 && go build ./...
```
Expected: PASS.

- [ ] **Step 9: La frontera en la CI**

En `.github/workflows/ci.yml`, en el paso «internal/httpapi no conoce go-rtmp», cambiar el `grep` a:

```yaml
          if go list -deps ./internal/httpapi | grep -E 'go-rtmp|internal/rtmpio'; then
            echo "::error::internal/httpapi importa go-rtmp o internal/rtmpio"
            exit 1
          fi
```

y en el paso «internal/relay sigue aislado»:

```yaml
          if go list -deps ./internal/relay | grep -E 'go-rtmp|database/sql|internal/store|internal/events'; then
            echo "::error::internal/relay importa go-rtmp, database/sql, store o events"
            exit 1
          fi
```

Comprobar en local:

```bash
go list -deps ./internal/httpapi | grep -E 'go-rtmp|internal/rtmpio'; echo "exit=$?"
go list -deps ./internal/relay | grep -E 'go-rtmp|database/sql|internal/store|internal/events'; echo "exit=$?"
```
Expected: `exit=1` las dos (sin coincidencias).

- [ ] **Step 10: Commit del backend**

```bash
git add internal/httpapi/test_destination.go internal/httpapi/test_destination_test.go internal/httpapi/server.go cmd/splitstream/main.go .github/workflows/ci.yml
git commit -m "feat(api): probar un destino sin emitir"
```

- [ ] **Step 11: Panel — «Probar» en el menú de la tarjeta**

En `web/src/api.js`, tras `reintentarDestino`:

```js
  probarDestino: (id) => pedir('POST', `/api/destinations/${id}/test`),
```

En `web/src/components/TarjetaDestino.vue`:

- `defineEmits([... , 'probar'])`.
- En el `<q-menu>`, antes del `<q-separator />`:

```html
            <q-item clickable v-close-popup @click="$emit('probar')">
              <q-item-section avatar><q-icon :name="iProbar" /></q-item-section>
              <q-item-section>
                Probar
                <q-item-label caption>Conecta sin emitir</q-item-label>
              </q-item-section>
            </q-item>
```

En `web/src/iconos.js`, añadir `mdiConnection as iProbar,` a la lista exportada, e importarlo en la tarjeta.

En `web/src/pages/Panel.vue`, `@probar="probar(element)"` en `<TarjetaDestino>`, y:

```js
const TITULOS_SONDA = {
  plausible: { titulo: 'Configuración plausible', tipo: 'positive' },
  closed_early: { titulo: 'Conecta y se corta', tipo: 'warning' },
  rejected: { titulo: 'Rechazado', tipo: 'negative' },
  unreachable: { titulo: 'No se llega al servidor', tipo: 'negative' },
}

async function probar(d) {
  // Facebook cuenta cada publicación como emisión activa, y las cuenta contra un cupo.
  if (d.platform === 'facebook') {
    const seguir = await new Promise((resolve) => {
      $q.dialog({
        title: 'Probar en Facebook',
        message: 'Facebook cuenta cada prueba como una emisión activa. Si tienes el cupo justo, mejor no.',
        cancel: { flat: true, noCaps: true, label: 'Cancelar' },
        ok: { unelevated: true, noCaps: true, color: 'primary', label: 'Probar igual' },
      }).onOk(() => resolve(true)).onCancel(() => resolve(false))
    })
    if (!seguir) return
  }
  const aviso = $q.notify({ type: 'ongoing', message: `Probando ${d.name}…`, timeout: 0 })
  try {
    const r = await api.probarDestino(d.id)
    const t = TITULOS_SONDA[r.outcome] ?? { titulo: r.outcome, tipo: 'info' }
    aviso()
    $q.dialog({
      title: t.titulo,
      message: `${r.message}<br><br><span class="text-caption text-grey-5">${r.stage} · ${(r.elapsed_ms / 1000).toFixed(1)} s</span>`,
      html: true,
      ok: { flat: true, noCaps: true, label: 'Cerrar' },
    })
    panel.refrescarEventos()
  } catch (e) {
    aviso()
    $q.notify({ type: 'negative', message: e.message })
  }
}
```

- [ ] **Step 12: Compilar el panel y commitear**

```bash
cd web && npm run build && cd ..
git add web/src
git commit -m "feat(panel): probar un destino desde la tarjeta"
```

---

### Task 7: `/healthz`, `-healthcheck` y el `HEALTHCHECK` de Docker

**Files:**
- Modify: `internal/store/db.go` (`Ping`)
- Create: `internal/httpapi/health.go`, `internal/httpapi/health_test.go`
- Modify: `internal/httpapi/server.go` (ruta pública)
- Modify: `cmd/splitstream/main.go` (flag `-healthcheck`), `cmd/splitstream/main_test.go`
- Modify: `deploy/Dockerfile`, `deploy/docker-compose.yml`

**Interfaces:**
- Produces: `GET /healthz` → `200 {"status":"ok","db":"ok"}` | `503 {"status":"degraded","db":"error"}`; `store.(*DB).Ping(ctx) error`; `main.healthcheck(httpAddr string) error`.

- [ ] **Step 1: Tests (rojo)**

Crear `internal/httpapi/health_test.go`:

```go
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// /healthz es público: lo consulta un healthcheck de Docker o un monitor externo, que no
// tienen cookie. No revela nada más que "existe y la base responde".
func TestHealthzIsPublicAndSaysOK(t *testing.T) {
	srv, _ := newTestServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	var got healthDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" || got.DB != "ok" {
		t.Errorf("dto = %+v", got)
	}
	// Sin versión: eso solo va en el estado autenticado.
	var suelto map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &suelto)
	if _, hay := suelto["version"]; hay {
		t.Error("/healthz revela la versión; eso solo va en el estado autenticado")
	}
}

func TestHealthzDegradesWhenTheDatabaseIsGone(t *testing.T) {
	srv, db := newTestServer(t)
	db.Close()

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("código = %d, quería 503: %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/httpapi/ -run 'Healthz' -count=1
```
Expected: FAIL, `undefined: healthDTO`.

- [ ] **Step 3: Implementar**

En `internal/store/db.go`, tras `Close`:

```go
// Ping comprueba que la base responde. Lo usa /healthz; un SELECT y no solo PingContext
// porque este último puede dar por buena una conexión que ya no puede leer el archivo.
func (d *DB) Ping(ctx context.Context) error {
	var uno int
	if err := d.ex.QueryRowContext(ctx, `SELECT 1`).Scan(&uno); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
}
```

Crear `internal/httpapi/health.go`:

```go
package httpapi

import (
	"context"
	"net/http"
	"time"
)

type healthDTO struct {
	Status string `json:"status"`
	DB     string `json:"db"`
}

// handleHealthz es público (spec v0.8 §6): responde si el proceso atiende y la base
// contesta. No mira el motor —"sin sesión" no es "enfermo"— ni dice la versión.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		s.logger.Warn("healthz: la base no responde", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, healthDTO{Status: "degraded", DB: "error"})
		return
	}
	writeJSON(w, http.StatusOK, healthDTO{Status: "ok", DB: "ok"})
}
```

En `routes()`, junto a las públicas: `s.mux.HandleFunc("GET /healthz", s.handleHealthz)`.

- [ ] **Step 4: Correr en verde y commitear**

```bash
go test ./internal/httpapi/ ./internal/store/ -race -count=1
git add internal/store/db.go internal/httpapi/health.go internal/httpapi/health_test.go internal/httpapi/server.go
git commit -m "feat(api): GET /healthz público"
```

- [ ] **Step 5: Test del `-healthcheck` (rojo)**

Añadir al final de `cmd/splitstream/main_test.go`:

```go
// -healthcheck existe porque la imagen es scratch y no tiene curl. Sale 0 si /healthz da
// 200 y 1 si no; no toca la configuración ni crea archivos de clave.
func TestHealthcheckFollowsHealthz(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if err := healthcheck(strings.TrimPrefix(ok.URL, "http://")); err != nil {
		t.Errorf("healthcheck contra un servidor sano = %v", err)
	}

	malo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer malo.Close()
	if err := healthcheck(strings.TrimPrefix(malo.URL, "http://")); err == nil {
		t.Error("healthcheck contra un 503 = nil, quería error")
	}

	if err := healthcheck(freeAddr(t)); err == nil {
		t.Error("healthcheck contra nadie = nil, quería error")
	}
}

// Un addr como ":8080" —el valor por defecto— apunta a la propia máquina.
func TestHealthcheckURLFor(t *testing.T) {
	for addr, want := range map[string]string{
		":8080":          "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/healthz",
		"127.0.0.1:8081": "http://127.0.0.1:8081/healthz",
	} {
		if got := healthcheckURL(addr); got != want {
			t.Errorf("healthcheckURL(%q) = %q, quería %q", addr, got, want)
		}
	}
}
```

Añadir `"net/http/httptest"` a los imports del test si no está.

- [ ] **Step 6: Correr en rojo**

```bash
go test ./cmd/splitstream/ -run 'Healthcheck' -count=1
```
Expected: FAIL, `undefined: healthcheck`.

- [ ] **Step 7: Implementar el flag**

En `cmd/splitstream/main.go`:

1. En `main()`, tras `setpw`: `hc := flag.Bool("healthcheck", false, "consulta /healthz del servicio local y sale 0 si responde; para el HEALTHCHECK de Docker")`, y tras el bloque de `showVersion`:

```go
	if *hc {
		// Solo el puerto: config.Load crearía un archivo de clave si no lo hubiera, y un
		// healthcheck no debe tener efectos secundarios.
		addr := os.Getenv("SPLITSTREAM_HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		if err := healthcheck(addr); err != nil {
			fmt.Fprintln(os.Stderr, "healthcheck:", err)
			os.Exit(1)
		}
		return
	}
```

2. Al final del archivo:

```go
// healthcheckURL apunta siempre a la propia máquina: el addr de escucha puede ser ":8080"
// o "0.0.0.0:8080", que no son direcciones a las que conectar.
func healthcheckURL(addr string) string {
	_, puerto, err := net.SplitHostPort(addr)
	if err != nil || puerto == "" {
		puerto = "8080"
	}
	return "http://127.0.0.1:" + puerto + "/healthz"
}

func healthcheck(addr string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(healthcheckURL(addr))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/healthz respondió %d", resp.StatusCode)
	}
	return nil
}
```

Añadir `"net"` a los imports de `main.go`.

- [ ] **Step 8: Docker**

En `deploy/Dockerfile`, antes de `ENTRYPOINT`:

```dockerfile
# La imagen es scratch: no hay curl ni wget. El propio binario hace la comprobación.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD ["/splitstream", "-healthcheck"]
```

En `deploy/docker-compose.yml`, sustituir el comentario «Sin healthcheck con curl…» por:

```yaml
    # El healthcheck lo trae la imagen: `splitstream -healthcheck` consulta /healthz.
    # `docker compose ps` enseña healthy/unhealthy.
```

- [ ] **Step 9: Verificar y commitear**

```bash
go test ./cmd/splitstream/ -race -count=1
docker build -f deploy/Dockerfile -t splitstream:hc . && docker run -d --name hc -e SPLITSTREAM_MASTER_KEY="$(docker run --rm splitstream:hc -genkey)" splitstream:hc && sleep 20 && docker inspect --format '{{.State.Health.Status}}' hc; docker rm -f hc
```
Expected: `healthy`.

```bash
git add cmd/splitstream deploy/Dockerfile deploy/docker-compose.yml
git commit -m "feat: -healthcheck y HEALTHCHECK en la imagen"
```

---

### Task 8: `/metrics` en formato Prometheus

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go` (`MetricsToken`)
- Create: `internal/httpapi/metrics.go`, `internal/httpapi/metrics_test.go`
- Modify: `internal/httpapi/server.go` (`Config.MetricsToken`, `Config.ExtraMetrics`, ruta, `requireSessionOrToken`)
- Modify: `cmd/splitstream/main.go` (token y métricas del bus)

**Interfaces:**
- Produces: `httpapi.Metric{Name, Help, Type string; Labels map[string]string; Value float64}`; `httpapi.ExtraMetrics func() []Metric`; `Config.ExtraMetrics []ExtraMetrics`; `GET /metrics`.

- [ ] **Step 1: Test de config (rojo)**

En `internal/config/config_test.go`, añadir:

```go
func TestMetricsTokenIsReadAndNeverLogged(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY":    testKeyB64(),
		"SPLITSTREAM_METRICS_TOKEN": "token-de-metricas-inconfundible",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.MetricsToken != "token-de-metricas-inconfundible" {
		t.Errorf("MetricsToken = %q", cfg.MetricsToken)
	}

	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("config", "config", cfg)
	if strings.Contains(buf.String(), "inconfundible") {
		t.Error("el token de métricas salió por el log")
	}
	blob, _ := json.Marshal(cfg)
	if strings.Contains(string(blob), "inconfundible") {
		t.Error("el token de métricas salió por JSON")
	}
}
```

- [ ] **Step 2: Implementar en config**

En `internal/config/config.go`, en `Config` tras `SecureCookies`:

```go
	// MetricsToken autoriza GET /metrics con `Authorization: Bearer`. Vacío: solo cookie
	// de sesión. Se omite en LogValue y MarshalJSON como la master key.
	MetricsToken string
```

y en `LoadFrom`, tras `SecureCookies:`: `MetricsToken: get("SPLITSTREAM_METRICS_TOKEN", ""),`. `LogValue` y `MarshalJSON` ya enumeran campos explícitos: no hay que tocarlos.

```bash
go test ./internal/config/ -race -count=1
```
Expected: PASS.

- [ ] **Step 3: Tests del endpoint (rojo)**

Crear `internal/httpapi/metrics_test.go`:

```go
package httpapi

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/relay"
)

// parseMetrics lee la exposición de Prometheus lo justo para afirmar sobre ella: una
// muestra por línea, `nombre{etiquetas} valor`.
func parseMetrics(t *testing.T, body string) map[string]string {
	t.Helper()
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		linea := sc.Text()
		if linea == "" || strings.HasPrefix(linea, "#") {
			continue
		}
		i := strings.LastIndex(linea, " ")
		if i < 0 {
			t.Fatalf("línea sin valor: %q", linea)
		}
		out[linea[:i]] = linea[i+1:]
	}
	return out
}

func getMetrics(t *testing.T, srv *Server, cookies []*http.Cookie, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

func TestMetricsRequiresSessionOrToken(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.metricsToken = "secreto"

	if rec := getMetrics(t, srv, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("sin nada: %d, quería 401", rec.Code)
	}
	if rec := getMetrics(t, srv, nil, "otro"); rec.Code != http.StatusUnauthorized {
		t.Errorf("token malo: %d, quería 401", rec.Code)
	}
	if rec := getMetrics(t, srv, nil, "secreto"); rec.Code != http.StatusOK {
		t.Errorf("token bueno: %d, quería 200", rec.Code)
	}
	if rec := getMetrics(t, srv, cookies, ""); rec.Code != http.StatusOK {
		t.Errorf("cookie: %d, quería 200", rec.Code)
	}
}

// Sin token configurado, un Bearer cualquiera no vale: solo la cookie.
func TestMetricsWithoutATokenOnlyAcceptsTheCookie(t *testing.T) {
	srv, _, _, _, _ := newDestServer(t)
	srv.metricsToken = ""
	if rec := getMetrics(t, srv, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("Bearer vacío sin token configurado: %d, quería 401", rec.Code)
	}
}

func TestMetricsExposeSessionAndDestinations(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, `canal "raro"`, "k", true)
	eng.setSesion(relay.LiveSession{ID: 7, BitrateBPS: 3_000_000})
	eng.setMetrics(map[int64]relay.Metrics{d.ID: {
		State: "live", Degraded: true, BytesSent: 1234, BitrateBPS: 2_900_000,
		DroppedFrames: 5, Reconnections: 2, QueuedBytes: 99,
	}})

	rec := getMetrics(t, srv, cookies, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	m := parseMetrics(t, rec.Body.String())

	if m["splitstream_session_live"] != "1" {
		t.Errorf("session_live = %q", m["splitstream_session_live"])
	}
	if m["splitstream_session_bitrate_bps"] != "3000000" {
		t.Errorf("session_bitrate = %q", m["splitstream_session_bitrate_bps"])
	}
	// Las etiquetas con comillas se escapan; el nombre lo escribe el usuario.
	base := `destination="` + itoa(d.ID) + `",name="canal \"raro\"",platform="custom"`
	if m[`splitstream_destination_state{`+base+`,state="live"}`] != "1" {
		t.Errorf("falta la serie live=1; claves: %v", claves(m))
	}
	if m[`splitstream_destination_state{`+base+`,state="idle"}`] != "0" {
		t.Error("falta la serie idle=0")
	}
	if m[`splitstream_destination_bytes_sent_total{`+base+`}`] != "1234" {
		t.Error("bytes_sent_total mal")
	}
	if m[`splitstream_destination_degraded{`+base+`}`] != "1" {
		t.Error("degraded mal")
	}
}

func TestMetricsIncludeExtras(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.extra = []ExtraMetrics{func() []Metric {
		return []Metric{{Name: "splitstream_events_bus_dropped_total", Help: "x", Type: "counter", Value: 3}}
	}}
	m := parseMetrics(t, getMetrics(t, srv, cookies, "").Body.String())
	if m["splitstream_events_bus_dropped_total"] != "3" {
		t.Errorf("extra = %q", m["splitstream_events_bus_dropped_total"])
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func claves(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

Añadir `"strconv"` a los imports.

- [ ] **Step 4: Correr en rojo**

```bash
go test ./internal/httpapi/ -run 'Metrics' -count=1
```
Expected: FAIL, `srv.metricsToken undefined`.

- [ ] **Step 5: Implementar**

En `internal/httpapi/server.go`:

- `Config` gana `MetricsToken string` y `ExtraMetrics []ExtraMetrics`; `Server` gana `metricsToken string` y `extra []ExtraMetrics`; `New` los copia.
- En `routes()`, tras `/healthz`: `s.mux.Handle("GET /metrics", s.requireSessionOrToken(http.HandlerFunc(s.handleMetrics)))`.

Crear `internal/httpapi/metrics.go`:

```go
package httpapi

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
)

// Metric es una muestra en el formato de exposición de Prometheus. Se escribe a mano: son
// ochenta líneas, y el spec base §5 no quiere una dependencia para esto.
type Metric struct {
	Name   string
	Help   string
	Type   string // "gauge" | "counter"
	Labels map[string]string
	Value  float64
}

// ExtraMetrics permite a otros componentes —el bus de eventos, los webhooks— aportar sus
// contadores sin que este paquete los importe.
type ExtraMetrics func() []Metric

// requireSessionOrToken protege /metrics: cookie de sesión, o Bearer con el token
// configurado. Sin token configurado, solo cookie. La comparación es en tiempo constante.
func (s *Server) requireSessionOrToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			dado := strings.TrimPrefix(auth, "Bearer ")
			if s.metricsToken != "" && subtle.ConstantTimeCompare([]byte(dado), []byte(s.metricsToken)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "token de métricas inválido")
			return
		}
		s.requireSession(next).ServeHTTP(w, r)
	})
}

// estados son las series de destination_state: una por estado, 1 en el activo. Así en
// PromQL se pregunta `splitstream_destination_state{state="live"} == 1` sin parsear texto.
var estados = []relay.State{
	relay.StateIdle, relay.StateConnecting, relay.StateLive,
	relay.StateReconnecting, relay.StateError, relay.StateSuspended,
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var ms []Metric
	add := func(name, help, typ string, labels map[string]string, v float64) {
		ms = append(ms, Metric{Name: name, Help: help, Type: typ, Labels: labels, Value: v})
	}

	add("splitstream_build_info", "Versión del binario.", "gauge", map[string]string{"version": s.version}, 1)

	var live float64
	if s.engine != nil {
		if ses := s.engine.Session(); ses.ID != 0 {
			live = 1
			add("splitstream_session_bitrate_bps", "Bitrate medido de la ingesta.", "gauge", nil, float64(ses.BitrateBPS))
			add("splitstream_session_uptime_seconds", "Segundos desde que el publisher conectó.", "gauge", nil,
				time.Since(ses.StartedAt).Seconds())
		}
	}
	add("splitstream_session_live", "1 si hay un publisher emitiendo.", "gauge", nil, live)

	dests, err := s.db.ListDestinations(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	var snap map[int64]relay.Metrics
	if s.engine != nil {
		snap = s.engine.Snapshot()
	}
	for _, d := range dests {
		base := map[string]string{
			"destination": strconv.FormatInt(d.ID, 10), "name": d.Name, "platform": string(d.Platform),
		}
		m, ok := snap[d.ID]
		if !ok {
			m = relay.Metrics{State: relay.StateIdle.String()}
		}
		for _, st := range estados {
			l := clonar(base)
			l["state"] = st.String()
			var v float64
			if m.State == st.String() {
				v = 1
			}
			add("splitstream_destination_state", "Estado del destino: 1 en la serie activa.", "gauge", l, v)
		}
		add("splitstream_destination_degraded", "1 si descartó vídeo en los últimos 10 s.", "gauge", base, b2f(m.Degraded))
		add("splitstream_destination_bytes_sent_total", "Bytes enviados en esta sesión.", "counter", base, float64(m.BytesSent))
		add("splitstream_destination_bitrate_bps", "Bitrate de salida, media móvil de 5 s.", "gauge", base, float64(m.BitrateBPS))
		add("splitstream_destination_dropped_frames_total", "Mensajes descartados por la cola.", "counter", base, float64(m.DroppedFrames))
		add("splitstream_destination_reconnections_total", "Reconexiones en esta sesión.", "counter", base, float64(m.Reconnections))
		add("splitstream_destination_queued_bytes", "Bytes encolados hacia el destino.", "gauge", base, float64(m.QueuedBytes))
	}

	for _, extra := range s.extra {
		ms = append(ms, extra()...)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	escribirMetricas(w, ms)
}

// escribirMetricas agrupa por nombre para escribir HELP y TYPE una sola vez, en el orden
// de primera aparición.
func escribirMetricas(w http.ResponseWriter, ms []Metric) {
	var orden []string
	porNombre := map[string][]Metric{}
	for _, m := range ms {
		if _, visto := porNombre[m.Name]; !visto {
			orden = append(orden, m.Name)
		}
		porNombre[m.Name] = append(porNombre[m.Name], m)
	}
	for _, nombre := range orden {
		grupo := porNombre[nombre]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", nombre, grupo[0].Help, nombre, grupo[0].Type)
		for _, m := range grupo {
			fmt.Fprintf(w, "%s%s %s\n", nombre, etiquetas(m.Labels), strconv.FormatFloat(m.Value, 'f', -1, 64))
		}
	}
}

// etiquetas serializa {a="1",b="2"} con las claves ordenadas y los valores escapados:
// el nombre del destino lo escribe el usuario y puede llevar comillas o saltos de línea.
func etiquetas(l map[string]string) string {
	if len(l) == 0 {
		return ""
	}
	claves := make([]string, 0, len(l))
	for k := range l {
		claves = append(claves, k)
	}
	sort.Strings(claves)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range claves {
		if i > 0 {
			sb.WriteByte(',')
		}
		v := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(l[k])
		sb.WriteString(k + `="` + v + `"`)
	}
	sb.WriteByte('}')
	return sb.String()
}

func clonar(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
```

- [ ] **Step 6: Correr en verde**

```bash
go test ./internal/httpapi/ -race -count=1
```
Expected: PASS.

- [ ] **Step 7: Cablear en `main.go`**

En el literal de `httpapi.Config`: `MetricsToken: cfg.MetricsToken,` y

```go
		ExtraMetrics: []httpapi.ExtraMetrics{func() []httpapi.Metric {
			return []httpapi.Metric{{
				Name: "splitstream_events_bus_dropped_total", Type: "counter",
				Help: "Eventos que un consumidor lento no llegó a recibir.",
				Value: float64(bus.Dropped()),
			}}
		}},
```

Quitar el `_ = bus` de la Task 2.

- [ ] **Step 8: Verificar a mano y commitear**

```bash
go build -o /tmp/ss ./cmd/splitstream && SPLITSTREAM_DB_PATH=/tmp/ss-metrics.db SPLITSTREAM_METRICS_TOKEN=t /tmp/ss & sleep 2
curl -s -H 'Authorization: Bearer t' http://127.0.0.1:8080/metrics | head -20; kill %1
```
Expected: `# HELP splitstream_build_info …` y `splitstream_session_live 0`.

```bash
git add internal/config internal/httpapi/metrics.go internal/httpapi/metrics_test.go internal/httpapi/server.go cmd/splitstream/main.go
git commit -m "feat(api): GET /metrics en formato Prometheus"
```

---

### Task 9: Retención y planificador diario

**Files:**
- Create: `internal/store/retention.go`, `internal/store/retention_test.go`
- Create: `internal/maintenance/scheduler.go`, `internal/maintenance/scheduler_test.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go` (`RetentionDays`, `RetentionMaxEvents`)
- Modify: `cmd/splitstream/main.go` (cablear)

**Interfaces:**
- Produces: `store.(*DB).PruneEvents(ctx, olderThan time.Time, keepAtMost int) (int64, error)`; `store.(*DB).PruneSessions(ctx, olderThan time.Time) (int64, error)`.
- Produces: `maintenance.Job{Name string; Run func(ctx) (string, error)}`; `maintenance.Scheduler{Jobs, Busy func() bool, Hour int, InitialDelay, RetryDelay time.Duration, OnDone func(resumen string, err error), Logger, Now}`; `(*Scheduler).RunOnce(ctx) (string, error)`; `(*Scheduler).Run(ctx)`; `maintenance.NextRun(now time.Time, hour int) time.Time`; `maintenance.ErrBusy`.

- [ ] **Step 1: Tests de la poda (rojo)**

Crear `internal/store/retention_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

const anchoFijo = "2006-01-02T15:04:05.000000000Z07:00"

// insertarEventoCon mete un evento con un created_at concreto, saltándose LogEvent (que
// siempre pone "ahora").
func insertarEventoCon(t *testing.T, db *store.DB, cuando time.Time, kind string) {
	t.Helper()
	if _, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO events (level, kind, message, created_at) VALUES ('info', ?, 'x', ?)`,
		kind, cuando.UTC().Format(anchoFijo)); err != nil {
		t.Fatal(err)
	}
}

func contarEventos(t *testing.T, db *store.DB) int {
	t.Helper()
	var n int
	if err := db.SQL().QueryRowContext(context.Background(), `SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPruneEventsByAge(t *testing.T) {
	db := openTemp(t)
	ahora := time.Now()
	insertarEventoCon(t, db, ahora.Add(-100*24*time.Hour), "viejo")
	insertarEventoCon(t, db, ahora.Add(-1*time.Hour), "reciente")

	n, err := db.PruneEvents(context.Background(), ahora.Add(-90*24*time.Hour), 0)
	if err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	if n != 1 || contarEventos(t, db) != 1 {
		t.Errorf("borrados = %d, quedan %d; quería 1 y 1", n, contarEventos(t, db))
	}
}

// El tope de filas manda aunque los eventos sean recientes: es lo que protege el disco de
// un destino que aletea toda la noche.
func TestPruneEventsByCount(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelInfo, Kind: "k", Message: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.PruneEvents(ctx, time.Time{}, 4)
	if err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	if n != 6 || contarEventos(t, db) != 4 {
		t.Errorf("borrados = %d, quedan %d; quería 6 y 4", n, contarEventos(t, db))
	}
	// Se conservan los MÁS RECIENTES.
	ev, _ := db.RecentEvents(ctx, 10)
	if ev[len(ev)-1].ID != 7 {
		t.Errorf("el más antiguo que queda es %d, quería 7", ev[len(ev)-1].ID)
	}
}

// Ambos límites a la vez: muerde el que borre más.
func TestPruneEventsAppliesBothLimits(t *testing.T) {
	db := openTemp(t)
	ahora := time.Now()
	for i := 0; i < 5; i++ {
		insertarEventoCon(t, db, ahora.Add(-200*24*time.Hour), "viejo")
	}
	for i := 0; i < 5; i++ {
		insertarEventoCon(t, db, ahora, "nuevo")
	}
	if _, err := db.PruneEvents(context.Background(), ahora.Add(-90*24*time.Hour), 3); err != nil {
		t.Fatal(err)
	}
	if contarEventos(t, db) != 3 {
		t.Errorf("quedan %d, quería 3", contarEventos(t, db))
	}
}

func TestPruneEventsWithZeroLimitsDoesNothing(t *testing.T) {
	db := openTemp(t)
	insertarEventoCon(t, db, time.Now().Add(-1000*24*time.Hour), "viejo")
	n, err := db.PruneEvents(context.Background(), time.Time{}, 0)
	if err != nil || n != 0 || contarEventos(t, db) != 1 {
		t.Errorf("n=%d err=%v quedan=%d", n, err, contarEventos(t, db))
	}
}

// Solo se podan sesiones CERRADAS y sin eventos que sigan apuntándolas: la de ahora mismo
// y las que todavía tienen historia se quedan.
func TestPruneSessionsKeepsOpenAndReferencedOnes(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	vieja := time.Now().Add(-200 * 24 * time.Hour).UTC().Format(anchoFijo)

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.SQL().ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// 1: cerrada y sin eventos → se va. 2: cerrada con evento → se queda. 3: abierta → se queda.
	exec(`INSERT INTO sessions (id, started_at, ended_at) VALUES (1, ?, ?)`, vieja, vieja)
	exec(`INSERT INTO sessions (id, started_at, ended_at) VALUES (2, ?, ?)`, vieja, vieja)
	exec(`INSERT INTO events (session_id, level, kind, message, created_at) VALUES (2, 'info', 'k', 'x', ?)`, vieja)
	exec(`INSERT INTO sessions (id, started_at) VALUES (3, ?)`, vieja)

	n, err := db.PruneSessions(ctx, time.Now().Add(-90*24*time.Hour))
	if err != nil {
		t.Fatalf("PruneSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("borradas = %d, quería 1", n)
	}
	for _, id := range []int64{2, 3} {
		if _, err := db.SessionByID(ctx, id); err != nil {
			t.Errorf("la sesión %d desapareció: %v", id, err)
		}
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/store/ -run 'Prune' -count=1
```
Expected: FAIL, `db.PruneEvents undefined`.

- [ ] **Step 3: Implementar la poda**

Crear `internal/store/retention.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"
)

// PruneEvents borra eventos más viejos que olderThan (si no es cero) y deja como mucho
// keepAtMost filas (si es > 0), conservando las más recientes. Devuelve cuántas borró.
//
// La comparación por fecha es sobre texto y funciona porque desde la migración 0002 todos
// los timestamps tienen ancho fijo en UTC (spec base §15.4).
func (d *DB) PruneEvents(ctx context.Context, olderThan time.Time, keepAtMost int) (int64, error) {
	var total int64
	if !olderThan.IsZero() {
		res, err := d.ex.ExecContext(ctx, `DELETE FROM events WHERE created_at < ?`, formatTime(olderThan))
		if err != nil {
			return total, fmt.Errorf("podar eventos por fecha: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if keepAtMost > 0 {
		res, err := d.ex.ExecContext(ctx,
			`DELETE FROM events WHERE id NOT IN (SELECT id FROM events ORDER BY id DESC LIMIT ?)`, keepAtMost)
		if err != nil {
			return total, fmt.Errorf("podar eventos por cantidad: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// PruneSessions borra sesiones cerradas que empezaron antes de olderThan y a las que ya
// no apunta ningún evento. Una sesión abierta nunca se toca, por vieja que parezca: puede
// ser la de ahora mismo tras un reloj mal puesto.
func (d *DB) PruneSessions(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := d.ex.ExecContext(ctx,
		`DELETE FROM sessions
		  WHERE ended_at IS NOT NULL
		    AND started_at < ?
		    AND id NOT IN (SELECT session_id FROM events WHERE session_id IS NOT NULL)`,
		formatTime(olderThan))
	if err != nil {
		return 0, fmt.Errorf("podar sesiones: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
```

- [ ] **Step 4: Correr en verde y commitear**

```bash
go test ./internal/store/ -race -count=1
git add internal/store/retention.go internal/store/retention_test.go
git commit -m "feat(store): poda de eventos y sesiones"
```

- [ ] **Step 5: Tests del planificador (rojo)**

Crear `internal/maintenance/scheduler_test.go`:

```go
package maintenance_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/maintenance"
)

func TestRunOnceRunsEveryJobAndSummarises(t *testing.T) {
	var orden []string
	s := &maintenance.Scheduler{
		Jobs: []maintenance.Job{
			{Name: "a", Run: func(context.Context) (string, error) { orden = append(orden, "a"); return "a: 3 filas", nil }},
			{Name: "b", Run: func(context.Context) (string, error) { orden = append(orden, "b"); return "b: 0 filas", nil }},
		},
	}
	resumen, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(orden) != 2 || orden[0] != "a" {
		t.Errorf("orden = %v", orden)
	}
	if !strings.Contains(resumen, "a: 3 filas") || !strings.Contains(resumen, "b: 0 filas") {
		t.Errorf("resumen = %q", resumen)
	}
}

// Un job que falla no impide a los demás, y el error se devuelve.
func TestRunOnceContinuesAfterAFailingJob(t *testing.T) {
	corrido := false
	s := &maintenance.Scheduler{Jobs: []maintenance.Job{
		{Name: "rompe", Run: func(context.Context) (string, error) { return "", errors.New("disco") }},
		{Name: "sigue", Run: func(context.Context) (string, error) { corrido = true; return "ok", nil }},
	}}
	if _, err := s.RunOnce(context.Background()); err == nil {
		t.Error("quería el error del job")
	}
	if !corrido {
		t.Error("el segundo job no corrió")
	}
}

// Nunca se poda con una transmisión en curso: el DELETE compite con los sinks por la
// única conexión a la base.
func TestRunOnceRefusesWhileBusy(t *testing.T) {
	corrido := false
	s := &maintenance.Scheduler{
		Busy: func() bool { return true },
		Jobs: []maintenance.Job{{Name: "x", Run: func(context.Context) (string, error) { corrido = true; return "", nil }}},
	}
	if _, err := s.RunOnce(context.Background()); !errors.Is(err, maintenance.ErrBusy) {
		t.Errorf("err = %v, quería ErrBusy", err)
	}
	if corrido {
		t.Error("corrió un job con el servicio ocupado")
	}
}

func TestNextRunIsTheNextOccurrenceOfTheHour(t *testing.T) {
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.Local)
	if got := maintenance.NextRun(base, 4); !got.Equal(time.Date(2026, 9, 10, 4, 0, 0, 0, time.Local)) {
		t.Errorf("a las 10 → %v, quería mañana a las 4", got)
	}
	antes := time.Date(2026, 9, 9, 3, 0, 0, 0, time.Local)
	if got := maintenance.NextRun(antes, 4); !got.Equal(time.Date(2026, 9, 9, 4, 0, 0, 0, time.Local)) {
		t.Errorf("a las 3 → %v, quería hoy a las 4", got)
	}
}

// Run hace una pasada inicial y avisa por OnDone; el contexto la para.
func TestRunDoesAnInitialPass(t *testing.T) {
	hecho := make(chan string, 1)
	s := &maintenance.Scheduler{
		InitialDelay: 10 * time.Millisecond,
		Jobs:         []maintenance.Job{{Name: "x", Run: func(context.Context) (string, error) { return "x: ok", nil }}},
		OnDone:       func(resumen string, err error) { hecho <- resumen },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	select {
	case r := <-hecho:
		if !strings.Contains(r, "x: ok") {
			t.Errorf("resumen = %q", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no hubo pasada inicial")
	}
}
```

- [ ] **Step 6: Correr en rojo**

```bash
go test ./internal/maintenance/ -count=1
```
Expected: FAIL, paquete inexistente.

- [ ] **Step 7: Implementar el planificador**

Crear `internal/maintenance/scheduler.go`:

```go
// Package maintenance ejecuta tareas periódicas de limpieza —hoy la poda de eventos y
// sesiones; en la v0.9, la de grabaciones— sin pisar una transmisión en curso.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ErrBusy: había una sesión viva y no se corrió nada. Se reintenta en el siguiente tick.
var ErrBusy = errors.New("mantenimiento pospuesto: hay una transmisión en curso")

// Job es una tarea. Run devuelve un resumen legible ("eventos: 120 borrados").
type Job struct {
	Name string
	Run  func(ctx context.Context) (string, error)
}

// Scheduler corre los jobs una vez al arrancar y después cada día a la hora Hour.
type Scheduler struct {
	Jobs []Job
	// Busy dice si hay una transmisión en curso. Con true no se corre nada: un DELETE
	// grande compite con los sinks por la única conexión a la base.
	Busy func() bool
	// Hour es la hora local de la pasada diaria. 4 por defecto: madrugada.
	Hour int
	// InitialDelay es la espera antes de la primera pasada. Un minuto por defecto, para
	// no competir con el arranque.
	InitialDelay time.Duration
	// RetryDelay es cuánto se espera si Busy devolvió true. Diez minutos por defecto.
	RetryDelay time.Duration
	// OnDone recibe el resumen de cada pasada, o su error. Es donde main deja el evento.
	OnDone func(resumen string, err error)
	Logger *slog.Logger
	Now    func() time.Time
}

// NextRun devuelve la próxima ocurrencia de la hora `hour` después de `now`.
func NextRun(now time.Time, hour int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// RunOnce ejecuta todos los jobs en orden. Un job que falla no detiene a los demás; el
// primer error se devuelve al final junto al resumen de los que sí corrieron.
func (s *Scheduler) RunOnce(ctx context.Context) (string, error) {
	if s.Busy != nil && s.Busy() {
		return "", ErrBusy
	}
	var partes []string
	var primero error
	for _, j := range s.Jobs {
		r, err := j.Run(ctx)
		if err != nil {
			partes = append(partes, fmt.Sprintf("%s: error (%v)", j.Name, err))
			if primero == nil {
				primero = fmt.Errorf("%s: %w", j.Name, err)
			}
			continue
		}
		partes = append(partes, r)
	}
	return strings.Join(partes, "; "), primero
}

// Run bloquea hasta que el contexto termine.
func (s *Scheduler) Run(ctx context.Context) {
	log := s.Logger
	if log == nil {
		log = slog.Default()
	}
	now := s.Now
	if now == nil {
		now = time.Now
	}
	hour := s.Hour
	if hour == 0 {
		hour = 4
	}
	initial := s.InitialDelay
	if initial == 0 {
		initial = time.Minute
	}
	retry := s.RetryDelay
	if retry == 0 {
		retry = 10 * time.Minute
	}

	espera := initial
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(espera):
		}

		resumen, err := s.RunOnce(ctx)
		if errors.Is(err, ErrBusy) {
			log.Info("mantenimiento pospuesto: hay una transmisión en curso", "reintento", retry)
			espera = retry
			continue
		}
		if err != nil {
			log.Error("mantenimiento con errores", "err", err, "resumen", resumen)
		} else {
			log.Info("mantenimiento hecho", "resumen", resumen)
		}
		if s.OnDone != nil {
			s.OnDone(resumen, err)
		}
		espera = time.Until(NextRun(now(), hour))
	}
}
```

- [ ] **Step 8: Correr en verde y commitear**

```bash
go test ./internal/maintenance/ -race -count=1
git add internal/maintenance
git commit -m "feat(maintenance): planificador diario que respeta la sesión en curso"
```

- [ ] **Step 9: Config y cableado**

En `internal/config/config_test.go`:

```go
func TestRetentionDefaultsAndOverrides(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{"SPLITSTREAM_MASTER_KEY": testKeyB64()}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RetentionDays != 90 || cfg.RetentionMaxEvents != 50000 {
		t.Errorf("defaults = %d días, %d eventos", cfg.RetentionDays, cfg.RetentionMaxEvents)
	}

	cfg, err = config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(), "SPLITSTREAM_RETENTION_DAYS": "0",
		"SPLITSTREAM_RETENTION_MAX_EVENTS": "1000",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RetentionDays != 0 || cfg.RetentionMaxEvents != 1000 {
		t.Errorf("override = %d días, %d eventos", cfg.RetentionDays, cfg.RetentionMaxEvents)
	}

	if _, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(), "SPLITSTREAM_RETENTION_DAYS": "muchos",
	})); err == nil {
		t.Error("un valor no numérico debería ser error")
	}
}
```

En `internal/config/config.go`: campos `RetentionDays int` y `RetentionMaxEvents int` en `Config` (con `slog.Int` en `LogValue`); en `LoadFrom`, tras el nivel de log:

```go
	if cfg.RetentionDays, err = parseNonNegative(get("SPLITSTREAM_RETENTION_DAYS", "90"), "SPLITSTREAM_RETENTION_DAYS"); err != nil {
		return nil, err
	}
	if cfg.RetentionMaxEvents, err = parseNonNegative(get("SPLITSTREAM_RETENTION_MAX_EVENTS", "50000"), "SPLITSTREAM_RETENTION_MAX_EVENTS"); err != nil {
		return nil, err
	}
```

y al final del archivo (añadir `"strconv"` a los imports):

```go
func parseNonNegative(s, name string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s inválido %q: usa un entero mayor o igual que 0", name, s)
	}
	return n, nil
}
```

En `cmd/splitstream/main.go`, tras construir `engine` y antes de la API, importar `internal/maintenance` y:

```go
	// Mantenimiento diario: poda de eventos y sesiones. Nunca con sesión viva.
	mant := &maintenance.Scheduler{
		Logger: logger,
		Busy:   func() bool { return engine.SessionID() != 0 },
		Jobs: []maintenance.Job{
			{Name: "eventos", Run: func(ctx context.Context) (string, error) {
				var corte time.Time
				if cfg.RetentionDays > 0 {
					corte = time.Now().Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour)
				}
				n, err := db.PruneEvents(ctx, corte, cfg.RetentionMaxEvents)
				return fmt.Sprintf("eventos: %d borrados", n), err
			}},
			{Name: "sesiones", Run: func(ctx context.Context) (string, error) {
				if cfg.RetentionDays == 0 {
					return "sesiones: retención desactivada", nil
				}
				n, err := db.PruneSessions(ctx, time.Now().Add(-time.Duration(cfg.RetentionDays)*24*time.Hour))
				return fmt.Sprintf("sesiones: %d borradas", n), err
			}},
		},
		OnDone: func(resumen string, err error) {
			level := store.LevelInfo
			if err != nil {
				level = store.LevelWarn
			}
			if _, e := db.LogEvent(context.Background(), store.Event{
				Level: level, Kind: "maintenance_ran", Message: "mantenimiento: " + resumen,
			}); e != nil {
				logger.Error("no se pudo registrar el mantenimiento", "err", e)
			}
		},
	}
	go mant.Run(sinkCtx)
```

- [ ] **Step 10: Comprobar y commitear**

```bash
go test ./internal/config/ ./cmd/splitstream/ -race -count=1 && go vet ./...
git add internal/config cmd/splitstream/main.go
git commit -m "feat: retención configurable con pasada diaria"
```

---

### Task 10: Sesiones, respaldo y `-backup`

**Files:**
- Create: `internal/store/sessions.go`, `internal/store/sessions_test.go`
- Create: `internal/store/backup.go`, `internal/store/backup_test.go`
- Create: `internal/httpapi/sessions.go`, `internal/httpapi/sessions_test.go`
- Create: `internal/httpapi/backup.go`, `internal/httpapi/backup_test.go`
- Modify: `internal/httpapi/server.go` (rutas), `internal/httpapi/dto.go` (`sessionSummaryDTO`)
- Modify: `cmd/splitstream/main.go`, `cmd/splitstream/main_test.go` (`-backup`)

**Interfaces:**
- Produces: `store.SessionSummary{Session; Info, Warn, Error int}`; `store.(*DB).ListSessions(ctx, limit int, before int64) ([]SessionSummary, error)`; `store.(*DB).BackupTo(ctx, path string) error`.
- Produces: `GET /api/sessions?limit=&before=` → `[{id, started_at, ended_at, width, height, bitrate_bps, events:{info,warn,error}}]`; `POST /api/backup` → archivo `.db`; evento `backup_downloaded` (warn).

- [ ] **Step 1: Tests del store (rojo)**

Crear `internal/store/sessions_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestListSessionsNewestFirstWithEventCounts(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := db.StartSession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := db.FinishSession(ctx, ids[0], 1280, 720, 3000); err != nil {
		t.Fatal(err)
	}
	for _, lvl := range []store.Level{store.LevelInfo, store.LevelWarn, store.LevelWarn, store.LevelError} {
		if _, err := db.LogEvent(ctx, store.Event{SessionID: &ids[2], Level: lvl, Kind: "k", Message: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.ListSessions(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 3 || got[0].ID != ids[2] || got[2].ID != ids[0] {
		t.Fatalf("orden = %v", got)
	}
	if got[0].Info != 1 || got[0].Warn != 2 || got[0].Error != 1 {
		t.Errorf("contadores = %d/%d/%d", got[0].Info, got[0].Warn, got[0].Error)
	}
	if got[2].EndedAt == nil || got[2].Width == nil || *got[2].Width != 1280 {
		t.Errorf("la sesión cerrada no trae sus datos: %+v", got[2])
	}
	if got[0].EndedAt != nil {
		t.Error("la sesión abierta trae ended_at")
	}
}

// La paginación es por id, no por texto de fecha (spec base §15.4).
func TestListSessionsPaginatesByID(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := db.StartSession(ctx); err != nil {
			t.Fatal(err)
		}
	}
	pag1, _ := db.ListSessions(ctx, 2, 0)
	if len(pag1) != 2 || pag1[0].ID != 5 {
		t.Fatalf("página 1 = %v", pag1)
	}
	pag2, _ := db.ListSessions(ctx, 2, pag1[1].ID)
	if len(pag2) != 2 || pag2[0].ID != 3 {
		t.Fatalf("página 2 = %v", pag2)
	}
}
```

Crear `internal/store/backup_test.go` (usa `openTemp` y un cipher propio para no depender de otros helpers):

```go
package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

func cipherDePrueba(t *testing.T) *crypto.Cipher {
	t.Helper()
	var k [32]byte
	for i := range k {
		k[i] = byte(i)
	}
	c, err := crypto.NewCipher(k)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// El respaldo tiene que abrir con store.Open y pasar Bootstrap con la MISMA clave: es
// exactamente lo que hará quien lo restaure.
func TestBackupToProducesAnOpenableConsistentCopy(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	c := cipherDePrueba(t)
	if err := db.Bootstrap(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateDestination(ctx, c, store.NewDestination{
		Name: "YouTube", Platform: store.PlatformYouTube, RTMPURL: "rtmp://a/b",
		Key: crypto.Secret("k"), Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	copia := filepath.Join(t.TempDir(), "respaldo.db")
	if err := db.BackupTo(ctx, copia); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	db2, err := store.Open(ctx, copia)
	if err != nil {
		t.Fatalf("abrir el respaldo: %v", err)
	}
	defer db2.Close()
	if err := db2.Bootstrap(ctx, c); err != nil {
		t.Fatalf("el respaldo no pasa Bootstrap con la misma clave: %v", err)
	}
	dests, err := db2.ListDestinations(ctx)
	if err != nil || len(dests) != 1 {
		t.Errorf("destinos en el respaldo = %d (%v), quería 1", len(dests), err)
	}
}

func TestBackupToRefusesToRunInsideATransaction(t *testing.T) {
	db := openTemp(t)
	err := db.InTx(context.Background(), func(tx *store.DB) error {
		return tx.BackupTo(context.Background(), filepath.Join(t.TempDir(), "x.db"))
	})
	if err == nil {
		t.Error("VACUUM INTO dentro de una transacción debería fallar")
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/store/ -run 'ListSessions|BackupTo' -count=1
```
Expected: FAIL, `db.ListSessions undefined`.

- [ ] **Step 3: Implementar**

Crear `internal/store/sessions.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"
)

// SessionSummary es una sesión con sus contadores de eventos por nivel, para el listado
// del historial.
type SessionSummary struct {
	Session
	Info  int
	Warn  int
	Error int
}

const (
	defaultSessionLimit = 50
	maxSessionLimit     = 500
)

// ListSessions devuelve sesiones de la más reciente a la más antigua. before pagina por
// id (0 = desde la última): nunca por texto de fecha (spec base §15.4).
func (d *DB) ListSessions(ctx context.Context, limit int, before int64) ([]SessionSummary, error) {
	if limit <= 0 {
		limit = defaultSessionLimit
	}
	if limit > maxSessionLimit {
		limit = maxSessionLimit
	}
	rows, err := d.ex.QueryContext(ctx,
		`SELECT s.id, s.started_at, s.ended_at, s.width, s.height, s.bitrate_bps,
		        (SELECT count(*) FROM events e WHERE e.session_id = s.id AND e.level = 'info'),
		        (SELECT count(*) FROM events e WHERE e.session_id = s.id AND e.level = 'warn'),
		        (SELECT count(*) FROM events e WHERE e.session_id = s.id AND e.level = 'error')
		   FROM sessions s
		  WHERE (? = 0 OR s.id < ?)
		  ORDER BY s.id DESC
		  LIMIT ?`, before, before, limit)
	if err != nil {
		return nil, fmt.Errorf("listar sesiones: %w", err)
	}
	defer rows.Close()

	out := []SessionSummary{}
	for rows.Next() {
		var (
			s         SessionSummary
			startedAt string
			endedAt   *string
		)
		if err := rows.Scan(&s.ID, &startedAt, &endedAt, &s.Width, &s.Height, &s.BitrateBPS,
			&s.Info, &s.Warn, &s.Error); err != nil {
			return nil, fmt.Errorf("listar sesiones: %w", err)
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
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar sesiones: %w", err)
	}
	return out, nil
}
```

Crear `internal/store/backup.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// BackupTo escribe una copia consistente de la base en path.
//
// Usa VACUUM INTO y no una copia del archivo: en modo WAL, parte de los datos puede
// estar todavía en el -wal, y copiar solo el .db da un archivo que abre pero al que le
// faltan las últimas escrituras. Escribe a un temporal y renombra, para que un fallo a
// medias no deje un respaldo truncado con nombre de bueno.
func (d *DB) BackupTo(ctx context.Context, path string) error {
	if _, ok := d.ex.(*sql.Tx); ok {
		return errors.New("respaldar: no se puede dentro de una transacción")
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if _, err := d.ex.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("respaldar: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("respaldar: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Correr en verde y commitear**

```bash
go test ./internal/store/ -race -count=1
git add internal/store/sessions.go internal/store/sessions_test.go internal/store/backup.go internal/store/backup_test.go
git commit -m "feat(store): listado de sesiones y respaldo con VACUUM INTO"
```

- [ ] **Step 5: Tests de la API (rojo)**

Crear `internal/httpapi/sessions_test.go`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestSessionsListNewestFirst(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	ctx := context.Background()
	var ultimo int64
	for i := 0; i < 3; i++ {
		ultimo, _ = db.StartSession(ctx)
	}
	if _, err := db.LogEvent(ctx, store.Event{SessionID: &ultimo, Level: store.LevelError, Kind: "k", Message: "x"}); err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, cookies, http.MethodGet, "/api/sessions?limit=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	var got []sessionSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != ultimo || got[0].Events.Error != 1 {
		t.Errorf("got = %+v", got)
	}

	rec = do(t, srv, cookies, http.MethodGet, "/api/sessions?before="+itoa(got[1].ID), "")
	var resto []sessionSummaryDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &resto)
	if len(resto) != 1 {
		t.Errorf("página 2 = %d sesiones, quería 1", len(resto))
	}
}

func TestSessionsRejectsNonNumericParams(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	if rec := do(t, srv, cookies, http.MethodGet, "/api/sessions?before=ayer", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("código = %d, quería 400", rec.Code)
	}
}
```

Crear `internal/httpapi/backup_test.go`:

```go
package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestBackupDownloadsAnOpenableDatabaseAndAudits(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "yt", "k", true)

	rec := do(t, srv, cookies, http.MethodPost, "/api/backup", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="splitstream-`) || !strings.HasSuffix(cd, `.db"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}

	ruta := filepath.Join(t.TempDir(), "descargado.db")
	if err := os.WriteFile(ruta, rec.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db2, err := store.Open(ctx, ruta)
	if err != nil {
		t.Fatalf("el archivo descargado no abre: %v", err)
	}
	defer db2.Close()
	if err := db2.Bootstrap(ctx, srv.cipher); err != nil {
		t.Errorf("no pasa Bootstrap con la misma clave: %v", err)
	}
	dests, _ := db2.ListDestinations(ctx)
	if len(dests) != 1 {
		t.Errorf("destinos = %d, quería 1", len(dests))
	}

	eventos, _ := db.RecentEvents(ctx, 5)
	var visto bool
	for _, e := range eventos {
		if e.Kind == "backup_downloaded" && e.Level == store.LevelWarn {
			visto = true
		}
	}
	if !visto {
		t.Error("descargar el respaldo no dejó evento")
	}
}

func TestBackupRequiresASession(t *testing.T) {
	srv, _, _, _, _ := newDestServer(t)
	if rec := do(t, srv, nil, http.MethodPost, "/api/backup", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("código = %d, quería 401", rec.Code)
	}
}
```

- [ ] **Step 6: Correr en rojo**

```bash
go test ./internal/httpapi/ -run 'Sessions|Backup' -count=1
```
Expected: FAIL, `undefined: sessionSummaryDTO`.

- [ ] **Step 7: Implementar**

En `internal/httpapi/dto.go`, al final:

```go
// sessionSummaryDTO es una fila del historial: la sesión y cuántos eventos dejó por nivel.
type sessionSummaryDTO struct {
	ID         int64      `json:"id"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at"`
	Width      *int       `json:"width"`
	Height     *int       `json:"height"`
	BitrateBPS *int       `json:"bitrate_bps"`
	Events     struct {
		Info  int `json:"info"`
		Warn  int `json:"warn"`
		Error int `json:"error"`
	} `json:"events"`
}

func newSessionSummaryDTO(s store.SessionSummary) sessionSummaryDTO {
	dto := sessionSummaryDTO{
		ID: s.ID, StartedAt: s.StartedAt.UTC(),
		Width: s.Width, Height: s.Height, BitrateBPS: s.BitrateBPS,
	}
	if s.EndedAt != nil {
		e := s.EndedAt.UTC()
		dto.EndedAt = &e
	}
	dto.Events.Info, dto.Events.Warn, dto.Events.Error = s.Info, s.Warn, s.Error
	return dto
}
```

Crear `internal/httpapi/sessions.go`:

```go
package httpapi

import (
	"net/http"
	"strconv"
)

// handleSessions lista el historial de sesiones. Pagina por id (`before`), nunca por
// fecha.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryInt(w, r, "limit")
	if !ok {
		return
	}
	before, ok := queryInt(w, r, "before")
	if !ok {
		return
	}
	sesiones, err := s.db.ListSessions(r.Context(), limit, int64(before))
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	out := make([]sessionSummaryDTO, 0, len(sesiones))
	for _, ses := range sesiones {
		out = append(out, newSessionSummaryDTO(ses))
	}
	writeJSON(w, http.StatusOK, out)
}

// queryInt lee un parámetro numérico opcional. Ausente vale 0; mal formado es 400.
func queryInt(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidInput, name+" debe ser un número")
		return 0, false
	}
	return n, true
}
```

Crear `internal/httpapi/backup.go`:

```go
package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

// handleBackup descarga una copia consistente de la base (spec v0.8 §7).
//
// Es POST y no GET a propósito: produce un archivo con todas las claves —cifradas, pero
// todas— y deja un evento. Un GET lo prefetchearía cualquier extensión del navegador.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	dir, err := os.MkdirTemp("", "splitstream-backup-")
	if err != nil {
		s.logger.Error("no se pudo crear el temporal del respaldo", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	defer os.RemoveAll(dir)

	nombre := "splitstream-" + time.Now().UTC().Format("20060102-150405") + ".db"
	ruta := filepath.Join(dir, nombre)
	if err := s.db.BackupTo(r.Context(), ruta); err != nil {
		s.logger.Error("no se pudo generar el respaldo", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "no se pudo generar el respaldo")
		return
	}

	// warn y no info: es un archivo con todas las claves. Si alguien entra en tu panel,
	// quieres poder ver que se lo llevó.
	if _, err := s.db.LogEvent(r.Context(), store.Event{
		Level: store.LevelWarn, Kind: "backup_downloaded",
		Message: "se descargó un respaldo de la base de datos",
	}); err != nil {
		s.logger.Error("no se pudo registrar la descarga del respaldo", "err", err)
	}

	f, err := os.Open(ruta)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	defer f.Close()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+nombre+`"`)
	// Recordatorio para quien mire las cabeceras: sin la clave, esto es ilegible.
	w.Header().Set("X-Splitstream-Note", "sin splitstream.key (o SPLITSTREAM_MASTER_KEY) este archivo no sirve")
	http.ServeContent(w, r, nombre, info.ModTime(), f)
}
```

En `routes()`: `protegida("GET /api/sessions", s.handleSessions)` y `protegida("POST /api/backup", s.handleBackup)`.

- [ ] **Step 8: Correr en verde y commitear**

```bash
go test ./internal/httpapi/ -race -count=1
git add internal/httpapi/sessions.go internal/httpapi/sessions_test.go internal/httpapi/backup.go internal/httpapi/backup_test.go internal/httpapi/dto.go internal/httpapi/server.go
git commit -m "feat(api): historial de sesiones y descarga de respaldo"
```

- [ ] **Step 9: El flag `-backup`**

En `cmd/splitstream/main_test.go` (usa `setPasswordEnv`, que ya deja `SPLITSTREAM_DB_PATH` y la clave en el entorno; adaptar la primera línea si su firma difiere):

```go
func TestBackupFlagWritesAnOpenableCopy(t *testing.T) {
	_ = setPasswordEnv(t)
	ctx := context.Background()

	destino := filepath.Join(t.TempDir(), "copia.db")
	var out bytes.Buffer
	if err := backup(ctx, destino, &out); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !strings.Contains(out.String(), destino) {
		t.Errorf("la salida no dice dónde quedó el respaldo: %q", out.String())
	}
	db, err := store.Open(ctx, destino)
	if err != nil {
		t.Fatalf("el respaldo no abre: %v", err)
	}
	db.Close()
}
```

En `main.go`: flag `bk := flag.String("backup", "", "escribe una copia consistente de la base en la ruta dada y sale")`, y tras el bloque de `setpw`:

```go
	if *bk != "" {
		if err := backup(context.Background(), *bk, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
```

y al final del archivo:

```go
// backup copia la base con VACUUM INTO. Abre la base como el servicio —con sus
// migraciones— para que el respaldo esté en la versión actual del esquema.
func backup(ctx context.Context, destino string, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.BackupTo(ctx, destino); err != nil {
		return err
	}
	fmt.Fprintf(out, "respaldo escrito en %s\nRecuerda: sin la clave maestra es ilegible.\n", destino)
	return nil
}
```

```bash
go test ./cmd/splitstream/ -race -count=1
git add cmd/splitstream
git commit -m "feat: splitstream -backup <ruta>"
```

---

### Task 11: Webhooks: migración, store y despachador

**Files:**
- Create: `internal/store/migrations/0005_webhooks.sql`; Modify: `internal/store/db.go` (`SchemaVersion = 5`), `internal/store/db_test.go` (la lista de tablas gana `webhooks`)
- Create: `internal/store/webhooks.go`, `internal/store/webhooks_test.go`
- Create: `internal/alerts/payload.go`, `internal/alerts/payload_test.go`, `internal/alerts/webhooks.go`, `internal/alerts/webhooks_test.go`

**Interfaces:**
- Produces (store): `WebhookFormat` (`json|discord|slack`); `Webhook{ID, Name, URL, Format, HasSecret bool, MinLevel Level, Enabled bool, LastStatus *int, LastError string, CreatedAt, UpdatedAt}`; `NewWebhook{Name, URL, Format, Secret crypto.Secret, MinLevel, Enabled}`; `WebhookPatch{Name, URL *string; Format *WebhookFormat; Secret *crypto.Secret; MinLevel *Level; Enabled *bool}`; `ListWebhooks`, `CreateWebhook(ctx, c, in)`, `UpdateWebhook(ctx, c, id, patch)`, `DeleteWebhook`, `WebhookSecret(ctx, c, id) (crypto.Secret, error)`, `RecordWebhookDelivery(ctx, id, status int, errMsg string) error`; `ErrWebhookNotFound`.
- Produces (alerts): `Payload(format store.WebhookFormat, ev store.Event, dest *store.Destination) ([]byte, error)`; `Sign(secret crypto.Secret, body []byte) string`; `NewWebhookDispatcher(bus *events.Bus, db *store.DB, c *crypto.Cipher, log *slog.Logger, version string) *WebhookDispatcher`; `(*WebhookDispatcher).Run(ctx)`; `(*WebhookDispatcher).Send(ctx, w store.Webhook, ev store.Event) error`; `(*WebhookDispatcher).Stats() (ok, failed uint64)`.

- [ ] **Step 1: La migración**

Crear `internal/store/migrations/0005_webhooks.sql`:

```sql
-- Webhooks salientes (spec v0.8 §5.2). El secreto va cifrado con la clave maestra, como
-- las claves de destino, y NULL cuando el formato no firma (discord, slack: la URL ya
-- lleva su token). last_status y last_error son para que el panel enseñe si el aviso
-- llega, sin tener que consultar el log.
-- IF NOT EXISTS por la misma razón que 0004: los tests rebobinan user_version.
CREATE TABLE IF NOT EXISTS webhooks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT    NOT NULL,
    url              TEXT    NOT NULL,
    format           TEXT    NOT NULL CHECK (format IN ('json', 'discord', 'slack')),
    secret_encrypted BLOB,
    min_level        TEXT    NOT NULL CHECK (min_level IN ('info', 'warn', 'error')),
    enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    last_status      INTEGER,
    last_error       TEXT    NOT NULL DEFAULT '',
    created_at       TEXT    NOT NULL,
    updated_at       TEXT    NOT NULL
);
```

En `internal/store/db.go`: `const SchemaVersion = 5`. En `internal/store/db_test.go`, `TestOpenCreatesSchema`: `want := []string{"destination_logos", "destinations", "events", "sessions", "settings", "webhooks"}`.

```bash
go test ./internal/store/ -race -count=1
```
Expected: PASS.

- [ ] **Step 2: Tests del store (rojo)**

Crear `internal/store/webhooks_test.go`:

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

func nuevoWebhook(nombre string) store.NewWebhook {
	return store.NewWebhook{
		Name: nombre, URL: "https://hooks.example.com/" + nombre, Format: store.WebhookJSON,
		Secret: crypto.Secret("secreto-de-" + nombre), MinLevel: store.LevelWarn, Enabled: true,
	}
}

func TestCreateAndListWebhooksNeverReturnTheSecret(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()

	w, err := db.CreateWebhook(ctx, c, nuevoWebhook("uno"))
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if !w.HasSecret || w.Format != store.WebhookJSON || w.MinLevel != store.LevelWarn || !w.Enabled {
		t.Errorf("webhook = %+v", w)
	}

	lista, err := db.ListWebhooks(ctx)
	if err != nil || len(lista) != 1 {
		t.Fatalf("ListWebhooks = %v, %v", lista, err)
	}
	// Recuperar el secreto exige pedirlo aparte y con el cipher.
	s, err := db.WebhookSecret(ctx, c, w.ID)
	if err != nil || s.Reveal() != "secreto-de-uno" {
		t.Errorf("WebhookSecret = %q, %v", s.Reveal(), err)
	}
}

// La firma no puede viajar en claro por internet: solo https, salvo loopback para probar.
func TestCreateWebhookValidatesTheURL(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()

	for _, url := range []string{"http://hooks.example.com/x", "ftp://x", "", "hooks.example.com"} {
		in := nuevoWebhook("x")
		in.URL = url
		if _, err := db.CreateWebhook(ctx, c, in); !errors.Is(err, store.ErrInvalidInput) {
			t.Errorf("URL %q aceptada (err = %v)", url, err)
		}
	}
	for _, url := range []string{"https://hooks.example.com/x", "http://localhost:9000/x", "http://127.0.0.1/x"} {
		in := nuevoWebhook("x")
		in.URL = url
		if _, err := db.CreateWebhook(ctx, c, in); err != nil {
			t.Errorf("URL %q rechazada: %v", url, err)
		}
	}
}

func TestCreateWebhookValidatesFormatLevelAndName(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()

	in := nuevoWebhook("x")
	in.Format = "telegram"
	if _, err := db.CreateWebhook(ctx, c, in); !errors.Is(err, store.ErrInvalidInput) {
		t.Error("formato desconocido aceptado")
	}
	in = nuevoWebhook("x")
	in.MinLevel = "fatal"
	if _, err := db.CreateWebhook(ctx, c, in); !errors.Is(err, store.ErrInvalidInput) {
		t.Error("nivel desconocido aceptado")
	}
	in = nuevoWebhook("x")
	in.Name = "  "
	if _, err := db.CreateWebhook(ctx, c, in); !errors.Is(err, store.ErrInvalidInput) {
		t.Error("nombre vacío aceptado")
	}
}

// Un secreto en discord o slack no tiene sentido: esas URL llevan su token. Se guarda
// igual —no hace daño— pero el formato decide si se firma. Aquí solo se comprueba que un
// secreto vacío deja HasSecret en false.
func TestWebhookWithoutSecret(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	in := nuevoWebhook("d")
	in.Format, in.Secret = store.WebhookDiscord, ""
	w, err := db.CreateWebhook(context.Background(), c, in)
	if err != nil {
		t.Fatal(err)
	}
	if w.HasSecret {
		t.Error("HasSecret = true sin secreto")
	}
	s, err := db.WebhookSecret(context.Background(), c, w.ID)
	if err != nil || s.Reveal() != "" {
		t.Errorf("WebhookSecret = %q, %v; quería vacío", s.Reveal(), err)
	}
}

func TestUpdateWebhookPatchesOnlyWhatCame(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()
	w, _ := db.CreateWebhook(ctx, c, nuevoWebhook("uno"))

	off := false
	nombre := "renombrado"
	got, err := db.UpdateWebhook(ctx, c, w.ID, store.WebhookPatch{Name: &nombre, Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renombrado" || got.Enabled || got.URL != w.URL || !got.HasSecret {
		t.Errorf("patch mal aplicado: %+v", got)
	}
	// Secreto vacío en el patch = quitarlo.
	vacio := crypto.Secret("")
	got, _ = db.UpdateWebhook(ctx, c, w.ID, store.WebhookPatch{Secret: &vacio})
	if got.HasSecret {
		t.Error("el secreto no se quitó")
	}
}

func TestDeleteWebhookAndNotFound(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()
	w, _ := db.CreateWebhook(ctx, c, nuevoWebhook("uno"))
	if err := db.DeleteWebhook(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteWebhook(ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("segundo borrado = %v, quería ErrNotFound", err)
	}
	if _, err := db.UpdateWebhook(ctx, c, w.ID, store.WebhookPatch{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update de inexistente = %v", err)
	}
}

func TestRecordWebhookDelivery(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()
	w, _ := db.CreateWebhook(ctx, c, nuevoWebhook("uno"))

	if err := db.RecordWebhookDelivery(ctx, w.ID, 502, "bad gateway"); err != nil {
		t.Fatal(err)
	}
	lista, _ := db.ListWebhooks(ctx)
	if lista[0].LastStatus == nil || *lista[0].LastStatus != 502 || lista[0].LastError != "bad gateway" {
		t.Errorf("entrega no registrada: %+v", lista[0])
	}
	if err := db.RecordWebhookDelivery(ctx, w.ID, 200, ""); err != nil {
		t.Fatal(err)
	}
	lista, _ = db.ListWebhooks(ctx)
	if *lista[0].LastStatus != 200 || lista[0].LastError != "" {
		t.Errorf("entrega buena no borró el error: %+v", lista[0])
	}
	// Un error larguísimo se recorta: es para el panel, no un log.
	_ = db.RecordWebhookDelivery(ctx, w.ID, 0, strings.Repeat("x", 1000))
	lista, _ = db.ListWebhooks(ctx)
	if len(lista[0].LastError) > 200 {
		t.Errorf("last_error mide %d", len(lista[0].LastError))
	}
}
```

- [ ] **Step 3: Correr en rojo**

```bash
go test ./internal/store/ -run 'Webhook' -count=1
```
Expected: FAIL, `undefined: store.NewWebhook`.

- [ ] **Step 4: Implementar el store**

Crear `internal/store/webhooks.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
)

// ErrWebhookNotFound se devuelve cuando el id no existe.
var ErrWebhookNotFound = notFound("webhook no encontrado")

// WebhookFormat es el conjunto cerrado de formatos de cuerpo. Duplica el CHECK del esquema
// para que el error llegue antes y legible.
type WebhookFormat string

const (
	WebhookJSON    WebhookFormat = "json"
	WebhookDiscord WebhookFormat = "discord"
	WebhookSlack   WebhookFormat = "slack"
)

func (f WebhookFormat) Valid() bool {
	switch f {
	case WebhookJSON, WebhookDiscord, WebhookSlack:
		return true
	}
	return false
}

// Webhook es un destino de avisos. No tiene campo para el secreto: hay que pedirlo aparte
// con WebhookSecret, de modo que serializar este struct nunca puede filtrarlo.
type Webhook struct {
	ID         int64
	Name       string
	URL        string
	Format     WebhookFormat
	HasSecret  bool
	MinLevel   Level
	Enabled    bool
	LastStatus *int
	LastError  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type NewWebhook struct {
	Name     string
	URL      string
	Format   WebhookFormat
	Secret   crypto.Secret
	MinLevel Level
	Enabled  bool
}

// WebhookPatch es una modificación parcial. Un Secret presente y vacío QUITA el secreto.
type WebhookPatch struct {
	Name     *string
	URL      *string
	Format   *WebhookFormat
	Secret   *crypto.Secret
	MinLevel *Level
	Enabled  *bool
}

// maxLastError acota lo que se guarda del último fallo: es para una línea del panel.
const maxLastError = 200

// validateWebhookURL exige https, salvo loopback para probar en local: la firma HMAC no
// debe viajar en claro por internet.
func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return invalidInput("URL de webhook inválida: falta el servidor")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		h := u.Hostname()
		if h == "localhost" || h == "127.0.0.1" || h == "::1" {
			return nil
		}
		return invalidInput("URL de webhook inválida: usa https:// (http solo vale hacia localhost)")
	default:
		return invalidInput(fmt.Sprintf("URL de webhook inválida: esquema %q, usa https://", u.Scheme))
	}
}

func validateWebhook(name string, format WebhookFormat, level Level) error {
	if strings.TrimSpace(name) == "" {
		return invalidInput("el nombre no puede estar vacío")
	}
	if !format.Valid() {
		return invalidInput(fmt.Sprintf("formato %q no soportado: json, discord o slack", format))
	}
	if !level.Valid() {
		return invalidInput(fmt.Sprintf("nivel %q no soportado: info, warn o error", level))
	}
	return nil
}

func (d *DB) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	rows, err := d.ex.QueryContext(ctx,
		`SELECT id, name, url, format, secret_encrypted IS NOT NULL, min_level, enabled,
		        last_status, last_error, created_at, updated_at
		   FROM webhooks ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listar webhooks: %w", err)
	}
	defer rows.Close()
	out := []Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar webhooks: %w", err)
	}
	return out, nil
}

func (d *DB) CreateWebhook(ctx context.Context, c *crypto.Cipher, in NewWebhook) (*Webhook, error) {
	if err := validateWebhook(in.Name, in.Format, in.MinLevel); err != nil {
		return nil, err
	}
	if err := validateWebhookURL(in.URL); err != nil {
		return nil, err
	}
	secret, err := encryptOptional(c, in.Secret)
	if err != nil {
		return nil, err
	}
	now := nowRFC3339()
	res, err := d.ex.ExecContext(ctx,
		`INSERT INTO webhooks (name, url, format, secret_encrypted, min_level, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Name, in.URL, string(in.Format), secret, string(in.MinLevel), boolToInt(in.Enabled), now, now)
	if err != nil {
		return nil, fmt.Errorf("crear webhook: %w", err)
	}
	id, _ := res.LastInsertId()
	return d.webhook(ctx, id)
}

func (d *DB) UpdateWebhook(ctx context.Context, c *crypto.Cipher, id int64, patch WebhookPatch) (*Webhook, error) {
	actual, err := d.webhook(ctx, id)
	if err != nil {
		return nil, err
	}
	name, format, level := actual.Name, actual.Format, actual.MinLevel
	if patch.Name != nil {
		name = *patch.Name
	}
	if patch.Format != nil {
		format = *patch.Format
	}
	if patch.MinLevel != nil {
		level = *patch.MinLevel
	}
	if err := validateWebhook(name, format, level); err != nil {
		return nil, err
	}
	sets := []string{"name = ?", "format = ?", "min_level = ?"}
	args := []any{name, string(format), string(level)}
	if patch.URL != nil {
		if err := validateWebhookURL(*patch.URL); err != nil {
			return nil, err
		}
		sets = append(sets, "url = ?")
		args = append(args, *patch.URL)
	}
	if patch.Secret != nil {
		secret, err := encryptOptional(c, *patch.Secret)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "secret_encrypted = ?")
		args = append(args, secret)
	}
	if patch.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, boolToInt(*patch.Enabled))
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, nowRFC3339(), id)
	if _, err := d.ex.ExecContext(ctx, "UPDATE webhooks SET "+joinComma(sets)+" WHERE id = ?", args...); err != nil {
		return nil, fmt.Errorf("actualizar webhook: %w", err)
	}
	return d.webhook(ctx, id)
}

func (d *DB) DeleteWebhook(ctx context.Context, id int64) error {
	res, err := d.ex.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("borrar webhook: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrWebhookNotFound
	}
	return nil
}

// WebhookSecret descifra el secreto, o devuelve "" si no hay. No audita: no es una
// divulgación a una persona, lo lee el despachador para firmar.
func (d *DB) WebhookSecret(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error) {
	var blob []byte
	err := d.ex.QueryRowContext(ctx, `SELECT secret_encrypted FROM webhooks WHERE id = ?`, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrWebhookNotFound
	}
	if err != nil {
		return "", fmt.Errorf("leer el secreto del webhook: %w", err)
	}
	if blob == nil {
		return "", nil
	}
	plain, err := c.Decrypt(blob)
	if err != nil {
		return "", fmt.Errorf("descifrar el secreto del webhook: %w", err)
	}
	return crypto.Secret(plain), nil
}

// RecordWebhookDelivery guarda el resultado del último envío. status 0 significa que no
// hubo respuesta HTTP (red, timeout).
func (d *DB) RecordWebhookDelivery(ctx context.Context, id int64, status int, errMsg string) error {
	if len(errMsg) > maxLastError {
		errMsg = errMsg[:maxLastError]
	}
	var st *int
	if status != 0 {
		st = &status
	}
	if _, err := d.ex.ExecContext(ctx,
		`UPDATE webhooks SET last_status = ?, last_error = ? WHERE id = ?`, st, errMsg, id); err != nil {
		return fmt.Errorf("registrar entrega del webhook: %w", err)
	}
	return nil
}

func (d *DB) webhook(ctx context.Context, id int64) (*Webhook, error) {
	row := d.ex.QueryRowContext(ctx,
		`SELECT id, name, url, format, secret_encrypted IS NOT NULL, min_level, enabled,
		        last_status, last_error, created_at, updated_at
		   FROM webhooks WHERE id = ?`, id)
	w, err := scanWebhook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebhookNotFound
	}
	return w, err
}

func scanWebhook(s scanner) (*Webhook, error) {
	var (
		w                    Webhook
		format, level        string
		hasSecret, enabled   int
		createdAt, updatedAt string
	)
	if err := s.Scan(&w.ID, &w.Name, &w.URL, &format, &hasSecret, &level, &enabled,
		&w.LastStatus, &w.LastError, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("leer webhook: %w", err)
	}
	w.Format, w.MinLevel = WebhookFormat(format), Level(level)
	w.HasSecret, w.Enabled = hasSecret == 1, enabled == 1
	var err error
	if w.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return nil, fmt.Errorf("created_at inválido: %w", err)
	}
	if w.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &w, nil
}

// encryptOptional cifra un secreto, o devuelve nil (NULL en la base) si está vacío.
func encryptOptional(c *crypto.Cipher, s crypto.Secret) ([]byte, error) {
	if s.Reveal() == "" {
		return nil, nil
	}
	blob, err := c.Encrypt([]byte(s.Reveal()))
	if err != nil {
		return nil, fmt.Errorf("cifrar el secreto del webhook: %w", err)
	}
	return blob, nil
}
```

- [ ] **Step 5: Correr en verde y commitear**

```bash
go test ./internal/store/ -race -count=1
git add internal/store/migrations/0005_webhooks.sql internal/store/db.go internal/store/db_test.go internal/store/webhooks.go internal/store/webhooks_test.go
git commit -m "feat(store): webhooks con secreto cifrado"
```

- [ ] **Step 6: Tests del payload y del despachador (rojo)**

Crear `internal/alerts/payload_test.go`:

```go
package alerts_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/alerts"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

func evento() store.Event {
	dest, ses := int64(3), int64(7)
	return store.Event{
		ID: 42, SessionID: &ses, DestinationID: &dest, Level: store.LevelError,
		Kind: "destination_suspended", Message: "el destino queda suspendido",
		CreatedAt: time.Date(2026, 9, 9, 20, 15, 3, 0, time.UTC),
	}
}

func TestJSONPayloadShape(t *testing.T) {
	body, err := alerts.Payload(store.WebhookJSON, evento(), &store.Destination{ID: 3, Name: "YouTube", Platform: "youtube"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != float64(42) || got["kind"] != "destination_suspended" || got["level"] != "error" {
		t.Errorf("cuerpo = %s", body)
	}
	if got["session_id"] != float64(7) {
		t.Errorf("session_id = %v", got["session_id"])
	}
	d := got["destination"].(map[string]any)
	if d["name"] != "YouTube" || d["platform"] != "youtube" {
		t.Errorf("destination = %v", d)
	}
	if got["at"] != "2026-09-09T20:15:03Z" {
		t.Errorf("at = %v", got["at"])
	}
}

func TestJSONPayloadWithoutDestinationIsNull(t *testing.T) {
	ev := evento()
	ev.DestinationID, ev.SessionID = nil, nil
	body, _ := alerts.Payload(store.WebhookJSON, ev, nil)
	if !strings.Contains(string(body), `"destination":null`) || !strings.Contains(string(body), `"session_id":null`) {
		t.Errorf("cuerpo = %s", body)
	}
}

func TestDiscordAndSlackPayloads(t *testing.T) {
	dest := &store.Destination{Name: "YouTube"}
	body, _ := alerts.Payload(store.WebhookDiscord, evento(), dest)
	var d map[string]string
	_ = json.Unmarshal(body, &d)
	if !strings.HasPrefix(d["content"], "**[error]** YouTube · ") {
		t.Errorf("discord = %s", body)
	}
	body, _ = alerts.Payload(store.WebhookSlack, evento(), dest)
	var s map[string]string
	_ = json.Unmarshal(body, &s)
	if !strings.HasPrefix(s["text"], "[error] YouTube · ") {
		t.Errorf("slack = %s", body)
	}
}

func TestSignIsHMACSHA256Hex(t *testing.T) {
	body := []byte(`{"a":1}`)
	mac := hmac.New(sha256.New, []byte("s3cr3t"))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := alerts.Sign(crypto.Secret("s3cr3t"), body); got != want {
		t.Errorf("Sign = %q, quería %q", got, want)
	}
}
```

Crear `internal/alerts/webhooks_test.go`:

```go
package alerts_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/alerts"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/events"
	"github.com/aprendomx/splitstream/internal/store"
)

func setup(t *testing.T) (*store.DB, *crypto.Cipher, *events.Bus) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var k [32]byte
	c, _ := crypto.NewCipher(k)
	if err := db.Bootstrap(ctx, c); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	db.SetEventHook(bus.Publish)
	return db, c, bus
}

// receptor captura lo que llega y responde lo que el test diga.
type receptor struct {
	mu       sync.Mutex
	cuerpos  [][]byte
	cabecera []http.Header
	codigos  []int // se consumen en orden; agotados, 200
}

func (r *receptor) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.cuerpos = append(r.cuerpos, b)
		r.cabecera = append(r.cabecera, req.Header.Clone())
		code := http.StatusOK
		if len(r.codigos) > 0 {
			code, r.codigos = r.codigos[0], r.codigos[1:]
		}
		r.mu.Unlock()
		w.WriteHeader(code)
	}
}

func (r *receptor) recibidos() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cuerpos)
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

func crearHook(t *testing.T, db *store.DB, c *crypto.Cipher, url string, ajustar func(*store.NewWebhook)) *store.Webhook {
	t.Helper()
	in := store.NewWebhook{Name: "h", URL: url, Format: store.WebhookJSON, MinLevel: store.LevelWarn, Enabled: true}
	if ajustar != nil {
		ajustar(&in)
	}
	w, err := db.CreateWebhook(context.Background(), c, in)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestDispatcherDeliversSignedEventsAboveMinLevel(t *testing.T) {
	db, c, bus := setup(t)
	rec := &receptor{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	crearHook(t, db, c, srv.URL, func(in *store.NewWebhook) { in.Secret = crypto.Secret("s3cr3t") })

	d := alerts.NewWebhookDispatcher(bus, db, c, nil, "v-test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// info: por debajo del mínimo, no viaja. warn: sí.
	if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelInfo, Kind: "ruido", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelWarn, Kind: "destination_disconnected", Message: "se cayó"}); err != nil {
		t.Fatal(err)
	}
	esperar(t, 5*time.Second, func() bool { return rec.recibidos() == 1 }, "llegó un envío")
	time.Sleep(200 * time.Millisecond)
	if rec.recibidos() != 1 {
		t.Fatalf("llegaron %d envíos, quería 1 (el info no pasa el mínimo)", rec.recibidos())
	}

	rec.mu.Lock()
	cuerpo, h := rec.cuerpos[0], rec.cabecera[0]
	rec.mu.Unlock()
	if !strings.Contains(string(cuerpo), `"kind":"destination_disconnected"`) {
		t.Errorf("cuerpo = %s", cuerpo)
	}
	if h.Get("X-Splitstream-Event") != "destination_disconnected" {
		t.Errorf("X-Splitstream-Event = %q", h.Get("X-Splitstream-Event"))
	}
	if !strings.HasPrefix(h.Get("User-Agent"), "splitstream/v-test") {
		t.Errorf("User-Agent = %q", h.Get("User-Agent"))
	}
	mac := hmac.New(sha256.New, []byte("s3cr3t"))
	mac.Write(cuerpo)
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); h.Get("X-Splitstream-Signature") != want {
		t.Errorf("firma = %q, quería %q", h.Get("X-Splitstream-Signature"), want)
	}

	esperar(t, 2*time.Second, func() bool {
		l, _ := db.ListWebhooks(ctx)
		return l[0].LastStatus != nil && *l[0].LastStatus == 200
	}, "se registró la entrega")
	ok, failed := d.Stats()
	if ok != 1 || failed != 0 {
		t.Errorf("stats = %d ok, %d failed", ok, failed)
	}
}

// Un 5xx se reintenta; un 4xx es configuración y no.
func TestDispatcherRetriesOn5xxButNotOn4xx(t *testing.T) {
	db, c, _ := setup(t)
	rec := &receptor{codigos: []int{503, 503, 200}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	w := crearHook(t, db, c, srv.URL, nil)

	d := alerts.NewWebhookDispatcher(nil, db, c, nil, "v")
	d.Backoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	ev := store.Event{ID: 1, Level: store.LevelWarn, Kind: "k", Message: "m"}
	if err := d.Send(context.Background(), *w, ev); err != nil {
		t.Fatalf("Send tras dos 503 y un 200 = %v", err)
	}
	if rec.recibidos() != 3 {
		t.Errorf("intentos = %d, quería 3", rec.recibidos())
	}

	rec2 := &receptor{codigos: []int{400}}
	srv2 := httptest.NewServer(rec2.handler())
	defer srv2.Close()
	w2 := crearHook(t, db, c, srv2.URL, nil)
	if err := d.Send(context.Background(), *w2, ev); err == nil {
		t.Error("Send con 400 = nil, quería error")
	}
	if rec2.recibidos() != 1 {
		t.Errorf("intentos con 400 = %d, quería 1", rec2.recibidos())
	}
	l, _ := db.ListWebhooks(context.Background())
	if *l[1].LastStatus != 400 || l[1].LastError == "" {
		t.Errorf("no se registró el 400: %+v", l[1])
	}
}

// Un endpoint que no responde no puede frenar al bus ni a los sinks: el plazo por intento
// acota, y el bus no bloquea.
func TestDispatcherDoesNotBlockTheBusOnASlowEndpoint(t *testing.T) {
	db, c, bus := setup(t)
	var atendidas atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atendidas.Add(1)
		time.Sleep(30 * time.Second)
	}))
	defer srv.CloseClientConnections()
	defer srv.Close()
	crearHook(t, db, c, srv.URL, nil)

	d := alerts.NewWebhookDispatcher(bus, db, c, nil, "v")
	d.Timeout = 200 * time.Millisecond
	d.Backoff = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	inicio := time.Now()
	for i := 0; i < 50; i++ {
		if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelError, Kind: "k", Message: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(inicio); d > 2*time.Second {
		t.Fatalf("50 LogEvent tardaron %v con un webhook colgado", d)
	}
	esperar(t, 5*time.Second, func() bool { _, f := d.Stats(); return f >= 1 }, "algún envío falló por plazo")
}
```

- [ ] **Step 7: Correr en rojo**

```bash
go test ./internal/alerts/ -count=1
```
Expected: FAIL, paquete inexistente.

- [ ] **Step 8: Implementar el payload**

Crear `internal/alerts/payload.go`:

```go
// Package alerts convierte eventos en avisos: hoy webhooks salientes; el panel recibe los
// suyos por el WebSocket de estado.
package alerts

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

type jsonDestination struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

type jsonPayload struct {
	ID          int64            `json:"id"`
	Kind        string           `json:"kind"`
	Level       string           `json:"level"`
	Message     string           `json:"message"`
	SessionID   *int64           `json:"session_id"`
	Destination *jsonDestination `json:"destination"`
	At          time.Time        `json:"at"`
}

// Payload compone el cuerpo del aviso según el formato (spec v0.8 §5.2). dest puede ser
// nil: un evento del sistema no tiene destino.
func Payload(format store.WebhookFormat, ev store.Event, dest *store.Destination) ([]byte, error) {
	switch format {
	case store.WebhookJSON:
		p := jsonPayload{
			ID: ev.ID, Kind: ev.Kind, Level: string(ev.Level), Message: ev.Message,
			SessionID: ev.SessionID, At: ev.CreatedAt.UTC(),
		}
		if dest != nil {
			p.Destination = &jsonDestination{ID: dest.ID, Name: dest.Name, Platform: string(dest.Platform)}
		}
		return json.Marshal(p)
	case store.WebhookDiscord:
		return json.Marshal(map[string]string{"content": "**[" + string(ev.Level) + "]** " + linea(ev, dest)})
	case store.WebhookSlack:
		return json.Marshal(map[string]string{"text": "[" + string(ev.Level) + "] " + linea(ev, dest)})
	default:
		return nil, fmt.Errorf("formato de webhook desconocido %q", format)
	}
}

// linea es el texto de una sola línea para los chats: "YouTube · el destino …" o solo el
// mensaje si no hay destino.
func linea(ev store.Event, dest *store.Destination) string {
	if dest != nil {
		return dest.Name + " · " + ev.Message
	}
	return ev.Message
}

// Sign devuelve "sha256=<hex>" del HMAC-SHA256 del cuerpo con el secreto.
func Sign(secret crypto.Secret, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret.Reveal()))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
```

- [ ] **Step 9: Implementar el despachador**

Crear `internal/alerts/webhooks.go`:

```go
package alerts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/events"
	"github.com/aprendomx/splitstream/internal/store"
)

const (
	// busBuffer es cuántos eventos aguanta la cola antes de perder alguno. Un destino
	// aleteando produce uno cada pocos segundos; 256 son minutos de margen.
	busBuffer = 256
	// maxParalelo acota los envíos simultáneos. Con dos o tres webhooks configurados,
	// cuatro es de sobra; el tope existe para que un endpoint colgado no acumule
	// goroutines.
	maxParalelo = 4
	// defaultTimeout es el plazo de cada intento.
	defaultTimeout = 10 * time.Second
)

// defaultBackoff son las esperas entre reintentos. Solo con error de red o 5xx: un 4xx es
// configuración, y reintentarlo es martillear.
var defaultBackoff = []time.Duration{time.Second, 4 * time.Second, 16 * time.Second}

// WebhookDispatcher escucha el bus y manda cada evento a los webhooks cuyo nivel mínimo
// alcance. Nunca bloquea al bus: la cola tiene tope y los envíos van en goroutines
// acotadas por un semáforo.
type WebhookDispatcher struct {
	db      *store.DB
	cipher  *crypto.Cipher
	log     *slog.Logger
	client  *http.Client
	ch      <-chan store.Event
	release func()
	version string

	// Timeout y Backoff son públicos para los tests; en producción se dejan por defecto.
	Timeout time.Duration
	Backoff []time.Duration

	sem    chan struct{}
	wg     sync.WaitGroup
	ok     atomic.Uint64
	failed atomic.Uint64
}

// NewWebhookDispatcher se suscribe al bus (si no es nil) y queda listo para Run.
func NewWebhookDispatcher(bus *events.Bus, db *store.DB, c *crypto.Cipher, log *slog.Logger, version string) *WebhookDispatcher {
	if log == nil {
		log = slog.Default()
	}
	d := &WebhookDispatcher{
		db: db, cipher: c, log: log, version: version,
		client:  &http.Client{},
		Timeout: defaultTimeout, Backoff: defaultBackoff,
		sem: make(chan struct{}, maxParalelo),
	}
	if bus != nil {
		d.ch, d.release = bus.Subscribe(busBuffer)
	}
	return d
}

// Run consume el bus hasta que el contexto termine. Espera a los envíos en vuelo al salir.
func (d *WebhookDispatcher) Run(ctx context.Context) {
	if d.ch == nil {
		return
	}
	defer d.release()
	for {
		select {
		case <-ctx.Done():
			d.wg.Wait()
			return
		case ev, ok := <-d.ch:
			if !ok {
				d.wg.Wait()
				return
			}
			d.dispatch(ctx, ev)
		}
	}
}

// Stats devuelve entregas buenas y fallidas (tras agotar reintentos), para /metrics.
func (d *WebhookDispatcher) Stats() (ok, failed uint64) {
	return d.ok.Load(), d.failed.Load()
}

func (d *WebhookDispatcher) dispatch(ctx context.Context, ev store.Event) {
	hooks, err := d.db.ListWebhooks(ctx)
	if err != nil {
		d.log.Error("no se pudieron leer los webhooks", "err", err)
		return
	}
	for _, h := range hooks {
		if !h.Enabled || rank(ev.Level) < rank(h.MinLevel) {
			continue
		}
		h := h
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			select {
			case d.sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-d.sem }()
			if err := d.Send(ctx, h, ev); err != nil {
				d.log.Warn("webhook sin entregar", "webhook", h.Name, "err", err)
			}
		}()
	}
}

// Send entrega un evento a un webhook con reintentos, y deja constancia del resultado
// en la fila del webhook. Lo usa también el botón «Probar» del panel.
func (d *WebhookDispatcher) Send(ctx context.Context, w store.Webhook, ev store.Event) error {
	var dest *store.Destination
	if ev.DestinationID != nil {
		if x, err := d.db.DestinationByID(ctx, *ev.DestinationID); err == nil {
			dest = x
		}
	}
	body, err := Payload(w.Format, ev, dest)
	if err != nil {
		return err
	}
	var firma string
	if w.Format == store.WebhookJSON && w.HasSecret {
		secret, err := d.db.WebhookSecret(ctx, d.cipher, w.ID)
		if err != nil {
			return err
		}
		if secret.Reveal() != "" {
			firma = Sign(secret, body)
		}
	}

	var (
		status  int
		lastErr error
	)
	for intento := 0; ; intento++ {
		status, lastErr = d.intento(ctx, w.URL, ev.Kind, firma, body)
		if lastErr == nil {
			d.ok.Add(1)
			_ = d.db.RecordWebhookDelivery(context.Background(), w.ID, status, "")
			return nil
		}
		// Solo se reintenta lo transitorio: red (status 0) o 5xx.
		if (status != 0 && status < 500) || intento >= len(d.Backoff) {
			break
		}
		select {
		case <-ctx.Done():
			lastErr = ctx.Err()
			intento = len(d.Backoff)
		case <-time.After(d.Backoff[intento]):
			continue
		}
		break
	}
	d.failed.Add(1)
	_ = d.db.RecordWebhookDelivery(context.Background(), w.ID, status, lastErr.Error())
	return lastErr
}

// intento hace un POST. Devuelve el código HTTP (0 si no hubo respuesta) y el error.
func (d *WebhookDispatcher) intento(ctx context.Context, url, kind, firma string, body []byte) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "splitstream/"+d.version)
	req.Header.Set("X-Splitstream-Event", kind)
	if firma != "" {
		req.Header.Set("X-Splitstream-Signature", firma)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, fmt.Errorf("el servidor respondió %d", resp.StatusCode)
}

// rank ordena los niveles para comparar con min_level.
func rank(l store.Level) int {
	switch l {
	case store.LevelWarn:
		return 1
	case store.LevelError:
		return 2
	default:
		return 0
	}
}

var _ = errors.New // reservado para errores centinela futuros
```

(Quitar la última línea si `errors` no se usa; es solo para que el archivo compile si se reordena.)

- [ ] **Step 10: Correr en verde y commitear**

```bash
go test ./internal/alerts/ -race -count=1
git add internal/alerts
git commit -m "feat(alerts): webhooks salientes firmados con reintentos"
```

- [ ] **Step 11: Cablear en `main.go`**

Importar `internal/alerts`. Tras crear el bus (Task 2):

```go
	// Webhooks salientes: consumen el bus en su propia goroutine y jamás lo frenan.
	webhooks := alerts.NewWebhookDispatcher(bus, db, cipher, logger, version)
	go webhooks.Run(sinkCtx)
```

(`sinkCtx` se declara antes; mover la creación del bus y del despachador después de `sinkCtx` si hace falta.) Y en `ExtraMetrics` (Task 8), añadir:

```go
			ok, failed := webhooks.Stats()
			return []httpapi.Metric{
				{Name: "splitstream_events_bus_dropped_total", Type: "counter",
					Help: "Eventos que un consumidor lento no llegó a recibir.", Value: float64(bus.Dropped())},
				{Name: "splitstream_webhook_deliveries_total", Type: "counter", Help: "Entregas de webhooks por resultado.",
					Labels: map[string]string{"result": "ok"}, Value: float64(ok)},
				{Name: "splitstream_webhook_deliveries_total", Type: "counter", Help: "Entregas de webhooks por resultado.",
					Labels: map[string]string{"result": "failed"}, Value: float64(failed)},
			}
```

```bash
go build ./... && go vet ./... && go test ./cmd/splitstream/ -race -count=1
git add cmd/splitstream/main.go
git commit -m "feat: cablear el despachador de webhooks"
```

---

### Task 12: API de webhooks y `recent_events` en el estado

**Files:**
- Create: `internal/httpapi/webhooks.go`, `internal/httpapi/webhooks_test.go`
- Modify: `internal/httpapi/server.go` (`Config.Webhooks`, rutas), `internal/httpapi/dto.go` (`webhookDTO`, `statusDTO.RecentEvents`), `internal/httpapi/status.go`
- Modify: `internal/httpapi/status_test.go` (test de `recent_events`)
- Modify: `cmd/splitstream/main.go` (`Webhooks: webhooks`)

**Interfaces:**
- Produces: `httpapi.WebhookSender interface{ Send(ctx, store.Webhook, store.Event) error }`; `GET/POST /api/webhooks`, `PATCH/DELETE /api/webhooks/{id}`, `POST /api/webhooks/{id}/test`; `webhookDTO{id, name, url, format, has_secret, min_level, enabled, last_status, last_error, created_at, updated_at}`; `statusDTO.RecentEvents []eventDTO json:"recent_events"` (últimos 20).

- [ ] **Step 1: Tests (rojo)**

Crear `internal/httpapi/webhooks_test.go`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

type fakeSender struct {
	enviados []store.Event
	err      error
}

func (f *fakeSender) Send(ctx context.Context, w store.Webhook, ev store.Event) error {
	f.enviados = append(f.enviados, ev)
	return f.err
}

func decodeWebhook(t *testing.T, body []byte) webhookDTO {
	t.Helper()
	var w webhookDTO
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("decodificar: %v — %s", err, body)
	}
	return w
}

func TestWebhookCRUDNeverReturnsTheSecret(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodPost, "/api/webhooks",
		`{"name":"discord","url":"https://discord.com/api/webhooks/1/abc","format":"json","secret":"s3cr3t-inconfundible","min_level":"warn","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("crear: %d — %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "inconfundible") {
		t.Error("la respuesta del alta lleva el secreto")
	}
	w := decodeWebhook(t, rec.Body.Bytes())
	if !w.HasSecret || w.MinLevel != "warn" {
		t.Errorf("dto = %+v", w)
	}

	rec = do(t, srv, cookies, http.MethodGet, "/api/webhooks", "")
	if strings.Contains(rec.Body.String(), "inconfundible") {
		t.Error("el listado lleva el secreto")
	}

	rec = do(t, srv, cookies, http.MethodPatch, "/api/webhooks/"+itoa(w.ID), `{"enabled":false,"name":"apagado"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}
	if got := decodeWebhook(t, rec.Body.Bytes()); got.Enabled || got.Name != "apagado" {
		t.Errorf("patch mal aplicado: %+v", got)
	}

	if rec = do(t, srv, cookies, http.MethodDelete, "/api/webhooks/"+itoa(w.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec = do(t, srv, cookies, http.MethodDelete, "/api/webhooks/"+itoa(w.ID), ""); rec.Code != http.StatusNotFound {
		t.Errorf("segundo delete: %d, quería 404", rec.Code)
	}
}

func TestWebhookCreateValidates(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	rec := do(t, srv, cookies, http.MethodPost, "/api/webhooks",
		`{"name":"x","url":"http://hooks.example.com/x","format":"json","min_level":"warn"}`)
	if rec.Code != http.StatusBadRequest || errorCodeDe(t, rec) != codeInvalidInput {
		t.Errorf("código = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookTestSendsASyntheticEvent(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	fs := &fakeSender{}
	srv.webhooks = fs
	rec := do(t, srv, cookies, http.MethodPost, "/api/webhooks",
		`{"name":"x","url":"https://hooks.example.com/x","format":"slack","min_level":"info","enabled":true}`)
	w := decodeWebhook(t, rec.Body.Bytes())

	rec = do(t, srv, cookies, http.MethodPost, "/api/webhooks/"+itoa(w.ID)+"/test", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("test: %d — %s", rec.Code, rec.Body.String())
	}
	if len(fs.enviados) != 1 || fs.enviados[0].Kind != "webhook_test" {
		t.Errorf("enviados = %+v", fs.enviados)
	}
}

func TestWebhookTestReportsAFailedDelivery(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.webhooks = &fakeSender{err: context.DeadlineExceeded}
	rec := do(t, srv, cookies, http.MethodPost, "/api/webhooks",
		`{"name":"x","url":"https://hooks.example.com/x","format":"json","min_level":"info","enabled":true}`)
	w := decodeWebhook(t, rec.Body.Bytes())

	rec = do(t, srv, cookies, http.MethodPost, "/api/webhooks/"+itoa(w.ID)+"/test", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("código = %d, quería 502: %s", rec.Code, rec.Body.String())
	}
}
```

En `internal/httpapi/status_test.go`, añadir:

```go
// El estado lleva los últimos eventos para que el panel no sondee GET /api/events. Van en
// el mismo tipo que empuja el WebSocket (spec base §10).
func TestStatusCarriesRecentEventsNewestFirst(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelInfo, Kind: "k", Message: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	st := decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if len(st.RecentEvents) != 20 {
		t.Fatalf("recent_events = %d, quería 20", len(st.RecentEvents))
	}
	if st.RecentEvents[0].ID < st.RecentEvents[1].ID {
		t.Error("recent_events no va del más reciente al más antiguo")
	}
}
```

- [ ] **Step 2: Correr en rojo**

```bash
go test ./internal/httpapi/ -run 'Webhook|RecentEvents' -count=1
```
Expected: FAIL de compilación.

- [ ] **Step 3: Implementar**

En `internal/httpapi/dto.go`, en `statusDTO` tras `Destinations`:

```go
	// RecentEvents son los últimos 20 eventos, para el registro del panel y sus avisos.
	RecentEvents []eventDTO `json:"recent_events"`
```

y al final del archivo:

```go
type webhookDTO struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Format     string    `json:"format"`
	HasSecret  bool      `json:"has_secret"`
	MinLevel   string    `json:"min_level"`
	Enabled    bool      `json:"enabled"`
	LastStatus *int      `json:"last_status"`
	LastError  string    `json:"last_error"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// newWebhookDTO. El secreto no aparece: el store ni siquiera lo pone en Webhook.
func newWebhookDTO(w store.Webhook) webhookDTO {
	return webhookDTO{
		ID: w.ID, Name: w.Name, URL: w.URL, Format: string(w.Format), HasSecret: w.HasSecret,
		MinLevel: string(w.MinLevel), Enabled: w.Enabled, LastStatus: w.LastStatus,
		LastError: w.LastError, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
	}
}
```

En `internal/httpapi/status.go`, en `status()` antes del `return out, nil`:

```go
	// Los últimos eventos viajan con el estado: el panel los pinta y avisa de los nuevos
	// sin sondear. 20 es lo que cabe en el registro sin desplazarse.
	recientes, err := s.db.RecentEvents(ctx, recentEventsInStatus)
	if err != nil {
		return out, err
	}
	out.RecentEvents = make([]eventDTO, 0, len(recientes))
	for _, e := range recientes {
		out.RecentEvents = append(out.RecentEvents, newEventDTO(e))
	}
```

y arriba del archivo `const recentEventsInStatus = 20`.

En `internal/httpapi/server.go`: interfaz, campo, config y rutas:

```go
// WebhookSender manda un evento a un webhook. Lo cumple *alerts.WebhookDispatcher; la API
// lo usa para el botón «Probar».
type WebhookSender interface {
	Send(ctx context.Context, w store.Webhook, ev store.Event) error
}
```

`Config.Webhooks WebhookSender`; `Server.webhooks WebhookSender`; en `New`: `webhooks: cfg.Webhooks,`. Rutas:

```go
	protegida("GET /api/webhooks", s.handleListWebhooks)
	protegida("POST /api/webhooks", s.handleCreateWebhook)
	protegida("PATCH /api/webhooks/{id}", s.handlePatchWebhook)
	protegida("DELETE /api/webhooks/{id}", s.handleDeleteWebhook)
	protegida("POST /api/webhooks/{id}/test", s.handleTestWebhook)
```

Crear `internal/httpapi/webhooks.go`:

```go
package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

type webhookCreate struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Format   string `json:"format"`
	Secret   string `json:"secret"`
	MinLevel string `json:"min_level"`
	Enabled  bool   `json:"enabled"`
}

// webhookPatch usa punteros para distinguir "no vino" de "vino vacío": un secret vacío
// QUITA el secreto, y un secret ausente lo deja como está.
type webhookPatch struct {
	Name     *string `json:"name"`
	URL      *string `json:"url"`
	Format   *string `json:"format"`
	Secret   *string `json:"secret"`
	MinLevel *string `json:"min_level"`
	Enabled  *bool   `json:"enabled"`
}

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	hooks, err := s.db.ListWebhooks(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	out := make([]webhookDTO, 0, len(hooks))
	for _, h := range hooks {
		out = append(out, newWebhookDTO(h))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var in webhookCreate
	if !decodeBody(w, r, &in) {
		return
	}
	h, err := s.db.CreateWebhook(r.Context(), s.cipher, store.NewWebhook{
		Name: in.Name, URL: in.URL, Format: store.WebhookFormat(in.Format),
		Secret: crypto.Secret(in.Secret), MinLevel: store.Level(in.MinLevel), Enabled: in.Enabled,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	w.Header().Set("Location", "/api/webhooks/"+strconv.FormatInt(h.ID, 10))
	writeJSON(w, http.StatusCreated, newWebhookDTO(*h))
}

func (s *Server) handlePatchWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var in webhookPatch
	if !decodeBody(w, r, &in) {
		return
	}
	patch := store.WebhookPatch{Name: in.Name, URL: in.URL, Enabled: in.Enabled}
	if in.Format != nil {
		f := store.WebhookFormat(*in.Format)
		patch.Format = &f
	}
	if in.MinLevel != nil {
		l := store.Level(*in.MinLevel)
		patch.MinLevel = &l
	}
	if in.Secret != nil {
		sec := crypto.Secret(*in.Secret)
		patch.Secret = &sec
	}
	h, err := s.db.UpdateWebhook(r.Context(), s.cipher, id, patch)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newWebhookDTO(*h))
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if err := s.db.DeleteWebhook(r.Context(), id); err != nil {
		s.writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestWebhook manda un evento sintético, con reintentos y plazo, y responde 204 si
// llegó o 502 si no. No pasa por el bus: el usuario quiere la respuesta ahora.
func (s *Server) handleTestWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if s.webhooks == nil {
		writeError(w, http.StatusConflict, codeConflict, "los webhooks no están disponibles en este arranque")
		return
	}
	hooks, err := s.db.ListWebhooks(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	var hook *store.Webhook
	for i := range hooks {
		if hooks[i].ID == id {
			hook = &hooks[i]
		}
	}
	if hook == nil {
		s.writeStoreError(w, store.ErrWebhookNotFound)
		return
	}
	ev := store.Event{
		Level: store.LevelInfo, Kind: "webhook_test",
		Message: "prueba de aviso desde Splitstream: si lees esto, el webhook funciona",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.webhooks.Send(r.Context(), *hook, ev); err != nil {
		writeError(w, http.StatusBadGateway, codeConflict, "no se pudo entregar: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

**Nota sobre el código de error:** `codeConflict` con 502 reutiliza el conjunto cerrado de `errors.go`; si el revisor prefiere un código nuevo (`upstream`), añadirlo a la lista y al test de códigos, no inventarlo suelto.

En `cmd/splitstream/main.go`, en `httpapi.Config`: `Webhooks: webhooks,`.

- [ ] **Step 4: Correr en verde y commitear**

```bash
go test ./internal/httpapi/ -race -count=1 && go build ./...
git add internal/httpapi/webhooks.go internal/httpapi/webhooks_test.go internal/httpapi/dto.go internal/httpapi/status.go internal/httpapi/status_test.go internal/httpapi/server.go cmd/splitstream/main.go
git commit -m "feat(api): CRUD de webhooks y últimos eventos en el estado"
```

---

### Task 13: Panel: registro de eventos, avisos y página de ajustes

**Files:**
- Create: `web/src/components/RegistroEventos.vue`, `web/src/components/DialogoWebhook.vue`, `web/src/pages/Ajustes.vue`
- Modify: `web/src/api.js`, `web/src/iconos.js`, `web/src/router.js`, `web/src/App.vue`, `web/src/stores/panel.js`, `web/src/pages/Panel.vue`

**Interfaces:**
- Consumes: `statusDTO.recent_events` (Task 12), `/api/webhooks*` (Task 12), `/api/backup`, `/api/sessions` (Task 10).
- Produces: ruta `/ajustes`; getter `panel.eventosRecientes`; aviso por evento `error` nuevo.

- [ ] **Step 1: Cliente de la API**

En `web/src/api.js`, dentro de `export const api = {`, al final:

```js
  webhooks: () => pedir('GET', '/api/webhooks'),
  crearWebhook: (w) => pedir('POST', '/api/webhooks', w),
  editarWebhook: (id, patch) => pedir('PATCH', `/api/webhooks/${id}`, patch),
  borrarWebhook: (id) => pedir('DELETE', `/api/webhooks/${id}`),
  probarWebhook: (id) => pedir('POST', `/api/webhooks/${id}/test`),
  sesiones: (limit = 50, before = 0) => pedir('GET', `/api/sessions?limit=${limit}&before=${before}`),
  // El respaldo es una descarga, no JSON: se pide con fetch y se entrega como blob.
  descargarRespaldo: async () => {
    const res = await fetch('/api/backup', { method: 'POST', credentials: 'same-origin' })
    if (!res.ok) throw new ApiError(res.status, 'internal', 'No se pudo generar el respaldo')
    const nombre = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? 'splitstream.db'
    return { blob: await res.blob(), nombre }
  },
```

En `web/src/iconos.js`, añadir a la exportación: `mdiCog as iAjustes,`, `mdiWebhook as iWebhook,`, `mdiDownload as iDescargar,`, `mdiHistory as iRegistro,`.

- [ ] **Step 2: Store — eventos recientes y avisos**

En `web/src/stores/panel.js`:

- En `state`, añadir `ultimoEventoAvisado: null,`.
- En `getters`, añadir `eventosRecientes: (s) => s.estado?.recent_events ?? [],`.
- Sustituir la asignación `this.estado = JSON.parse(ev.data)` de `ws.onmessage` por:

```js
          const nuevo = JSON.parse(ev.data)
          this.avisarErroresNuevos(nuevo.recent_events ?? [])
          this.estado = nuevo
```

- En `cargar()`, tras `this.estado = await api.estado()`, añadir la línea `this.marcarVistos(this.estado.recent_events ?? [])` para que el primer estado tras cargar no avise de eventos viejos.
- En `actions`, añadir:

```js
    /** El primer estado no avisa: son eventos de antes de abrir el panel. */
    marcarVistos(eventos) {
      this.ultimoEventoAvisado = eventos[0]?.id ?? 0
    },

    /**
     * Un aviso por cada evento de nivel error que no se había visto. Los warn no avisan:
     * un destino reconectando durante una emisión larga produciría una notificación cada
     * pocos segundos, y eso es ruido que acaba en "cerrar sin leer".
     */
    avisarErroresNuevos(eventos) {
      if (this.ultimoEventoAvisado === null) { this.marcarVistos(eventos); return }
      const nuevos = eventos.filter((e) => e.id > this.ultimoEventoAvisado)
      if (!nuevos.length) return
      this.ultimoEventoAvisado = nuevos[0].id
      for (const e of nuevos.filter((e) => e.level === 'error').reverse()) {
        Notify.create({ type: 'negative', message: e.message, timeout: 8000, actions: [{ label: 'Cerrar', color: 'white' }] })
      }
    },
```

e importar `import { Notify } from 'quasar'` arriba. (`refrescarEventos` y `eventos` se quedan: los usa el revelado de clave para forzar una lectura completa.)

- [ ] **Step 3: El registro de eventos**

Crear `web/src/components/RegistroEventos.vue`:

```vue
<script setup>
import { computed } from 'vue'
import { iRegistro } from '@/iconos'
import { usePanel } from '@/stores/panel'

// El «panel de log en vivo» del spec base §10, que hasta la v0.8 nadie había pintado. Se
// alimenta de recent_events, que viaja con el estado: no hay petición aparte.
const panel = usePanel()

const NIVEL = {
  info: { color: 'grey-6', etiqueta: 'info' },
  warn: { color: 'warning', etiqueta: 'aviso' },
  error: { color: 'negative', etiqueta: 'error' },
}

const nombreDestino = (id) => panel.destinos.find((d) => d.id === id)?.name ?? null

const filas = computed(() => panel.eventosRecientes.map((e) => ({
  ...e,
  nivel: NIVEL[e.level] ?? NIVEL.info,
  destino: e.destination_id ? nombreDestino(e.destination_id) : null,
  hora: new Date(e.created_at).toLocaleTimeString('es', { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
})))
</script>

<template>
  <q-card flat bordered>
    <q-card-section class="row items-center q-py-sm">
      <q-icon :name="iRegistro" size="20px" class="q-mr-sm text-grey-5" />
      <div class="text-subtitle2">Registro</div>
      <q-space />
      <div class="text-caption text-grey-6">últimos {{ filas.length }}</div>
    </q-card-section>
    <q-separator />
    <q-list dense class="registro">
      <q-item v-for="e in filas" :key="e.id" class="fila">
        <q-item-section side class="hora">{{ e.hora }}</q-item-section>
        <q-item-section side>
          <q-badge :color="e.nivel.color" :label="e.nivel.etiqueta" class="nivel" />
        </q-item-section>
        <q-item-section>
          <q-item-label class="mensaje">
            <span v-if="e.destino" class="text-weight-medium">{{ e.destino }} · </span>{{ e.message }}
          </q-item-label>
        </q-item-section>
      </q-item>
      <q-item v-if="!filas.length">
        <q-item-section class="text-grey-6 text-caption">Todavía no ha pasado nada.</q-item-section>
      </q-item>
    </q-list>
  </q-card>
</template>

<style scoped>
.registro { max-height: 320px; overflow-y: auto; }
.hora {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: rgba(255, 255, 255, 0.5);
  min-width: 64px;
}
.nivel { font-size: 10px; min-width: 42px; justify-content: center; }
.mensaje { font-size: 13px; white-space: normal; }
</style>
```

En `web/src/pages/Panel.vue`: importar `RegistroEventos` y ponerlo al final de la `<q-page>`, tras el `<draggable>`/tarjeta vacía y antes de `<DialogoDestino>`:

```html
    <RegistroEventos class="q-mt-md" />
```

- [ ] **Step 4: La página de ajustes con webhooks y respaldo**

Crear `web/src/components/DialogoWebhook.vue`:

```vue
<script setup>
import { ref, computed, watch } from 'vue'
import { iCerrar, iError } from '@/iconos'
import { api, ApiError } from '@/api'

const props = defineProps({ modelValue: Boolean, webhook: { type: Object, default: null } })
const emit = defineEmits(['update:modelValue', 'guardado'])

const editando = computed(() => Boolean(props.webhook))
const nombre = ref('')
const url = ref('')
const formato = ref('discord')
const secreto = ref('')
const nivel = ref('warn')
const habilitado = ref(true)
const guardando = ref(false)
const error = ref(null)

const FORMATOS = [
  { value: 'discord', label: 'Discord' },
  { value: 'slack', label: 'Slack' },
  { value: 'json', label: 'JSON genérico (con firma)' },
]
const NIVELES = [
  { value: 'error', label: 'Solo errores' },
  { value: 'warn', label: 'Avisos y errores' },
  { value: 'info', label: 'Todo' },
]

watch(() => props.modelValue, (abierto) => {
  if (!abierto) return
  error.value = null
  guardando.value = false
  secreto.value = ''
  if (props.webhook) {
    nombre.value = props.webhook.name
    url.value = props.webhook.url
    formato.value = props.webhook.format
    nivel.value = props.webhook.min_level
    habilitado.value = props.webhook.enabled
  } else {
    nombre.value = ''
    url.value = ''
    formato.value = 'discord'
    nivel.value = 'warn'
    habilitado.value = true
  }
})

function cerrar() { emit('update:modelValue', false) }

async function guardar() {
  guardando.value = true
  error.value = null
  try {
    const datos = { name: nombre.value, url: url.value, format: formato.value, min_level: nivel.value, enabled: habilitado.value }
    if (editando.value) {
      // Secreto vacío al editar = "no lo toques". Quitarlo se hace desde el botón aparte.
      if (secreto.value) datos.secret = secreto.value
      await api.editarWebhook(props.webhook.id, datos)
    } else {
      datos.secret = secreto.value
      await api.crearWebhook(datos)
    }
    emit('guardado')
    cerrar()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'No se pudo guardar'
  } finally {
    guardando.value = false
  }
}
</script>

<template>
  <q-dialog :model-value="modelValue" @update:model-value="emit('update:modelValue', $event)"
            :maximized="$q.screen.lt.sm">
    <q-card class="dialogo-webhook column no-wrap">
      <q-card-section class="row items-center q-pb-sm">
        <div class="text-h6">{{ editando ? 'Editar aviso' : 'Nuevo aviso' }}</div>
        <q-space />
        <q-btn flat round dense :icon="iCerrar" aria-label="Cerrar" @click="cerrar" />
      </q-card-section>
      <q-card-section class="col scroll q-pt-none q-gutter-y-md">
        <q-input v-model="nombre" label="Nombre" outlined dense maxlength="60" />
        <q-select v-model="formato" :options="FORMATOS" emit-value map-options label="Formato" outlined dense />
        <q-input v-model="url" label="URL" placeholder="https://…" outlined dense inputmode="url"
                 autocapitalize="off" autocorrect="off" spellcheck="false"
                 hint="Discord y Slack te dan la URL al crear el webhook en el canal. Solo https." />
        <q-input v-if="formato === 'json'" v-model="secreto" label="Secreto para firmar" outlined dense
                 type="password" autocomplete="off"
                 :hint="editando && webhook?.has_secret ? 'Déjalo vacío para conservar el actual' : 'Opcional. Se manda como HMAC-SHA256 en X-Splitstream-Signature'" />
        <q-select v-model="nivel" :options="NIVELES" emit-value map-options label="Avisar de" outlined dense />
        <q-toggle v-model="habilitado" label="Activo" />
        <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">
          <template #avatar><q-icon :name="iError" color="negative" /></template>
          {{ error }}
        </q-banner>
      </q-card-section>
      <q-card-actions align="right" class="q-pa-md">
        <q-btn flat no-caps label="Cancelar" @click="cerrar" />
        <q-btn unelevated no-caps color="primary" :loading="guardando" :label="editando ? 'Guardar' : 'Crear'" @click="guardar" />
      </q-card-actions>
    </q-card>
  </q-dialog>
</template>

<style scoped>
.dialogo-webhook { width: 480px; max-width: 100vw; max-height: 90vh; }
</style>
```

Crear `web/src/pages/Ajustes.vue`:

```vue
<script setup>
import { ref, onMounted } from 'vue'
import { useQuasar } from 'quasar'
import { iMas, iEditar, iBorrar, iWebhook, iDescargar, iProbar } from '@/iconos'
import { api } from '@/api'
import DialogoWebhook from '@/components/DialogoWebhook.vue'

// Ajustes que no caben en el panel principal: avisos por webhook y respaldo. En la v0.9
// gana la grabación.
const $q = useQuasar()
const webhooks = ref([])
const dialogo = ref(false)
const editando = ref(null)
const probando = ref(null)
const respaldando = ref(false)

async function cargar() {
  try { webhooks.value = await api.webhooks() } catch (e) { $q.notify({ type: 'negative', message: e.message }) }
}
onMounted(cargar)

function abrirAlta() { editando.value = null; dialogo.value = true }
function abrirEdicion(w) { editando.value = w; dialogo.value = true }

async function alternar(w) {
  try { await api.editarWebhook(w.id, { enabled: !w.enabled }); await cargar() }
  catch (e) { $q.notify({ type: 'negative', message: e.message }) }
}

function borrar(w) {
  $q.dialog({
    title: 'Eliminar aviso', message: `Se eliminará «${w.name}».`,
    cancel: { flat: true, noCaps: true, label: 'Cancelar' },
    ok: { color: 'negative', unelevated: true, noCaps: true, label: 'Eliminar' }, persistent: true,
  }).onOk(async () => {
    try { await api.borrarWebhook(w.id); await cargar() }
    catch (e) { $q.notify({ type: 'negative', message: e.message }) }
  })
}

async function probar(w) {
  probando.value = w.id
  try {
    await api.probarWebhook(w.id)
    $q.notify({ type: 'positive', message: `Aviso entregado a ${w.name}` })
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message, timeout: 8000 })
  } finally {
    probando.value = null
    await cargar()
  }
}

/** La descarga del respaldo pasa por un <a download>: fetch con la cookie y luego un blob. */
async function respaldar() {
  respaldando.value = true
  try {
    const { blob, nombre } = await api.descargarRespaldo()
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = nombre
    a.click()
    URL.revokeObjectURL(a.href)
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    respaldando.value = false
  }
}

const estadoEntrega = (w) => {
  if (w.last_status === null) return { texto: 'sin enviar aún', color: 'grey-6' }
  if (w.last_status >= 200 && w.last_status < 300) return { texto: `último envío: ${w.last_status}`, color: 'positive' }
  return { texto: `último envío falló (${w.last_status || 'sin respuesta'}): ${w.last_error}`, color: 'negative' }
}
</script>

<template>
  <q-page class="q-pa-md q-pb-xl ajustes">
    <div class="contenido">
      <div class="row items-center q-mb-sm">
        <div class="text-h6">Avisos</div>
        <q-space />
        <q-btn unelevated no-caps color="primary" :icon="iMas" label="Nuevo aviso" @click="abrirAlta" />
      </div>
      <p class="text-body2 text-grey-5">
        Cuando un canal falla o se corta la emisión, Splitstream puede avisarte en Discord, Slack o
        cualquier servidor que reciba JSON.
      </p>

      <q-card v-if="!webhooks.length" flat bordered class="q-pa-lg text-center">
        <q-icon :name="iWebhook" size="36px" class="text-grey-7" />
        <div class="text-body2 text-grey-5 q-mt-sm">Todavía no hay avisos configurados.</div>
      </q-card>

      <q-list v-else bordered separator class="rounded-borders">
        <q-item v-for="w in webhooks" :key="w.id">
          <q-item-section>
            <q-item-label>{{ w.name }} <q-badge outline color="grey-6" :label="w.format" class="q-ml-xs" /></q-item-label>
            <q-item-label caption class="ellipsis">{{ w.url }}</q-item-label>
            <q-item-label caption :class="`text-${estadoEntrega(w).color}`">{{ estadoEntrega(w).texto }}</q-item-label>
          </q-item-section>
          <q-item-section side>
            <div class="row items-center no-wrap q-gutter-xs">
              <q-btn flat dense no-caps size="sm" :icon="iProbar" label="Probar" :loading="probando === w.id" @click="probar(w)" />
              <q-toggle :model-value="w.enabled" dense @update:model-value="alternar(w)" :aria-label="`${w.enabled ? 'Desactivar' : 'Activar'} ${w.name}`" />
              <q-btn flat round dense :icon="iEditar" size="sm" aria-label="Editar" @click="abrirEdicion(w)" />
              <q-btn flat round dense :icon="iBorrar" size="sm" class="text-negative" aria-label="Eliminar" @click="borrar(w)" />
            </div>
          </q-item-section>
        </q-item>
      </q-list>

      <div class="text-h6 q-mt-xl q-mb-sm">Respaldo</div>
      <q-card flat bordered>
        <q-card-section>
          <p class="text-body2 text-grey-5 q-mb-md">
            Descarga una copia de la base de datos: canales, claves cifradas y contraseña del
            panel. <b>Sin tu clave maestra el archivo no sirve</b>: guárdala aparte.
          </p>
          <q-btn unelevated no-caps color="primary" :icon="iDescargar" label="Descargar respaldo"
                 :loading="respaldando" @click="respaldar" />
        </q-card-section>
      </q-card>
    </div>
    <DialogoWebhook v-model="dialogo" :webhook="editando" @guardado="cargar" />
  </q-page>
</template>

<style scoped>
.contenido { max-width: 760px; margin: 0 auto; }
</style>
```

En `web/src/router.js`, añadir la ruta antes del comodín:

```js
    { path: '/ajustes', name: 'ajustes', component: () => import('@/pages/Ajustes.vue') },
```

En `web/src/App.vue`, en la barra, antes del botón de créditos:

```html
        <q-btn v-if="panel.autenticado" flat round dense :icon="iAjustes" aria-label="Ajustes" :to="{ name: 'ajustes' }" />
```

e importar `iAjustes` de `@/iconos`.

- [ ] **Step 5: Compilar, probar a mano y commitear**

```bash
cd web && npm run build && cd ..
make build-go && SPLITSTREAM_DB_PATH=/tmp/ss-panel.db ./splitstream
```
A mano, con el binario arrancado: crear un webhook de Discord real y pulsar «Probar» (debe llegar el mensaje); apagar un canal con OBS emitiendo y ver el evento en el registro; descargar el respaldo y abrirlo con `sqlite3` para ver que tiene tablas. Anotar en el ledger cualquier cosa que no funcione.

```bash
git add web/src
git commit -m "feat(panel): registro de eventos, avisos por error, ajustes con webhooks y respaldo"
```

---

### Task 14: Documentación, enmiendas al spec base, puerta de salida y etiqueta

**Files:**
- Modify: `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (§3.7, §6.5, §9, §12: enmiendas con fecha)
- Modify: `README.md` (operación: `/healthz`, `/metrics`, variables, `-backup`, `-healthcheck`)
- Modify: `docs/manual-de-usuario.md` (§4 estado «Suspendido», §5 «Probar», § nuevo «Avisos», § nuevo «Respaldo»)
- Modify: `docs/lanzamiento.md` (párrafo «Lo que aprendimos» gana el aviso y `/metrics`)
- Create: `docs/superpowers/plans/2026-09-09-confianza-produccion-ledger.md`

- [ ] **Step 1: Enmiendas al spec base**

En `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`:

- §3.7: sustituir la lista de estados por «`idle | connecting | live | reconnecting | error | suspended`, más un `degraded bool` independiente. *(`suspended` desde la v0.8, 2026-09-09; ver spec de la entrega §2.1.)*»
- §6.5: tras «Reintentos indefinidos mientras la sesión siga viva.» añadir el párrafo:

```markdown
> **Enmienda 2026-09-09 (v0.8):** los reintentos ya no son indefinidos. Tras
> `SuspendAfterAttempts` (10) intentos seguidos sin transmitir, o `SuspendAfterFlaps` (5)
> sesiones cortas seguidas, el sink pasa a `suspended` y deja de reintentar hasta que el
> usuario pulse «Reintentar» o empiece otra sesión. `enabled` no se toca. Razón: con una
> clave mal pegada, el bucle era silencioso; contra Facebook, cada intento cuenta como
> emisión activa y agotó el cupo de una cuenta real.
```

- §9: añadir al bloque de rutas las de la v0.8 (las once del spec de la entrega §2.3) con la nota «*(v0.8)*».
- §12: añadir las tres variables y los dos comandos nuevos.

- [ ] **Step 2: README y manual**

`README.md`, sección «Configuración»: tres filas nuevas en la tabla (`SPLITSTREAM_METRICS_TOKEN`, `SPLITSTREAM_RETENTION_DAYS`, `SPLITSTREAM_RETENTION_MAX_EVENTS`) y dos comandos (`-backup <ruta>`, `-healthcheck`). Nueva subsección «Vigilarlo desde fuera»:

```markdown
### Vigilarlo desde fuera

- `GET /healthz` responde `200` si el proceso atiende y la base contesta. No necesita
  sesión. Es lo que consulta el `HEALTHCHECK` de la imagen de Docker.
- `GET /metrics` expone métricas en formato Prometheus: estado y bitrate de cada canal,
  descartes, reconexiones, entregas de avisos. Pide sesión o
  `Authorization: Bearer $SPLITSTREAM_METRICS_TOKEN`.

```yaml
# prometheus.yml
scrape_configs:
  - job_name: splitstream
    authorization: { credentials: TU_TOKEN }
    static_configs: [{ targets: ['127.0.0.1:8080'] }]
```
```

`docs/manual-de-usuario.md`:

- §4, fila nueva: `| **Suspendido** | Lo intentó diez veces sin conseguirlo y dejó de insistir en esta emisión | Revisa la clave y pulsa **Reintentar** |`
- §5, subsección nueva «Probar un canal antes de emitir»: qué hace el botón, qué significa cada resultado (los cuatro mensajes de `probeMessage`), y las dos advertencias (no con el canal emitiendo; Facebook cuenta cada prueba).
- Sección nueva «Avisos»: cómo crear un webhook de Discord (Ajustes del canal → Integraciones → Webhooks → copiar URL), qué avisa cada nivel, y el formato JSON con la firma para quien monte su propio receptor.
- Sección nueva «Respaldo»: el botón, y que sin `splitstream.key` no sirve.

- [ ] **Step 3: La suite completa y la CI**

```bash
make vet && make test && make build && make sinks-up && make test-integration; make sinks-down
cd web && npm run build && cd ..
git add -A docs README.md
git commit -m "docs: v0.8 en el spec base, el README y el manual"
git push -u origin feat/confianza-produccion
```
Abrir el PR y esperar la CI **verificando el `headSha` del run** (`gh run list --branch feat/confianza-produccion`), no `gh pr checks`.

- [ ] **Step 4: Puerta de salida**

Con el binario de la rama en la máquina de siempre y OBS delante:

1. Un Prometheus local scrapeando `/metrics` cada 15 s con el token.
2. Un webhook de Discord con `min_level=warn`.
3. Emitir 30 minutos a YouTube, Twitch y Facebook reales.
4. A los 10 minutos, editar la clave de Twitch por una inventada: debe pasar por «Conecta y se corta» y llegar a «Suspendido» en ~2 minutos, con un aviso en Discord y en el panel; «Reintentar» tras restaurar la clave lo devuelve a «Emitiendo».
5. «Probar» sobre el canal de Facebook **apagado** con la clave buena → `plausible`; con una clave inventada en Twitch apagado → `closed_early`.
6. Descargar el respaldo y arrancar un segundo binario sobre él con la misma `.key`.
7. Al terminar: `docker compose ps` enseña `healthy` en el despliegue de Docker.

Anotar en el ledger fecha, duración, plataformas y lo que se desvió de lo esperado.

- [ ] **Step 5: Ledger, fusión y etiqueta**

Crear `docs/superpowers/plans/2026-09-09-confianza-produccion-ledger.md` con: decisiones tomadas durante la ejecución, desviaciones del plan por tarea (con el commit), hallazgos de la puerta de salida, y lo que queda abierto. Commitear, fusionar el PR, y:

```bash
git checkout main && git pull
git tag -a v0.8.0 -m "v0.8.0: confianza en producción"
git push origin v0.8.0
```

`release.yml` construye y publica los binarios. Comprobar que la release lleva los cinco archivos y `SHA256SUMS.txt`.

---

## Autorrevisión

**Cobertura del spec de la entrega:** §1.1 probar destino → Tasks 5 y 6; §1.2 hook → Task 2; §1.3 alertas (registro, avisos, webhooks) → Tasks 11, 12 y 13; §1.4 `/healthz`, `/metrics`, `-healthcheck` → Tasks 7 y 8; §1.5 retención, respaldo, sesiones → Tasks 9 y 10; §1.6 suspender → Tasks 3 y 4; §2 enmiendas → Task 14; §3 las dos reglas de seguridad de la sonda → Task 6 (409 con `live`; aviso de Facebook en el panel); §5.2 URL solo https, secreto cifrado y nunca devuelto, reintentos 5xx/no 4xx, `last_status` → Task 11; §6 series por estado y escapado de etiquetas → Task 8; §7 nunca con sesión viva, `VACUUM INTO` + rename, `backup_downloaded` warn → Tasks 9 y 10; §8 cada bloque de pruebas tiene su test con nombre en la tarea correspondiente; puerta de salida → Task 14.

**Marcadores:** sin «TBD» ni «implementar después». Las dos líneas de «adaptar si la firma difiere» (Task 10, `setPasswordEnv`) están porque el helper existe con esa firma —`setPasswordEnv(t) string`, verificado— y solo protegen contra un cambio posterior.

**Consistencia de nombres:** `store.EventHook`/`SetEventHook` (Task 2) es lo que cablea `main.go` con `bus.Publish`; `events.Bus.Subscribe(buffer)` (Task 2) es lo que consume `alerts.NewWebhookDispatcher` (Task 11); `relay.StateSuspended.String()` (Task 3) es lo que comparan `handleRetryDestination` (Task 4) y `handleTestDestination` (Task 6) y lo que lista `estados` en `/metrics` (Task 8); `probe.Result` (Task 5) es lo que devuelven `rtmpio.Probe` y `sinks.(*Factory).Test`, y lo que consume `httpapi.DestinationTester` (Task 6); `store.DestinationByID` (Task 4) lo reutilizan Task 6 y `alerts.Send` (Task 11); `httpapi.Metric`/`ExtraMetrics` (Task 8) es lo que rellena `main.go` con `bus.Dropped()` y `webhooks.Stats()` (Task 11); `store.Webhook`/`WebhookSecret`/`RecordWebhookDelivery` (Task 11) son lo que usan `alerts.Send` y los handlers de Task 12; `statusDTO.RecentEvents` (Task 12) es lo que lee `panel.eventosRecientes` (Task 13).
