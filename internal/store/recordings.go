package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"
)

var (
	ErrRecordingNotFound   = notFound("grabación no encontrada")
	ErrRecordingInProgress = conflict("la grabación está en curso")
)

// Recording es un segmento grabado. Path es relativo al directorio de grabaciones.
type Recording struct {
	ID         int64
	SessionID  *int64
	Path       string
	Segment    int
	StartedAt  time.Time
	EndedAt    *time.Time
	Bytes      int64
	DurationMS int
}

// OpenRecording registra un segmento recién abierto. sessionID 0 se guarda como NULL.
func (d *DB) OpenRecording(ctx context.Context, sessionID int64, path string, segment int, startedAt time.Time) (int64, error) {
	var sid *int64
	if sessionID != 0 {
		sid = &sessionID
	}
	res, err := d.ex.ExecContext(ctx,
		`INSERT INTO recordings (session_id, path, segment, started_at) VALUES (?, ?, ?, ?)`,
		sid, path, segment, formatTime(startedAt))
	if err != nil {
		return 0, fmt.Errorf("registrar la grabación: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// FinishRecording cierra un segmento por su path.
func (d *DB) FinishRecording(ctx context.Context, path string, endedAt time.Time, bytes int64, durationMS int) error {
	res, err := d.ex.ExecContext(ctx,
		`UPDATE recordings SET ended_at = ?, bytes = ?, duration_ms = ? WHERE path = ?`,
		formatTime(endedAt), bytes, durationMS, path)
	if err != nil {
		return fmt.Errorf("cerrar la grabación: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRecordingNotFound
	}
	return nil
}

// CloseDanglingRecordings cierra las filas que quedaron en curso de un arranque anterior.
//
// Existe porque un `kill -9` —o un corte de luz— no llama a OnSegment: la fila se queda
// con `ended_at NULL` y `bytes 0` para siempre. Y una fila así envenena todo lo demás:
// no se puede descargar ni borrar (409), la poda la salta, la cuota la cuenta como 0
// bytes y PruneSessions nunca puede reciclar su sesión.
//
// stat responde por el path relativo: si el archivo está, la fila se cierra con SU tamaño
// y SU fecha de modificación —lo escrito hasta el último flush es reproducible, spec v0.9
// §4—; si no está, la fila se borra porque no describe nada. `duration_ms` se deja como
// está (0 si nunca se supo): la duración real exigiría releer el FLV entero, y para lo
// que sirve la columna —informar en el listado— no lo vale.
func (d *DB) CloseDanglingRecordings(ctx context.Context, stat func(rel string) (bytes int64, mtime time.Time, exists bool)) (closed, removed int, err error) {
	abiertas, err := d.recordingsWhere(ctx, `ended_at IS NULL ORDER BY id`)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range abiertas {
		bytes, mtime, exists := stat(r.Path)
		if !exists {
			if _, err := d.ex.ExecContext(ctx, `DELETE FROM recordings WHERE id = ?`, r.ID); err != nil {
				return closed, removed, fmt.Errorf("borrar la grabación sin archivo %s: %w", r.Path, err)
			}
			removed++
			continue
		}
		if _, err := d.ex.ExecContext(ctx,
			`UPDATE recordings SET ended_at = ?, bytes = ? WHERE id = ?`,
			formatTime(mtime), bytes, r.ID); err != nil {
			return closed, removed, fmt.Errorf("cerrar la grabación %s: %w", r.Path, err)
		}
		closed++
	}
	return closed, removed, nil
}

const (
	defaultRecordingLimit = 50
	maxRecordingLimit     = 500
)

// ListRecordings devuelve grabaciones de la más reciente a la más antigua. sessionID 0
// no filtra; before pagina por id (0 = desde la última).
func (d *DB) ListRecordings(ctx context.Context, sessionID int64, limit int, before int64) ([]Recording, error) {
	if limit <= 0 {
		limit = defaultRecordingLimit
	}
	if limit > maxRecordingLimit {
		limit = maxRecordingLimit
	}
	rows, err := d.ex.QueryContext(ctx,
		`SELECT id, session_id, path, segment, started_at, ended_at, bytes, duration_ms
		   FROM recordings
		  WHERE (? = 0 OR session_id = ?) AND (? = 0 OR id < ?)
		  ORDER BY id DESC LIMIT ?`, sessionID, sessionID, before, before, limit)
	if err != nil {
		return nil, fmt.Errorf("listar grabaciones: %w", err)
	}
	defer rows.Close()
	out := []Recording{}
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar grabaciones: %w", err)
	}
	return out, nil
}

func (d *DB) RecordingByID(ctx context.Context, id int64) (*Recording, error) {
	r, err := scanRecording(d.ex.QueryRowContext(ctx,
		`SELECT id, session_id, path, segment, started_at, ended_at, bytes, duration_ms FROM recordings WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRecordingNotFound
	}
	return r, err
}

// DeleteRecording borra la fila. NO borra el archivo: eso lo hace quien conoce el
// directorio raíz, antes de llamar aquí.
func (d *DB) DeleteRecording(ctx context.Context, id int64) error {
	r, err := d.RecordingByID(ctx, id)
	if err != nil {
		return err
	}
	if r.EndedAt == nil {
		return ErrRecordingInProgress
	}
	if _, err := d.ex.ExecContext(ctx, `DELETE FROM recordings WHERE id = ?`, id); err != nil {
		return fmt.Errorf("borrar la grabación: %w", err)
	}
	return nil
}

func (d *DB) RecordingsTotalBytes(ctx context.Context) (int64, error) {
	var total int64
	if err := d.ex.QueryRowContext(ctx, `SELECT coalesce(sum(bytes), 0) FROM recordings`).Scan(&total); err != nil {
		return 0, fmt.Errorf("sumar las grabaciones: %w", err)
	}
	return total, nil
}

func (d *DB) CountSessionRecordings(ctx context.Context, sessionID int64) (int, error) {
	var n int
	if err := d.ex.QueryRowContext(ctx, `SELECT count(*) FROM recordings WHERE session_id = ?`, sessionID).Scan(&n); err != nil {
		return 0, fmt.Errorf("contar las grabaciones: %w", err)
	}
	return n, nil
}

// PruneRecordings borra segmentos cerrados: primero los terminados antes de olderThan (si
// no es cero), después los más antiguos mientras la suma supere maxBytes (si es > 0): la
// de gigas manda (roadmap §6). remove borra el archivo por su path relativo; ErrNotExist
// cuenta como borrado. Un fallo real al borrar conserva la fila: mejor una fila huérfana
// visible que un archivo huérfano invisible.
func (d *DB) PruneRecordings(ctx context.Context, olderThan time.Time, maxBytes int64, remove func(path string) error) (deleted int, freed int64, err error) {
	borrar := func(r Recording) error {
		if err := remove(r.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("borrar %s: %w", r.Path, err)
		}
		if _, err := d.ex.ExecContext(ctx, `DELETE FROM recordings WHERE id = ?`, r.ID); err != nil {
			return fmt.Errorf("borrar la fila de %s: %w", r.Path, err)
		}
		deleted++
		freed += r.Bytes
		return nil
	}

	if !olderThan.IsZero() {
		viejas, err := d.recordingsWhere(ctx, `ended_at IS NOT NULL AND ended_at < ? ORDER BY id`, formatTime(olderThan))
		if err != nil {
			return deleted, freed, err
		}
		for _, r := range viejas {
			if err := borrar(r); err != nil {
				return deleted, freed, err
			}
		}
	}
	if maxBytes > 0 {
		total, err := d.RecordingsTotalBytes(ctx)
		if err != nil {
			return deleted, freed, err
		}
		if total > maxBytes {
			cerradas, err := d.recordingsWhere(ctx, `ended_at IS NOT NULL ORDER BY id`)
			if err != nil {
				return deleted, freed, err
			}
			for _, r := range cerradas {
				if total <= maxBytes {
					break
				}
				if err := borrar(r); err != nil {
					return deleted, freed, err
				}
				total -= r.Bytes
			}
		}
	}
	return deleted, freed, nil
}

func (d *DB) recordingsWhere(ctx context.Context, where string, args ...any) ([]Recording, error) {
	rows, err := d.ex.QueryContext(ctx,
		`SELECT id, session_id, path, segment, started_at, ended_at, bytes, duration_ms FROM recordings WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("leer grabaciones: %w", err)
	}
	defer rows.Close()
	var out []Recording
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func scanRecording(s scanner) (*Recording, error) {
	var (
		r         Recording
		startedAt string
		endedAt   *string
	)
	if err := s.Scan(&r.ID, &r.SessionID, &r.Path, &r.Segment, &startedAt, &endedAt, &r.Bytes, &r.DurationMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("leer grabación: %w", err)
	}
	var err error
	if r.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt); err != nil {
		return nil, fmt.Errorf("started_at inválido: %w", err)
	}
	if endedAt != nil {
		t, err := time.Parse(time.RFC3339Nano, *endedAt)
		if err != nil {
			return nil, fmt.Errorf("ended_at inválido: %w", err)
		}
		r.EndedAt = &t
	}
	return &r, nil
}
