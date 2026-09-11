package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
)

// ErrDestinationNotFound se devuelve cuando el id no existe.
var ErrDestinationNotFound = notFound("destino no encontrado")

// ErrInvalidDestinationURL indica que la URL del destino no sirve para retransmitir.
var ErrInvalidDestinationURL = invalidInput("URL de destino inválida")

// validateRTMPURL comprueba que la URL sea rtmp:// o rtmps:// y tenga host y app.
//
// Duplica a propósito parte de lo que hace rtmpio.parseTarget: que internal/store
// importara internal/rtmpio invertiría las capas. El esquema decide si la conexión usa
// TLS (spec §3.1), así que persistir otra cosa deja un destino que nunca podrá publicar.
func validateRTMPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: no se pudo interpretar", ErrInvalidDestinationURL)
	}
	if u.Scheme != "rtmp" && u.Scheme != "rtmps" {
		return fmt.Errorf("%w: esquema %q, usa rtmp:// o rtmps://", ErrInvalidDestinationURL, u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%w: falta el host", ErrInvalidDestinationURL)
	}
	if strings.Trim(u.Path, "/") == "" {
		return fmt.Errorf("%w: falta la app tras el host", ErrInvalidDestinationURL)
	}
	return nil
}

// validateName rechaza el nombre vacío o de solo espacios. El esquema no lo impide y sin
// esto el destino aparecería en la interfaz como una fila sin etiqueta.
func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return invalidInput("el nombre no puede estar vacío")
	}
	return nil
}

// validatePlatform delega en Platform.Valid, que ya conoce el conjunto cerrado, y le pone
// la clase de error encima: sin ella la API no podría distinguir esto de un fallo de disco.
func validatePlatform(p Platform) error {
	if !p.Valid() {
		return invalidInput(fmt.Sprintf("plataforma %q no soportada", p))
	}
	return nil
}

// validateKey rechaza la clave vacía. Un destino sin clave conecta y la plataforma lo
// rechaza en la primera escritura, que es el modo de fallo más confuso que tenemos: el
// sink queda reintentando contra un destino que nunca lo va a aceptar.
func validateKey(k crypto.Secret) error {
	if k.Reveal() == "" {
		return invalidInput("la clave no puede estar vacía")
	}
	return nil
}

// Platform es el conjunto cerrado de plataformas soportadas. Duplica el CHECK del
// esquema a propósito: así el error llega antes y con un mensaje legible.
type Platform string

const (
	PlatformYouTube  Platform = "youtube"
	PlatformTwitch   Platform = "twitch"
	PlatformFacebook Platform = "facebook"
	PlatformKick     Platform = "kick"
	PlatformX        Platform = "x"
	// PlatformTikTok se añadió en la migración 0003. A diferencia de las demás, TikTok
	// emite el servidor de ingesta POR EMISIÓN desde su Live Center, así que su preset no
	// puede precargar una URL: hay que pegar servidor y clave.
	PlatformTikTok Platform = "tiktok"
	PlatformCustom Platform = "custom"
)

// Valid indica si p es una de las plataformas soportadas.
func (p Platform) Valid() bool {
	switch p {
	case PlatformYouTube, PlatformTwitch, PlatformFacebook, PlatformKick, PlatformX,
		PlatformTikTok, PlatformCustom:
		return true
	}
	return false
}

// Destination es un destino de retransmisión. No tiene campo para la clave: hay que
// pedirla aparte, con DestinationKeyForRelay o con RevealDestinationKey, de modo que
// serializar este struct nunca puede filtrarla.
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
	// AccountID es el id de la cuenta vinculada, nil sin cuenta; sale del JOIN con
	// destination_accounts para que el DTO no haga una consulta más.
	AccountID *int64
	// KeyFromAPI es true cuando la emisión vinculada trajo la clave desde la API de la
	// plataforma (YouTube): «probar destino» se salta, porque no hay clave inválida que
	// probar. Sale del JOIN con destination_broadcasts.
	KeyFromAPI bool
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
		`SELECT d.id, d.name, d.platform, d.rtmp_url, d.stream_key_last4, d.enabled, d.sort_order, d.created_at, d.updated_at, da.account_id, COALESCE(br.key_from_api, 0)
		 FROM destinations d LEFT JOIN destination_accounts da ON da.destination_id = d.id
		   LEFT JOIN destination_broadcasts br ON br.destination_id = d.id
		 ORDER BY d.sort_order, d.id`)
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
	if err := validateName(in.Name); err != nil {
		return nil, err
	}
	if err := validatePlatform(in.Platform); err != nil {
		return nil, err
	}
	if err := validateRTMPURL(in.RTMPURL); err != nil {
		return nil, err
	}
	if err := validateKey(in.Key); err != nil {
		return nil, err
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
	actual, err := d.destination(ctx, id)
	if err != nil {
		return nil, err
	}

	sets := []string{}
	args := []any{}

	if patch.Name != nil {
		if err := validateName(*patch.Name); err != nil {
			return nil, err
		}
		sets = append(sets, "name = ?")
		args = append(args, *patch.Name)
	}
	if patch.Platform != nil {
		if err := validatePlatform(*patch.Platform); err != nil {
			return nil, err
		}
		sets = append(sets, "platform = ?")
		args = append(args, string(*patch.Platform))
	}
	if patch.RTMPURL != nil {
		if err := validateRTMPURL(*patch.RTMPURL); err != nil {
			return nil, err
		}
		sets = append(sets, "rtmp_url = ?")
		args = append(args, *patch.RTMPURL)
	}
	if patch.Key != nil {
		if err := validateKey(*patch.Key); err != nil {
			return nil, err
		}
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

	// Cambiar de plataforma invalida el enlace con la cuenta: una cuenta de Twitch no
	// puede quedar colgando de un destino de YouTube (el título en vivo y el chat irían
	// a la plataforma equivocada). Se borra el enlace y el panel vuelve a pedir la cuenta.
	cambiaPlataforma := patch.Platform != nil && *patch.Platform != actual.Platform

	aplicar := func(tx *DB) error {
		if len(sets) > 0 {
			query := "UPDATE destinations SET " + joinComma(sets) + " WHERE id = ?"
			if _, err := tx.ex.ExecContext(ctx, query, args...); err != nil {
				return fmt.Errorf("actualizar destino: %w", err)
			}
		}
		if cambiaPlataforma {
			if _, err := tx.ex.ExecContext(ctx,
				`DELETE FROM destination_accounts WHERE destination_id = ?`, id); err != nil {
				return fmt.Errorf("desvincular la cuenta al cambiar de plataforma: %w", err)
			}
		}
		return nil
	}

	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, nowRFC3339(), id)
	}
	// El UPDATE y el borrado del enlace van en la misma transacción: si se partieran, un
	// fallo dejaría el destino con la plataforma nueva y la cuenta vieja aún enlazada.
	// Si ya venimos dentro de una transacción (ToggleAll) se reutiliza: InTx no se anida.
	if _, enTx := d.ex.(*sql.Tx); enTx {
		err = aplicar(d)
	} else {
		err = d.InTx(ctx, aplicar)
	}
	if err != nil {
		return nil, err
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

// DestinationKeyForRelay descifra la clave del destino para que el motor pueda construir
// su sink. NO audita: el motor la lee en cada sesión y por cada destino, así que auditar
// aquí llenaría el log de ruido y taparía justo lo que la auditoría hace visible.
//
// No es una divulgación a una persona; la clave no sale del proceso. El camino que sí lo
// es —y que sí audita— es RevealDestinationKey.
func (d *DB) DestinationKeyForRelay(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error) {
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

// RevealDestinationKey descifra la clave y deja constancia de que alguien la pidió.
//
// El evento se escribe en la MISMA transacción que la lectura, así que no existe un camino
// que revele sin auditar: o pasan las dos cosas o no pasa ninguna (spec §15.5). Antes esto
// era un comentario pidiendo al llamante que registrara el evento, y un comentario no es
// un invariante.
//
// InTx no se puede anidar (ErrNestedTransaction), así que esto NO se puede llamar desde
// dentro de otra transacción. El motor no lo hace: usa DestinationKeyForRelay.
func (d *DB) RevealDestinationKey(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error) {
	var key crypto.Secret
	err := d.InTx(ctx, func(tx *DB) error {
		k, err := tx.DestinationKeyForRelay(ctx, c, id)
		if err != nil {
			return err
		}
		// El mensaje NO lleva la clave, ni siquiera enmascarada (spec §8). El destino se
		// identifica por su id, que es lo que hace falta para investigar.
		if _, err := tx.LogEvent(ctx, Event{
			DestinationID: &id,
			Level:         LevelWarn,
			Kind:          "key_revealed",
			Message:       "se reveló la clave del destino",
		}); err != nil {
			return err
		}
		key = k
		return nil
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

// DestinationByID devuelve un destino por su id, sin la clave.
func (d *DB) DestinationByID(ctx context.Context, id int64) (*Destination, error) {
	return d.destination(ctx, id)
}

func (d *DB) destination(ctx context.Context, id int64) (*Destination, error) {
	row := d.ex.QueryRowContext(ctx,
		`SELECT d.id, d.name, d.platform, d.rtmp_url, d.stream_key_last4, d.enabled, d.sort_order, d.created_at, d.updated_at, da.account_id, COALESCE(br.key_from_api, 0)
		 FROM destinations d LEFT JOIN destination_accounts da ON da.destination_id = d.id
		   LEFT JOIN destination_broadcasts br ON br.destination_id = d.id
		 WHERE d.id = ?`, id)
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
		dest       Destination
		platform   string
		last4      string
		enabled    int
		createdAt  string
		updatedAt  string
		acct       sql.NullInt64
		keyFromAPI int
	)
	if err := s.Scan(&dest.ID, &dest.Name, &platform, &dest.RTMPURL, &last4,
		&enabled, &dest.SortOrder, &createdAt, &updatedAt, &acct, &keyFromAPI); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("leer destino: %w", err)
	}

	dest.Platform = Platform(platform)
	dest.KeyMask = crypto.Secret(last4).Mask()
	dest.Enabled = enabled == 1
	dest.KeyFromAPI = keyFromAPI == 1
	if acct.Valid {
		v := acct.Int64
		dest.AccountID = &v
	}

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
