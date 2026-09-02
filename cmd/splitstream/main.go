// Command splitstream es el servicio de retransmisión RTMP.
//
// Fase 1: arranca, valida la configuración y la master key, migra la base de datos
// y espera a SIGTERM. Todavía no hay servidor RTMP ni API HTTP.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aprendomx/splitstream/internal/config"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/store"
)

func main() {
	genkey := flag.Bool("genkey", false, "imprime una SPLITSTREAM_MASTER_KEY nueva y sale")
	flag.Parse()

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
	engine.SetSinkProvider(func() ([]*relay.Sink, error) {
		dests, err := db.ListDestinations(ctx)
		if err != nil {
			return nil, err
		}

		var out []*relay.Sink
		for _, d := range dests {
			if !d.Enabled {
				continue
			}
			key, err := db.RevealDestinationKey(ctx, cipher, d.ID)
			if err != nil {
				logger.Error("no se pudo leer la clave del destino", "destino", d.Name, "err", err)
				continue
			}
			// Validar aquí evita crear un sink que no podría conectar nunca.
			if _, err := rtmpio.NewPublisher(rtmpio.PublisherConfig{
				URL: d.RTMPURL, StreamKey: key, Logger: logger,
			}); err != nil {
				logger.Error("destino mal configurado", "destino", d.Name, "err", err)
				continue
			}

			url, name, id := d.RTMPURL, d.Name, d.ID
			out = append(out, relay.NewSink(relay.SinkConfig{
				ID:   id,
				Name: name,
				// Cada reconexión necesita un publisher nuevo: uno cerrado no se reabre.
				NewPub: func() (relay.Publisher, error) {
					return rtmpio.NewPublisher(rtmpio.PublisherConfig{
						URL: url, StreamKey: key, Logger: logger,
					})
				},
				Logger: logger,
				OnEvent: func(ev relay.EngineEvent) {
					if _, err := db.LogEvent(ctx, store.Event{
						DestinationID: ev.DestinationID,
						Level:         store.Level(ev.Level),
						Kind:          ev.Kind,
						Message:       ev.Message,
					}); err != nil {
						logger.Error("no se pudo registrar el evento del destino", "err", err)
					}
				},
			}))
		}
		logger.Info("destinos de la sesión", "n", len(out))
		return out, nil
	})

	ingest := rtmpio.NewIngest(rtmpio.IngestConfig{
		Addr:    cfg.RTMPAddr,
		Handler: engine,
		Logger:  logger,
	})

	var wg sync.WaitGroup
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
	logger.Info("splitstream arrancado", "config", cfg, "ingest_app", settings.IngestApp)

	<-ctx.Done()
	logger.Info("apagando")

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
