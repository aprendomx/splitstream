package record

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Payloads FLV mínimos: el primer byte lleva frameType|codecID (0x17 keyframe AVC, 0x27
// inter AVC), el segundo el AVCPacketType (0 seq header, 1 NALU). Audio: 0xAF = AAC,
// segundo byte 0 seq header, 1 raw.
var (
	pMeta     = []byte{0x02, 0x00, 0x0a, 'o', 'n', 'M', 'e', 't', 'a', 'D', 'a', 't', 'a', 0x08, 0, 0, 0, 0, 0, 0, 0x09}
	pVideoSeq = []byte{0x17, 0x00, 0, 0, 0, 0x01, 0x64, 0x00, 0x1f}
	pAudioSeq = []byte{0xAF, 0x00, 0x12, 0x10}
	pKey      = []byte{0x17, 0x01, 0, 0, 0, 0xaa}
	pInter    = []byte{0x27, 0x01, 0, 0, 0, 0xbb}
	pAudio    = []byte{0xAF, 0x01, 0xcc}
)

// segmentos captura las llamadas a OnOpen y OnSegment.
type segmentos struct {
	abiertos []string
	cerrados []Segment
}

func nuevoWriter(t *testing.T, o Options) (*FLVWriter, *segmentos) {
	t.Helper()
	segs := &segmentos{}
	if o.Dir == "" {
		o.Dir = filepath.Join(t.TempDir(), "sesion-1")
	}
	o.OnOpen = func(path string, _ int, _ time.Time) { segs.abiertos = append(segs.abiertos, path) }
	o.OnSegment = func(s Segment) { segs.cerrados = append(segs.cerrados, s) }
	return NewFLVWriter(o), segs
}

// preambulo manda lo que el sink manda al conectar: meta, seq headers y el keyframe de
// arranque, todo con ts=0 (spec base §6.3).
func preambulo(t *testing.T, w *FLVWriter) {
	t.Helper()
	for _, f := range []func() error{
		func() error { return w.WriteMeta(0, pMeta) },
		func() error { return w.WriteVideo(0, pVideoSeq) },
		func() error { return w.WriteAudio(0, pAudioSeq) },
		func() error { return w.WriteVideo(0, pKey) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
}

func leerArchivo(t *testing.T, path string) []tagLeido {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return leerFLV(t, b)
}

func TestWriterProducesAValidFLVWithThePreambleFirst(t *testing.T) {
	w, segs := nuevoWriter(t, Options{})
	if err := w.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	preambulo(t, w)
	if err := w.WriteAudio(10, pAudio); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteVideo(33, pInter); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(segs.abiertos) != 1 || len(segs.cerrados) != 1 {
		t.Fatalf("abiertos=%d cerrados=%d, quería 1 y 1", len(segs.abiertos), len(segs.cerrados))
	}
	seg := segs.cerrados[0]
	if seg.Index != 1 || seg.DurationMS != 33 || seg.Path != segs.abiertos[0] {
		t.Errorf("segmento = %+v", seg)
	}
	info, err := os.Stat(seg.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != seg.Bytes {
		t.Errorf("Bytes = %d, el archivo mide %d", seg.Bytes, info.Size())
	}
	if filepath.Ext(seg.Path) != ".flv" {
		t.Errorf("extensión de %q", seg.Path)
	}

	tags := leerArchivo(t, seg.Path)
	quiero := []byte{TagScript, TagVideo, TagAudio, TagVideo, TagAudio, TagVideo}
	if len(tags) != len(quiero) {
		t.Fatalf("tags = %d, quería %d", len(tags), len(quiero))
	}
	for i, q := range quiero {
		if tags[i].Tipo != q {
			t.Errorf("tag %d es tipo %d, quería %d", i, tags[i].Tipo, q)
		}
	}
	if tags[3].TS != 0 || tags[4].TS != 10 || tags[5].TS != 33 {
		t.Errorf("timestamps = %d %d %d, quería 0 10 33", tags[3].TS, tags[4].TS, tags[5].TS)
	}
	if !bytes.Equal(tags[0].Data, pMeta) {
		t.Error("el onMetaData no se escribió tal cual")
	}
}

// Cada segmento nuevo empieza por el preámbulo con ts=0 y rebasa los timestamps al
// keyframe de rotación: para el reproductor es un archivo completo por sí mismo.
func TestWriterSegmentsOnAKeyframeAfterTheInterval(t *testing.T) {
	w, segs := nuevoWriter(t, Options{SegmentMinutes: 1})
	if err := w.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	preambulo(t, w)
	// 150 s de media: keyframe cada 2 s, un inter entre medias, audio cada 500 ms.
	for ts := uint32(500); ts <= 150_000; ts += 500 {
		var err error
		switch {
		case ts%2000 == 0:
			err = w.WriteVideo(ts, pKey)
		case ts%1000 == 0:
			err = w.WriteVideo(ts, pInter)
		}
		if err == nil {
			err = w.WriteAudio(ts, pAudio)
		}
		if err != nil {
			t.Fatalf("ts=%d: %v", ts, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if len(segs.cerrados) != 3 {
		t.Fatalf("segmentos = %d, quería 3 (0-60 s, 60-120 s, 120-150 s)", len(segs.cerrados))
	}
	for i, seg := range segs.cerrados {
		if seg.Index != i+1 {
			t.Errorf("segmento %d tiene Index %d", i, seg.Index)
		}
		tags := leerArchivo(t, seg.Path)
		if len(tags) < 4 || tags[0].Tipo != TagScript || tags[1].Tipo != TagVideo || tags[2].Tipo != TagAudio || tags[3].Tipo != TagVideo {
			t.Fatalf("segmento %d no empieza por meta + seq headers + keyframe", i+1)
		}
		if tags[3].TS != 0 || !bytes.Equal(tags[3].Data, pKey) {
			t.Errorf("segmento %d: el primer frame es ts=%d, quería el keyframe con 0", i+1, tags[3].TS)
		}
		if tags[1].TS != 0 || tags[2].TS != 0 {
			t.Errorf("segmento %d: seq headers con ts %d/%d, quería 0", i+1, tags[1].TS, tags[2].TS)
		}
	}
	if d := segs.cerrados[0].DurationMS; d < 59_500 || d > 60_000 {
		t.Errorf("duración del primer segmento = %d ms, quería ~60000", d)
	}
	if d := segs.cerrados[2].DurationMS; d < 29_500 || d > 30_000 {
		t.Errorf("duración del último segmento = %d ms, quería ~30000", d)
	}
}

// Tras rotar, el audio anterior a la base del segmento se descarta en vez de emitirse con
// un timestamp negativo (spec base §3.2).
func TestWriterDropsAudioOlderThanTheSegmentBase(t *testing.T) {
	w, segs := nuevoWriter(t, Options{SegmentMinutes: 1})
	if err := w.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	preambulo(t, w)
	if err := w.WriteVideo(60_000, pKey); err != nil { // rota aquí
		t.Fatal(err)
	}
	if err := w.WriteAudio(59_900, pAudio); err != nil { // llega tarde: fuera
		t.Fatal(err)
	}
	if err := w.WriteAudio(60_100, pAudio); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	tags := leerArchivo(t, segs.cerrados[1].Path)
	var audios []uint32
	for _, tg := range tags[3:] {
		if tg.Tipo == TagAudio {
			audios = append(audios, tg.TS)
		}
	}
	if len(audios) != 1 || audios[0] != 100 {
		t.Errorf("audios del segundo segmento = %v, quería [100]", audios)
	}
}

func TestWriterConnectFailsWhenTheQuotaIsExhausted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	w, segs := nuevoWriter(t, Options{Dir: dir, Quota: Quota{MaxBytes: 100, UsedBytes: 100}})
	err := w.Connect(context.Background())
	if !errors.Is(err, ErrDiskFull) {
		t.Fatalf("Connect = %v, quería ErrDiskFull", err)
	}
	if len(segs.abiertos) != 0 {
		t.Error("se abrió un archivo con la cuota agotada")
	}
	if entradas, _ := os.ReadDir(dir); len(entradas) != 0 {
		t.Errorf("quedaron archivos: %v", entradas)
	}
}

// Al rotar se vuelve a comprobar la cuota: el segmento en curso se cierra bien y es el
// siguiente el que no arranca.
func TestWriterRotationFailsCleanlyWhenTheQuotaRunsOut(t *testing.T) {
	w, segs := nuevoWriter(t, Options{SegmentMinutes: 1, Quota: Quota{MaxBytes: 400}})
	if err := w.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	preambulo(t, w)
	// Rellenar por encima de 400 bytes antes de la rotación.
	for ts := uint32(500); ts < 60_000; ts += 500 {
		if err := w.WriteAudio(ts, pAudio); err != nil {
			t.Fatal(err)
		}
	}
	err := w.WriteVideo(60_000, pKey)
	if !errors.Is(err, ErrDiskFull) {
		t.Fatalf("la rotación devolvió %v, quería ErrDiskFull", err)
	}
	if len(segs.cerrados) != 1 || segs.cerrados[0].Index != 1 {
		t.Errorf("el primer segmento no se cerró limpiamente: %+v", segs.cerrados)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close tras el fallo = %v", err)
	}
}

func TestWriterWarnsOnceAtEightyPercent(t *testing.T) {
	avisos := 0
	w, _ := nuevoWriter(t, Options{SegmentMinutes: 1, Quota: Quota{MaxBytes: 1000, UsedBytes: 850}})
	w.o.OnDiskWarning = func(used, max int64) { avisos++ }
	if err := w.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	preambulo(t, w)
	if err := w.WriteVideo(60_000, pKey); err != nil { // rotación: segunda comprobación
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if avisos != 1 {
		t.Errorf("avisos = %d, quería exactamente 1", avisos)
	}
}

func TestWriterCloseIsIdempotentAndWriteAfterCloseFails(t *testing.T) {
	w, _ := nuevoWriter(t, Options{})
	if err := w.Close(); err != nil {
		t.Errorf("Close sin Connect = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("segundo Close = %v", err)
	}
	if err := w.WriteAudio(0, pAudio); err == nil {
		t.Error("escribir tras Close no dio error")
	}
}

// La prueba de verdad: tags de un FLV generado por ffmpeg pasan por el writer y ffprobe
// reconoce el resultado. Sin ffmpeg/ffprobe se salta; la CI los tiene en el job de
// integración.
func TestWriterOutputIsReadableByFFprobe(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("hace falta %s", tool)
		}
	}
	origen := filepath.Join(t.TempDir(), "origen.flv")
	gen := exec.Command("ffmpeg", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", "3", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "30",
		"-c:a", "aac", "-ar", "44100", "-f", "flv", origen)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	b, err := os.ReadFile(origen)
	if err != nil {
		t.Fatal(err)
	}

	w, segs := nuevoWriter(t, Options{})
	if err := w.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tg := range leerFLV(t, b) {
		var err error
		switch tg.Tipo {
		case TagScript:
			err = w.WriteMeta(tg.TS, tg.Data)
		case TagVideo:
			err = w.WriteVideo(tg.TS, tg.Data)
		case TagAudio:
			err = w.WriteAudio(tg.TS, tg.Data)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	probe := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name:format=duration",
		"-of", "default=noprint_wrappers=1", segs.cerrados[0].Path)
	out, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v\n%s", err, out)
	}
	for _, want := range []string{"codec_name=h264", "codec_name=aac", "duration="} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("falta %q en:\n%s", want, out)
		}
	}
}
