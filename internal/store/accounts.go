package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
)

const (
	AccountStatusOK     = "ok"
	AccountStatusReauth = "reauth"
)

// Account es una cuenta de plataforma conectada. No tiene ningún campo de token, como
// Destination no tiene la clave: serializarla no puede filtrar nada. Los tokens se piden
// aparte con AccountTokens, y solo el tokens.Manager lo hace.
type Account struct {
	ID          int64
	Platform    Platform
	ExternalID  string
	DisplayName string
	Scopes      []string
	Status      string
	ExpiresAt   *time.Time
	OwnApp      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Tokens es el par de tokens en claro, solo en memoria y como Secret.
type Tokens struct {
	Access    crypto.Secret
	Refresh   crypto.Secret
	ExpiresAt time.Time
}

// NewAccount son los datos para crear o actualizar una cuenta.
type NewAccount struct {
	Platform    Platform
	ExternalID  string
	DisplayName string
	Scopes      []string
	Tokens      Tokens
}

// UpsertAccount crea la cuenta o, si ya existe la misma (platform, external_id), la
// actualiza con los tokens y el nombre nuevos y la devuelve a `ok`: reconectar es la forma
// de salir de `reauth`.
func (d *DB) UpsertAccount(ctx context.Context, c *crypto.Cipher, in NewAccount) (*Account, error) {
	switch in.Platform {
	case PlatformTwitch, PlatformYouTube, PlatformKick:
	default:
		return nil, invalidInput("plataforma sin cuentas: " + string(in.Platform))
	}
	if in.ExternalID == "" || in.DisplayName == "" {
		return nil, invalidInput("la cuenta necesita id y nombre")
	}
	if in.Tokens.Access.Reveal() == "" {
		return nil, invalidInput("la cuenta necesita un token de acceso")
	}
	access, refresh, expira, err := cifrarTokens(c, in.Tokens)
	if err != nil {
		return nil, err
	}
	ahora := nowRFC3339()
	scopes := strings.Join(in.Scopes, " ")
	var id int64
	err = d.InTx(ctx, func(tx *DB) error {
		if _, err := tx.ex.ExecContext(ctx,
			`INSERT INTO platform_accounts (platform, external_id, display_name, access_token_encrypted,
			    refresh_token_encrypted, expires_at, scopes, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 'ok', ?, ?)
			 ON CONFLICT (platform, external_id) DO UPDATE SET
			    display_name = excluded.display_name,
			    access_token_encrypted = excluded.access_token_encrypted,
			    refresh_token_encrypted = excluded.refresh_token_encrypted,
			    expires_at = excluded.expires_at,
			    scopes = excluded.scopes,
			    status = 'ok',
			    updated_at = excluded.updated_at`,
			string(in.Platform), in.ExternalID, in.DisplayName, access, refresh, expira, scopes, ahora, ahora); err != nil {
			return fmt.Errorf("guardar la cuenta: %w", err)
		}
		// Con ON CONFLICT DO UPDATE, LastInsertId no es fiable (ruling): se relee por la
		// clave única en vez de usar RETURNING.
		return tx.ex.QueryRowContext(ctx,
			`SELECT id FROM platform_accounts WHERE platform = ? AND external_id = ?`,
			string(in.Platform), in.ExternalID).Scan(&id)
	})
	if err != nil {
		return nil, err
	}
	return d.AccountByID(ctx, id)
}

func cifrarTokens(c *crypto.Cipher, t Tokens) (access, refresh []byte, expira any, err error) {
	if access, err = c.Encrypt([]byte(t.Access.Reveal())); err != nil {
		return nil, nil, nil, fmt.Errorf("cifrar el token: %w", err)
	}
	if t.Refresh.Reveal() != "" {
		if refresh, err = c.Encrypt([]byte(t.Refresh.Reveal())); err != nil {
			return nil, nil, nil, fmt.Errorf("cifrar el refresh token: %w", err)
		}
	}
	if !t.ExpiresAt.IsZero() {
		expira = formatTime(t.ExpiresAt)
	}
	return access, refresh, expira, nil
}

const accountCols = `id, platform, external_id, display_name, scopes, status, expires_at, own_app, created_at, updated_at`

// Accounts lista todas las cuentas, sin tokens.
func (d *DB) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := d.ex.QueryContext(ctx, `SELECT `+accountCols+` FROM platform_accounts ORDER BY platform, display_name, id`)
	if err != nil {
		return nil, fmt.Errorf("listar cuentas: %w", err)
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// AccountByID devuelve una cuenta por su id, sin tokens.
func (d *DB) AccountByID(ctx context.Context, id int64) (*Account, error) {
	a, err := scanAccount(d.ex.QueryRowContext(ctx, `SELECT `+accountCols+` FROM platform_accounts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("cuenta no encontrada")
	}
	return a, err
}

func scanAccount(s scanner) (*Account, error) {
	var (
		a                    Account
		platform, scopes     string
		expira               sql.NullString
		ownApp               int
		createdAt, updatedAt string
	)
	if err := s.Scan(&a.ID, &platform, &a.ExternalID, &a.DisplayName, &scopes, &a.Status, &expira, &ownApp, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("leer cuenta: %w", err)
	}
	a.Platform = Platform(platform)
	if scopes != "" {
		a.Scopes = strings.Split(scopes, " ")
	}
	a.OwnApp = ownApp == 1
	var err error
	if expira.Valid {
		t, err := time.Parse(time.RFC3339Nano, expira.String)
		if err != nil {
			return nil, fmt.Errorf("expires_at inválido: %w", err)
		}
		a.ExpiresAt = &t
	}
	if a.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return nil, fmt.Errorf("created_at inválido: %w", err)
	}
	if a.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &a, nil
}

// AccountTokens descifra los tokens. NO audita: el manager los lee a cada rato para
// llamar a la plataforma, igual que el motor lee la clave con DestinationKeyForRelay.
func (d *DB) AccountTokens(ctx context.Context, c *crypto.Cipher, id int64) (Tokens, error) {
	var (
		access, refresh []byte
		expira          sql.NullString
	)
	err := d.ex.QueryRowContext(ctx,
		`SELECT access_token_encrypted, refresh_token_encrypted, expires_at FROM platform_accounts WHERE id = ?`, id).
		Scan(&access, &refresh, &expira)
	if errors.Is(err, sql.ErrNoRows) {
		return Tokens{}, notFound("cuenta no encontrada")
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("leer los tokens: %w", err)
	}
	var t Tokens
	plain, err := c.Decrypt(access)
	if err != nil {
		return Tokens{}, fmt.Errorf("descifrar el token: %w", err)
	}
	t.Access = crypto.Secret(plain)
	if len(refresh) > 0 {
		plain, err := c.Decrypt(refresh)
		if err != nil {
			return Tokens{}, fmt.Errorf("descifrar el refresh token: %w", err)
		}
		t.Refresh = crypto.Secret(plain)
	}
	if expira.Valid {
		if t.ExpiresAt, err = time.Parse(time.RFC3339Nano, expira.String); err != nil {
			return Tokens{}, fmt.Errorf("expires_at inválido: %w", err)
		}
	}
	return t, nil
}

// SaveTokens guarda un par nuevo en una sola escritura. Con Twitch el refresh token rota
// y el usado deja de valer: si el proceso muriera entre refrescar y guardar, habría que
// reconectar; por eso el manager guarda antes de devolver el token.
func (d *DB) SaveTokens(ctx context.Context, c *crypto.Cipher, id int64, t Tokens) error {
	if t.Access.Reveal() == "" {
		return invalidInput("token de acceso vacío")
	}
	access, refresh, expira, err := cifrarTokens(c, t)
	if err != nil {
		return err
	}
	res, err := d.ex.ExecContext(ctx,
		`UPDATE platform_accounts SET access_token_encrypted = ?, refresh_token_encrypted = ?, expires_at = ?,
		    status = 'ok', updated_at = ? WHERE id = ?`, access, refresh, expira, nowRFC3339(), id)
	if err != nil {
		return fmt.Errorf("guardar los tokens: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("cuenta no encontrada")
	}
	return nil
}

// SetAccountStatus cambia el estado de la cuenta ('ok' o 'reauth'). El manager lo pone en
// reauth cuando el refresh falla con una credencial ya no válida.
func (d *DB) SetAccountStatus(ctx context.Context, id int64, status string) error {
	if status != AccountStatusOK && status != AccountStatusReauth {
		return invalidInput("estado de cuenta inválido: " + status)
	}
	res, err := d.ex.ExecContext(ctx, `UPDATE platform_accounts SET status = ?, updated_at = ? WHERE id = ?`, status, nowRFC3339(), id)
	if err != nil {
		return fmt.Errorf("cambiar el estado de la cuenta: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("cuenta no encontrada")
	}
	return nil
}

// DeleteAccount borra la cuenta y sus tokens; los enlaces caen por la clave ajena.
func (d *DB) DeleteAccount(ctx context.Context, id int64) error {
	res, err := d.ex.ExecContext(ctx, `DELETE FROM platform_accounts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("borrar la cuenta: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("cuenta no encontrada")
	}
	return nil
}

// LinkDestination vincula un destino a una cuenta de su misma plataforma; sustituye el
// enlace anterior si lo había.
func (d *DB) LinkDestination(ctx context.Context, destID, accountID int64) error {
	return d.InTx(ctx, func(tx *DB) error {
		dest, err := tx.destination(ctx, destID)
		if err != nil {
			return err
		}
		acct, err := tx.AccountByID(ctx, accountID)
		if err != nil {
			return err
		}
		if dest.Platform != acct.Platform {
			return invalidInput(fmt.Sprintf("la cuenta es de %s y el destino de %s", acct.Platform, dest.Platform))
		}
		if _, err := tx.ex.ExecContext(ctx,
			`INSERT INTO destination_accounts (destination_id, account_id) VALUES (?, ?)
			 ON CONFLICT (destination_id) DO UPDATE SET account_id = excluded.account_id`, destID, accountID); err != nil {
			return fmt.Errorf("vincular la cuenta: %w", err)
		}
		return nil
	})
}

// UnlinkDestination borra el enlace, si lo había. No es error desvincular un destino sin
// cuenta: es idempotente, como los DELETE del resto del paquete.
func (d *DB) UnlinkDestination(ctx context.Context, destID int64) error {
	if _, err := d.ex.ExecContext(ctx, `DELETE FROM destination_accounts WHERE destination_id = ?`, destID); err != nil {
		return fmt.Errorf("desvincular la cuenta: %w", err)
	}
	return nil
}

// AccountForDestination devuelve la cuenta vinculada, o ErrNotFound si no hay.
func (d *DB) AccountForDestination(ctx context.Context, destID int64) (*Account, error) {
	a, err := scanAccount(d.ex.QueryRowContext(ctx,
		`SELECT `+accountColsPrefixed("a")+` FROM platform_accounts a
		   JOIN destination_accounts da ON da.account_id = a.id WHERE da.destination_id = ?`, destID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("el destino no tiene cuenta vinculada")
	}
	return a, err
}

func accountColsPrefixed(p string) string {
	cols := strings.Split(accountCols, ", ")
	for i, c := range cols {
		cols[i] = p + "." + c
	}
	return strings.Join(cols, ", ")
}

// DestinationsOfAccount devuelve los ids de los destinos vinculados a la cuenta.
func (d *DB) DestinationsOfAccount(ctx context.Context, accountID int64) ([]int64, error) {
	rows, err := d.ex.QueryContext(ctx, `SELECT destination_id FROM destination_accounts WHERE account_id = ? ORDER BY destination_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("destinos de la cuenta: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
