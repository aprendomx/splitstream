package flv

import "errors"

// ErrMalformedESDS indica que lo que llegó empieza como un ES_Descriptor pero no se
// puede recorrer hasta el AudioSpecificConfig.
var ErrMalformedESDS = errors.New("ES_Descriptor malformado")

// Etiquetas de los descriptores de ISO 14496-1 §7.2.6 que hay que atravesar.
const (
	esDescrTag             byte = 0x03
	decoderConfigDescrTag  byte = 0x04
	decoderSpecificInfoTag byte = 0x05
)

// AudioSpecificConfig devuelve el ASC que va dentro de un AAC sequence header a partir
// de lo que AudioEncoder entrega en decoderConfig.description.
//
// Chrome entrega el ASC desnudo (2 bytes para AAC-LC). Safari entrega un ES_Descriptor
// completo —lo que iría en una caja esds de MP4— con el ASC dentro como
// DecoderSpecificInfo. Se distingue por el primer byte: un ASC de AAC-LC empieza por el
// audioObjectType 2 en sus 5 bits altos (0x10–0x17), nunca por 0x03.
func AudioSpecificConfig(description []byte) ([]byte, error) {
	if len(description) == 0 {
		return nil, ErrEmptyPayload
	}
	if description[0] != esDescrTag {
		return description, nil
	}

	es, err := leerDescriptor(description, esDescrTag)
	if err != nil {
		return nil, err
	}
	// ES_ID (2 bytes) y flags (1). Los campos opcionales que anuncian los flags no los
	// produce ningún navegador, pero se saltan igual: cuesta tres ifs.
	if len(es) < 3 {
		return nil, ErrMalformedESDS
	}
	flags := es[2]
	es = es[3:]
	if flags&0x80 != 0 { // streamDependenceFlag: dependsOn_ES_ID (2 bytes)
		if len(es) < 2 {
			return nil, ErrMalformedESDS
		}
		es = es[2:]
	}
	if flags&0x40 != 0 { // URL_Flag: URLlength (1 byte) + URL
		if len(es) < 1 || len(es) < 1+int(es[0]) {
			return nil, ErrMalformedESDS
		}
		es = es[1+int(es[0]):]
	}
	if flags&0x20 != 0 { // OCRstreamFlag: OCR_ES_Id (2 bytes)
		if len(es) < 2 {
			return nil, ErrMalformedESDS
		}
		es = es[2:]
	}

	dc, err := leerDescriptor(es, decoderConfigDescrTag)
	if err != nil {
		return nil, err
	}
	// objectTypeIndication (1), streamType/upStream/reserved (1), bufferSizeDB (3),
	// maxBitrate (4), avgBitrate (4): 13 bytes antes del DecoderSpecificInfo.
	if len(dc) < 13 {
		return nil, ErrMalformedESDS
	}
	asc, err := leerDescriptor(dc[13:], decoderSpecificInfoTag)
	if err != nil {
		return nil, err
	}
	if len(asc) < 2 {
		return nil, ErrMalformedESDS
	}
	return asc, nil
}

// leerDescriptor comprueba la etiqueta, lee la longitud «base 128 extensible» (de 1 a 4
// bytes, el bit alto de cada uno dice si sigue otro) y devuelve el cuerpo.
func leerDescriptor(b []byte, tag byte) ([]byte, error) {
	if len(b) < 2 || b[0] != tag {
		return nil, ErrMalformedESDS
	}
	size, n := 0, 1
	for i := 0; i < 4; i++ {
		if n >= len(b) {
			return nil, ErrMalformedESDS
		}
		c := b[n]
		n++
		size = size<<7 | int(c&0x7f)
		if c&0x80 == 0 {
			break
		}
	}
	if size > len(b)-n {
		return nil, ErrMalformedESDS
	}
	return b[n : n+size], nil
}
