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
	DefaultMaxItems      = 8192

	// hardBytesFactor es cuánto puede excederse el límite de bytes antes de que la red de
	// seguridad empiece a tirar audio.
	//
	// El límite blando dispara el descarte de vídeo, que es el sacrificio barato: se ve
	// como un salto y el audio sigue. Solo si ni tirando todo el vídeo se baja —lo que
	// ocurre con un destino caído mientras la transmisión continúa, porque el sink deja de
	// drenar la cola durante el backoff— se toca el audio. Perder audio antiguo, que de
	// todos modos llegaría tarde, es preferible a una cola sin tope.
	hardBytesFactor = 4
)

type queueConfig struct {
	MaxBytes int
	MaxSpan  uint32 // milisegundos
	MaxItems int
}

// queue es la cola de un sink: un deque acotado con dos niveles de descarte.
//
// No es un canal porque la decisión de descarte necesita inspeccionar lo ya encolado:
// al desbordar hay que tirar todo el vídeo pendiente, no solo rechazar el que llega.
//
// Nivel blando (maxBytes, maxSpan): tira vídeo por GOP. El audio, la metadata y los
// sequence headers se conservan siempre — es el mecanismo normal del spec (§3.3, §6.4).
// Nivel duro (maxItems, hardBytes): red de seguridad para cuando tirar todo el vídeo no
// basta —un destino caído mientras la transmisión continúa acumula audio sin límite—, y
// aquí sí se tiran los no esenciales más antiguos, audio incluido. La metadata y los
// sequence headers no se tiran en ningún nivel.
type queue struct {
	mu         sync.Mutex
	signal     chan struct{}
	items      []*Message
	bytes      int
	videoItems int // mensajes de vídeo descartables encolados
	maxBytes   int
	hardBytes  int
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
	hardBytes := maxBytes * hardBytesFactor
	return &queue{
		signal:    make(chan struct{}, 1),
		maxBytes:  maxBytes,
		hardBytes: hardBytes,
		maxSpan:   maxSpan,
		maxItems:  maxItems,
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

	// Nivel blando: tirar todo el vídeo pendiente. El guardia sobre videoItems evita
	// reescanear la cola entera en cada push cuando no hay vídeo que tirar.
	if q.over() {
		if q.videoItems > 0 {
			q.dropQueuedVideo()
		}
		q.dropping = true
	}

	// Nivel duro: si ni así se baja, se tiran los no esenciales más antiguos.
	q.shedToHardLimits()

	q.wake()
}

// over indica si la cola supera alguno de sus dos límites blandos.
func (q *queue) over() bool {
	return q.bytes > q.maxBytes || q.spanMillis() > q.maxSpan
}

// overHard indica si la cola supera alguno de sus límites duros.
func (q *queue) overHard() bool {
	return len(q.items) > q.maxItems || q.bytes > q.hardBytes
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

// dropQueuedVideo tira todo el vídeo pendiente. Conserva el audio, la metadata y los
// sequence headers: el audio es barato y su corte se nota mucho más que un salto de
// vídeo (spec §6.4).
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

// shedToHardLimits devuelve la cola dentro de sus límites duros.
//
// Lo hace en dos pasos y el orden importa: primero tira TODO el vídeo pendiente de golpe,
// que es atómico y por tanto no puede dejar frames intermedios huérfanos de su keyframe;
// solo después recorta el audio más antiguo. Recortar por antigüedad mezclando vídeo y
// audio sí dejaría huérfanos, y el destino vería bloques corruptos: exactamente el fallo
// que el descarte por GOP existe para evitar.
func (q *queue) shedToHardLimits() {
	if !q.overHard() {
		return
	}

	// Paso 1: todo el vídeo, de una vez. Tras esto videoItems es 0, así que el paso 2
	// solo puede tocar audio.
	if q.videoItems > 0 {
		q.dropQueuedVideo()
		q.dropping = true
	}
	if !q.overHard() {
		return
	}

	// Paso 2: el audio más antiguo. Ya no queda vídeo que pueda quedar huérfano.
	items, bytes := len(q.items), q.bytes
	kept := q.items[:0]
	for _, m := range q.items {
		tooMany := items > q.maxItems || bytes > q.hardBytes
		if tooMany && m.Kind == KindAudio && !essential(m) {
			q.drops++
			items--
			bytes -= len(m.Payload)
			continue
		}
		kept = append(kept, m)
	}
	for i := len(kept); i < len(q.items); i++ {
		q.items[i] = nil
	}
	q.items = kept
	q.bytes = bytes
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

// dropped devuelve cuántos mensajes se han descartado en total: vídeo por la política de
// GOP en el nivel blando, y no esenciales (audio incluido) por el nivel duro.
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
