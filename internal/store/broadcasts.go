package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	BroadcastCreated  = "created"
	BroadcastLive     = "live"
	BroadcastComplete = "complete"
)

// Broadcast es lo que la plataforma dio para un destino: la emisión de YouTube (ids de
// emisión, stream y chat) o solo la clave (Kick). KeyFromAPI hace que «probar destino»
// se salte: si la clave la trajo la API, no hay clave inválida que probar.
type Broadcast struct {
	DestinationID int64
	AccountID     int64
	Platform      Platform
	BroadcastRef  string
	StreamRef     string
	LiveChatID    string
	KeyFromAPI    bool
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SetBroadcast crea o sustituye la emisión del destino; siempre vuelve a `created`.
func (d *DB) SetBroadcast(ctx context.Context, b Broadcast) error {
	if b.DestinationID == 0 || b.AccountID == 0 || b.Platform == "" {
		return invalidInput("la emisión necesita destino, cuenta y plataforma")
	}
	ahora := nowRFC3339()
	_, err := d.ex.ExecContext(ctx,
		`INSERT INTO destination_broadcasts (destination_id, account_id, platform, broadcast_ref, stream_ref, live_chat_id, key_from_api, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'created', ?, ?)
		 ON CONFLICT (destination_id) DO UPDATE SET account_id = excluded.account_id, platform = excluded.platform,
		    broadcast_ref = excluded.broadcast_ref, stream_ref = excluded.stream_ref, live_chat_id = excluded.live_chat_id,
		    key_from_api = excluded.key_from_api, status = 'created', updated_at = excluded.updated_at`,
		b.DestinationID, b.AccountID, string(b.Platform), b.BroadcastRef, b.StreamRef, b.LiveChatID, boolToInt(b.KeyFromAPI), ahora, ahora)
	if err != nil {
		return fmt.Errorf("guardar la emisión: %w", err)
	}
	return nil
}

// BroadcastFor devuelve la emisión vinculada al destino, o ErrNotFound si no hay.
func (d *DB) BroadcastFor(ctx context.Context, destID int64) (*Broadcast, error) {
	var (
		b                    Broadcast
		platform             string
		key                  int
		createdAt, updatedAt string
	)
	err := d.ex.QueryRowContext(ctx,
		`SELECT destination_id, account_id, platform, broadcast_ref, stream_ref, live_chat_id, key_from_api, status, created_at, updated_at
		   FROM destination_broadcasts WHERE destination_id = ?`, destID).
		Scan(&b.DestinationID, &b.AccountID, &platform, &b.BroadcastRef, &b.StreamRef, &b.LiveChatID, &key, &b.Status, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("el destino no tiene emisión")
	}
	if err != nil {
		return nil, fmt.Errorf("leer la emisión: %w", err)
	}
	b.Platform, b.KeyFromAPI = Platform(platform), key == 1
	if b.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return nil, fmt.Errorf("created_at inválido: %w", err)
	}
	if b.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &b, nil
}

// SetBroadcastStatus cambia el estado de la emisión del destino.
func (d *DB) SetBroadcastStatus(ctx context.Context, destID int64, status string) error {
	switch status {
	case BroadcastCreated, BroadcastLive, BroadcastComplete:
	default:
		return invalidInput("estado de emisión inválido: " + status)
	}
	res, err := d.ex.ExecContext(ctx, `UPDATE destination_broadcasts SET status = ?, updated_at = ? WHERE destination_id = ?`, status, nowRFC3339(), destID)
	if err != nil {
		return fmt.Errorf("cambiar el estado de la emisión: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("el destino no tiene emisión")
	}
	return nil
}

// ClearBroadcast quita la emisión del destino, si la había. Idempotente, como los DELETE
// del resto del paquete.
func (d *DB) ClearBroadcast(ctx context.Context, destID int64) error {
	if _, err := d.ex.ExecContext(ctx, `DELETE FROM destination_broadcasts WHERE destination_id = ?`, destID); err != nil {
		return fmt.Errorf("quitar la emisión: %w", err)
	}
	return nil
}

// BroadcastsByDestination devuelve todas las emisiones indexadas por destino, para decorar
// una lista de destinos sin una consulta por fila.
func (d *DB) BroadcastsByDestination(ctx context.Context) (map[int64]Broadcast, error) {
	rows, err := d.ex.QueryContext(ctx,
		`SELECT destination_id, account_id, platform, broadcast_ref, stream_ref, live_chat_id, key_from_api, status, created_at, updated_at
		   FROM destination_broadcasts`)
	if err != nil {
		return nil, fmt.Errorf("listar emisiones: %w", err)
	}
	defer rows.Close()

	out := map[int64]Broadcast{}
	for rows.Next() {
		var (
			b                    Broadcast
			platform             string
			key                  int
			createdAt, updatedAt string
		)
		if err := rows.Scan(&b.DestinationID, &b.AccountID, &platform, &b.BroadcastRef, &b.StreamRef, &b.LiveChatID, &key, &b.Status, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("leer la emisión: %w", err)
		}
		b.Platform, b.KeyFromAPI = Platform(platform), key == 1
		if b.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, fmt.Errorf("created_at inválido: %w", err)
		}
		if b.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
			return nil, fmt.Errorf("updated_at inválido: %w", err)
		}
		out[b.DestinationID] = b
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar emisiones: %w", err)
	}
	return out, nil
}
