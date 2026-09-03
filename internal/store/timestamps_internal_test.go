package store

import (
	"testing"
	"time"
)

// TestFormatTimeSortsLexicographically es el test que define el arreglo: dos instantes
// cuyo orden cronológico se conoce deben ordenarse igual como texto.
//
// Con time.RFC3339Nano esto falla, porque recorta los ceros finales de la fracción: el
// instante sin fracción produce una 'Z' donde el otro tiene un '.', y '.' (0x2E) es menor
// que 'Z' (0x5A), así que el posterior queda antes.
//
// Va en un test interno —el resto del paquete se prueba desde fuera— porque formatTime es
// privada y comprobar esta propiedad desde el exterior obligaría a insertar muchas filas
// y confiar en que alguna caiga con nanosegundos acabados en cero. Eso no es un test, es
// una apuesta.
func TestFormatTimeSortsLexicographically(t *testing.T) {
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

	casos := []struct {
		nombre         string
		antes, despues time.Time
	}{
		{"sin fracción contra media fracción", base, base.Add(500 * time.Millisecond)},
		{"media fracción contra un segundo", base.Add(500 * time.Millisecond), base.Add(time.Second)},
		{"un nanosegundo cuenta", base, base.Add(time.Nanosecond)},
		{"cruce de minuto", base.Add(59 * time.Second), base.Add(time.Minute)},
		{"cruce de día", base.Add(13*time.Hour + 59*time.Minute), base.Add(14 * time.Hour)},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			a, b := formatTime(c.antes), formatTime(c.despues)
			if !(a < b) {
				t.Errorf("orden de texto invertido:\n  antes   = %q\n  después = %q", a, b)
			}
		})
	}
}

// TestFormatTimeIsAlwaysTheSameWidth deja explícito el porqué: si todas las cadenas miden
// lo mismo, la comparación de texto no puede desalinearse.
func TestFormatTimeIsAlwaysTheSameWidth(t *testing.T) {
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	quiero := len(formatTime(base))

	for _, d := range []time.Duration{0, time.Nanosecond, time.Microsecond, time.Millisecond, 500 * time.Millisecond, time.Second} {
		if got := formatTime(base.Add(d)); len(got) != quiero {
			t.Errorf("%q mide %d, quería %d", got, len(got), quiero)
		}
	}
}

// TestFormatTimeNormalisesToUTC: el orden de texto solo coincide con el cronológico si
// todas las filas comparten huso. Un time.Time con zona debe salir en UTC.
func TestFormatTimeNormalisesToUTC(t *testing.T) {
	zona := time.FixedZone("CDT", -5*3600)
	conZona := time.Date(2026, 9, 2, 5, 0, 0, 0, zona)

	got := formatTime(conZona)
	if quiero := "2026-09-02T10:00:00.000000000Z"; got != quiero {
		t.Errorf("got %q, quería %q", got, quiero)
	}
}

// TestFormatTimeRoundTrips comprueba que lo escrito se vuelve a leer sin perder
// precisión: el parseo sigue usando RFC3339Nano, que acepta cualquier número de decimales.
func TestFormatTimeRoundTrips(t *testing.T) {
	quiero := time.Date(2026, 9, 2, 10, 0, 0, 123456789, time.UTC)

	got, err := time.Parse(time.RFC3339Nano, formatTime(quiero))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.Equal(quiero) {
		t.Errorf("got %v, quería %v", got, quiero)
	}
}
