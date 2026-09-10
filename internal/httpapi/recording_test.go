package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// fakeRecorder construye un sink con el id reservado que no escribe a ninguna parte, y
// apunta las sesiones para las que se le pidió.
type fakeRecorder struct {
	llamadas []int64
	nilSink  bool
}

func (f *fakeRecorder) BuildRecorder(ctx context.Context, sessionID int64) (*relay.Sink, error) {
	f.llamadas = append(f.llamadas, sessionID)
	if f.nilSink {
		return nil, nil
	}
	return relay.NewSink(relay.SinkConfig{ID: relay.RecorderSinkID, Name: "grabación"}), nil
}

func decodeRecSettings(t *testing.T, body []byte) recordingSettingsDTO {
	t.Helper()
	var out recordingSettingsDTO
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decodificar: %v — %s", err, body)
	}
	return out
}

func TestRecordingSettingsGetAndPatch(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()

	rec := do(t, srv, cookies, http.MethodGet, "/api/recording/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d — %s", rec.Code, rec.Body.String())
	}
	got := decodeRecSettings(t, rec.Body.Bytes())
	if got.Enabled || got.SegmentMin != 10 || got.MaxGB != 20 || got.KeepDays != 30 || got.Dir != srv.recDir {
		t.Errorf("defaults = %+v", got)
	}
	if got.FreeBytes <= 0 {
		t.Errorf("free_bytes = %d, quería > 0 para un directorio real", got.FreeBytes)
	}

	rec = do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"segment_min":5,"max_gb":2.5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d — %s", rec.Code, rec.Body.String())
	}
	if got = decodeRecSettings(t, rec.Body.Bytes()); got.SegmentMin != 5 || got.MaxGB != 2.5 {
		t.Errorf("patch mal aplicado: %+v", got)
	}

	rec = do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"segment_min":-1}`)
	if rec.Code != http.StatusBadRequest || errorCodeDe(t, rec) != codeInvalidInput {
		t.Errorf("segment_min=-1: %d %s", rec.Code, rec.Body.String())
	}
}

// Encender con sesión viva arranca la grabación al momento; apagar la para. Los demás
// ajustes esperan a la siguiente sesión.
func TestPatchEnablingRecordingWhileLiveAddsAndRemovesTheSink(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	fr := &fakeRecorder{}
	srv.recorder = fr
	eng.setLive(7)

	if rec := do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("encender: %d — %s", rec.Code, rec.Body.String())
	}
	added, _ := eng.snapshotSinks()
	if len(fr.llamadas) != 1 || fr.llamadas[0] != 7 || len(added) != 1 || added[0] != relay.RecorderSinkID {
		t.Errorf("llamadas=%v added=%v", fr.llamadas, added)
	}

	// Cambiar otro ajuste con la grabación ya encendida Y CORRIENDO no reconstruye el
	// sink. El fake no arranca nada de verdad, así que se le dice a mano que el sink de
	// grabación está en el motor, como estaría tras el AddSink de arriba.
	eng.setMetrics(map[int64]relay.Metrics{relay.RecorderSinkID: {State: "live"}})
	if rec := do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"segment_min":3}`); rec.Code != http.StatusOK {
		t.Fatalf("segment_min: %d", rec.Code)
	}
	if added, _ = eng.snapshotSinks(); len(added) != 1 {
		t.Errorf("un ajuste sin cambio de enabled reconstruyó el sink: %v", added)
	}

	if rec := do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("apagar: %d", rec.Code)
	}
	if _, removed := eng.snapshotSinks(); len(removed) != 1 || removed[0] != relay.RecorderSinkID {
		t.Errorf("removed = %v", removed)
	}
}

// La grabación puede estar encendida en la base y NO estar corriendo: al abrir la sesión
// no había sitio y BuildRecorder la saltó. Hacer hueco en el disco y volver a guardar los
// ajustes la rearma sin cortar la emisión. El Snapshot vacío del fake es justo ese caso.
func TestPatchRecoversASkippedRecordingWhenEnabledStaysOn(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	fr := &fakeRecorder{}
	srv.recorder = fr
	on := true
	if _, err := db.UpdateRecordingSettings(context.Background(), store.RecordingSettingsPatch{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	eng.setLive(7)

	if rec := do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"segment_min":5}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d — %s", rec.Code, rec.Body.String())
	}
	added, removed := eng.snapshotSinks()
	if len(fr.llamadas) != 1 || fr.llamadas[0] != 7 {
		t.Errorf("llamadas a BuildRecorder = %v, quería [7]", fr.llamadas)
	}
	if len(added) != 1 || added[0] != relay.RecorderSinkID || len(removed) != 0 {
		t.Errorf("added=%v removed=%v, quería que se añadiera el recorder", added, removed)
	}
}

func TestPatchRecordingWithoutASessionDoesNotTouchTheEngine(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	fr := &fakeRecorder{}
	srv.recorder = fr
	if rec := do(t, srv, cookies, http.MethodPatch, "/api/recording/settings", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d", rec.Code)
	}
	if added, _ := eng.snapshotSinks(); len(added) != 0 || len(fr.llamadas) != 0 {
		t.Errorf("sin sesión se tocó el motor: added=%v llamadas=%v", added, fr.llamadas)
	}
}

func TestStatusCarriesTheRecordingBlock(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()
	on := true
	if _, err := db.UpdateRecordingSettings(context.Background(), store.RecordingSettingsPatch{Enabled: &on}); err != nil {
		t.Fatal(err)
	}

	st := decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if !st.Recording.Enabled || st.Recording.Active || st.Recording.Dir != srv.recDir {
		t.Errorf("sin sesión: %+v", st.Recording)
	}

	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{relay.RecorderSinkID: {State: "live", BytesSent: 500, Degraded: true, DroppedFrames: 3}})
	st = decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if !st.Recording.Active || st.Recording.State != "live" || st.Recording.Bytes != 500 || !st.Recording.Degraded || st.Recording.DroppedFrames != 3 {
		t.Errorf("con sesión: %+v", st.Recording)
	}
	if st.Recording.MaxBytes != 20<<30 || st.Recording.FreeBytes <= 0 {
		t.Errorf("cuota: %+v", st.Recording)
	}
}

func crearGrabacion(t *testing.T, srv *Server, db *store.DB, rel string, contenido []byte, cerrar bool) int64 {
	t.Helper()
	ctx := context.Background()
	abs := filepath.Join(srv.recDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, contenido, 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := db.OpenRecording(ctx, 0, rel, 1, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if cerrar {
		if err := db.FinishRecording(ctx, rel, time.Now(), int64(len(contenido)), 1000); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestListDownloadAndDeleteRecordings(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()
	contenido := []byte("FLV\x01\x05\x00\x00\x00\x09\x00\x00\x00\x00")
	id := crearGrabacion(t, srv, db, "sesion-1/20260910-120000-01.flv", contenido, true)

	rec := do(t, srv, cookies, http.MethodGet, "/api/recordings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("listar: %d — %s", rec.Code, rec.Body.String())
	}
	var lista []recordingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &lista); err != nil {
		t.Fatal(err)
	}
	if len(lista) != 1 || lista[0].ID != id || lista[0].InProgress || lista[0].Bytes != int64(len(contenido)) {
		t.Errorf("lista = %+v", lista)
	}

	rec = do(t, srv, cookies, http.MethodGet, "/api/recordings/"+itoa(id)+"/download", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("descargar: %d — %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="20260910-120000-01.flv"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if !strings.HasPrefix(rec.Body.String(), "FLV") || rec.Body.Len() != len(contenido) {
		t.Errorf("cuerpo = %q", rec.Body.String())
	}

	if rec = do(t, srv, cookies, http.MethodDelete, "/api/recordings/"+itoa(id), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("borrar: %d — %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(srv.recDir, "sesion-1", "20260910-120000-01.flv")); !os.IsNotExist(err) {
		t.Error("el archivo sigue en disco")
	}
	if rec = do(t, srv, cookies, http.MethodGet, "/api/recordings/"+itoa(id)+"/download", ""); rec.Code != http.StatusNotFound {
		t.Errorf("descargar tras borrar: %d, quería 404", rec.Code)
	}
}

func TestDownloadAndDeleteRefuseAnOpenSegment(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()
	id := crearGrabacion(t, srv, db, "s/abierta.flv", []byte("FLV"), false)

	if rec := do(t, srv, cookies, http.MethodGet, "/api/recordings/"+itoa(id)+"/download", ""); rec.Code != http.StatusConflict {
		t.Errorf("descargar en curso: %d, quería 409", rec.Code)
	}
	if rec := do(t, srv, cookies, http.MethodDelete, "/api/recordings/"+itoa(id), ""); rec.Code != http.StatusConflict {
		t.Errorf("borrar en curso: %d, quería 409", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(srv.recDir, "s", "abierta.flv")); err != nil {
		t.Error("el archivo en curso desapareció")
	}
}

// Una fila cuyo archivo ya no está (borrado a mano) se puede borrar igual: mejor limpiar
// que quedarse con una fila que no se puede quitar.
func TestDeleteRecordingWhoseFileIsGone(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()
	id := crearGrabacion(t, srv, db, "s/b.flv", []byte("FLV"), true)
	if err := os.Remove(filepath.Join(srv.recDir, "s", "b.flv")); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, srv, cookies, http.MethodDelete, "/api/recordings/"+itoa(id), ""); rec.Code != http.StatusNoContent {
		t.Errorf("borrar sin archivo: %d — %s", rec.Code, rec.Body.String())
	}
}

func TestMetricsIncludeRecording(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	srv.recDir = t.TempDir()
	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{relay.RecorderSinkID: {State: "live", BytesSent: 42, DroppedFrames: 2}})
	m := parseMetrics(t, getMetrics(t, srv, cookies, "").Body.String())
	if m["splitstream_recording_active"] != "1" || m["splitstream_recording_bytes_total"] != "42" || m["splitstream_recording_dropped_frames_total"] != "2" {
		t.Errorf("métricas de grabación: %v", claves(m))
	}
	if m["splitstream_recording_free_bytes"] == "" || m["splitstream_recording_free_bytes"] == "0" {
		t.Errorf("free_bytes = %q", m["splitstream_recording_free_bytes"])
	}
}
