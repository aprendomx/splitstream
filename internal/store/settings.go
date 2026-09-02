package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
)

// ingestKeyBytes es la entropía de la clave de ingesta: 24 bytes → 32 caracteres
// en base64 seguro para URL, que es lo que se pega en OBS.
const ingestKeyBytes = 24

// ErrSettingsNotInitialized se devuelve cuando se opera sobre settings antes de Bootstrap.
var ErrSettingsNotInitialized = errors.New("settings no inicializado: falta Bootstrap")

// Settings es la configuración persistente. IngestKeyMask ya viene enmascarada: para
// obtener la clave real hay que llamar a RevealIngestKey de forma explícita.
type Settings struct {
	IngestApp     string
	IngestKeyMask string
	PasswordHash  string
	UpdatedAt     time.Time
}

// GenerateKey produce una credencial aleatoria segura para usar en una URL.
func GenerateKey() (crypto.Secret, error) {
	buf := make([]byte, ingestKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generar clave: %w", err)
	}
	return crypto.Secret(base64.RawURLEncoding.EncodeToString(buf)), nil
}

// Bootstrap deja la base lista para operar. Si no hay fila de settings, la crea con
// una clave de ingesta nueva y el key check value de c. Si ya existe, verifica que c
// sea la misma master key que la cifró y devuelve crypto.ErrWrongMasterKey si no.
func (d *DB) Bootstrap(ctx context.Context, c *crypto.Cipher) error {
	var kcv []byte
	err := d.db.QueryRowContext(ctx, `SELECT master_key_check FROM settings WHERE id = 1`).Scan(&kcv)
	switch {
	case err == nil:
		return c.VerifyCheckValue(kcv)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("leer settings: %w", err)
	}

	key, err := GenerateKey()
	if err != nil {
		return err
	}
	encrypted, err := c.Encrypt([]byte(key.Reveal()))
	if err != nil {
		return fmt.Errorf("cifrar clave de ingesta: %w", err)
	}
	newKCV, err := c.NewCheckValue()
	if err != nil {
		return fmt.Errorf("key check value: %w", err)
	}

	now := nowRFC3339()
	_, err = d.db.ExecContext(ctx,
		`INSERT INTO settings
		   (id, ingest_app, ingest_key_encrypted, ingest_key_last4, password_hash, master_key_check, created_at, updated_at)
		 VALUES (1, 'live', ?, ?, '', ?, ?, ?)`,
		encrypted, key.Last4(), newKCV, now, now)
	if err != nil {
		return fmt.Errorf("crear settings: %w", err)
	}
	return nil
}

// Settings devuelve la configuración persistente, con la clave de ingesta enmascarada.
func (d *DB) Settings(ctx context.Context) (*Settings, error) {
	var (
		s         Settings
		last4     string
		updatedAt string
	)
	err := d.db.QueryRowContext(ctx,
		`SELECT ingest_app, ingest_key_last4, password_hash, updated_at FROM settings WHERE id = 1`).
		Scan(&s.IngestApp, &last4, &s.PasswordHash, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("leer settings: %w", err)
	}
	// last4 viene ya truncado de la base, así que aquí no hay nada que filtrar.
	s.IngestKeyMask = maskFromLast4(last4)
	s.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &s, nil
}

// RevealIngestKey descifra y devuelve la clave de ingesta en claro.
func (d *DB) RevealIngestKey(ctx context.Context, c *crypto.Cipher) (crypto.Secret, error) {
	var blob []byte
	if err := d.db.QueryRowContext(ctx,
		`SELECT ingest_key_encrypted FROM settings WHERE id = 1`).Scan(&blob); err != nil {
		return "", fmt.Errorf("leer clave de ingesta: %w", err)
	}
	plain, err := c.Decrypt(blob)
	if err != nil {
		return "", fmt.Errorf("descifrar clave de ingesta: %w", err)
	}
	return crypto.Secret(plain), nil
}

// RotateIngestKey genera una clave nueva, la persiste y la devuelve en claro.
func (d *DB) RotateIngestKey(ctx context.Context, c *crypto.Cipher) (crypto.Secret, error) {
	key, err := GenerateKey()
	if err != nil {
		return "", err
	}
	encrypted, err := c.Encrypt([]byte(key.Reveal()))
	if err != nil {
		return "", fmt.Errorf("cifrar clave de ingesta: %w", err)
	}
	res, err := d.db.ExecContext(ctx,
		`UPDATE settings SET ingest_key_encrypted = ?, ingest_key_last4 = ?, updated_at = ? WHERE id = 1`,
		encrypted, key.Last4(), nowRFC3339())
	if err != nil {
		return "", fmt.Errorf("rotar clave de ingesta: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("rotar clave de ingesta: %w", err)
	}
	if n == 0 {
		return "", ErrSettingsNotInitialized
	}
	return key, nil
}

// SetPasswordHash guarda el hash argon2id de la contraseña del panel.
func (d *DB) SetPasswordHash(ctx context.Context, hash string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE settings SET password_hash = ?, updated_at = ? WHERE id = 1`,
		hash, nowRFC3339())
	if err != nil {
		return fmt.Errorf("guardar contraseña: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("guardar contraseña: %w", err)
	}
	if n == 0 {
		return ErrSettingsNotInitialized
	}
	return nil
}

// maskFromLast4 compone la máscara pública a partir de los 4 caracteres guardados,
// usando el mismo formato que crypto.Secret.Mask().
func maskFromLast4(last4 string) string { return crypto.Secret(last4).Mask() }

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }
