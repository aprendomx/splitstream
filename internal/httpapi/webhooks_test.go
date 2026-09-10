package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

type fakeSender struct {
	enviados []store.Event
	err      error
}

func (f *fakeSender) Send(ctx context.Context, w store.Webhook, ev store.Event) error {
	f.enviados = append(f.enviados, ev)
	return f.err
}

func decodeWebhook(t *testing.T, body []byte) webhookDTO {
	t.Helper()
	var w webhookDTO
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("decodificar: %v — %s", err, body)
	}
	return w
}

func TestWebhookCRUDNeverReturnsTheSecret(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodPost, "/api/webhooks",
		`{"name":"discord","url":"https://discord.com/api/webhooks/1/abc","format":"json","secret":"s3cr3t-inconfundible","min_level":"warn","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("crear: %d — %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "inconfundible") {
		t.Error("la respuesta del alta lleva el secreto")
	}
	w := decodeWebhook(t, rec.Body.Bytes())
	if !w.HasSecret || w.MinLevel != "warn" {
		t.Errorf("dto = %+v", w)
	}

	rec = do(t, srv, cookies, http.MethodGet, "/api/webhooks", "")
	if strings.Contains(rec.Body.String(), "inconfundible") {
		t.Error("el listado lleva el secreto")
	}

	rec = do(t, srv, cookies, http.MethodPatch, "/api/webhooks/"+itoa(w.ID), `{"enabled":false,"name":"apagado"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}
	if got := decodeWebhook(t, rec.Body.Bytes()); got.Enabled || got.Name != "apagado" {
		t.Errorf("patch mal aplicado: %+v", got)
	}

	if rec = do(t, srv, cookies, http.MethodDelete, "/api/webhooks/"+itoa(w.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec = do(t, srv, cookies, http.MethodDelete, "/api/webhooks/"+itoa(w.ID), ""); rec.Code != http.StatusNotFound {
		t.Errorf("segundo delete: %d, quería 404", rec.Code)
	}
}

func TestWebhookCreateValidates(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	rec := do(t, srv, cookies, http.MethodPost, "/api/webhooks",
		`{"name":"x","url":"http://hooks.example.com/x","format":"json","min_level":"warn"}`)
	if rec.Code != http.StatusBadRequest || errorCodeDe(t, rec) != codeInvalidInput {
		t.Errorf("código = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookTestSendsASyntheticEvent(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	fs := &fakeSender{}
	srv.webhooks = fs
	rec := do(t, srv, cookies, http.MethodPost, "/api/webhooks",
		`{"name":"x","url":"https://hooks.example.com/x","format":"slack","min_level":"info","enabled":true}`)
	w := decodeWebhook(t, rec.Body.Bytes())

	rec = do(t, srv, cookies, http.MethodPost, "/api/webhooks/"+itoa(w.ID)+"/test", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("test: %d — %s", rec.Code, rec.Body.String())
	}
	if len(fs.enviados) != 1 || fs.enviados[0].Kind != "webhook_test" {
		t.Errorf("enviados = %+v", fs.enviados)
	}
}

func TestWebhookTestReportsAFailedDelivery(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.webhooks = &fakeSender{err: context.DeadlineExceeded}
	rec := do(t, srv, cookies, http.MethodPost, "/api/webhooks",
		`{"name":"x","url":"https://hooks.example.com/x","format":"json","min_level":"info","enabled":true}`)
	w := decodeWebhook(t, rec.Body.Bytes())

	rec = do(t, srv, cookies, http.MethodPost, "/api/webhooks/"+itoa(w.ID)+"/test", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("código = %d, quería 502: %s", rec.Code, rec.Body.String())
	}
	// No es un conflicto de estado: el webhook está bien configurado y quien falló fue el
	// otro extremo.
	if got := errorCodeDe(t, rec); got != codeInternal {
		t.Errorf("code = %q, quería %q", got, codeInternal)
	}
}
