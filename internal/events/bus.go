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
