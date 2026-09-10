// Package probe define el resultado de probar un destino sin emitir. Es un paquete de
// tipos sin dependencias para que la capa HTTP pueda hablar de sondas sin importar rtmpio
// —y con él go-rtmp—, que es una de las fronteras que vigila la CI.
package probe

import "time"

// Outcome es el veredicto de una sonda.
type Outcome uint8

const (
	// Unreachable: no se llegó a hablar RTMP (DNS, TCP o TLS).
	Unreachable Outcome = iota
	// Rejected: la URL no vale o la plataforma rechazó el handshake.
	Rejected
	// ClosedEarly: aceptó publish y cerró dentro de la gracia. Casi siempre es la clave.
	ClosedEarly
	// Plausible: aceptó publish y seguía abierta al terminar la gracia. No es "correcta":
	// solo emitir de verdad confirma la clave.
	Plausible
)

func (o Outcome) String() string {
	switch o {
	case Unreachable:
		return "unreachable"
	case Rejected:
		return "rejected"
	case ClosedEarly:
		return "closed_early"
	case Plausible:
		return "plausible"
	default:
		return "desconocido"
	}
}

// Result es lo que devuelve una sonda. Stage es la etapa en la que se decidió: url, dns,
// tcp, tls, connect, createStream, publish o grace. Err nunca contiene la URL ni la clave.
type Result struct {
	Outcome Outcome
	Stage   string
	Elapsed time.Duration
	Err     error
}
