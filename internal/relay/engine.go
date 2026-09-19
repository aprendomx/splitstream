package relay

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aprendomx/splitstream/internal/flv"
)

// EngineStore es lo que el motor necesita de la persistencia. Es una interfaz para que
// internal/relay no importe internal/store y siga siendo testeable en memoria (spec §4).
type EngineStore interface {
	StartSession(ctx context.Context) (int64, error)
	FinishSession(ctx context.Context, id int64, width, height, bitrateBPS int) error
	LogEvent(ctx context.Context, e EngineEvent) error
}

// EngineEvent es una entrada del log persistente, en los términos del motor.
type EngineEvent struct {
	SessionID     *int64
	DestinationID *int64
	Level         string
	Kind          string
	Message       string
}

// SinkProvider construye los sinks de una sesión. Recibe el id de la sesión que arranca
// porque la grabación necesita saber a qué sesión pertenece cada archivo. Se llama al
// aceptar a un publisher, no al arrancar el proceso: cada sesión de ingesta abre su propia
// conexión con cada destino (spec §6.5). Arrancarlos una sola vez al inicio hacía que la
// segunda transmisión reutilizara el timebase de la primera, con lo que su sequence header
// nunca llegaba.
type SinkProvider func(sessionID int64) ([]*Sink, error)

// ErrSessionInProgress se devuelve cuando ya hay una publicación en curso. Aceptar una
// segunda intercalaría frames de dos codificadores en el mismo stream de salida.
var ErrSessionInProgress = errors.New("ya hay una publicación en curso")

// Origen de la sesión en curso: de dónde entran los mensajes. El panel lo enseña; no se
// persiste (spec cámara §10).
const (
	SourceRTMP    = "rtmp"    // OBS o cualquier publisher RTMP
	SourceBrowser = "browser" // la cámara del navegador por /api/camera/ws
)

// EngineConfig son los datos para construir el motor.
type EngineConfig struct {
	Hub    *Hub
	Store  EngineStore
	Logger *slog.Logger
	// BaseContext es el contexto de vida del proceso. Los sinks lo heredan al arrancar.
	// Va aquí, y no como parámetro, porque OnPublishStart lo llama la interfaz
	// IngestHandler, que no recibe contexto.
	BaseContext context.Context
}

// Engine une la ingesta con el hub: valida al publisher, abre y cierra la sesión, y
// reparte los mensajes. Satisface rtmpio.IngestHandler.
type Engine struct {
	hub     *Hub
	store   EngineStore
	log     *slog.Logger
	baseCtx context.Context

	mu            sync.Mutex
	validate      func(app, key string) error
	newSinks      SinkProvider
	sessionID     int64
	sessionSource string
	// arrancando marca la ventana en la que hay una sesión abriéndose pero todavía sin
	// id: startSession suelta el mutex para hablar con la base. Sin esta bandera, dos
	// arranques simultáneos —dos teléfonos en la página de la cámara— pasarían los dos la
	// comprobación de sessionID y dejarían dos filas de sesión abiertas, cuando el spec §6
	// promete un 4002 al segundo.
	arrancando bool

	sessionWidth   int
	sessionHeight  int
	sessionBytes   uint64
	sessionStarted time.Time
}

// NewEngine construye el motor. Hasta que se llame a SetValidator, rechaza a todo
// publisher: es más seguro que aceptar por defecto.
func NewEngine(cfg EngineConfig) *Engine {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	baseCtx := cfg.BaseContext
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	return &Engine{
		hub:      cfg.Hub,
		store:    cfg.Store,
		log:      log,
		baseCtx:  baseCtx,
		validate: func(string, string) error { return errors.New("ingesta sin configurar") },
		newSinks: func(int64) ([]*Sink, error) { return nil, nil },
	}
}

// SetValidator fija la función que decide si un publisher puede publicar.
func (e *Engine) SetValidator(fn func(app, key string) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.validate = fn
}

// SetSinkProvider fija la función que construye los sinks de cada sesión.
func (e *Engine) SetSinkProvider(fn SinkProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.newSinks = fn
}

// SessionID devuelve el id de la sesión en curso, o 0 si no hay ninguna.
func (e *Engine) SessionID() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessionID
}

// WaitIdle bloquea hasta que no haya ninguna sesión abierta, o hasta que venza el
// contexto.
//
// Es lo que hace que el apagado sea limpio de verdad: cerrar la ingesta corta los
// sockets, pero go-rtmp atiende cada conexión en su propia goroutine y es esa la que
// todavía tiene que disparar OnPublishEnd, que cierra la sesión en la base. Salir antes
// deja la sesión abierta para siempre con ended_at en NULL, y sin ningún aviso, porque
// el proceso muere antes de poder loguearlo.
func (e *Engine) WaitIdle(ctx context.Context) error {
	const poll = 20 * time.Millisecond

	t := time.NewTicker(poll)
	defer t.Stop()

	for {
		if e.SessionID() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// OnPublishStart valida al publisher RTMP y abre la sesión.
func (e *Engine) OnPublishStart(app, streamKey string) error {
	e.mu.Lock()
	if e.sessionID != 0 {
		e.mu.Unlock()
		return ErrSessionInProgress
	}
	validate := e.validate
	e.mu.Unlock()

	if err := validate(app, streamKey); err != nil {
		return err
	}
	return e.startSession(SourceRTMP, app, "el publisher conectó")
}

// StartLocalSession abre una sesión para la cámara del navegador (spec cámara §4). No
// pasa por el validador: quien llega aquí ya se autenticó con la cookie del panel, y la
// clave de ingesta es cosa de RTMP. Todo lo demás es idéntico a OnPublishStart, y la
// sesión se cierra con el mismo OnPublishEnd.
func (e *Engine) StartLocalSession() error {
	return e.startSession(SourceBrowser, "browser", "la cámara del navegador conectó")
}

// startSession es lo común a las dos entradas: abre la sesión en la base, arranca los
// sinks del proveedor y deja constancia. Vuelve a comprobar que no haya sesión porque
// OnPublishStart soltó el mutex para validar, y toma arrancando bajo el MISMO candado que
// esa comprobación: entre aquí y la escritura de sessionID hay un viaje a la base con el
// mutex suelto, y quien llegue en ese hueco tiene que ver el motor ocupado.
func (e *Engine) startSession(source, app, mensaje string) error {
	e.mu.Lock()
	if e.sessionID != 0 || e.arrancando {
		e.mu.Unlock()
		return ErrSessionInProgress
	}
	e.arrancando = true
	provider := e.newSinks
	e.mu.Unlock()

	// A partir de aquí toda salida suelta la bandera: si un fallo de la base la dejara
	// puesta, el motor rechazaría para siempre sin que haya ninguna sesión abierta.
	defer func() {
		e.mu.Lock()
		e.arrancando = false
		e.mu.Unlock()
	}()

	ctx := context.Background()
	id, err := e.store.StartSession(ctx)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.sessionID = id
	e.sessionSource = source
	e.sessionWidth, e.sessionHeight = 0, 0
	e.sessionBytes = 0
	e.sessionStarted = time.Now()
	e.mu.Unlock()

	// Los destinos se conectan al empezar la sesión, no al arrancar el proceso.
	sinks, err := provider(id)
	if err != nil {
		// Un fallo construyendo destinos no debe rechazar al publisher: es preferible
		// ingestar sin retransmitir que cortarle la transmisión al usuario.
		e.log.Error("no se pudieron construir los destinos de la sesión", "err", err)
	}
	for _, s := range sinks {
		e.AddSink(s)
	}

	e.logEvent(ctx, &id, nil, "info", "publisher_connected", mensaje)
	e.log.Info("sesión iniciada", "sesion_id", id, "app", app, "origen", source)
	return nil
}

// AddSink añade un destino a la sesión en curso y lo ARRANCA.
//
// Existe porque Hub.Add solo registra: arrancar necesita el contexto de vida del proceso y
// el preámbulo, y los dos los tiene el motor. Cuando la API añadía el sink al hub por su
// cuenta, el destino se quedaba en idle acumulando mensajes hasta desbordar la cola, y
// desde fuera parecía "degradado" — la pista equivocada. Se descubrió probando con Facebook
// contra un directo real.
//
// El contexto es e.baseCtx a propósito, NO el de la petición HTTP que provocó el alta: con
// el de la petición, el sink moriría al devolver la respuesta.
//
// Añadir un sink con un id que ya está reemplaza al anterior sin ventana de escritura
// doble, que es lo que hace falta al editar un destino en caliente.
func (e *Engine) AddSink(s *Sink) {
	s.Start(e.baseCtx, e.hub.Preamble())
	e.hub.Add(s)
}

// RemoveSink quita un destino de la sesión en curso y para su sink, cerrando su conexión.
func (e *Engine) RemoveSink(id int64) {
	e.hub.Remove(id)
}

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

// LiveSession describe la sesión de ingesta en curso. ID a 0 significa que no hay ninguna,
// y entonces el resto de los campos no significan nada.
//
// Width y Height valen 0 hasta que llega el primer AVC sequence header, que en la práctica
// es el primer segundo: la interfaz debe tratar el cero como "todavía no se sabe", no como
// un error.
type LiveSession struct {
	ID         int64
	StartedAt  time.Time
	Width      int
	Height     int
	BitrateBPS int
	// Source es SourceRTMP o SourceBrowser: el panel enseña de dónde viene lo que está
	// en el aire y deshabilita la otra entrada.
	Source string
}

// Session devuelve lo que se sabe de la sesión en curso, sin esperar a que termine.
//
// Existe porque el spec §10 pide que el panel enseñe resolución y bitrate EN VIVO, y hasta
// ahora esos datos solo se escribían en FinishSession: la API devolvía null durante toda la
// emisión. Se detectó usando el producto contra YouTube, no por un test.
//
// El bitrate se calcula igual que en OnPublishEnd —bytes de media entre tiempo transcurrido—
// para que lo que se enseña en vivo y lo que queda en el historial no se contradigan.
func (e *Engine) Session() LiveSession {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.sessionID == 0 {
		return LiveSession{}
	}
	out := LiveSession{
		ID:        e.sessionID,
		StartedAt: e.sessionStarted,
		Width:     e.sessionWidth,
		Height:    e.sessionHeight,
		Source:    e.sessionSource,
	}
	if elapsed := time.Since(e.sessionStarted); elapsed > 0 {
		out.BitrateBPS = int(float64(e.sessionBytes*8) / elapsed.Seconds())
	}
	return out
}

// OnPublishEnd cierra la sesión y olvida el preámbulo: los sequence headers de esta
// transmisión no valen para la siguiente.
//
// sessionID solo se pone a 0 al final, DESPUÉS de FinishSession y LogEvent. Si se
// pusiera a 0 al entrar (como en una versión anterior), WaitIdle vería "sin sesión"
// mientras la escritura en la base todavía está en vuelo, y el apagado podría cerrar
// la base antes de que termine: exactamente la carrera que WaitIdle existe para evitar.
func (e *Engine) OnPublishEnd() {
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
	e.logEvent(ctx, &id, nil, "info", "publisher_disconnected", "el publisher se desconectó")
	// Cerrar el hub para los sinks y vacía el preámbulo: la conexión saliente de esta
	// sesión se cierra, y la siguiente abrirá una nueva con su propio timebase.
	e.hub.Close()

	e.mu.Lock()
	e.sessionID = 0
	e.sessionSource = ""
	e.mu.Unlock()

	e.log.Info("sesión terminada", "sesion_id", id)
}

func (e *Engine) logEvent(ctx context.Context, sessionID, destID *int64, level, kind, msg string) {
	if err := e.store.LogEvent(ctx, EngineEvent{
		SessionID:     sessionID,
		DestinationID: destID,
		Level:         level,
		Kind:          kind,
		Message:       msg,
	}); err != nil {
		e.log.Error("no se pudo registrar el evento", "kind", kind, "err", err)
	}
}

// Snapshot devuelve las métricas de todos los destinos. La fase 4 la sirve por WebSocket.
func (e *Engine) Snapshot() map[int64]Metrics { return e.hub.Snapshot() }

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
