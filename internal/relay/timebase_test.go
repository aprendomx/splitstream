package relay

import "testing"

func TestTimebaseAnchorsToKeyframe(t *testing.T) {
	var tb timebase
	if tb.started() {
		t.Fatal("un timebase recién creado no está arrancado")
	}
	tb.start(5000)
	if !tb.started() {
		t.Fatal("start() debe marcarlo como arrancado")
	}

	out, ok := tb.translate(5000)
	if !ok || out != 0 {
		t.Errorf("translate(5000) = (%d, %v), quería (0, true)", out, ok)
	}
	out, ok = tb.translate(5033)
	if !ok || out != 33 {
		t.Errorf("translate(5033) = (%d, %v), quería (33, true)", out, ok)
	}
}

// Audio y video comparten base: la traducción no depende de la pista.
func TestTimebaseSharedAcrossTracks(t *testing.T) {
	var tb timebase
	tb.start(5000)

	video, okV := tb.translate(5100)
	audio, okA := tb.translate(5100)
	if !okV || !okA || video != audio {
		t.Errorf("video=%d audio=%d: la base debe ser común a las dos pistas", video, audio)
	}
}

// El audio anterior a la base se descarta en vez de emitirse negativo (spec §3.2).
func TestTimebaseDropsPreBaseAudio(t *testing.T) {
	var tb timebase
	tb.start(5000)

	if _, ok := tb.translate(4999); ok {
		t.Error("un timestamp anterior a la base debe descartarse")
	}
	if _, ok := tb.translate(4000); ok {
		t.Error("un timestamp muy anterior a la base debe descartarse")
	}
}

func TestTimebaseResetAllowsNewAnchor(t *testing.T) {
	var tb timebase
	tb.start(5000)
	tb.reset()
	if tb.started() {
		t.Fatal("reset debe desarmar el timebase")
	}
	tb.start(9000)
	out, ok := tb.translate(9500)
	if !ok || out != 500 {
		t.Errorf("tras reconectar, translate(9500) = (%d, %v), quería (500, true)", out, ok)
	}
}
