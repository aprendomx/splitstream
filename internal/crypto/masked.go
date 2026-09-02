package crypto

import (
	"encoding/json"
	"log/slog"
)

// maskPrefix son los cuatro puntos medios que preceden a los últimos 4 caracteres.
const maskPrefix = "••••"

// Secret es una credencial en claro que se enmascara al formatearse o loguearse.
// Para obtener el valor real hay que llamar a Reveal() de forma explícita, lo que
// hace que una fuga accidental requiera escribirla a propósito.
type Secret string

// Reveal devuelve la credencial en claro. Úsalo solo al enviarla a su destino real.
func (s Secret) Reveal() string { return string(s) }

// Last4 devuelve los últimos 4 caracteres, o "" si el secreto es más corto.
// Se persiste junto al ciphertext para poder enmascarar sin la master key.
func (s Secret) Last4() string {
	r := []rune(string(s))
	if len(r) < 4 {
		return ""
	}
	return string(r[len(r)-4:])
}

// Mask devuelve la representación pública: "••••" más los últimos 4 caracteres.
func (s Secret) Mask() string { return maskPrefix + s.Last4() }

// String implementa fmt.Stringer con la máscara, para que %s, %v y %q no filtren.
func (s Secret) String() string { return s.Mask() }

// LogValue implementa slog.LogValuer con la máscara.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.Mask()) }

// MarshalJSON implementa json.Marshaler devolviendo la máscara. encoding/json ignora
// fmt.Stringer, así que sin este método un Secret embebido en una respuesta de la API
// saldría en claro por el cable. Deliberadamente NO se implementa UnmarshalJSON: la
// entrada sí acepta la clave en claro (es como se da de alta un destino), y solo la
// salida se enmascara.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Mask())
}
