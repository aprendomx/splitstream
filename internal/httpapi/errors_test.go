package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

// discardLogger es el logger de los tests del paquete. Va aquí y no en login_test.go
// porque este archivo se escribe antes.
func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestErrorResponseShape fija el contrato del spec §9: TODO error de la API tiene esta
// forma, y el frontend de la fase 5 va a depender de ella.
func TestErrorResponseShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, codeInvalidInput, "el nombre no puede estar vacío")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("código = %d, quería 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, quería application/json", ct)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("el cuerpo no es el JSON esperado: %v — %s", err, rec.Body.String())
	}
	if body.Error.Code != "invalid_input" {
		t.Errorf("code = %q", body.Error.Code)
	}
	if body.Error.Message != "el nombre no puede estar vacío" {
		t.Errorf("message = %q", body.Error.Message)
	}
}

// TestStoreErrorsMapToStatusCodes es la razón de ser de la Task 2: la API decide el código
// preguntando por la CLASE del error, no por su identidad ni por su texto.
func TestStoreErrorsMapToStatusCodes(t *testing.T) {
	casos := []struct {
		nombre string
		err    error
		status int
		code   string
	}{
		{"no encontrado", store.ErrDestinationNotFound, http.StatusNotFound, codeNotFound},
		{"sesión no encontrada", store.ErrSessionNotFound, http.StatusNotFound, codeNotFound},
		{"entrada inválida", store.ErrInvalidDestinationURL, http.StatusBadRequest, codeInvalidInput},
		{"conflicto", store.ErrSettingsNotInitialized, http.StatusConflict, codeConflict},
		{"envuelto en contexto", fmt.Errorf("crear destino: %w", store.ErrInvalidDestinationURL), http.StatusBadRequest, codeInvalidInput},
		{"desconocido", errors.New("se rompió el disco"), http.StatusInternalServerError, codeInternal},
	}

	s := &Server{logger: discardLogger()}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.writeStoreError(rec, c.err)

			if rec.Code != c.status {
				t.Errorf("código = %d, quería %d", rec.Code, c.status)
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("cuerpo: %v", err)
			}
			if body.Error.Code != c.code {
				t.Errorf("code = %q, quería %q", body.Error.Code, c.code)
			}
		})
	}
}

// TestClassifiedErrorMessagesAreReadable: el mensaje de un 400 o un 404 va tal cual al
// cliente, así que tiene que estar escrito para una persona. Es lo que motivó el tipo
// classified del store en vez de envolver con fmt.Errorf.
func TestClassifiedErrorMessagesAreReadable(t *testing.T) {
	s := &Server{logger: discardLogger()}

	rec := httptest.NewRecorder()
	s.writeStoreError(rec, store.ErrDestinationNotFound)

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("cuerpo: %v", err)
	}
	if body.Error.Message != "destino no encontrado" {
		t.Errorf("message = %q, quería \"destino no encontrado\"", body.Error.Message)
	}
}

// TestInternalErrorsDoNotLeakDetails: un 500 no le cuenta al cliente qué se rompió por
// dentro. El detalle va al log, que es donde lo lee quien opera el servicio.
func TestInternalErrorsDoNotLeakDetails(t *testing.T) {
	s := &Server{logger: discardLogger()}
	rec := httptest.NewRecorder()

	s.writeStoreError(rec, errors.New("no such file: /home/jadrian/secreto.db"))

	if body := rec.Body.String(); strings.Contains(body, "secreto.db") {
		t.Errorf("el 500 filtra detalles internos: %s", body)
	}
}
