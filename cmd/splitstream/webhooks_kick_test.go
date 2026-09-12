package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// kickProviderFalso implementa platforms.Provider + platforms.ChatWebhook (como el
// webhookFalso de internal/chat/aggregator_test.go) para ejercitar renovarWebhooksKick sin
// un proveedor de Kick de verdad. `fallar` marca, por DisplayName, qué cuentas debe
// rechazar SubscribeChat; `suscritas` acumula las que sí aceptó.
type kickProviderFalso struct {
	mu        sync.Mutex
	fallar    map[string]bool
	suscritas []string
}

func (p *kickProviderFalso) ID() platforms.ID { return platforms.Kick }
func (p *kickProviderFalso) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{ChatRead: true, RequiresPublicURL: true}
}
func (p *kickProviderFalso) Configured() bool { return true }
func (p *kickProviderFalso) BeginAuth(context.Context, platforms.Credentials) (platforms.AuthPrompt, error) {
	return platforms.AuthPrompt{}, nil
}
func (p *kickProviderFalso) PollAuth(context.Context, platforms.Credentials, platforms.AuthPrompt) (store.NewAccount, error) {
	return store.NewAccount{}, nil
}
func (p *kickProviderFalso) Refresh(context.Context, store.Account, platforms.Credentials, crypto.Secret) (store.Tokens, error) {
	return store.Tokens{}, nil
}
func (p *kickProviderFalso) Validate(context.Context, crypto.Secret) (platforms.Identity, error) {
	return platforms.Identity{}, nil
}
func (p *kickProviderFalso) SubscribeChat(_ context.Context, acct store.Account, _ crypto.Secret, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fallar[acct.DisplayName] {
		return errors.New("kick rechazó la suscripción")
	}
	p.suscritas = append(p.suscritas, acct.DisplayName)
	return nil
}
func (p *kickProviderFalso) UnsubscribeChat(context.Context, store.Account, crypto.Secret) error {
	return nil
}
func (p *kickProviderFalso) ParseWebhook(http.Header, []byte) ([]platforms.ChatMessage, error) {
	return nil, nil
}

// tokensFalso da un token fijo, salvo para las cuentas marcadas en sinToken (por id), a las
// que devuelve error: es lo que ejercita la rama "sin token" de renovarWebhooksKick sin
// necesitar un tokens.Manager ni cuentas cifradas de verdad.
type tokensFalso struct {
	sinToken map[int64]bool
}

func (t tokensFalso) Token(_ context.Context, accountID int64) (crypto.Secret, error) {
	if t.sinToken[accountID] {
		return "", errors.New("sin token")
	}
	return "tok", nil
}

func abrirDBKick(t *testing.T) (*store.DB, *crypto.Cipher) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kick.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var k [32]byte
	c, err := crypto.NewCipher(k)
	if err != nil {
		t.Fatal(err)
	}
	return db, c
}

// crearCuentaKick crea una cuenta de Kick en `ok` y, si enlazada es true, un destino
// habilitado vinculado a ella: la condición exacta que cuentasConChat (y aquí,
// renovarWebhooksKick) exige para contarla como candidata.
func crearCuentaKick(t *testing.T, db *store.DB, c *crypto.Cipher, externalID, nombre string, enlazada bool) store.Account {
	t.Helper()
	ctx := context.Background()
	acct, err := db.UpsertAccount(ctx, c, store.NewAccount{
		Platform: store.PlatformKick, ExternalID: externalID, DisplayName: nombre,
		Tokens: store.Tokens{Access: "a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if enlazada {
		d, err := db.CreateDestination(ctx, c, store.NewDestination{
			Name: nombre, Platform: store.PlatformKick, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.LinkDestination(ctx, d.ID, acct.ID); err != nil {
			t.Fatal(err)
		}
	}
	return *acct
}

func TestRenovarWebhooksKickSinPublicURLNoTocaElProveedor(t *testing.T) {
	db, c := abrirDBKick(t)
	crearCuentaKick(t, db, c, "1", "uno", true)

	p := &kickProviderFalso{}
	registro := platforms.NewRegistry(p)
	// logger nil a propósito: con publicURL vacía la función vuelve antes de tocarlo, así
	// que si esto panicara, sería la prueba de que el guard temprano no está donde debe.
	resumen, err := renovarWebhooksKick(context.Background(), db, registro, tokensFalso{}, "", nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resumen != "kick: sin suscripciones que renovar" {
		t.Errorf("resumen = %q, quería el mensaje de sin suscripciones", resumen)
	}
	if len(p.suscritas) != 0 {
		t.Errorf("se llamó a SubscribeChat sin publicURL: %v", p.suscritas)
	}
}

func TestRenovarWebhooksKickSinCuentasCandidatas(t *testing.T) {
	db, c := abrirDBKick(t)
	// Cuenta sin destino vinculado: existe pero no cuenta como candidata.
	crearCuentaKick(t, db, c, "1", "sin-destino", false)

	p := &kickProviderFalso{}
	registro := platforms.NewRegistry(p)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	resumen, err := renovarWebhooksKick(context.Background(), db, registro, tokensFalso{}, "https://relay.example", logger)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resumen != "kick: sin suscripciones que renovar" {
		t.Errorf("resumen = %q, quería el mensaje de sin suscripciones", resumen)
	}
	if len(p.suscritas) != 0 {
		t.Errorf("se llamó a SubscribeChat sin candidatas: %v", p.suscritas)
	}
}

func TestRenovarWebhooksKickUnaSuscribeOtraSinToken(t *testing.T) {
	db, c := abrirDBKick(t)
	ok := crearCuentaKick(t, db, c, "1", "con-token", true)
	sinTok := crearCuentaKick(t, db, c, "2", "sin-token", true)

	p := &kickProviderFalso{}
	registro := platforms.NewRegistry(p)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	tg := tokensFalso{sinToken: map[int64]bool{sinTok.ID: true}}

	resumen, err := renovarWebhooksKick(context.Background(), db, registro, tg, "https://relay.example/", logger)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resumen != "kick: 1 suscripciones renovadas" {
		t.Errorf("resumen = %q, quería 1 suscripción renovada", resumen)
	}
	if len(p.suscritas) != 1 || p.suscritas[0] != ok.DisplayName {
		t.Errorf("suscritas = %v, quería solo %q", p.suscritas, ok.DisplayName)
	}
	// El fallo de la cuenta sin token queda solo en el log, nunca en el resumen que se
	// registra como evento de mantenimiento.
	if !strings.Contains(buf.String(), "sin-token") || !strings.Contains(buf.String(), "sin token") {
		t.Errorf("el log no explica el fallo de la cuenta sin token: %s", buf.String())
	}
	if strings.Contains(resumen, "sin-token") {
		t.Errorf("el resumen no debería nombrar la cuenta que falló: %q", resumen)
	}
}

func TestRenovarWebhooksKickTodasFallan(t *testing.T) {
	db, c := abrirDBKick(t)
	a := crearCuentaKick(t, db, c, "1", "una", true)
	b := crearCuentaKick(t, db, c, "2", "otra", true)

	p := &kickProviderFalso{fallar: map[string]bool{a.DisplayName: true, b.DisplayName: true}}
	registro := platforms.NewRegistry(p)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	resumen, err := renovarWebhooksKick(context.Background(), db, registro, tokensFalso{}, "https://relay.example", logger)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resumen != "kick: 0 suscripciones renovadas" {
		t.Errorf("resumen = %q, quería 0 suscripciones renovadas", resumen)
	}
	if len(p.suscritas) != 0 {
		t.Errorf("suscritas = %v, quería ninguna", p.suscritas)
	}
}
