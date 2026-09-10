package store

// EventHook recibe cada evento que LogEvent persistió, con su ID y su CreatedAt ya
// asignados. Es el único punto por el que pasan TODOS los eventos —los del motor, los de
// los sinks, los de la API y la auditoría de claves— porque todos escriben por LogEvent.
//
// Se llama en la goroutine que escribió, después de que el INSERT tuviera éxito. Dentro de
// una transacción se llama antes del commit; se acepta porque el único camino transaccional
// (RevealDestinationKey) no puede fallar después del LogEvent.
//
// El hook NO debe bloquear: LogEvent lo llaman las goroutines de los sinks a mitad de
// transmisión. events.Bus cumple esa regla con entregas no bloqueantes.
type EventHook func(Event)

// SetEventHook fija el hook. nil lo quita. No es seguro llamarlo en concurrencia con
// LogEvent: se cablea una vez en el arranque.
func (d *DB) SetEventHook(h EventHook) { d.hook = h }
