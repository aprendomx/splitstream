package httpapi

import (
	"context"
	"net/http"

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

// handlePreviewWS es la vista previa silenciada (spec vista previa §5): abre un tap del
// hub y reenvía la config y cada frame de vídeo por mensajes binarios. La sesión y el
// Origin se comprueban igual que en handleWS: el handshake es HTTP normal con cookie.
func (s *Server) handlePreviewWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.logger.Warn("no se pudo abrir el WebSocket de la vista previa", "err", err)
		return
	}
	defer conn.CloseNow()

	// El tap se abre ANTES de leer VideoConfig(): si se leyera antes, un sequence header
	// publicado justo en ese hueco se perdería (el tap aún no existe para recibirlo) y el
	// cliente se quedaría con la config vieja. Abierto primero, lo peor que puede pasar es
	// una config duplicada —VideoConfig() y luego el mismo seq header por el tap—, que el
	// cliente ya tolera. Si no hay señal, release() (vía defer) cierra el tap enseguida.
	ch, release := s.engine.Tap()
	defer release()

	// Sin emisión (o sin sequence header todavía) no hay nada que enseñar. Se cierra con
	// un código de aplicación en vez de esperar: el botón del panel solo se habilita con
	// ingesta viva, así que llegar aquí sin señal es la carrera de pulsar justo cuando se
	// corta — y la respuesta honesta es decirlo, no colgarse a esperar.
	cfg := previewConfigMsg(s.engine.VideoConfig())
	if s.engine.Session().ID == 0 || cfg == nil {
		conn.Close(previewCloseNoSignal, "sin señal")
		return
	}

	// El cliente nunca manda mensajes: es un protocolo de solo push. CloseRead deja el
	// socket leyendo en segundo plano —así responde a los ping/pong y a la trama de
	// cierre— y cancela este contexto en cuanto el cliente se va. Sin esto, un cliente que
	// se marcha sin que llegue ningún frame nuevo se queda en el select para siempre: el
	// contexto de la petición no se cancela solo porque el otro lado cerró el socket.
	ctx := conn.CloseRead(r.Context())
	if !s.writePreview(ctx, conn, cfg) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			// El cliente se fue o el servidor está cerrando.
			return
		case msg, ok := <-ch:
			if !ok {
				// El hub cerró los taps: la emisión terminó.
				conn.Close(previewCloseEnded, "la emisión terminó")
				return
			}
			var out []byte
			if msg.IsSeqHeader {
				// Renegociación a mitad: config nueva, el cliente reconfigura.
				out = previewConfigMsg(msg.Payload)
			} else {
				out = previewFrameMsg(msg)
			}
			if out == nil {
				continue
			}
			if !s.writePreview(ctx, conn, out) {
				return
			}
		}
	}
}

// writePreview escribe un mensaje binario con el mismo plazo que el WS de estado: un
// cliente que no lee se corta, no se acumula.
func (s *Server) writePreview(ctx context.Context, conn *websocket.Conn, b []byte) bool {
	escritura, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	if err := conn.Write(escritura, websocket.MessageBinary, b); err != nil {
		s.logger.Debug("se cerró el WebSocket de la vista previa", "err", err)
		return false
	}
	return true
}
