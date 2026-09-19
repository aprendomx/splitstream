package flv

import (
	"bytes"
	"fmt"

	"github.com/yutopp/go-amf0"
)

// Meta son los campos del onMetaData que Splitstream declara cuando el publisher es el
// navegador. Con OBS no hace falta: OBS manda el suyo y el relay lo reenvía tal cual.
type Meta struct {
	Width, Height    int
	Framerate        float64
	VideoBitrateKbps float64
	AudioBitrateKbps float64
	AudioSampleRate  int
	Stereo           bool
	// Encoder es el nombre que las plataformas enseñan como «software de emisión».
	Encoder string
}

// OnMetaData codifica en AMF0 el cuerpo de un @setDataFrame: la cadena "onMetaData" y un
// ECMA array con los campos. Es exactamente el payload que OnSetDataFrame recibe de OBS
// (go-rtmp entrega los bytes que siguen a "@setDataFrame"), así que Publisher.WriteMeta
// y el grabador lo tratan igual que al de OBS sin tocarse.
//
// Los números van como float64 porque AMF0 solo tiene un tipo numérico (Number, IEEE
// 754); los codecid 7 y 10 son los mismos que Inspect* reconoce.
func OnMetaData(m Meta) ([]byte, error) {
	var buf bytes.Buffer
	enc := amf0.NewEncoder(&buf)
	if err := enc.Encode("onMetaData"); err != nil {
		return nil, fmt.Errorf("codificar onMetaData: %w", err)
	}
	campos := amf0.ECMAArray{
		"width":           float64(m.Width),
		"height":          float64(m.Height),
		"framerate":       m.Framerate,
		"videocodecid":    float64(CodecIDAVC),
		"videodatarate":   m.VideoBitrateKbps,
		"audiocodecid":    float64(SoundFormatAAC),
		"audiodatarate":   m.AudioBitrateKbps,
		"audiosamplerate": float64(m.AudioSampleRate),
		"audiosamplesize": float64(16),
		"stereo":          m.Stereo,
		"encoder":         m.Encoder,
	}
	if err := enc.Encode(campos); err != nil {
		return nil, fmt.Errorf("codificar los campos de onMetaData: %w", err)
	}
	return buf.Bytes(), nil
}
