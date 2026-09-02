package relay

// timebase traduce los timestamps del publisher a los que ve un destino concreto.
//
// La base es ÚNICA para audio y video y se ancla al keyframe con el que arranca el envío
// (spec §3.2). Anclar cada pista por separado desincronizaría el audio del video, que es
// el clásico "el audio va adelantado tras reconectar". El audio anterior a la base se
// descarta en vez de emitirse negativo.
//
// En cada reconexión se llama a reset() y luego a start() con el keyframe nuevo: es una
// sesión RTMP nueva y la plataforma espera un timeline que arranca en 0.
type timebase struct {
	armed bool
	base  uint32
}

// started indica si ya hay una base fijada.
func (t *timebase) started() bool { return t.armed }

// start fija la base en el timestamp del keyframe de arranque.
func (t *timebase) start(keyframeTimestamp uint32) {
	t.base = keyframeTimestamp
	t.armed = true
}

// reset desarma el timebase para que la próxima conexión fije una base nueva.
func (t *timebase) reset() {
	t.armed = false
	t.base = 0
}

// translate convierte un timestamp del publisher al del destino. Devuelve ok=false si el
// mensaje es anterior a la base y por tanto debe descartarse.
func (t *timebase) translate(ts uint32) (uint32, bool) {
	if !t.armed || ts < t.base {
		return 0, false
	}
	return ts - t.base, true
}
