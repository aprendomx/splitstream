package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
)

// ErrDestinationNotFound se devuelve cuando el id no existe.
var ErrDestinationNotFound = errors.New("destino no encontrado")

// Platform es el conjunto cerrado de plataformas soportadas. Duplica el CHECK del
// esquema a propósito: así el error llega antes y con un mensaje legible.
type Platform string

const (
	PlatformYouTube  Platform = "youtube"
	PlatformTwitch   Platform = "twitch"
	PlatformFacebook Platform = "facebook"
	PlatformKick     Platform = "kick"
	PlatformX        Platform = "x"
	PlatformCustom   Platform = "custom"
)

// Valid indica si p es una de las plataformas soportadas.
func (p Platform) Valid() bool {
	switch p {
	case PlatformYouTube, PlatformTwitch, PlatformFacebook, PlatformKick, PlatformX, PlatformCustom:
		return true
	}
	return false
}

// Destination es un destino de retransmisión. No tiene campo para la clave: para
// obtenerla hay que llamar a RevealDestinationKey, de modo que serializar este
// struct nunca puede filtrarla.
type Destination struct {
	ID        int64
	Name      string
	Platform  Platform
	RTMPURL   string
	KeyMask   string
	Enabled   bool
	SortOrder int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewDestination son los datos para crear un destino.
type NewDestination struct {
	Name     string
	Platform Platform
	RTMPURL  string
	Key      crypto.Secret
	Enabled  bool
}

// DestinationPatch es una modificación parcial: los campos nil no se tocan.
type DestinationPatch struct {
	Name     *string
	Platform *Platform
	RTMPURL  *string
	Key      *crypto.Secret
	Enabled  *bool
}

// ListDestinations devuelve todos los destinos ordenados por sort_order.
func (d *DB) ListDestinations(ctx context.Context) ([]Destination, error) {
	rows, err := d.ex.QueryContext(ctx,
		`SELECT id, name, platform, rtmp_url, stream_key_last4, enabled, sort_order, created_at, updated_at
		 FROM destinations ORDER BY sort_order, id`)
	if err != nil {
		return nil, fmt.Errorf("listar destinos: %w", err)
	}
	defer rows.Close()

	out := []Destination{}
	for rows.Next() {
		dest, err := scanDestination(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *dest)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar destinos: %w", err)
	}
	return out, nil
}

// CreateDestination cifra la clave y añade el destino al final del orden.
func (d *DB) CreateDestination(ctx context.Context, c *crypto.Cipher, in NewDestination) (*Destination, error) {
	if !in.Platform.Valid() {
		return nil, fmt.Errorf("plataforma desconocida %q", in.Platform)
	}
	if in.Name == "" {
		return nil, errors.New("el nombre no puede estar vacío")
	}
	if in.RTMPURL == "" {
		return nil, errors.New("la URL RTMP no puede estar vacía")
	}

	encrypted, err := c.Encrypt([]byte(in.Key.Reveal()))
	if err != nil {
		return nil, fmt.Errorf("cifrar la clave del destino: %w", err)
	}

	var next int
	if err := d.ex.QueryRowContext(ctx,
		`SELECT coalesce(max(sort_order), -1) + 1 FROM destinations`).Scan(&next); err != nil {
		return nil, fmt.Errorf("calcular sort_order: %w", err)
	}

	now := nowRFC3339()
	res, err := d.ex.ExecContext(ctx,
		`INSERT INTO destinations
		   (name, platform, rtmp_url, stream_key_encrypted, stream_key_last4, enabled, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Name, string(in.Platform), in.RTMPURL, encrypted, in.Key.Last4(), boolToInt(in.Enabled), next, now, now)
	if err != nil {
		return nil, fmt.Errorf("crear destino: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("crear destino: %w", err)
	}
	return d.destination(ctx, id)
}

// UpdateDestination aplica una modificación parcial. Los campos nil del patch
// se dejan como están.
func (d *DB) UpdateDestination(ctx context.Context, c *crypto.Cipher, id int64, patch DestinationPatch) (*Destination, error) {
	if _, err := d.destination(ctx, id); err != nil {
		return nil, err
	}

	sets := []string{}
	args := []any{}

	if patch.Name != nil {
		if *patch.Name == "" {
			return nil, errors.New("el nombre no puede estar vacío")
		}
		sets = append(sets, "name = ?")
		args = append(args, *patch.Name)
	}
	if patch.Platform != nil {
		if !patch.Platform.Valid() {
			return nil, fmt.Errorf("plataforma desconocida %q", *patch.Platform)
		}
		sets = append(sets, "platform = ?")
		args = append(args, string(*patch.Platform))
	}
	if patch.RTMPURL != nil {
		if *patch.RTMPURL == "" {
			return nil, errors.New("la URL RTMP no puede estar vacía")
		}
		sets = append(sets, "rtmp_url = ?")
		args = append(args, *patch.RTMPURL)
	}
	if patch.Key != nil {
		encrypted, err := c.Encrypt([]byte(patch.Key.Reveal()))
		if err != nil {
			return nil, fmt.Errorf("cifrar la clave del destino: %w", err)
		}
		sets = append(sets, "stream_key_encrypted = ?", "stream_key_last4 = ?")
		args = append(args, encrypted, patch.Key.Last4())
	}
	if patch.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, boolToInt(*patch.Enabled))
	}

	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, nowRFC3339(), id)
		query := "UPDATE destinations SET " + joinComma(sets) + " WHERE id = ?"
		if _, err := d.ex.ExecContext(ctx, query, args...); err != nil {
			return nil, fmt.Errorf("actualizar destino: %w", err)
		}
	}
	return d.destination(ctx, id)
}

// DeleteDestination borra el destino. Los eventos asociados sobreviven con
// destination_id a NULL.
func (d *DB) DeleteDestination(ctx context.Context, id int64) error {
	res, err := d.ex.ExecContext(ctx, `DELETE FROM destinations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("borrar destino: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("borrar destino: %w", err)
	}
	if n == 0 {
		return ErrDestinationNotFound
	}
	return nil
}

// ReorderDestinations fija el orden a partir de la secuencia de ids recibida.
// Exige exactamente el conjunto completo de destinos existentes, sin repetidos y sin
// omisiones. Validar solo la longitud dejaría pasar una lista con un id duplicado, que
// dejaría sort_order empatados en silencio.
func (d *DB) ReorderDestinations(ctx context.Context, ids []int64) error {
	return d.InTx(ctx, func(tx *DB) error {
		existing, err := tx.destinationIDs(ctx)
		if err != nil {
			return err
		}

		seen := make(map[int64]bool, len(ids))
		for _, id := range ids {
			if seen[id] {
				return fmt.Errorf("reordenar: el id %d aparece más de una vez", id)
			}
			if !existing[id] {
				return fmt.Errorf("reordenar: %w (id %d)", ErrDestinationNotFound, id)
			}
			seen[id] = true
		}
		if len(seen) != len(existing) {
			return fmt.Errorf("reordenar exige los %d destinos, se recibieron %d", len(existing), len(seen))
		}

		for i, id := range ids {
			if _, err := tx.ex.ExecContext(ctx,
				`UPDATE destinations SET sort_order = ? WHERE id = ?`, i, id); err != nil {
				return fmt.Errorf("reordenar: %w", err)
			}
		}
		return nil
	})
}

// destinationIDs devuelve el conjunto de ids de destino existentes.
func (d *DB) destinationIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := d.ex.QueryContext(ctx, `SELECT id FROM destinations`)
	if err != nil {
		return nil, fmt.Errorf("reordenar: %w", err)
	}
	defer rows.Close()

	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("reordenar: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reordenar: %w", err)
	}
	return out, nil
}

// RevealDestinationKey descifra y devuelve la clave del destino en claro.
// Es el único camino para obtenerla; quien lo llame debe registrar un evento.
func (d *DB) RevealDestinationKey(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error) {
	var blob []byte
	err := d.ex.QueryRowContext(ctx,
		`SELECT stream_key_encrypted FROM destinations WHERE id = ?`, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDestinationNotFound
	}
	if err != nil {
		return "", fmt.Errorf("leer la clave del destino: %w", err)
	}
	plain, err := c.Decrypt(blob)
	if err != nil {
		return "", fmt.Errorf("descifrar la clave del destino: %w", err)
	}
	return crypto.Secret(plain), nil
}

func (d *DB) destination(ctx context.Context, id int64) (*Destination, error) {
	row := d.ex.QueryRowContext(ctx,
		`SELECT id, name, platform, rtmp_url, stream_key_last4, enabled, sort_order, created_at, updated_at
		 FROM destinations WHERE id = ?`, id)
	dest, err := scanDestination(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDestinationNotFound
	}
	return dest, err
}

// scanner abstrae *sql.Row y *sql.Rows, que comparten la firma de Scan.
type scanner interface{ Scan(dest ...any) error }

func scanDestination(s scanner) (*Destination, error) {
	var (
		dest      Destination
		platform  string
		last4     string
		enabled   int
		createdAt string
		updatedAt string
	)
	if err := s.Scan(&dest.ID, &dest.Name, &platform, &dest.RTMPURL, &last4,
		&enabled, &dest.SortOrder, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("leer destino: %w", err)
	}

	dest.Platform = Platform(platform)
	dest.KeyMask = crypto.Secret(last4).Mask()
	dest.Enabled = enabled == 1

	var err error
	if dest.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return nil, fmt.Errorf("created_at inválido: %w", err)
	}
	if dest.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &dest, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
