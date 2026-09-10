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
	"github.com/aprendomx/splitstream/internal/config"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/events"
	"github.com/aprendomx/splitstream/internal/httpapi"
	"github.com/aprendomx/splitstream/internal/maintenance"
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
		conTLS := os.Getenv("SPLITSTREAM_TLS_DOMAIN") != "" || os.Getenv("SPLITSTREAM_TLS_CERT_FILE") != ""
		if err := healthcheck(addr, conTLS); err != nil {
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

	// El TLS integrado se construye ANTES de la Config de httpapi: PublicURL sale de aquí
	// y ese paquete no sabe nada de webtls (la CI lo comprueba).
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

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if tlsSetup != nil {
			// Con TLSConfig puesto, los archivos vacíos son correctos: el certificado
			// sale de GetCertificate (autocert) o de Certificates (propio).
			err = httpSrv.ListenAndServeTLS("", "")
		} else {
			err = httpSrv.ListenAndServe()
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
			if err := redirSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	finFondo := make(chan struct{})
	go func() { fondo.Wait(); close(finFondo) }()
	select {
	case <-finFondo:
	case <-time.After(10 * time.Second):
		logger.Warn("los avisos y el mantenimiento no terminaron en 10s; se sigue adelante")
	}
	return nil
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

func healthcheck(addr string, conTLS bool) error {
	client := &http.Client{Timeout: 3 * time.Second}
	if conTLS {
		// Es loopback y solo mide vida: el certificado es del dominio, nunca de
		// 127.0.0.1, así que validarlo fallaría siempre.
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
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
