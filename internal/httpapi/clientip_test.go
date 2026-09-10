package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func servidorConProxies(t *testing.T, cidrs ...string) *Server {
	t.Helper()
	s := &Server{}
	for _, c := range cidrs {
		s.proxies = append(s.proxies, netip.MustParsePrefix(c))
	}
	return s
}

func peticion(remota, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/setup", nil)
	r.RemoteAddr = remota
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// Sin proxies de confianza la cabecera se ignora: es la política de siempre, y la que
// protege a quien expone el puerto directamente.
func TestClientIPIgnoresForwardedForWithoutTrustedProxies(t *testing.T) {
	s := servidorConProxies(t)
	got := s.clientIP(peticion("127.0.0.1:5555", "203.0.113.9"))
	if got.String() != "127.0.0.1" {
		t.Errorf("clientIP = %s, quería 127.0.0.1", got)
	}
	if !s.esLocal(peticion("127.0.0.1:5555", "203.0.113.9")) {
		t.Error("loopback con cabecera inventada debería seguir siendo local sin proxies")
	}
}

func TestClientIPTrustsTheHeaderOnlyFromATrustedProxy(t *testing.T) {
	s := servidorConProxies(t, "127.0.0.1/32")
	r := peticion("127.0.0.1:5555", "203.0.113.9")
	if got := s.clientIP(r); got.String() != "203.0.113.9" {
		t.Errorf("clientIP = %s, quería 203.0.113.9", got)
	}
	if s.esLocal(r) {
		t.Error("una IP pública detrás del proxy no es local")
	}
	// Desde otra máquina, aunque traiga cabecera, no se le cree.
	if got := s.clientIP(peticion("198.51.100.4:1", "127.0.0.1")); got.String() != "198.51.100.4" {
		t.Errorf("clientIP = %s: la cabecera de un origen sin confianza se ignora", got)
	}
}

// Cadena de dos proxies: el último salto es de confianza, el anterior también, y la
// primera dirección que no lo es —de derecha a izquierda— es el cliente.
func TestClientIPWalksTheChainFromTheRight(t *testing.T) {
	s := servidorConProxies(t, "10.0.0.0/8", "127.0.0.1/32")
	got := s.clientIP(peticion("127.0.0.1:9", "198.51.100.7, 203.0.113.9, 10.1.2.3"))
	if got.String() != "203.0.113.9" {
		t.Errorf("clientIP = %s, quería 203.0.113.9 (198.51.100.7 lo puso el cliente y no vale)", got)
	}
	// Varias cabeceras X-Forwarded-For cuentan como una sola cadena.
	r := peticion("127.0.0.1:9", "")
	r.Header.Add("X-Forwarded-For", "203.0.113.9")
	r.Header.Add("X-Forwarded-For", "10.1.2.3")
	if got := s.clientIP(r); got.String() != "203.0.113.9" {
		t.Errorf("clientIP = %s con cabeceras repetidas", got)
	}
}

func TestClientIPFallsBackToTheProxyOnGarbageOrAllTrusted(t *testing.T) {
	s := servidorConProxies(t, "127.0.0.1/32")
	if got := s.clientIP(peticion("127.0.0.1:9", "no-es-una-ip")); got.String() != "127.0.0.1" {
		t.Errorf("basura en la cabecera: clientIP = %s, quería la del proxy", got)
	}
	if got := s.clientIP(peticion("127.0.0.1:9", "127.0.0.1")); got.String() != "127.0.0.1" {
		t.Errorf("todo de confianza: clientIP = %s, quería la del proxy", got)
	}
	if got := s.clientIP(peticion("127.0.0.1:9", "")); got.String() != "127.0.0.1" {
		t.Errorf("sin cabecera: clientIP = %s", got)
	}
}

func TestClientIPHandlesIPv6AndMappedAddresses(t *testing.T) {
	s := servidorConProxies(t, "::1/128")
	if got := s.clientIP(peticion("[::1]:9", "2001:db8::5")); got.String() != "2001:db8::5" {
		t.Errorf("clientIP = %s, quería 2001:db8::5", got)
	}
	// Una IPv4 mapeada en IPv6 se compara como IPv4.
	s = servidorConProxies(t, "127.0.0.1/32")
	if got := s.clientIP(peticion("[::ffff:127.0.0.1]:9", "203.0.113.9")); got.String() != "203.0.113.9" {
		t.Errorf("clientIP = %s con RemoteAddr mapeada", got)
	}
	if !s.esLocal(peticion("[::1]:9", "")) {
		t.Error("::1 es loopback")
	}
	// Un RemoteAddr que no parsea no es local ni de confianza.
	if s.esLocal(peticion("pipe", "")) {
		t.Error("un RemoteAddr sin IP no puede ser local")
	}
}
