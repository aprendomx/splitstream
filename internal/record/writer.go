package record

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/aprendomx/splitstream/internal/flv"
	"github.com/aprendomx/splitstream/internal/relay"
)

const (
	// bufSize es el buffer de escritura. Un MiB son unos segundos de media a bitrates
	// normales: el disco recibe escrituras grandes y pocas.
	bufSize = 1 << 20
	// flushEveryMS es cada cuánto tiempo de media se vacía el buffer. Acota lo que un
	// kill -9 puede costar dentro del segmento en curso.
	flushEveryMS = 2000
)

// Segment describe un archivo cerrado.
type Segment struct {
	Path       string
	Index      int
	StartedAt  time.Time
	EndedAt    time.Time
	Bytes      int64
	DurationMS uint32
}

// Options son los datos para construir un FLVWriter.
type Options struct {
	Dir            string
	SessionID      int64
	SegmentMinutes int
	Quota          Quota
	OnOpen         func(path string, index int, startedAt time.Time)
	OnSegment      func(Segment)
	OnDiskWarning  func(used, max int64)
	Now            func() time.Time
	Logger         *slog.Logger
}

// FLVWriter es el Publisher de la grabación: escribe lo que el sink le manda en archivos
// FLV segmentados. Como todo Publisher, se usa desde una sola goroutine (la del sink), así
// que no lleva mutex. La regla innegociable del spec v0.9 §1 se cumple por construcción:
// este código solo corre en la goroutine del sink de grabación, y el hub le entrega
// mensajes sin bloquear.
type FLVWriter struct {
	o     Options
	now   func() time.Time
	log   *slog.Logger
	stamp string

	f           *os.File
	buf         *bufio.Writer
	path        string
	index       int
	segStart    time.Time
	segBytes    int64
	total       int64
	base        uint32
	lastTS      uint32
	lastFlushTS uint32

	meta, videoSeq, audioSeq []byte
	warned                   bool
	closed                   bool
}

func NewFLVWriter(o Options) *FLVWriter {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	return &FLVWriter{o: o, now: now, log: log.With("sesion_id", o.SessionID)}
}

var _ relay.Publisher = (*FLVWriter)(nil)

// Connect crea el directorio, comprueba la cuota y abre el primer segmento.
func (w *FLVWriter) Connect(ctx context.Context) error {
	if w.closed {
		return errors.New("el escritor está cerrado")
	}
	if err := os.MkdirAll(w.o.Dir, 0o700); err != nil {
		return fmt.Errorf("crear el directorio de grabación: %w", err)
	}
	if err := w.checkQuota(); err != nil {
		return err
	}
	w.stamp = w.now().Format("20060102-150405")
	return w.openSegment()
}

// checkQuota aplica la cuota con lo que este writer lleva escrito, y avisa una sola vez
// al cruzar el umbral.
func (w *FLVWriter) checkQuota() error {
	warn, err := w.o.Quota.Check(w.o.Dir, w.total)
	if err != nil {
		return err
	}
	if warn && !w.warned {
		w.warned = true
		if w.o.OnDiskWarning != nil {
			w.o.OnDiskWarning(w.o.Quota.UsedBytes+w.total, w.o.Quota.MaxBytes)
		}
	}
	return nil
}

func (w *FLVWriter) openSegment() error {
	w.index++
	w.path = filepath.Join(w.o.Dir, fmt.Sprintf("%s-%02d.flv", w.stamp, w.index))
	// O_EXCL: dos sesiones en el mismo segundo no pueden pisarse un archivo.
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("abrir el segmento: %w", err)
	}
	w.f = f
	w.buf = bufio.NewWriterSize(f, bufSize)
	w.segStart = w.now()
	w.segBytes = 0
	if err := WriteHeader(w.buf); err != nil {
		return err
	}
	w.segBytes += int64(len(flvHeader))
	w.total += int64(len(flvHeader))
	if w.o.OnOpen != nil {
		w.o.OnOpen(w.path, w.index, w.segStart)
	}
	return nil
}

// closeSegment vacía, sincroniza y cierra el archivo en curso, y avisa por OnSegment. Es
// el único sitio con Sync: una vez por segmento, nunca por frame.
func (w *FLVWriter) closeSegment() error {
	if w.f == nil {
		return nil
	}
	err := w.buf.Flush()
	if serr := w.f.Sync(); err == nil {
		err = serr
	}
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	seg := Segment{
		Path: w.path, Index: w.index, StartedAt: w.segStart, EndedAt: w.now(),
		Bytes: w.segBytes, DurationMS: w.lastTS - w.base,
	}
	w.f, w.buf = nil, nil
	if w.o.OnSegment != nil {
		w.o.OnSegment(seg)
	}
	return err
}

// rotate cierra el segmento en curso y abre el siguiente con base en el keyframe ts.
func (w *FLVWriter) rotate(ts uint32) error {
	if err := w.closeSegment(); err != nil {
		return err
	}
	if err := w.checkQuota(); err != nil {
		return err
	}
	if err := w.openSegment(); err != nil {
		return err
	}
	w.base = ts
	w.lastTS = ts
	w.lastFlushTS = ts
	return w.writePreamble()
}

// writePreamble reescribe lo que el sink mandó al conectar, con ts=0: cada segmento tiene
// que poder reproducirse solo.
func (w *FLVWriter) writePreamble() error {
	if w.meta != nil {
		if err := w.writeTag(TagScript, 0, w.meta); err != nil {
			return err
		}
	}
	if w.videoSeq != nil {
		if err := w.writeTag(TagVideo, 0, w.videoSeq); err != nil {
			return err
		}
	}
	if w.audioSeq != nil {
		if err := w.writeTag(TagAudio, 0, w.audioSeq); err != nil {
			return err
		}
	}
	return nil
}

// writeTag escribe un tag con timestamp YA relativo al segmento y lleva las cuentas.
func (w *FLVWriter) writeTag(typ byte, rel uint32, data []byte) error {
	if w.closed {
		return errors.New("el escritor está cerrado")
	}
	if w.f == nil {
		return errors.New("el escritor no está conectado")
	}
	if err := WriteTag(w.buf, typ, rel, data); err != nil {
		return fmt.Errorf("escribir en la grabación: %w", err)
	}
	n := int64(11 + len(data) + 4)
	w.segBytes += n
	w.total += n
	return nil
}

// media escribe un tag de audio o vídeo con timestamp del sink: lo rebasa al segmento,
// descarta lo anterior a la base y vacía el buffer cada flushEveryMS de media.
func (w *FLVWriter) media(typ byte, ts uint32, data []byte) error {
	if ts < w.base {
		return nil
	}
	if err := w.writeTag(typ, ts-w.base, data); err != nil {
		return err
	}
	if ts > w.lastTS {
		w.lastTS = ts
	}
	if ts-w.lastFlushTS >= flushEveryMS {
		w.lastFlushTS = ts
		if err := w.buf.Flush(); err != nil {
			return fmt.Errorf("escribir en la grabación: %w", err)
		}
	}
	return nil
}

func clonar(b []byte) []byte { return append([]byte(nil), b...) }

// WriteMeta escribe el onMetaData como script tag y lo cachea para los segmentos
// siguientes. El payload que circula por el hub ya es el cuerpo AMF0 completo.
func (w *FLVWriter) WriteMeta(ts uint32, payload []byte) error {
	w.meta = clonar(payload)
	return w.writeTag(TagScript, 0, payload)
}

// WriteVideo escribe un tag de vídeo. Los sequence headers se cachean; un keyframe que
// cruza el intervalo de segmentación rota el archivo antes de escribirse.
func (w *FLVWriter) WriteVideo(ts uint32, payload []byte) error {
	info, err := flv.InspectVideo(payload)
	if err != nil {
		return err
	}
	if info.IsSequenceHeader {
		w.videoSeq = clonar(payload)
		return w.writeTag(TagVideo, 0, payload)
	}
	if info.IsKeyframe && w.o.SegmentMinutes > 0 && w.f != nil &&
		ts-w.base >= uint32(w.o.SegmentMinutes)*60_000 {
		if err := w.rotate(ts); err != nil {
			return err
		}
	}
	return w.media(TagVideo, ts, payload)
}

// WriteAudio escribe un tag de audio; el sequence header se cachea.
func (w *FLVWriter) WriteAudio(ts uint32, payload []byte) error {
	info, err := flv.InspectAudio(payload)
	if err != nil {
		return err
	}
	if info.IsSequenceHeader {
		w.audioSeq = clonar(payload)
		return w.writeTag(TagAudio, 0, payload)
	}
	return w.media(TagAudio, ts, payload)
}

// Close cierra el segmento en curso. Es idempotente y tolera que Connect no se haya
// llamado o haya fallado.
func (w *FLVWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.closeSegment()
}
