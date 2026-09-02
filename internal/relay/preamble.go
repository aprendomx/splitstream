package relay

import "sync"

// Preamble guarda los tres mensajes que todo destino necesita recibir antes que cualquier
// frame: el onMetaData, el AVC sequence header y el AAC sequence header (spec §6.2).
//
// Sin ellos, un destino que se conecte a mitad de transmisión no sabe decodificar nada.
// El valor cero está listo para usarse.
type Preamble struct {
	mu       sync.RWMutex
	meta     *Message
	videoSeq *Message
	audioSeq *Message
}

// Observe registra el mensaje si es uno de los tres del preámbulo. Los frames normales
// se ignoran. Un sequence header nuevo sustituye al anterior: si el publisher renegocia
// a mitad de transmisión, manda el último.
func (p *Preamble) Observe(msg *Message) {
	switch {
	case msg.Kind == KindMeta:
	case msg.Kind == KindVideo && msg.IsSeqHeader:
	case msg.Kind == KindAudio && msg.IsSeqHeader:
	default:
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	switch msg.Kind {
	case KindMeta:
		p.meta = msg
	case KindVideo:
		p.videoSeq = msg
	case KindAudio:
		p.audioSeq = msg
	}
}

// Snapshot devuelve los tres mensajes cacheados. Cualquiera puede ser nil si todavía no
// se ha visto. Los mensajes son inmutables, así que devolverlos es seguro.
func (p *Preamble) Snapshot() (meta, videoSeq, audioSeq *Message) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.meta, p.videoSeq, p.audioSeq
}

// Reset olvida los tres mensajes. Se llama al terminar una sesión: los headers de la
// transmisión anterior no valen para la siguiente.
func (p *Preamble) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.meta, p.videoSeq, p.audioSeq = nil, nil, nil
}
