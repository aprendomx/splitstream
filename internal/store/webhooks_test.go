package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

func nuevoWebhook(nombre string) store.NewWebhook {
	return store.NewWebhook{
		Name: nombre, URL: "https://hooks.example.com/" + nombre, Format: store.WebhookJSON,
		Secret: crypto.Secret("secreto-de-" + nombre), MinLevel: store.LevelWarn, Enabled: true,
	}
}

func TestCreateAndListWebhooksNeverReturnTheSecret(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()

	w, err := db.CreateWebhook(ctx, c, nuevoWebhook("uno"))
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if !w.HasSecret || w.Format != store.WebhookJSON || w.MinLevel != store.LevelWarn || !w.Enabled {
		t.Errorf("webhook = %+v", w)
	}

	lista, err := db.ListWebhooks(ctx)
	if err != nil || len(lista) != 1 {
		t.Fatalf("ListWebhooks = %v, %v", lista, err)
	}
	// Recuperar el secreto exige pedirlo aparte y con el cipher.
	s, err := db.WebhookSecret(ctx, c, w.ID)
	if err != nil || s.Reveal() != "secreto-de-uno" {
		t.Errorf("WebhookSecret = %q, %v", s.Reveal(), err)
	}
}

// La firma no puede viajar en claro por internet: solo https, salvo loopback para probar.
func TestCreateWebhookValidatesTheURL(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()

	for _, url := range []string{"http://hooks.example.com/x", "ftp://x", "", "hooks.example.com"} {
		in := nuevoWebhook("x")
		in.URL = url
		if _, err := db.CreateWebhook(ctx, c, in); !errors.Is(err, store.ErrInvalidInput) {
			t.Errorf("URL %q aceptada (err = %v)", url, err)
		}
	}
	for _, url := range []string{"https://hooks.example.com/x", "http://localhost:9000/x", "http://127.0.0.1/x"} {
		in := nuevoWebhook("x")
		in.URL = url
		if _, err := db.CreateWebhook(ctx, c, in); err != nil {
			t.Errorf("URL %q rechazada: %v", url, err)
		}
	}
}

func TestCreateWebhookValidatesFormatLevelAndName(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()

	in := nuevoWebhook("x")
	in.Format = "telegram"
	if _, err := db.CreateWebhook(ctx, c, in); !errors.Is(err, store.ErrInvalidInput) {
		t.Error("formato desconocido aceptado")
	}
	in = nuevoWebhook("x")
	in.MinLevel = "fatal"
	if _, err := db.CreateWebhook(ctx, c, in); !errors.Is(err, store.ErrInvalidInput) {
		t.Error("nivel desconocido aceptado")
	}
	in = nuevoWebhook("x")
	in.Name = "  "
	if _, err := db.CreateWebhook(ctx, c, in); !errors.Is(err, store.ErrInvalidInput) {
		t.Error("nombre vacío aceptado")
	}
}

// Un secreto en discord o slack no tiene sentido: esas URL llevan su token. Se guarda
// igual —no hace daño— pero el formato decide si se firma. Aquí solo se comprueba que un
// secreto vacío deja HasSecret en false.
func TestWebhookWithoutSecret(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	in := nuevoWebhook("d")
	in.Format, in.Secret = store.WebhookDiscord, ""
	w, err := db.CreateWebhook(context.Background(), c, in)
	if err != nil {
		t.Fatal(err)
	}
	if w.HasSecret {
		t.Error("HasSecret = true sin secreto")
	}
	s, err := db.WebhookSecret(context.Background(), c, w.ID)
	if err != nil || s.Reveal() != "" {
		t.Errorf("WebhookSecret = %q, %v; quería vacío", s.Reveal(), err)
	}
}

func TestUpdateWebhookPatchesOnlyWhatCame(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()
	w, _ := db.CreateWebhook(ctx, c, nuevoWebhook("uno"))

	off := false
	nombre := "renombrado"
	got, err := db.UpdateWebhook(ctx, c, w.ID, store.WebhookPatch{Name: &nombre, Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renombrado" || got.Enabled || got.URL != w.URL || !got.HasSecret {
		t.Errorf("patch mal aplicado: %+v", got)
	}
	// Secreto vacío en el patch = quitarlo.
	vacio := crypto.Secret("")
	got, _ = db.UpdateWebhook(ctx, c, w.ID, store.WebhookPatch{Secret: &vacio})
	if got.HasSecret {
		t.Error("el secreto no se quitó")
	}
}

func TestDeleteWebhookAndNotFound(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()
	w, _ := db.CreateWebhook(ctx, c, nuevoWebhook("uno"))
	if err := db.DeleteWebhook(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteWebhook(ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("segundo borrado = %v, quería ErrNotFound", err)
	}
	if _, err := db.UpdateWebhook(ctx, c, w.ID, store.WebhookPatch{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update de inexistente = %v", err)
	}
}

func TestRecordWebhookDelivery(t *testing.T) {
	db := openTemp(t)
	c := cipherDePrueba(t)
	ctx := context.Background()
	w, _ := db.CreateWebhook(ctx, c, nuevoWebhook("uno"))

	if err := db.RecordWebhookDelivery(ctx, w.ID, 502, "bad gateway"); err != nil {
		t.Fatal(err)
	}
	lista, _ := db.ListWebhooks(ctx)
	if lista[0].LastStatus == nil || *lista[0].LastStatus != 502 || lista[0].LastError != "bad gateway" {
		t.Errorf("entrega no registrada: %+v", lista[0])
	}
	if err := db.RecordWebhookDelivery(ctx, w.ID, 200, ""); err != nil {
		t.Fatal(err)
	}
	lista, _ = db.ListWebhooks(ctx)
	if *lista[0].LastStatus != 200 || lista[0].LastError != "" {
		t.Errorf("entrega buena no borró el error: %+v", lista[0])
	}
	// Un error larguísimo se recorta: es para el panel, no un log.
	_ = db.RecordWebhookDelivery(ctx, w.ID, 0, strings.Repeat("x", 1000))
	lista, _ = db.ListWebhooks(ctx)
	if len(lista[0].LastError) > 200 {
		t.Errorf("last_error mide %d", len(lista[0].LastError))
	}
}
