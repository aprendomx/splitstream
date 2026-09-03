package httpapi

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testMaster(fill byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = fill
	}
	return k
}

func testSigner(t *testing.T, fill byte) *sessionSigner {
	t.Helper()
	s, err := newSessionSigner(testMaster(fill))
	if err != nil {
		t.Fatalf("newSessionSigner: %v", err)
	}
	return s
}

// TestSessionCookieRoundTrip: lo que se emite se verifica.
func TestSessionCookieRoundTrip(t *testing.T) {
	s := testSigner(t, 1)
	ahora := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	if err := s.verify(s.issue(ahora), ahora.Add(time.Hour)); err != nil {
		t.Errorf("una cookie recién emitida no verifica: %v", err)
	}
}

// TestSessionCookieRejectsTampering es el punto entero del HMAC: cambiar un solo byte, en
// el payload o en la firma, invalida la cookie.
func TestSessionCookieRejectsTampering(t *testing.T) {
	s := testSigner(t, 1)
	ahora := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	bueno := s.issue(ahora)

	partes := strings.Split(bueno, ".")
	if len(partes) != 3 {
		t.Fatalf("formato inesperado de la cookie: %q", bueno)
	}

	casos := []struct{ nombre, valor string }{
		{"caducidad estirada", partes[0] + "." + "99999999999" + "." + partes[2]},
		{"firma cambiada", partes[0] + "." + partes[1] + "." + flipLast(partes[2])},
		{"versión cambiada", "v2." + partes[1] + "." + partes[2]},
		{"sin firma", partes[0] + "." + partes[1]},
		{"caducidad no numérica", partes[0] + ".mañana." + partes[2]},
		{"vacía", ""},
		{"basura", "no-es-una-cookie"},
		{"solo puntos", ".."},
		{"partes de más", bueno + ".extra"},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if err := s.verify(c.valor, ahora); err == nil {
				t.Errorf("se aceptó una cookie manipulada: %q", c.valor)
			}
		})
	}
}

// TestSessionCookieExpires: pasada la vida útil, deja de valer aunque la firma sea buena.
func TestSessionCookieExpires(t *testing.T) {
	s := testSigner(t, 1)
	ahora := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	cookie := s.issue(ahora)

	if err := s.verify(cookie, ahora.Add(sessionTTL-time.Minute)); err != nil {
		t.Errorf("caducó antes de tiempo: %v", err)
	}
	err := s.verify(cookie, ahora.Add(sessionTTL+time.Minute))
	if !errors.Is(err, errCookieExpired) {
		t.Errorf("err = %v, quería errCookieExpired", err)
	}
}

// TestSessionCookieIsTiedToTheMasterKey: rotar la master key invalida todas las sesiones.
// Es el único mecanismo de revocación que tenemos, así que tiene que funcionar.
func TestSessionCookieIsTiedToTheMasterKey(t *testing.T) {
	ahora := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	cookie := testSigner(t, 1).issue(ahora)

	if err := testSigner(t, 2).verify(cookie, ahora); err == nil {
		t.Error("una cookie firmada con otra master key fue aceptada")
	}
}

// TestSessionKeyIsNotTheMasterKey: la clave de firma se deriva, no se reutiliza. Si un día
// alguien "simplifica" esto, el test lo para: un fallo en la firma de la cookie no debe
// ayudar a atacar el cifrado de las claves de destino.
func TestSessionKeyIsNotTheMasterKey(t *testing.T) {
	master := testMaster(7)
	s := testSigner(t, 7)

	if string(s.key) == string(master[:]) {
		t.Error("la clave de firma ES la master key; debe derivarse con HKDF")
	}
}

// TestExpiryIsCheckedAfterTheSignature: la caducidad la escribe quien manda la cookie, así
// que sin firma válida no significa nada. Una cookie caducada Y con la firma rota debe
// fallar por la firma, no por la caducidad.
func TestExpiryIsCheckedAfterTheSignature(t *testing.T) {
	s := testSigner(t, 1)
	ahora := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	partes := strings.Split(s.issue(ahora), ".")
	rota := partes[0] + "." + partes[1] + "." + flipLast(partes[2])

	err := s.verify(rota, ahora.Add(sessionTTL+time.Hour))
	if errors.Is(err, errCookieExpired) {
		t.Error("se comprobó la caducidad antes que la firma")
	}
	if !errors.Is(err, errCookieBadSignature) {
		t.Errorf("err = %v, quería errCookieBadSignature", err)
	}
}

// flipLast cambia el último carácter por otro distinto, para corromper una firma sin
// cambiar su longitud.
func flipLast(s string) string {
	if s == "" {
		return "x"
	}
	ultimo := s[len(s)-1]
	nuevo := byte('A')
	if ultimo == 'A' {
		nuevo = 'B'
	}
	return s[:len(s)-1] + string(nuevo)
}
