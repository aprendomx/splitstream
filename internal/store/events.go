package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrSessionNotFound se devuelve cuando el id de sesión no existe.
var ErrSessionNotFound = errors.New("sesión no encontrada")

// Level es la severidad de un evento.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Valid indica si l es una severidad soportada.
func (l Level) Valid() bool {
	switch l {
	case LevelInfo, LevelWarn, LevelError:
		return true
	}
	return false
}

const (
	defaultEventLimit = 100
	maxEventLimit     = 1000
)

// Session es una transmisión: desde que OBS conecta hasta que se va. Los campos de
// resolución y bitrate son punteros porque una sesión abierta los tiene en NULL, y
// porque "desconocido" y "cero" no deben ser indistinguibles.
type Session struct {
	ID         int64
	StartedAt  time.Time
	EndedAt    *time.Time
	Width      *int
	Height     *int
	BitrateBPS *int
}

// Event es una entrada del log persistente. SessionID y DestinationID son
// opcionales: un evento del sistema no tiene ninguno de los dos.
type Event struct {
	ID            int64
	SessionID     *int64
	DestinationID *int64
	Level         Level
	Kind          string
	Message       string
	CreatedAt     time.Time
}

// StartSession abre una sesión y devuelve su id.
func (d *DB) StartSession(ctx context.Context) (int64, error) {
	res, err := d.ex.ExecContext(ctx,
		`INSERT INTO sessions (started_at) VALUES (?)`, nowRFC3339())
	if err != nil {
		return 0, fmt.Errorf("iniciar sesión: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("iniciar sesión: %w", err)
	}
	return id, nil
}

// FinishSession cierra la sesión y guarda lo que se midió del ingest.
func (d *DB) FinishSession(ctx context.Context, id int64, width, height, bitrateBPS int) error {
	res, err := d.ex.ExecContext(ctx,
		`UPDATE sessions SET ended_at = ?, width = ?, height = ?, bitrate_bps = ? WHERE id = ?`,
		nowRFC3339(), width, height, bitrateBPS, id)
	if err != nil {
		return fmt.Errorf("cerrar sesión: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cerrar sesión: %w", err)
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// SessionByID devuelve una sesión por su id.
func (d *DB) SessionByID(ctx context.Context, id int64) (*Session, error) {
	var (
		s         Session
		startedAt string
		endedAt   *string
	)
	err := d.ex.QueryRowContext(ctx,
		`SELECT id, started_at, ended_at, width, height, bitrate_bps FROM sessions WHERE id = ?`, id).
		Scan(&s.ID, &startedAt, &endedAt, &s.Width, &s.Height, &s.BitrateBPS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("leer sesión: %w", err)
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
	return &s, nil
}

// LogEvent persiste un evento y devuelve su id. Ignora e.ID y e.CreatedAt.
func (d *DB) LogEvent(ctx context.Context, e Event) (int64, error) {
	if !e.Level.Valid() {
		return 0, fmt.Errorf("nivel de evento desconocido %q", e.Level)
	}
	if e.Kind == "" {
		return 0, fmt.Errorf("el evento necesita un kind")
	}
	res, err := d.ex.ExecContext(ctx,
		`INSERT INTO events (session_id, destination_id, level, kind, message, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.SessionID, e.DestinationID, string(e.Level), e.Kind, e.Message, nowRFC3339())
	if err != nil {
		return 0, fmt.Errorf("registrar evento: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("registrar evento: %w", err)
	}
	return id, nil
}

// RecentEvents devuelve los eventos del más reciente al más antiguo.
func (d *DB) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = defaultEventLimit
	}
	if limit > maxEventLimit {
		limit = maxEventLimit
	}

	rows, err := d.ex.QueryContext(ctx,
		`SELECT id, session_id, destination_id, level, kind, message, created_at
		 FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("leer eventos: %w", err)
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		var (
			e         Event
			level     string
			createdAt string
		)
		if err := rows.Scan(&e.ID, &e.SessionID, &e.DestinationID, &level,
			&e.Kind, &e.Message, &createdAt); err != nil {
			return nil, fmt.Errorf("leer eventos: %w", err)
		}
		e.Level = Level(level)
		if e.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, fmt.Errorf("created_at inválido: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("leer eventos: %w", err)
	}
	return out, nil
}
