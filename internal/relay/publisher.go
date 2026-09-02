package relay

import "context"

// Publisher es un destino de salida ya resuelto: sabe a dónde conectarse y con qué clave.
//
// TODOS sus métodos, Close incluido, deben llamarse desde UNA SOLA goroutine. No es una
// preferencia: el ChunkStreamer de go-rtmp comparte un encoder sin mutex entre los chunk
// streams de audio y vídeo, y Close escribe un deleteStream por la misma conexión. El
// sink cumple el contrato porque difiere su Close en la misma goroutine que hace los
// Write; cualquier otro consumidor debe hacer lo mismo.
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
