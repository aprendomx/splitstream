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
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/probe"
	"github.com/aprendomx/splitstream/internal/record"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/store"
)

// Factory construye sinks. Es seguro compartirlo: no guarda estado propio, salvo el
// directorio de grabaciones, que se fija una sola vez al arrancar.
type Factory struct {
	db     *store.DB
	cipher *crypto.Cipher
	logger *slog.Logger
	recDir string
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
		// El contexto de los eventos NO es el de quien llamó a Build.
		//
		// Los eventos del sink —conectar, caerse, reconectar— ocurren mucho después, a lo
		// largo de toda la transmisión. Con el contexto del llamante, un destino añadido
		// desde un handler HTTP dejaba de registrar en cuanto la petición respondía: se
		// veía como "context canceled" en el log y como un destino sin ningún evento en el
		// panel. Se descubrió añadiendo Facebook a un directo en curso.
		//
		// context.Background() y no el del proceso: escribir un evento es una operación
		// corta, y durante el apagado interesa que los últimos eventos LLEGUEN a la base en
		// vez de cancelarse a medias.
		OnEvent: func(ev relay.EngineEvent) {
			if _, err := f.db.LogEvent(context.Background(), store.Event{
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

// probeGrace es lo que se espera tras publish antes de dar la configuración por
// plausible. Twitch corta una clave mala en menos de un segundo; tres da margen a
// plataformas más lentas sin que el botón parezca colgado.
const probeGrace = 3 * time.Second

// Test sondea un destino sin emitir (spec v0.8 §3). Como Build, lee la clave con
// DestinationKeyForRelay: no es una divulgación y no se audita como tal.
func (f *Factory) Test(ctx context.Context, d store.Destination) (probe.Result, error) {
	key, err := f.db.DestinationKeyForRelay(ctx, f.cipher, d.ID)
	if err != nil {
		return probe.Result{}, err
	}
	return rtmpio.Probe(ctx, rtmpio.PublisherConfig{
		URL: d.RTMPURL, StreamKey: key, Logger: f.logger,
	}, probeGrace), nil
}

// SetRecordingsDir fija el directorio raíz de las grabaciones. Sin él, BuildRecorder no
// construye nada: grabar sin saber dónde no es una opción.
func (f *Factory) SetRecordingsDir(dir string) { f.recDir = dir }

// logEvent deja un evento del sistema (sin sesión ni destino) con context.Background():
// son escrituras cortas que interesa que lleguen aunque quien llamó ya se haya ido.
func (f *Factory) logEvent(level store.Level, kind, msg string) {
	if _, err := f.db.LogEvent(context.Background(), store.Event{Level: level, Kind: kind, Message: msg}); err != nil {
		f.logger.Error("no se pudo registrar el evento de grabación", "kind", kind, "err", err)
	}
}

// recordingQuota compone la cuota con lo que ya ocupan las grabaciones.
func (f *Factory) recordingQuota(ctx context.Context, st *store.RecordingSettings) (record.Quota, error) {
	used, err := f.db.RecordingsTotalBytes(ctx)
	if err != nil {
		return record.Quota{}, err
	}
	return record.Quota{MaxBytes: st.MaxBytes(), UsedBytes: used}, nil
}

// ReconcileRecordings cierra o borra las filas que quedaron en curso de un arranque
// anterior. Se llama UNA vez al arrancar, antes de que el motor pueda abrir una sesión:
// después habría filas en curso legítimas y se cerrarían por la espalda.
//
// El archivo manda: si está, la fila se cierra con su tamaño y su fecha; si no, se borra.
func (f *Factory) ReconcileRecordings(ctx context.Context) (closed, removed int, err error) {
	if f.recDir == "" {
		return 0, 0, nil
	}
	stat := func(rel string) (int64, time.Time, bool) {
		info, err := os.Stat(filepath.Join(f.recDir, filepath.FromSlash(rel)))
		if err != nil {
			return 0, time.Time{}, false
		}
		return info.Size(), info.ModTime(), true
	}
	closed, removed, err = f.db.CloseDanglingRecordings(ctx, stat)
	if closed+removed > 0 {
		f.logEvent(store.LevelWarn, "recording_reconciled", fmt.Sprintf(
			"grabación: %d segmentos cerrados y %d filas sin archivo borradas tras un arranque sin cierre limpio",
			closed, removed))
	}
	return closed, removed, err
}

// relPath vuelve un path absoluto de una grabación relativo al directorio raíz, que es
// como se guarda: mover la carpeta entera no rompe el listado.
func (f *Factory) relPath(abs string) string {
	rel, err := filepath.Rel(f.recDir, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// PruneRecordings aplica la retención de grabaciones: por días y por gigas, la de gigas
// manda. Lo usa el job diario y BuildRecorder cuando no hay sitio.
func (f *Factory) PruneRecordings(ctx context.Context) (deleted int, freed int64, err error) {
	st, err := f.db.RecordingSettings(ctx)
	if err != nil {
		return 0, 0, err
	}
	var corte time.Time
	if st.KeepDays > 0 {
		corte = time.Now().Add(-time.Duration(st.KeepDays) * 24 * time.Hour)
	}
	remove := func(rel string) error {
		return os.Remove(filepath.Join(f.recDir, filepath.FromSlash(rel)))
	}
	return f.db.PruneRecordings(ctx, corte, st.MaxBytes(), remove)
}

// BuildRecorder construye el sink de grabación de una sesión (spec v0.9 §6): nil, nil si
// la grabación está apagada o no hay sitio. Sin sitio se intenta podar antes de rendirse,
// porque el planificador puede no haber corrido todavía hoy.
func (f *Factory) BuildRecorder(ctx context.Context, sessionID int64) (*relay.Sink, error) {
	if f.recDir == "" {
		return nil, nil
	}
	st, err := f.db.RecordingSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !st.Enabled {
		return nil, nil
	}
	if err := os.MkdirAll(f.recDir, 0o700); err != nil {
		return nil, fmt.Errorf("crear el directorio de grabaciones: %w", err)
	}

	q, err := f.recordingQuota(ctx, st)
	if err != nil {
		return nil, err
	}
	if _, err := q.Check(f.recDir, 0); err != nil {
		if _, _, perr := f.PruneRecordings(ctx); perr != nil {
			f.logger.Warn("no se pudo podar antes de grabar", "err", perr)
		}
		if q, err = f.recordingQuota(ctx, st); err != nil {
			return nil, err
		}
		if _, err = q.Check(f.recDir, 0); err != nil {
			kind := "recording_skipped_disk"
			if q.MaxBytes > 0 && q.UsedBytes >= q.MaxBytes {
				kind = "recording_skipped_quota"
			}
			f.logEvent(store.LevelWarn, kind, "grabación: no arranca, "+err.Error())
			return nil, nil
		}
	}

	dir := filepath.Join(f.recDir, fmt.Sprintf("sesion-%d", sessionID))
	bg := context.Background()
	// Una vez por sesión, los dos: el sink reintenta con backoff y cada intento
	// construye un writer NUEVO, que trae su propio «ya avisé». Sin esto, el aviso del
	// 80 % se repetía una vez por reconexión y llenaba el registro de eventos.
	var discoLlenoAvisado, avisoDiscoDado bool

	newPub := func() (relay.Publisher, error) {
		q, err := f.recordingQuota(bg, st)
		if err != nil {
			return nil, err
		}
		// La numeración sigue donde la dejó la sesión: un apagado y encendido en caliente
		// crea otro writer, y volver a empezar en 1 daba dos «segmento 1» y podía chocar
		// con el archivo anterior si los dos caían en el mismo segundo (O_EXCL).
		n, err := f.db.CountSessionRecordings(bg, sessionID)
		if err != nil {
			f.logger.Warn("no se pudieron contar los segmentos de la sesión", "err", err)
			n = 0
		}
		return record.NewFLVWriter(record.Options{
			Dir: dir, SessionID: sessionID, SegmentMinutes: st.SegmentMin, Quota: q,
			FirstIndex: n + 1, Logger: f.logger,
			OnOpen: func(path string, index int, startedAt time.Time) {
				if _, err := f.db.OpenRecording(bg, sessionID, f.relPath(path), index, startedAt); err != nil {
					f.logger.Error("no se pudo registrar el segmento", "err", err)
				}
			},
			OnSegment: func(s record.Segment) {
				if err := f.db.FinishRecording(bg, f.relPath(s.Path), s.EndedAt, s.Bytes, int(s.DurationMS)); err != nil {
					f.logger.Error("no se pudo cerrar el segmento", "err", err)
				}
				f.logEvent(store.LevelInfo, "recording_segment", fmt.Sprintf(
					"grabación: segmento %d cerrado, %.1f MB y %s", s.Index,
					float64(s.Bytes)/(1<<20), (time.Duration(s.DurationMS)*time.Millisecond).Round(time.Second)))
			},
			OnDiskWarning: func(used, max int64) {
				if avisoDiscoDado {
					return
				}
				avisoDiscoDado = true
				f.logEvent(store.LevelWarn, "recording_disk_warning", fmt.Sprintf(
					"grabación: las grabaciones ocupan el %d %% del tope; se borrarán las más antiguas al llegar", used*100/max))
			},
		}), nil
	}

	return relay.NewSink(relay.SinkConfig{
		ID: relay.RecorderSinkID, Name: "grabación", NewPub: newPub, Logger: f.logger,
		// Los eventos del sink se traducen: sin destination_id (-1 violaría la clave
		// ajena) y con kind recording_*. Los de sospecha y aleteo no se pasan: para un
		// archivo no significan nada y solo harían ruido.
		OnEvent: func(ev relay.EngineEvent) {
			level := store.Level(ev.Level)
			var kind string
			switch ev.Kind {
			case "destination_connected":
				kind, ev.Message = "recording_started", "empezó a escribir"
			case "destination_disconnected":
				kind = "recording_stopped"
				if strings.Contains(ev.Message, record.ErrDiskFull.Error()) {
					if discoLlenoAvisado {
						return
					}
					discoLlenoAvisado = true
					kind, level = "recording_stopped_disk_full", store.LevelError
				}
			case "destination_suspended":
				kind = "recording_suspended"
			default:
				return
			}
			f.logEvent(level, kind, "grabación: "+ev.Message)
		},
	}), nil
}
