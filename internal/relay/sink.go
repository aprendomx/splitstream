package relay

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// DefaultQueueSize es la capacidad por defecto de la cola de un sink.
//
// La fase 3 sustituye esta cola por un deque acotado por bytes y duración, con descarte
// por GOP completo (spec §3.3 y §3.4). Mientras tanto, un canal con descarte simple basta
// para el objetivo de la fase 2: un destino de punta a punta.
const DefaultQueueSize = 512

// State es el estado de un destino. `degraded` es un atributo aparte y llega en la fase 3
// (spec §3.7); aquí solo están los estados de esta fase.
type State uint8

const (
	StateIdle State = iota
	StateConnecting
	StateLive
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
	case StateError:
		return "error"
	default:
		return "desconocido"
	}
}

// SinkConfig son los datos para construir un sink.
type SinkConfig struct {
	ID     int64
	Name   string
	Pub    Publisher
	Queue  int          // capacidad de la cola; 0 usa DefaultQueueSize
	Logger *slog.Logger // nil usa slog.Default()
}

// Sink atiende a un destino desde su propia goroutine. Posee su Publisher, su timebase y
// su estado; nadie más los toca.
type Sink struct {
	id     int64
	name   string
	pub    Publisher
	log    *slog.Logger
	ch     chan *Message
	quit   chan struct{}
	done   chan struct{}
	once   sync.Once
	state  atomic.Uint32
	drops  atomic.Uint64
	errMu  sync.Mutex
	lastEr error
}

// NewSink construye un sink parado. Hay que llamar a Start para que atienda.
func NewSink(cfg SinkConfig) *Sink {
	size := cfg.Queue
	if size <= 0 {
		size = DefaultQueueSize
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Sink{
		id:   cfg.ID,
		name: cfg.Name,
		pub:  cfg.Pub,
		log:  log.With("destino_id", cfg.ID, "destino", cfg.Name),
		ch:   make(chan *Message, size),
		quit: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// ID devuelve el identificador del destino.
func (s *Sink) ID() int64 { return s.id }

// State devuelve el estado actual.
func (s *Sink) State() State { return State(s.state.Load()) }

// Dropped devuelve cuántos mensajes se han descartado por cola llena.
func (s *Sink) Dropped() uint64 { return s.drops.Load() }

// LastError devuelve el último error observado, o nil.
func (s *Sink) LastError() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.lastEr
}

func (s *Sink) setState(st State) { s.state.Store(uint32(st)) }

func (s *Sink) fail(err error) {
	s.errMu.Lock()
	s.lastEr = err
	s.errMu.Unlock()
	s.setState(StateError)
	s.log.Error("destino en error", "err", err)
}

// Start lanza la goroutine del sink. pre es el preámbulo de la sesión: el sink lo lee
// justo antes de mandar su primer keyframe.
func (s *Sink) Start(ctx context.Context, pre *Preamble) {
	go s.run(ctx, pre)
}

// Enqueue entrega un mensaje al sink sin bloquear nunca. Si la cola está llena, el
// mensaje se descarta y se cuenta: un destino lento no puede frenar al publisher ni a
// sus hermanos (spec §6.2).
func (s *Sink) Enqueue(msg *Message) {
	select {
	case s.ch <- msg:
	default:
		s.drops.Add(1)
	}
}

// Stop detiene el sink y espera a que su goroutine termine. Es idempotente.
func (s *Sink) Stop() {
	s.once.Do(func() { close(s.quit) })
	<-s.done
}

func (s *Sink) run(ctx context.Context, pre *Preamble) {
	defer close(s.done)
	defer s.pub.Close()

	s.setState(StateConnecting)
	if err := s.pub.Connect(ctx); err != nil {
		s.fail(err)
		// Se sale de inmediato en vez de esperar a Stop(). Esperar en <-s.quit dejaba
		// la goroutine viva para siempre si nadie paraba el sink, y con ella la conexión
		// sin cerrar. El estado se queda en error, que es lo que el consumidor necesita
		// saber; la fase 3 reconectará desde aquí.
		return
	}
	s.setState(StateLive)
	s.log.Info("destino conectado")

	var tb timebase
	for {
		select {
		case <-s.quit:
			s.setState(StateIdle)
			return
		case <-ctx.Done():
			s.setState(StateIdle)
			return
		case msg := <-s.ch:
			if err := s.handle(msg, pre, &tb); err != nil {
				s.fail(err)
				// Igual que arriba: salir libera la goroutine y cierra el Publisher.
				// La reconexión llega en la fase 3.
				return
			}
		}
	}
}

// handle procesa un mensaje. Antes del primer keyframe descarta todo; en el keyframe
// manda el preámbulo y ancla el timebase; después traduce y reenvía.
func (s *Sink) handle(msg *Message, pre *Preamble, tb *timebase) error {
	if !tb.started() {
		// Solo un keyframe de video real arranca el envío. Un sequence header trae el
		// bit de keyframe puesto pero no es un frame decodificable.
		if msg.Kind != KindVideo || !msg.IsKeyframe || msg.IsSeqHeader {
			return nil
		}
		if err := s.sendPreamble(pre); err != nil {
			return err
		}
		tb.start(msg.Timestamp)
	}

	ts, ok := tb.translate(msg.Timestamp)
	if !ok {
		return nil // anterior a la base: se descarta (spec §3.2)
	}

	switch msg.Kind {
	case KindVideo:
		return s.pub.WriteVideo(ts, msg.Payload)
	case KindAudio:
		return s.pub.WriteAudio(ts, msg.Payload)
	case KindMeta:
		return s.pub.WriteMeta(ts, msg.Payload)
	}
	return nil
}

// sendPreamble manda onMetaData, AVC sequence header y AAC sequence header, los tres con
// ts=0, antes de cualquier frame (spec §6.3).
func (s *Sink) sendPreamble(pre *Preamble) error {
	meta, videoSeq, audioSeq := pre.Snapshot()
	if meta != nil {
		if err := s.pub.WriteMeta(0, meta.Payload); err != nil {
			return err
		}
	}
	if videoSeq != nil {
		if err := s.pub.WriteVideo(0, videoSeq.Payload); err != nil {
			return err
		}
	}
	if audioSeq != nil {
		if err := s.pub.WriteAudio(0, audioSeq.Payload); err != nil {
			return err
		}
	}
	return nil
}
