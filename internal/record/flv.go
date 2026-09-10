// Package record graba la sesión a disco como un sink más: un relay.Publisher que escribe
// tags FLV en archivos segmentados, con cuota de disco. Importa relay (por Publisher y
// Message) y flv (para reconocer keyframes y sequence headers); nada del motor lo importa.
package record

import (
	"encoding/binary"
	"errors"
	"io"
)

// Tipos de tag FLV.
const (
	TagAudio  byte = 8
	TagVideo  byte = 9
	TagScript byte = 18
)

// maxTagSize es lo que caben en los 3 bytes de DataSize. Un tag mayor no existe en la
// práctica (un keyframe 4K a bitrate alto son cientos de KB), pero escribirlo truncaría el
// tamaño y corrompería todo lo que siguiera.
const maxTagSize = 1<<24 - 1

var errTagTooBig = errors.New("tag FLV demasiado grande")

// flvHeader es la cabecera del archivo más el PreviousTagSize0: "FLV", versión 1, flags
// audio+vídeo, DataOffset 9, y los cuatro ceros del primer PreviousTagSize.
var flvHeader = []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}

// WriteHeader escribe la cabecera del archivo.
func WriteHeader(w io.Writer) error {
	_, err := w.Write(flvHeader)
	return err
}

// WriteTag escribe un tag y su PreviousTagSize. El timestamp va en 3 bytes más el byte
// extendido con los 8 altos, que es la disposición rara de FLV.
func WriteTag(w io.Writer, typ byte, ts uint32, data []byte) error {
	n := len(data)
	if n > maxTagSize {
		return errTagTooBig
	}
	var h [11]byte
	h[0] = typ
	h[1], h[2], h[3] = byte(n>>16), byte(n>>8), byte(n)
	h[4], h[5], h[6], h[7] = byte(ts>>16), byte(ts>>8), byte(ts), byte(ts>>24)
	// h[8..10]: StreamID, siempre 0.
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	var prev [4]byte
	binary.BigEndian.PutUint32(prev[:], uint32(11+n))
	_, err := w.Write(prev[:])
	return err
}
