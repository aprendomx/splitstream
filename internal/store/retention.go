package store

import (
	"context"
	"fmt"
	"time"
)

// PruneEvents borra eventos más viejos que olderThan (si no es cero) y deja como mucho
// keepAtMost filas (si es > 0), conservando las más recientes. Devuelve cuántas borró.
//
// La comparación por fecha es sobre texto y funciona porque desde la migración 0002 todos
// los timestamps tienen ancho fijo en UTC (spec base §15.4).
func (d *DB) PruneEvents(ctx context.Context, olderThan time.Time, keepAtMost int) (int64, error) {
	var total int64
	if !olderThan.IsZero() {
		res, err := d.ex.ExecContext(ctx, `DELETE FROM events WHERE created_at < ?`, formatTime(olderThan))
		if err != nil {
			return total, fmt.Errorf("podar eventos por fecha: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if keepAtMost > 0 {
		res, err := d.ex.ExecContext(ctx,
			`DELETE FROM events WHERE id NOT IN (SELECT id FROM events ORDER BY id DESC LIMIT ?)`, keepAtMost)
		if err != nil {
			return total, fmt.Errorf("podar eventos por cantidad: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// PruneSessions borra sesiones cerradas que empezaron antes de olderThan y a las que ya
// no apunta ningún evento. Una sesión abierta nunca se toca, por vieja que parezca: puede
// ser la de ahora mismo tras un reloj mal puesto. Tampoco se toca una sesión con
// grabaciones colgadas: la vista de historial cuelga los archivos de ella, así que
// borrarla dejaría grabaciones huérfanas en el historial.
func (d *DB) PruneSessions(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := d.ex.ExecContext(ctx,
		`DELETE FROM sessions
		  WHERE ended_at IS NOT NULL
		    AND started_at < ?
		    AND id NOT IN (SELECT session_id FROM events WHERE session_id IS NOT NULL)
		    AND id NOT IN (SELECT session_id FROM recordings WHERE session_id IS NOT NULL)`,
		formatTime(olderThan))
	if err != nil {
		return 0, fmt.Errorf("podar sesiones: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PruneQuota borra los días anteriores a olderThanDay (YYYY-MM-DD, exclusivo).
func (d *DB) PruneQuota(ctx context.Context, olderThanDay string) (int64, error) {
	res, err := d.ex.ExecContext(ctx, `DELETE FROM quota_usage WHERE day < ?`, olderThanDay)
	if err != nil {
		return 0, fmt.Errorf("podar la cuota: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PruneChat deja como mucho keepAtMost mensajes de chat, los más recientes. Las filas de
// sesiones borradas ya cayeron por la clave ajena; esto acota el resto.
func (d *DB) PruneChat(ctx context.Context, keepAtMost int) (int64, error) {
	if keepAtMost <= 0 {
		return 0, nil
	}
	res, err := d.ex.ExecContext(ctx,
		`DELETE FROM chat_messages WHERE id NOT IN (SELECT id FROM chat_messages ORDER BY id DESC LIMIT ?)`, keepAtMost)
	if err != nil {
		return 0, fmt.Errorf("podar el chat: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
