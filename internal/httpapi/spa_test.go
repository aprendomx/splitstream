package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// panelDePrueba imita el dist compilado: un index y un asset con hash en el nombre.
func panelDePrueba() fs.FS {
	return fstest.MapFS{
		"index.html":             {Data: []byte("<!doctype html><div id=app></div>")},
		"assets/index-abc123.js": {Data: []byte("console.log(1)")},
	}
}

func servidorConPanel(t *testing.T) (*Server, []*http.Cookie) {
	t.Helper()
	srv, _ := newTestServer(t, func(c *Config) { c.SPA = panelDePrueba() })
	return srv, login(t, srv)
}

// TestSPAServesTheIndexAtRoot: lo mínimo, que la raíz devuelva el panel.
func TestSPAServesTheIndexAtRoot(t *testing.T) {
	srv, _ := servidorConPanel(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d, quería 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "id=app") {
		t.Errorf("no devolvió el index: %s", rec.Body.String())
	}
}

// TestSPAFallsBackToIndexForClientRoutes: el router del cliente usa URL limpias (spec §10),
// así que recargar en /ajustes tiene que devolver el index y no un 404.
func TestSPAFallsBackToIndexForClientRoutes(t *testing.T) {
	srv, _ := servidorConPanel(t)

	for _, ruta := range []string{"/ajustes", "/destinos/3", "/lo/que/sea"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ruta, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s: código = %d, quería 200", ruta, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "id=app") {
			t.Errorf("%s: no devolvió el index", ruta)
		}
	}
}

// TestSPADoesNotSwallowTheAPI es el riesgo real de montar el panel en la raíz: que
// /api/... acabe devolviendo el index y el frontend reciba HTML donde espera JSON. En el
// mux de Go 1.22 gana el patrón más específico, y este test lo fija.
func TestSPADoesNotSwallowTheAPI(t *testing.T) {
	srv, cookies := servidorConPanel(t)

	rec := do(t, srv, cookies, http.MethodGet, "/api/destinations", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d, quería 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, quería application/json: el panel se comió la API", ct)
	}

	// Y una ruta de API inexistente tampoco debe devolver el index.
	rec2 := do(t, srv, cookies, http.MethodGet, "/api/no-existe", "")
	if strings.Contains(rec2.Body.String(), "id=app") {
		t.Error("una ruta /api desconocida devolvió el panel en vez de un error")
	}
}

// TestSPADoesNotSwallowTheWebSocket: /ws sin sesión debe seguir dando 401, no el index.
func TestSPADoesNotSwallowTheWebSocket(t *testing.T) {
	srv, _ := servidorConPanel(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("código = %d, quería 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "id=app") {
		t.Error("/ws devolvió el panel")
	}
}

// TestSPACachesAssetsButNotTheIndex: los assets llevan hash en el nombre y se pueden
// cachear para siempre; el index no, porque es lo que apunta a los assets nuevos tras una
// actualización. Cachearlo dejaría al usuario con la versión vieja hasta que purgue.
func TestSPACachesAssetsButNotTheIndex(t *testing.T) {
	srv, _ := servidorConPanel(t)

	casos := []struct{ ruta, quiero string }{
		{"/assets/index-abc123.js", "public, max-age=31536000, immutable"},
		{"/", "no-cache"},
		{"/index.html", "no-cache"},
		{"/una/ruta/del/cliente", "no-cache"},
	}

	for _, c := range casos {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.ruta, nil))
		if got := rec.Header().Get("Cache-Control"); got != c.quiero {
			t.Errorf("%s: Cache-Control = %q, quería %q", c.ruta, got, c.quiero)
		}
	}
}

// TestSPADoesNotServeHTMLForMissingFiles: una ruta con extensión es un archivo que no
// existe, no una ruta del cliente. Devolver el index para /favicon.ico o para un .js cuyo
// hash cambió da HTML donde el navegador espera otra cosa, y el error de consola resultante
// no dice nada de lo que pasó.
func TestSPADoesNotServeHTMLForMissingFiles(t *testing.T) {
	srv, _ := servidorConPanel(t)

	for _, ruta := range []string{"/favicon.ico", "/assets/viejo-abc.js", "/algo.css"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ruta, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: código = %d, quería 404", ruta, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "id=app") {
			t.Errorf("%s: devolvió el panel en vez de un 404", ruta)
		}
	}

	// Pero una ruta del cliente sin extensión sigue cayendo al index.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ajustes/red", nil))
	if !strings.Contains(rec.Body.String(), "id=app") {
		t.Error("una ruta del cliente dejó de caer al index")
	}
}

// TestWithoutABuiltPanelTheAPIStillWorks: un binario sin frontend compilado debe seguir
// sirviendo la API y decir qué hacer, en vez de devolver una página en blanco.
func TestWithoutABuiltPanelTheAPIStillWorks(t *testing.T) {
	srv, db := newTestServer(t)
	_ = db
	cookies := login(t, srv)

	// La API va.
	if rec := do(t, srv, cookies, http.MethodGet, "/api/destinations", ""); rec.Code != http.StatusOK {
		t.Fatalf("la API no responde sin panel: %d", rec.Code)
	}

	// Y la raíz no explota: sin panel montado, el mux no tiene ruta y responde 404.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("código = %d, quería 404 sin panel compilado", rec.Code)
	}
}
