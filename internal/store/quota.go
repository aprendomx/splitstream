package store

import (
	"context"
	"fmt"
)

// AddQuota suma unidades de cuota de una cuenta en un día (formato YYYY-MM-DD, en la zona
// que decida quien llama: para YouTube, la del Pacífico, que es donde Google reinicia).
func (d *DB) AddQuota(ctx context.Context, accountID int64, day string, units int) error {
	if units <= 0 || len(day) != 10 {
		return invalidInput("cuota: unidades y día inválidos")
	}
	_, err := d.ex.ExecContext(ctx,
		`INSERT INTO quota_usage (account_id, day, units) VALUES (?, ?, ?)
		 ON CONFLICT (account_id, day) DO UPDATE SET units = units + excluded.units`, accountID, day, units)
	if err != nil {
		return fmt.Errorf("sumar cuota: %w", err)
	}
	return nil
}

// QuotaUsed devuelve las unidades consumidas por la cuenta en el día.
func (d *DB) QuotaUsed(ctx context.Context, accountID int64, day string) (int, error) {
	var n int
	err := d.ex.QueryRowContext(ctx, `SELECT COALESCE(SUM(units), 0) FROM quota_usage WHERE account_id = ? AND day = ?`, accountID, day).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("leer la cuota: %w", err)
	}
	return n, nil
}
