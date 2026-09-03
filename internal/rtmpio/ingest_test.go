package rtmpio

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
)

// recorder captura lo que el servidor de ingesta entrega.
type recorder struct {
	mu       sync.Mutex
	msgs     []*relay.Message
	starts   int
	ends     int
	startErr error
	apps     []string
}

func (r *recorder) OnPublishStart(app, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	r.apps = append(r.apps, app)
	return r.startErr
}

// lastApp es la app con la que conectó el último publisher, o "" si no hubo ninguno.
func (r *recorder) lastApp() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.apps) == 0 {
		return ""
	}
	return r.apps[len(r.apps)-1]
}

func (r *recorder) OnMessage(msg *relay.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
}

func (r *recorder) OnPublishEnd() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends++
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

func TestClassifyVideoKeyframe(t *testing.T) {
	msg, err := classifyVideo(100, []byte{0x17, 0x01, 0, 0, 0})
	if err != nil {
		t.Fatalf("classifyVideo: %v", err)
	}
	if msg.Kind != relay.KindVideo {
		t.Errorf("Kind = %v", msg.Kind)
	}
	if !msg.IsKeyframe {
		t.Error("IsKeyframe = false")
	}
	if msg.IsSeqHeader {
		t.Error("IsSeqHeader = true")
	}
	if msg.Timestamp != 100 {
		t.Errorf("Timestamp = %d", msg.Timestamp)
	}
}

func TestClassifyVideoSequenceHeader(t *testing.T) {
	msg, err := classifyVideo(0, []byte{0x17, 0x00, 0, 0, 0})
	if err != nil {
		t.Fatalf("classifyVideo: %v", err)
	}
	if !msg.IsSeqHeader {
		t.Error("IsSeqHeader = false, quería true")
	}
}

// Enhanced-RTMP (HEVC/AV1) se rechaza: no se puede transcodificar y Twitch no lo acepta.
func TestClassifyVideoRejectsEnhancedRTMP(t *testing.T) {
	_, err := classifyVideo(0, []byte{0x90, 'h', 'v', 'c', '1'})
	if !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("classifyVideo con enhanced-RTMP = %v, quería ErrUnsupportedCodec", err)
	}
}

func TestClassifyVideoRejectsNonAVC(t *testing.T) {
	// codecID 2 = Sorenson H.263
	if _, err := classifyVideo(0, []byte{0x12, 0x00}); !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("classifyVideo con H.263 = %v, quería ErrUnsupportedCodec", err)
	}
}

func TestClassifyAudioAAC(t *testing.T) {
	msg, err := classifyAudio(50, []byte{0xAF, 0x01, 0x21})
	if err != nil {
		t.Fatalf("classifyAudio: %v", err)
	}
	if msg.Kind != relay.KindAudio {
		t.Errorf("Kind = %v", msg.Kind)
	}
	if msg.IsSeqHeader {
		t.Error("un frame raw no es sequence header")
	}
	if msg.Timestamp != 50 {
		t.Errorf("Timestamp = %d", msg.Timestamp)
	}
}

func TestClassifyAudioSequenceHeader(t *testing.T) {
	msg, err := classifyAudio(0, []byte{0xAF, 0x00, 0x12, 0x10})
	if err != nil {
		t.Fatalf("classifyAudio: %v", err)
	}
	if !msg.IsSeqHeader {
		t.Error("IsSeqHeader = false, quería true")
	}
}

func TestClassifyAudioRejectsNonAAC(t *testing.T) {
	// soundFormat 2 = MP3
	if _, err := classifyAudio(0, []byte{0x2F, 0x00}); !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("classifyAudio con MP3 = %v, quería ErrUnsupportedCodec", err)
	}
}

// El payload que llega al relay debe ser una copia: go-rtmp reutiliza sus buffers.
func TestClassifyCopiesPayload(t *testing.T) {
	src := []byte{0x17, 0x01, 0xAA, 0xBB}
	msg, err := classifyVideo(0, src)
	if err != nil {
		t.Fatalf("classifyVideo: %v", err)
	}
	src[2] = 0xFF
	if msg.Payload[2] == 0xFF {
		t.Error("el mensaje comparte memoria con el buffer de origen: hay que copiar")
	}
}

// Close debe cortar de verdad las publicaciones en curso, no solo dejar de aceptar
// conexiones nuevas: en un SIGTERM, un OBS conectado no puede quedarse publicando
// contra un proceso que ya se cree cerrado.
func TestCloseTerminatesActivePublish(t *testing.T) {
	rec := &recorder{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := NewIngest(IngestConfig{Addr: ln.Addr().String(), Handler: rec})
	go ing.Serve(ln)
	defer ing.Close()
	time.Sleep(200 * time.Millisecond)

	pub, err := NewPublisher(PublisherConfig{
		URL:       "rtmp://" + ln.Addr().String() + "/live",
		StreamKey: crypto.Secret("clave"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pub.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Confirmar que la publicación está viva antes de cerrar.
	if err := pub.WriteVideo(0, []byte{0x17, 0x01, 0x00}); err != nil {
		t.Fatalf("WriteVideo antes de Close: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		starts := rec.starts
		rec.mu.Unlock()
		if starts > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := ing.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Tras Close, el handler debe recibir su OnPublishEnd sin que el publisher haga nada.
	endDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(endDeadline) {
		rec.mu.Lock()
		ends := rec.ends
		rec.mu.Unlock()
		if ends > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("OnPublishEnd no se llamó tras Close: la conexión activa sobrevivió al cierre")
}

// Close sigue siendo seguro antes de Serve y llamado dos veces.
func TestCloseIsSafeBeforeServeAndIdempotent(t *testing.T) {
	ing := NewIngest(IngestConfig{Addr: "127.0.0.1:0", Handler: &recorder{}})
	if err := ing.Close(); err != nil {
		t.Errorf("Close antes de Serve = %v, quería nil", err)
	}
	if err := ing.Close(); err != nil {
		t.Errorf("segundo Close = %v, quería nil", err)
	}
}

// waitFor sondea hasta que cond se cumple o vence el plazo. Los tests de red no pueden
// afirmar de inmediato: entre el Close del socket y el OnPublishEnd hay un viaje real.
func waitFor(t *testing.T, plazo time.Duration, cond func() bool) bool {
	t.Helper()
	limite := time.Now().Add(plazo)
	for time.Now().Before(limite) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// serveIngest levanta la ingesta en un puerto libre y devuelve su dirección.
func serveIngest(t *testing.T, rec *recorder) (*Ingest, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := NewIngest(IngestConfig{Addr: ln.Addr().String(), Handler: rec})
	go ing.Serve(ln)
	time.Sleep(200 * time.Millisecond)
	return ing, ln.Addr().String()
}

// connectPublisher conecta un publisher y publica un frame para que la sesión exista.
func connectPublisher(t *testing.T, addr string) *Publisher {
	t.Helper()
	pub, err := NewPublisher(PublisherConfig{
		URL: "rtmp://" + addr + "/live", StreamKey: crypto.Secret("clave"),
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pub.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := pub.WriteVideo(0, []byte{0x17, 0x01, 0x00}); err != nil {
		t.Fatalf("WriteVideo: %v", err)
	}
	return pub
}

func starts(rec *recorder) int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.starts
}

func ends(rec *recorder) int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.ends
}

// TestDisconnectPublisherCutsTheStreamButKeepsListening: rotar la clave con disconnect_now
// debe echar a quien publica con la clave vieja y dejar el servidor listo para que vuelva
// a entrar con la nueva.
//
// Close() no sirve para esto: cierra también el listener, y entonces recuperar la ingesta
// exigiría reiniciar el proceso — justo lo que la rotación quiere evitar.
func TestDisconnectPublisherCutsTheStreamButKeepsListening(t *testing.T) {
	rec := &recorder{}
	ing, addr := serveIngest(t, rec)
	defer ing.Close()

	pub := connectPublisher(t, addr)
	defer pub.Close()

	if !waitFor(t, 2*time.Second, func() bool { return starts(rec) == 1 }) {
		t.Fatal("el primer publisher no llegó a arrancar sesión")
	}

	if n := ing.DisconnectPublisher(); n != 1 {
		t.Errorf("DisconnectPublisher = %d, quería 1", n)
	}

	if !waitFor(t, 3*time.Second, func() bool { return ends(rec) == 1 }) {
		t.Fatal("no llegó OnPublishEnd: la conexión sobrevivió al corte")
	}

	// Y lo que de verdad distingue esto de Close: se puede volver a entrar.
	pub2 := connectPublisher(t, addr)
	defer pub2.Close()

	if !waitFor(t, 2*time.Second, func() bool { return starts(rec) == 2 }) {
		t.Fatal("el segundo publisher no pudo conectar: el listener se cerró de más")
	}
}

// TestDisconnectPublisherWithNobodyConnected: no debe fallar ni colgarse, y devuelve 0.
func TestDisconnectPublisherWithNobodyConnected(t *testing.T) {
	rec := &recorder{}
	ing, _ := serveIngest(t, rec)
	defer ing.Close()

	if n := ing.DisconnectPublisher(); n != 0 {
		t.Errorf("DisconnectPublisher = %d, quería 0", n)
	}
}

// TestDisconnectPublisherBeforeServe: la API podría llamarlo antes de que la ingesta
// llegue a escuchar. No debe entrar en pánico por el mapa nil.
func TestDisconnectPublisherBeforeServe(t *testing.T) {
	ing := NewIngest(IngestConfig{Addr: "127.0.0.1:0", Handler: &recorder{}})
	if n := ing.DisconnectPublisher(); n != 0 {
		t.Errorf("DisconnectPublisher = %d, quería 0", n)
	}
}

// TestDisconnectPublisherIsSafeConcurrently: la API puede llamarlo justo mientras entran
// conexiones nuevas o se caen las viejas. Lo que se prueba es que NUESTRO registro de
// conexiones aguanta esa concurrencia bajo -race.
//
// Se usa net.Dial en crudo y no el Publisher completo a propósito. Con publishers reales,
// el test se colgaba: el ledger de la fase 2 dejó avisado que go-rtmp v0.0.7 tiene una
// carrera en su propio CLIENTE —entre (*streams).Delete y su goroutine de lectura— que
// aflora al llamar a Publisher.Close() sobre una conexión que ya murió por debajo. Ese es
// un bug de la librería, no nuestro, y provocarlo aquí solo conseguiría que la CI se
// cuelgue diez minutos sin decir nada útil. El handshake RTMP no aporta nada a lo que este
// test comprueba: track() registra la conexión en cuanto se acepta.
func TestDisconnectPublisherIsSafeConcurrently(t *testing.T) {
	rec := &recorder{}
	ing, addr := serveIngest(t, rec)
	defer ing.Close()

	fin := make(chan struct{})
	var wg sync.WaitGroup

	// Conexiones entrando y cayéndose sin parar.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-fin:
					return
				default:
				}
				c, err := net.DialTimeout("tcp", addr, time.Second)
				if err == nil {
					c.Close()
				}
				// Con pausa: sin ella, el bucle agota los puertos efímeros en TIME_WAIT
				// y el test falla con "can't assign requested address" por su propia
				// culpa, no por un fallo del código.
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}

	// Y cortes en paralelo.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-fin:
					return
				default:
				}
				ing.DisconnectPublisher()
				time.Sleep(time.Millisecond)
			}
		}()
	}

	time.Sleep(time.Second)
	close(fin)

	// Un plazo duro: si alguna goroutine se quedara bloqueada, este test debe FALLAR con
	// un mensaje, no colgar la corrida entera hasta el timeout del runner.
	hecho := make(chan struct{})
	go func() { wg.Wait(); close(hecho) }()
	select {
	case <-hecho:
	case <-time.After(10 * time.Second):
		t.Fatal("alguna goroutine se quedó bloqueada tras cerrar el canal")
	}

	// El servidor sigue en pie tras el vendaval: acepta y puede volver a cortar.
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("el listener no sobrevivió: %v", err)
	}
	c.Close()
}

// TestCloseStillWorksAfterDisconnectPublisher: el apagado ordenado de la fase 3 no puede
// romperse porque alguien haya rotado una clave antes.
func TestCloseStillWorksAfterDisconnectPublisher(t *testing.T) {
	rec := &recorder{}
	ing, addr := serveIngest(t, rec)

	pub := connectPublisher(t, addr)
	defer pub.Close()

	ing.DisconnectPublisher()
	if err := ing.Close(); err != nil {
		t.Errorf("Close tras DisconnectPublisher = %v, quería nil", err)
	}
	if err := ing.Close(); err != nil {
		t.Errorf("segundo Close = %v, quería nil", err)
	}
}
