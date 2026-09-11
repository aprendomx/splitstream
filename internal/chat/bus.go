// Package chat agrega el chat de las plataformas durante una sesión: lo persiste por
// lotes y lo reparte a los WebSockets del panel. Aparte del bus de eventos porque
// store.Event no tiene autor ni texto, y porque un chat animado no debe competir con
// las alertas.
package chat

import (
	"sync"
	"sync/atomic"

	"github.com/aprendomx/splitstream/internal/platforms"
)

// Message es un mensaje de chat de la sesión en curso.
type Message struct {
	SessionID int64
	platforms.ChatMessage
}

// recentKeep es el snapshot que recibe un WebSocket al conectar: lo que cabe en pantalla.
const recentKeep = 50

type Bus struct {
	mu      sync.RWMutex
	subs    map[*sub]struct{}
	recent  []Message
	dropped atomic.Uint64
}

type sub struct {
	ch   chan Message
	once sync.Once
}

func NewBus() *Bus { return &Bus{subs: map[*sub]struct{}{}} }

// Subscribe devuelve un canal y una función de baja idempotente. Igual que events.Bus.
func (b *Bus) Subscribe(buffer int) (<-chan Message, func()) {
	if buffer <= 0 {
		buffer = 256
	}
	s := &sub{ch: make(chan Message, buffer)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s.ch, func() {
		b.mu.Lock()
		delete(b.subs, s)
		b.mu.Unlock()
		s.once.Do(func() { close(s.ch) })
	}
}

// Publish reparte sin bloquear y guarda el mensaje en la ventana de recientes.
func (b *Bus) Publish(m Message) {
	b.mu.Lock()
	b.recent = append(b.recent, m)
	if len(b.recent) > recentKeep {
		b.recent = b.recent[len(b.recent)-recentKeep:]
	}
	b.mu.Unlock()

	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		select {
		case s.ch <- m:
		default:
			b.dropped.Add(1)
		}
	}
}

// Recent devuelve una copia de los últimos mensajes, en orden.
func (b *Bus) Recent() []Message {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Message, len(b.recent))
	copy(out, b.recent)
	return out
}

// Reset vacía los recientes: al empezar una sesión nueva, el panel no debe ver el chat
// de la anterior.
func (b *Bus) Reset() {
	b.mu.Lock()
	b.recent = nil
	b.mu.Unlock()
}

func (b *Bus) Dropped() uint64 { return b.dropped.Load() }
