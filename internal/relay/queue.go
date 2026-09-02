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
)

type queueConfig struct {
	MaxBytes int
	MaxSpan  uint32 // milisegundos
}

// queue es la cola de un sink: un deque acotado con política de descarte por GOP.
//
// No es un canal porque la decisión de descarte necesita inspeccionar lo ya encolado:
// al desbordar hay que tirar todo el vídeo pendiente, no solo rechazar el que llega.
type queue struct {
	mu       sync.Mutex
	signal   chan struct{}
	items    []*Message
	bytes    int
	maxBytes int
	maxSpan  uint32
	dropping bool
	drops    uint64
	closed   bool
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
	return &queue{
		signal:   make(chan struct{}, 1),
		maxBytes: maxBytes,
		maxSpan:  maxSpan,
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

	// En modo descarte solo un keyframe reanuda el vídeo. Descartar frames sueltos
	// corrompería la decodificación hasta el siguiente IDR, que es peor que un salto
	// limpio (spec §3.3).
	if q.dropping && msg.Kind == KindVideo && !essential(msg) {
		if !msg.IsKeyframe {
			q.drops++
			return
		}
		q.dropping = false
	}

	q.items = append(q.items, msg)
	q.bytes += len(msg.Payload)

	if q.over() {
		q.dropQueuedVideo()
	}

	q.wake()
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
	q.dropping = true
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
