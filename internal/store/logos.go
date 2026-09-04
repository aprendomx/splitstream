package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrLogoNotFound: ese destino existe pero no tiene logo. Se distingue de que el destino
// no exista, porque la API responde distinto a cada caso.
var ErrLogoNotFound = notFound("ese canal no tiene logo")

// Logo es la imagen de un destino tal y como está guardada: PNG ya normalizado.
type Logo struct {
	Image     []byte
	ETag      string
	UpdatedAt time.Time
}

// etagDe deriva el identificador de versión de los bytes.
//
// Son los primeros 64 bits del SHA-256 en hexadecimal: 16 caracteres. Va corto a propósito
// porque este valor viaja en el DTO del destino, y ese DTO lo empuja el WebSocket cada
// segundo mientras el panel esté abierto. Para distinguir versiones de una imagen y romper
// una caché, 64 bits sobran; no es un control de integridad frente a un adversario.
func etagDe(image []byte) string {
	sum := sha256.Sum256(image)
	return hex.EncodeToString(sum[:8])
}

// SetDestinationLogo guarda (o reemplaza) el logo de un destino y devuelve su etag.
//
// Recibe los bytes ya normalizados: validar el formato y reducir el tamaño es trabajo de
// quien recibe la subida, no del store.
func (d *DB) SetDestinationLogo(ctx context.Context, id int64, image []byte) (string, error) {
	if len(image) == 0 {
		return "", invalidInput("el logo llegó vacío")
	}

	// Se comprueba que el destino existe en vez de dejar que falle la clave ajena: el error
	// del driver diría "FOREIGN KEY constraint failed", que es lo que acabaría leyendo el
	// usuario en el panel.
	if _, err := d.destination(ctx, id); err != nil {
		return "", err
	}

	etag := etagDe(image)
	_, err := d.ex.ExecContext(ctx,
		`INSERT INTO destination_logos (destination_id, image, etag, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (destination_id) DO UPDATE SET
		     image = excluded.image, etag = excluded.etag, updated_at = excluded.updated_at`,
		id, image, etag, nowRFC3339())
	if err != nil {
		return "", fmt.Errorf("guardar el logo: %w", err)
	}
	return etag, nil
}

// DestinationLogo devuelve el logo de un destino. ErrLogoNotFound si no tiene.
func (d *DB) DestinationLogo(ctx context.Context, id int64) (*Logo, error) {
	var l Logo
	var actualizado string
	err := d.ex.QueryRowContext(ctx,
		`SELECT image, etag, updated_at FROM destination_logos WHERE destination_id = ?`, id).
		Scan(&l.Image, &l.ETag, &actualizado)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLogoNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("leer el logo: %w", err)
	}
	l.UpdatedAt, err = time.Parse(time.RFC3339Nano, actualizado)
	if err != nil {
		return nil, fmt.Errorf("leer el logo: fecha ilegible %q: %w", actualizado, err)
	}
	return &l, nil
}

// DeleteDestinationLogo quita el logo de un destino. Es idempotente: quitar uno que ya no
// está no es un error, porque lo que se pedía —que no haya logo— ya se cumple.
func (d *DB) DeleteDestinationLogo(ctx context.Context, id int64) error {
	if _, err := d.ex.ExecContext(ctx,
		`DELETE FROM destination_logos WHERE destination_id = ?`, id); err != nil {
		return fmt.Errorf("borrar el logo: %w", err)
	}
	return nil
}

// DestinationLogoETags devuelve qué destinos tienen logo y con qué versión, sin traer ni
// un byte de imagen. Es lo que necesita el listado para decidir si pinta un avatar.
func (d *DB) DestinationLogoETags(ctx context.Context) (map[int64]string, error) {
	rows, err := d.ex.QueryContext(ctx, `SELECT destination_id, etag FROM destination_logos`)
	if err != nil {
		return nil, fmt.Errorf("listar los logos: %w", err)
	}
	defer rows.Close()

	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var etag string
		if err := rows.Scan(&id, &etag); err != nil {
			return nil, fmt.Errorf("listar los logos: %w", err)
		}
		out[id] = etag
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar los logos: %w", err)
	}
	return out, nil
}
