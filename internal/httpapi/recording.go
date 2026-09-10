package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/aprendomx/splitstream/internal/record"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// freeBytes devuelve el espacio libre del directorio de grabaciones, o 0 si no se puede
// saber (sin directorio configurado, o todavía sin crear).
func (s *Server) freeBytes() int64 {
	if s.recDir == "" {
		return 0
	}
	free, _, err := record.FreeSpace(s.recDir)
	if err != nil {
		return 0
	}
	return free
}

// recordingStatus compone el bloque de grabación del estado. Lo que se sabe del sink sale
// del motor (Snapshot con el id reservado); el resto, del store y del disco.
func (s *Server) recordingStatus(ctx context.Context) (recordingStatusDTO, error) {
	st, err := s.db.RecordingSettings(ctx)
	if err != nil {
		return recordingStatusDTO{}, err
	}
	used, err := s.db.RecordingsTotalBytes(ctx)
	if err != nil {
		return recordingStatusDTO{}, err
	}
	out := recordingStatusDTO{
		Enabled: st.Enabled, MaxBytes: int64(st.MaxGB * float64(1<<30)), UsedBytes: used,
		FreeBytes: s.freeBytes(), Dir: s.recDir,
	}
	if s.engine == nil {
		return out, nil
	}
	if ses := s.engine.Session(); ses.ID != 0 {
		if m, ok := s.engine.Snapshot()[relay.RecorderSinkID]; ok {
			out.Active = m.State == relay.StateLive.String()
			out.State, out.Degraded, out.Bytes, out.DroppedFrames = m.State, m.Degraded, m.BytesSent, m.DroppedFrames
		}
		if n, err := s.db.CountSessionRecordings(ctx, ses.ID); err == nil {
			out.Segments = n
		}
	}
	return out, nil
}

func (s *Server) recordingSettingsDTO(ctx context.Context, st *store.RecordingSettings) (recordingSettingsDTO, error) {
	used, err := s.db.RecordingsTotalBytes(ctx)
	if err != nil {
		return recordingSettingsDTO{}, err
	}
	return recordingSettingsDTO{
		Enabled: st.Enabled, SegmentMin: st.SegmentMin, MaxGB: st.MaxGB, KeepDays: st.KeepDays,
		Dir: s.recDir, UsedBytes: used, FreeBytes: s.freeBytes(),
	}, nil
}

func (s *Server) handleGetRecordingSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.db.RecordingSettings(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	dto, err := s.recordingSettingsDTO(r.Context(), st)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

type recordingSettingsPatch struct {
	Enabled    *bool    `json:"enabled"`
	SegmentMin *int     `json:"segment_min"`
	MaxGB      *float64 `json:"max_gb"`
	KeepDays   *int     `json:"keep_days"`
}

// handlePatchRecordingSettings guarda los ajustes y, si hay sesión viva y cambió
// `enabled`, arranca o para la grabación al momento. Los demás ajustes esperan a la
// siguiente sesión: cambiar el tamaño de segmento a mitad de archivo no vale la pena.
func (s *Server) handlePatchRecordingSettings(w http.ResponseWriter, r *http.Request) {
	var in recordingSettingsPatch
	if !decodeBody(w, r, &in) {
		return
	}
	ctx := r.Context()
	antes, err := s.db.RecordingSettings(ctx)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	st, err := s.db.UpdateRecordingSettings(ctx, store.RecordingSettingsPatch{
		Enabled: in.Enabled, SegmentMin: in.SegmentMin, MaxGB: in.MaxGB, KeepDays: in.KeepDays,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	if s.liveSession() && antes.Enabled != st.Enabled {
		switch {
		case st.Enabled && s.recorder != nil:
			sink, err := s.recorder.BuildRecorder(ctx, s.engine.Session().ID)
			if err != nil {
				s.logger.Error("no se pudo arrancar la grabación en caliente", "err", err)
			} else if sink != nil {
				s.engine.AddSink(sink)
			}
		case !st.Enabled:
			s.engine.RemoveSink(relay.RecorderSinkID)
		}
	}

	dto, err := s.recordingSettingsDTO(ctx, st)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := queryInt(w, r, "session_id")
	if !ok {
		return
	}
	limit, ok := queryInt(w, r, "limit")
	if !ok {
		return
	}
	before, ok := queryInt(w, r, "before")
	if !ok {
		return
	}
	lista, err := s.db.ListRecordings(r.Context(), int64(sessionID), limit, int64(before))
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	out := make([]recordingDTO, 0, len(lista))
	for _, rec := range lista {
		out = append(out, newRecordingDTO(rec))
	}
	writeJSON(w, http.StatusOK, out)
}

// recordingPath resuelve el archivo de una grabación. El path guardado es relativo y no
// puede salir del directorio raíz: filepath.Join lo limpia, y se comprueba el prefijo por
// si una fila editada a mano trajera "..".
func (s *Server) recordingPath(rel string) (string, bool) {
	abs := filepath.Join(s.recDir, filepath.FromSlash(rel))
	root := filepath.Clean(s.recDir) + string(filepath.Separator)
	return abs, s.recDir != "" && len(abs) > len(root) && abs[:len(root)] == root
}

func (s *Server) handleDownloadRecording(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	rec, err := s.db.RecordingByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if rec.EndedAt == nil {
		s.writeStoreError(w, store.ErrRecordingInProgress)
		return
	}
	abs, ok := s.recordingPath(rec.Path)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "la grabación no está en el directorio de grabaciones")
		return
	}
	f, err := os.Open(abs)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, codeNotFound, "el archivo de la grabación ya no está en disco")
		return
	}
	if err != nil {
		s.logger.Error("no se pudo abrir la grabación", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(abs)+`"`)
	http.ServeContent(w, r, filepath.Base(abs), info.ModTime(), f)
}

// handleDeleteRecording borra el archivo y después la fila. Un archivo que ya no existe
// no impide borrar la fila: mejor limpiar que dejar una fila que nadie puede quitar.
func (s *Server) handleDeleteRecording(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	rec, err := s.db.RecordingByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if rec.EndedAt == nil {
		s.writeStoreError(w, store.ErrRecordingInProgress)
		return
	}
	if abs, ok := s.recordingPath(rec.Path); ok {
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.logger.Error("no se pudo borrar el archivo de la grabación", "err", err)
			writeError(w, http.StatusInternalServerError, codeInternal, "no se pudo borrar el archivo")
			return
		}
	}
	if err := s.db.DeleteRecording(r.Context(), id); err != nil {
		s.writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
