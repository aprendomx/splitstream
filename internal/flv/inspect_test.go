package flv_test

import (
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/flv"
)

func TestInspectVideoKeyframeAVC(t *testing.T) {
	// 0x17 = frameType 1 (keyframe) | codecID 7 (AVC); 0x01 = NALU (no seq header)
	got, err := flv.InspectVideo([]byte{0x17, 0x01, 0, 0, 0})
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if !got.IsKeyframe {
		t.Error("IsKeyframe = false, quería true")
	}
	if got.IsSequenceHeader {
		t.Error("IsSequenceHeader = true, quería false")
	}
	if got.IsEnhanced {
		t.Error("IsEnhanced = true, quería false")
	}
	if got.CodecID != flv.CodecIDAVC {
		t.Errorf("CodecID = %d, quería %d", got.CodecID, flv.CodecIDAVC)
	}
}

func TestInspectVideoInterFrame(t *testing.T) {
	// 0x27 = frameType 2 (inter frame) | codecID 7
	got, err := flv.InspectVideo([]byte{0x27, 0x01, 0, 0, 0})
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if got.IsKeyframe {
		t.Error("un inter frame no es keyframe")
	}
}

func TestInspectVideoSequenceHeader(t *testing.T) {
	// 0x17 keyframe|AVC, 0x00 = AVCPacketType 0 = sequence header
	got, err := flv.InspectVideo([]byte{0x17, 0x00, 0, 0, 0})
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if !got.IsSequenceHeader {
		t.Error("IsSequenceHeader = false, quería true")
	}
	// El AVC sequence header lleva el bit de keyframe puesto, pero no es un frame.
	if !got.IsKeyframe {
		t.Error("el sequence header trae el bit de keyframe; IsKeyframe debería reflejarlo")
	}
}

// Un sequence header necesita 2 bytes: si solo hay 1, no se puede decidir.
func TestInspectVideoSingleByteIsNotSequenceHeader(t *testing.T) {
	got, err := flv.InspectVideo([]byte{0x17})
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if got.IsSequenceHeader {
		t.Error("con un solo byte no se puede afirmar que sea sequence header")
	}
}

func TestInspectVideoDetectsEnhancedRTMP(t *testing.T) {
	// Bit alto (0x80) = isExVideoHeader de enhanced-RTMP. 0x90 | FourCC "hvc1".
	got, err := flv.InspectVideo([]byte{0x90, 'h', 'v', 'c', '1'})
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if !got.IsEnhanced {
		t.Error("IsEnhanced = false: no se detectó enhanced-RTMP")
	}
}

func TestInspectVideoRejectsEmpty(t *testing.T) {
	if _, err := flv.InspectVideo(nil); !errors.Is(err, flv.ErrEmptyPayload) {
		t.Fatalf("InspectVideo(nil) = %v, quería ErrEmptyPayload", err)
	}
}

func TestInspectAudioAACSequenceHeader(t *testing.T) {
	// 0xAF = soundFormat 10 (AAC) | 44kHz | 16bit | stereo; 0x00 = AACPacketType 0
	got, err := flv.InspectAudio([]byte{0xAF, 0x00, 0x12, 0x10})
	if err != nil {
		t.Fatalf("InspectAudio: %v", err)
	}
	if !got.IsSequenceHeader {
		t.Error("IsSequenceHeader = false, quería true")
	}
	if got.SoundFormat != flv.SoundFormatAAC {
		t.Errorf("SoundFormat = %d, quería %d", got.SoundFormat, flv.SoundFormatAAC)
	}
}

func TestInspectAudioAACRawFrame(t *testing.T) {
	got, err := flv.InspectAudio([]byte{0xAF, 0x01, 0x21, 0x00})
	if err != nil {
		t.Fatalf("InspectAudio: %v", err)
	}
	if got.IsSequenceHeader {
		t.Error("un frame raw no es sequence header")
	}
}

// Solo AAC usa AACPacketType. Con MP3 (soundFormat 2) el segundo byte es audio, no un tipo.
func TestInspectAudioNonAACIsNeverSequenceHeader(t *testing.T) {
	got, err := flv.InspectAudio([]byte{0x2F, 0x00, 0x00})
	if err != nil {
		t.Fatalf("InspectAudio: %v", err)
	}
	if got.IsSequenceHeader {
		t.Error("un tag no-AAC nunca es sequence header")
	}
	if got.SoundFormat != 2 {
		t.Errorf("SoundFormat = %d, quería 2 (MP3)", got.SoundFormat)
	}
}

func TestInspectAudioRejectsEmpty(t *testing.T) {
	if _, err := flv.InspectAudio([]byte{}); !errors.Is(err, flv.ErrEmptyPayload) {
		t.Fatalf("InspectAudio(vacío) = %v, quería ErrEmptyPayload", err)
	}
}
