package alerts_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/alerts"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/events"
	"github.com/aprendomx/splitstream/internal/store"
)

func setup(t *testing.T) (*store.DB, *crypto.Cipher, *events.Bus) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var k [32]byte
	c, _ := crypto.NewCipher(k)
	if err := db.Bootstrap(ctx, c); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	db.SetEventHook(bus.Publish)
	return db, c, bus
}

// receptor captura lo que llega y responde lo que el test diga.
type receptor struct {
	mu       sync.Mutex
	cuerpos  [][]byte
	cabecera []http.Header
	codigos  []int // se consumen en orden; agotados, 200
}

func (r *receptor) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.cuerpos = append(r.cuerpos, b)
		r.cabecera = append(r.cabecera, req.Header.Clone())
		code := http.StatusOK
		if len(r.codigos) > 0 {
			code, r.codigos = r.codigos[0], r.codigos[1:]
		}
		r.mu.Unlock()
		w.WriteHeader(code)
	}
}

func (r *receptor) recibidos() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cuerpos)
}

func esperar(t *testing.T, plazo time.Duration, cond func() bool, msg string) {
	t.Helper()
	fin := time.Now().Add(plazo)
	for time.Now().Before(fin) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tiempo agotado: %s", msg)
}

func crearHook(t *testing.T, db *store.DB, c *crypto.Cipher, url string, ajustar func(*store.NewWebhook)) *store.Webhook {
	t.Helper()
	in := store.NewWebhook{Name: "h", URL: url, Format: store.WebhookJSON, MinLevel: store.LevelWarn, Enabled: true}
	if ajustar != nil {
		ajustar(&in)
	}
	w, err := db.CreateWebhook(context.Background(), c, in)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestDispatcherDeliversSignedEventsAboveMinLevel(t *testing.T) {
	db, c, bus := setup(t)
	rec := &receptor{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	crearHook(t, db, c, srv.URL, func(in *store.NewWebhook) { in.Secret = crypto.Secret("s3cr3t") })

	d := alerts.NewWebhookDispatcher(bus, db, c, nil, "v-test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// info: por debajo del mínimo, no viaja. warn: sí.
	if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelInfo, Kind: "ruido", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelWarn, Kind: "destination_disconnected", Message: "se cayó"}); err != nil {
		t.Fatal(err)
	}
	esperar(t, 5*time.Second, func() bool { return rec.recibidos() == 1 }, "llegó un envío")
	time.Sleep(200 * time.Millisecond)
	if rec.recibidos() != 1 {
		t.Fatalf("llegaron %d envíos, quería 1 (el info no pasa el mínimo)", rec.recibidos())
	}

	rec.mu.Lock()
	cuerpo, h := rec.cuerpos[0], rec.cabecera[0]
	rec.mu.Unlock()
	if !strings.Contains(string(cuerpo), `"kind":"destination_disconnected"`) {
		t.Errorf("cuerpo = %s", cuerpo)
	}
	if h.Get("X-Splitstream-Event") != "destination_disconnected" {
		t.Errorf("X-Splitstream-Event = %q", h.Get("X-Splitstream-Event"))
	}
	if !strings.HasPrefix(h.Get("User-Agent"), "splitstream/v-test") {
		t.Errorf("User-Agent = %q", h.Get("User-Agent"))
	}
	mac := hmac.New(sha256.New, []byte("s3cr3t"))
	mac.Write(cuerpo)
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); h.Get("X-Splitstream-Signature") != want {
		t.Errorf("firma = %q, quería %q", h.Get("X-Splitstream-Signature"), want)
	}

	esperar(t, 2*time.Second, func() bool {
		l, _ := db.ListWebhooks(ctx)
		return l[0].LastStatus != nil && *l[0].LastStatus == 200
	}, "se registró la entrega")
	ok, failed := d.Stats()
	if ok != 1 || failed != 0 {
		t.Errorf("stats = %d ok, %d failed", ok, failed)
	}
}

// Un 5xx se reintenta; un 4xx es configuración y no.
func TestDispatcherRetriesOn5xxButNotOn4xx(t *testing.T) {
	db, c, _ := setup(t)
	rec := &receptor{codigos: []int{503, 503, 200}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	w := crearHook(t, db, c, srv.URL, nil)

	d := alerts.NewWebhookDispatcher(nil, db, c, nil, "v")
	d.Backoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	ev := store.Event{ID: 1, Level: store.LevelWarn, Kind: "k", Message: "m"}
	if err := d.Send(context.Background(), *w, ev); err != nil {
		t.Fatalf("Send tras dos 503 y un 200 = %v", err)
	}
	if rec.recibidos() != 3 {
		t.Errorf("intentos = %d, quería 3", rec.recibidos())
	}

	rec2 := &receptor{codigos: []int{400}}
	srv2 := httptest.NewServer(rec2.handler())
	defer srv2.Close()
	w2 := crearHook(t, db, c, srv2.URL, nil)
	if err := d.Send(context.Background(), *w2, ev); err == nil {
		t.Error("Send con 400 = nil, quería error")
	}
	if rec2.recibidos() != 1 {
		t.Errorf("intentos con 400 = %d, quería 1", rec2.recibidos())
	}
	l, _ := db.ListWebhooks(context.Background())
	if *l[1].LastStatus != 400 || l[1].LastError == "" {
		t.Errorf("no se registró el 400: %+v", l[1])
	}
}

// Un endpoint que no responde no puede frenar al bus ni a los sinks: el plazo por intento
// acota, y el bus no bloquea.
func TestDispatcherDoesNotBlockTheBusOnASlowEndpoint(t *testing.T) {
	db, c, bus := setup(t)
	var atendidas atomic.Int32
	// El handler no contesta nunca: se queda ahí hasta que el cliente se rinde por plazo
	// (o hasta que acaba el test). Antes dormía 30 s, y como httptest.Server.Close espera
	// a los handlers vivos, el test entero duraba medio minuto.
	fin := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atendidas.Add(1)
		select {
		case <-r.Context().Done():
		case <-fin:
		}
	}))
	// Los defer corren en orden inverso: primero se sueltan los handlers, luego se cortan
	// las conexiones y solo al final Close, que ya no tiene a quién esperar.
	defer srv.Close()
	defer srv.CloseClientConnections()
	defer close(fin)
	crearHook(t, db, c, srv.URL, nil)

	d := alerts.NewWebhookDispatcher(bus, db, c, nil, "v")
	d.Timeout = 200 * time.Millisecond
	d.Backoff = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	inicio := time.Now()
	for i := 0; i < 50; i++ {
		if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelError, Kind: "k", Message: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(inicio); d > 2*time.Second {
		t.Fatalf("50 LogEvent tardaron %v con un webhook colgado", d)
	}
	esperar(t, 5*time.Second, func() bool { _, f := d.Stats(); return f >= 1 }, "algún envío falló por plazo")
}

// La URL de un webhook de Discord o de Slack ES el secreto: quien la tiene puede publicar
// en ese canal. Un fallo de red no puede filtrarla ni al error, ni al log, ni a la fila.
func TestSendDoesNotLeakTheURLOnANetworkError(t *testing.T) {
	db, c, _ := setup(t)
	const token = "TOKEN-INCONFUNDIBLE"
	// Puerto 1: cerrado en cualquier máquina, así que el fallo es de red y el *url.Error
	// que devuelve el cliente HTTP lleva la URL entera.
	w := crearHook(t, db, c, "http://127.0.0.1:1/hooks/"+token, nil)

	d := alerts.NewWebhookDispatcher(nil, db, c, nil, "v")
	d.Backoff = nil
	ev := store.Event{ID: 1, Level: store.LevelWarn, Kind: "k", Message: "m"}
	err := d.Send(context.Background(), *w, ev)
	if err == nil {
		t.Fatal("Send contra un puerto cerrado = nil, quería error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("el error devuelto lleva la URL del webhook: %v", err)
	}

	hooks, err := db.ListWebhooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hooks[0].LastError == "" {
		t.Fatal("no se registró el fallo en la fila del webhook")
	}
	if strings.Contains(hooks[0].LastError, token) {
		t.Errorf("last_error lleva la URL del webhook: %q", hooks[0].LastError)
	}
}

// Run no puede volver mientras quede un envío en vuelo: main espera a que vuelva justo
// antes de que corra su `defer db.Close()`, y todo envío apunta su resultado con
// RecordWebhookDelivery. Si Run se adelantara, esa escritura caería sobre una base cerrada.
func TestRunWaitsForInFlightSends(t *testing.T) {
	db, c, bus := setup(t)
	recibida := make(chan struct{})
	var unaVez sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unaVez.Do(func() { close(recibida) })
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	defer srv.CloseClientConnections()
	crearHook(t, db, c, srv.URL, nil)

	d := alerts.NewWebhookDispatcher(bus, db, c, nil, "v")
	d.Backoff = nil
	ctx, cancel := context.WithCancel(context.Background())
	vuelto := make(chan struct{})
	go func() { d.Run(ctx); close(vuelto) }()

	if _, err := db.LogEvent(context.Background(), store.Event{Level: store.LevelError, Kind: "k", Message: "m"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-recibida:
	case <-time.After(5 * time.Second):
		t.Fatal("el receptor no llegó a recibir el envío")
	}
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-vuelto:
	case <-time.After(5 * time.Second):
		t.Fatal("Run no volvió tras cancelar el contexto")
	}

	// Sin esperas ni reintentos a propósito: lo que main necesita es que esto ya esté
	// hecho en el instante en que Run vuelve.
	ok, failed := d.Stats()
	if ok+failed != 1 {
		t.Errorf("Stats = %d ok, %d failed al volver Run; quería el envío ya contabilizado", ok, failed)
	}
	hooks, err := db.ListWebhooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hooks[0].LastStatus == nil && hooks[0].LastError == "" {
		t.Error("Run volvió antes de que el envío registrara su resultado en la base")
	}
}
