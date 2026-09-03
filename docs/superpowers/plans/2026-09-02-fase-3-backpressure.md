# Splitstream — Plan de implementación, Fase 3

> **Para agentes:** SUB-SKILL REQUERIDA: usa `superpowers:subagent-driven-development`
> (recomendado) o `superpowers:executing-plans` para ejecutar este plan tarea por tarea.
> Los pasos usan sintaxis de checkbox (`- [ ]`) para seguimiento.

**Goal:** Que el motor retransmita a **varios destinos a la vez**, sobreviva a que uno se
caiga o se ponga lento sin afectar a los demás, y reporte qué está pasando con cada uno.

**Architecture:** La cola de cada sink pasa de un canal simple a un deque acotado por bytes
y por duración de media, con descarte **por GOP completo**: al desbordar se tira todo el
vídeo encolado y se sigue tirando hasta el siguiente keyframe. Cada sink gana un bucle de
reconexión con backoff exponencial y jitter, que reancla su timebase en cada intento. Y un
recolector de métricas por destino alimenta lo que la fase 4 expondrá por WebSocket.

**Tech Stack:** Go 1.25, `github.com/yutopp/go-rtmp` v0.0.7, `modernc.org/sqlite`,
`log/slog`. Docker con `mediamtx` y `ffmpeg` para los tests de integración.

**Spec:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
Lee especialmente **§3.3** (descarte por GOP), **§3.4** (cola por bytes y duración),
**§3.7** (`degraded` es atributo), **§3.8** (resolución del SPS), **§6.4** (política de
descarte), **§6.5** (reconexión y cierre ordenado), **§6.6** (métricas) y **§16.2** (el
timeout de 5 s de `Stream.Write`).

**Ledgers de las fases anteriores**, con las notas de arrastre que esta fase paga:
`docs/superpowers/plans/2026-09-01-fase-1-ledger.md` y
`docs/superpowers/plans/2026-09-02-fase-2-ledger.md`.

## Global Constraints

- Módulo Go: `github.com/aprendomx/splitstream`. Piso `go 1.25.0` en `go.mod`.
- **NUNCA ejecutes `go mod tidy`.** Recorre las dependencias de test de
  `modernc.org/sqlite`, que arrastra `libc → ccgo → gc/v2 → gc/v3`. Tarda minutos y ha
  matado tres agentes. Esta fase **no añade dependencias**.
- Dependencias directas, exactamente tres y ninguna más: `modernc.org/sqlite`,
  `golang.org/x/crypto`, `github.com/yutopp/go-rtmp`.
- `CGO_ENABLED=0` en el **build**. Los **tests** corren con cgo, porque `-race` lo exige.
- **`internal/relay` no importa `go-rtmp` ni `database/sql`.** Verifícalo con
  `go list -deps ./internal/relay | grep -E 'go-rtmp|database/sql'`, que debe salir vacío.
- **Ejecuta los tests de esta fase siempre con `-race`.** Es la fase más concurrente del
  proyecto; sin el detector no verificas nada.
- El descarte es **por GOP completo**, nunca por frame suelto. Descartar P-frames corrompe
  la decodificación hasta el siguiente IDR (spec §3.3).
- La cola se acota por **bytes y duración**, no por número de mensajes (spec §3.4).
- `degraded` es un **atributo booleano**, no un estado. Estados:
  `idle | connecting | live | reconnecting | error` (spec §3.7).
- El backoff va de **1 s a 30 s**, exponencial, con **jitter ±20%** (spec §6.5).
- Un `Publisher` se usa **desde una sola goroutine**, `Close` incluido. Lo dice su interfaz
  y la razón es que el chunk streamer de go-rtmp comparte un encoder sin mutex.
- Ningún secreto en logs ni en errores. Las stream keys viajan como `crypto.Secret`.
- Comentarios y mensajes de error en español.
- Todo acceso a la base con métodos `...Context`, y por `d.ex`, nunca por `d.db`.

## Fuera de alcance

Va a las fases 4 a 6: la API HTTP, el WebSocket, el frontend, Docker y el README. Esta fase
deja el motor completo y sus métricas **en memoria**, listas para que la fase 4 las sirva.

## Notas de arrastre que esta fase paga

| Origen | Qué |
| --- | --- |
| Fase 1 §15.7 / Fase 2 T1 | Ningún test lanza goroutines contra el store: `-race` no verifica nada ahí. Task 6. |
| Fase 2 T4 | Wraparound de timestamps de 32 bits (~24,8 días). Task 3, decidido explícitamente. |
| Fase 2 T7 | `Stream.Publish` es fire-and-forget: una clave rechazada parece éxito. Task 4. |
| Fase 2 final | Falta `FCUnpublish` en el cierre ordenado (§6.5). Task 5. |
| Fase 2 final | Los sinks no tienen los 3 s de gracia del §6.5. Task 7. |
| Fase 2 final | `Hub.Add` con un sink nunca arrancado se cuelga. Task 5. |
| Fase 2 final | Carrera en los `defer` del test de integración. Task 8. |
| Fase 1 §15.2 / Spec §3.8 | Resolución del SPS y bitrate medido. Tasks 2 y 6. |

## Estructura de archivos de esta fase

| Archivo | Responsabilidad |
| --- | --- |
| `internal/relay/queue.go` | Deque acotado por bytes y duración, con descarte por GOP |
| `internal/relay/backoff.go` | Backoff exponencial 1 s→30 s con jitter ±20% |
| `internal/relay/metrics.go` | Métricas por destino y bitrate por media móvil de 5 s |
| `internal/relay/sink.go` (reescribir) | Bucle de reconexión, estados, `degraded` |
| `internal/relay/hub.go` (modificar) | `Add` que no se cuelga; `Snapshot()` de métricas |
| `internal/relay/engine.go` (modificar) | Eventos por destino; resolución y bitrate al cerrar |
| `internal/flv/sps.go` | Parseo del SPS: resolución del AVC sequence header |
| `internal/store/concurrency_test.go` | El test de contención que la fase 1 pidió |
| `internal/rtmpio/publisher.go` (modificar) | `FCUnpublish` en el cierre ordenado |
| `cmd/splitstream/main.go` (modificar) | N destinos, gracia de apagado |
| `test/integration/fanout_test.go` | Fan-out a dos sinks y test de reconexión |

---

### Task 1: Cola acotada con descarte por GOP

El corazón del backpressure. Sustituye el canal simple de la fase 2 por un deque que se
acota por **bytes y duración de media**, y que al desbordar descarta **GOPs completos**.

**Files:**
- Create: `internal/relay/queue.go`
- Test: `internal/relay/queue_test.go`

**Interfaces:**
- Consumes: `Message`, `Kind`, `KindAudio`, `KindVideo`, `KindMeta` de la fase 2.
- Produces:
  ```go
  const (
      DefaultMaxBytes      = 16 << 20 // 16 MiB
      DefaultMaxSpanMillis = 3000     // 3 s de media encolada
  )
  type queueConfig struct {
      MaxBytes int
      MaxSpan  uint32 // milisegundos
  }
  func newQueue(cfg queueConfig) *queue
  func (q *queue) push(msg *Message)
  func (q *queue) pop(ctx context.Context) (*Message, bool)
  func (q *queue) close()
  func (q *queue) dropped() uint64
  func (q *queue) droppingVideo() bool
  func (q *queue) stats() (items int, bytes int, spanMillis uint32)
  ```
  Todo no exportado: solo lo usa `sink.go` del mismo paquete.

- [ ] **Step 1: Escribir el test que falla**

`internal/relay/queue_test.go`:

```go
package relay

import (
	"context"
	"sync"
	"testing"
	"time"
)

func vKey(ts uint32, size int) *Message {
	return &Message{Kind: KindVideo, Timestamp: ts, Payload: make([]byte, size), IsKeyframe: true}
}
func vInter(ts uint32, size int) *Message {
	return &Message{Kind: KindVideo, Timestamp: ts, Payload: make([]byte, size)}
}
func aRaw(ts uint32, size int) *Message {
	return &Message{Kind: KindAudio, Timestamp: ts, Payload: make([]byte, size)}
}
func vSeq() *Message {
	return &Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true}
}
func meta() *Message {
	return &Message{Kind: KindMeta, Payload: []byte{0xFF}}
}

func drain(t *testing.T, q *queue) []*Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var out []*Message
	for {
		q.mu.Lock()
		empty := len(q.items) == 0
		q.mu.Unlock()
		if empty {
			return out
		}
		m, ok := q.pop(ctx)
		if !ok {
			return out
		}
		out = append(out, m)
	}
}

func TestQueueFIFOWhenUnderLimits(t *testing.T) {
	q := newQueue(queueConfig{})
	defer q.close()

	q.push(vKey(0, 10))
	q.push(aRaw(10, 5))
	q.push(vInter(33, 10))

	got := drain(t, q)
	if len(got) != 3 {
		t.Fatalf("len = %d, quería 3", len(got))
	}
	if got[0].Timestamp != 0 || got[1].Timestamp != 10 || got[2].Timestamp != 33 {
		t.Errorf("orden alterado: %d %d %d", got[0].Timestamp, got[1].Timestamp, got[2].Timestamp)
	}
	if q.dropped() != 0 {
		t.Errorf("dropped = %d, quería 0", q.dropped())
	}
}

// Al desbordar por bytes se tira TODO el vídeo encolado, no un frame suelto (spec §3.3).
func TestQueueDropsWholeGOPOnByteOverflow(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 100, MaxSpan: 1_000_000})
	defer q.close()

	q.push(meta())
	q.push(vSeq())
	q.push(vKey(0, 40))
	q.push(aRaw(10, 5))
	q.push(vInter(33, 40))
	q.push(vInter(66, 40)) // aquí desborda

	if !q.droppingVideo() {
		t.Fatal("la cola debería estar descartando vídeo tras desbordar")
	}

	got := drain(t, q)
	for _, m := range got {
		if m.Kind == KindVideo && !m.IsSeqHeader {
			t.Errorf("quedó vídeo encolado tras el desbordamiento: ts=%d", m.Timestamp)
		}
	}
	// El audio, la metadata y los sequence headers sobreviven.
	var nAudio, nMeta, nSeq int
	for _, m := range got {
		switch {
		case m.IsSeqHeader:
			nSeq++
		case m.Kind == KindAudio:
			nAudio++
		case m.Kind == KindMeta:
			nMeta++
		}
	}
	if nAudio != 1 || nMeta != 1 || nSeq != 1 {
		t.Errorf("audio=%d meta=%d seq=%d, quería 1 1 1", nAudio, nMeta, nSeq)
	}
	if q.dropped() == 0 {
		t.Error("el contador de descartes no subió")
	}
}

// También desborda por duración, aunque quepa en bytes.
func TestQueueDropsOnSpanOverflow(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 1 << 30, MaxSpan: 1000})
	defer q.close()

	q.push(vKey(0, 1))
	q.push(vInter(500, 1))
	q.push(vInter(2000, 1)) // 2 s de span > 1 s

	if !q.droppingVideo() {
		t.Fatal("la cola debería descartar al superar la duración máxima")
	}
}

// En modo descarte, los inter frames se tiran y el siguiente keyframe resincroniza.
func TestQueueResyncsOnNextKeyframe(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 50, MaxSpan: 1_000_000})
	defer q.close()

	q.push(vKey(0, 30))
	q.push(vInter(33, 30)) // desborda
	if !q.droppingVideo() {
		t.Fatal("debería estar descartando")
	}

	q.push(vInter(66, 1)) // se tira: no es keyframe
	q.push(vInter(99, 1)) // se tira
	q.push(vKey(132, 1))  // resincroniza aquí

	if q.droppingVideo() {
		t.Error("un keyframe debe sacar a la cola del modo descarte")
	}
	got := drain(t, q)
	var lastVideo *Message
	for _, m := range got {
		if m.Kind == KindVideo && !m.IsSeqHeader {
			lastVideo = m
		}
	}
	if lastVideo == nil || lastVideo.Timestamp != 132 {
		t.Errorf("el vídeo que sobrevive debe ser el keyframe de resincronización, fue %v", lastVideo)
	}
}

// El audio nunca se descarta: es barato y su corte se nota mucho más que un salto de vídeo.
func TestQueueNeverDropsAudio(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 20, MaxSpan: 1_000_000})
	defer q.close()

	for i := 0; i < 50; i++ {
		q.push(aRaw(uint32(i*20), 10))
	}
	got := drain(t, q)
	if len(got) != 50 {
		t.Errorf("sobrevivieron %d mensajes de audio de 50: el audio no debe descartarse", len(got))
	}
}

// Los sequence headers y la metadata nunca se descartan: sin ellos no se decodifica nada.
func TestQueueNeverDropsEssentials(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 10, MaxSpan: 1_000_000})
	defer q.close()

	q.push(meta())
	q.push(vSeq())
	q.push(vKey(0, 100)) // desborda de sobra
	q.push(vInter(33, 100))

	got := drain(t, q)
	var seenMeta, seenSeq bool
	for _, m := range got {
		if m.Kind == KindMeta {
			seenMeta = true
		}
		if m.IsSeqHeader {
			seenSeq = true
		}
	}
	if !seenMeta || !seenSeq {
		t.Errorf("meta=%v seq=%v: los esenciales nunca se descartan", seenMeta, seenSeq)
	}
}

func TestQueuePopBlocksUntilPush(t *testing.T) {
	q := newQueue(queueConfig{})
	defer q.close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan *Message, 1)
	go func() {
		m, ok := q.pop(ctx)
		if ok {
			done <- m
		} else {
			close(done)
		}
	}()

	time.Sleep(50 * time.Millisecond)
	q.push(vKey(42, 1))

	select {
	case m := <-done:
		if m == nil || m.Timestamp != 42 {
			t.Errorf("pop devolvió %v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pop no despertó tras el push")
	}
}

func TestQueuePopRespectsContext(t *testing.T) {
	q := newQueue(queueConfig{})
	defer q.close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, ok := q.pop(ctx); ok {
		t.Fatal("pop debería fallar al vencer el contexto")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("pop tardó %v en rendirse", d)
	}
}

func TestQueueCloseUnblocksPop(t *testing.T) {
	q := newQueue(queueConfig{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		q.pop(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)
	q.close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("close no despertó a pop")
	}
}

func TestQueuePushAfterCloseIsSafe(t *testing.T) {
	q := newQueue(queueConfig{})
	q.close()
	q.push(vKey(0, 1)) // no debe entrar en pánico
	q.close()          // idempotente
}

// push desde varias goroutines mientras otra hace pop, bajo -race.
func TestQueueConcurrentPushPop(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 4096, MaxSpan: 1_000_000})
	defer q.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if i%10 == 0 {
					q.push(vKey(uint32(i*33), 64))
				} else {
					q.push(vInter(uint32(i*33), 64))
				}
			}
		}(g)
	}

	consumed := make(chan int, 1)
	go func() {
		n := 0
		for {
			if _, ok := q.pop(ctx); !ok {
				consumed <- n
				return
			}
			n++
		}
	}()

	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-consumed // no se comprueba el número: con descarte, es no determinista
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/relay/ -run Queue -v`
Expected: FAIL con `undefined: newQueue`.

- [ ] **Step 3: Implementar `internal/relay/queue.go`**

```go
package relay

import (
	"context"
	"sync"
)

// Límites por defecto de la cola de un sink.
//
// Se acota por bytes y por duración de media, no por número de mensajes: 512 mensajes son
// 0,3 s a 8 Mbps o 20 s a 500 kbps, así que ese número no permite razonar ni sobre la
// latencia ni sobre la RAM (spec §3.4).
const (
	DefaultMaxBytes      = 16 << 20 // 16 MiB
	DefaultMaxSpanMillis = 3000     // 3 s de media encolada

	// DefaultMaxItems es una cota dura de mensajes, red de seguridad y no mecanismo
	// principal. Existe porque tirar todo el vídeo no siempre basta: un destino caído
	// mientras la transmisión continúa acumula audio, que no se descarta, y el sink deja
	// de drenar la cola mientras espera el backoff. Sin esta cota la cola crece sin tope.
	DefaultMaxItems = 8192
)

type queueConfig struct {
	MaxBytes int
	MaxSpan  uint32 // milisegundos
	MaxItems int
}

// queue es la cola de un sink: un deque acotado con política de descarte por GOP.
//
// No es un canal porque la decisión de descarte necesita inspeccionar lo ya encolado:
// al desbordar hay que tirar todo el vídeo pendiente, no solo rechazar el que llega.
type queue struct {
	mu         sync.Mutex
	signal     chan struct{}
	items      []*Message
	bytes      int
	videoItems int // mensajes de vídeo descartables encolados
	maxBytes   int
	maxSpan    uint32
	maxItems   int
	dropping   bool
	drops      uint64
	closed     bool
}

func newQueue(cfg queueConfig) *queue {
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	maxSpan := cfg.MaxSpan
	if maxSpan == 0 {
		maxSpan = DefaultMaxSpanMillis
	}
	maxItems := cfg.MaxItems
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}
	return &queue{
		signal:   make(chan struct{}, 1),
		maxBytes: maxBytes,
		maxSpan:  maxSpan,
		maxItems: maxItems,
	}
}

// essential indica si un mensaje no se puede descartar nunca. Sin el onMetaData y los dos
// sequence headers, el destino no puede decodificar nada de lo que venga después.
func essential(msg *Message) bool {
	return msg.Kind == KindMeta || msg.IsSeqHeader
}

// push encola un mensaje aplicando la política de descarte. Nunca bloquea.
func (q *queue) push(msg *Message) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return
	}

	droppableVideo := msg.Kind == KindVideo && !essential(msg)

	// En modo descarte solo un keyframe reanuda el vídeo. Descartar frames sueltos
	// corrompería la decodificación hasta el siguiente IDR, que es peor que un salto
	// limpio (spec §3.3).
	if q.dropping && droppableVideo {
		if !msg.IsKeyframe {
			q.drops++
			return
		}
		q.dropping = false
	}

	q.items = append(q.items, msg)
	q.bytes += len(msg.Payload)
	if droppableVideo {
		q.videoItems++
	}

	if q.over() {
		// Primero el sacrificio barato: todo el vídeo pendiente. El guardia sobre
		// videoItems evita reescanear la cola entera en cada push cuando la saturación
		// la causa el audio y no hay vídeo que tirar.
		if q.videoItems > 0 {
			q.dropQueuedVideo()
		}
		q.dropping = true
	}

	// Red de seguridad. Perder audio que de todos modos llegaría tarde es mejor que una
	// cola sin tope.
	q.shedToItemCap()

	q.wake()
}

// shedToItemCap tira los mensajes no esenciales más antiguos hasta bajar de la cota dura,
// en una sola pasada y solo cuando la cota se supera de verdad.
func (q *queue) shedToItemCap() {
	excess := len(q.items) - q.maxItems
	if excess <= 0 {
		return
	}

	kept := q.items[:0]
	for _, m := range q.items {
		if excess > 0 && !essential(m) {
			q.drops++
			q.bytes -= len(m.Payload)
			if m.Kind == KindVideo {
				q.videoItems--
			}
			excess--
			continue
		}
		kept = append(kept, m)
	}
	for i := len(kept); i < len(q.items); i++ {
		q.items[i] = nil
	}
	q.items = kept
}

// over indica si la cola supera alguno de sus dos límites.
func (q *queue) over() bool {
	return q.bytes > q.maxBytes || q.spanMillis() > q.maxSpan
}

// spanMillis es la duración de media encolada, del primer mensaje al último.
//
// Si el último timestamp es menor que el primero —reordenamiento o wraparound del contador
// de 32 bits de RTMP, que ocurre a los ~24,8 días de sesión continua— se devuelve 0: es
// preferible no descartar por una medida sin sentido que descartar de más.
func (q *queue) spanMillis() uint32 {
	if len(q.items) < 2 {
		return 0
	}
	first := q.items[0].Timestamp
	last := q.items[len(q.items)-1].Timestamp
	if last < first {
		return 0
	}
	return last - first
}

// dropQueuedVideo tira todo el vídeo pendiente y entra en modo descarte hasta el siguiente
// keyframe. Conserva el audio, la metadata y los sequence headers: el audio es barato y su
// corte se nota mucho más que un salto de vídeo (spec §6.4).
func (q *queue) dropQueuedVideo() {
	kept := q.items[:0]
	for _, m := range q.items {
		if m.Kind == KindVideo && !essential(m) {
			q.drops++
			q.bytes -= len(m.Payload)
			continue
		}
		kept = append(kept, m)
	}
	// Anular las posiciones sobrantes para no retener los payloads descartados.
	for i := len(kept); i < len(q.items); i++ {
		q.items[i] = nil
	}
	q.items = kept
	q.videoItems = 0
}

// wake avisa a pop de que hay algo. El canal tiene capacidad 1 y el envío es no
// bloqueante: una señal pendiente basta.
func (q *queue) wake() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// pop saca el mensaje más antiguo, bloqueando hasta que haya uno, se cierre la cola o
// venza el contexto. El segundo valor es false en los dos últimos casos.
func (q *queue) pop(ctx context.Context) (*Message, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			m := q.items[0]
			q.items[0] = nil
			q.items = q.items[1:]
			q.bytes -= len(m.Payload)
			if m.Kind == KindVideo && !essential(m) {
				q.videoItems--
			}
			q.mu.Unlock()
			return m, true
		}
		closed := q.closed
		q.mu.Unlock()

		if closed {
			return nil, false
		}

		select {
		case <-ctx.Done():
			return nil, false
		case <-q.signal:
		}
	}
}

// close vacía la cola y despierta a quien esté esperando. Es idempotente.
func (q *queue) close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	q.items = nil
	q.bytes = 0
	q.videoItems = 0
	q.mu.Unlock()

	q.wake()
}

// dropped devuelve cuántos mensajes de vídeo se han descartado.
func (q *queue) dropped() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.drops
}

// droppingVideo indica si la cola está descartando vídeo a la espera de un keyframe.
func (q *queue) droppingVideo() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropping
}

// stats devuelve el estado de ocupación, para métricas y diagnóstico.
func (q *queue) stats() (items int, bytes int, spanMillis uint32) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items), q.bytes, q.spanMillis()
}
```

Nota sobre `q.items = q.items[1:]` en `pop`: reutiliza el array subyacente, así que la
cabeza no se recupera hasta que `append` reasigna. En régimen estable el array crece a unas
dos veces el tamaño máximo y se reasigna; es aceptable y evita copiar en cada `pop`. La
posición liberada se pone a `nil` para no retener el payload.

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/relay/ -run Queue -race -count=5 -v`
Expected: PASS en los 11 tests, cinco veces seguidas.

- [ ] **Step 5: Commit**

```bash
git add internal/relay/queue.go internal/relay/queue_test.go
git commit -m "feat(relay): cola acotada por bytes y duración con descarte por GOP"
```

---

### Task 2: Parseo del SPS para la resolución

El spec §3.8 dice que la resolución sale del SPS y no del `onMetaData`, que es declarativo y
puede mentir. Es manipulación de bits con codificación exp-Golomb.

**Files:**
- Create: `internal/flv/sps.go`
- Test: `internal/flv/sps_test.go`

**Interfaces:**
- Consumes: nada. `internal/flv` no importa nada del proyecto.
- Produces:
  ```go
  var ErrNotAVCSequenceHeader = errors.New("no es un AVC sequence header")
  var ErrMalformedSPS = errors.New("SPS malformado")
  func ParseResolution(avcSeqHeader []byte) (width, height int, err error)
  ```

- [ ] **Step 1: Escribir el test que falla**

`internal/flv/sps_test.go`:

```go
package flv_test

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/flv"
)

// avcSeqHeader construye un tag de vídeo con un AVCDecoderConfigurationRecord que
// contiene el SPS dado.
//
// Formato del tag: [0]=0x17 (keyframe|AVC), [1]=0x00 (sequence header), [2..4]=composition
// time. Luego el AVCDecoderConfigurationRecord: version, profile, compat, level,
// 0xFF (lengthSizeMinusOne), 0xE1 (numOfSPS=1), 2 bytes de longitud del SPS, y el SPS.
func avcSeqHeader(sps []byte) []byte {
	out := []byte{0x17, 0x00, 0, 0, 0, 0x01, sps[1], sps[2], sps[3], 0xFF, 0xE1}
	out = append(out, byte(len(sps)>>8), byte(len(sps)))
	return append(out, sps...)
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex: %v", err)
	}
	return b
}

// SPS real de 640x360, generado con:
//
//	ffmpeg -f lavfi -i testsrc2=size=640x360:rate=30 -t 1 -c:v libx264 -f flv out.flv
func TestParseResolution640x360(t *testing.T) {
	sps := mustHex(t, "6764001eacd940a02ff9610000030001000003003c8f162d96")
	w, h, err := flv.ParseResolution(avcSeqHeader(sps))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if w != 640 || h != 360 {
		t.Errorf("resolución = %dx%d, quería 640x360", w, h)
	}
}

// SPS real de 1920x1080.
func TestParseResolution1920x1080(t *testing.T) {
	sps := mustHex(t, "67640028acd100780227e5c05a808080a0000003002000000791e30632c0")
	w, h, err := flv.ParseResolution(avcSeqHeader(sps))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if w != 1920 || h != 1080 {
		t.Errorf("resolución = %dx%d, quería 1920x1080", w, h)
	}
}

func TestParseResolutionRejectsNonSequenceHeader(t *testing.T) {
	// AVCPacketType 1 = NALU, no sequence header.
	if _, _, err := flv.ParseResolution([]byte{0x17, 0x01, 0, 0, 0}); !errors.Is(err, flv.ErrNotAVCSequenceHeader) {
		t.Fatalf("err = %v, quería ErrNotAVCSequenceHeader", err)
	}
}

func TestParseResolutionRejectsNonAVC(t *testing.T) {
	// codecID 2 = Sorenson H.263.
	if _, _, err := flv.ParseResolution([]byte{0x12, 0x00, 0, 0, 0}); !errors.Is(err, flv.ErrNotAVCSequenceHeader) {
		t.Fatalf("err = %v, quería ErrNotAVCSequenceHeader", err)
	}
}

func TestParseResolutionRejectsTruncated(t *testing.T) {
	for _, bad := range [][]byte{
		{},
		{0x17},
		{0x17, 0x00},
		{0x17, 0x00, 0, 0, 0},             // sin AVCDecoderConfigurationRecord
		{0x17, 0x00, 0, 0, 0, 1, 0, 0, 0}, // record truncado
	} {
		if _, _, err := flv.ParseResolution(bad); err == nil {
			t.Errorf("ParseResolution(%v) = nil, quería error", bad)
		}
	}
}

// Un SPS cuyo contenido no se puede decodificar debe dar error, no una resolución absurda.
func TestParseResolutionRejectsGarbageSPS(t *testing.T) {
	garbage := []byte{0x67, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	_, _, err := flv.ParseResolution(avcSeqHeader(garbage))
	if err == nil {
		t.Fatal("un SPS ilegible debe dar error")
	}
}
```

**Antes de implementar, verifica los dos SPS de los tests.** Genera un FLV real con ffmpeg,
extrae el AVC sequence header y compáralo. Si los bytes hexadecimales de arriba no
corresponden a 640x360 y 1920x1080, **sustitúyelos por los reales y anótalo en tu informe**;
un test con datos inventados no prueba nada. Comando para obtenerlos:

```bash
ffmpeg -y -loglevel error -f lavfi -i testsrc2=size=640x360:rate=30 -t 1 \
  -c:v libx264 -preset ultrafast -pix_fmt yuv420p -f flv /tmp/probe.flv
ffprobe -v error -select_streams v:0 -show_entries stream=extradata \
  -of default=noprint_wrappers=1 /tmp/probe.flv
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/flv/ -run Resolution -v`
Expected: FAIL con `undefined: flv.ParseResolution`.

- [ ] **Step 3: Implementar `internal/flv/sps.go`**

```go
package flv

import "errors"

// ErrNotAVCSequenceHeader indica que el tag no es un AVC sequence header.
var ErrNotAVCSequenceHeader = errors.New("no es un AVC sequence header")

// ErrMalformedSPS indica que el SPS no se pudo decodificar.
var ErrMalformedSPS = errors.New("SPS malformado")

// ParseResolution extrae el ancho y el alto del SPS que viaja dentro de un AVC sequence
// header.
//
// Se prefiere al onMetaData porque este es declarativo y puede mentir: OBS lo suele mandar
// bien, pero nada obliga a que coincida con lo que realmente codifica (spec §3.8).
func ParseResolution(tag []byte) (int, int, error) {
	sps, err := extractSPS(tag)
	if err != nil {
		return 0, 0, err
	}
	return parseSPS(sps)
}

// extractSPS saca el primer SPS del AVCDecoderConfigurationRecord.
//
// Disposición del tag: 1 byte de frameType|codecID, 1 de AVCPacketType, 3 de composition
// time, y luego el record: 1 de versión, 3 de perfil/compat/nivel, 1 con lengthSizeMinusOne,
// 1 con numOfSPS en sus 5 bits bajos, 2 con la longitud del SPS, y el SPS.
func extractSPS(tag []byte) ([]byte, error) {
	const headerLen = 5 // frameType|codecID + AVCPacketType + composition time

	if len(tag) < headerLen {
		return nil, ErrNotAVCSequenceHeader
	}
	if tag[0]&0x80 != 0 || tag[0]&0x0f != CodecIDAVC {
		return nil, ErrNotAVCSequenceHeader
	}
	if tag[1] != 0x00 {
		return nil, ErrNotAVCSequenceHeader
	}

	record := tag[headerLen:]
	// versión + 3 de perfil + lengthSize + numOfSPS + 2 de longitud = 8 como mínimo.
	if len(record) < 8 {
		return nil, ErrMalformedSPS
	}
	if record[5]&0x1f == 0 {
		return nil, ErrMalformedSPS
	}

	spsLen := int(record[6])<<8 | int(record[7])
	if spsLen == 0 || len(record) < 8+spsLen {
		return nil, ErrMalformedSPS
	}
	return record[8 : 8+spsLen], nil
}

// bitReader lee bits de izquierda a derecha, que es como se codifica H.264.
type bitReader struct {
	data []byte
	pos  int // posición en bits
}

func (r *bitReader) bit() (uint, error) {
	if r.pos >= len(r.data)*8 {
		return 0, ErrMalformedSPS
	}
	b := r.data[r.pos/8]
	shift := 7 - uint(r.pos%8)
	r.pos++
	return uint(b>>shift) & 1, nil
}

func (r *bitReader) bits(n int) (uint, error) {
	var out uint
	for i := 0; i < n; i++ {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		out = out<<1 | b
	}
	return out, nil
}

// ue lee un entero sin signo en código exp-Golomb, que es como H.264 codifica casi todos
// sus campos: N ceros, un uno, y luego N bits de resto.
func (r *bitReader) ue() (uint, error) {
	zeros := 0
	for {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		if b == 1 {
			break
		}
		zeros++
		// Un prefijo de más de 32 ceros no es un valor legítimo: es basura o un bucle.
		if zeros > 32 {
			return 0, ErrMalformedSPS
		}
	}
	if zeros == 0 {
		return 0, nil
	}
	rest, err := r.bits(zeros)
	if err != nil {
		return 0, err
	}
	return (1 << uint(zeros)) - 1 + rest, nil
}

// se lee un entero con signo en código exp-Golomb.
func (r *bitReader) se() (int, error) {
	v, err := r.ue()
	if err != nil {
		return 0, err
	}
	if v%2 == 0 {
		return -int(v / 2), nil
	}
	return int((v + 1) / 2), nil
}

// removeEmulationPrevention quita los bytes 0x03 que H.264 inserta para que no aparezcan
// secuencias 0x000001 dentro del payload. Sin quitarlos, el lector de bits se desalinea.
func removeEmulationPrevention(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if i >= 2 && i+1 <= len(b) && b[i] == 0x03 && b[i-1] == 0x00 && b[i-2] == 0x00 {
			continue
		}
		out = append(out, b[i])
	}
	return out
}

// parseSPS decodifica el SPS hasta los campos que dan la resolución.
func parseSPS(sps []byte) (int, int, error) {
	if len(sps) < 4 {
		return 0, 0, ErrMalformedSPS
	}

	// sps[0] es la cabecera NAL; el resto es el RBSP.
	r := &bitReader{data: removeEmulationPrevention(sps[1:])}

	profileIDC, err := r.bits(8)
	if err != nil {
		return 0, 0, err
	}
	if _, err := r.bits(8); err != nil { // constraint flags + reserved
		return 0, 0, err
	}
	if _, err := r.bits(8); err != nil { // level_idc
		return 0, 0, err
	}
	if _, err := r.ue(); err != nil { // seq_parameter_set_id
		return 0, 0, err
	}

	chromaFormatIDC := uint(1) // 4:2:0 por defecto
	switch profileIDC {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chromaFormatIDC, err = r.ue()
		if err != nil {
			return 0, 0, err
		}
		if chromaFormatIDC == 3 {
			if _, err := r.bit(); err != nil { // separate_colour_plane_flag
				return 0, 0, err
			}
		}
		if _, err := r.ue(); err != nil { // bit_depth_luma_minus8
			return 0, 0, err
		}
		if _, err := r.ue(); err != nil { // bit_depth_chroma_minus8
			return 0, 0, err
		}
		if _, err := r.bit(); err != nil { // qpprime_y_zero_transform_bypass_flag
			return 0, 0, err
		}
		seqScalingMatrix, err := r.bit()
		if err != nil {
			return 0, 0, err
		}
		if seqScalingMatrix == 1 {
			lists := 8
			if chromaFormatIDC == 3 {
				lists = 12
			}
			for i := 0; i < lists; i++ {
				present, err := r.bit()
				if err != nil {
					return 0, 0, err
				}
				if present == 0 {
					continue
				}
				size := 16
				if i >= 6 {
					size = 64
				}
				last, next := 8, 8
				for j := 0; j < size; j++ {
					if next != 0 {
						delta, err := r.se()
						if err != nil {
							return 0, 0, err
						}
						next = (last + delta + 256) % 256
					}
					if next != 0 {
						last = next
					}
				}
			}
		}
	}

	if _, err := r.ue(); err != nil { // log2_max_frame_num_minus4
		return 0, 0, err
	}
	picOrderCntType, err := r.ue()
	if err != nil {
		return 0, 0, err
	}
	switch picOrderCntType {
	case 0:
		if _, err := r.ue(); err != nil { // log2_max_pic_order_cnt_lsb_minus4
			return 0, 0, err
		}
	case 1:
		if _, err := r.bit(); err != nil { // delta_pic_order_always_zero_flag
			return 0, 0, err
		}
		if _, err := r.se(); err != nil { // offset_for_non_ref_pic
			return 0, 0, err
		}
		if _, err := r.se(); err != nil { // offset_for_top_to_bottom_field
			return 0, 0, err
		}
		n, err := r.ue()
		if err != nil {
			return 0, 0, err
		}
		if n > 256 {
			return 0, 0, ErrMalformedSPS
		}
		for i := uint(0); i < n; i++ {
			if _, err := r.se(); err != nil {
				return 0, 0, err
			}
		}
	}

	if _, err := r.ue(); err != nil { // max_num_ref_frames
		return 0, 0, err
	}
	if _, err := r.bit(); err != nil { // gaps_in_frame_num_value_allowed_flag
		return 0, 0, err
	}

	widthInMbsMinus1, err := r.ue()
	if err != nil {
		return 0, 0, err
	}
	heightInMapUnitsMinus1, err := r.ue()
	if err != nil {
		return 0, 0, err
	}
	frameMbsOnly, err := r.bit()
	if err != nil {
		return 0, 0, err
	}
	if frameMbsOnly == 0 {
		if _, err := r.bit(); err != nil { // mb_adaptive_frame_field_flag
			return 0, 0, err
		}
	}
	if _, err := r.bit(); err != nil { // direct_8x8_inference_flag
		return 0, 0, err
	}

	width := int(widthInMbsMinus1+1) * 16
	height := int(heightInMapUnitsMinus1+1) * 16
	if frameMbsOnly == 0 {
		height *= 2
	}

	// El recorte quita las filas y columnas que se codificaron solo para completar
	// macrobloques de 16x16: sin él, 1080 se lee como 1088.
	cropping, err := r.bit()
	if err != nil {
		return 0, 0, err
	}
	if cropping == 1 {
		left, err := r.ue()
		if err != nil {
			return 0, 0, err
		}
		right, err := r.ue()
		if err != nil {
			return 0, 0, err
		}
		top, err := r.ue()
		if err != nil {
			return 0, 0, err
		}
		bottom, err := r.ue()
		if err != nil {
			return 0, 0, err
		}

		subWidth, subHeight := 2, 2
		switch chromaFormatIDC {
		case 0: // monocromo
			subWidth, subHeight = 1, 1
		case 2: // 4:2:2
			subHeight = 1
		case 3: // 4:4:4
			subWidth, subHeight = 1, 1
		}
		if frameMbsOnly == 0 {
			subHeight *= 2
		}

		width -= int(left+right) * subWidth
		height -= int(top+bottom) * subHeight
	}

	if width <= 0 || height <= 0 || width > 16384 || height > 16384 {
		return 0, 0, ErrMalformedSPS
	}
	return width, height, nil
}
```

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/flv/ -race -count=1 -v`
Expected: PASS en los 16 tests del paquete (los 10 de la fase 2 más los 6 nuevos).

Si los tests de resolución fallan, **lo más probable es que los bytes del SPS del test no
sean reales**. Genera uno con el comando ffmpeg del Step 1 y compara antes de tocar el
parser.

- [ ] **Step 5: Commit**

```bash
git add internal/flv/sps.go internal/flv/sps_test.go
git commit -m "feat(flv): resolución desde el SPS del AVC sequence header"
```

---

### Task 3: Backoff y métricas

Las dos piezas puras que el sink necesita para reconectar y para reportar.

**Files:**
- Create: `internal/relay/backoff.go`
- Create: `internal/relay/metrics.go`
- Test: `internal/relay/backoff_test.go`
- Test: `internal/relay/metrics_test.go`

**Interfaces:**
- Consumes: nada del proyecto.
- Produces:
  ```go
  const (
      BackoffMin = time.Second
      BackoffMax = 30 * time.Second
  )
  type backoff struct{ ... }
  func newBackoff(seed int64) *backoff
  func (b *backoff) next() time.Duration
  func (b *backoff) reset()
  func (b *backoff) attempts() int

  // Metrics es la instantánea pública de un destino. La fase 4 la sirve por WebSocket.
  type Metrics struct {
      State          string
      Degraded       bool
      BytesSent      uint64
      BitrateBPS     uint64
      DroppedFrames  uint64
      Uptime         time.Duration
      Reconnections  uint64
      LastError      string
      QueuedBytes    int
      QueuedMessages int
  }
  type metrics struct{ ... }
  func newMetrics(now func() time.Time) *metrics
  func (m *metrics) connected()
  func (m *metrics) disconnected()
  func (m *metrics) sent(n int)
  func (m *metrics) markDegraded()
  func (m *metrics) setError(err error)
  func (m *metrics) snapshot(state State, dropped uint64, qMsgs, qBytes int) Metrics
  ```

- [ ] **Step 1: Escribir los tests que fallan**

`internal/relay/backoff_test.go`:

```go
package relay

import (
	"testing"
	"time"
)

func TestBackoffGrowsExponentiallyAndCaps(t *testing.T) {
	b := newBackoff(1)

	var last time.Duration
	for i := 0; i < 12; i++ {
		d := b.next()
		if d < BackoffMin*8/10 {
			t.Fatalf("intento %d: %v está por debajo del mínimo con jitter", i, d)
		}
		if d > BackoffMax*12/10 {
			t.Fatalf("intento %d: %v supera el máximo con jitter", i, d)
		}
		last = d
	}
	// Tras 12 intentos debe estar pegado al techo.
	if last < BackoffMax*7/10 {
		t.Errorf("tras 12 intentos la espera es %v: debería estar cerca de %v", last, BackoffMax)
	}
}

func TestBackoffFirstAttemptIsAroundMin(t *testing.T) {
	b := newBackoff(1)
	d := b.next()
	if d < BackoffMin*8/10 || d > BackoffMin*12/10 {
		t.Errorf("primera espera = %v, quería ~%v con jitter de ±20%%", d, BackoffMin)
	}
}

func TestBackoffJitterVaries(t *testing.T) {
	// Dos backoffs con semillas distintas no deben dar la misma secuencia: si varios
	// destinos se caen a la vez, no pueden reintentar en sincronía (spec §6.5).
	a, b := newBackoff(1), newBackoff(2)
	same := 0
	for i := 0; i < 8; i++ {
		if a.next() == b.next() {
			same++
		}
	}
	if same == 8 {
		t.Error("dos backoffs con semillas distintas dieron la misma secuencia entera")
	}
}

func TestBackoffReset(t *testing.T) {
	b := newBackoff(1)
	for i := 0; i < 6; i++ {
		b.next()
	}
	if b.attempts() != 6 {
		t.Errorf("attempts = %d, quería 6", b.attempts())
	}

	b.reset()
	if b.attempts() != 0 {
		t.Errorf("attempts tras reset = %d, quería 0", b.attempts())
	}
	if d := b.next(); d > BackoffMin*12/10 {
		t.Errorf("tras reset la primera espera es %v, quería ~%v", d, BackoffMin)
	}
}
```

`internal/relay/metrics_test.go`:

```go
package relay

import (
	"errors"
	"testing"
	"time"
)

// fakeClock permite avanzar el tiempo sin dormir.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestMetricsCountsBytes(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)
	m.connected()

	m.sent(100)
	m.sent(250)

	got := m.snapshot(StateLive, 0, 0, 0)
	if got.BytesSent != 350 {
		t.Errorf("BytesSent = %d, quería 350", got.BytesSent)
	}
}

// El bitrate es una media móvil de 5 s (spec §6.6).
func TestMetricsBitrateOverFiveSecondWindow(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)
	m.connected()

	// 125 000 bytes por segundo durante los 5 s de la ventana = 1 Mbps.
	// Han de ser 5 muestras: el divisor es la ventana completa, no el tiempo entre la
	// primera y la última. Con 4 saldrían 800 kbps, que también sería correcto pero
	// confundiría a quien lea el test.
	for i := 0; i < 5; i++ {
		m.sent(125_000)
		c.advance(time.Second)
	}

	got := m.snapshot(StateLive, 0, 0, 0)
	if got.BitrateBPS < 900_000 || got.BitrateBPS > 1_100_000 {
		t.Errorf("BitrateBPS = %d, quería ~1000000", got.BitrateBPS)
	}
}

// Lo que sale de la ventana deja de contar.
func TestMetricsBitrateDropsWhenIdle(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)
	m.connected()

	m.sent(1_000_000)
	c.advance(10 * time.Second) // muy fuera de la ventana de 5 s

	got := m.snapshot(StateLive, 0, 0, 0)
	if got.BitrateBPS != 0 {
		t.Errorf("BitrateBPS = %d tras 10 s sin enviar, quería 0", got.BitrateBPS)
	}
}

func TestMetricsUptimeAndReconnections(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)

	m.connected()
	c.advance(30 * time.Second)
	if got := m.snapshot(StateLive, 0, 0, 0); got.Uptime != 30*time.Second {
		t.Errorf("Uptime = %v, quería 30s", got.Uptime)
	}
	if got := m.snapshot(StateLive, 0, 0, 0); got.Reconnections != 0 {
		t.Errorf("la primera conexión no es una reconexión")
	}

	m.disconnected()
	if got := m.snapshot(StateReconnecting, 0, 0, 0); got.Uptime != 0 {
		t.Errorf("Uptime = %v estando desconectado, quería 0", got.Uptime)
	}

	m.connected()
	c.advance(5 * time.Second)
	got := m.snapshot(StateLive, 0, 0, 0)
	if got.Reconnections != 1 {
		t.Errorf("Reconnections = %d, quería 1", got.Reconnections)
	}
	if got.Uptime != 5*time.Second {
		t.Errorf("Uptime = %v, quería 5s: cuenta desde la reconexión", got.Uptime)
	}
}

// degraded se apaga solo si no hay descartes recientes (spec §6.4).
func TestMetricsDegradedExpires(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)
	m.connected()

	m.markDegraded()
	if !m.snapshot(StateLive, 1, 0, 0).Degraded {
		t.Error("debería estar degradado justo tras un descarte")
	}

	c.advance(5 * time.Second)
	if !m.snapshot(StateLive, 1, 0, 0).Degraded {
		t.Error("a los 5 s todavía debería estar degradado")
	}

	c.advance(6 * time.Second) // 11 s en total
	if m.snapshot(StateLive, 1, 0, 0).Degraded {
		t.Error("pasados 10 s sin descartes debería dejar de estar degradado")
	}
}

func TestMetricsLastError(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)

	if got := m.snapshot(StateIdle, 0, 0, 0); got.LastError != "" {
		t.Errorf("LastError inicial = %q, quería vacío", got.LastError)
	}

	m.setError(errors.New("connection refused"))
	if got := m.snapshot(StateError, 0, 0, 0); got.LastError != "connection refused" {
		t.Errorf("LastError = %q", got.LastError)
	}
}

func TestMetricsSnapshotCarriesQueueAndState(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	m := newMetrics(c.now)

	got := m.snapshot(StateReconnecting, 42, 7, 1234)
	if got.State != "reconnecting" {
		t.Errorf("State = %q", got.State)
	}
	if got.DroppedFrames != 42 {
		t.Errorf("DroppedFrames = %d", got.DroppedFrames)
	}
	if got.QueuedMessages != 7 || got.QueuedBytes != 1234 {
		t.Errorf("cola = %d msgs / %d bytes", got.QueuedMessages, got.QueuedBytes)
	}
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/relay/ -run 'Backoff|Metrics' -v`
Expected: FAIL con `undefined: newBackoff`.

Nota: los tests usan `StateReconnecting`, que la Task 4 añade. Para que compilen ahora,
añade la constante en esta tarea, en `sink.go`, junto a las que ya existen:

```go
	StateReconnecting
```

y su rama en `State.String()`:

```go
	case StateReconnecting:
		return "reconnecting"
```

- [ ] **Step 3: Implementar `internal/relay/backoff.go`**

```go
package relay

import (
	"math/rand"
	"time"
)

// Límites del backoff de reconexión (spec §6.5).
const (
	BackoffMin = time.Second
	BackoffMax = 30 * time.Second

	// backoffJitter es la fracción de variación aleatoria, ±20%. Sin ella, varios
	// destinos que se caen a la vez —lo típico cuando el que falla es tu enlace y no la
	// plataforma— reintentarían en sincronía y volverían a saturarlo.
	backoffJitter = 0.2
)

// backoff calcula la espera entre reintentos: 1 s, 2 s, 4 s… hasta 30 s, con jitter.
//
// No es seguro para uso concurrente: lo usa la goroutine de un solo sink.
type backoff struct {
	attempt int
	rnd     *rand.Rand
}

// newBackoff construye un backoff con una semilla propia, para que dos destinos no
// generen la misma secuencia.
func newBackoff(seed int64) *backoff {
	return &backoff{rnd: rand.New(rand.NewSource(seed))}
}

// next devuelve la espera del siguiente intento y avanza el contador.
func (b *backoff) next() time.Duration {
	base := BackoffMin << uint(min(b.attempt, 30))
	if base > BackoffMax || base <= 0 {
		base = BackoffMax
	}
	b.attempt++

	// Jitter en [-20%, +20%].
	factor := 1 + backoffJitter*(2*b.rnd.Float64()-1)
	return time.Duration(float64(base) * factor)
}

// reset vuelve al principio de la secuencia. Se llama tras una conexión que aguantó.
func (b *backoff) reset() { b.attempt = 0 }

// attempts devuelve cuántos intentos van desde el último reset.
func (b *backoff) attempts() int { return b.attempt }
```

- [ ] **Step 4: Implementar `internal/relay/metrics.go`**

```go
package relay

import (
	"sync"
	"time"
)

const (
	// bitrateWindow es la ventana de la media móvil del bitrate (spec §6.6).
	bitrateWindow = 5 * time.Second

	// degradedWindow es cuánto se mantiene la marca de degradado tras el último
	// descarte (spec §6.4).
	degradedWindow = 10 * time.Second
)

// Metrics es la instantánea pública del estado de un destino. La fase 4 la serializa y la
// empuja por WebSocket.
type Metrics struct {
	State          string
	Degraded       bool
	BytesSent      uint64
	BitrateBPS     uint64
	DroppedFrames  uint64
	Uptime         time.Duration
	Reconnections  uint64
	LastError      string
	QueuedBytes    int
	QueuedMessages int
}

// sample es una medición de bytes enviados en un instante.
type sample struct {
	at    time.Time
	bytes int
}

// metrics acumula las medidas de un destino.
//
// Es seguro para uso concurrente: lo escribe la goroutine del sink y lo lee quien pida un
// snapshot, que en la fase 4 será el bucle del WebSocket.
type metrics struct {
	mu sync.Mutex

	now func() time.Time

	bytesSent     uint64
	reconnections uint64
	connectedAt   time.Time
	everConnected bool
	lastDrop      time.Time
	lastErr       string
	window        []sample
}

func newMetrics(now func() time.Time) *metrics {
	if now == nil {
		now = time.Now
	}
	return &metrics{now: now}
}

// connected marca el inicio de una conexión. A partir de la segunda cuenta como
// reconexión.
func (m *metrics) connected() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.everConnected {
		m.reconnections++
	}
	m.everConnected = true
	m.connectedAt = m.now()
}

// disconnected marca que la conexión se perdió: el uptime vuelve a cero.
func (m *metrics) disconnected() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectedAt = time.Time{}
}

// sent registra n bytes enviados.
func (m *metrics) sent(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.bytesSent += uint64(n)
	m.window = append(m.window, sample{at: m.now(), bytes: n})
	m.pruneLocked()
}

// markDegraded registra que hubo un descarte ahora.
func (m *metrics) markDegraded() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastDrop = m.now()
}

// setError guarda el último error. Un nil lo limpia.
func (m *metrics) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.lastErr = ""
		return
	}
	m.lastErr = err.Error()
}

// pruneLocked descarta las muestras que salieron de la ventana. Hay que tener el mutex.
func (m *metrics) pruneLocked() {
	cutoff := m.now().Add(-bitrateWindow)
	i := 0
	for i < len(m.window) && m.window[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		m.window = append(m.window[:0], m.window[i:]...)
	}
}

// snapshot compone la instantánea. Los datos que no vive aquí —el estado, los descartes y
// la ocupación de la cola— los aporta quien llama, que es el sink.
func (m *metrics) snapshot(state State, dropped uint64, queuedMsgs, queuedBytes int) Metrics {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneLocked()

	var total int
	for _, s := range m.window {
		total += s.bytes
	}
	// El divisor es siempre la ventana completa, no el tiempo entre la primera y la
	// última muestra: si no, un único envío daría un bitrate enorme.
	bitrate := uint64(float64(total*8) / bitrateWindow.Seconds())

	var uptime time.Duration
	if !m.connectedAt.IsZero() {
		uptime = m.now().Sub(m.connectedAt)
	}

	degraded := !m.lastDrop.IsZero() && m.now().Sub(m.lastDrop) < degradedWindow

	return Metrics{
		State:          state.String(),
		Degraded:       degraded,
		BytesSent:      m.bytesSent,
		BitrateBPS:     bitrate,
		DroppedFrames:  dropped,
		Uptime:         uptime,
		Reconnections:  m.reconnections,
		LastError:      m.lastErr,
		QueuedBytes:    queuedBytes,
		QueuedMessages: queuedMsgs,
	}
}
```

- [ ] **Step 5: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/relay/ -run 'Backoff|Metrics' -race -count=5 -v`
Expected: PASS en los 11 tests, cinco veces.

- [ ] **Step 6: Commit**

```bash
git add internal/relay/backoff.go internal/relay/metrics.go \
        internal/relay/backoff_test.go internal/relay/metrics_test.go internal/relay/sink.go
git commit -m "feat(relay): backoff con jitter y métricas por destino"
```

---

### Task 4: Sink con reconexión

Reescribe el bucle del sink: cola nueva, reconexión con backoff, `degraded`, métricas, y el
timebase reanclado en cada conexión.

**Files:**
- Modify: `internal/relay/sink.go`
- Test: `internal/relay/sink_test.go`
- Test: `internal/relay/sink_reconnect_test.go`

**Interfaces:**
- Consumes: `queue` (Task 1), `backoff` y `metrics` (Task 3), `Publisher`, `Preamble`,
  `timebase` de la fase 2.
- Produces:
  ```go
  type SinkConfig struct {
      ID       int64
      Name     string
      Pub      Publisher
      NewPub   func() (Publisher, error) // para reconectar; nil usa siempre Pub
      Queue    queueConfig
      Logger   *slog.Logger
      Now      func() time.Time // nil usa time.Now
      Seed     int64            // semilla del backoff; 0 usa el ID
      OnEvent  func(EngineEvent)
  }
  func (s *Sink) Metrics() Metrics
  ```
  Se conservan `NewSink`, `Start`, `Enqueue`, `Stop`, `State`, `Dropped`, `LastError`, `ID`.
  **`Sink.Enqueue` ya no descarta por cola llena**: eso ahora lo decide la `queue`.

- [ ] **Step 1: Escribir el test de reconexión**

`internal/relay/sink_reconnect_test.go`:

```go
package relay

import (
	"context"
	"sync"
	"testing"
	"time"
)

// flakyPublisher falla las primeras n conexiones y luego funciona.
type flakyPublisher struct {
	mu        sync.Mutex
	failFirst int
	attempts  int
	inner     *fakePublisher
}

func (f *flakyPublisher) Connect(ctx context.Context) error {
	f.mu.Lock()
	f.attempts++
	fail := f.attempts <= f.failFirst
	f.mu.Unlock()
	if fail {
		return errFakeWrite
	}
	return f.inner.Connect(ctx)
}

func (f *flakyPublisher) WriteMeta(ts uint32, p []byte) error  { return f.inner.WriteMeta(ts, p) }
func (f *flakyPublisher) WriteAudio(ts uint32, p []byte) error { return f.inner.WriteAudio(ts, p) }
func (f *flakyPublisher) WriteVideo(ts uint32, p []byte) error { return f.inner.WriteVideo(ts, p) }
func (f *flakyPublisher) Close() error                         { return f.inner.Close() }

func (f *flakyPublisher) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// Un destino que falla al conectar debe reintentar, no rendirse.
func TestSinkReconnectsAfterConnectFailure(t *testing.T) {
	inner := &fakePublisher{}
	pub := &flakyPublisher{failFirst: 2, inner: inner}

	s := NewSink(SinkConfig{
		ID: 1, Name: "X", Pub: pub,
		NewPub: func() (Publisher, error) { return pub, nil },
	})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	// El backoff arranca en 1 s, así que dos fallos son ~3 s.
	waitForDur(t, 20*time.Second, func() bool { return s.State() == StateLive }, "llegó a live tras reintentar")

	if got := pub.attemptCount(); got < 3 {
		t.Errorf("se intentó conectar %d veces, quería al menos 3", got)
	}
	// Ojo: Reconnections sigue en 0. La primera conexión que tiene éxito no es una
	// reconexión, por muchos intentos fallidos que la precedan. El contador se verifica en
	// TestSinkResendsPreambleAfterReconnect, donde sí se cae una conexión ya establecida.
}

// Tras reconectar, el preámbulo se reenvía y el timeline arranca de nuevo en 0.
func TestSinkResendsPreambleAfterReconnect(t *testing.T) {
	var mu sync.Mutex
	var pubs []*fakePublisher

	s := NewSink(SinkConfig{
		ID: 1, Name: "X",
		NewPub: func() (Publisher, error) {
			p := &fakePublisher{}
			mu.Lock()
			pubs = append(pubs, p)
			mu.Unlock()
			return p, nil
		},
	})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()

	waitFor(t, func() bool { return s.State() == StateLive }, "primera conexión")

	s.Enqueue(videoKey(500000))
	s.Enqueue(videoInter(500033))
	mu.Lock()
	first := pubs[0]
	mu.Unlock()
	waitFor(t, func() bool { return len(first.snapshot()) >= 5 }, "la primera conexión escribió")

	// Forzar el fallo de escritura para disparar la reconexión.
	first.mu.Lock()
	first.writeErr = errFakeWrite
	first.mu.Unlock()
	s.Enqueue(videoInter(500066))

	waitForDur(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(pubs) >= 2
	}, "se creó un publisher nuevo")
	waitForDur(t, 20*time.Second, func() bool { return s.State() == StateLive }, "volvió a live")

	mu.Lock()
	second := pubs[1]
	mu.Unlock()

	// Timestamps altos de nuevo: la base debe reanclarse, no continuar la anterior.
	s.Enqueue(videoKey(900000))
	s.Enqueue(videoInter(900033))
	waitForDur(t, 10*time.Second, func() bool { return len(second.snapshot()) >= 5 }, "la segunda conexión escribió")

	got := second.snapshot()
	if got[0].Kind != KindMeta || got[1].Kind != KindVideo || got[2].Kind != KindAudio {
		t.Errorf("la reconexión no reenvió el preámbulo: %v %v %v", got[0].Kind, got[1].Kind, got[2].Kind)
	}
	if got[3].TS != 0 {
		t.Errorf("el primer frame tras reconectar salió con ts=%d, quería 0", got[3].TS)
	}
	if got[4].TS != 33 {
		t.Errorf("el segundo frame salió con ts=%d, quería 33", got[4].TS)
	}
	// Aquí sí: se cayó una conexión que estaba establecida.
	if m := s.Metrics(); m.Reconnections == 0 {
		t.Error("el contador de reconexiones no subió tras caerse una conexión viva")
	}
}

// waitForDur es waitFor con un plazo propio, para las esperas que dependen del backoff.
func waitForDur(t *testing.T, limit time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tiempo agotado esperando: %s", msg)
}

// Stop debe cortar la espera del backoff, no esperar a que venza.
func TestSinkStopInterruptsBackoff(t *testing.T) {
	pub := &flakyPublisher{failFirst: 100, inner: &fakePublisher{}}
	s := NewSink(SinkConfig{
		ID: 1, Name: "X", Pub: pub,
		NewPub: func() (Publisher, error) { return pub, nil },
	})
	s.Start(context.Background(), preambleWith())

	waitForDur(t, 5*time.Second, func() bool { return s.State() == StateReconnecting }, "entró en reconnecting")

	start := time.Now()
	s.Stop()
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Stop tardó %v: debería cortar la espera del backoff", d)
	}
}
```

Y añade a `internal/relay/sink_test.go` los tests del backpressure y las métricas:

```go
// Un destino lento descarta GOPs y se marca degradado, sin bloquear a quien encola.
func TestSinkDegradesUnderBackpressure(t *testing.T) {
	block := make(chan struct{})
	pub := &fakePublisher{blockWrites: block}

	s := NewSink(SinkConfig{
		ID: 1, Name: "X", Pub: pub,
		Queue: queueConfig{MaxBytes: 4096, MaxSpan: 1_000_000},
	})
	s.Start(context.Background(), preambleWith())
	defer func() { close(block); s.Stop() }()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 400; i++ {
			if i%10 == 0 {
				s.Enqueue(&Message{Kind: KindVideo, Timestamp: uint32(i * 33),
					Payload: make([]byte, 256), IsKeyframe: true})
			} else {
				s.Enqueue(&Message{Kind: KindVideo, Timestamp: uint32(i * 33),
					Payload: make([]byte, 256)})
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Enqueue se bloqueó bajo presión")
	}

	waitFor(t, func() bool { return s.Dropped() > 0 }, "hubo descartes")
	waitFor(t, func() bool { return s.Metrics().Degraded }, "se marcó degradado")
	if s.State() != StateLive {
		t.Errorf("State = %v: degraded es un atributo, no un estado (spec §3.7)", s.State())
	}
}

func TestSinkMetricsCountBytes(t *testing.T) {
	pub := &fakePublisher{}
	s := NewSink(SinkConfig{ID: 1, Name: "X", Pub: pub})
	s.Start(context.Background(), preambleWith())
	defer s.Stop()
	waitFor(t, func() bool { return s.State() == StateLive }, "estado live")

	s.Enqueue(videoKey(1000))
	waitFor(t, func() bool { return s.Metrics().BytesSent > 0 }, "se contaron bytes")
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/relay/ -run 'Reconnect|Degrades|MetricsCount|Backoff' -v`
Expected: FAIL porque `SinkConfig` no tiene `NewPub`, `Queue` es de otro tipo y no existe
`Sink.Metrics`.

- [ ] **Step 3: Reescribir `internal/relay/sink.go`**

```go
package relay

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// State es el estado de un destino. `degraded` va aparte, en Metrics, porque estando
// degradado la conexión sigue arriba (spec §3.7).
type State uint8

const (
	StateIdle State = iota
	StateConnecting
	StateLive
	StateReconnecting
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
	case StateReconnecting:
		return "reconnecting"
	case StateError:
		return "error"
	default:
		return "desconocido"
	}
}

// suspectThreshold es el número de reconexiones seguidas sin haber llegado a transmitir
// tras las que se registra un evento de sospecha.
//
// El spec §6.5 pide reintentos indefinidos, así que NO se deja de reintentar. Pero
// `Stream.Publish` de go-rtmp no espera el onStatus, así que una clave rechazada por la
// plataforma parece un éxito y solo falla en la primera escritura: sin esto, una clave
// mal pegada produce un bucle silencioso para siempre.
const suspectThreshold = 5

// SinkConfig son los datos para construir un sink.
type SinkConfig struct {
	ID   int64
	Name string
	// Pub es el publisher inicial. Si NewPub es nil, se reutiliza en cada reconexión.
	Pub Publisher
	// NewPub construye un publisher nuevo para cada intento de conexión. Es lo correcto
	// en producción: un Publisher cerrado no se puede reabrir.
	NewPub  func() (Publisher, error)
	Queue   queueConfig
	Logger  *slog.Logger
	Now     func() time.Time
	Seed    int64
	OnEvent func(EngineEvent)
}

// Sink atiende a un destino desde su propia goroutine: conecta, reenvía, y reconecta con
// backoff cuando se cae. Posee su publisher, su cola, su timebase y sus métricas.
type Sink struct {
	id      int64
	name    string
	newPub  func() (Publisher, error)
	log     *slog.Logger
	q       *queue
	met     *metrics
	bo      *backoff
	onEvent func(EngineEvent)

	quit chan struct{}
	done chan struct{}
	once sync.Once

	state  atomic.Uint32
	errMu  sync.Mutex
	lastEr error
}

// NewSink construye un sink parado.
func NewSink(cfg SinkConfig) *Sink {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	seed := cfg.Seed
	if seed == 0 {
		seed = cfg.ID
	}

	newPub := cfg.NewPub
	if newPub == nil {
		pub := cfg.Pub
		newPub = func() (Publisher, error) {
			if pub == nil {
				return nil, errors.New("el sink no tiene publisher")
			}
			return pub, nil
		}
	}

	return &Sink{
		id:      cfg.ID,
		name:    cfg.Name,
		newPub:  newPub,
		log:     log.With("destino_id", cfg.ID, "destino", cfg.Name),
		q:       newQueue(cfg.Queue),
		met:     newMetrics(cfg.Now),
		bo:      newBackoff(seed),
		onEvent: cfg.OnEvent,
		quit:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (s *Sink) ID() int64        { return s.id }
func (s *Sink) State() State     { return State(s.state.Load()) }
func (s *Sink) Dropped() uint64  { return s.q.dropped() }

func (s *Sink) LastError() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.lastEr
}

// Metrics devuelve la instantánea del destino.
func (s *Sink) Metrics() Metrics {
	msgs, bytes, _ := s.q.stats()
	return s.met.snapshot(s.State(), s.q.dropped(), msgs, bytes)
}

func (s *Sink) setState(st State) { s.state.Store(uint32(st)) }

func (s *Sink) fail(err error) {
	s.errMu.Lock()
	s.lastEr = err
	s.errMu.Unlock()
	s.met.setError(err)
	s.log.Warn("destino caído", "err", err)
}

func (s *Sink) emit(level, kind, msg string) {
	if s.onEvent == nil {
		return
	}
	id := s.id
	s.onEvent(EngineEvent{DestinationID: &id, Level: level, Kind: kind, Message: msg})
}

// Start lanza la goroutine del sink.
func (s *Sink) Start(ctx context.Context, pre *Preamble) {
	go s.run(ctx, pre)
}

// Enqueue entrega un mensaje. Nunca bloquea: la cola aplica su política de descarte.
//
// La marca de degradado se pone aquí y no en el bucle de envío porque el caso que
// interesa es justo aquel en el que el envío está atascado: si se marcara ahí, un destino
// bloqueado escribiendo nunca llegaría a marcarse.
func (s *Sink) Enqueue(msg *Message) {
	s.q.push(msg)
	if s.q.droppingVideo() {
		s.met.markDegraded()
	}
}

// Stop detiene el sink y espera a que su goroutine termine. Es idempotente.
func (s *Sink) Stop() {
	s.once.Do(func() {
		close(s.quit)
		s.q.close()
	})
	<-s.done
}

// run es el bucle de vida del destino: conectar, transmitir, y reconectar al caer.
func (s *Sink) run(ctx context.Context, pre *Preamble) {
	defer close(s.done)

	for {
		select {
		case <-s.quit:
			s.setState(StateIdle)
			return
		case <-ctx.Done():
			s.setState(StateIdle)
			return
		default:
		}

		if s.bo.attempts() == 0 {
			s.setState(StateConnecting)
		} else {
			s.setState(StateReconnecting)
		}

		transmitted, err := s.session(ctx, pre)
		if err == nil {
			// Solo se sale sin error al pararse.
			s.setState(StateIdle)
			return
		}

		s.fail(err)
		s.met.disconnected()

		if transmitted {
			// La conexión llegó a transmitir, así que la configuración es buena: se
			// reinicia el backoff para que una caída puntual reconecte rápido.
			s.bo.reset()
			s.emit("warn", "destination_disconnected", "el destino se desconectó: "+err.Error())
		} else if s.bo.attempts() == suspectThreshold {
			// Nunca llegó a transmitir en varios intentos seguidos. Se sigue
			// reintentando (spec §6.5), pero se deja constancia: lo más probable es una
			// clave incorrecta, y go-rtmp no la reporta como tal.
			s.emit("error", "destination_suspect",
				"el destino falla siempre antes de transmitir; revisa la URL y la clave")
			s.log.Error("el destino nunca llega a transmitir: revisa la URL y la clave")
		}

		wait := s.bo.next()
		s.setState(StateReconnecting)
		s.log.Info("reintentando el destino", "espera", wait, "intento", s.bo.attempts())

		select {
		case <-s.quit:
			s.setState(StateIdle)
			return
		case <-ctx.Done():
			s.setState(StateIdle)
			return
		case <-time.After(wait):
		}
	}
}

// session abre una conexión y transmite hasta que falla o se para el sink.
//
// Devuelve transmitted=true si llegó a escribir media, y err=nil solo si el sink se paró
// de forma ordenada.
func (s *Sink) session(ctx context.Context, pre *Preamble) (bool, error) {
	pub, err := s.newPub()
	if err != nil {
		return false, err
	}
	defer pub.Close()

	if err := pub.Connect(ctx); err != nil {
		return false, err
	}

	s.met.connected()
	s.met.setError(nil)
	s.setState(StateLive)
	// El backoff NO se reinicia aquí, sino en run() y solo si la conexión llegó a
	// transmitir. Reiniciarlo al conectar anularía la detección de clave rechazada: con
	// una clave mala, `Connect` tiene éxito y el fallo llega en la primera escritura, así
	// que el contador de intentos volvería a cero cada vez y nunca alcanzaría el umbral.
	s.log.Info("destino conectado")
	s.emit("info", "destination_connected", "el destino conectó")

	var (
		tb          timebase
		transmitted bool
	)

	for {
		select {
		case <-s.quit:
			return transmitted, nil
		case <-ctx.Done():
			return transmitted, nil
		default:
		}

		msg, ok := s.q.pop(ctx)
		if !ok {
			// La cola se cerró o venció el contexto: parada ordenada.
			return transmitted, nil
		}

		sent, err := s.deliver(pub, msg, pre, &tb)
		if err != nil {
			// Un fallo de escritura es una conexión perdida, no algo que reintentar
			// sobre la misma conexión: el Write de go-rtmp ya trae su propio timeout de
			// 5 s (spec §16.2).
			return transmitted, err
		}
		if sent {
			transmitted = true
		}
	}
}

// deliver procesa un mensaje. Antes del primer keyframe descarta; en el keyframe manda el
// preámbulo y ancla el timebase; después traduce y reenvía. Devuelve si escribió algo.
func (s *Sink) deliver(pub Publisher, msg *Message, pre *Preamble, tb *timebase) (bool, error) {
	if !tb.started() {
		// Solo un keyframe real arranca. El sequence header trae el bit de keyframe
		// puesto pero no es un frame decodificable.
		if msg.Kind != KindVideo || !msg.IsKeyframe || msg.IsSeqHeader {
			return false, nil
		}
		if err := s.sendPreamble(pub, pre); err != nil {
			return false, err
		}
		tb.start(msg.Timestamp)
	}

	ts, ok := tb.translate(msg.Timestamp)
	if !ok {
		return false, nil // anterior a la base: se descarta (spec §3.2)
	}

	var err error
	switch msg.Kind {
	case KindVideo:
		err = pub.WriteVideo(ts, msg.Payload)
	case KindAudio:
		err = pub.WriteAudio(ts, msg.Payload)
	case KindMeta:
		err = pub.WriteMeta(ts, msg.Payload)
	}
	if err != nil {
		return false, err
	}
	s.met.sent(len(msg.Payload))
	return true, nil
}

// sendPreamble manda onMetaData, AVC sequence header y AAC sequence header, los tres con
// ts=0, antes de cualquier frame (spec §6.3).
func (s *Sink) sendPreamble(pub Publisher, pre *Preamble) error {
	meta, videoSeq, audioSeq := pre.Snapshot()
	if meta != nil {
		if err := pub.WriteMeta(0, meta.Payload); err != nil {
			return err
		}
		s.met.sent(len(meta.Payload))
	}
	if videoSeq != nil {
		if err := pub.WriteVideo(0, videoSeq.Payload); err != nil {
			return err
		}
		s.met.sent(len(videoSeq.Payload))
	}
	if audioSeq != nil {
		if err := pub.WriteAudio(0, audioSeq.Payload); err != nil {
			return err
		}
		s.met.sent(len(audioSeq.Payload))
	}
	return nil
}
```

- [ ] **Step 4: Ejecutar todos los tests del paquete**

Run: `go test ./internal/relay/ -race -count=3 -timeout 300s`
Expected: PASS. Los tests de reconexión tardan porque el backoff arranca en 1 s.

Si `TestSinkConnectFailureSetsErrorState` de la fase 2 ahora falla, es esperado: el sink ya
no se queda en `StateError`, sino que pasa a `StateReconnecting` y reintenta. **Adáptalo**
para que compruebe que llega a `StateReconnecting` y que `LastError()` no es nil, y anota
el cambio en tu informe.

- [ ] **Step 5: Commit**

```bash
git add internal/relay/sink.go internal/relay/sink_test.go internal/relay/sink_reconnect_test.go
git commit -m "feat(relay): sink con reconexión, backoff, backpressure y métricas"
```

---

### Task 5: Hub multi-destino y eventos

El hub gana un `Snapshot()` de métricas y un `Add` que no se cuelga con un sink nunca
arrancado.

**Files:**
- Modify: `internal/relay/hub.go`
- Test: `internal/relay/hub_test.go`

**Interfaces:**
- Consumes: `Sink`, `Metrics` de las tareas anteriores.
- Produces:
  ```go
  func (h *Hub) Snapshot() map[int64]Metrics
  func (h *Hub) Len() int
  ```
  `Add`, `Remove`, `Publish`, `Close` y `Preamble` no cambian de firma.

- [ ] **Step 1: Escribir los tests que fallan**

Añade a `internal/relay/hub_test.go`:

```go
func TestHubSnapshotHasEveryDestination(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	for i := 1; i <= 3; i++ {
		s := NewSink(SinkConfig{ID: int64(i), Name: "dest", Pub: &fakePublisher{}})
		s.Start(context.Background(), h.Preamble())
		h.Add(s)
	}
	waitFor(t, func() bool { return h.Len() == 3 }, "tres destinos registrados")

	snap := h.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("Snapshot tiene %d destinos, quería 3", len(snap))
	}
	for id := int64(1); id <= 3; id++ {
		if _, ok := snap[id]; !ok {
			t.Errorf("falta el destino %d en el snapshot", id)
		}
	}
}

// Añadir un sink que nunca se arrancó no debe colgar el hub.
func TestHubAddNeverStartedSinkDoesNotHang(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()

	old := NewSink(SinkConfig{ID: 1, Name: "sin arrancar", Pub: &fakePublisher{}})
	// A propósito: no se llama a old.Start.
	h.Add(old)

	done := make(chan struct{})
	go func() {
		defer close(done)
		fresh := NewSink(SinkConfig{ID: 1, Name: "nuevo", Pub: &fakePublisher{}})
		fresh.Start(context.Background(), h.Preamble())
		h.Add(fresh)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Add se colgó al reemplazar un sink que nunca se arrancó")
	}
}

// Un destino lento no debe frenar la entrega a los demás.
func TestHubSlowSinkDoesNotBlockOthers(t *testing.T) {
	h := NewHub(nil)
	block := make(chan struct{})
	defer func() { close(block); h.Close() }()

	slow := &fakePublisher{blockWrites: block}
	fast := &fakePublisher{}

	sSlow := NewSink(SinkConfig{ID: 1, Name: "lento", Pub: slow,
		Queue: queueConfig{MaxBytes: 1024, MaxSpan: 1_000_000}})
	sSlow.Start(context.Background(), h.Preamble())
	h.Add(sSlow)

	sFast := NewSink(SinkConfig{ID: 2, Name: "rápido", Pub: fast})
	sFast.Start(context.Background(), h.Preamble())
	h.Add(sFast)

	waitFor(t, func() bool { return sFast.State() == StateLive }, "el rápido está live")

	h.Publish(&Message{Kind: KindMeta, Payload: []byte{0xFF}})
	h.Publish(&Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true})
	h.Publish(&Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			h.Publish(&Message{Kind: KindVideo, Timestamp: uint32(i * 33),
				Payload: make([]byte, 512), IsKeyframe: i%10 == 0})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish se bloqueó por culpa del destino lento")
	}

	waitFor(t, func() bool { return len(fast.snapshot()) > 10 }, "el destino rápido siguió recibiendo")
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que fallan**

Run: `go test ./internal/relay/ -run 'HubSnapshot|HubAddNeverStarted|HubSlowSink' -v`
Expected: FAIL con `undefined: h.Snapshot` y `undefined: h.Len`.

- [ ] **Step 3: Modificar `internal/relay/hub.go`**

Añade el campo `started` al `Sink` para poder pararlo sin colgarse. En `sink.go`, añade a
`Sink`:

```go
	started atomic.Bool
```

y en `Start`, antes del `go s.run(...)`:

```go
	s.started.Store(true)
```

y en `Stop`, sustituye el cuerpo por:

```go
// Stop detiene el sink y espera a que su goroutine termine. Es idempotente, y es seguro
// sobre un sink que nunca se arrancó: en ese caso no hay goroutine a la que esperar.
func (s *Sink) Stop() {
	s.once.Do(func() {
		close(s.quit)
		s.q.close()
	})
	if s.started.Load() {
		<-s.done
	}
}
```

En `hub.go`, añade los dos métodos nuevos:

```go
// Len devuelve cuántos destinos hay registrados.
func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sinks)
}

// Snapshot devuelve las métricas de todos los destinos, indexadas por id. Es lo que la
// fase 4 empujará por WebSocket cada segundo.
func (h *Hub) Snapshot() map[int64]Metrics {
	h.mu.RLock()
	sinks := make([]*Sink, 0, len(h.sinks))
	for _, s := range h.sinks {
		sinks = append(sinks, s)
	}
	h.mu.RUnlock()

	// Fuera del lock: Metrics() toma los mutex del sink y no queremos encadenarlos con
	// el del hub, que bloquearía a Publish.
	out := make(map[int64]Metrics, len(sinks))
	for _, s := range sinks {
		out[s.ID()] = s.Metrics()
	}
	return out
}
```

- [ ] **Step 4: Ejecutar los tests y verificar que pasan**

Run: `go test ./internal/relay/ -race -count=3 -timeout 300s`
Expected: PASS en todo el paquete.

- [ ] **Step 5: Commit**

```bash
git add internal/relay/hub.go internal/relay/hub_test.go internal/relay/sink.go
git commit -m "feat(relay): snapshot de métricas del hub y Stop seguro sin arrancar"
```

---

### Task 6: Store bajo concurrencia, resolución y bitrate

Paga la nota de arrastre de la fase 1 (§15.7): ningún test lanza goroutines contra el store,
así que `-race` no verifica nada ahí. Y conecta la resolución del SPS con el cierre de sesión.

**Files:**
- Create: `internal/store/concurrency_test.go`
- Modify: `internal/relay/engine.go`
- Test: `internal/relay/engine_test.go`

**Interfaces:**
- Consumes: `store.DB`, `InTx`, `flv.ParseResolution` (Task 2), `Hub.Snapshot` (Task 5).
- Produces:
  ```go
  // en internal/relay
  func (e *Engine) Snapshot() map[int64]Metrics
  // EngineStore gana un método:
  type EngineStore interface {
      StartSession(ctx context.Context) (int64, error)
      FinishSession(ctx context.Context, id int64, width, height, bitrateBPS int) error
      LogEvent(ctx context.Context, e EngineEvent) error
  }
  ```
  El `Engine` observa la resolución del primer AVC sequence header y el bitrate medio de la
  sesión, y los pasa a `FinishSession`.

- [ ] **Step 1: Escribir el test de concurrencia del store**

`internal/store/concurrency_test.go`:

```go
package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

// La fase 3 pone una goroutine por destino escribiendo eventos contra una base con
// SetMaxOpenConns(1). Hasta ahora ningún test lanzaba goroutines contra el store, así que
// `-race` no verificaba nada ahí.
func TestStoreHandlesConcurrentWriters(t *testing.T) {
	db, c := bootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	sessionID, err := db.StartSession(ctx)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	const writers, perWriter = 8, 40

	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := db.LogEvent(ctx, store.Event{
					SessionID:     &sessionID,
					DestinationID: &dest.ID,
					Level:         store.LevelInfo,
					Kind:          "prueba_concurrencia",
					Message:       "evento",
				}); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}

	// Lectores en paralelo con los escritores.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := db.RecentEvents(ctx, 10); err != nil {
					errs <- err
					return
				}
				if _, err := db.ListDestinations(ctx); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(25 * time.Second):
		t.Fatal("los escritores concurrentes no terminaron: contención o autobloqueo")
	}
	close(errs)
	for err := range errs {
		t.Fatalf("error de un escritor concurrente: %v", err)
	}

	events, err := db.RecentEvents(ctx, 1000)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	var n int
	for _, e := range events {
		if e.Kind == "prueba_concurrencia" {
			n++
		}
	}
	if n != writers*perWriter {
		t.Errorf("se persistieron %d eventos, quería %d: se perdieron escrituras",
			n, writers*perWriter)
	}
}

// Transacciones concurrentes contra la única conexión: deben serializarse, no bloquearse.
func TestStoreConcurrentTransactions(t *testing.T) {
	db, c := bootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				err := db.InTx(ctx, func(tx *store.DB) error {
					if _, err := tx.LogEvent(ctx, store.Event{
						DestinationID: &dest.ID,
						Level:         store.LevelWarn,
						Kind:          "tx_concurrente",
						Message:       "dentro de transacción",
					}); err != nil {
						return err
					}
					_, err := tx.ListDestinations(ctx)
					return err
				})
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		t.Fatal("las transacciones concurrentes se bloquearon")
	}
	close(errs)
	for err := range errs {
		t.Fatalf("error en una transacción concurrente: %v", err)
	}
}
```

- [ ] **Step 2: Ejecutar los tests y verificar que pasan o revelan un problema**

Run: `go test ./internal/store/ -run Concurrent -race -count=3 -timeout 300s -v`
Expected: PASS. **Si se cuelgan o pierden escrituras, es un hallazgo real**: anótalo en tu
informe con el diagnóstico y **no** relajes el test para que pase. La premisa de
`SetMaxOpenConns(1)` es justo esto, y esta es la primera vez que se verifica.

- [ ] **Step 3: Escribir el test del engine**

Añade a `internal/relay/engine_test.go`:

```go
// La resolución sale del SPS del AVC sequence header, no del onMetaData (spec §3.8).
func TestEngineRecordsResolutionFromSPS(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()
	st := &fakeStore{}
	e := NewEngine(EngineConfig{Hub: h, Store: st, BaseContext: context.Background()})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}

	// AVC sequence header real de 640x360.
	seq := mustAVCSeqHeader()
	e.OnMessage(&Message{Kind: KindVideo, Payload: seq, IsSeqHeader: true, IsKeyframe: true})
	e.OnMessage(&Message{Kind: KindVideo, Timestamp: 0, Payload: make([]byte, 1000), IsKeyframe: true})
	e.OnMessage(&Message{Kind: KindVideo, Timestamp: 1000, Payload: make([]byte, 1000)})
	e.OnPublishEnd()

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.lastWidth != 640 || st.lastHeight != 360 {
		t.Errorf("resolución = %dx%d, quería 640x360", st.lastWidth, st.lastHeight)
	}
	if st.lastBitrate <= 0 {
		t.Errorf("bitrate = %d, quería un valor medido positivo", st.lastBitrate)
	}
}

// Sin sequence header no se inventa una resolución: se cierra con ceros.
func TestEngineRecordsZeroResolutionWithoutSPS(t *testing.T) {
	h := NewHub(nil)
	defer h.Close()
	st := &fakeStore{}
	e := NewEngine(EngineConfig{Hub: h, Store: st, BaseContext: context.Background()})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart: %v", err)
	}
	e.OnMessage(&Message{Kind: KindVideo, Timestamp: 0, Payload: []byte{0x17, 0x01}, IsKeyframe: true})
	e.OnPublishEnd()

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.lastWidth != 0 || st.lastHeight != 0 {
		t.Errorf("resolución = %dx%d, quería 0x0 sin sequence header", st.lastWidth, st.lastHeight)
	}
}
```

Amplía `fakeStore` en el mismo archivo con los campos nuevos:

```go
	lastWidth   int
	lastHeight  int
	lastBitrate int
```

y guárdalos en `FinishSession`:

```go
func (f *fakeStore) FinishSession(ctx context.Context, id int64, w, h, b int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended++
	f.lastWidth, f.lastHeight, f.lastBitrate = w, h, b
	return nil
}
```

Y añade el helper que construye un AVC sequence header real, **con el mismo SPS que uses en
la Task 2**:

```go
// mustAVCSeqHeader devuelve un AVC sequence header real de 640x360.
func mustAVCSeqHeader() []byte {
	sps := []byte{
		0x67, 0x64, 0x00, 0x1e, 0xac, 0xd9, 0x40, 0xa0, 0x2f, 0xf9, 0x61, 0x00,
		0x00, 0x03, 0x00, 0x01, 0x00, 0x00, 0x03, 0x00, 0x3c, 0x8f, 0x16, 0x2d, 0x96,
	}
	out := []byte{0x17, 0x00, 0, 0, 0, 0x01, sps[1], sps[2], sps[3], 0xFF, 0xE1}
	out = append(out, byte(len(sps)>>8), byte(len(sps)))
	return append(out, sps...)
}
```

- [ ] **Step 4: Modificar `internal/relay/engine.go`**

Añade `"github.com/aprendomx/splitstream/internal/flv"` y `"time"` a los imports, más los
campos de sesión al `Engine`:

```go
	sessionWidth   int
	sessionHeight  int
	sessionBytes   uint64
	sessionStarted time.Time
```

En `OnPublishStart`, tras fijar `e.sessionID`, resetea los acumuladores dentro del mismo
bloque con el mutex:

```go
	e.sessionWidth, e.sessionHeight = 0, 0
	e.sessionBytes = 0
	e.sessionStarted = time.Now()
```

Sustituye `OnMessage` por:

```go
// OnMessage reparte un mensaje a los destinos y acumula lo que la sesión necesita medir.
func (e *Engine) OnMessage(msg *Message) {
	e.observe(msg)
	e.hub.Publish(msg)
}

// observe acumula el tamaño para el bitrate medido y saca la resolución del primer AVC
// sequence header. Se prefiere el SPS al onMetaData porque este es declarativo y puede
// mentir (spec §3.8).
func (e *Engine) observe(msg *Message) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.sessionID == 0 {
		return
	}
	e.sessionBytes += uint64(len(msg.Payload))

	if msg.Kind != KindVideo || !msg.IsSeqHeader || e.sessionWidth != 0 {
		return
	}
	w, h, err := flv.ParseResolution(msg.Payload)
	if err != nil {
		e.log.Warn("no se pudo leer la resolución del sequence header", "err", err)
		return
	}
	e.sessionWidth, e.sessionHeight = w, h
	e.log.Info("resolución detectada", "ancho", w, "alto", h)
}
```

Y en `OnPublishEnd`, sustituye la llamada a `FinishSession` por una que use lo medido.
Lee los acumuladores en el mismo bloque donde ya lees `id`:

```go
	e.mu.Lock()
	id := e.sessionID
	width, height := e.sessionWidth, e.sessionHeight
	bytes := e.sessionBytes
	started := e.sessionStarted
	e.mu.Unlock()

	if id == 0 {
		return
	}

	// Bitrate medio real de la sesión, no el declarado.
	bitrate := 0
	if elapsed := time.Since(started); elapsed > 0 {
		bitrate = int(float64(bytes*8) / elapsed.Seconds())
	}

	ctx := context.Background()
	if err := e.store.FinishSession(ctx, id, width, height, bitrate); err != nil {
		e.log.Error("no se pudo cerrar la sesión", "sesion_id", id, "err", err)
	}
```

Deja el resto de `OnPublishEnd` intacto, en particular el orden `logEvent` → `hub.Close()`
→ `sessionID = 0`.

Añade además el paso de métricas:

```go
// Snapshot devuelve las métricas de todos los destinos. La fase 4 la sirve por WebSocket.
func (e *Engine) Snapshot() map[int64]Metrics { return e.hub.Snapshot() }
```

**Nota:** `internal/relay` ya importaba `internal/flv`? No. Comprueba tras el cambio que
`go list -deps ./internal/relay | grep -E 'go-rtmp|database/sql'` sigue vacío — `internal/flv`
no importa ninguno de los dos, así que la frontera se mantiene.

- [ ] **Step 5: Ejecutar los tests y verificar que pasan**

Run: `go test ./... -race -count=1 -timeout 600s`
Expected: PASS en todos los paquetes.

- [ ] **Step 6: Commit**

```bash
git add internal/store/concurrency_test.go internal/relay/engine.go internal/relay/engine_test.go
git commit -m "feat: resolución del SPS, bitrate medido y test de contención del store"
```

---

### Task 7: N destinos, gracia de apagado y FCUnpublish

**Files:**
- Modify: `cmd/splitstream/main.go`
- Modify: `internal/rtmpio/publisher.go`
- Test: `internal/rtmpio/publisher_test.go`

**Interfaces:**
- Consumes: todo lo anterior.
- Produces: `main.go` arranca **todos** los destinos habilitados, con un contexto de sinks
  separado del de señales para darles los 3 s de gracia del spec §6.5.

- [ ] **Step 1: Escribir el test de `FCUnpublish`**

Añade a `internal/rtmpio/publisher_test.go`:

```go
// El cierre ordenado manda FCUnpublish antes de deleteStream (spec §6.5). Sin conexión,
// Close debe seguir siendo seguro.
func TestCloseSendsFCUnpublishWhenConnected(t *testing.T) {
	rec := &recorder{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := NewIngest(IngestConfig{Addr: ln.Addr().String(), Handler: rec})
	go ing.Serve(ln)
	defer ing.Close()
	time.Sleep(200 * time.Millisecond)

	p, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://" + ln.Addr().String() + "/live",
		StreamKey: crypto.Secret("clave"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Close no debe fallar ni colgarse mandando FCUnpublish.
	done := make(chan error, 1)
	go func() { done <- p.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close = %v, quería nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close se colgó")
	}
}
```

- [ ] **Step 2: Ejecutar el test y verificar que pasa con el código actual**

Run: `go test ./internal/rtmpio/ -run FCUnpublish -race -v`
Expected: PASS. Es un test de no-regresión: el `FCUnpublish` que añades no debe romper el
cierre. Anota en tu informe que este test pasa antes y después.

- [ ] **Step 3: Añadir `FCUnpublish` en `internal/rtmpio/publisher.go`**

En `Close`, antes del `DeleteStream`:

```go
	if conn != nil && stream != nil {
		// FCUnpublish antes de deleteStream: es lo que espera el cierre ordenado del
		// spec §6.5, y varias plataformas lo usan para liberar el slot de emisión sin
		// esperar al timeout. Que falle no es motivo para no seguir cerrando.
		if err := p.writeCommand(stream, "FCUnpublish"); err != nil {
			p.log.Debug("FCUnpublish falló al cerrar", "err", err)
		}
		if err := conn.DeleteStream(&message.NetStreamDeleteStream{StreamID: stream.StreamID()}); err != nil {
			p.log.Debug("deleteStream falló al cerrar", "err", err)
		}
	}
```

- [ ] **Step 4: Modificar `cmd/splitstream/main.go`**

Sustituye el cuerpo del `SetSinkProvider` para que arranque **todos** los destinos
habilitados y para que cada sink pueda reconstruir su publisher:

```go
	engine.SetSinkProvider(func() ([]*relay.Sink, error) {
		dests, err := db.ListDestinations(ctx)
		if err != nil {
			return nil, err
		}

		var out []*relay.Sink
		for _, d := range dests {
			if !d.Enabled {
				continue
			}
			key, err := db.RevealDestinationKey(ctx, cipher, d.ID)
			if err != nil {
				logger.Error("no se pudo leer la clave del destino", "destino", d.Name, "err", err)
				continue
			}
			// Validar aquí evita crear un sink que no podría conectar nunca.
			if _, err := rtmpio.NewPublisher(rtmpio.PublisherConfig{
				URL: d.RTMPURL, StreamKey: key, Logger: logger,
			}); err != nil {
				logger.Error("destino mal configurado", "destino", d.Name, "err", err)
				continue
			}

			url, name, id := d.RTMPURL, d.Name, d.ID
			out = append(out, relay.NewSink(relay.SinkConfig{
				ID:   id,
				Name: name,
				// Cada reconexión necesita un publisher nuevo: uno cerrado no se reabre.
				NewPub: func() (relay.Publisher, error) {
					return rtmpio.NewPublisher(rtmpio.PublisherConfig{
						URL: url, StreamKey: key, Logger: logger,
					})
				},
				Logger: logger,
				OnEvent: func(ev relay.EngineEvent) {
					if _, err := db.LogEvent(ctx, store.Event{
						DestinationID: ev.DestinationID,
						Level:         store.Level(ev.Level),
						Kind:          ev.Kind,
						Message:       ev.Message,
					}); err != nil {
						logger.Error("no se pudo registrar el evento del destino", "err", err)
					}
				},
			}))
		}
		logger.Info("destinos de la sesión", "n", len(out))
		return out, nil
	})
```

Fíjate en que `url`, `name`, `id` y `key` se copian a variables locales dentro del bucle:
sin eso, todas las clausuras compartirían el último destino.

Y en el apagado, dale a los sinks los 3 s de gracia del spec §6.5. Sustituye el bloque que
va desde `<-ctx.Done()` hasta el `return nil` por:

```go
	<-ctx.Done()
	logger.Info("apagando")

	if err := ingest.Close(); err != nil {
		logger.Error("cerrar la ingesta", "err", err)
	}

	// Cerrar la ingesta corta los sockets, pero go-rtmp atiende cada conexión en su
	// propia goroutine y esa todavía tiene que disparar OnPublishEnd, que cierra la
	// sesión en la base y para los sinks. Sin esta espera el proceso puede salir antes,
	// dejando la sesión abierta para siempre.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := engine.WaitIdle(shutdownCtx); err != nil {
		logger.Warn("la sesión no llegó a cerrarse durante el apagado", "err", err)
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
```

Y **el cambio que da la gracia real a los sinks**: el `BaseContext` del engine no puede ser
el `ctx` de señales, porque ese se cancela al llegar SIGTERM y mata a los sinks antes de que
puedan cerrar ordenadamente. Crea uno propio:

```go
	// Los sinks NO heredan el contexto de señales: si lo hicieran, un SIGTERM los mataría
	// antes de que el cierre ordenado del spec §6.5 pudiera mandar su FCUnpublish. Este
	// contexto se cancela al final, tras la espera.
	sinkCtx, cancelSinks := context.WithCancel(context.Background())
	defer cancelSinks()
```

Pásalo como `BaseContext: sinkCtx` en el `relay.EngineConfig`, y llama a `cancelSinks()`
justo **después** de `hub.Close()` en el apagado.

- [ ] **Step 5: Verificación completa**

```bash
go vet ./...
go test ./... -race -count=1 -timeout 600s
CGO_ENABLED=0 go build -o splitstream ./cmd/splitstream
go list -deps ./internal/relay | grep -E 'go-rtmp|database/sql'
```
Expected: todo verde y el último comando vacío.

- [ ] **Step 6: Commit**

```bash
git add cmd/splitstream/main.go internal/rtmpio/publisher.go internal/rtmpio/publisher_test.go
git commit -m "feat: N destinos simultáneos, FCUnpublish y gracia de apagado"
```

---

### Task 8: Tests de integración de fan-out y reconexión

Lo que el spec §11 pide: publicar hacia dos sinks a la vez, y matar uno a media transmisión
comprobando que se reconecta sin tumbar al otro.

**Files:**
- Create: `test/integration/fanout_test.go`
- Modify: `test/integration/relay_test.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: todo lo anterior.
- Produces: `TestFanOutToTwoSinks` y `TestReconnectAfterSinkDies`.

- [ ] **Step 1: Arreglar el orden de los `defer` en el test existente**

En `test/integration/relay_test.go`, la base se cierra antes de que la ingesta termine de
procesar el cierre de la conexión, lo que imprime un `database is closed` al final. Mueve el
cierre de la ingesta **antes** del de la base: sustituye

```go
	defer db.Close()
```

por un cierre explícito al final del test, después de `ingest.Close()`, o registra los
`defer` en el orden inverso al de creación de forma que `db.Close()` sea el **último** en
registrarse y por tanto el **primero** en ejecutarse... que es justo lo contrario de lo que
queremos. Lo más claro es no usar `defer` para la base:

```go
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// La base se cierra a mano al final, después de la ingesta: si se cerrara antes, el
	// OnPublishEnd que dispara el cierre de la conexión fallaría al registrar su evento.
	defer func() {
		ingest.Close()
		time.Sleep(300 * time.Millisecond)
		db.Close()
	}()
```

Ajusta el resto del test para que `ingest` esté declarada antes de ese `defer`.

- [ ] **Step 2: Escribir el test de fan-out**

`test/integration/fanout_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
)

const sinkB = "rtmp://localhost:19352/live"

// probeStream lee del sink y devuelve la salida de ffprobe.
func probeStream(t *testing.T, ctx context.Context, url, out string) string {
	t.Helper()
	rec := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-rw_timeout", "15000000", "-i", url, "-t", "3", "-c", "copy", "-y", out)
	if b, err := rec.CombinedOutput(); err != nil {
		t.Fatalf("no se pudo leer de %s: %v\n%s", url, err, b)
	}
	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name,codec_type", "-of", "default=noprint_wrappers=1", out)
	b, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe sobre %s: %v\n%s", out, err, b)
	}
	return string(b)
}

// TestFanOutToTwoSinks comprueba lo que pide el spec §11: un stream entra y sale hacia dos
// destinos a la vez, con vídeo y audio decodificables en ambos.
func TestFanOutToTwoSinks(t *testing.T) {
	requireTool(t, "ffmpeg")
	requireTool(t, "ffprobe")
	requireSink(t, "localhost:19351")
	requireSink(t, "localhost:19352")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	name := fmt.Sprintf("fan%d", time.Now().UnixNano())
	hub := relay.NewHub(nil)
	defer hub.Close()

	for i, base := range []string{sinkA, sinkB} {
		url, id := base, int64(i+1)
		s := relay.NewSink(relay.SinkConfig{
			ID: id, Name: fmt.Sprintf("sink-%d", id),
			NewPub: func() (relay.Publisher, error) {
				return rtmpio.NewPublisher(rtmpio.PublisherConfig{
					URL: url, StreamKey: crypto.Secret(name),
				})
			},
		})
		s.Start(ctx, hub.Preamble())
		hub.Add(s)
	}

	stop := startIngestAndPublish(t, ctx, hub, name, 25)
	defer stop()

	time.Sleep(6 * time.Second)

	dir := t.TempDir()
	for i, base := range []string{sinkA, sinkB} {
		got := probeStream(t, ctx, fmt.Sprintf("%s/%s", base, name),
			filepath.Join(dir, fmt.Sprintf("out%d.flv", i)))
		t.Logf("sink %d:\n%s", i+1, got)
		for _, want := range []string{"codec_name=h264", "codec_name=aac"} {
			if !strings.Contains(got, want) {
				t.Errorf("sink %d: falta %q en\n%s", i+1, want, got)
			}
		}
	}

	snap := hub.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("el hub reporta %d destinos, quería 2", len(snap))
	}
	for id, m := range snap {
		if m.State != "live" {
			t.Errorf("destino %d en estado %q, quería live", id, m.State)
		}
		if m.BytesSent == 0 {
			t.Errorf("destino %d no envió bytes", id)
		}
		if m.BitrateBPS == 0 {
			t.Errorf("destino %d reporta bitrate 0", id)
		}
	}
}

// TestReconnectAfterSinkDies mata un sink a media transmisión y comprueba que se reconecta
// sin tumbar al otro (spec §11).
func TestReconnectAfterSinkDies(t *testing.T) {
	requireTool(t, "ffmpeg")
	requireTool(t, "docker")
	requireSink(t, "localhost:19351")
	requireSink(t, "localhost:19352")

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	name := fmt.Sprintf("rec%d", time.Now().UnixNano())
	hub := relay.NewHub(nil)
	defer hub.Close()

	for i, base := range []string{sinkA, sinkB} {
		url, id := base, int64(i+1)
		s := relay.NewSink(relay.SinkConfig{
			ID: id, Name: fmt.Sprintf("sink-%d", id),
			NewPub: func() (relay.Publisher, error) {
				return rtmpio.NewPublisher(rtmpio.PublisherConfig{
					URL: url, StreamKey: crypto.Secret(name),
				})
			},
		})
		s.Start(ctx, hub.Preamble())
		hub.Add(s)
	}

	stop := startIngestAndPublish(t, ctx, hub, name, 90)
	defer stop()

	time.Sleep(8 * time.Second)
	before := hub.Snapshot()
	for id, m := range before {
		if m.State != "live" {
			t.Fatalf("antes de matar nada, el destino %d está en %q", id, m.State)
		}
	}
	survivorBytes := before[1].BytesSent

	// Matar el sink B.
	t.Log("reiniciando splitstream-test-sink-b")
	if b, err := exec.CommandContext(ctx, "docker", "restart", "-t", "0",
		"splitstream-test-sink-b").CombinedOutput(); err != nil {
		t.Fatalf("docker restart: %v\n%s", err, b)
	}

	// El destino B debe salir de live.
	deadline := time.Now().Add(30 * time.Second)
	var sawDown bool
	for time.Now().Before(deadline) {
		if s := hub.Snapshot(); s[2].State != "live" {
			sawDown = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !sawDown {
		t.Error("el destino B nunca salió de live tras reiniciarse su servidor")
	}

	// Y debe volver por sí solo.
	deadline = time.Now().Add(90 * time.Second)
	var recovered bool
	for time.Now().Before(deadline) {
		if s := hub.Snapshot(); s[2].State == "live" && s[2].Reconnections > 0 {
			recovered = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !recovered {
		t.Errorf("el destino B no se reconectó: %+v", hub.Snapshot()[2])
	}

	// Y el destino A no se enteró: siguió enviando todo el tiempo.
	after := hub.Snapshot()
	if after[1].State != "live" {
		t.Errorf("el destino A quedó en %q: la caída de B lo afectó", after[1].State)
	}
	if after[1].BytesSent <= survivorBytes {
		t.Errorf("el destino A dejó de enviar durante la caída de B: %d → %d",
			survivorBytes, after[1].BytesSent)
	}
	if after[1].Reconnections != 0 {
		t.Errorf("el destino A se reconectó %d veces sin motivo", after[1].Reconnections)
	}
}
```

- [ ] **Step 3: Escribir el helper compartido**

Añade a `test/integration/fanout_test.go` el helper que levanta la ingesta y publica:

```go
// startIngestAndPublish levanta la ingesta sobre un puerto efímero, arranca ffmpeg
// publicando contra ella durante `seconds`, y devuelve una función de parada.
func startIngestAndPublish(t *testing.T, ctx context.Context, hub *relay.Hub, key string, seconds int) func() {
	t.Helper()

	engine := relay.NewEngine(relay.EngineConfig{
		Hub: hub, Store: nopStore{}, BaseContext: ctx,
	})
	engine.SetValidator(func(app, k string) error {
		if app == "live" && k == key {
			return nil
		}
		return rtmpio.ErrBadStreamKey
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := rtmpio.NewIngest(rtmpio.IngestConfig{Addr: ln.Addr().String(), Handler: engine})
	go ing.Serve(ln)
	time.Sleep(300 * time.Millisecond)

	pubURL := fmt.Sprintf("rtmp://%s/live/%s", ln.Addr().String(), key)
	ff := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-re", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", fmt.Sprint(seconds), "-c:v", "libx264", "-preset", "ultrafast",
		"-pix_fmt", "yuv420p", "-g", "30", "-b:v", "800k",
		"-c:a", "aac", "-b:a", "128k", "-ar", "44100", "-f", "flv", pubURL)
	if err := ff.Start(); err != nil {
		t.Fatalf("arrancar ffmpeg: %v", err)
	}

	return func() {
		if ff.Process != nil {
			ff.Process.Kill()
		}
		ing.Close()
	}
}

// nopStore satisface relay.EngineStore sin persistir nada: estos tests miden el motor,
// no la base.
type nopStore struct{}

func (nopStore) StartSession(ctx context.Context) (int64, error) { return 1, nil }
func (nopStore) FinishSession(ctx context.Context, id int64, w, h, b int) error { return nil }
func (nopStore) LogEvent(ctx context.Context, e relay.EngineEvent) error { return nil }
```

- [ ] **Step 4: Ejecutar los tests de integración**

```bash
make sinks-up
sleep 5
make test-integration
make sinks-down
```

Expected: `TestRelayEndToEnd`, `TestFanOutToTwoSinks` y `TestReconnectAfterSinkDies` pasan.
El de reconexión tarda ~2 minutos porque espera al backoff.

**No debilites estos tests para que pasen.** Si el de reconexión falla, mira las métricas
que el propio test imprime y `docker logs splitstream-test-sink-b`. Si no logras que pase,
repórtalo como BLOCKED con el diagnóstico.

- [ ] **Step 5: Subir el timeout del target de integración en el Makefile**

```makefile
# Requiere sinks-up, ffmpeg y ffprobe. El test de reconexión espera al backoff, que llega
# a 30 s entre intentos.
test-integration:
	go test -tags integration ./test/integration/ -v -count=1 -timeout 15m
```

- [ ] **Step 6: Commit**

```bash
git add test/integration/ Makefile
git commit -m "test: fan-out a dos destinos y reconexión tras caída de un sink"
```

---

## Definición de terminado, fase 3

> Verificada entera el 2026-09-02 sobre `main` en 3ddda9e. Las evidencias, comando por
> comando, están en el ledger: `docs/superpowers/plans/2026-09-02-fase-3-ledger.md`.


- [x] `go test ./... -race -count=1` pasa entero.
- [x] `go vet ./...` limpio.
- [x] `CGO_ENABLED=0 go build ./cmd/splitstream` produce el binario.
- [x] `go.mod` sigue con exactamente tres directas y `go 1.25.0`.
- [x] `go list -deps ./internal/relay | grep -E 'go-rtmp|database/sql'` vacío.
- [x] `make sinks-up && make test-integration` pasa los tres tests, incluidos el de fan-out
      a dos destinos y el de reconexión tras matar uno.
- [x] Un destino lento descarta **GOPs completos**, se marca `degraded`, y no bloquea al
      publisher ni a sus hermanos.
- [x] Un destino caído reconecta con backoff de 1 s a 30 s y reenvía el preámbulo.
- [x] Las métricas de cada destino reportan bytes, bitrate, descartes, uptime, reconexiones
      y último error.
- [x] La resolución de la sesión sale del SPS y el bitrate es el medido.
- [x] El store aguanta 8 escritores y 4 lectores concurrentes sin perder escrituras.

## Notas para la fase 4

- `Hub.Snapshot()` devuelve `map[int64]Metrics`: es exactamente lo que `GET /api/status` y
  el WebSocket tienen que serializar. `Metrics` **no tiene tags `json`** todavía; decidir si
  se añaden ahí o si la fase 4 escribe DTOs (spec §15.2).
- Sigue pendiente la taxonomía de errores del spec §15.3: las validaciones del store
  devuelven `errors.New` pelados, así que la API no podrá distinguir un 400 de un 500 sin
  comparar cadenas. `ErrInvalidDestinationURL` de la fase 2 es el primer centinela de
  validación; conviene generalizarlo a `ErrInvalidInput`.
- El orden lexicográfico de los timestamps TEXT no es cronológico (spec §15.4). Los índices
  `idx_events_created` e `idx_sessions_started` invitan a la consulta que sale mal.
- La auditoría del revelado de claves (spec §15.5) sigue siendo una convención, no un
  invariante.
- `store.GenerateKey()` sigue con nombre genérico (spec §15.8).
- Las tres funciones con `UPDATE` sin `RowsAffected` de la fase 1 ya se arreglaron; el
  patrón está resuelto.
- **Nada se ha probado contra una plataforma real.** Antes de cerrar la fase 3 conviene
  probar YouTube, Twitch y Facebook: `releaseStream`/`FCPublish` se mandan sobre el stream
  ya creado y con `TransactionID: 0`, mientras FMLE los manda sobre el stream 0 y antes de
  `createStream` (spec §14).
