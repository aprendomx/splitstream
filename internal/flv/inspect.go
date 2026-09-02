// Package flv inspecciona el primer byte de los tags de media de FLV/RTMP, lo justo
// para que el relay decida qué hacer con cada mensaje sin decodificar nada.
package flv

import "errors"

// ErrEmptyPayload indica que el tag no trae ni el byte de cabecera.
var ErrEmptyPayload = errors.New("payload de tag vacío")

// Identificadores de códec que Splitstream acepta. El relay rechaza lo demás porque no
// puede transcodificar y porque no todas las plataformas aceptan lo mismo (spec §3.6).
const (
	CodecIDAVC     uint8 = 7  // H.264 en la ruta clásica de FLV
	SoundFormatAAC uint8 = 10 // AAC
)

// exVideoHeaderBit es el bit alto del primer byte de un tag de video. En enhanced-RTMP
// marca que lo que sigue es una cabecera con FourCC (HEVC, AV1, VP9) en vez del par
// frameType/codecID clásico.
const exVideoHeaderBit = 0x80

// VideoInfo es lo que se puede saber de un tag de video sin decodificarlo.
type VideoInfo struct {
	IsKeyframe       bool
	IsSequenceHeader bool
	IsEnhanced       bool
	CodecID          uint8
}

// AudioInfo es lo que se puede saber de un tag de audio sin decodificarlo.
type AudioInfo struct {
	IsSequenceHeader bool
	SoundFormat      uint8
}

// InspectVideo lee la cabecera de un tag de video.
//
// Formato clásico: el primer byte son 4 bits de frameType (1 = keyframe) y 4 de codecID
// (7 = AVC). Si el códec es AVC, el segundo byte es el AVCPacketType, y 0 significa
// sequence header (SPS/PPS).
func InspectVideo(payload []byte) (VideoInfo, error) {
	if len(payload) == 0 {
		return VideoInfo{}, ErrEmptyPayload
	}

	b := payload[0]
	info := VideoInfo{
		IsEnhanced: b&exVideoHeaderBit != 0,
		IsKeyframe: (b>>4)&0x07 == 1,
		CodecID:    b & 0x0f,
	}
	// El AVCPacketType solo existe en la ruta clásica de AVC, y necesita un segundo byte.
	if !info.IsEnhanced && info.CodecID == CodecIDAVC && len(payload) >= 2 {
		info.IsSequenceHeader = payload[1] == 0x00
	}
	return info, nil
}

// InspectAudio lee la cabecera de un tag de audio.
//
// El primer byte son 4 bits de soundFormat (10 = AAC) y 4 de tasa, tamaño y canales.
// Solo en AAC el segundo byte es el AACPacketType, y 0 significa sequence header.
func InspectAudio(payload []byte) (AudioInfo, error) {
	if len(payload) == 0 {
		return AudioInfo{}, ErrEmptyPayload
	}

	info := AudioInfo{SoundFormat: (payload[0] >> 4) & 0x0f}
	if info.SoundFormat == SoundFormatAAC && len(payload) >= 2 {
		info.IsSequenceHeader = payload[1] == 0x00
	}
	return info, nil
}
