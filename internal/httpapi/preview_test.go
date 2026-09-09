package httpapi

import (
	"bytes"
	"testing"

	"github.com/aprendomx/splitstream/internal/relay"
)

// Payloads FLV de mentira: 5 bytes de cabecera (frameType/codecID, AVCPacketType,
// composition time) y detrás lo que importa. Al servidor solo le interesa dónde empieza.
func seqPayload(avcc ...byte) []byte {
	return append([]byte{0x17, 0x00, 0x00, 0x00, 0x00}, avcc...)
}

func framePayload(nalus ...byte) []byte {
	return append([]byte{0x27, 0x01, 0x00, 0x00, 0x00}, nalus...)
}

// El mensaje de config es 0x01 + el avcC: el payload FLV sin sus 5 bytes de cabecera.
func TestPreviewConfigMsgStripsTheFLVHeader(t *testing.T) {
	avcc := []byte{0x01, 0x64, 0x00, 0x1f, 0xff}
	got := previewConfigMsg(seqPayload(avcc...))
	want := append([]byte{previewMsgConfig}, avcc...)
	if !bytes.Equal(got, want) {
		t.Fatalf("previewConfigMsg = %x; quería %x", got, want)
	}
}

// Un payload que no da ni para la cabecera FLV no es una config: nil, y quien llama
// decide (no mandar nada, o «sin señal»).
func TestPreviewConfigMsgRejectsAShortPayload(t *testing.T) {
	for _, p := range [][]byte{nil, {}, {0x17, 0x00, 0x00, 0x00, 0x00}} {
		if got := previewConfigMsg(p); got != nil {
			t.Fatalf("previewConfigMsg(%x) = %x; quería nil", p, got)
		}
	}
}

// El mensaje de frame es 0x02, flags (bit 0 = keyframe), timestamp en 4 bytes
// big-endian de milisegundos, y los NALUs AVCC tal cual.
func TestPreviewFrameMsgEncodesFlagsTimestampAndNALUs(t *testing.T) {
	m := &relay.Message{
		Kind: relay.KindVideo, Timestamp: 0x01020304, IsKeyframe: true,
		Payload: framePayload(0xaa, 0xbb, 0xcc),
	}
	got := previewFrameMsg(m)
	want := []byte{previewMsgFrame, 0x01, 0x01, 0x02, 0x03, 0x04, 0xaa, 0xbb, 0xcc}
	if !bytes.Equal(got, want) {
		t.Fatalf("previewFrameMsg = %x; quería %x", got, want)
	}

	m.IsKeyframe = false
	if got := previewFrameMsg(m); got[1] != 0x00 {
		t.Fatalf("flags de un delta = %#x; quería 0x00", got[1])
	}

	if got := previewFrameMsg(&relay.Message{Payload: []byte{0x27, 0x01}}); got != nil {
		t.Fatalf("un payload sin NALUs dio %x; quería nil", got)
	}
}
