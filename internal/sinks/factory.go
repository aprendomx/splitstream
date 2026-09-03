// Package sinks construye los sinks de retransmisión a partir de lo que hay en la base de
// datos.
//
// Existe como paquete propio porque lo necesitan dos sitios: el motor, que arma los sinks
// de cada sesión de ingesta, y la API, que aplica en caliente el alta o la edición de un
// destino mientras se transmite. La alternativa —un closure en main.go y una copia en
// httpapi— garantizaba que las dos versiones divergieran.
//
// Es la capa de composición: importa store, crypto, rtmpio y relay. Por eso no puede vivir
// dentro de relay, que no debe conocer ni la base de datos ni la librería RTMP.
package sinks

import (
	"context"
	"log/slog"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/store"
)

// Factory construye sinks. Es seguro compartirlo: no guarda estado propio.
type Factory struct {
	db     *store.DB
	cipher *crypto.Cipher
	logger *slog.Logger
}

func NewFactory(db *store.DB, c *crypto.Cipher, logger *slog.Logger) *Factory {
	if logger == nil {
		logger = slog.Default()
	}
	return &Factory{db: db, cipher: c, logger: logger}
}

// Build construye el sink de un destino.
//
// Usa DestinationKeyForRelay y no RevealDestinationKey: leer la clave para armar un sink no
// es divulgarla a una persona, y auditarlo escribiría un evento por destino en cada
// arranque de transmisión (spec §15.5).
//
// Valida la configuración construyendo un publisher de prueba antes de devolver nada: sin
// eso se crearía un sink que no puede conectar jamás y que se pasaría la vida reintentando
// contra una URL imposible.
func (f *Factory) Build(ctx context.Context, d store.Destination) (*relay.Sink, error) {
	key, err := f.db.DestinationKeyForRelay(ctx, f.cipher, d.ID)
	if err != nil {
		return nil, err
	}

	url, name, id := d.RTMPURL, d.Name, d.ID
	if _, err := rtmpio.NewPublisher(rtmpio.PublisherConfig{
		URL: url, StreamKey: key, Logger: f.logger,
	}); err != nil {
		return nil, err
	}

	return relay.NewSink(relay.SinkConfig{
		ID:   id,
		Name: name,
		// Cada reconexión necesita un publisher nuevo: uno cerrado no se reabre.
		NewPub: func() (relay.Publisher, error) {
			return rtmpio.NewPublisher(rtmpio.PublisherConfig{
				URL: url, StreamKey: key, Logger: f.logger,
			})
		},
		Logger: f.logger,
		OnEvent: func(ev relay.EngineEvent) {
			if _, err := f.db.LogEvent(ctx, store.Event{
				DestinationID: ev.DestinationID,
				Level:         store.Level(ev.Level),
				Kind:          ev.Kind,
				Message:       ev.Message,
			}); err != nil {
				f.logger.Error("no se pudo registrar el evento del destino", "err", err)
			}
		},
	}), nil
}

// BuildEnabled construye los sinks de todos los destinos habilitados.
//
// Un destino roto se registra y se salta, no aborta la lista: con la política contraria una
// URL mal pegada en un destino dejaría al usuario sin ninguna transmisión y sin entender
// por qué.
func (f *Factory) BuildEnabled(ctx context.Context) ([]*relay.Sink, error) {
	dests, err := f.db.ListDestinations(ctx)
	if err != nil {
		return nil, err
	}

	var out []*relay.Sink
	for _, d := range dests {
		if !d.Enabled {
			continue
		}
		s, err := f.Build(ctx, d)
		if err != nil {
			f.logger.Error("destino mal configurado", "destino", d.Name, "err", err)
			continue
		}
		out = append(out, s)
	}
	f.logger.Info("destinos de la sesión", "n", len(out))
	return out, nil
}
