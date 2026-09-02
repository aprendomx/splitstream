package relay

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
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

// --- Etiquetado (gop, seq) de los frames de vídeo del test --------------------
//
// Cada frame de vídeo del test lleva su identidad dentro del payload, en una cabecera de
// 8 bytes: `gop` es el índice del keyframe que abre el grupo y `seq` la posición dentro
// de él, con el keyframe en `seq == 0`. La aserción de integridad necesita saber a qué
// GOP pertenece cada superviviente, y el orden de llegada no basta: un frame huérfano a
// mitad del flujo solo se distingue si el propio frame dice de qué keyframe depende.

// gopTagLen son los bytes de cabecera que ocupan (gop, seq) al principio del payload.
const gopTagLen = 8

// taggedVideo construye un frame de vídeo de `size` bytes etiquetado con (gop, seq).
func taggedVideo(gop, seq uint32, ts uint32, size int) *Message {
	if size < gopTagLen {
		size = gopTagLen
	}
	p := make([]byte, size)
	binary.BigEndian.PutUint32(p[0:4], gop)
	binary.BigEndian.PutUint32(p[4:8], seq)
	return &Message{Kind: KindVideo, Timestamp: ts, Payload: p, IsKeyframe: seq == 0}
}

// gopTag lee la etiqueta de un frame construido con taggedVideo.
func gopTag(t *testing.T, m *Message) (gop, seq uint32) {
	t.Helper()
	if len(m.Payload) < gopTagLen {
		t.Fatalf("frame de vídeo sin etiqueta (%d bytes): ¿se coló un mensaje no etiquetado?", len(m.Payload))
	}
	return binary.BigEndian.Uint32(m.Payload[0:4]), binary.BigEndian.Uint32(m.Payload[4:8])
}

// popSome saca hasta n mensajes sin bloquear si la cola se queda vacía.
func popSome(t *testing.T, q *queue, n int) []*Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var out []*Message
	for i := 0; i < n; i++ {
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
	return out
}

// runTaggedStream empuja un flujo realista y lo consume más despacio de lo que lo produce,
// que es la situación que crea la presión: GOPs de longitud variable que empiezan por un
// keyframe grande y siguen con frames P de tamaños dispares, con audio intercalado. Nada
// de tamaños uniformes, porque el descarte depende de los bytes y un patrón regular puede
// esconder justo el caso que rompe.
//
// Devuelve, EN ORDEN, todo lo que salió de la cola: lo consumido durante la ejecución más
// lo que quedó dentro al final. Consumir es imprescindible para que el test signifique
// algo: sin consumidor la cola se queda clavada en su límite y cada push tira el vídeo
// recién encolado, así que no sobreviviría ningún frame P que comprobar.
func runTaggedStream(t *testing.T, q *queue, seed int64, gops int) []*Message {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	var delivered []*Message
	pushed := 0
	push := func(m *Message) {
		q.push(m)
		pushed++
		// Se consume menos de lo que se produce: la cola crece, pero oscila en torno a
		// sus límites en vez de quedarse pegada a ellos.
		if pushed%4 == 0 {
			delivered = append(delivered, popSome(t, q, 3)...)
		}
	}

	ts := uint32(0)
	for gop := 0; gop < gops; gop++ {
		frames := 15 + rng.Intn(30)
		for f := 0; f < frames; f++ {
			if f == 0 {
				push(taggedVideo(uint32(gop), 0, ts, 400+rng.Intn(1200)))
			} else {
				push(taggedVideo(uint32(gop), uint32(f), ts, 40+rng.Intn(600)))
			}
			if rng.Intn(3) == 0 {
				push(aRaw(ts, 20+rng.Intn(80)))
			}
			ts += uint32(15 + rng.Intn(30))
		}
	}

	return append(delivered, drain(t, q)...)
}

// La integridad del GOP se mantiene bajo cualquiera de los límites: ningún frame P llega
// nunca sin el keyframe que lo abre, esté donde esté en el flujo. Si un frame con seq > 0
// sale de la cola y el (gop, 0) de su grupo se descartó, el destino decodifica bloques
// corruptos hasta el siguiente IDR — que es exactamente lo que el descarte por GOP existe
// para evitar.
func TestQueueHardLimitPreservesGOPIntegrity(t *testing.T) {
	// Los perfiles cubren qué límite es el que ata en cada caso. El de ítems importa
	// especialmente: es el único que llega al nivel duro SIN pasar antes por el blando,
	// porque hardBytes es un múltiplo de maxBytes y por bytes el blando siempre dispara
	// primero. Ahí es donde un recorte por antigüedad dejaría huérfanos.
	profiles := []struct {
		name string
		cfg  queueConfig
	}{
		{"duro-por-items", queueConfig{MaxBytes: 8 << 20, MaxSpan: 1_000_000, MaxItems: 200}},
		{"duro-por-items-apretado", queueConfig{MaxBytes: 8 << 20, MaxSpan: 1_000_000, MaxItems: 37}},
		{"blando-por-bytes", queueConfig{MaxBytes: 4000, MaxSpan: 1_000_000, MaxItems: 1 << 20}},
		{"blando-y-duro-juntos", queueConfig{MaxBytes: 4000, MaxSpan: 1_000_000, MaxItems: 200}},
		{"por-duracion", queueConfig{MaxBytes: 8 << 20, MaxSpan: 400, MaxItems: 500}},
	}
	seeds := []int64{1, 7, 42, 20260902}

	for _, p := range profiles {
		for _, seed := range seeds {
			t.Run(fmt.Sprintf("%s/semilla-%d", p.name, seed), func(t *testing.T) {
				q := newQueue(p.cfg)
				defer q.close()

				q.push(metaMsg())
				q.push(vSeq())

				got := runTaggedStream(t, q, seed, 120)

				// Recorrido en orden: un keyframe abre su GOP, y a partir de ahí sus
				// frames P son legítimos. Un frame P cuyo keyframe no haya salido antes
				// es un huérfano, tanto si el keyframe se descartó como si el orden se
				// alteró.
				opened := map[uint32]bool{}
				var keys, inter int
				for i, m := range got {
					if m.Kind != KindVideo || m.IsSeqHeader {
						continue
					}
					gop, seq := gopTag(t, m)
					if seq == 0 {
						opened[gop] = true
						keys++
						continue
					}
					inter++
					if !opened[gop] {
						t.Fatalf("el mensaje %d es el frame (gop=%d, seq=%d, ts=%d) y su keyframe (gop=%d, seq=0) no sobrevivió: el destino vería bloques",
							i, gop, seq, m.Timestamp, gop)
					}
				}

				// Guardias contra una aserción vacía: sin descartes, sin keyframes o sin
				// frames P el test no habría comprobado nada.
				if q.dropped() == 0 {
					t.Fatal("el perfil no descartó nada: no llega a ejercitar la integridad bajo presión")
				}
				if keys == 0 || inter == 0 {
					t.Fatalf("keyframes=%d frames P=%d: la aserción sería vacía", keys, inter)
				}
			})
		}
	}
}

// metaSeq construye un onMetaData reconocible por su número de orden.
func metaSeq(n int) *Message {
	return &Message{Kind: KindMeta, Payload: []byte(fmt.Sprintf("meta-%06d", n))}
}

// Los esenciales no se descartan nunca por presión, pero tampoco se apilan: un
// onMetaData nuevo deja obsoleto al anterior. Sin esto, 30.000 onMetaData —alcanzables
// desde la red por OnSetDataFrame— dejan la cola sin cota ninguna.
func TestQueueDeduplicatesQueuedEssentials(t *testing.T) {
	const n = 30_000
	q := newQueue(queueConfig{})
	defer q.close()

	for i := 0; i < n; i++ {
		q.push(metaSeq(i))
	}

	items, bytes, _ := q.stats()
	if items > 8 {
		t.Errorf("%d onMetaData dejaron %d ítems en la cola: los esenciales se están apilando", n, items)
	}
	if bytes > 1024 {
		t.Errorf("%d onMetaData dejaron %d bytes en la cola", n, bytes)
	}

	// Y el que sobrevive es el último, no el primero: el destino solo necesita ese.
	got := drain(t, q)
	if len(got) == 0 {
		t.Fatal("no sobrevivió ningún onMetaData: los esenciales no se descartan")
	}
	if want := string(metaSeq(n - 1).Payload); string(got[len(got)-1].Payload) != want {
		t.Errorf("sobrevivió %q, quería el último (%q)", got[len(got)-1].Payload, want)
	}
}

// Las tres clases de esencial son ranuras independientes: la metadata no pisa a la
// cabecera de vídeo ni esta a la de audio.
func TestQueueDeduplicatesEachEssentialClassApart(t *testing.T) {
	q := newQueue(queueConfig{})
	defer q.close()

	audioSeq := func() *Message {
		return &Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true}
	}
	for i := 0; i < 1000; i++ {
		q.push(metaSeq(i))
		q.push(vSeq())
		q.push(audioSeq())
	}

	got := drain(t, q)
	var nMeta, nVideoSeq, nAudioSeq int
	for _, m := range got {
		switch essentialClass(m) {
		case essMeta:
			nMeta++
		case essVideoSeq:
			nVideoSeq++
		case essAudioSeq:
			nAudioSeq++
		}
	}
	if nMeta != 1 || nVideoSeq != 1 || nAudioSeq != 1 {
		t.Errorf("meta=%d videoSeq=%d audioSeq=%d, quería 1 1 1", nMeta, nVideoSeq, nAudioSeq)
	}
}

// Un esencial sustituido no le quita el sitio al media que venga detrás: sigue saliendo
// antes que él, que es lo único que el destino necesita para decodificar.
func TestQueueReplacedEssentialKeepsItsPlace(t *testing.T) {
	q := newQueue(queueConfig{})
	defer q.close()

	q.push(metaSeq(1))
	q.push(vKey(0, 10))
	q.push(metaSeq(2))
	q.push(aRaw(10, 5))

	got := drain(t, q)
	if len(got) != 3 {
		t.Fatalf("len = %d, quería 3 (la metadata sustituida no añade ítem)", len(got))
	}
	if got[0].Kind != KindMeta || string(got[0].Payload) != string(metaSeq(2).Payload) {
		t.Errorf("el primero debe ser la última metadata, fue %v %q", got[0].Kind, got[0].Payload)
	}
	if got[1].Kind != KindVideo || got[2].Kind != KindAudio {
		t.Errorf("el resto salió desordenado: %v %v", got[1].Kind, got[2].Kind)
	}
}

// Una avalancha de esenciales tampoco puede costar O(n²): al doblar el número de mensajes
// el tiempo debe doblarse, no cuadruplicarse.
func TestQueueEssentialFloodIsNotQuadratic(t *testing.T) {
	flood := func(n int) time.Duration {
		q := newQueue(queueConfig{})
		defer q.close()
		start := time.Now()
		for i := 0; i < n; i++ {
			q.push(metaSeq(i))
		}
		return time.Since(start)
	}

	flood(1000) // calentamiento: la primera pasada paga el arranque
	d1 := flood(10_000)
	d2 := flood(20_000)

	// Con 50 ms de holgura para el ruido de la máquina. Un crecimiento cuadrático da
	// ratio ~4 sobre magnitudes que ya se miden en cientos de milisegundos, así que no
	// se cuela por el margen.
	if limit := 3*d1 + 50*time.Millisecond; d2 > limit {
		t.Errorf("10.000 esenciales tardaron %v y 20.000 tardaron %v (ratio %.2f): escala de forma cuadrática",
			d1, d2, float64(d2)/float64(d1))
	}
	if d2 > 500*time.Millisecond {
		t.Errorf("20.000 esenciales tardaron %v: hay un reescaneo O(n) por push", d2)
	}
}

// Un esencial no puede mover el span: su timestamp no es un tiempo de presentación, y como
// se sustituye en su sitio, uno con un Timestamp disparatado en cabeza dejaba el span en 0
// y desactivaba el límite blando por duración. El Timestamp de un KindMeta viene del cable
// (OnSetDataFrame), así que es alcanzable desde la red.
func TestQueueSpanIgnoresEssentials(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 1 << 30, MaxSpan: 2000, MaxItems: 1 << 20})
	defer q.close()

	// Una metadata en cabeza y 2 s justos de media detrás: el límite todavía no dispara.
	q.push(metaSeq(1))
	q.push(vKey(1000, 10))
	q.push(aRaw(2000, 5))
	q.push(vInter(3000, 10))

	if _, _, span := q.stats(); span != 2000 {
		t.Fatalf("span = %d, quería 2000 (de la media, no de la metadata)", span)
	}
	if q.droppingVideo() {
		t.Fatal("2000 ms no supera un máximo de 2000 ms: no debería estar descartando")
	}

	// Una metadata nueva con un timestamp disparatado sustituye a la de items[0]. Ni el
	// span cambia ni se desactiva el límite.
	q.push(&Message{Kind: KindMeta, Timestamp: 999_999, Payload: []byte("meta-nueva")})
	if _, _, span := q.stats(); span != 2000 {
		t.Errorf("span = %d tras encolar un esencial con ts=999999: quería 2000", span)
	}

	// Y el límite sigue disparando cuando le toca, con media de verdad.
	q.push(vInter(3500, 10)) // 2500 ms de media > 2000
	if !q.droppingVideo() {
		t.Error("el límite blando por duración no disparó: la sustitución lo desactivó")
	}
}

// Lo mismo por el otro extremo: un esencial al final tampoco estira el span.
func TestQueueSpanIgnoresTrailingEssential(t *testing.T) {
	q := newQueue(queueConfig{MaxBytes: 1 << 30, MaxSpan: 1_000_000, MaxItems: 1 << 20})
	defer q.close()

	q.push(vKey(1000, 10))
	q.push(aRaw(1500, 5))
	q.push(&Message{Kind: KindMeta, Timestamp: 900_000, Payload: []byte("meta")})

	if _, _, span := q.stats(); span != 500 {
		t.Errorf("span = %d, quería 500: el esencial del final no cuenta", span)
	}
}

// sampleQueue mira dentro de la cola: cuántos esenciales hay de cada clase, cuántos ítems
// y cuántos bytes. Es lo que permite medir DURANTE la ejecución y no solo al terminar.
func sampleQueue(q *queue) (perClass [essClasses]int, items, bytes int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, m := range q.items {
		if c := essentialClass(m); c >= 0 {
			perClass[c]++
		}
	}
	return perClass, len(q.items), q.bytes
}

// El régimen de producción: un sink consumiendo mientras la ingesta encola, con la cola
// retenida (entra el doble de lo que sale). Los cuatro tests de dedupliación anteriores
// empujaban sin consumir nunca, y ese régimen no ejercita la contabilidad de índices de
// pop: quitar el decremento de q.ess[i] dejaba la suite entera verde mientras los
// esenciales volvían a apilarse.
//
// La cota se comprueba en cada paso, no al final.
func TestQueueStaysBoundedWithAConsumerRunning(t *testing.T) {
	const maxItems, maxBytes = 64, 8192
	q := newQueue(queueConfig{MaxBytes: maxBytes, MaxSpan: 1_000_000, MaxItems: maxItems})
	defer q.close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// El consumidor saca un mensaje por cada ficha. El productor le da una ficha cada dos
	// mensajes encolados, así que la cola se queda con backlog permanente: es ahí donde
	// los índices de los esenciales tienen que seguir siendo correctos.
	tokens := make(chan struct{})
	consumed := make(chan struct{})
	go func() {
		defer close(consumed)
		for range tokens {
			if _, ok := q.pop(ctx); !ok {
				return
			}
		}
	}()

	audioSeq := func() *Message {
		return &Message{Kind: KindAudio, Payload: []byte{0xAF, 0x00}, IsSeqHeader: true}
	}

	ts := uint32(0)
	for i := 0; i < 4000; i++ {
		// Esenciales de forma continuada, mezclados con la media que los rodea en
		// producción.
		switch i % 4 {
		case 0:
			q.push(metaSeq(i))
		case 1:
			q.push(vSeq())
		case 2:
			q.push(audioSeq())
		case 3:
			q.push(aRaw(ts, 120))
		}
		if i%5 == 0 {
			q.push(vKey(ts, 300))
		} else {
			q.push(vInter(ts, 150))
		}
		ts += 33

		tokens <- struct{}{} // dos dentro, uno fuera

		perClass, items, bytes := sampleQueue(q)
		for c, n := range perClass {
			if n > 1 {
				t.Fatalf("paso %d: hay %d esenciales de la clase %d en la cola; solo puede haber uno", i, n, c)
			}
		}
		if items > maxItems {
			t.Fatalf("paso %d: %d ítems con una cota de %d", i, items, maxItems)
		}
		if bytes > maxBytes*hardBytesFactor {
			t.Fatalf("paso %d: %d bytes con un límite duro de %d", i, bytes, maxBytes*hardBytesFactor)
		}
	}

	close(tokens)
	<-consumed

	// Y la garantía que no debía romperse: encolados los tres esenciales y saturada la
	// cola sin consumir nada, siguen dentro. No se apilan, pero tampoco se descartan por
	// presión.
	q.push(metaSeq(1_000_000))
	q.push(vSeq())
	q.push(audioSeq())
	for i := 0; i < 2000; i++ {
		q.push(aRaw(ts, 200))
		ts += 20
	}
	perClass, items, _ := sampleQueue(q)
	for c, n := range perClass {
		if n != 1 {
			t.Errorf("tras saturar la cola hay %d esenciales de la clase %d, quería 1", n, c)
		}
	}
	if items > maxItems {
		t.Errorf("%d ítems al terminar, con una cota de %d", items, maxItems)
	}
}
