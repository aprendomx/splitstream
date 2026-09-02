package relay

import (
	"context"
	"errors"
	"log/slog"
	"sync"
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

// EngineConfig son los datos para construir el motor.
type EngineConfig struct {
	Hub    *Hub
	Store  EngineStore
	Logger *slog.Logger
}

// Engine une la ingesta con el hub: valida al publisher, abre y cierra la sesión, y
// reparte los mensajes. Satisface rtmpio.IngestHandler.
type Engine struct {
	hub   *Hub
	store EngineStore
	log   *slog.Logger

	mu        sync.Mutex
	validate  func(app, key string) error
	sessionID int64
}

// NewEngine construye el motor. Hasta que se llame a SetValidator, rechaza a todo
// publisher: es más seguro que aceptar por defecto.
func NewEngine(cfg EngineConfig) *Engine {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		hub:      cfg.Hub,
		store:    cfg.Store,
		log:      log,
		validate: func(string, string) error { return errors.New("ingesta sin configurar") },
	}
}

// SetValidator fija la función que decide si un publisher puede publicar.
func (e *Engine) SetValidator(fn func(app, key string) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.validate = fn
}

// SessionID devuelve el id de la sesión en curso, o 0 si no hay ninguna.
func (e *Engine) SessionID() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessionID
}

// OnPublishStart valida al publisher y abre la sesión.
func (e *Engine) OnPublishStart(app, streamKey string) error {
	e.mu.Lock()
	validate := e.validate
	e.mu.Unlock()

	if err := validate(app, streamKey); err != nil {
		return err
	}

	ctx := context.Background()
	id, err := e.store.StartSession(ctx)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.sessionID = id
	e.mu.Unlock()

	e.logEvent(ctx, &id, nil, "info", "publisher_connected", "el publisher conectó")
	e.log.Info("sesión iniciada", "sesion_id", id, "app", app)
	return nil
}

// OnMessage reparte un mensaje de media a los destinos.
func (e *Engine) OnMessage(msg *Message) { e.hub.Publish(msg) }

// OnPublishEnd cierra la sesión y olvida el preámbulo: los sequence headers de esta
// transmisión no valen para la siguiente.
func (e *Engine) OnPublishEnd() {
	e.mu.Lock()
	id := e.sessionID
	e.sessionID = 0
	e.mu.Unlock()

	if id == 0 {
		return
	}

	ctx := context.Background()
	// La resolución y el bitrate medidos llegan en la fase 3; aquí se cierra con ceros.
	if err := e.store.FinishSession(ctx, id, 0, 0, 0); err != nil {
		e.log.Error("no se pudo cerrar la sesión", "sesion_id", id, "err", err)
	}
	e.logEvent(ctx, &id, nil, "info", "publisher_disconnected", "el publisher se desconectó")
	e.hub.Preamble().Reset()
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
