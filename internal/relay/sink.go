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

	quit      chan struct{}
	done      chan struct{}
	once      sync.Once
	startOnce sync.Once
	started   atomic.Bool

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

func (s *Sink) ID() int64       { return s.id }
func (s *Sink) State() State    { return State(s.state.Load()) }
func (s *Sink) Dropped() uint64 { return s.q.dropped() }

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

// Start lanza la goroutine del sink. Es idempotente igual que Stop: una segunda llamada
// no hace nada. Sin esto, dos goroutines cerraban el mismo canal `done` y el proceso
// entero moría con "close of closed channel"; hoy solo se llama desde OnPublishStart,
// pero un pánico no recuperable por una llamada de más no es un contrato aceptable.
func (s *Sink) Start(ctx context.Context, pre *Preamble) {
	s.startOnce.Do(func() {
		// Arrancar un sink ya parado dejaría una goroutine que nadie espera.
		select {
		case <-s.quit:
			return
		default:
		}
		s.started.Store(true)
		go s.run(ctx, pre)
	})
}

// Enqueue entrega un mensaje. Nunca bloquea: la cola aplica su política de descarte.
//
// La marca de degradado va aquí y no en el bucle de envío porque el caso que interesa es
// justo aquel en el que el envío está atascado: si se marcara ahí, un destino bloqueado
// escribiendo nunca llegaría a marcarse.
func (s *Sink) Enqueue(msg *Message) {
	s.q.push(msg)
	if s.q.droppingVideo() {
		s.met.markDegraded()
	}
}

// Stop detiene el sink y espera a que su goroutine termine. Es idempotente, y es seguro
// sobre un sink que nunca se arrancó: en ese caso no hay goroutine a la que esperar.
//
// La espera no tiene plazo a propósito: este es el camino del aislamiento en caliente
// (Hub.Add y Hub.Remove), donde dejar viva la goroutine de un sink que se reemplaza haría
// que dos escribieran a la vez al mismo endpoint RTMP. El apagado del proceso sí tiene
// plazo, y lo pone Hub.Close con signalStop + waitStopped.
func (s *Sink) Stop() {
	s.signalStop()
	if s.started.Load() {
		<-s.done
	}
}

// signalStop pide la parada sin esperar a que la goroutine termine. Es idempotente.
func (s *Sink) signalStop() {
	s.once.Do(func() {
		close(s.quit)
		s.q.close()
	})
}

// waitStopped espera a que la goroutine del sink termine, hasta que se cierre grace.
// Devuelve false si venció el plazo con el sink todavía dentro de una escritura.
func (s *Sink) waitStopped(grace <-chan struct{}) bool {
	if !s.started.Load() {
		return true
	}
	select {
	case <-s.done:
		return true
	case <-grace:
		return false
	}
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
