package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// /healthz es público: lo consulta un healthcheck de Docker o un monitor externo, que no
// tienen cookie. No revela nada más que "existe y la base responde".
func TestHealthzIsPublicAndSaysOK(t *testing.T) {
	srv, _ := newTestServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	var got healthDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" || got.DB != "ok" {
		t.Errorf("dto = %+v", got)
	}
	// Sin versión: eso solo va en el estado autenticado.
	var suelto map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &suelto)
	if _, hay := suelto["version"]; hay {
		t.Error("/healthz revela la versión; eso solo va en el estado autenticado")
	}
}

func TestHealthzDegradesWhenTheDatabaseIsGone(t *testing.T) {
	srv, db := newTestServer(t)
	db.Close()

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("código = %d, quería 503: %s", rec.Code, rec.Body.String())
	}
}
