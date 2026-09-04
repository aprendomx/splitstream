package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func listaDestinos(t *testing.T, rec interface{ Bytes() []byte }) []destinationDTO {
	t.Helper()
	var out []destinationDTO
	if err := json.Unmarshal(rec.Bytes(), &out); err != nil {
		t.Fatalf("respuesta ilegible: %v — %s", err, rec.Bytes())
	}
	return out
}

func TestEncenderTodosLosCanales(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "A", "clave-1234", false)
	crearDest(t, db, srv, "B", "clave-5678", false)
	crearDest(t, db, srv, "C", "clave-9012", true)

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations/toggle-all", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body)
	}

	dtos := listaDestinos(t, rec.Body)
	if len(dtos) != 3 {
		t.Fatalf("destinos = %d, quería 3", len(dtos))
	}
	for _, d := range dtos {
		if !d.Enabled {
			t.Errorf("%q quedó apagado", d.Name)
		}
	}
}

func TestApagarTodosLosCanales(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "A", "clave-1234", true)
	crearDest(t, db, srv, "B", "clave-5678", true)

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations/toggle-all", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body)
	}
	for _, d := range listaDestinos(t, rec.Body) {
		if d.Enabled {
			t.Errorf("%q siguió encendido", d.Name)
		}
	}
}

// TestEncenderTodosEsIdempotente: el panel puede mandar esto dos veces por un doble clic.
// Lo observable es updated_at: si no cambió, no se escribió la fila.
func TestEncenderTodosEsIdempotente(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "A", "clave-1234", true)

	primera := listaDestinos(t, do(t, srv, cookies, http.MethodPost,
		"/api/destinations/toggle-all", `{"enabled":true}`).Body)
	segunda := listaDestinos(t, do(t, srv, cookies, http.MethodPost,
		"/api/destinations/toggle-all", `{"enabled":true}`).Body)

	if !primera[0].UpdatedAt.Equal(segunda[0].UpdatedAt) {
		t.Errorf("se reescribió una fila que ya estaba como se pedía: %v -> %v",
			primera[0].UpdatedAt, segunda[0].UpdatedAt)
	}
}

// TestEncenderTodosArrancaLosSinks: con una emisión en curso, encender un canal tiene que
// engancharlo en caliente, igual que hace el interruptor de una sola tarjeta.
func TestEncenderTodosArrancaLosSinks(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "A", "clave-1234", false)
	crearDest(t, db, srv, "B", "clave-5678", false)
	eng.setLive(1)

	if rec := do(t, srv, cookies, http.MethodPost,
		"/api/destinations/toggle-all", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body)
	}

	eng.mu.Lock()
	defer eng.mu.Unlock()
	if len(eng.added) != 2 {
		t.Errorf("sinks arrancados = %v, quería 2", eng.added)
	}
}

// TestApagarTodosSueltaLosSinks es la otra mitad: apagar corta lo que estaba emitiendo.
func TestApagarTodosSueltaLosSinks(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "A", "clave-1234", true)
	crearDest(t, db, srv, "B", "clave-5678", true)
	eng.setLive(1)

	do(t, srv, cookies, http.MethodPost, "/api/destinations/toggle-all", `{"enabled":false}`)

	eng.mu.Lock()
	defer eng.mu.Unlock()
	if len(eng.removed) != 2 {
		t.Errorf("sinks soltados = %v, quería 2", eng.removed)
	}
}

func TestToggleAllExigeElCampoEnabled(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations/toggle-all", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("código = %d, quería 400: %s", rec.Code, rec.Body)
	}
}

func TestToggleAllExigeSesion(t *testing.T) {
	srv, _, _, _, _ := newDestServer(t)

	rec := do(t, srv, nil, http.MethodPost, "/api/destinations/toggle-all", `{"enabled":true}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("código = %d, quería 401", rec.Code)
	}
}

// TestToggleAllSinDestinos: un panel recién instalado no debe dar error al pulsarlo.
func TestToggleAllSinDestinos(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations/toggle-all", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body)
	}
	if got := listaDestinos(t, rec.Body); len(got) != 0 {
		t.Errorf("destinos = %v, quería lista vacía", got)
	}
}
