// Command splitstream es el servicio de retransmisión RTMP.
//
// Fase 3: recibe un stream por RTMP, lo reparte a N destinos a la vez con cola
// acotada y reconexión con backoff, y apaga ordenadamente con SIGTERM. Todavía no
// hay API HTTP ni panel web.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aprendomx/splitstream/internal/alerts"
	"github.com/aprendomx/splitstream/internal/chat"
	"github.com/aprendomx/splitstream/internal/config"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/events"
	"github.com/aprendomx/splitstream/internal/httpapi"
	"github.com/aprendomx/splitstream/internal/maintenance"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/platforms/kick"
	"github.com/aprendomx/splitstream/internal/platforms/quota"
	"github.com/aprendomx/splitstream/internal/platforms/tokens"
	"github.com/aprendomx/splitstream/internal/platforms/twitch"
	"github.com/aprendomx/splitstream/internal/platforms/youtube"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/sinks"
	"github.com/aprendomx/splitstream/internal/store"
	"github.com/aprendomx/splitstream/internal/update"
	"github.com/aprendomx/splitstream/internal/webtls"
	"github.com/aprendomx/splitstream/web"
)

// version la fija el Makefile con -ldflags a partir de `git describe`. Un `go build`
// o un `go run` sin flags la dejan en "dev": el binario sigue siendo utilizable y dice
// la verdad sobre su procedencia.
var version = "dev"

func main() {
	genkey := flag.Bool("genkey", false, "imprime una SPLITSTREAM_MASTER_KEY nueva y sale")
	showVersion := flag.Bool("version", false, "imprime la versión del binario y sale")
	// Sin comillas invertidas en el texto: el paquete flag las interpreta como el nombre
	// del operando y la ayuda sale rota.
	setpw := flag.Bool("setpassword", false,
		"lee una contraseña de stdin y la fija como la del panel; "+
			"invócalo como: read -rs PW && printf '%s' \"$PW\" | splitstream -setpassword")
	hc := flag.Bool("healthcheck", false,
		"consulta /healthz del servicio local y sale 0 si responde; para el HEALTHCHECK de Docker")
	bk := flag.String("backup", "", "escribe una copia consistente de la base en la ruta dada y sale")
	flag.Parse()

	if *showVersion {
		printVersion(os.Stdout)
		return
	}

	if *hc {
		// Solo el puerto y si hay TLS: config.Load crearía un archivo de clave si no lo
		// hubiera, y un healthcheck no debe tener efectos secundarios.
		addr := os.Getenv("SPLITSTREAM_HTTP_ADDR")
		// TrimSpace porque config.go también recorta el dominio: si aquí no se hiciera, un
		// espacio de más en el .env daría un SNI distinto al del certificado servido.
		dominio := strings.TrimSpace(os.Getenv("SPLITSTREAM_TLS_DOMAIN"))
		conTLS := dominio != "" || os.Getenv("SPLITSTREAM_TLS_CERT_FILE") != ""
		if err := healthcheck(addr, conTLS, dominio); err != nil {
			fmt.Fprintln(os.Stderr, "healthcheck:", err)
			os.Exit(1)
		}
		return
	}

	if *setpw {
		if err := setPassword(context.Background(), os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	if *bk != "" {
		if err := backup(context.Background(), *bk, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	if *genkey {
		key, err := generateMasterKey()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println(key)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Stdout); err != nil {
		// El error puede venir de config o de la master key; ninguno de los dos
		// incluye material secreto en su mensaje.
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// printVersion escribe la versión en una sola línea. No toca la configuración ni la
// base de datos: debe funcionar en un contenedor recién arrancado, sin master key.
func printVersion(out io.Writer) {
	fmt.Fprintf(out, "splitstream %s\n", version)
}

// minPasswordLen es el mínimo aceptable. No es una política de seguridad seria, es un
// filtro contra el descuido: el panel queda expuesto a internet y una contraseña de tres
// letras no es una contraseña.
//
// internal/httpapi tiene su propia constante con el mismo valor, para el asistente del
// primer arranque. Son dos caminos distintos hacia la misma regla; si uno cambia, el otro
// también debe hacerlo.
const minPasswordLen = 8

// readPassword lee una contraseña de una línea de r.
//
// Quita solo el salto de línea final —y el retorno de carro, por si viene pegada desde
// Windows—: los espacios interiores son parte de la contraseña, porque una frase de paso
// los lleva.
//
// No se suprime el eco del terminal: eso necesitaría golang.org/x/term, que no está entre
// las cinco dependencias que el spec §5 permite. La invocación recomendada deja que lo
// haga el shell, que ya sabe:
//
//	read -rs PW && printf '%s' "$PW" | splitstream -setpassword && unset PW
func readPassword(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("leer la contraseña: %w", err)
	}
	pw := strings.TrimRight(line, "\r\n")

	if strings.TrimSpace(pw) == "" {
		return "", errors.New("la contraseña no puede estar vacía")
	}
	if len(pw) < minPasswordLen {
		return "", fmt.Errorf("la contraseña necesita al menos %d caracteres", minPasswordLen)
	}
	return pw, nil
}

// setPassword fija la contraseña del panel. No imprime nada que dependa de ella: ni la
// contraseña, ni su longitud, ni un prefijo (spec §8).
func setPassword(ctx context.Context, in io.Reader, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	pw, err := readPassword(in)
	if err != nil {
		return err
	}

	hash, err := crypto.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("hashear la contraseña: %w", err)
	}

	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	cipher, err := crypto.NewCipher(cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("inicializar el cifrado: %w", err)
	}
	// Bootstrap deja la fila de settings creada; sin él, SetPasswordHash no tiene dónde
	// escribir en una base recién hecha. Es idempotente: sobre una base existente no
	// rota nada, solo comprueba la master key.
	if err := db.Bootstrap(ctx, cipher); err != nil {
		return err
	}

	if err := db.SetPasswordHash(ctx, hash); err != nil {
		return err
	}

	fmt.Fprintln(out, "contraseña del panel actualizada")
	return nil
}

// generateMasterKey produce una master key de 32 bytes en base64 estándar.
func generateMasterKey() (string, error) {
	buf := make([]byte, config.MasterKeyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generar master key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func run(ctx context.Context, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	cipher, err := crypto.NewCipher(cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("inicializar el cifrado: %w", err)
	}

	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Bootstrap(ctx, cipher); err != nil {
		if errors.Is(err, crypto.ErrWrongMasterKey) {
			return fmt.Errorf(
				"%w: %s fue cifrada con otra SPLITSTREAM_MASTER_KEY. "+
					"Restaura la clave original o empieza con una base de datos nueva",
				err, cfg.DBPath)
		}
		return err
	}

	settings, err := db.Settings(ctx)
	if err != nil {
		return err
	}

	// El bus de eventos: todo lo que se persiste en `events` sale también por aquí, para
	// las alertas del panel y los webhooks. Se cablea antes que el motor y la API para que
	// ningún evento del arranque se quede sin anunciar.
	bus := events.NewBus()
	db.SetEventHook(bus.Publish)

	// Los sinks NO heredan el contexto de señales: si lo hicieran, un SIGTERM los mataría
	// antes de que el cierre ordenado del spec §6.5 pudiera mandar su FCUnpublish. Este
	// contexto se cancela al final, tras la espera.
	sinkCtx, cancelSinks := context.WithCancel(context.Background())
	defer cancelSinks()

	// fondo agrupa a los consumidores del bus que escriben en la base —el despachador de
	// webhooks y el mantenimiento—. Hay que verlos volver ANTES de salir de run(), porque
	// al salir corre el `defer db.Close()` de arriba y una entrega en vuelo todavía tiene
	// que apuntar su resultado con RecordWebhookDelivery.
	var fondo sync.WaitGroup

	// Webhooks salientes: consumen el bus en su propia goroutine y jamás lo frenan.
	webhooks := alerts.NewWebhookDispatcher(bus, db, cipher, logger, version)
	fondo.Add(1)
	go func() {
		defer fondo.Done()
		webhooks.Run(sinkCtx)
	}()

	// El TLS integrado se construye ANTES de la Config de httpapi y del bloque de
	// plataformas: PublicURL sale de aquí y ese paquete no sabe nada de webtls (la CI lo
	// comprueba); el job de mantenimiento de Kick necesita publicURL más abajo.
	tlsSetup, err := webtls.Build(cfg, func(err error) {
		// Sin secretos: el dominio y el texto de ACME. Cadencia acotada por webtls.
		if _, e := db.LogEvent(context.Background(), store.Event{
			Level: store.LevelError, Kind: "tls_certificate_error",
			Message: "no se pudo obtener el certificado de " + cfg.TLSDomain + ": " + err.Error(),
		}); e != nil {
			logger.Error("no se pudo registrar el fallo de certificado", "err", e)
		}
	})
	if err != nil {
		return err
	}
	if cfg.SecureCookiesDesactivadas {
		logger.Warn("SPLITSTREAM_SECURE_COOKIES=false con TLS integrado: la cookie de sesión viaja sin Secure")
	}
	publicURL := ""
	if tlsSetup != nil {
		publicURL = tlsSetup.PublicURL
	}

	// Capa de plataformas (v0.11, con YouTube y Kick desde v0.12): proveedores por
	// capacidad, tokens y chat. Nada de esto lo conoce el motor; entra por el bus de
	// eventos y por la API.
	cuota := quota.NewCounter(db)
	cuota.Logger = logger
	registro := platforms.NewRegistry(
		twitch.New(twitch.Options{ClientID: twitch.ResolveClientID(cfg.TwitchClientID), Logger: logger}),
		youtube.New(youtube.Options{Logger: logger, Quota: cuota.Sink,
			ChatBudget: func(accountID int64) (int, int, bool) {
				used, err := cuota.UsedToday(context.Background(), accountID)
				return used, cfg.YouTubeChatBudget, err == nil
			},
			OnChatPaused: func(acct store.Account, used, budget int) {
				db.LogEvent(context.Background(), store.Event{Level: store.LevelWarn, Kind: "chat_paused_quota",
					Message: fmt.Sprintf("el chat de YouTube de %s se pausó al llegar a %d de %d unidades; se reanuda mañana o si subes SPLITSTREAM_YOUTUBE_CHAT_BUDGET", acct.DisplayName, used, budget)})
			}}),
		kick.New(kick.Options{Logger: logger}),
	)
	gestorTokens := tokens.NewManager(db, cipher, registro.Get)
	gestorTokens.Logger = logger
	gestorTokens.OnReauth = func(acct store.Account) {
		if _, err := db.LogEvent(context.Background(), store.Event{Level: store.LevelWarn, Kind: "account_reauth_required",
			Message: "la cuenta de " + string(acct.Platform) + " " + acct.DisplayName + " necesita reconectarse"}); err != nil {
			logger.Error("no se pudo registrar el aviso de reconexión", "err", err)
		}
	}
	fondo.Add(1)
	go func() {
		defer fondo.Done()
		gestorTokens.Run(sinkCtx)
	}()

	chatBus := chat.NewBus()
	agregador := chat.NewAggregator(chat.Config{DB: db, Events: bus, Chat: chatBus, Registry: registro, Tokens: gestorTokens, Logger: logger})
	fondo.Add(1)
	go func() {
		defer fondo.Done()
		agregador.Run(sinkCtx)
	}()

	// Sin client_id —ni el propio (SPLITSTREAM_TWITCH_CLIENT_ID) ni el incluido en el
	// binario— conectar cuentas de Twitch no puede funcionar: se avisa una vez al
	// arrancar, no se falla, porque el resto del servicio (RTMP, otros destinos) sigue
	// sirviendo igual.
	if twitch.ResolveClientID(cfg.TwitchClientID) == "" {
		logger.Info("twitch sin client_id: conectar cuentas está desactivado hasta configurar SPLITSTREAM_TWITCH_CLIENT_ID")
	}

	hub := relay.NewHub(logger)
	engine := relay.NewEngine(relay.EngineConfig{
		Hub:         hub,
		Store:       storeAdapter{db: db},
		Logger:      logger,
		BaseContext: sinkCtx,
	})

	// La clave se compara descifrada y en tiempo constante no hace falta aquí: es un
	// servicio de un solo usuario y el rate limit vive en la API, no en RTMP.
	engine.SetValidator(func(app, key string) error {
		if app != settings.IngestApp {
			return rtmpio.ErrBadStreamKey
		}
		real, err := db.RevealIngestKey(ctx, cipher)
		if err != nil {
			return err
		}
		if key != real.Reveal() {
			return rtmpio.ErrBadStreamKey
		}
		return nil
	})

	// Los sinks se construyen por sesión, no al arrancar el proceso: cada sesión de
	// ingesta abre su propia conexión con cada destino (spec §6.5). Arrancarlos una sola
	// vez aquí hacía que la segunda transmisión reutilizara el timebase de la primera.
	factory := sinks.NewFactory(db, cipher, logger)
	factory.SetRecordingsDir(cfg.RecordingsDir)
	factory.SetRTMPPreCommands(cfg.RTMPPreCommands)
	// Antes de que el motor pueda abrir una sesión: las filas que quedaron «en curso» de
	// un arranque anterior (kill -9, corte de luz) se cierran con lo que diga el archivo,
	// o se borran si el archivo no está. Si no, esas filas no se pueden descargar ni
	// borrar, la poda las salta y la cuota las cuenta como 0 bytes. Un fallo aquí no
	// impide arrancar: se graba igual, solo que con la contabilidad vieja sucia.
	if cerradas, borradas, err := factory.ReconcileRecordings(ctx); err != nil {
		logger.Error("no se pudieron reconciliar las grabaciones del arranque anterior", "err", err)
	} else if cerradas+borradas > 0 {
		logger.Warn("grabaciones reconciliadas", "cerradas", cerradas, "borradas", borradas)
	}
	// Los destinos y, si está encendida, la grabación: un sink más de la misma sesión.
	// Un fallo construyendo la grabación no puede impedir la sesión: se registra y se
	// sigue sin grabar.
	engine.SetSinkProvider(func(sessionID int64) ([]*relay.Sink, error) {
		sinks, err := factory.BuildEnabled(ctx)
		if err != nil {
			return nil, err
		}
		rec, err := factory.BuildRecorder(ctx, sessionID)
		if err != nil {
			logger.Error("no se pudo construir la grabación", "err", err)
			return sinks, nil
		}
		if rec != nil {
			sinks = append(sinks, rec)
		}
		return sinks, nil
	})

	// Mantenimiento diario: poda de eventos y sesiones. Nunca con sesión viva.
	mant := &maintenance.Scheduler{
		Logger: logger,
		Busy:   func() bool { return engine.SessionID() != 0 },
		Jobs: []maintenance.Job{
			{Name: "eventos", Run: func(ctx context.Context) (string, error) {
				var corte time.Time
				if cfg.RetentionDays > 0 {
					corte = time.Now().Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour)
				}
				n, err := db.PruneEvents(ctx, corte, cfg.RetentionMaxEvents)
				return fmt.Sprintf("eventos: %d borrados", n), err
			}},
			{Name: "sesiones", Run: func(ctx context.Context) (string, error) {
				if cfg.RetentionDays == 0 {
					return "sesiones: retención desactivada", nil
				}
				n, err := db.PruneSessions(ctx, time.Now().Add(-time.Duration(cfg.RetentionDays)*24*time.Hour))
				return fmt.Sprintf("sesiones: %d borradas", n), err
			}},
			{Name: "grabaciones", Run: func(ctx context.Context) (string, error) {
				n, freed, err := factory.PruneRecordings(ctx)
				return fmt.Sprintf("grabaciones: %d borradas, %.1f MB liberados", n, float64(freed)/(1<<20)), err
			}},
			{Name: "chat", Run: func(ctx context.Context) (string, error) {
				n, err := db.PruneChat(ctx, cfg.RetentionMaxChat)
				return fmt.Sprintf("chat: %d mensajes borrados", n), err
			}},
			{Name: "cuota", Run: func(ctx context.Context) (string, error) {
				n, err := db.PruneQuota(ctx, cuota.DayBefore(7))
				return fmt.Sprintf("cuota: %d filas borradas", n), err
			}},
			{Name: "webhooks_kick", Run: func(ctx context.Context) (string, error) {
				return renovarWebhooksKick(ctx, db, registro, gestorTokens, publicURL, logger)
			}},
		},
		OnDone: func(resumen string, err error) {
			level := store.LevelInfo
			if err != nil {
				level = store.LevelWarn
			}
			if _, e := db.LogEvent(context.Background(), store.Event{
				Level: level, Kind: "maintenance_ran", Message: "mantenimiento: " + resumen,
			}); e != nil {
				logger.Error("no se pudo registrar el mantenimiento", "err", e)
			}
		},
	}
	fondo.Add(1)
	go func() {
		defer fondo.Done()
		mant.Run(sinkCtx)
	}()

	// Aviso de versión: una consulta a GitHub 30 s después de arrancar y luego cada 24 h.
	// Va en `fondo` para que el apagado la espere como a los webhooks; Run vuelve en
	// cuanto el contexto termina.
	var updateInfo func() httpapi.UpdateStatus
	if cfg.UpdateCheck {
		chk := &update.Checker{Current: version, Logger: logger}
		fondo.Add(1)
		go func() {
			defer fondo.Done()
			chk.Run(ctx, 30*time.Second, 24*time.Hour, func(i update.Info) {
				if _, err := db.LogEvent(context.Background(), store.Event{
					Level: store.LevelInfo, Kind: "update_available",
					Message: "Hay una versión nueva: " + i.Latest,
				}); err != nil {
					logger.Error("no se pudo registrar el aviso de versión", "err", err)
				}
			})
		}()
		updateInfo = func() httpapi.UpdateStatus { return httpapi.UpdateStatus(chk.Latest()) }
	}

	ingest := rtmpio.NewIngest(rtmpio.IngestConfig{
		Addr:    cfg.RTMPAddr,
		Handler: engine,
		Logger:  logger,
	})

	// Si esta ejecución acaba de crear la clave maestra, hay que decirlo AHORA y una sola
	// vez: es el único momento en que el usuario puede enterarse de que ese archivo
	// existe y de que respaldar solo la base no le sirve de nada.
	if cfg.MasterKeyAutogenerada {
		fmt.Fprint(out, avisoClaveCreada(cfg.MasterKeyPath))
	}

	// Primer arranque: si no hay contraseña, se genera un código de un solo uso y se
	// imprime bien visible. Solo hace falta para configurar desde OTRA máquina; desde el
	// propio equipo el asistente no lo pide, porque quien está en el teclado ya lo
	// controla.
	var setupCode string
	if settings.PasswordHash == "" {
		setupCode, err = httpapi.GenerateSetupCode()
		if err != nil {
			return err
		}
		fmt.Fprint(out, avisoPrimerArranque(cfg.HTTPAddr, setupCode))
	}

	panelFS, err := web.FS()
	if err != nil {
		// No es fatal: el binario puede servir solo la API. Se dice y se sigue.
		logger.Warn("el panel no está disponible en este binario", "err", err)
		panelFS = nil
	}

	api, err := httpapi.New(httpapi.Config{
		DB:             db,
		Cipher:         cipher,
		Engine:         engine,
		Ingest:         ingest,
		Sinks:          factory,
		Tester:         factory,
		Recorder:       factory,
		RecordingsDir:  cfg.RecordingsDir,
		Webhooks:       webhooks,
		MasterKey:      cfg.MasterKey,
		RTMPAddr:       cfg.RTMPAddr,
		Version:        version,
		SetupCode:      setupCode,
		SPA:            panelFS,
		Logger:         logger,
		SecureCookies:  cfg.SecureCookies,
		TrustedProxies: cfg.TrustedProxies,
		TLS:            cfg.TLS(),
		PublicURL:      publicURL,
		MetricsToken:   cfg.MetricsToken,
		UpdateInfo:     updateInfo,
		Platforms:      registro,
		Tokens:         gestorTokens,
		Chat:           chatBus,
		ChatStats:      agregador.Stats,
		Quota:          cuota,
		ChatIngest:     agregador.Ingest,
		ChatBudget:     cfg.YouTubeChatBudget,
		YouTubeQuota:   cfg.YouTubeQuota,
		// El padre de los sondeos de autorización en curso: el mismo contexto de vida
		// de los sinks, para que se corten en el apagado en vez de sobrevivir hasta que
		// venza el código de dispositivo (30 min).
		BaseContext: sinkCtx,
		ExtraMetrics: []httpapi.ExtraMetrics{func() []httpapi.Metric {
			ok, failed := webhooks.Stats()
			return []httpapi.Metric{
				{Name: "splitstream_events_bus_dropped_total", Type: "counter",
					Help: "Eventos que un consumidor lento no llegó a recibir.", Value: float64(bus.Dropped())},
				{Name: "splitstream_webhook_deliveries_total", Type: "counter", Help: "Entregas de webhooks por resultado.",
					Labels: map[string]string{"result": "ok"}, Value: float64(ok)},
				{Name: "splitstream_webhook_deliveries_total", Type: "counter", Help: "Entregas de webhooks por resultado.",
					Labels: map[string]string{"result": "failed"}, Value: float64(failed)},
			}
		}},
	})
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: api.Handler(),
		// Sin WriteTimeout: lo mataría el WebSocket, que por definición escribe durante
		// horas. El plazo de escritura del WS va por mensaje, dentro de su handler.
		ReadHeaderTimeout: 10 * time.Second,
	}
	var redirSrv *http.Server
	if tlsSetup != nil {
		httpSrv.TLSConfig = tlsSetup.TLSConfig
		if cfg.TLSRedirectAddr != "" {
			// Mínimo: atiende el reto HTTP-01 y redirige. Sin Handler propio no hay nada
			// más que pueda hacer, así que el plazo es corto.
			redirSrv = &http.Server{Addr: cfg.TLSRedirectAddr, Handler: tlsSetup.Redirect, ReadHeaderTimeout: 5 * time.Second}
		}
	}

	// El listener se abre AQUÍ y no dentro de la goroutine: si el bind falla, tiene que
	// morir el proceso. Con TLS integrado los puertos por defecto son :443 y :80, y el
	// fallo típico es "permission denied" por no tener CAP_NET_BIND_SERVICE; dentro de la
	// goroutine solo salía una línea de log y el proceso seguía vivo sin panel, así que
	// systemd lo veía sano y nadie lo reiniciaba.
	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("escuchar el panel en %s: %w", cfg.HTTPAddr, err)
	}
	// Serve cierra el listener al terminar; este Close de más devuelve un error que no
	// importa. Está para los caminos de error de más abajo, antes de arrancar el servidor.
	defer ln.Close()

	// Lo mismo con el de redirección: si alguien pide el :80 y no puede tenerlo, mejor
	// enterarse ahora. Quien no lo quiera, lo desactiva con `none`.
	var lnRedir net.Listener
	if redirSrv != nil {
		lnRedir, err = net.Listen("tcp", cfg.TLSRedirectAddr)
		if err != nil {
			return fmt.Errorf("escuchar la redirección a HTTPS en %s: %w", cfg.TLSRedirectAddr, err)
		}
		defer lnRedir.Close()
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if tlsSetup != nil {
			// Con TLSConfig puesto, los archivos vacíos son correctos: el certificado
			// sale de GetCertificate (autocert) o de Certificates (propio).
			err = httpSrv.ServeTLS(ln, "", "")
		} else {
			err = httpSrv.Serve(ln)
		}
		// ErrServerClosed es lo que devuelve SIEMPRE tras un Shutdown: no es un fallo.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("el servidor HTTP dejó de atender", "err", err)
		}
	}()
	if redirSrv != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := redirSrv.Serve(lnRedir); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("el listener de redirección a HTTPS dejó de atender", "err", err)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := ingest.ListenAndServe(); err != nil {
			// Al cerrar, Serve devuelve un error de listener cerrado: no es un fallo.
			logger.Info("la ingesta dejó de atender", "err", err)
		}
	}()

	// Sin `ingest_key`: el spec §8 dice que las claves jamás aparecen en los logs, sin
	// matices, y eso incluye la máscara con los últimos 4 caracteres, que es para la
	// interfaz —otra superficie, con otro control de acceso—. `ingest_app` no es secreto
	// y se queda.
	logArgs := []any{"config", cfg, "ingest_app", settings.IngestApp, "http_addr", cfg.HTTPAddr, "tls", cfg.TLS()}
	if publicURL != "" {
		logArgs = append(logArgs, "public_url", publicURL)
	}
	logger.Info("splitstream arrancado", logArgs...)

	<-ctx.Done()
	logger.Info("apagando")

	// El HTTP se cierra PRIMERO: así no puede entrar una petición que toque la base
	// mientras se está cerrando la sesión de ingesta.
	httpShutdown, cancelHTTP := context.WithTimeout(context.Background(), 5*time.Second)
	if err := httpSrv.Shutdown(httpShutdown); err != nil {
		logger.Warn("el servidor HTTP no cerró limpiamente", "err", err)
	}
	cancelHTTP()

	if redirSrv != nil {
		redirCtx, cancelRedir := context.WithTimeout(context.Background(), 2*time.Second)
		if err := redirSrv.Shutdown(redirCtx); err != nil {
			logger.Warn("el listener de redirección no cerró limpiamente", "err", err)
		}
		cancelRedir()
	}

	if err := ingest.Close(); err != nil {
		logger.Error("cerrar la ingesta", "err", err)
	}

	// Cerrar la ingesta corta los sockets, pero go-rtmp atiende cada conexión en su
	// propia goroutine y esa todavía tiene que disparar OnPublishEnd, que cierra la
	// sesión en la base y para los sinks. Sin esta espera el proceso puede salir antes,
	// dejando la sesión abierta para siempre.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := engine.WaitIdle(shutdownCtx); err != nil {
		logger.Warn("la sesión no llegó a cerrarse durante el apagado", "err", err)
	}

	// hub.Close() señala la parada a todos los destinos y espera con la gracia ÚNICA de
	// 3 s del spec §6.5, no una por destino. Agotada, se sigue adelante: cancelar el
	// contexto de los sinks es lo que acaba desatascando al que siga dentro de un Write.
	hub.Close()
	cancelSinks()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		logger.Warn("la ingesta no cerró en 3s; se sigue adelante")
	}

	// Y esto va lo ÚLTIMO, justo antes del return: al volver de run() corre el
	// `defer db.Close()`, y tanto el despachador como el mantenimiento escriben en la
	// base. WebhookDispatcher.Run espera a sus envíos en vuelo antes de volver, así que
	// verlo volver es lo que garantiza que ningún RecordWebhookDelivery —ni el del aviso
	// de apagado— llegue tarde, cuando la base ya no acepta escrituras.
	//
	// Diez segundos, que es el plazo por intento del despachador: un envío que se lanzó
	// justo antes de cancelar sobrevive a la cancelación y hay que dejarle terminar, o el
	// aviso de apagado —el que más le importa a quien opera esto— se pierde siempre. El
	// peor caso del cierre entero suma HTTP 5 s + redirección 2 s + WaitIdle 5 s + hub 3 s +
	// fondo 10 s + ingesta 3 s = 28 s, por debajo del TimeoutStopSec=30 de systemd.
	//
	// api.Wait() se suma a la MISMA espera y no a una nueva: espera a los sondeos de
	// autorización en curso (platforms.go), que cuelgan de BaseContext = sinkCtx, ya
	// cancelado arriba, así que terminan solos y rápido. Sumar un tope propio habría
	// dejado el apagado en 38 s, por encima de los 30 de systemd; comparten el mismo
	// presupuesto de 10 s porque ninguno de los dos escribe en la base tras su propio
	// Wait, así que da igual cuál termine primero.
	finFondo := make(chan struct{})
	go func() { fondo.Wait(); api.Wait(); close(finFondo) }()
	select {
	case <-finFondo:
	case <-time.After(10 * time.Second):
		logger.Warn("los avisos, el mantenimiento o los sondeos de autorización no terminaron en 10s; se sigue adelante")
	}
	return nil
}

// tokenGetter es lo mínimo que renovarWebhooksKick necesita del gestor de tokens: pedir
// uno vigente por cuenta. La interfaz existe para poder testear el job con un doble, sin
// levantar un tokens.Manager de verdad (que a su vez necesita el registro completo).
type tokenGetter interface {
	Token(ctx context.Context, accountID int64) (crypto.Secret, error)
}

// renovarWebhooksKick vuelve a suscribir el chat de Kick para cada cuenta conectada con un
// destino habilitado vinculado. Kick da de baja una suscripción tras un día entero de
// fallos de entrega, así que el mantenimiento diario la renueva sin esperar a que el
// panel provoque una reconexión: es best-effort, un fallo con una cuenta no impide seguir
// con las demás.
//
// publicURL vacía ya cubre tanto "sin TLS" como "con TLS pero con certificado propio sin
// dominio" (webtls.Build solo rellena PublicURL con Let's Encrypt): no hace falta un
// booleano aparte para TLS.
func renovarWebhooksKick(ctx context.Context, db *store.DB, registro *platforms.Registry,
	tg tokenGetter, publicURL string, logger *slog.Logger) (string, error) {
	if publicURL == "" {
		return "kick: sin suscripciones que renovar", nil
	}
	p, ok := registro.Get(platforms.Kick)
	if !ok {
		return "kick: sin suscripciones que renovar", nil
	}
	cw, ok := p.(platforms.ChatWebhook)
	if !ok {
		return "kick: sin suscripciones que renovar", nil
	}

	cuentas, err := db.Accounts(ctx)
	if err != nil {
		return "", err
	}
	dests, err := db.ListDestinations(ctx)
	if err != nil {
		return "", err
	}
	habilitadas := map[int64]bool{}
	for _, d := range dests {
		if d.Enabled && d.AccountID != nil {
			habilitadas[*d.AccountID] = true
		}
	}

	var candidatas []store.Account
	for _, acct := range cuentas {
		if acct.Platform == store.PlatformKick && acct.Status == store.AccountStatusOK && habilitadas[acct.ID] {
			candidatas = append(candidatas, acct)
		}
	}
	if len(candidatas) == 0 {
		return "kick: sin suscripciones que renovar", nil
	}

	url := strings.TrimSuffix(publicURL, "/") + "/api/platforms/kick/webhook"
	n := 0
	for _, acct := range candidatas {
		// Un timeout por cuenta, no uno solo para todo el job: una cuenta colgada no puede
		// hacer esperar a las demás ni al mantenimiento entero. cancel() se llama al final
		// de cada vuelta (no con defer, que las acumularía todas hasta que la función
		// entera vuelva).
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		tok, err := tg.Token(cctx, acct.ID)
		if err != nil {
			cancel()
			logger.Warn("kick: no se pudo obtener el token para renovar el webhook del chat", "cuenta", acct.DisplayName, "err", err)
			continue
		}
		err = cw.SubscribeChat(cctx, acct, tok, url)
		cancel()
		if err != nil {
			logger.Warn("kick: no se pudo renovar el webhook del chat", "cuenta", acct.DisplayName, "err", err)
			continue
		}
		n++
	}
	return fmt.Sprintf("kick: %d suscripciones renovadas", n), nil
}

// avisoClaveCreada explica el archivo de clave que se acaba de crear.
//
// Va a la salida estándar y no al logger por lo mismo que el aviso del primer arranque:
// esto hay que verlo ahora, no encontrarlo después en un journal.
func avisoClaveCreada(ruta string) string {
	return "\n" +
		"  Se ha creado tu clave maestra:\n" +
		"\n" +
		"      " + ruta + "\n" +
		"\n" +
		"  Cifra las claves de tus canales. RESPÁLDALA junto a la base de datos:\n" +
		"  si la pierdes, tendrás que volver a pegar la clave de cada plataforma.\n" +
		"\n" +
		"  Está al lado de la base a propósito, para que se copien juntas. Eso\n" +
		"  también significa que quien tenga acceso a esa carpeta lo tiene todo:\n" +
		"  en un servidor compartido, usa SPLITSTREAM_MASTER_KEY y guárdala aparte.\n" +
		"\n"
}

// avisoPrimerArranque es lo primero que ve alguien que acaba de instalar esto.
//
// Va a la salida estándar y no al logger a propósito: el logger escribe una línea por
// evento, pensada para leerla después en journalctl. Esto hay que verlo AHORA, y el código
// hay que poder copiarlo de un vistazo.
func avisoPrimerArranque(httpAddr, codigo string) string {
	url := httpAddr
	if strings.HasPrefix(url, ":") {
		url = "localhost" + url
	}
	return "\n" +
		"  ┌───────────────────────────────────────────────────────────┐\n" +
		"  │  Splitstream todavía no está configurado                  │\n" +
		"  └───────────────────────────────────────────────────────────┘\n" +
		"\n" +
		"  Abre el panel y elige tu contraseña:\n" +
		"\n" +
		"      http://" + url + "\n" +
		"\n" +
		"  Si lo abres desde ESTA misma máquina, no hace falta nada más.\n" +
		"\n" +
		"  Si lo abres desde otro equipo —un servidor, el móvil—, el panel\n" +
		"  te pedirá este código:\n" +
		"\n" +
		"      " + codigo + "\n" +
		"\n" +
		"  Sirve una sola vez y cambia en cada arranque. Existe para que\n" +
		"  nadie que llegue antes que tú se quede con el servicio.\n" +
		"\n"
}

// storeAdapter traduce el store al contrato EngineStore, para que internal/relay no
// tenga que importar internal/store.
type storeAdapter struct{ db *store.DB }

func (a storeAdapter) StartSession(ctx context.Context) (int64, error) {
	return a.db.StartSession(ctx)
}

func (a storeAdapter) FinishSession(ctx context.Context, id int64, w, h, b int) error {
	return a.db.FinishSession(ctx, id, w, h, b)
}

func (a storeAdapter) LogEvent(ctx context.Context, e relay.EngineEvent) error {
	_, err := a.db.LogEvent(ctx, store.Event{
		SessionID:     e.SessionID,
		DestinationID: e.DestinationID,
		Level:         store.Level(e.Level),
		Kind:          e.Kind,
		Message:       e.Message,
	})
	return err
}

// healthcheckURL apunta siempre a la propia máquina: el addr de escucha puede ser ":8080"
// o "0.0.0.0:8080", que no son direcciones a las que conectar. Con TLS integrado el
// esquema y el puerto por defecto cambian a https y 443.
func healthcheckURL(addr string, conTLS bool) string {
	esquema, puertoDef := "http", "8080"
	if conTLS {
		esquema, puertoDef = "https", "443"
	}
	_, puerto, err := net.SplitHostPort(addr)
	if err != nil || puerto == "" {
		puerto = puertoDef
	}
	return esquema + "://127.0.0.1:" + puerto + "/healthz"
}

func healthcheck(addr string, conTLS bool, dominio string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	if conTLS {
		// InsecureSkipVerify: es loopback y solo mide vida; el certificado es del dominio,
		// nunca de 127.0.0.1, así que validarlo fallaría siempre.
		//
		// ServerName: la URL apunta a un literal IP y un literal IP no manda SNI. Sin SNI,
		// el GetCertificate de autocert rechaza el saludo ("missing server name") y el
		// healthcheck fallaba SIEMPRE con dominio configurado —el contenedor entero se
		// declaraba unhealthy. Con certificado propio `dominio` viene vacío y no se manda
		// SNI, que es justo lo que quiere ese camino: se sirve Certificates[0] igual.
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         dominio,
		}}
	}
	resp, err := client.Get(healthcheckURL(addr, conTLS))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/healthz respondió %d", resp.StatusCode)
	}
	return nil
}

// backup copia la base con VACUUM INTO. Abre la base como el servicio —con sus
// migraciones— para que el respaldo esté en la versión actual del esquema.
func backup(ctx context.Context, destino string, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.BackupTo(ctx, destino); err != nil {
		return err
	}
	fmt.Fprintf(out, "respaldo escrito en %s\nRecuerda: sin la clave maestra es ilegible.\n", destino)
	return nil
}
