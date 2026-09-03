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

// discardLogger está en errors_test.go, del mismo paquete.

const testPassword = "la-contraseña-de-prueba"

// newTestServer levanta un Server contra una base temporal, con la contraseña ya fijada.
func newTestServer(t *testing.T) (*Server, *store.DB) {
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
	hash, err := crypto.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := db.SetPasswordHash(ctx, hash); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}

	srv, err := New(Config{DB: db, Cipher: cipher, MasterKey: master, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, db
}

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// login hace el login y devuelve las cookies de la sesión.
func login(t *testing.T, srv *Server) []*http.Cookie {
	t.Helper()
	rec := postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"`+testPassword+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login: %d — %s", rec.Code, rec.Body.String())
	}
	return rec.Result().Cookies()
}

// TestLoginSetsAnHttpOnlyCookie: el spec §9 pide httpOnly explícitamente, porque es lo que
// impide que un XSS en el panel se lleve la sesión.
func TestLoginSetsAnHttpOnlyCookie(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"`+testPassword+`"}`)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("código = %d, quería 204: %s", rec.Code, rec.Body.String())
	}

	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no se puso la cookie de sesión")
	}
	if !cookie.HttpOnly {
		t.Error("la cookie no es httpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Error("la cookie no es SameSite=Lax")
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, quería /", cookie.Path)
	}
}

// TestLoginRejectsTheWrongPassword, y sin poner cookie.
func TestLoginRejectsTheWrongPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"la-equivocada"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("código = %d, quería 401", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			t.Error("se puso una cookie de sesión pese al fallo")
		}
	}
}

// TestLoginNeverEchoesThePassword (spec §8).
func TestLoginNeverEchoesThePassword(t *testing.T) {
	srv, _ := newTestServer(t)

	for _, pw := range []string{testPassword, "la-equivocada"} {
		rec := postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"`+pw+`"}`)
		if strings.Contains(rec.Body.String(), pw) {
			t.Errorf("la respuesta lleva la contraseña %q: %s", pw, rec.Body.String())
		}
	}
}

// TestProtectedEndpointsNeedASession recorre TODOS los endpoints protegidos: sin cookie,
// 401. Es la lista que hay que ampliar cuando las tasks 8, 9 y 10 añadan handlers, y la
// que impide añadir un endpoint olvidándose de protegerlo.
func TestProtectedEndpointsNeedASession(t *testing.T) {
	srv, _ := newTestServer(t)

	protegidos := []struct{ metodo, path string }{
		{http.MethodGet, "/api/ingest"},
		{http.MethodPost, "/api/ingest/rotate-key"},
		{http.MethodGet, "/api/destinations"},
		{http.MethodPost, "/api/destinations"},
		{http.MethodPatch, "/api/destinations/1"},
		{http.MethodDelete, "/api/destinations/1"},
		{http.MethodPost, "/api/destinations/1/toggle"},
		{http.MethodPost, "/api/destinations/reorder"},
		{http.MethodGet, "/api/destinations/1/key"},
		{http.MethodGet, "/api/status"},
		{http.MethodGet, "/api/events"},
		{http.MethodGet, "/ws"},
	}

	for _, p := range protegidos {
		t.Run(p.metodo+" "+p.path, func(t *testing.T) {
			req := httptest.NewRequest(p.metodo, p.path, strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("código = %d, quería 401: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestTamperedCookieIsRejected: una cookie con la firma cambiada no abre nada.
func TestTamperedCookieIsRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	cookies := login(t, srv)

	req := httptest.NewRequest(http.MethodGet, "/api/destinations", nil)
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			c.Value = flipLast(c.Value)
		}
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("código = %d, quería 401 con la cookie manipulada", rec.Code)
	}
}

// TestSessionCookieOpensTheDoor: el camino feliz completo, login y después una petición
// autenticada.
//
// De momento el endpoint devuelve 501 porque su handler llega en la Task 8; lo que este
// test comprueba HOY es que la sesión pasa el middleware, no que el endpoint funcione.
// Cuando la Task 8 lo implemente, esto pasa a exigir 200.
func TestSessionCookieOpensTheDoor(t *testing.T) {
	srv, _ := newTestServer(t)
	cookies := login(t, srv)

	req := httptest.NewRequest(http.MethodGet, "/api/destinations", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("la sesión no pasó el middleware: %s", rec.Body.String())
	}
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("código = %d; mientras el handler no exista se espera 501 — "+
			"si la Task 8 ya está hecha, sube este test a 200", rec.Code)
	}
}

// TestLogoutInvalidatesTheCookie: cerrar sesión tiene que dejarte fuera de verdad.
func TestLogoutInvalidatesTheCookie(t *testing.T) {
	srv, _ := newTestServer(t)
	cookies := login(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}

	var borrada *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			borrada = c
		}
	}
	if borrada == nil || borrada.MaxAge >= 0 {
		t.Error("el logout no manda al navegador borrar la cookie")
	}
}

// TestLoginIsRateLimited: sin esto, la contraseña del panel se puede probar a fuerza bruta
// tan rápido como aguante el argon2id, que es lento pero no infinitamente.
func TestLoginIsRateLimited(t *testing.T) {
	srv, _ := newTestServer(t)

	var limitado bool
	for i := 0; i < 30; i++ {
		rec := postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"la-equivocada"}`)
		if rec.Code == http.StatusTooManyRequests {
			limitado = true
			if got := errorCodeDe(t, rec); got != codeRateLimited {
				t.Errorf("code = %q, quería %q", got, codeRateLimited)
			}
			break
		}
	}
	if !limitado {
		t.Error("treinta intentos fallidos seguidos y ninguno fue limitado")
	}
}

// TestRateLimitDoesNotBlockTheRightPassword: el limitador no debe dejarte fuera de tu
// propio panel por equivocarte un par de veces al escribir.
func TestRateLimitDoesNotBlockTheRightPassword(t *testing.T) {
	srv, _ := newTestServer(t)

	for i := 0; i < 2; i++ {
		postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"la-equivocada"}`)
	}
	rec := postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"`+testPassword+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Errorf("código = %d, quería 204 tras dos fallos: %s", rec.Code, rec.Body.String())
	}
}

// TestLoginWithoutAPasswordConfigured: si nadie ejecutó -setpassword, el panel no está
// listo. 409 y no 401: la petición es correcta, el servicio no está configurado.
func TestLoginWithoutAPasswordConfigured(t *testing.T) {
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

	srv, err := New(Config{DB: db, Cipher: cipher, MasterKey: master, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := postJSON(t, srv.Handler(), "/api/auth/login", `{"password":"lo-que-sea"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("código = %d, quería 409: %s", rec.Code, rec.Body.String())
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("cuerpo: %v", err)
	}
	if !strings.Contains(body.Error.Message, "setpassword") {
		t.Errorf("el mensaje no dice cómo arreglarlo: %q", body.Error.Message)
	}
}

// TestLoginRejectsAMalformedBody
func TestLoginRejectsAMalformedBody(t *testing.T) {
	srv, _ := newTestServer(t)

	for _, body := range []string{`{"password":`, `no es json`, ``} {
		rec := postJSON(t, srv.Handler(), "/api/auth/login", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("con %q el código fue %d, quería 400", body, rec.Code)
		}
	}
}

// TestNewRejectsAnIncompleteConfig: construir el servidor sin base o sin cifrado es un
// error de programación, y vale más que salte al arrancar que en la primera petición.
func TestNewRejectsAnIncompleteConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("New aceptó una configuración vacía")
	}
}

func errorCodeDe(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decodificar error: %v — %s", err, rec.Body.String())
	}
	return b.Error.Code
}
