package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/probe"
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

// DestinationTester sondea un destino sin emitir. Lo cumple *sinks.Factory. Devuelve
// probe.Result y no un tipo de rtmpio para que este paquete no importe go-rtmp ni de
// forma transitiva.
type DestinationTester interface {
	Test(ctx context.Context, d store.Destination) (probe.Result, error)
}

// RecorderBuilder construye el sink de grabación de una sesión (nil si está apagada o no
// hay sitio). Lo cumple *sinks.Factory. La API lo usa al encender la grabación en caliente.
type RecorderBuilder interface {
	BuildRecorder(ctx context.Context, sessionID int64) (*relay.Sink, error)
}

// EngineView es lo que la API necesita saber del motor: si hay sesión y cómo va cada
// destino. Lo cumple *relay.Engine.
//
// Es una interfaz y no el tipo concreto porque así los tests de la API pueden simular una
// sesión viva sin montar un motor entero con su ingesta: montarlo costaría un servidor
// RTMP y un publisher real en cada test de un handler.
type EngineView interface {
	// Session describe la sesión en curso: id, arranque, resolución y bitrate medido. Se
	// pide al motor y no a la base porque la fila de sessions solo se completa al CERRAR,
	// así que durante la emisión tendría la resolución en null — que es justo lo que el
	// panel del spec §10 necesita enseñar en vivo.
	Session() relay.LiveSession
	Snapshot() map[int64]relay.Metrics

	// AddSink arranca el sink y lo mete en el reparto; RemoveSink lo para. Van en el
	// motor y no en el hub porque arrancar necesita el contexto de vida del proceso y el
	// preámbulo, y los dos los tiene él. Hub.Add solo registra: usarlo a secas dejaba el
	// destino en idle descartando mensajes.
	AddSink(s *relay.Sink)
	RemoveSink(id int64)

	// Tap y VideoConfig alimentan la vista previa (spec vista previa §3 y §5): un grifo
	// de solo lectura sobre el hub y el sequence header con el avcC. En el fake de los
	// tests son un canal y un slice que el test controla.
	Tap() (<-chan *relay.Message, func())
	VideoConfig() []byte
}

// WebhookSender manda un evento a un webhook. Lo cumple *alerts.WebhookDispatcher; la API
// lo usa para el botón «Probar».
type WebhookSender interface {
	Send(ctx context.Context, w store.Webhook, ev store.Event) error
}

// Config son las dependencias del servidor. DB y Cipher son obligatorias; el resto puede
// ser nil en los tests que no las ejercitan.
type Config struct {
	DB     *store.DB
	Cipher *crypto.Cipher
	Engine EngineView
	Ingest Disconnecter
	Sinks  SinkBuilder
	Tester DestinationTester
	// Recorder construye el sink de grabación al encenderla en caliente. Nil en los tests
	// que no la ejercitan.
	Recorder RecorderBuilder
	// RecordingsDir es el directorio raíz de las grabaciones: de ahí sale lo que enseña
	// GET /api/recording/settings y lo que sirve la descarga.
	RecordingsDir string
	// Webhooks manda el evento sintético del botón «Probar». Nil en los tests que no lo
	// ejercitan: handleTestWebhook responde 409 en vez de entrar en pánico.
	Webhooks WebhookSender
	// MasterKey solo se usa para derivar la clave de firma de la cookie; no se guarda.
	MasterKey [32]byte
	Logger    *slog.Logger
	// RTMPAddr es la dirección donde escucha la ingesta. Solo se usa el puerto: el host de
	// la URL que se le enseña al usuario sale de la petición, porque el panel se alcanza
	// por algún nombre concreto y la ingesta está en esa misma máquina.
	RTMPAddr string
	// Version es la del binario, para enseñarla en los créditos del panel.
	Version string
	// SetupCode es el código de un solo uso del primer arranque. Solo hace falta cuando el
	// panel se abre desde OTRA máquina: en local, quien está en el teclado ya controla el
	// equipo. Vacío si el servicio ya tiene contraseña.
	SetupCode string
	// SPA son los archivos del panel compilado. Si es nil, el binario solo sirve la API:
	// útil en los tests, que no necesitan el frontend para nada.
	SPA fs.FS
	// SecureCookies marca la cookie como Secure. Va en la configuración y no se deduce de
	// la petición porque en el despliegue del spec §12 el TLS lo termina un proxy y el
	// binario solo ve HTTP: adivinarlo daría una cookie sin Secure justo en producción.
	SecureCookies bool
	// TrustedProxies son las redes desde las que se cree X-Forwarded-For (ver clientip.go).
	TrustedProxies []netip.Prefix
	// MetricsToken autoriza GET /metrics con `Authorization: Bearer` sin cookie de sesión,
	// para que Prometheus pueda scrapearlo. Vacío: solo la cookie.
	MetricsToken string
	// ExtraMetrics aporta series adicionales a /metrics —el bus de eventos, los
	// webhooks— sin que este paquete tenga que importar esos componentes.
	ExtraMetrics []ExtraMetrics
}

// Server sirve la API del spec §9.
type Server struct {
	db           *store.DB
	cipher       *crypto.Cipher
	engine       EngineView
	ingest       Disconnecter
	sinks        SinkBuilder
	tester       DestinationTester
	recorder     RecorderBuilder
	recDir       string
	webhooks     WebhookSender
	signer       *sessionSigner
	limiter      *loginLimiter
	logger       *slog.Logger
	setupCode    string
	version      string
	spa          fs.FS
	secure       bool
	rtmpPort     string
	mux          *http.ServeMux
	metricsToken string
	extra        []ExtraMetrics
	proxies      []netip.Prefix
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
		db: cfg.DB, cipher: cfg.Cipher, engine: cfg.Engine,
		ingest: cfg.Ingest, sinks: cfg.Sinks, tester: cfg.Tester, webhooks: cfg.Webhooks,
		recorder: cfg.Recorder, recDir: cfg.RecordingsDir,
		signer: signer, limiter: newLoginLimiter(), logger: logger,
		setupCode: cfg.SetupCode, version: cfg.Version, spa: cfg.SPA,
		secure: cfg.SecureCookies, mux: http.NewServeMux(),
		metricsToken: cfg.MetricsToken, extra: cfg.ExtraMetrics,
		proxies: cfg.TrustedProxies,
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
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.Handle("GET /metrics", s.requireSessionOrToken(http.HandlerFunc(s.handleMetrics)))

	// La configuración inicial también es pública, por definición: existe justo cuando
	// todavía no hay contraseña con la que autenticarse. Se protege de otra forma —solo
	// funciona mientras no haya contraseña, y desde fuera exige el código de la consola—.
	s.mux.HandleFunc("GET /api/setup", s.handleSetupEstado)
	s.mux.HandleFunc("POST /api/setup", s.handleSetup)

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
	// toggle-all va con nombre fijo, no con {id}: el mux de Go da preferencia al patrón
	// más específico, así que no compite con PATCH/DELETE /api/destinations/{id}.
	protegida("POST /api/destinations/toggle-all", s.handleToggleAllDestinations)
	protegida("GET /api/destinations/{id}/key", s.handleRevealDestinationKey)
	protegida("POST /api/destinations/{id}/retry", s.handleRetryDestination)
	protegida("POST /api/destinations/{id}/test", s.handleTestDestination)
	protegida("PUT /api/destinations/{id}/logo", s.handlePutDestinationLogo)
	protegida("GET /api/destinations/{id}/logo", s.handleGetDestinationLogo)
	protegida("DELETE /api/destinations/{id}/logo", s.handleDeleteDestinationLogo)
	protegida("GET /api/webhooks", s.handleListWebhooks)
	protegida("POST /api/webhooks", s.handleCreateWebhook)
	protegida("PATCH /api/webhooks/{id}", s.handlePatchWebhook)
	protegida("DELETE /api/webhooks/{id}", s.handleDeleteWebhook)
	protegida("POST /api/webhooks/{id}/test", s.handleTestWebhook)
	protegida("GET /api/status", s.handleStatus)
	protegida("GET /api/events", s.handleEvents)
	protegida("GET /api/sessions", s.handleSessions)
	protegida("POST /api/backup", s.handleBackup)
	protegida("GET /api/recording/settings", s.handleGetRecordingSettings)
	protegida("PATCH /api/recording/settings", s.handlePatchRecordingSettings)
	protegida("GET /api/recordings", s.handleListRecordings)
	protegida("GET /api/recordings/{id}/download", s.handleDownloadRecording)
	protegida("DELETE /api/recordings/{id}", s.handleDeleteRecording)
	protegida("GET /ws", s.handleWS)
	protegida("GET /api/preview/ws", s.handlePreviewWS)

	// El panel va en la raíz y se registra el ÚLTIMO: en el mux de Go 1.22 los patrones
	// más específicos ganan, así que /api/... y /ws siguen entrando por sus handlers.
	//
	// No lleva requireSession: sin sesión, la propia interfaz enseña el login. Protegerlo
	// obligaría a servir una página de login aparte, y todo lo que hay detrás —los datos—
	// ya está protegido en la API.
	if s.spa != nil {
		// Cortafuegos antes del panel: cualquier /api/... que no case con un endpoint
		// concreto responde JSON, no HTML. Sin esto, una ruta mal escrita devolvería el
		// index y el cliente fallaría al parsear con un error que no dice nada. El patrón
		// "/api/" es menos específico que los endpoints, así que solo recoge lo que sobra.
		s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, codeNotFound, "no existe ese endpoint")
		})
		// Sin método en el patrón: "GET /" y "/api/" son ambiguos para el mux —uno tiene
		// método pero ruta más general que el otro— y registrar los dos hace panic. El
		// método se comprueba dentro del handler.
		s.mux.HandleFunc("/", s.serveSPA(s.spa))
	}
}
