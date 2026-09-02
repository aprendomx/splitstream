package relay

import (
	"context"
	"sync"
	"testing"
	"time"
)

func vKey(ts uint32, size int) *Message {
	return &Message{Kind: KindVideo, Timestamp: ts, Payload: make([]byte, size), IsKeyframe: true}
}
func vInter(ts uint32, size int) *Message {
	return &Message{Kind: KindVideo, Timestamp: ts, Payload: make([]byte, size)}
}
func aRaw(ts uint32, size int) *Message {
	return &Message{Kind: KindAudio, Timestamp: ts, Payload: make([]byte, size)}
}
func vSeq() *Message {
	return &Message{Kind: KindVideo, Payload: []byte{0x17, 0x00}, IsSeqHeader: true, IsKeyframe: true}
}
func metaMsg() *Message {
	return &Message{Kind: KindMeta, Payload: []byte{0xFF}}
}

func drain(t *testing.T, q *queue) []*Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var out []*Message
	for {
		q.mu.Lock()
		empty := len(q.items) == 0
		q.mu.Unlock()
		if empty {
			return out
		}
		m, ok := q.pop(ctx)
		if !ok {
			return out
		}
		out = append(out, m)
	}
}

func TestQueueFIFOWhenUnderLimits(t *testing.T) {
	q := newQueue(queueConfig{})
	defer q.close()

	q.push(vKey(0, 10))
	q.push(aRaw(10, 5))
	q.push(vInter(33, 10))

	got := drain(t, q)
	if len(got) != 3 {
		t.Fatalf("len = %d, quería 3", len(got))
	}
	if got[0].Timestamp != 0 || got[1].Timestamp != 10 || got[2].Timestamp != 33 {
		t.Errorf("orden alterado: %d %d %d", got[0].Timestamp, got[1].Timestamp, got[2].Timestamp)
	}
	if q.dropped() != 0 {
		t.Errorf("dropped = %d, quería 0", q.dropped())
	}
}

// Al desbordar por bytes se tira TODO el vídeo encolado, no un frame suelto (spec §3.3).
func TestQueueDropsWholeGOPOnByteOverflow(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 100, MaxSpan: 1_000_000})
	defer q.close()

	q.push(metaMsg())
	q.push(vSeq())
	q.push(vKey(0, 40))
	q.push(aRaw(10, 5))
	q.push(vInter(33, 40))
	q.push(vInter(66, 40)) // aquí desborda

	if !q.droppingVideo() {
		t.Fatal("la cola debería estar descartando vídeo tras desbordar")
	}

	got := drain(t, q)
	for _, m := range got {
		if m.Kind == KindVideo && !m.IsSeqHeader {
			t.Errorf("quedó vídeo encolado tras el desbordamiento: ts=%d", m.Timestamp)
		}
	}
	// El audio, la metadata y los sequence headers sobreviven.
	var nAudio, nMeta, nSeq int
	for _, m := range got {
		switch {
		case m.IsSeqHeader:
			nSeq++
		case m.Kind == KindAudio:
			nAudio++
		case m.Kind == KindMeta:
			nMeta++
		}
	}
	if nAudio != 1 || nMeta != 1 || nSeq != 1 {
		t.Errorf("audio=%d meta=%d seq=%d, quería 1 1 1", nAudio, nMeta, nSeq)
	}
	if q.dropped() == 0 {
		t.Error("el contador de descartes no subió")
	}
}

// También desborda por duración, aunque quepa en bytes.
func TestQueueDropsOnSpanOverflow(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 1 << 30, MaxSpan: 1000})
	defer q.close()

	q.push(vKey(0, 1))
	q.push(vInter(500, 1))
	q.push(vInter(2000, 1)) // 2 s de span > 1 s

	if !q.droppingVideo() {
		t.Fatal("la cola debería descartar al superar la duración máxima")
	}
}

// En modo descarte, los inter frames se tiran y el siguiente keyframe resincroniza.
func TestQueueResyncsOnNextKeyframe(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 50, MaxSpan: 1_000_000})
	defer q.close()

	q.push(vKey(0, 30))
	q.push(vInter(33, 30)) // desborda
	if !q.droppingVideo() {
		t.Fatal("debería estar descartando")
	}

	q.push(vInter(66, 1)) // se tira: no es keyframe
	q.push(vInter(99, 1)) // se tira
	q.push(vKey(132, 1))  // resincroniza aquí

	if q.droppingVideo() {
		t.Error("un keyframe debe sacar a la cola del modo descarte")
	}
	got := drain(t, q)
	var lastVideo *Message
	for _, m := range got {
		if m.Kind == KindVideo && !m.IsSeqHeader {
			lastVideo = m
		}
	}
	if lastVideo == nil || lastVideo.Timestamp != 132 {
		t.Errorf("el vídeo que sobrevive debe ser el keyframe de resincronización, fue %v", lastVideo)
	}
}

// El audio no se descarta en el nivel blando: es barato y su corte se nota mucho más que
// un salto de vídeo. (El nivel duro sí lo tira, pero eso es la red de seguridad y tiene
// su propio test.)
func TestQueueNeverDropsAudio(t *testing.T) {
	// MaxBytes 200 dispara el nivel blando con 500 bytes de audio, pero se queda muy por
	// debajo del duro (800), así que nada de audio debe perderse.
	q := newQueue(queueConfig{MaxBytes: 200, MaxSpan: 1_000_000})
	defer q.close()

	for i := 0; i < 50; i++ {
		q.push(aRaw(uint32(i*20), 10))
	}
	got := drain(t, q)
	if len(got) != 50 {
		t.Errorf("sobrevivieron %d mensajes de audio de 50: el nivel blando no debe tirar audio", len(got))
	}
}

// Los sequence headers y la metadata nunca se descartan: sin ellos no se decodifica nada.
func TestQueueNeverDropsEssentials(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 10, MaxSpan: 1_000_000})
	defer q.close()

	q.push(metaMsg())
	q.push(vSeq())
	q.push(vKey(0, 100)) // desborda de sobra
	q.push(vInter(33, 100))

	got := drain(t, q)
	var seenMeta, seenSeq bool
	for _, m := range got {
		if m.Kind == KindMeta {
			seenMeta = true
		}
		if m.IsSeqHeader {
			seenSeq = true
		}
	}
	if !seenMeta || !seenSeq {
		t.Errorf("meta=%v seq=%v: los esenciales nunca se descartan", seenMeta, seenSeq)
	}
}

func TestQueuePopBlocksUntilPush(t *testing.T) {
	q := newQueue(queueConfig{})
	defer q.close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan *Message, 1)
	go func() {
		m, ok := q.pop(ctx)
		if ok {
			done <- m
		} else {
			close(done)
		}
	}()

	time.Sleep(50 * time.Millisecond)
	q.push(vKey(42, 1))

	select {
	case m := <-done:
		if m == nil || m.Timestamp != 42 {
			t.Errorf("pop devolvió %v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pop no despertó tras el push")
	}
}

func TestQueuePopRespectsContext(t *testing.T) {
	q := newQueue(queueConfig{})
	defer q.close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, ok := q.pop(ctx); ok {
		t.Fatal("pop debería fallar al vencer el contexto")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("pop tardó %v en rendirse", d)
	}
}

func TestQueueCloseUnblocksPop(t *testing.T) {
	q := newQueue(queueConfig{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		q.pop(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)
	q.close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("close no despertó a pop")
	}
}

func TestQueuePushAfterCloseIsSafe(t *testing.T) {
	q := newQueue(queueConfig{})
	q.close()
	q.push(vKey(0, 1)) // no debe entrar en pánico
	q.close()          // idempotente
}

// push desde varias goroutines mientras otra hace pop, bajo -race.
func TestQueueConcurrentPushPop(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 4096, MaxSpan: 1_000_000})
	defer q.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if i%10 == 0 {
					q.push(vKey(uint32(i*33), 64))
				} else {
					q.push(vInter(uint32(i*33), 64))
				}
			}
		}(g)
	}

	consumed := make(chan int, 1)
	go func() {
		n := 0
		for {
			if _, ok := q.pop(ctx); !ok {
				consumed <- n
				return
			}
			n++
		}
	}()

	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-consumed // no se comprueba el número: con descarte, es no determinista
}

// Una saturación causada solo por audio también tiene que acotarse: un destino caído
// mientras la transmisión continúa acumularía audio para siempre.
func TestQueueBoundedUnderAudioOnlySaturation(t *testing.T) {
	const maxBytes, maxItems = 1000, 64
	q := newQueue(queueConfig{MaxBytes: maxBytes, MaxSpan: 1_000_000, MaxItems: maxItems})
	defer q.close()

	for i := 0; i < 20_000; i++ {
		q.push(aRaw(uint32(i*20), 100))
	}

	items, bytes, _ := q.stats()
	if items > maxItems {
		t.Errorf("la cola tiene %d ítems con una cota de %d: no está acotada", items, maxItems)
	}
	// El límite duro de bytes es maxBytes * hardBytesFactor.
	if bytes > maxBytes*hardBytesFactor {
		t.Errorf("la cola retiene %d bytes con un límite duro de %d",
			bytes, maxBytes*hardBytesFactor)
	}
	if q.dropped() == 0 {
		t.Error("no se descartó nada pese a la saturación")
	}
}

// Y ese caso no puede costar O(n²): 20 000 push deben tardar milisegundos, no segundos.
func TestQueueAudioOnlySaturationIsCheap(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 1000, MaxSpan: 1_000_000, MaxItems: 64})
	defer q.close()

	start := time.Now()
	for i := 0; i < 20_000; i++ {
		q.push(aRaw(uint32(i*20), 100))
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("20 000 push de audio tardaron %v: hay un reescaneo O(n) por push", d)
	}
}

// Ni el nivel duro tira la metadata ni los sequence headers.
func TestQueueHardLimitKeepsEssentials(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 100, MaxSpan: 1_000_000, MaxItems: 8})
	defer q.close()

	q.push(metaMsg())
	q.push(vSeq())
	for i := 0; i < 500; i++ {
		q.push(aRaw(uint32(i*20), 50))
	}

	got := drain(t, q)
	var seenMeta, seenSeq bool
	for _, m := range got {
		if m.Kind == KindMeta {
			seenMeta = true
		}
		if m.IsSeqHeader {
			seenSeq = true
		}
	}
	if !seenMeta || !seenSeq {
		t.Errorf("meta=%v seq=%v: el nivel duro tampoco puede tirar los esenciales",
			seenMeta, seenSeq)
	}
}

// El contador de vídeo no se descuadra con push, pop y descartes mezclados.
func TestQueueVideoCounterStaysConsistent(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 1 << 30, MaxSpan: 1_000_000, MaxItems: 1 << 20})
	defer q.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < 100; i++ {
		if i%10 == 0 {
			q.push(vKey(uint32(i*33), 10))
		} else {
			q.push(vInter(uint32(i*33), 10))
		}
		q.push(aRaw(uint32(i*33), 5))
	}
	for i := 0; i < 150; i++ {
		q.pop(ctx)
	}

	q.mu.Lock()
	counted := 0
	for _, m := range q.items {
		if m.Kind == KindVideo && !essential(m) {
			counted++
		}
	}
	tracked := q.videoItems
	q.mu.Unlock()

	if counted != tracked {
		t.Errorf("videoItems = %d pero hay %d de vídeo en la cola", tracked, counted)
	}
}

// La integridad del GOP se mantiene incluso bajo el límite duro: nunca puede quedar un
// frame intermedio delante de su keyframe, porque el destino lo decodificaría como bloques.
func TestQueueHardLimitPreservesGOPIntegrity(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 4000, MaxSpan: 1_000_000, MaxItems: 200})
	defer q.close()

	// Patrón realista: keyframe cada 30 frames, audio intercalado.
	ts := uint32(0)
	for gop := 0; gop < 200; gop++ {
		for f := 0; f < 30; f++ {
			if f == 0 {
				q.push(vKey(ts, 500))
			} else {
				q.push(vInter(ts, 200))
			}
			if f%3 == 0 {
				q.push(aRaw(ts, 40))
			}
			ts += 33
		}
	}

	got := drain(t, q)
	var seenKeyframe bool
	for i, m := range got {
		if m.Kind != KindVideo || m.IsSeqHeader {
			continue
		}
		if m.IsKeyframe {
			seenKeyframe = true
			continue
		}
		if !seenKeyframe {
			t.Fatalf("el mensaje %d es un inter frame (ts=%d) sin keyframe previo: el destino vería bloques",
				i, m.Timestamp)
		}
	}
}
