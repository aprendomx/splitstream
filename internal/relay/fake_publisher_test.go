package relay

import (
	"context"
	"errors"
	"sync"
)

// writtenMsg es lo que el fake registra de cada escritura.
type writtenMsg struct {
	Kind Kind
	TS   uint32
	Data []byte
}

// fakePublisher es un Publisher en memoria. Permite testear el motor entero sin red.
type fakePublisher struct {
	mu          sync.Mutex
	written     []writtenMsg
	connects    int
	closes      int
	connectErr  error
	writeErr    error
	blockWrites chan struct{} // si no es nil, cada escritura espera aquí
}

func (f *fakePublisher) Connect(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connects++
	return f.connectErr
}

func (f *fakePublisher) write(kind Kind, ts uint32, payload []byte) error {
	if f.blockWrites != nil {
		<-f.blockWrites
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	f.written = append(f.written, writtenMsg{Kind: kind, TS: ts, Data: cp})
	return nil
}

func (f *fakePublisher) WriteMeta(ts uint32, p []byte) error  { return f.write(KindMeta, ts, p) }
func (f *fakePublisher) WriteAudio(ts uint32, p []byte) error { return f.write(KindAudio, ts, p) }
func (f *fakePublisher) WriteVideo(ts uint32, p []byte) error { return f.write(KindVideo, ts, p) }

func (f *fakePublisher) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return nil
}

func (f *fakePublisher) snapshot() []writtenMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]writtenMsg, len(f.written))
	copy(out, f.written)
	return out
}

var errFakeWrite = errors.New("fallo simulado de escritura")
