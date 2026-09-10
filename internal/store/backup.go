package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// BackupTo escribe una copia consistente de la base en path.
//
// Usa VACUUM INTO y no una copia del archivo: en modo WAL, parte de los datos puede
// estar todavía en el -wal, y copiar solo el .db da un archivo que abre pero al que le
// faltan las últimas escrituras. Escribe a un temporal y renombra, para que un fallo a
// medias no deje un respaldo truncado con nombre de bueno.
func (d *DB) BackupTo(ctx context.Context, path string) error {
	if _, ok := d.ex.(*sql.Tx); ok {
		return errors.New("respaldar: no se puede dentro de una transacción")
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if _, err := d.ex.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("respaldar: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("respaldar: %w", err)
	}
	return nil
}
