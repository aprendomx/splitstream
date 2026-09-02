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

// Add registra un sink ya arrancado. Si ya había uno con el mismo id, lo para por
// completo ANTES de registrar el nuevo: solaparlos haría que dos sinks escribieran al
// mismo endpoint RTMP a la vez.
func (h *Hub) Add(s *Sink) {
	h.mu.Lock()
	old, existed := h.sinks[s.ID()]
	if existed {
		delete(h.sinks, s.ID())
	}
	h.mu.Unlock()

	// Fuera del mutex: Stop bloquea hasta que la goroutine del sink termina, y
	// retener el lock aquí frenaría a Publish.
	if existed {
		old.Stop()
	}

	h.mu.Lock()
	h.sinks[s.ID()] = s
	h.mu.Unlock()

	if existed {
		h.log.Info("destino reemplazado en el hub", "destino_id", s.ID())
	} else {
		h.log.Info("destino registrado en el hub", "destino_id", s.ID())
	}
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
