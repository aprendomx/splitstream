package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ChatMessage es un mensaje de chat de una sesión, tal como se guardó.
type ChatMessage struct {
	ID        int64
	SessionID int64
	AccountID *int64
	Platform  string
	AuthorID  string
	Author    string
	Text      string
	Color     string
	Badges    []string
	MessageID string
	At        time.Time
}

// NewChatMessage es un mensaje por guardar.
type NewChatMessage struct {
	SessionID int64
	AccountID *int64
	Platform  string
	AuthorID  string
	Author    string
	Text      string
	Color     string
	Badges    []string
	MessageID string
	At        time.Time
}

// InsertChatMessages guarda un lote en una sola transacción. Con una única conexión
// (SetMaxOpenConns(1)), una fila por mensaje competiría con los sinks a cada línea del
// chat; un lote cada 100 ms es una escritura corta y predecible.
func (d *DB) InsertChatMessages(ctx context.Context, ms []NewChatMessage) error {
	if len(ms) == 0 {
		return nil
	}
	return d.InTx(ctx, func(tx *DB) error {
		for _, m := range ms {
			if m.SessionID == 0 || m.Platform == "" {
				return invalidInput("mensaje de chat sin sesión o plataforma")
			}
			if _, err := tx.ex.ExecContext(ctx,
				`INSERT INTO chat_messages (session_id, account_id, platform, author_id, author, text, color, badges, message_id, at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				m.SessionID, m.AccountID, m.Platform, m.AuthorID, m.Author, m.Text, m.Color,
				strings.Join(m.Badges, " "), m.MessageID, formatTime(m.At)); err != nil {
				return fmt.Errorf("guardar el chat: %w", err)
			}
		}
		return nil
	})
}

// ChatMessages devuelve hasta limit mensajes de la sesión con id mayor que after, en
// orden ascendente: es la paginación del historial y del snapshot del WebSocket.
func (d *DB) ChatMessages(ctx context.Context, sessionID, after int64, limit int) ([]ChatMessage, error) {
	if limit <= 0 || limit > 1000 {
		return nil, invalidInput("limit debe estar entre 1 y 1000")
	}
	rows, err := d.ex.QueryContext(ctx,
		`SELECT id, session_id, account_id, platform, author_id, author, text, color, badges, message_id, at
		   FROM chat_messages WHERE session_id = ? AND id > ? ORDER BY id LIMIT ?`, sessionID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("leer el chat: %w", err)
	}
	defer rows.Close()
	out := []ChatMessage{}
	for rows.Next() {
		var (
			m      ChatMessage
			acct   sql.NullInt64
			badges string
			at     string
		)
		if err := rows.Scan(&m.ID, &m.SessionID, &acct, &m.Platform, &m.AuthorID, &m.Author, &m.Text, &m.Color, &badges, &m.MessageID, &at); err != nil {
			return nil, fmt.Errorf("leer mensaje: %w", err)
		}
		if acct.Valid {
			v := acct.Int64
			m.AccountID = &v
		}
		if badges != "" {
			m.Badges = strings.Split(badges, " ")
		}
		if m.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("at inválido: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
