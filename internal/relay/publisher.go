package relay

import "context"

// Publisher es un destino de salida ya resuelto: sabe a dónde conectarse y con qué clave.
//
// Cada sink posee el suyo y lo usa desde una sola goroutine, así que las implementaciones
// NO necesitan ser seguras para uso concurrente. A cambio, deben tolerar que Close() se
// llame sin que Connect() haya tenido éxito.
type Publisher interface {
	// Connect abre la conexión y deja el stream listo para recibir media.
	Connect(ctx context.Context) error
	// WriteMeta envía el onMetaData. La implementación es responsable de envolverlo en
	// @setDataFrame (spec §3.5).
	WriteMeta(ts uint32, payload []byte) error
	WriteAudio(ts uint32, payload []byte) error
	WriteVideo(ts uint32, payload []byte) error
	// Close cierra ordenadamente. Es idempotente.
	Close() error
}
