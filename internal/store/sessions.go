package store

import (
	"context"
	"fmt"
	"time"
)

// SessionSummary es una sesión con sus contadores de eventos por nivel, para el listado
// del historial.
type SessionSummary struct {
	Session
	Info  int
	Warn  int
	Error int
}

const (
	defaultSessionLimit = 50
	maxSessionLimit     = 500
)

// ListSessions devuelve sesiones de la más reciente a la más antigua. before pagina por
// id (0 = desde la última): nunca por texto de fecha (spec base §15.4).
func (d *DB) ListSessions(ctx context.Context, limit int, before int64) ([]SessionSummary, error) {
	if limit <= 0 {
		limit = defaultSessionLimit
	}
	if limit > maxSessionLimit {
		limit = maxSessionLimit
	}
	rows, err := d.ex.QueryContext(ctx,
		`SELECT s.id, s.started_at, s.ended_at, s.width, s.height, s.bitrate_bps,
		        (SELECT count(*) FROM events e WHERE e.session_id = s.id AND e.level = 'info'),
		        (SELECT count(*) FROM events e WHERE e.session_id = s.id AND e.level = 'warn'),
		        (SELECT count(*) FROM events e WHERE e.session_id = s.id AND e.level = 'error')
		   FROM sessions s
		  WHERE (? = 0 OR s.id < ?)
		  ORDER BY s.id DESC
		  LIMIT ?`, before, before, limit)
	if err != nil {
		return nil, fmt.Errorf("listar sesiones: %w", err)
	}
	defer rows.Close()

	out := []SessionSummary{}
	for rows.Next() {
		var (
			s         SessionSummary
			startedAt string
			endedAt   *string
		)
		if err := rows.Scan(&s.ID, &startedAt, &endedAt, &s.Width, &s.Height, &s.BitrateBPS,
			&s.Info, &s.Warn, &s.Error); err != nil {
			return nil, fmt.Errorf("listar sesiones: %w", err)
		}
		if s.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt); err != nil {
			return nil, fmt.Errorf("started_at inválido: %w", err)
		}
		if endedAt != nil {
			t, err := time.Parse(time.RFC3339Nano, *endedAt)
			if err != nil {
				return nil, fmt.Errorf("ended_at inválido: %w", err)
			}
			s.EndedAt = &t
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar sesiones: %w", err)
	}
	return out, nil
}
