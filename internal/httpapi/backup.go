package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

// handleBackup descarga una copia consistente de la base (spec v0.8 §7).
//
// Es POST y no GET a propósito: produce un archivo con todas las claves —cifradas, pero
// todas— y deja un evento. Un GET lo prefetchearía cualquier extensión del navegador.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	dir, err := os.MkdirTemp("", "splitstream-backup-")
	if err != nil {
		s.logger.Error("no se pudo crear el temporal del respaldo", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	defer os.RemoveAll(dir)

	nombre := "splitstream-" + time.Now().UTC().Format("20060102-150405") + ".db"
	ruta := filepath.Join(dir, nombre)
	if err := s.db.BackupTo(r.Context(), ruta); err != nil {
		s.logger.Error("no se pudo generar el respaldo", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "no se pudo generar el respaldo")
		return
	}

	// warn y no info: es un archivo con todas las claves. Si alguien entra en tu panel,
	// quieres poder ver que se lo llevó.
	if _, err := s.db.LogEvent(r.Context(), store.Event{
		Level: store.LevelWarn, Kind: "backup_downloaded",
		Message: "se descargó un respaldo de la base de datos",
	}); err != nil {
		s.logger.Error("no se pudo registrar la descarga del respaldo", "err", err)
	}

	f, err := os.Open(ruta)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	defer f.Close()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+nombre+`"`)
	// Recordatorio para quien mire las cabeceras: sin la clave, esto es ilegible.
	w.Header().Set("X-Splitstream-Note", "sin splitstream.key (o SPLITSTREAM_MASTER_KEY) este archivo no sirve")
	http.ServeContent(w, r, nombre, info.ModTime(), f)
}
