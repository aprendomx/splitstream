package httpapi

import (
	"github.com/coder/websocket"

	"github.com/aprendomx/splitstream/internal/relay"
)

// El protocolo de la vista previa (spec vista previa §4): mensajes binarios con 1 byte
// de tipo. La config lleva el avcC y los frames llevan los NALUs AVCC tal cual — el
// mismo formato que acepta VideoDecoder de WebCodecs, así que aquí no se parsea nada:
// se recorta la cabecera FLV y se reenvía.
const (
	previewMsgConfig byte = 0x01
	previewMsgFrame  byte = 0x02

	// flvVideoHeaderLen son los 5 bytes que preceden a los datos en un tag de vídeo AVC:
	// frameType/codecID, AVCPacketType y 3 de composition time.
	flvVideoHeaderLen = 5
)

// Códigos de cierre de aplicación: el cliente los enseña como motivo.
const (
	previewCloseEnded    websocket.StatusCode = 4000 // la emisión terminó
	previewCloseNoSignal websocket.StatusCode = 4001 // no hay emisión que enseñar
)

// previewConfigMsg construye el mensaje de config a partir del payload FLV del sequence
// header. Devuelve nil si el payload no da ni para la cabecera: sin avcC no hay nada que
// configurar.
func previewConfigMsg(flvPayload []byte) []byte {
	if len(flvPayload) <= flvVideoHeaderLen {
		return nil
	}
	out := make([]byte, 0, 1+len(flvPayload)-flvVideoHeaderLen)
	out = append(out, previewMsgConfig)
	return append(out, flvPayload[flvVideoHeaderLen:]...)
}

// previewFrameMsg construye el mensaje de un frame: flags, timestamp y NALUs. El
// timestamp viaja por si algún día hace falta; hoy el cliente pinta según decodifica.
func previewFrameMsg(m *relay.Message) []byte {
	if len(m.Payload) <= flvVideoHeaderLen {
		return nil
	}
	var flags byte
	if m.IsKeyframe {
		flags = 0x01
	}
	out := make([]byte, 0, 6+len(m.Payload)-flvVideoHeaderLen)
	out = append(out, previewMsgFrame, flags,
		byte(m.Timestamp>>24), byte(m.Timestamp>>16), byte(m.Timestamp>>8), byte(m.Timestamp))
	return append(out, m.Payload[flvVideoHeaderLen:]...)
}
