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

// ErrWebhookNotFound se devuelve cuando el id no existe.
var ErrWebhookNotFound = notFound("webhook no encontrado")

// WebhookFormat es el conjunto cerrado de formatos de cuerpo. Duplica el CHECK del esquema
// para que el error llegue antes y legible.
type WebhookFormat string

const (
	WebhookJSON    WebhookFormat = "json"
	WebhookDiscord WebhookFormat = "discord"
	WebhookSlack   WebhookFormat = "slack"
)

func (f WebhookFormat) Valid() bool {
	switch f {
	case WebhookJSON, WebhookDiscord, WebhookSlack:
		return true
	}
	return false
}

// Webhook es un destino de avisos. No tiene campo para el secreto: hay que pedirlo aparte
// con WebhookSecret, de modo que serializar este struct nunca puede filtrarlo.
type Webhook struct {
	ID         int64
	Name       string
	URL        string
	Format     WebhookFormat
	HasSecret  bool
	MinLevel   Level
	Enabled    bool
	LastStatus *int
	LastError  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type NewWebhook struct {
	Name     string
	URL      string
	Format   WebhookFormat
	Secret   crypto.Secret
	MinLevel Level
	Enabled  bool
}

// WebhookPatch es una modificación parcial. Un Secret presente y vacío QUITA el secreto.
type WebhookPatch struct {
	Name     *string
	URL      *string
	Format   *WebhookFormat
	Secret   *crypto.Secret
	MinLevel *Level
	Enabled  *bool
}

// maxLastError acota lo que se guarda del último fallo: es para una línea del panel.
const maxLastError = 200

// validateWebhookURL exige https, salvo loopback para probar en local: la firma HMAC no
// debe viajar en claro por internet.
func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return invalidInput("URL de webhook inválida: falta el servidor")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		h := u.Hostname()
		if h == "localhost" || h == "127.0.0.1" || h == "::1" {
			return nil
		}
		return invalidInput("URL de webhook inválida: usa https:// (http solo vale hacia localhost)")
	default:
		return invalidInput(fmt.Sprintf("URL de webhook inválida: esquema %q, usa https://", u.Scheme))
	}
}

func validateWebhook(name string, format WebhookFormat, level Level) error {
	if strings.TrimSpace(name) == "" {
		return invalidInput("el nombre no puede estar vacío")
	}
	if !format.Valid() {
		return invalidInput(fmt.Sprintf("formato %q no soportado: json, discord o slack", format))
	}
	if !level.Valid() {
		return invalidInput(fmt.Sprintf("nivel %q no soportado: info, warn o error", level))
	}
	return nil
}

func (d *DB) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	rows, err := d.ex.QueryContext(ctx,
		`SELECT id, name, url, format, secret_encrypted IS NOT NULL, min_level, enabled,
		        last_status, last_error, created_at, updated_at
		   FROM webhooks ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listar webhooks: %w", err)
	}
	defer rows.Close()
	out := []Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar webhooks: %w", err)
	}
	return out, nil
}

func (d *DB) CreateWebhook(ctx context.Context, c *crypto.Cipher, in NewWebhook) (*Webhook, error) {
	if err := validateWebhook(in.Name, in.Format, in.MinLevel); err != nil {
		return nil, err
	}
	if err := validateWebhookURL(in.URL); err != nil {
		return nil, err
	}
	secret, err := encryptOptional(c, in.Secret)
	if err != nil {
		return nil, err
	}
	now := nowRFC3339()
	res, err := d.ex.ExecContext(ctx,
		`INSERT INTO webhooks (name, url, format, secret_encrypted, min_level, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Name, in.URL, string(in.Format), secret, string(in.MinLevel), boolToInt(in.Enabled), now, now)
	if err != nil {
		return nil, fmt.Errorf("crear webhook: %w", err)
	}
	id, _ := res.LastInsertId()
	return d.webhook(ctx, id)
}

func (d *DB) UpdateWebhook(ctx context.Context, c *crypto.Cipher, id int64, patch WebhookPatch) (*Webhook, error) {
	actual, err := d.webhook(ctx, id)
	if err != nil {
		return nil, err
	}
	name, format, level := actual.Name, actual.Format, actual.MinLevel
	if patch.Name != nil {
		name = *patch.Name
	}
	if patch.Format != nil {
		format = *patch.Format
	}
	if patch.MinLevel != nil {
		level = *patch.MinLevel
	}
	if err := validateWebhook(name, format, level); err != nil {
		return nil, err
	}
	sets := []string{"name = ?", "format = ?", "min_level = ?"}
	args := []any{name, string(format), string(level)}
	if patch.URL != nil {
		if err := validateWebhookURL(*patch.URL); err != nil {
			return nil, err
		}
		sets = append(sets, "url = ?")
		args = append(args, *patch.URL)
	}
	if patch.Secret != nil {
		secret, err := encryptOptional(c, *patch.Secret)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "secret_encrypted = ?")
		args = append(args, secret)
	}
	if patch.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, boolToInt(*patch.Enabled))
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, nowRFC3339(), id)
	if _, err := d.ex.ExecContext(ctx, "UPDATE webhooks SET "+joinComma(sets)+" WHERE id = ?", args...); err != nil {
		return nil, fmt.Errorf("actualizar webhook: %w", err)
	}
	return d.webhook(ctx, id)
}

func (d *DB) DeleteWebhook(ctx context.Context, id int64) error {
	res, err := d.ex.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("borrar webhook: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrWebhookNotFound
	}
	return nil
}

// WebhookSecret descifra el secreto, o devuelve "" si no hay. No audita: no es una
// divulgación a una persona, lo lee el despachador para firmar.
func (d *DB) WebhookSecret(ctx context.Context, c *crypto.Cipher, id int64) (crypto.Secret, error) {
	var blob []byte
	err := d.ex.QueryRowContext(ctx, `SELECT secret_encrypted FROM webhooks WHERE id = ?`, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrWebhookNotFound
	}
	if err != nil {
		return "", fmt.Errorf("leer el secreto del webhook: %w", err)
	}
	if blob == nil {
		return "", nil
	}
	plain, err := c.Decrypt(blob)
	if err != nil {
		return "", fmt.Errorf("descifrar el secreto del webhook: %w", err)
	}
	return crypto.Secret(plain), nil
}

// RecordWebhookDelivery guarda el resultado del último envío. status 0 significa que no
// hubo respuesta HTTP (red, timeout).
func (d *DB) RecordWebhookDelivery(ctx context.Context, id int64, status int, errMsg string) error {
	if len(errMsg) > maxLastError {
		errMsg = errMsg[:maxLastError]
	}
	var st *int
	if status != 0 {
		st = &status
	}
	if _, err := d.ex.ExecContext(ctx,
		`UPDATE webhooks SET last_status = ?, last_error = ? WHERE id = ?`, st, errMsg, id); err != nil {
		return fmt.Errorf("registrar entrega del webhook: %w", err)
	}
	return nil
}

func (d *DB) webhook(ctx context.Context, id int64) (*Webhook, error) {
	row := d.ex.QueryRowContext(ctx,
		`SELECT id, name, url, format, secret_encrypted IS NOT NULL, min_level, enabled,
		        last_status, last_error, created_at, updated_at
		   FROM webhooks WHERE id = ?`, id)
	w, err := scanWebhook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebhookNotFound
	}
	return w, err
}

func scanWebhook(s scanner) (*Webhook, error) {
	var (
		w                    Webhook
		format, level        string
		hasSecret, enabled   int
		createdAt, updatedAt string
	)
	if err := s.Scan(&w.ID, &w.Name, &w.URL, &format, &hasSecret, &level, &enabled,
		&w.LastStatus, &w.LastError, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("leer webhook: %w", err)
	}
	w.Format, w.MinLevel = WebhookFormat(format), Level(level)
	w.HasSecret, w.Enabled = hasSecret == 1, enabled == 1
	var err error
	if w.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return nil, fmt.Errorf("created_at inválido: %w", err)
	}
	if w.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &w, nil
}

// encryptOptional cifra un secreto, o devuelve nil (NULL en la base) si está vacío.
func encryptOptional(c *crypto.Cipher, s crypto.Secret) ([]byte, error) {
	if s.Reveal() == "" {
		return nil, nil
	}
	blob, err := c.Encrypt([]byte(s.Reveal()))
	if err != nil {
		return nil, fmt.Errorf("cifrar el secreto del webhook: %w", err)
	}
	return blob, nil
}
