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
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aprendomx/splitstream/internal/config"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/httpapi"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/sinks"
	"github.com/aprendomx/splitstream/internal/store"
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
	flag.Parse()

	if *showVersion {
		printVersion(os.Stdout)
		return
	}

	if *setpw {
		if err := setPassword(context.Background(), os.Stdin, os.Stdout); err != nil {
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

	// Los sinks NO heredan el contexto de señales: si lo hicieran, un SIGTERM los mataría
	// antes de que el cierre ordenado del spec §6.5 pudiera mandar su FCUnpublish. Este
	// contexto se cancela al final, tras la espera.
	sinkCtx, cancelSinks := context.WithCancel(context.Background())
	defer cancelSinks()

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
	engine.SetSinkProvider(func() ([]*relay.Sink, error) {
		return factory.BuildEnabled(ctx)
	})

	ingest := rtmpio.NewIngest(rtmpio.IngestConfig{
		Addr:    cfg.RTMPAddr,
		Handler: engine,
		Logger:  logger,
	})

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
		DB:            db,
		Cipher:        cipher,
		Engine:        engine,
		Ingest:        ingest,
		Sinks:         factory,
		MasterKey:     cfg.MasterKey,
		RTMPAddr:      cfg.RTMPAddr,
		Version:       version,
		SetupCode:     setupCode,
		SPA:           panelFS,
		Logger:        logger,
		SecureCookies: cfg.SecureCookies,
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

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// ErrServerClosed es lo que devuelve SIEMPRE tras un Shutdown: no es un fallo.
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("el servidor HTTP dejó de atender", "err", err)
		}
	}()

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
	logger.Info("splitstream arrancado", "config", cfg,
		"ingest_app", settings.IngestApp, "http_addr", cfg.HTTPAddr)

	<-ctx.Done()
	logger.Info("apagando")

	// El HTTP se cierra PRIMERO: así no puede entrar una petición que toque la base
	// mientras se está cerrando la sesión de ingesta.
	httpShutdown, cancelHTTP := context.WithTimeout(context.Background(), 5*time.Second)
	if err := httpSrv.Shutdown(httpShutdown); err != nil {
		logger.Warn("el servidor HTTP no cerró limpiamente", "err", err)
	}
	cancelHTTP()

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
	return nil
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
