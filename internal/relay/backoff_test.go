package relay

import (
	"testing"
	"time"
)

func TestBackoffGrowsExponentiallyAndCaps(t *testing.T) {
	b := newBackoff(1)

	var last time.Duration
	for i := 0; i < 12; i++ {
		d := b.next()
		if d < BackoffMin*8/10 {
			t.Fatalf("intento %d: %v está por debajo del mínimo con jitter", i, d)
		}
		if d > BackoffMax*12/10 {
			t.Fatalf("intento %d: %v supera el máximo con jitter", i, d)
		}
		last = d
	}
	// Tras 12 intentos debe estar pegado al techo.
	if last < BackoffMax*7/10 {
		t.Errorf("tras 12 intentos la espera es %v: debería estar cerca de %v", last, BackoffMax)
	}
}

func TestBackoffFirstAttemptIsAroundMin(t *testing.T) {
	b := newBackoff(1)
	d := b.next()
	if d < BackoffMin*8/10 || d > BackoffMin*12/10 {
		t.Errorf("primera espera = %v, quería ~%v con jitter de ±20%%", d, BackoffMin)
	}
}

func TestBackoffJitterVaries(t *testing.T) {
	// Dos backoffs con semillas distintas no deben dar la misma secuencia: si varios
	// destinos se caen a la vez, no pueden reintentar en sincronía (spec §6.5).
	a, b := newBackoff(1), newBackoff(2)
	same := 0
	for i := 0; i < 8; i++ {
		if a.next() == b.next() {
			same++
		}
	}
	if same == 8 {
		t.Error("dos backoffs con semillas distintas dieron la misma secuencia entera")
	}
}

func TestBackoffReset(t *testing.T) {
	b := newBackoff(1)
	for i := 0; i < 6; i++ {
		b.next()
	}
	if b.attempts() != 6 {
		t.Errorf("attempts = %d, quería 6", b.attempts())
	}

	b.reset()
	if b.attempts() != 0 {
		t.Errorf("attempts tras reset = %d, quería 0", b.attempts())
	}
	if d := b.next(); d > BackoffMin*12/10 {
		t.Errorf("tras reset la primera espera es %v, quería ~%v", d, BackoffMin)
	}
}
