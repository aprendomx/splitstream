package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

// sinConfigurar levanta un servicio recién instalado: base migrada, sin contraseña.
func sinConfigurar(t *testing.T, codigo string) *Server {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	master := testMaster(1)
	cipher, err := crypto.NewCipher(master)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	if err := db.Bootstrap(ctx, cipher); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	srv, err := New(Config{
		DB: db, Cipher: cipher, MasterKey: master,
		SetupCode: codigo, Logger: discardLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

// pedirDesde lanza una petición fingiendo venir de una dirección concreta. Es lo que
// distingue "estoy en el teclado de esta máquina" de "vengo de internet".
func pedirDesde(t *testing.T, srv *Server, remoto, metodo, ruta, cuerpo string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if cuerpo == "" {
		r = httptest.NewRequest(metodo, ruta, nil)
	} else {
		r = httptest.NewRequest(metodo, ruta, strings.NewReader(cuerpo))
		r.Header.Set("Content-Type", "application/json")
	}
	r.RemoteAddr = remoto
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

const (
	local  = "127.0.0.1:54321"
	remoto = "203.0.113.7:54321"
)

// TestSetupSaysWhenItIsNeeded: la interfaz consulta esto antes de tener sesión, para saber
// si enseñar el asistente o el login.
func TestSetupSaysWhenItIsNeeded(t *testing.T) {
	srv := sinConfigurar(t, "A7K2-9QMX-4RTZ")

	var e setupEstadoDTO
	rec := pedirDesde(t, srv, local, http.MethodGet, "/api/setup", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if !e.Necesario {
		t.Error("un servicio sin contraseña debería pedir configuración")
	}
	if e.PideCodigo {
		t.Error("desde la propia máquina no debería pedir código")
	}
	if !e.Local {
		t.Error("127.0.0.1 debería reconocerse como local")
	}

	// Desde fuera, el mismo servicio sí exige código.
	rec = pedirDesde(t, srv, remoto, http.MethodGet, "/api/setup", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if !e.PideCodigo || e.Local {
		t.Errorf("desde fuera debería pedir código: %+v", e)
	}
}

// TestSetupFromLocalhostNeedsNoCode: el caso del PC de escritorio, que es la mayoría. Quien
// está en el teclado ya controla el equipo; pedirle un código sería fricción sin ganancia.
func TestSetupFromLocalhostNeedsNoCode(t *testing.T) {
	srv := sinConfigurar(t, "A7K2-9QMX-4RTZ")

	rec := pedirDesde(t, srv, local, http.MethodPost, "/api/setup",
		`{"password":"mi-contraseña-nueva"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("código = %d, quería 204: %s", rec.Code, rec.Body.String())
	}

	// Y deja la sesión abierta: obligar a escribir otra vez la contraseña recién elegida
	// no aporta nada y rompe el hilo del asistente.
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Error("la configuración inicial no dejó sesión abierta")
	}
}

// TestSetupFromOutsideNeedsTheCode es la razón de ser de todo esto: sin código, el primero
// que cargue el panel de un VPS con el puerto abierto se quedaría con el servicio.
func TestSetupFromOutsideNeedsTheCode(t *testing.T) {
	srv := sinConfigurar(t, "A7K2-9QMX-4RTZ")

	casos := []struct{ nombre, cuerpo string }{
		{"sin código", `{"password":"mi-contraseña-nueva"}`},
		{"código vacío", `{"password":"mi-contraseña-nueva","codigo":""}`},
		{"código equivocado", `{"password":"mi-contraseña-nueva","codigo":"XXXX-XXXX-XXXX"}`},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			rec := pedirDesde(t, srv, remoto, http.MethodPost, "/api/setup", c.cuerpo)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("código = %d, quería 401: %s", rec.Code, rec.Body.String())
			}
		})
	}

	// Con el código correcto sí entra.
	rec := pedirDesde(t, srv, remoto, http.MethodPost, "/api/setup",
		`{"password":"mi-contraseña-nueva","codigo":"A7K2-9QMX-4RTZ"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("con el código correcto: código = %d, quería 204: %s", rec.Code, rec.Body.String())
	}
}

// TestSetupAcceptsTheCodeInLowercaseAndTrimmed: el código se lee de una consola y se teclea
// en otra pantalla, a veces desde el móvil. Rechazarlo por mayúsculas o por un espacio
// pegado sería castigar al usuario por algo que no importa.
func TestSetupAcceptsTheCodeInLowercaseAndTrimmed(t *testing.T) {
	srv := sinConfigurar(t, "A7K2-9QMX-4RTZ")

	rec := pedirDesde(t, srv, remoto, http.MethodPost, "/api/setup",
		`{"password":"mi-contraseña-nueva","codigo":"  a7k2-9qmx-4rtz  "}`)
	if rec.Code != http.StatusNoContent {
		t.Errorf("código = %d, quería 204: %s", rec.Code, rec.Body.String())
	}
}

// TestSetupClosesForeverOnceConfigured: es lo que impide que alguien reclame un servicio
// que ya tiene dueño. Ni siquiera desde la propia máquina.
func TestSetupClosesForeverOnceConfigured(t *testing.T) {
	srv := sinConfigurar(t, "A7K2-9QMX-4RTZ")

	if rec := pedirDesde(t, srv, local, http.MethodPost, "/api/setup",
		`{"password":"la-primera-contraseña"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("primera configuración: %d", rec.Code)
	}

	for _, desde := range []string{local, remoto} {
		rec := pedirDesde(t, srv, desde, http.MethodPost, "/api/setup",
			`{"password":"la-de-otro","codigo":"A7K2-9QMX-4RTZ"}`)
		if rec.Code != http.StatusConflict {
			t.Errorf("desde %s: código = %d, quería 409", desde, rec.Code)
		}
	}

	// Y el estado deja de pedir configuración.
	var e setupEstadoDTO
	rec := pedirDesde(t, srv, local, http.MethodGet, "/api/setup", "")
	json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Necesario {
		t.Error("sigue diciendo que hace falta configurar")
	}
}

// TestSetupRejectsAShortPassword: la misma regla que `splitstream -setpassword`.
func TestSetupRejectsAShortPassword(t *testing.T) {
	srv := sinConfigurar(t, "")

	rec := pedirDesde(t, srv, local, http.MethodPost, "/api/setup", `{"password":"corta"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("código = %d, quería 400: %s", rec.Code, rec.Body.String())
	}
}

// TestSetupWithoutACodeRefusesRemoteEvenIfNoneWasGenerated: un binario arrancado sin código
// no puede configurarse desde fuera. Lo contrario —permitirlo por no haber código— sería el
// agujero exacto que el código viene a tapar.
func TestSetupWithoutACodeRefusesRemote(t *testing.T) {
	srv := sinConfigurar(t, "")

	rec := pedirDesde(t, srv, remoto, http.MethodPost, "/api/setup",
		`{"password":"mi-contraseña-nueva"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("código = %d, quería 409: %s", rec.Code, rec.Body.String())
	}
	// Pero desde la propia máquina sigue funcionando.
	if rec := pedirDesde(t, srv, local, http.MethodPost, "/api/setup",
		`{"password":"mi-contraseña-nueva"}`); rec.Code != http.StatusNoContent {
		t.Errorf("en local: código = %d, quería 204", rec.Code)
	}
}

// TestSetupIsRateLimited: sin esto, un código de doce caracteres se prueba a fuerza bruta.
func TestSetupIsRateLimited(t *testing.T) {
	srv := sinConfigurar(t, "A7K2-9QMX-4RTZ")

	var limitado bool
	for i := 0; i < 30; i++ {
		rec := pedirDesde(t, srv, remoto, http.MethodPost, "/api/setup",
			`{"password":"mi-contraseña-nueva","codigo":"XXXX-XXXX-XXXX"}`)
		if rec.Code == http.StatusTooManyRequests {
			limitado = true
			break
		}
	}
	if !limitado {
		t.Error("treinta intentos de código y ninguno fue limitado")
	}
}

// TestSetupCodeIsReadableAndRandom: se lee de una consola y se teclea en otra pantalla, así
// que no puede llevar caracteres que se confundan entre sí.
func TestSetupCodeIsReadableAndRandom(t *testing.T) {
	a, err := GenerateSetupCode()
	if err != nil {
		t.Fatalf("GenerateSetupCode: %v", err)
	}
	b, err := GenerateSetupCode()
	if err != nil {
		t.Fatalf("GenerateSetupCode: %v", err)
	}
	if a == b {
		t.Error("dos códigos seguidos salieron iguales")
	}
	for _, c := range "ILO01" {
		if strings.ContainsRune(a, c) {
			t.Errorf("el código %q lleva %q, que se confunde al teclearlo", a, string(c))
		}
	}
	if strings.Count(a, "-") != 2 {
		t.Errorf("el código %q debería ir agrupado en tríos para poder leerlo", a)
	}
}

// TestSetupDoesNotLeakWhetherTheCodeIsClose: el mensaje de error no debe distinguir entre
// "código incorrecto" y "casi lo tienes".
func TestSetupDoesNotLeakWhetherTheCodeIsClose(t *testing.T) {
	srv := sinConfigurar(t, "A7K2-9QMX-4RTZ")

	casi := pedirDesde(t, srv, remoto, http.MethodPost, "/api/setup",
		`{"password":"mi-contraseña-nueva","codigo":"A7K2-9QMX-4RTY"}`)
	nada := pedirDesde(t, srv, remoto, http.MethodPost, "/api/setup",
		`{"password":"mi-contraseña-nueva","codigo":"ZZZZ-ZZZZ-ZZZZ"}`)

	if casi.Body.String() != nada.Body.String() {
		t.Errorf("el error distingue cuánto se acercó:\n  casi: %s\n  nada: %s",
			casi.Body.String(), nada.Body.String())
	}
}
