package flv

// Este archivo es el inverso de inspect.go: escribe las cabeceras de tag que Inspect*
// lee. Existe porque, cuando el publisher es el navegador (spec cámara §4), lo que llega
// son NALUs y frames AAC desnudos, y el hub —y todo lo que hay detrás: sinks, grabación,
// vista previa— espera cuerpos de tag FLV como los que manda OBS.

const (
	// Primer byte de un tag de vídeo AVC clásico: frameType en el nibble alto (1 =
	// keyframe, 2 = inter) y codecID en el bajo (7 = AVC).
	videoHeaderKeyframe byte = 0x17
	videoHeaderInter    byte = 0x27

	// AVCPacketType: 0 = sequence header (avcC), 1 = NALUs.
	avcPacketSeqHeader byte = 0x00
	avcPacketNALU      byte = 0x01

	// Primer byte de un tag de audio AAC: soundFormat 10 en el nibble alto y, en el
	// bajo, tasa 3 (44 kHz), tamaño 1 (16 bits) y tipo 1 (estéreo). Para AAC la
	// especificación FLV fija esos tres campos SIEMPRE así, sean cuales sean la tasa y
	// los canales reales: esos van dentro del AudioSpecificConfig, que es lo que leen
	// las plataformas.
	audioHeaderAAC byte = 0xAF

	// AACPacketType: 0 = sequence header (AudioSpecificConfig), 1 = frame AAC crudo.
	aacPacketSeqHeader byte = 0x00
	aacPacketRaw       byte = 0x01
)

// WrapVideo envuelve NALUs en formato AVCC (longitud prefijada de 4 bytes) en el cuerpo
// de un tag de vídeo. El composition time va a 0: los codificadores de navegador en modo
// realtime no producen B-frames, y sin B-frames la marca de presentación coincide con la
// de decodificación.
func WrapVideo(nalus []byte, keyframe bool) []byte {
	h := videoHeaderInter
	if keyframe {
		h = videoHeaderKeyframe
	}
	out := make([]byte, 0, 5+len(nalus))
	out = append(out, h, avcPacketNALU, 0, 0, 0)
	return append(out, nalus...)
}

// WrapVideoSeqHeader envuelve un AVCDecoderConfigurationRecord (avcC) en el cuerpo de un
// AVC sequence header. Va marcado como keyframe, igual que lo manda OBS, y es lo que
// InspectVideo espera de un sequence header.
func WrapVideoSeqHeader(avcC []byte) []byte {
	out := make([]byte, 0, 5+len(avcC))
	out = append(out, videoHeaderKeyframe, avcPacketSeqHeader, 0, 0, 0)
	return append(out, avcC...)
}

// WrapAudio envuelve un frame AAC crudo (sin cabecera ADTS) en el cuerpo de un tag de
// audio.
func WrapAudio(aac []byte) []byte {
	out := make([]byte, 0, 2+len(aac))
	out = append(out, audioHeaderAAC, aacPacketRaw)
	return append(out, aac...)
}

// WrapAudioSeqHeader envuelve un AudioSpecificConfig en el cuerpo de un AAC sequence
// header.
func WrapAudioSeqHeader(asc []byte) []byte {
	out := make([]byte, 0, 2+len(asc))
	out = append(out, audioHeaderAAC, aacPacketSeqHeader)
	return append(out, asc...)
}
