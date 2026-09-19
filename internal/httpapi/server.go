package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/aprendomx/splitstream/internal/chat"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/probe"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// TokenGetter es lo que la API necesita del gestor de tokens: entregar el token vigente de
// una cuenta. Es una interfaz, y no tokens.Manager, para que este paquete no importe
// internal/platforms/tokens ni de forma transitiva.
type TokenGetter interface {
	Token(ctx context.Context, accountID int64) (crypto.Secret, error)
}

// QuotaReader dice cuánta cuota lleva gastada hoy una cuenta. Lo cumple *quota.Counter
// (Task 6); es una interfaz para que este paquete no importe internal/platforms/quota ni
// —por su cadena— los proveedores, igual que TokenGetter.
type QuotaReader interface {
	UsedToday(ctx context.Context, accountID int64) (int, error)
}

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

	// StartLocalSession, OnMessage y OnPublishEnd son la ingesta de la cámara del
	// navegador (spec cámara §4): aquí la API ES el publisher. Son los mismos métodos
	// con los que rtmpio alimenta al motor, a través de una interfaz que no lo importa.
	StartLocalSession() error
	OnMessage(msg *relay.Message)
	OnPublishEnd()
}

// WebhookSender manda un evento a un webhook. Lo cumple *alerts.WebhookDispatcher; la API
// lo usa para el botón «Probar».
type WebhookSender interface {
	Send(ctx context.Context, w store.Webhook, ev store.Event) error
}

// UpdateStatus es lo que el checker de versiones sabe; este paquete no lo importa, lo
// recibe como función igual que ExtraMetrics.
type UpdateStatus struct {
	Latest    string
	URL       string
	Available bool
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
	// TLS y PublicURL describen cómo se sirve el panel (ver panelDTO). Son datos: este
	// paquete no termina TLS ni importa webtls.
	TLS       bool
	PublicURL string
	// MetricsToken autoriza GET /metrics con `Authorization: Bearer` sin cookie de sesión,
	// para que Prometheus pueda scrapearlo. Vacío: solo la cookie.
	MetricsToken string
	// ExtraMetrics aporta series adicionales a /metrics —el bus de eventos, los
	// webhooks— sin que este paquete tenga que importar esos componentes.
	ExtraMetrics []ExtraMetrics
	// UpdateInfo da el último resultado del checker de versiones (Task 5). Nil: sin
	// aviso, igual que ExtraMetrics, para que este paquete no importe internal/update.
	UpdateInfo func() UpdateStatus
	// Platforms es el registro de proveedores (Twitch, y en la v0.12 YouTube y Kick).
	// Nil en los tests que no lo ejercitan: /api/platforms devuelve una lista vacía.
	Platforms *platforms.Registry
	// Tokens entrega tokens vigentes; es tokens.Manager sin importarlo.
	Tokens TokenGetter
	// Chat es el bus del chat de la sesión; nil desactiva /api/chat/ws (404).
	Chat *chat.Bus
	// ChatStats alimenta /metrics.
	ChatStats func() (map[platforms.ID]uint64, map[platforms.ID]bool, uint64)
	// ChatIngest mete en el bus los mensajes que llegan por webhook (Kick) y devuelve
	// cuántos aceptó; es chat.Aggregator.Ingest sin importarlo. No bloquea. Nil: el
	// webhook sigue respondiendo 200 y los mensajes se descartan.
	ChatIngest func([]platforms.ChatMessage) int
	// ChatBudget son las unidades de cuota diarias que el lector de chat de YouTube tiene
	// permitido gastar. El panel lo enseña junto a la cuota consumida para que se entienda
	// por qué el chat se pausa; aquí es un dato que se pasa tal cual, no un límite que
	// este paquete aplique. 0: el panel no enseña presupuesto.
	ChatBudget int
	// YouTubeQuota es la cuota diaria del proyecto de Google, la que Google aplica y este
	// binario solo repite (SPLITSTREAM_YOUTUBE_QUOTA). Viaja en el estado para que el
	// panel pueda enseñar el presupuesto del chat como lo que es: una parte de ella.
	// 0: el panel no la enseña.
	YouTubeQuota int
	// Quota lee la cuota gastada hoy por cuenta (YouTube). Nil: el panel y /metrics no
	// enseñan cuota, que es distinto de enseñar cero.
	Quota QuotaReader
	// BaseContext es el padre de los flujos de autorización en curso (platforms.go): al
	// cancelarlo, todos los sondeos en marcha cortan en vez de seguir vivos hasta que
	// venza su código de dispositivo. Nil usa context.Background(); main.go le pasará el
	// contexto de vida de los sinks en la Task 7.
	BaseContext context.Context
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
	tls          bool
	publicURL    string
	updateInfo   func() UpdateStatus
	platforms    *platforms.Registry
	tokens       TokenGetter
	chat         *chat.Bus
	chatStats    func() (map[platforms.ID]uint64, map[platforms.ID]bool, uint64)
	chatIngest   func([]platforms.ChatMessage) int
	chatBudget   int
	ytQuota      int
	quota        QuotaReader
	// now es la hora del servidor para firmar y verificar el state del flujo con redirect.
	// Campo y no time.Now directo para que un test pueda firmar un state ya caducado sin
	// esperar once minutos.
	now func() time.Time
	// rechazoMu y ultimoRechazo acotan el evento `webhook_rejected` a uno por minuto: un
	// atacante —o una plataforma mal configurada— podría llenar el registro con un bucle
	// de webhooks inválidos (ver webhook.go).
	rechazoMu     sync.Mutex
	ultimoRechazo time.Time
	// liveTimeout es el plazo por destino de POST /api/live/title (ver live.go). Campo y
	// no constante para que los tests puedan bajarlo sin dormir diez segundos.
	liveTimeout time.Duration
	// auths son los flujos de dispositivo en curso (ver platforms.go).
	auths *authFlows
	// baseCtx es el padre de los flujos de autorización en curso; wg cuenta sus
	// goroutines de sondeo para que Wait() pueda esperarlas.
	baseCtx context.Context
	wg      sync.WaitGroup
	// cameraCtx es el padre de cada WebSocket de la cámara del navegador (camera.go).
	// Deliberadamente independiente de baseCtx: main.go cancela baseCtx (vía cancelSinks)
	// DESPUÉS de WaitIdle, así que colgar la cámara de él no cortaría nada a tiempo.
	// cancelCameras es lo que DisconnectCameras acciona, el equivalente de Ingest.Close()
	// para RTMP: el gancho que el apagado ordenado necesita para sacar al publisher ANTES
	// de que main.go espere WaitIdle.
	cameraCtx     context.Context
	cancelCameras context.CancelFunc
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
	baseCtx := cfg.BaseContext
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	cameraCtx, cancelCameras := context.WithCancel(context.Background())

	s := &Server{
		db: cfg.DB, cipher: cfg.Cipher, engine: cfg.Engine,
		ingest: cfg.Ingest, sinks: cfg.Sinks, tester: cfg.Tester, webhooks: cfg.Webhooks,
		recorder: cfg.Recorder, recDir: cfg.RecordingsDir,
		signer: signer, limiter: newLoginLimiter(), logger: logger,
		setupCode: cfg.SetupCode, version: cfg.Version, spa: cfg.SPA,
		secure: cfg.SecureCookies, mux: http.NewServeMux(),
		metricsToken: cfg.MetricsToken, extra: cfg.ExtraMetrics,
		proxies: cfg.TrustedProxies,
		tls:     cfg.TLS, publicURL: cfg.PublicURL,
		updateInfo: cfg.UpdateInfo,
		platforms:  cfg.Platforms, tokens: cfg.Tokens, chat: cfg.Chat, chatStats: cfg.ChatStats,
		chatIngest: cfg.ChatIngest, chatBudget: cfg.ChatBudget, ytQuota: cfg.YouTubeQuota, quota: cfg.Quota,
		auths:         &authFlows{flows: map[string]*authFlow{}},
		baseCtx:       baseCtx,
		liveTimeout:   liveTimeoutPorDefecto,
		now:           time.Now,
		cameraCtx:     cameraCtx,
		cancelCameras: cancelCameras,
	}
	if _, puerto, err := net.SplitHostPort(cfg.RTMPAddr); err == nil {
		s.rtmpPort = puerto
	}
	s.routes()
	return s, nil
}

// Handler envuelve el mux con conIdioma: así el idioma se negocia una vez por petición y
// writeError lo encuentra sin que ninguno de sus ~90 sitios de llamada cambie.
func (s *Server) Handler() http.Handler { return conIdioma(s.mux) }

// DisconnectCameras corta las sesiones de la cámara del navegador en curso. Existe por
// la misma razón que Ingest.Close() para RTMP: el apagado ordenado (main.go) necesita
// que el publisher se vaya ANTES de WaitIdle, y una conexión WebSocket secuestrada no se
// entera de http.Server.Shutdown ni de la cancelación del contexto de la petición.
func (s *Server) DisconnectCameras() { s.cancelCameras() }

// ruta es una entrada de la tabla de rutas: todo lo que hace falta saber de un endpoint,
// tanto para registrarlo en el mux como para contarlo en docs/api.md (spec v1.0 §5.1).
//
// Publica significa «no pide sesión», que es la excepción y lleva escrito al lado por qué.
// Envoltorio sustituye a requireSession cuando la ruta se autentica de otra forma: hoy solo
// /metrics, que además del panel acepta el token del recolector.
type ruta struct {
	Metodo     string
	Patron     string
	Handler    http.HandlerFunc
	Publica    bool
	Grupo      string
	Resumen    string
	Envoltorio func(http.Handler) http.Handler
}

// rutas es la tabla con TODOS los endpoints de la API (spec §9). Se declaran aquí y en
// ningún otro sitio: así la lista de qué existe y qué necesita sesión se escribe una sola
// vez, no se puede añadir un endpoint olvidándose de protegerlo, y docs/api.md se genera
// desde la misma fuente que el registro en el mux.
func (s *Server) rutas() []ruta {
	// publica y protegida arman las entradas; se leen igual que las llamadas que había
	// antes en routes() y dejan a la vista cuáles no piden sesión.
	publica := func(metodo, patron string, h http.HandlerFunc, grupo, resumen string) ruta {
		return ruta{Metodo: metodo, Patron: patron, Handler: h, Publica: true, Grupo: grupo, Resumen: resumen}
	}
	protegida := func(metodo, patron string, h http.HandlerFunc, grupo, resumen string) ruta {
		return ruta{Metodo: metodo, Patron: patron, Handler: h, Grupo: grupo, Resumen: resumen}
	}

	return []ruta{
		// Públicas: son el camino para conseguir una sesión.
		publica("POST", "/api/auth/login", s.handleLogin, "auth", "Entrega la cookie de sesión a cambio de la contraseña del panel"),
		publica("POST", "/api/auth/logout", s.handleLogout, "auth", "Cierra la sesión y caduca la cookie"),
		publica("GET", "/healthz", s.handleHealthz, "health", "Comprueba que el servidor y su base de datos responden"),
		// /metrics no es pública: pide sesión o, para el recolector, el token configurado.
		{Metodo: "GET", Patron: "/metrics", Handler: s.handleMetrics, Grupo: "metrics",
			Resumen: "Métricas del relay y de cada destino en formato Prometheus", Envoltorio: s.requireSessionOrToken},

		// La configuración inicial también es pública, por definición: existe justo cuando
		// todavía no hay contraseña con la que autenticarse. Se protege de otra forma —solo
		// funciona mientras no haya contraseña, y desde fuera exige el código de la consola—.
		publica("GET", "/api/setup", s.handleSetupEstado, "setup", "Dice si falta poner contraseña y si hará falta el código de la consola"),
		publica("POST", "/api/setup", s.handleSetup, "setup", "Fija la contraseña inicial del panel"),

		// El callback del flujo con redirect es público a la fuerza: quien llega es el
		// navegador que vuelve de la plataforma, y puede no traer la cookie del panel (otro
		// navegador, o el móvil). Lo que lo protege es el `state` firmado: sin una firma
		// nuestra y sin un flujo pendiente que le corresponda, no hace nada.
		publica("GET", "/api/platforms/{p}/callback", s.handleAuthCallback, "platforms", "Recoge la vuelta del navegador desde la plataforma y termina la autorización"),
		// El webhook del chat de Kick lo llama la plataforma, que tampoco tiene sesión. Lo
		// protege la firma del payload, que verifica el proveedor en ParseWebhook.
		publica("POST", "/api/platforms/kick/webhook", s.handleKickWebhook, "platforms", "Recibe los mensajes de chat que Kick manda por webhook"),

		protegida("GET", "/api/ingest", s.handleGetIngest, "ingest", "Datos de la ingesta RTMP, con la clave enmascarada"),
		protegida("POST", "/api/ingest/rotate-key", s.handleRotateIngestKey, "ingest", "Genera una clave de ingesta nueva y, si se pide, corta la publicación en curso"),
		protegida("GET", "/api/destinations", s.handleListDestinations, "destinations", "Lista los destinos con su estado y sus métricas"),
		protegida("POST", "/api/destinations", s.handleCreateDestination, "destinations", "Da de alta un destino con su URL RTMP y su clave"),
		protegida("PATCH", "/api/destinations/{id}", s.handlePatchDestination, "destinations", "Cambia los campos indicados de un destino"),
		protegida("DELETE", "/api/destinations/{id}", s.handleDeleteDestination, "destinations", "Borra un destino"),
		protegida("POST", "/api/destinations/{id}/toggle", s.handleToggleDestination, "destinations", "Enciende o apaga un destino"),
		protegida("POST", "/api/destinations/reorder", s.handleReorderDestinations, "destinations", "Reordena los destinos según la lista de ids que reciba"),
		// toggle-all va con nombre fijo, no con {id}: el mux de Go da preferencia al patrón
		// más específico, así que no compite con PATCH/DELETE /api/destinations/{id}.
		protegida("POST", "/api/destinations/toggle-all", s.handleToggleAllDestinations, "destinations", "Enciende o apaga todos los destinos de una vez"),
		// from-account va con nombre fijo, como toggle-all: tiene los mismos segmentos que
		// POST /api/destinations pero uno más, así que no compite con el alta normal.
		protegida("POST", "/api/destinations/from-account", s.handleCreateDestinationFromAccount, "destinations", "Crea un destino a partir de una cuenta ya vinculada"),
		protegida("POST", "/api/destinations/{id}/broadcast", s.handleCreateBroadcast, "destinations", "Crea en la plataforma la emisión del destino"),
		protegida("GET", "/api/destinations/{id}/broadcast", s.handleGetBroadcast, "destinations", "Consulta la emisión que la plataforma dio para el destino"),
		protegida("POST", "/api/destinations/{id}/broadcast/start", s.handleStartBroadcast, "destinations", "Pone en directo la emisión del destino"),
		protegida("POST", "/api/destinations/{id}/broadcast/end", s.handleEndBroadcast, "destinations", "Termina la emisión del destino"),
		protegida("GET", "/api/destinations/{id}/key", s.handleRevealDestinationKey, "destinations", "Devuelve la clave del destino en claro, para copiarla desde el panel"),
		protegida("POST", "/api/destinations/{id}/retry", s.handleRetryDestination, "destinations", "Fuerza un reintento de conexión del destino sin esperar la espera creciente"),
		protegida("POST", "/api/destinations/{id}/test", s.handleTestDestination, "destinations", "Prueba la conexión con el destino sin emitir nada"),
		protegida("PUT", "/api/destinations/{id}/logo", s.handlePutDestinationLogo, "destinations", "Sube el logotipo del destino"),
		protegida("GET", "/api/destinations/{id}/logo", s.handleGetDestinationLogo, "destinations", "Devuelve el logotipo del destino"),
		protegida("DELETE", "/api/destinations/{id}/logo", s.handleDeleteDestinationLogo, "destinations", "Borra el logotipo del destino"),
		protegida("GET", "/api/webhooks", s.handleListWebhooks, "webhooks", "Lista los webhooks de notificación"),
		protegida("POST", "/api/webhooks", s.handleCreateWebhook, "webhooks", "Da de alta un webhook de notificación"),
		protegida("PATCH", "/api/webhooks/{id}", s.handlePatchWebhook, "webhooks", "Cambia los campos indicados de un webhook"),
		protegida("DELETE", "/api/webhooks/{id}", s.handleDeleteWebhook, "webhooks", "Borra un webhook"),
		protegida("POST", "/api/webhooks/{id}/test", s.handleTestWebhook, "webhooks", "Manda un evento de prueba al webhook"),
		protegida("GET", "/api/status", s.handleStatus, "live", "Estado completo del panel: ingesta, sesión, destinos, grabación y eventos recientes"),
		protegida("GET", "/api/events", s.handleEvents, "live", "Lista los eventos del registro, de los más nuevos a los más viejos"),
		protegida("GET", "/api/sessions", s.handleSessions, "sessions", "Historial de sesiones con el recuento de eventos de cada una"),
		protegida("GET", "/api/sessions/{id}", s.handleSessionDetail, "sessions", "Ficha de una sesión con eventos, grabaciones y chat"),
		protegida("POST", "/api/backup", s.handleBackup, "backup", "Descarga una copia de seguridad consistente de la base de datos"),
		protegida("GET", "/api/recording/settings", s.handleGetRecordingSettings, "recordings", "Ajustes de grabación y espacio en disco"),
		protegida("PATCH", "/api/recording/settings", s.handlePatchRecordingSettings, "recordings", "Cambia los ajustes de grabación"),
		protegida("GET", "/api/recordings", s.handleListRecordings, "recordings", "Lista las grabaciones guardadas"),
		protegida("GET", "/api/recordings/{id}/download", s.handleDownloadRecording, "recordings", "Descarga el archivo de una grabación"),
		protegida("DELETE", "/api/recordings/{id}", s.handleDeleteRecording, "recordings", "Borra una grabación y su archivo"),
		protegida("GET", "/ws", s.handleWS, "ws", "Canal WebSocket con el estado del panel en tiempo real"),
		protegida("GET", "/api/preview/ws", s.handlePreviewWS, "ws", "Canal WebSocket con la vista previa silenciada de la ingesta"),
		protegida("GET", "/api/camera/ws", s.handleCameraWS, "ws", "Canal WebSocket por el que el navegador publica su cámara como fuente de la emisión"),

		// Plataformas, flujo de autorización sondeado en el servidor, y cuentas (v0.11 §6.1).
		protegida("GET", "/api/platforms", s.handleListPlatforms, "platforms", "Lista las plataformas soportadas y lo que cada una permite hacer"),
		protegida("POST", "/api/platforms/{p}/auth", s.handleStartAuth, "platforms", "Arranca la autorización de una cuenta en la plataforma"),
		// El literal "twitch/categories" es más específico que "{p}/auth/{state}" y tiene un
		// segmento menos, así que no compite con él en el mux.
		protegida("GET", "/api/platforms/twitch/categories", s.handleSearchCategories, "platforms", "Busca categorías de Twitch por texto"),
		protegida("GET", "/api/platforms/{p}/auth/{state}", s.handleAuthStatus, "platforms", "Dice cómo va una autorización en curso"),
		protegida("GET", "/api/accounts", s.handleListAccounts, "accounts", "Lista las cuentas vinculadas y su estado"),
		protegida("DELETE", "/api/accounts/{id}", s.handleDeleteAccount, "accounts", "Desvincula una cuenta y olvida sus tokens"),
		protegida("POST", "/api/live/title", s.handleLiveTitle, "live", "Cambia el título y la categoría en las plataformas de los destinos indicados"),

		// Chat de la sesión: en vivo por WebSocket, e historial paginado por sesión.
		protegida("GET", "/api/chat/ws", s.handleChatWS, "chat", "Canal WebSocket con el chat unificado de la sesión en vivo"),
		protegida("GET", "/api/sessions/{id}/chat", s.handleSessionChat, "chat", "Historial paginado del chat de una sesión"),
	}
}

// routes registra en el mux la tabla de rutas(). Los patrones con método son de Go 1.22,
// así que no hace falta router externo.
func (s *Server) routes() {
	for _, r := range s.rutas() {
		patron := r.Metodo + " " + r.Patron
		switch {
		case r.Envoltorio != nil:
			s.mux.Handle(patron, r.Envoltorio(r.Handler))
		case r.Publica:
			s.mux.HandleFunc(patron, r.Handler)
		default:
			s.mux.Handle(patron, s.requireSession(r.Handler))
		}
	}

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
