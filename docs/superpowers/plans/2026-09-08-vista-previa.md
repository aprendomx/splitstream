# Vista Previa Silenciada — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Un monitor de vídeo en vivo en el panel: botón «Vista previa», WebSocket binario con los NALUs H.264 tal cual, decodificación con WebCodecs y pintado en canvas. Sin audio, sin transcodificar, sin dependencias nuevas.

**Architecture:** El `Hub` gana *taps* (consumidores de solo lectura, canal con buffer, descarte con reenganche en keyframe). Un endpoint `GET /api/preview/ws` abre un tap, manda primero el `avcC` (mensaje `0x01`) y después cada frame de vídeo (`0x02`) recortando los 5 bytes de cabecera FLV. El cliente Vue configura un `VideoDecoder` con ese `avcC` y pinta cada `VideoFrame` en un `<canvas>`. `*Sink` no se toca.

**Tech Stack:** Go (stdlib + `github.com/coder/websocket` ya presente), Vue 3 + Quasar (ya presentes), WebCodecs (API del navegador).

**Spec:** `docs/superpowers/specs/2026-09-08-vista-previa-design.md`

## Global Constraints

- **Cero dependencias nuevas**: ni módulos Go ni paquetes npm. La CI comprueba además que `internal/httpapi` no importe go-rtmp ni transitivamente.
- **Comentarios, mensajes de commit, copys de interfaz y errores en español**, con el estilo del código existente (los comentarios explican el porqué, no el qué).
- **Tests con `-race`**: la suite corre `go test ./... -race -count=1` (`make test`). Los tests nuevos de `relay` deben pasar también con `GOMAXPROCS=2`.
- **Rutas con método** del mux de Go 1.22, registradas todas en `routes()` (`internal/httpapi/server.go:138`), las protegidas vía `protegida(...)`.
- **El payload de `relay.Message` es inmutable y compartido**: nunca escribir en él; recortar es tomar un subslice o copiar.
- Rama de trabajo: `feat/vista-previa` desde `main`.

---

### Task 1: Taps de solo lectura en el Hub

**Files:**
- Create: `internal/relay/tap.go`
- Create: `internal/relay/tap_test.go`
- Modify: `internal/relay/hub.go` (struct `Hub`, `NewHub`, `Publish`, `Close`)

**Interfaces:**
- Consumes: `relay.Message`, `relay.Hub` existentes.
- Produces: `func (h *Hub) Tap() (<-chan *Message, func())` — canal de mensajes de vídeo y `release` idempotente. Garantías: el primer mensaje no-seq-header que sale del canal es un keyframe; tras un descarte no sale nada hasta el siguiente keyframe; los sequence headers de vídeo pasan siempre; audio y meta no entran; `Hub.Close()` y `release()` cierran el canal. Constante `tapBuffer = 64`.

- [ ] **Step 1: Crear la rama**

```bash
git checkout -b feat/vista-previa main
```

- [ ] **Step 2: Escribir los tests que fallan**

Crear `internal/relay/tap_test.go`:

```go
package relay

import (
	"sync"
	"testing"
	"time"
)

func tapVideo(ts uint32, key bool) *Message {
	return &Message{Kind: KindVideo, Timestamp: ts, IsKeyframe: key, Payload: []byte{0x17, 0x01}}
}

// Los sequence headers reales llegan con el bit de keyframe puesto (0x17), y es lo que
// InspectVideo marca; el test los fabrica igual para no probar contra un caso irreal.
func tapSeqHeader() *Message {
	return &Message{Kind: KindVideo, IsSeqHeader: true, IsKeyframe: true, Payload: []byte{0x17, 0x00}}
}

func recvTap(t *testing.T, ch <-chan *Message) *Message {
	t.Helper()
	select {
	case m, ok := <-ch:
		if !ok {
			t.Fatal("el canal del tap se cerró antes de tiempo")
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("el tap no entregó nada en 2 s")
	}
	return nil
}

func expectNothing(t *testing.T, ch <-chan *Message) {
	t.Helper()
	select {
	case m, ok := <-ch:
		if !ok {
			t.Fatal("el canal del tap se cerró antes de tiempo")
		}
		t.Fatalf("el tap entregó un mensaje que no debía: ts=%d key=%v seq=%v",
			m.Timestamp, m.IsKeyframe, m.IsSeqHeader)
	default:
	}
}

// Un decodificador no puede arrancar en mitad de un GOP: lo primero que sale de un tap
// es siempre un keyframe.
func TestTapStartsAtAKeyframe(t *testing.T) {
	hub := NewHub(nil)
	ch, release := hub.Tap()
	defer release()

	hub.Publish(tapVideo(1, false))
	hub.Publish(tapVideo(2, true))
	hub.Publish(tapVideo(3, false))

	got := recvTap(t, ch)
	if !got.IsKeyframe || got.Timestamp != 2 {
		t.Fatalf("el primer mensaje fue ts=%d key=%v; quería el keyframe ts=2", got.Timestamp, got.IsKeyframe)
	}
	if got := recvTap(t, ch); got.Timestamp != 3 {
		t.Fatalf("tras el keyframe llegó ts=%d, quería 3", got.Timestamp)
	}
}

// La vista es silenciada por diseño: el audio ni entra al tap. El meta tampoco: el
// cliente no lo necesita. Los sequence headers de vídeo pasan SIEMPRE, incluso mientras
// el tap espera keyframe, porque llevan la config de una renegociación.
func TestTapIgnoresAudioAndMetaButPassesVideoSeqHeaders(t *testing.T) {
	hub := NewHub(nil)
	ch, release := hub.Tap()
	defer release()

	hub.Publish(&Message{Kind: KindAudio, Payload: []byte{0xaf, 0x01}})
	hub.Publish(&Message{Kind: KindMeta, Payload: []byte{0x02}})
	hub.Publish(tapSeqHeader())

	if got := recvTap(t, ch); !got.IsSeqHeader {
		t.Fatalf("quería el sequence header, llegó otra cosa: %+v", got)
	}
	expectNothing(t, ch)
}

// Con el buffer lleno se descarta, y tras el descarte no sale nada hasta el siguiente
// keyframe: entregar un delta con su pasado descartado le daría al decodificador un GOP
// roto.
func TestTapDropsAndResumesAtAKeyframe(t *testing.T) {
	hub := NewHub(nil)
	ch, release := hub.Tap()
	defer release()

	hub.Publish(tapVideo(0, true))
	for i := 1; i <= tapBuffer+10; i++ { // desborda el buffer a propósito
		hub.Publish(tapVideo(uint32(i), false))
	}

	// Se drena todo lo que el buffer retuvo.
	for {
		select {
		case <-ch:
			continue
		default:
		}
		break
	}

	hub.Publish(tapVideo(100, false)) // el tap está esperando keyframe: no debe salir
	expectNothing(t, ch)

	hub.Publish(tapVideo(101, true))
	if got := recvTap(t, ch); !got.IsKeyframe || got.Timestamp != 101 {
		t.Fatalf("tras el descarte llegó ts=%d key=%v; quería el keyframe 101", got.Timestamp, got.IsKeyframe)
	}
}

// El fin de la sesión cierra los taps, y release es idempotente y convive con Close:
// cerrar dos veces un canal es un panic, así que esto protege el apagado.
func TestHubCloseClosesTapsAndReleaseIsIdempotent(t *testing.T) {
	hub := NewHub(nil)
	ch, release := hub.Tap()

	hub.Close()
	if _, ok := <-ch; ok {
		t.Fatal("Close no cerró el canal del tap")
	}
	release()
	release() // segunda llamada: no debe hacer nada, y menos panic

	ch2, release2 := hub.Tap()
	release2()
	if _, ok := <-ch2; ok {
		t.Fatal("release no cerró el canal del tap")
	}
	hub.Close() // el tap ya liberado no debe hacer panic aquí
}

// Publish jamás bloquea por un tap, y abrir/soltar taps mientras se publica no puede
// tener carreras: este test existe sobre todo para el detector de -race.
func TestTapsDoNotBlockOrRacePublish(t *testing.T) {
	hub := NewHub(nil)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			hub.Publish(tapVideo(uint32(i), i%30 == 0))
		}
	}()

	for i := 0; i < 50; i++ {
		ch, release := hub.Tap()
		var drena sync.WaitGroup
		drena.Add(1)
		go func() {
			defer drena.Done()
			for range ch { //nolint:revive // drenar hasta que release cierre el canal
			}
		}()
		time.Sleep(time.Millisecond)
		release()
		drena.Wait()
	}

	close(stop)
	wg.Wait()
	hub.Close()
}
```

- [ ] **Step 3: Comprobar que fallan**

Run: `go test ./internal/relay/ -run 'TestTap|TestHubCloseClosesTaps' -race -count=1`
Expected: FAIL de compilación con "hub.Tap undefined" y "undefined: tapBuffer".

- [ ] **Step 4: Implementar el tap**

Crear `internal/relay/tap.go`:

```go
package relay

import (
	"sync"
	"sync/atomic"
)

// tapBuffer es el tamaño del buffer de cada tap. A 30 fps son unos dos segundos de
// margen; si quien mira no drena a esa velocidad, mejor descartar y reenganchar en el
// siguiente keyframe que acumular retraso.
const tapBuffer = 64

// tap es un consumidor de solo lectura del hub: la vista previa del panel. A diferencia
// de un Sink no tiene cola persistente, ni reconexión, ni conexión saliente — nace al
// abrirse el WebSocket de la vista previa y muere al cerrarse.
type tap struct {
	ch chan *Message
	// waiting marca que el tap espera un keyframe: al nacer, y tras cada descarte.
	// Entregar un delta con su pasado descartado le daría al decodificador un GOP roto.
	// Es atomic y no un campo bajo el mutex del hub porque deliver corre bajo el RLock
	// de Publish, donde no se puede escribir estado compartido protegido por ese lock.
	waiting atomic.Bool
	// once protege el close del canal: release y Hub.Close pueden coincidir, y cerrar
	// dos veces un canal es un panic.
	once sync.Once
}

func newTap() *tap {
	t := &tap{ch: make(chan *Message, tapBuffer)}
	t.waiting.Store(true)
	return t
}

func (t *tap) close() { t.once.Do(func() { close(t.ch) }) }

// deliver entrega un mensaje sin bloquear jamás: la vista previa no puede frenar al
// publisher ni a los sinks. Solo pasa vídeo — la vista es silenciada por diseño, así que
// el audio ni entra; el meta tampoco porque el cliente no lo usa. Los sequence headers
// pasan siempre, incluso esperando keyframe: llevan la config de una renegociación y sin
// ella el cliente no puede decodificar lo que venga después.
func (t *tap) deliver(msg *Message) {
	if msg.Kind != KindVideo {
		return
	}
	if t.waiting.Load() && !msg.IsKeyframe && !msg.IsSeqHeader {
		return
	}
	select {
	case t.ch <- msg:
		if !msg.IsSeqHeader {
			t.waiting.Store(false)
		}
	default:
		t.waiting.Store(true)
	}
}

// Tap registra un consumidor de solo lectura y devuelve su canal y una función release
// idempotente que lo da de baja y cierra el canal. El canal también se cierra cuando la
// sesión termina (Hub.Close).
func (h *Hub) Tap() (<-chan *Message, func()) {
	t := newTap()
	h.mu.Lock()
	h.taps[t] = struct{}{}
	h.mu.Unlock()

	release := func() {
		h.mu.Lock()
		delete(h.taps, t)
		h.mu.Unlock()
		// Fuera del lock, y solo tras quitarlo del mapa: así ningún Publish en vuelo
		// puede escribir en un canal cerrado — deliver solo se alcanza desde el mapa, y
		// el Lock de arriba espera a que ese RLock suelte.
		t.close()
	}
	return t.ch, release
}
```

Modificar `internal/relay/hub.go`:

En el struct `Hub` (tras `sinks map[int64]*Sink`, `hub.go:21`):

```go
	taps  map[*tap]struct{}
```

En `NewHub` (`hub.go:29`):

```go
	return &Hub{log: logger, sinks: map[int64]*Sink{}, taps: map[*tap]struct{}{}}
```

En `Publish`, dentro del RLock, tras el bucle de sinks (`hub.go:84`):

```go
	for t := range h.taps {
		t.deliver(msg)
	}
```

En `Close`, justo antes de `h.pre.Reset()` (`hub.go:153`):

```go
	// Los taps de la vista previa mueren con la sesión: el lado HTTP ve el canal
	// cerrado y cierra su WebSocket con «la emisión terminó».
	h.mu.Lock()
	taps := make([]*tap, 0, len(h.taps))
	for t := range h.taps {
		taps = append(taps, t)
	}
	h.taps = map[*tap]struct{}{}
	h.mu.Unlock()
	for _, t := range taps {
		t.close()
	}
```

- [ ] **Step 5: Comprobar que pasan, también con pocos núcleos**

Run: `go test ./internal/relay/ -race -count=1 && GOMAXPROCS=2 go test ./internal/relay/ -race -count=1`
Expected: PASS entero (los tests viejos del hub incluidos).

- [ ] **Step 6: Commit**

```bash
git add internal/relay/tap.go internal/relay/tap_test.go internal/relay/hub.go
git commit -m "feat(relay): taps de solo lectura en el hub para la vista previa"
```

---

### Task 2: El motor expone el tap y la config de vídeo

**Files:**
- Modify: `internal/relay/engine.go` (añadir dos métodos al final)
- Create/extend: `internal/relay/engine_tap_test.go`
- Modify: `internal/httpapi/server.go:37-51` (interfaz `EngineView`)
- Modify: `internal/httpapi/destinations_test.go:22-86` (`fakeEngine`)

**Interfaces:**
- Consumes: `Hub.Tap()` (Task 1), `Hub.Preamble().Snapshot()` existente.
- Produces: `func (e *Engine) Tap() (<-chan *Message, func())` y `func (e *Engine) VideoConfig() []byte` (payload FLV completo del AVC sequence header, `nil` si aún no llegó). `EngineView` gana esos dos métodos con firma `Tap() (<-chan *relay.Message, func())` y `VideoConfig() []byte`. `fakeEngine` gana: campos `tapCh chan *relay.Message`, `tapLiberados int`, `videoCfg []byte`; métodos `Tap()`, `VideoConfig()`; helpers `setVideoConfig(p []byte)`, `emitir(m *relay.Message)`, `cerrarTap()`, `liberados() int`.

- [ ] **Step 1: Escribir el test que falla**

Crear `internal/relay/engine_tap_test.go`:

```go
package relay

import (
	"bytes"
	"testing"
	"time"
)

// La API necesita dos cosas del motor para la vista previa: el sequence header de vídeo
// (que lleva el avcC) y un tap. Las dos delegan en el hub; este test fija el contrato.
func TestEngineExposesVideoConfigAndTap(t *testing.T) {
	hub := NewHub(nil)
	e := NewEngine(EngineConfig{Hub: hub})

	if got := e.VideoConfig(); got != nil {
		t.Fatalf("sin sequence header, VideoConfig() = %x; quería nil", got)
	}

	seq := &Message{Kind: KindVideo, IsSeqHeader: true, IsKeyframe: true,
		Payload: []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x64, 0x00, 0x1f}}
	hub.Publish(seq)
	if got := e.VideoConfig(); !bytes.Equal(got, seq.Payload) {
		t.Fatalf("VideoConfig() = %x; quería el payload del sequence header %x", got, seq.Payload)
	}

	ch, release := e.Tap()
	defer release()
	hub.Publish(&Message{Kind: KindVideo, IsKeyframe: true, Payload: []byte{0x17, 0x01}})
	select {
	case m := <-ch:
		if !m.IsKeyframe {
			t.Fatalf("del tap salió %+v; quería el keyframe", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("el tap del motor no entregó el keyframe")
	}
}
```

- [ ] **Step 2: Comprobar que falla**

Run: `go test ./internal/relay/ -run TestEngineExposesVideoConfigAndTap -race -count=1`
Expected: FAIL de compilación con "e.VideoConfig undefined".

- [ ] **Step 3: Implementar en el motor**

Añadir al final de `internal/relay/engine.go`:

```go
// Tap abre un grifo de solo lectura sobre el hub para la vista previa del panel. Va en
// el motor y no en el hub directamente por la misma razón que AddSink: la API habla con
// EngineView y no debe conocer al hub.
func (e *Engine) Tap() (<-chan *Message, func()) { return e.hub.Tap() }

// VideoConfig devuelve el payload FLV del AVC sequence header de la sesión en curso, o
// nil si todavía no llegó. Es lo primero que la vista previa manda al navegador: dentro
// va el avcC sin el que WebCodecs no puede decodificar nada.
func (e *Engine) VideoConfig() []byte {
	_, videoSeq, _ := e.hub.Preamble().Snapshot()
	if videoSeq == nil {
		return nil
	}
	return videoSeq.Payload
}
```

- [ ] **Step 4: Ampliar EngineView y el fake**

En `internal/httpapi/server.go`, dentro de la interfaz `EngineView` (tras `RemoveSink(id int64)`, línea 50):

```go
	// Tap y VideoConfig alimentan la vista previa (spec vista previa §3 y §5): un grifo
	// de solo lectura sobre el hub y el sequence header con el avcC. En el fake de los
	// tests son un canal y un slice que el test controla.
	Tap() (<-chan *relay.Message, func())
	VideoConfig() []byte
```

En `internal/httpapi/destinations_test.go`, añadir campos al struct `fakeEngine` (tras `removed []int64`, línea 28):

```go
	tapCh        chan *relay.Message
	tapLiberados int
	videoCfg     []byte
```

Y estos métodos tras `setMetrics` (línea 86):

```go
// --- vista previa ---

func (f *fakeEngine) canalTap() chan *relay.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tapCh == nil {
		f.tapCh = make(chan *relay.Message, 64)
	}
	return f.tapCh
}

func (f *fakeEngine) Tap() (<-chan *relay.Message, func()) {
	return f.canalTap(), func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.tapLiberados++
	}
}

func (f *fakeEngine) VideoConfig() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.videoCfg
}

func (f *fakeEngine) setVideoConfig(p []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.videoCfg = p
}

// emitir mete un mensaje en el tap sin bloquear, como hace el hub de verdad: si el
// handler está atascado escribiendo, el mensaje se pierde, que es el comportamiento real.
func (f *fakeEngine) emitir(m *relay.Message) {
	select {
	case f.canalTap() <- m:
	default:
	}
}

func (f *fakeEngine) cerrarTap() { close(f.canalTap()) }

func (f *fakeEngine) liberados() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tapLiberados
}
```

- [ ] **Step 5: Comprobar que todo compila y pasa**

Run: `go test ./internal/relay/ ./internal/httpapi/ -race -count=1`
Expected: PASS (los tests de httpapi compilan de nuevo porque fakeEngine vuelve a cumplir EngineView).

- [ ] **Step 6: Commit**

```bash
git add internal/relay/engine.go internal/relay/engine_tap_test.go internal/httpapi/server.go internal/httpapi/destinations_test.go
git commit -m "feat(relay): el motor expone Tap y VideoConfig para la vista previa"
```

---

### Task 3: Codificadores del protocolo binario

**Files:**
- Create: `internal/httpapi/preview.go` (solo constantes y codificadores en esta task)
- Create: `internal/httpapi/preview_test.go`

**Interfaces:**
- Consumes: `relay.Message`.
- Produces: `previewConfigMsg(flvPayload []byte) []byte` (mensaje `0x01` + avcC, `nil` si el payload no da), `previewFrameMsg(m *relay.Message) []byte` (mensaje `0x02` + flags + timestamp BE32 + NALUs, `nil` si no da), constantes `previewMsgConfig = 0x01`, `previewMsgFrame = 0x02`, `flvVideoHeaderLen = 5`, `previewCloseEnded websocket.StatusCode = 4000`, `previewCloseNoSignal websocket.StatusCode = 4001`.

- [ ] **Step 1: Escribir los tests que fallan**

Crear `internal/httpapi/preview_test.go`:

```go
package httpapi

import (
	"bytes"
	"testing"

	"github.com/aprendomx/splitstream/internal/relay"
)

// Payloads FLV de mentira: 5 bytes de cabecera (frameType/codecID, AVCPacketType,
// composition time) y detrás lo que importa. Al servidor solo le interesa dónde empieza.
func seqPayload(avcc ...byte) []byte {
	return append([]byte{0x17, 0x00, 0x00, 0x00, 0x00}, avcc...)
}

func framePayload(nalus ...byte) []byte {
	return append([]byte{0x27, 0x01, 0x00, 0x00, 0x00}, nalus...)
}

// El mensaje de config es 0x01 + el avcC: el payload FLV sin sus 5 bytes de cabecera.
func TestPreviewConfigMsgStripsTheFLVHeader(t *testing.T) {
	avcc := []byte{0x01, 0x64, 0x00, 0x1f, 0xff}
	got := previewConfigMsg(seqPayload(avcc...))
	want := append([]byte{previewMsgConfig}, avcc...)
	if !bytes.Equal(got, want) {
		t.Fatalf("previewConfigMsg = %x; quería %x", got, want)
	}
}

// Un payload que no da ni para la cabecera FLV no es una config: nil, y quien llama
// decide (no mandar nada, o «sin señal»).
func TestPreviewConfigMsgRejectsAShortPayload(t *testing.T) {
	for _, p := range [][]byte{nil, {}, {0x17, 0x00, 0x00, 0x00, 0x00}} {
		if got := previewConfigMsg(p); got != nil {
			t.Fatalf("previewConfigMsg(%x) = %x; quería nil", p, got)
		}
	}
}

// El mensaje de frame es 0x02, flags (bit 0 = keyframe), timestamp en 4 bytes
// big-endian de milisegundos, y los NALUs AVCC tal cual.
func TestPreviewFrameMsgEncodesFlagsTimestampAndNALUs(t *testing.T) {
	m := &relay.Message{
		Kind: relay.KindVideo, Timestamp: 0x01020304, IsKeyframe: true,
		Payload: framePayload(0xaa, 0xbb, 0xcc),
	}
	got := previewFrameMsg(m)
	want := []byte{previewMsgFrame, 0x01, 0x01, 0x02, 0x03, 0x04, 0xaa, 0xbb, 0xcc}
	if !bytes.Equal(got, want) {
		t.Fatalf("previewFrameMsg = %x; quería %x", got, want)
	}

	m.IsKeyframe = false
	if got := previewFrameMsg(m); got[1] != 0x00 {
		t.Fatalf("flags de un delta = %#x; quería 0x00", got[1])
	}

	if got := previewFrameMsg(&relay.Message{Payload: []byte{0x27, 0x01}}); got != nil {
		t.Fatalf("un payload sin NALUs dio %x; quería nil", got)
	}
}
```

- [ ] **Step 2: Comprobar que fallan**

Run: `go test ./internal/httpapi/ -run TestPreview -race -count=1`
Expected: FAIL de compilación con "undefined: previewConfigMsg".

- [ ] **Step 3: Implementar los codificadores**

Crear `internal/httpapi/preview.go`:

```go
package httpapi

import (
	"github.com/coder/websocket"

	"github.com/aprendomx/splitstream/internal/relay"
)

// El protocolo de la vista previa (spec vista previa §4): mensajes binarios con 1 byte
// de tipo. La config lleva el avcC y los frames llevan los NALUs AVCC tal cual — el
// mismo formato que acepta VideoDecoder de WebCodecs, así que aquí no se parsea nada:
// se recorta la cabecera FLV y se reenvía.
const (
	previewMsgConfig byte = 0x01
	previewMsgFrame  byte = 0x02

	// flvVideoHeaderLen son los 5 bytes que preceden a los datos en un tag de vídeo AVC:
	// frameType/codecID, AVCPacketType y 3 de composition time.
	flvVideoHeaderLen = 5
)

// Códigos de cierre de aplicación: el cliente los enseña como motivo.
const (
	previewCloseEnded    websocket.StatusCode = 4000 // la emisión terminó
	previewCloseNoSignal websocket.StatusCode = 4001 // no hay emisión que enseñar
)

// previewConfigMsg construye el mensaje de config a partir del payload FLV del sequence
// header. Devuelve nil si el payload no da ni para la cabecera: sin avcC no hay nada que
// configurar.
func previewConfigMsg(flvPayload []byte) []byte {
	if len(flvPayload) <= flvVideoHeaderLen {
		return nil
	}
	out := make([]byte, 0, 1+len(flvPayload)-flvVideoHeaderLen)
	out = append(out, previewMsgConfig)
	return append(out, flvPayload[flvVideoHeaderLen:]...)
}

// previewFrameMsg construye el mensaje de un frame: flags, timestamp y NALUs. El
// timestamp viaja por si algún día hace falta; hoy el cliente pinta según decodifica.
func previewFrameMsg(m *relay.Message) []byte {
	if len(m.Payload) <= flvVideoHeaderLen {
		return nil
	}
	var flags byte
	if m.IsKeyframe {
		flags = 0x01
	}
	out := make([]byte, 0, 6+len(m.Payload)-flvVideoHeaderLen)
	out = append(out, previewMsgFrame, flags,
		byte(m.Timestamp>>24), byte(m.Timestamp>>16), byte(m.Timestamp>>8), byte(m.Timestamp))
	return append(out, m.Payload[flvVideoHeaderLen:]...)
}
```

- [ ] **Step 4: Comprobar que pasan**

Run: `go test ./internal/httpapi/ -run TestPreview -race -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/preview.go internal/httpapi/preview_test.go
git commit -m "feat(httpapi): protocolo binario de la vista previa"
```

---

### Task 4: El endpoint GET /api/preview/ws

**Files:**
- Modify: `internal/httpapi/preview.go` (añadir el handler)
- Modify: `internal/httpapi/server.go:170` (registrar la ruta junto a `GET /ws`)
- Create: `internal/httpapi/preview_ws_test.go`

**Interfaces:**
- Consumes: `previewConfigMsg`/`previewFrameMsg` (Task 3), `EngineView.Tap()/VideoConfig()/Session()` (Task 2), `wsWriteTimeout` de `ws.go:22`, helpers de test `newDestServer` (`destinations_test.go:102`) y `dialWS` (`ws_test.go:29`).
- Produces: handler `handlePreviewWS` en la ruta protegida `GET /api/preview/ws`.

- [ ] **Step 1: Escribir los tests que fallan**

Crear `internal/httpapi/preview_ws_test.go`:

```go
package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aprendomx/splitstream/internal/relay"
)

// previewServer levanta el servidor real sobre httptest y devuelve el fake del motor,
// la URL ws:// del endpoint y las cookies de una sesión iniciada.
func previewServer(t *testing.T) (*fakeEngine, string, []*http.Cookie) {
	t.Helper()
	srv, _, eng, _, cookies := newDestServer(t)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return eng, "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/preview/ws", cookies
}

func leerBinario(t *testing.T, ctx context.Context, conn *websocket.Conn) []byte {
	t.Helper()
	leer, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	tipo, data, err := conn.Read(leer)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tipo != websocket.MessageBinary {
		t.Fatalf("llegó un mensaje %v; el protocolo es binario", tipo)
	}
	return data
}

// El handshake lleva la cookie: sin sesión no hay upgrade, igual que en el WS de estado.
func TestPreviewRequiresASession(t *testing.T) {
	_, url, _ := previewServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, resp, err := dialWS(ctx, url, nil)
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("la vista previa aceptó una conexión sin sesión")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("código = %d, quería 401", resp.StatusCode)
	}
}

// Sin emisión no hay nada que enseñar: se acepta y se cierra en seguida con el código de
// aplicación, que cubre además la carrera de pulsar el botón justo cuando se corta.
func TestPreviewClosesWithoutSignal(t *testing.T) {
	_, url, cookies := previewServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	leer, cancelLeer := context.WithTimeout(ctx, 4*time.Second)
	_, _, err = conn.Read(leer)
	cancelLeer()
	if err == nil {
		t.Fatal("sin emisión llegó un mensaje; quería el cierre «sin señal»")
	}
	if got := websocket.CloseStatus(err); got != previewCloseNoSignal {
		t.Fatalf("código de cierre = %d; quería %d", got, previewCloseNoSignal)
	}
}

// Con emisión: lo primero es la config (0x01 + avcC) y después cada frame (0x02) con
// flags, timestamp big-endian y los NALUs, que son el payload FLV sin sus 5 bytes.
func TestPreviewSendsConfigThenFrames(t *testing.T) {
	eng, url, cookies := previewServer(t)
	avcc := []byte{0x01, 0x64, 0x00, 0x1f, 0xff}
	eng.setLive(7)
	eng.setVideoConfig(seqPayload(avcc...))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	config := leerBinario(t, ctx, conn)
	if want := append([]byte{previewMsgConfig}, avcc...); !bytes.Equal(config, want) {
		t.Fatalf("primer mensaje = %x; quería la config %x", config, want)
	}

	eng.emitir(&relay.Message{
		Kind: relay.KindVideo, Timestamp: 0x0102, IsKeyframe: true,
		Payload: framePayload(0xaa, 0xbb),
	})
	frame := leerBinario(t, ctx, conn)
	want := []byte{previewMsgFrame, 0x01, 0x00, 0x00, 0x01, 0x02, 0xaa, 0xbb}
	if !bytes.Equal(frame, want) {
		t.Fatalf("frame = %x; quería %x", frame, want)
	}
}

// Si el publisher renegocia a mitad, el sequence header nuevo atraviesa el tap y sale
// como una segunda config por el mismo WebSocket.
func TestPreviewResendsConfigOnRenegotiation(t *testing.T) {
	eng, url, cookies := previewServer(t)
	eng.setLive(7)
	eng.setVideoConfig(seqPayload(0x01, 0x64, 0x00, 0x1f))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	leerBinario(t, ctx, conn) // la config inicial

	avcc2 := []byte{0x01, 0x64, 0x00, 0x28}
	eng.emitir(&relay.Message{
		Kind: relay.KindVideo, IsSeqHeader: true, IsKeyframe: true,
		Payload: seqPayload(avcc2...),
	})
	got := leerBinario(t, ctx, conn)
	if want := append([]byte{previewMsgConfig}, avcc2...); !bytes.Equal(got, want) {
		t.Fatalf("tras renegociar llegó %x; quería la config nueva %x", got, want)
	}
}

// El fin de la emisión cierra el tap, y el cliente ve el código «la emisión terminó».
func TestPreviewClosesWhenTheSessionEnds(t *testing.T) {
	eng, url, cookies := previewServer(t)
	eng.setLive(7)
	eng.setVideoConfig(seqPayload(0x01, 0x64, 0x00, 0x1f))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	leerBinario(t, ctx, conn) // la config inicial

	eng.cerrarTap()

	leer, cancelLeer := context.WithTimeout(ctx, 4*time.Second)
	_, _, err = conn.Read(leer)
	cancelLeer()
	if got := websocket.CloseStatus(err); got != previewCloseEnded {
		t.Fatalf("código de cierre = %d (err %v); quería %d", got, err, previewCloseEnded)
	}
}

// Cuando el cliente se va, el tap se libera: sin esto cada vista abierta y abandonada
// dejaría un consumidor colgado del hub durante toda la emisión.
func TestPreviewReleasesTheTapWhenTheClientLeaves(t *testing.T) {
	eng, url, cookies := previewServer(t)
	eng.setLive(7)
	eng.setVideoConfig(seqPayload(0x01, 0x64, 0x00, 0x1f))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	leerBinario(t, ctx, conn) // la config: el handler ya está en su bucle
	conn.Close(websocket.StatusNormalClosure, "")

	for i := 0; i < 50; i++ {
		if eng.liberados() >= 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("el tap no se liberó tras cerrar el cliente: liberados = %d", eng.liberados())
}

// Un cliente que no lee no puede retener el tap para siempre: el plazo de escritura de
// 2 s corta y libera, la misma defensa que el WS de estado.
func TestPreviewSurvivesASlowClient(t *testing.T) {
	if testing.Short() {
		t.Skip("tarda varios segundos; la CI no usa -short, así que allí sí corre")
	}

	eng, url, cookies := previewServer(t)
	eng.setLive(7)
	eng.setVideoConfig(seqPayload(0x01, 0x64, 0x00, 0x1f))

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	// No se lee NADA a propósito: este cliente está atascado.

	// Frames grandes hasta llenar los buffers TCP: entonces la escritura se atasca, el
	// plazo vence y el handler libera el tap.
	grande := &relay.Message{
		Kind: relay.KindVideo, IsKeyframe: true,
		Payload: append([]byte{0x17, 0x01, 0x00, 0x00, 0x00}, make([]byte, 256*1024)...),
	}
	plazo := time.After(30 * time.Second)
	for eng.liberados() == 0 {
		select {
		case <-plazo:
			t.Fatal("el tap no se liberó: el cliente atascado retuvo la vista previa")
		default:
		}
		eng.emitir(grande)
		time.Sleep(20 * time.Millisecond)
	}
}
```

- [ ] **Step 2: Comprobar que fallan**

Run: `go test ./internal/httpapi/ -run TestPreview -race -count=1`
Expected: los tests nuevos FAILean — `dialWS` conecta pero el mux responde 404 al upgrade (no existe la ruta), o falla la compilación si algún helper se nombró distinto. Los 3 de la Task 3 siguen en verde.

- [ ] **Step 3: Implementar el handler y la ruta**

Añadir al final de `internal/httpapi/preview.go`:

```go
// handlePreviewWS es la vista previa silenciada (spec vista previa §5): abre un tap del
// hub y reenvía la config y cada frame de vídeo por mensajes binarios. La sesión y el
// Origin se comprueban igual que en handleWS: el handshake es HTTP normal con cookie.
func (s *Server) handlePreviewWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.logger.Warn("no se pudo abrir el WebSocket de la vista previa", "err", err)
		return
	}
	defer conn.CloseNow()

	// Sin emisión (o sin sequence header todavía) no hay nada que enseñar. Se cierra con
	// un código de aplicación en vez de esperar: el botón del panel solo se habilita con
	// ingesta viva, así que llegar aquí sin señal es la carrera de pulsar justo cuando se
	// corta — y la respuesta honesta es decirlo, no colgarse a esperar.
	cfg := previewConfigMsg(s.engine.VideoConfig())
	if s.engine.Session().ID == 0 || cfg == nil {
		conn.Close(previewCloseNoSignal, "sin señal")
		return
	}

	ch, release := s.engine.Tap()
	defer release()

	ctx := r.Context()
	if !s.writePreview(ctx, conn, cfg) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			// El cliente se fue o el servidor está cerrando.
			return
		case msg, ok := <-ch:
			if !ok {
				// El hub cerró los taps: la emisión terminó.
				conn.Close(previewCloseEnded, "la emisión terminó")
				return
			}
			var out []byte
			if msg.IsSeqHeader {
				// Renegociación a mitad: config nueva, el cliente reconfigura.
				out = previewConfigMsg(msg.Payload)
			} else {
				out = previewFrameMsg(msg)
			}
			if out == nil {
				continue
			}
			if !s.writePreview(ctx, conn, out) {
				return
			}
		}
	}
}

// writePreview escribe un mensaje binario con el mismo plazo que el WS de estado: un
// cliente que no lee se corta, no se acumula.
func (s *Server) writePreview(ctx context.Context, conn *websocket.Conn, b []byte) bool {
	escritura, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	if err := conn.Write(escritura, websocket.MessageBinary, b); err != nil {
		s.logger.Debug("se cerró el WebSocket de la vista previa", "err", err)
		return false
	}
	return true
}
```

Los imports de `preview.go` quedan: `context`, `net/http`, `github.com/coder/websocket`, y el de `relay` que ya estaba.

En `internal/httpapi/server.go`, junto a `protegida("GET /ws", s.handleWS)` (línea 170):

```go
	protegida("GET /api/preview/ws", s.handlePreviewWS)
```

- [ ] **Step 4: Comprobar que pasan**

Run: `go test ./internal/httpapi/ -race -count=1`
Expected: PASS entero (los tests viejos incluidos).

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/preview.go internal/httpapi/preview_ws_test.go internal/httpapi/server.go
git commit -m "feat(httpapi): endpoint WebSocket de la vista previa"
```

---

### Task 5: El componente de la vista previa en el panel

**Files:**
- Create: `web/src/components/VistaPrevia.vue`
- Modify: `web/src/pages/Panel.vue` (imports, estado, botón en la tarjeta de ingesta, montaje del componente)

**Interfaces:**
- Consumes: el endpoint `GET /api/preview/ws` (Task 4) y su protocolo (Task 3); `usePanel().haySesion`.
- Produces: componente `VistaPrevia` sin props que emite `cerrar(motivo: string|null)` — `null` si lo cerró el usuario, un texto si lo cerró el servidor o un fallo.

No hay infraestructura de tests JS en el proyecto (a propósito): la verificación de esta task es que `npm run build` compila y la revisión manual de la Task 6.

- [ ] **Step 1: Escribir el componente**

Crear `web/src/components/VistaPrevia.vue`:

```vue
<script setup>
import { onBeforeUnmount, ref } from 'vue'

// La vista previa silenciada: solo vídeo, decodificado con WebCodecs y pintado en un
// canvas. Sin librerías — VideoDecoder y canvas son APIs del navegador, y el servidor
// manda los NALUs H.264 tal cual salen de OBS.
//
// Todo el ciclo de vida vive aquí: abrir el WebSocket, decodificar, pintar y limpiar. El
// padre solo decide cuándo existe el componente (v-if) y escucha 'cerrar'.
const emit = defineEmits(['cerrar'])

const lienzo = ref(null)
const aviso = ref('Conectando…')

let ws = null
let decoder = null
// Tras (re)configurar, no se le da nada al decodificador hasta el primer keyframe: un
// delta sin su pasado es un error de decodificación seguro.
let esperandoKeyframe = true
let cerrado = false

function cerrar(motivo = null) {
  if (cerrado) return
  cerrado = true
  if (ws) { ws.onclose = null; ws.close(); ws = null }
  if (decoder && decoder.state !== 'closed') decoder.close()
  decoder = null
  emit('cerrar', motivo)
}

function pintar(frame) {
  const canvas = lienzo.value
  if (!canvas) { frame.close(); return }
  if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
    canvas.width = frame.displayWidth
    canvas.height = frame.displayHeight
  }
  canvas.getContext('2d').drawImage(frame, 0, 0)
  // Obligatorio: cada VideoFrame retiene memoria de GPU hasta que se cierra.
  frame.close()
  aviso.value = null
}

async function configurar(avcc) {
  // El string de códec sale del propio avcC: perfil, flags de compatibilidad y nivel
  // son sus bytes 1 a 3. El servidor no parsea nada a propósito.
  const codec = 'avc1.' + [...avcc.subarray(1, 4)]
    .map((b) => b.toString(16).padStart(2, '0')).join('')
  const config = { codec, description: avcc, optimizeForLatency: true }

  if (typeof VideoDecoder === 'undefined') {
    cerrar('Tu navegador no soporta la vista previa')
    return
  }
  const soporte = await VideoDecoder.isConfigSupported(config).catch(() => null)
  if (!soporte?.supported) {
    cerrar('Tu navegador no soporta la vista previa')
    return
  }
  if (cerrado) return

  // Una config a mitad significa que el publisher renegoció: el decodificador anterior
  // ya no vale y el que viene arranca en el siguiente keyframe.
  if (decoder && decoder.state !== 'closed') decoder.close()
  decoder = new VideoDecoder({
    output: pintar,
    error: () => cerrar('La vista previa falló al decodificar'),
  })
  decoder.configure(config)
  esperandoKeyframe = true
}

function decodificar(b) {
  if (!decoder || decoder.state !== 'configured') return
  const esKeyframe = (b[1] & 0x01) === 0x01
  if (esperandoKeyframe && !esKeyframe) return
  esperandoKeyframe = false
  const ts = new DataView(b.buffer, b.byteOffset + 2, 4).getUint32(0)
  decoder.decode(new EncodedVideoChunk({
    type: esKeyframe ? 'key' : 'delta',
    timestamp: ts * 1000, // WebCodecs cuenta en microsegundos; el servidor manda ms
    data: b.subarray(6),
  }))
}

function conectar() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/api/preview/ws`)
  ws.binaryType = 'arraybuffer'
  ws.onmessage = (ev) => {
    const b = new Uint8Array(ev.data)
    if (b.length < 1) return
    if (b[0] === 0x01 && b.length > 4) configurar(b.subarray(1))
    else if (b[0] === 0x02 && b.length > 6) decodificar(b)
  }
  // Sin reconexión, a propósito: la vista es bajo demanda y gasta subida del servidor.
  // El servidor cierra con motivo («sin señal», «la emisión terminó») y ese texto es lo
  // que se le enseña al usuario, que decide si reabrir.
  ws.onclose = (ev) => { ws = null; cerrar(ev.reason || 'La vista previa se cortó') }
}

conectar()
onBeforeUnmount(() => cerrar())
</script>

<template>
  <q-card flat bordered class="q-mb-md">
    <q-card-section class="row items-center q-py-xs">
      <div class="text-caption text-grey-5">Vista previa · sin sonido</div>
      <q-space />
      <q-btn flat dense no-caps size="sm" label="Cerrar" @click="cerrar()" />
    </q-card-section>
    <q-separator />
    <q-card-section class="q-pa-none cuadro">
      <canvas ref="lienzo" class="lienzo" />
      <div v-if="aviso" class="text-caption text-grey-5 q-pa-md">{{ aviso }}</div>
    </q-card-section>
  </q-card>
</template>

<style scoped>
.cuadro { background: #000; text-align: center; }
.lienzo { max-width: 100%; height: auto; display: block; margin: 0 auto; }
</style>
```

- [ ] **Step 2: Integrarlo en Panel.vue**

En `web/src/pages/Panel.vue`:

1. Import, junto a los otros componentes (línea 10):

```js
import VistaPrevia from '@/components/VistaPrevia.vue'
```

2. Estado y cierre, junto a los otros `ref` del script (tras `const arrastrando = ref(false)`, línea 28):

```js
// La vista previa gasta subida del servidor mientras está abierta: existe solo tras un
// gesto explícito, y cerrarla (o que el servidor la cierre) la desmonta del todo.
const verPrevia = ref(false)
function cerrarPrevia(motivo) {
  verPrevia.value = false
  if (motivo) $q.notify({ type: 'warning', message: motivo })
}
```

3. El botón, en la primera `q-card-section` de la tarjeta de ingesta, después del `<div class="col">…</div>` que muestra «Recibiendo señal» (línea 273):

```html
        <q-btn v-if="panel.haySesion && !verPrevia" flat dense no-caps size="sm"
               label="Vista previa" @click="verPrevia = true" />
```

4. El componente, entre el cierre de la tarjeta de ingesta (`</q-card>`, línea 293) y el `<div class="row items-center q-mb-sm q-gutter-sm">` de «Canales»:

```html
    <VistaPrevia v-if="verPrevia" @cerrar="cerrarPrevia" />
```

- [ ] **Step 3: Comprobar que el panel compila**

Run: `cd web && npm run build && cd ..`
Expected: build de Vite sin errores ni warnings nuevos.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/VistaPrevia.vue web/src/pages/Panel.vue
git commit -m "feat(panel): vista previa silenciada con WebCodecs"
```

---

### Task 6: Manual, verificación completa y PR

**Files:**
- Modify: `docs/manual-de-usuario.md` (sección nueva)

**Interfaces:**
- Consumes: todo lo anterior.
- Produces: la rama lista para PR.

- [ ] **Step 1: Documentar la vista previa en el manual**

Leer `docs/manual-de-usuario.md` para ubicar la sección del panel y añadir, con el estilo de las secciones vecinas, una sección «Vista previa» con este contenido (adaptar el nivel de encabezado al del documento):

```markdown
## Vista previa

Mientras estás emitiendo, el botón «Vista previa» de la tarjeta de señal abre un monitor
del vídeo que está saliendo hacia tus canales. Va sin sonido a propósito: es para
comprobar que se ve bien, no para verte el directo.

Dos cosas que conviene saber:

- **Mientras la vista está abierta, el servidor gasta en subida más o menos lo mismo que
  un canal más.** Al cerrarla, ese gasto desaparece. Por eso se abre con un botón y no
  sola.
- Necesita un navegador razonablemente moderno (Chrome o Edge, Safari 16.4 o más nuevo,
  Firefox 130 o más nuevo). Si el tuyo no puede, el panel te lo dirá y no pasa nada más.

Si la emisión se corta, la vista se cierra sola avisando. No se reabre por su cuenta.
```

- [ ] **Step 2: Verificación completa**

```bash
make vet && make test
cd web && npm run build && cd ..
GOMAXPROCS=2 go test ./internal/relay/ -race -count=1
```

Expected: todo en verde. Si algo falla, arreglarlo antes de seguir (y si el arreglo no es obvio, parar y usar superpowers:systematic-debugging).

- [ ] **Step 3: Commit del manual y push**

```bash
git add docs/manual-de-usuario.md
git commit -m "docs(manual): la vista previa silenciada"
git push -u origin feat/vista-previa
```

- [ ] **Step 4: Abrir el PR**

```bash
gh pr create --title "feat: vista previa silenciada en el panel" --body "$(cat <<'EOF'
Monitor de vídeo en vivo en el panel, según el spec
`docs/superpowers/specs/2026-09-08-vista-previa-design.md`:

- Taps de solo lectura en el `Hub` (descarte con reenganche en keyframe; `*Sink` intacto).
- `GET /api/preview/ws`: WebSocket binario con el avcC y los NALUs H.264 tal cual.
- Componente `VistaPrevia`: WebCodecs + canvas, sin librerías nuevas. Sin sonido por diseño.
- Bajo demanda: la subida del VPS solo se gasta mientras la vista está abierta.

Pendiente de verificación manual con OBS delante (la interfaz no se puede capturar
autenticada, la limitación conocida de la fase 5).
EOF
)"
```

- [ ] **Step 5: Verificación por ejecución (manual, con el usuario)**

Los cinco fallos de las fases 5, 6 y del logo aparecieron ejecutando, no en los tests. Antes del merge, el usuario debe comprobar contra el binario real con OBS:

1. Emitir desde OBS; el botón «Vista previa» aparece en la tarjeta de señal.
2. Abrirla: el vídeo se ve en movimiento, sin sonido, con latencia baja.
3. Cerrarla y reabrirla varias veces seguidas.
4. Cambiar la resolución del vídeo en OBS a mitad de emisión: la vista se recupera sola.
5. Parar OBS con la vista abierta: aviso «la emisión terminó» y la vista se cierra.
6. Sin emitir, el botón no aparece.
