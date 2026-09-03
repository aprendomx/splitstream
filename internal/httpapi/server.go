package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// Disconnecter corta la publicación de ingesta en curso sin dejar de escuchar. Lo cumple
// *rtmpio.Ingest (Task 9).
//
// Es una interfaz y no el tipo concreto para que este paquete no importe go-rtmp ni de
// forma transitiva: la CI lo comprueba.
type Disconnecter interface {
	DisconnectPublisher() int
}

// SinkBuilder construye el sink de un destino para aplicar cambios en caliente. Lo cumple
// *sinks.Factory (Task 8).
type SinkBuilder interface {
	Build(ctx context.Context, d store.Destination) (*relay.Sink, error)
}

// EngineView es lo que la API necesita saber del motor: si hay sesión y cómo va cada
// destino. Lo cumple *relay.Engine.
//
// Es una interfaz y no el tipo concreto porque así los tests de la API pueden simular una
// sesión viva sin montar un motor entero con su ingesta: montarlo costaría un servidor
// RTMP y un publisher real en cada test de un handler.
type EngineView interface {
	SessionID() int64
	Snapshot() map[int64]relay.Metrics
}

// HubView es lo que la API necesita del hub para aplicar un cambio en caliente. Lo cumple
// *relay.Hub.
//
// Add reemplaza un sink existente con el mismo id sin dejar ventana de escritura doble, y
// Remove lo para: los dos comportamientos son de la fase 2, y son justo lo que hace falta
// para editar un destino a mitad de transmisión.
type HubView interface {
	Add(s *relay.Sink)
	Remove(id int64)
}

// Config son las dependencias del servidor. DB y Cipher son obligatorias; el resto puede
// ser nil en los tests que no las ejercitan.
type Config struct {
	DB     *store.DB
	Cipher *crypto.Cipher
	Engine EngineView
	Hub    HubView
	Ingest Disconnecter
	Sinks  SinkBuilder
	// MasterKey solo se usa para derivar la clave de firma de la cookie; no se guarda.
	MasterKey [32]byte
	Logger    *slog.Logger
	// RTMPAddr es la dirección donde escucha la ingesta. Solo se usa el puerto: el host de
	// la URL que se le enseña al usuario sale de la petición, porque el panel se alcanza
	// por algún nombre concreto y la ingesta está en esa misma máquina.
	RTMPAddr string
	// SecureCookies marca la cookie como Secure. Va en la configuración y no se deduce de
	// la petición porque en el despliegue del spec §12 el TLS lo termina un proxy y el
	// binario solo ve HTTP: adivinarlo daría una cookie sin Secure justo en producción.
	SecureCookies bool
}

// Server sirve la API del spec §9.
type Server struct {
	db       *store.DB
	cipher   *crypto.Cipher
	engine   EngineView
	hub      HubView
	ingest   Disconnecter
	sinks    SinkBuilder
	signer   *sessionSigner
	limiter  *loginLimiter
	logger   *slog.Logger
	secure   bool
	rtmpPort string
	mux      *http.ServeMux
}

func New(cfg Config) (*Server, error) {
	if cfg.DB == nil {
		return nil, errors.New("httpapi: falta DB")
	}
	if cfg.Cipher == nil {
		return nil, errors.New("httpapi: falta Cipher")
	}
	signer, err := newSessionSigner(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		db: cfg.DB, cipher: cfg.Cipher, engine: cfg.Engine, hub: cfg.Hub,
		ingest: cfg.Ingest, sinks: cfg.Sinks,
		signer: signer, limiter: newLoginLimiter(), logger: logger,
		secure: cfg.SecureCookies, mux: http.NewServeMux(),
	}
	if _, puerto, err := net.SplitHostPort(cfg.RTMPAddr); err == nil {
		s.rtmpPort = puerto
	}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

// routes registra las rutas del spec §9. Los patrones con método son de Go 1.22, así que
// no hace falta router externo.
//
// Se declaran TODAS aquí: así la lista de qué existe y qué necesita sesión se escribe en un
// solo sitio, y no se puede añadir un endpoint olvidándose de protegerlo.
func (s *Server) routes() {
	// Públicas: son el camino para conseguir una sesión.
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)

	protegida := func(pattern string, h http.HandlerFunc) {
		s.mux.Handle(pattern, s.requireSession(h))
	}

	protegida("GET /api/ingest", s.handleGetIngest)
	protegida("POST /api/ingest/rotate-key", s.handleRotateIngestKey)
	protegida("GET /api/destinations", s.handleListDestinations)
	protegida("POST /api/destinations", s.handleCreateDestination)
	protegida("PATCH /api/destinations/{id}", s.handlePatchDestination)
	protegida("DELETE /api/destinations/{id}", s.handleDeleteDestination)
	protegida("POST /api/destinations/{id}/toggle", s.handleToggleDestination)
	protegida("POST /api/destinations/reorder", s.handleReorderDestinations)
	protegida("GET /api/destinations/{id}/key", s.handleRevealDestinationKey)
	protegida("GET /api/status", s.handleStatus)
	protegida("GET /api/events", s.handleEvents)
	protegida("GET /ws", s.handleWS)
}

// clientIP saca la IP para el limitador. No se mira X-Forwarded-For: quien llega directo
// puede inventárselo y saltarse el límite creando una IP nueva por intento.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
