package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP es la dirección desde la que llega la petición, tal como deben verla el
// limitador del login y el asistente del primer arranque.
//
// Por defecto es RemoteAddr y nada más: X-Forwarded-For la puede inventar quien llega
// directo, y entonces ni el límite limitaría ni «local» querría decir nada. Solo cuando
// RemoteAddr es uno de los proxies de confianza de la configuración se cree la cabecera,
// y aun así con cuidado: se recorre de derecha a izquierda —la derecha la escribió el
// proxy más cercano, la izquierda la trajo el cliente— saltando los proxies conocidos y
// quedándose con la primera dirección que no lo es. Si la cabecera trae basura, o todo
// lo que hay es de confianza, se devuelve la del proxy: es la opción conservadora, que
// nunca es local y agrupa en un solo cubo del limitador lo que venga mal formado.
func (s *Server) clientIP(r *http.Request) netip.Addr {
	remota := addrDe(r.RemoteAddr)
	if !remota.IsValid() || !s.esProxyDeConfianza(remota) {
		return remota
	}
	var cadena []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		cadena = append(cadena, strings.Split(v, ",")...)
	}
	for i := len(cadena) - 1; i >= 0; i-- {
		a := addrDe(strings.TrimSpace(cadena[i]))
		// Loopback y 0.0.0.0/:: nunca son una dirección de cliente legítima aquí: si el
		// proxy corre en la misma máquina, su propia IP loopback ya es de confianza y se
		// salta unas líneas más abajo. Que quede una en la cadena significa que la
		// escribió el cliente, y creerla volvería «local» —sin código del primer arranque
		// y con el limitador del login en otro cubo— a cualquiera que la mande.
		if !a.IsValid() || a.IsLoopback() || a.IsUnspecified() {
			return remota
		}
		if !s.esProxyDeConfianza(a) {
			return a
		}
	}
	return remota
}

// esLocal dice si la petición viene de la propia máquina. Es lo que decide si el
// asistente del primer arranque pide el código de la consola: quien está en el teclado ya
// controla el equipo; quien llega por la red tiene que demostrar que puede leerla.
func (s *Server) esLocal(r *http.Request) bool {
	return s.clientIP(r).IsLoopback()
}

func (s *Server) esProxyDeConfianza(a netip.Addr) bool {
	for _, p := range s.proxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// addrDe saca la IP de "host:puerto" o de una IP sola. Unmap para que ::ffff:127.0.0.1
// sea 127.0.0.1 y caiga en los prefijos IPv4.
func addrDe(s string) netip.Addr {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}
