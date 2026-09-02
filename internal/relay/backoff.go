package relay

import (
	"math/rand"
	"time"
)

// Límites del backoff de reconexión (spec §6.5).
const (
	BackoffMin = time.Second
	BackoffMax = 30 * time.Second

	// backoffJitter es la fracción de variación aleatoria, ±20%. Sin ella, varios
	// destinos que se caen a la vez —lo típico cuando el que falla es tu enlace y no la
	// plataforma— reintentarían en sincronía y volverían a saturarlo.
	backoffJitter = 0.2
)

// backoff calcula la espera entre reintentos: 1 s, 2 s, 4 s… hasta 30 s, con jitter.
//
// No es seguro para uso concurrente: lo usa la goroutine de un solo sink.
type backoff struct {
	attempt int
	rnd     *rand.Rand
}

// newBackoff construye un backoff con una semilla propia, para que dos destinos no
// generen la misma secuencia.
func newBackoff(seed int64) *backoff {
	return &backoff{rnd: rand.New(rand.NewSource(seed))}
}

// next devuelve la espera del siguiente intento y avanza el contador.
func (b *backoff) next() time.Duration {
	base := BackoffMin << uint(min(b.attempt, 30))
	if base > BackoffMax || base <= 0 {
		base = BackoffMax
	}
	b.attempt++

	// Jitter en [-20%, +20%].
	factor := 1 + backoffJitter*(2*b.rnd.Float64()-1)
	return time.Duration(float64(base) * factor)
}

// reset vuelve al principio de la secuencia. Se llama tras una conexión que aguantó.
func (b *backoff) reset() { b.attempt = 0 }

// attempts devuelve cuántos intentos van desde el último reset.
func (b *backoff) attempts() int { return b.attempt }
