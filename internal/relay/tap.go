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
