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
	Info         int
	Warn         int
	Error        int
	HasRecording bool
}

const (
	defaultSessionLimit = 50
	maxSessionLimit     = 500

	// eventsBySessionCap es el tope duro de EventsBySession: una sesión de 8 horas
	// con reconexiones no pasa de unos cientos de eventos, así que el tope es un
	// cinturón de seguridad, no un límite de diseño.
	defaultEventsBySessionLimit = 2000
	eventsBySessionCap          = 2000
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
		        (SELECT count(*) FROM events e WHERE e.session_id = s.id AND e.level = 'error'),
		        EXISTS (SELECT 1 FROM recordings r WHERE r.session_id = s.id)
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
			&s.Info, &s.Warn, &s.Error, &s.HasRecording); err != nil {
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

// EventsBySession devuelve los eventos de una sesión del más antiguo al más reciente,
// para reconstruir su cronología en la vista de historial. limit <= 0 usa
// defaultEventsBySessionLimit; el tope duro es eventsBySessionCap.
func (d *DB) EventsBySession(ctx context.Context, sessionID int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = defaultEventsBySessionLimit
	}
	if limit > eventsBySessionCap {
		limit = eventsBySessionCap
	}
	rows, err := d.ex.QueryContext(ctx,
		`SELECT id, session_id, destination_id, level, kind, message, created_at
		   FROM events WHERE session_id = ? ORDER BY id ASC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("leer eventos de la sesión: %w", err)
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("leer eventos de la sesión: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("leer eventos de la sesión: %w", err)
	}
	return out, nil
}

// ChatCountBySession cuenta los mensajes de chat de una sesión: el total y el desglose
// por plataforma. Devuelve un mapa vacío (no nil) cuando la sesión no tiene mensajes.
func (d *DB) ChatCountBySession(ctx context.Context, sessionID int64) (int, map[string]int, error) {
	rows, err := d.ex.QueryContext(ctx,
		`SELECT platform, count(*) FROM chat_messages WHERE session_id = ? GROUP BY platform`, sessionID)
	if err != nil {
		return 0, nil, fmt.Errorf("contar el chat de la sesión: %w", err)
	}
	defer rows.Close()

	total := 0
	porPlataforma := map[string]int{}
	for rows.Next() {
		var (
			platform string
			n        int
		)
		if err := rows.Scan(&platform, &n); err != nil {
			return 0, nil, fmt.Errorf("contar el chat de la sesión: %w", err)
		}
		porPlataforma[platform] = n
		total += n
	}
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("contar el chat de la sesión: %w", err)
	}
	return total, porPlataforma, nil
}
