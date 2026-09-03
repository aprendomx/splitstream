package store

import "errors"

// Las tres clases transversales de error del store.
//
// Existen para que la capa HTTP pueda decidir el código de respuesta sin conocer cada
// centinela concreto ni comparar cadenas (spec §15.3). Los centinelas específicos siguen
// existiendo y siguen siendo lo que usa el código del motor: estas clases se añaden por
// debajo, así que errors.Is sigue funcionando en ambos sentidos.
var (
	// ErrNotFound: la fila pedida no existe. La API responde 404.
	ErrNotFound = errors.New("no encontrado")

	// ErrInvalidInput: lo que llega no es aceptable, y reintentarlo igual tampoco lo
	// sería. La API responde 400.
	ErrInvalidInput = errors.New("entrada inválida")

	// ErrConflict: la entrada es válida pero choca con el estado actual. La API responde
	// 409.
	ErrConflict = errors.New("conflicto con el estado actual")
)

// classified es un error con mensaje propio que además pertenece a una clase.
//
// Existe porque `fmt.Errorf("%w: destino", ErrNotFound)` produce "no encontrado: destino",
// y ese texto va tal cual al cliente en las respuestas 400, 404 y 409. Separando el
// mensaje de la clase, el usuario lee "destino no encontrado" y la API sigue pudiendo
// clasificar con errors.Is.
type classified struct {
	msg   string
	class error
}

func (e *classified) Error() string { return e.msg }
func (e *classified) Unwrap() error { return e.class }

// notFound, invalidInput y conflict construyen un error con su mensaje y su clase.
func notFound(msg string) error     { return &classified{msg: msg, class: ErrNotFound} }
func invalidInput(msg string) error { return &classified{msg: msg, class: ErrInvalidInput} }
func conflict(msg string) error     { return &classified{msg: msg, class: ErrConflict} }
