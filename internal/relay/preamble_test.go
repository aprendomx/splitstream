package relay

import "testing"

func msg(kind Kind, ts uint32, seq bool) *Message {
	return &Message{Kind: kind, Timestamp: ts, Payload: []byte{byte(ts)}, IsSeqHeader: seq}
}

func TestPreambleCachesTheThreeMessages(t *testing.T) {
	var p Preamble
	p.Observe(msg(KindMeta, 0, false))
	p.Observe(msg(KindVideo, 0, true))
	p.Observe(msg(KindAudio, 0, true))
	// Media normal: no debe reemplazar nada de lo anterior.
	p.Observe(msg(KindVideo, 33, false))
	p.Observe(msg(KindAudio, 23, false))

	meta, videoSeq, audioSeq := p.Snapshot()
	if meta == nil || meta.Kind != KindMeta {
		t.Errorf("meta = %v", meta)
	}
	if videoSeq == nil || !videoSeq.IsSeqHeader || videoSeq.Kind != KindVideo {
		t.Errorf("videoSeq = %v", videoSeq)
	}
	if audioSeq == nil || !audioSeq.IsSeqHeader || audioSeq.Kind != KindAudio {
		t.Errorf("audioSeq = %v", audioSeq)
	}
	if videoSeq.Timestamp != 0 {
		t.Errorf("el frame normal sobrescribió el sequence header de video")
	}
}

func TestPreambleEmptySnapshot(t *testing.T) {
	var p Preamble
	meta, videoSeq, audioSeq := p.Snapshot()
	if meta != nil || videoSeq != nil || audioSeq != nil {
		t.Error("un preámbulo vacío debe devolver tres nil")
	}
}

// Si el publisher renegocia (OBS cambia de códec a mitad), el nuevo header manda.
func TestPreambleLatestSequenceHeaderWins(t *testing.T) {
	var p Preamble
	p.Observe(&Message{Kind: KindVideo, Timestamp: 0, Payload: []byte{1}, IsSeqHeader: true})
	p.Observe(&Message{Kind: KindVideo, Timestamp: 500, Payload: []byte{2}, IsSeqHeader: true})

	_, videoSeq, _ := p.Snapshot()
	if videoSeq.Payload[0] != 2 {
		t.Errorf("payload = %v, quería el sequence header más reciente", videoSeq.Payload)
	}
}

func TestPreambleReset(t *testing.T) {
	var p Preamble
	p.Observe(msg(KindMeta, 0, false))
	p.Observe(msg(KindVideo, 0, true))
	p.Reset()

	meta, videoSeq, audioSeq := p.Snapshot()
	if meta != nil || videoSeq != nil || audioSeq != nil {
		t.Error("Reset debe vaciar el preámbulo")
	}
}
