// Package relay es el motor de retransmisión: reparte los mensajes del publisher a los
// destinos. No importa go-rtmp ni database/sql, así que se testea entero en memoria.
package relay

// Kind distingue las tres clases de mensaje que circulan por el hub.
type Kind uint8

const (
	KindAudio Kind = iota
	KindVideo
	KindMeta
)

// String da un nombre legible para logs y errores.
func (k Kind) String() string {
	switch k {
	case KindAudio:
		return "audio"
	case KindVideo:
		return "video"
	case KindMeta:
		return "meta"
	default:
		return "desconocido"
	}
}

// Message es un mensaje de media listo para reenviar.
//
// Payload es inmutable y se comparte entre todos los sinks: nadie debe escribir en él.
// No se usa un pool ni refcount a propósito — a 8 Mbps son ~1 MB/s de asignaciones, que
// el GC no nota. Si algún día se vuelve medible, el pool va detrás de esta misma forma.
type Message struct {
	Kind        Kind
	Timestamp   uint32
	Payload     []byte
	IsKeyframe  bool
	IsSeqHeader bool
}
