package twitch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/platforms/twitch"
)

// eventSubFalso es un servidor WebSocket que se comporta como EventSub: welcome, espera la
// suscripción por REST, y después manda lo que el test le diga.
type eventSubFalso struct {
	t          *testing.T
	srv        *httptest.Server
	mu         sync.Mutex
	conns      []*websocket.Conn
	subs       atomic.Int32
	sessionIDs []string
	subsBody   []map[string]any
	// guion: qué mandar tras el welcome, por conexión (índice = número de conexión).
	guion [][]string
	// cerrarCon: código con el que se cierra la PRIMERA conexión tras su guion; 0 = dejarla
	// abierta. Solo la primera, para que el test no tenga que apagarlo a tiempo (sería una
	// carrera con el arranque de ReadChat).
	cerrarCon int
}

func nuevoEventSub(t *testing.T) *eventSubFalso {
	e := &eventSubFalso{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		e.mu.Lock()
		n := len(e.conns)
		e.conns = append(e.conns, c)
		sid := "sesion-" + string(rune('A'+n))
		e.sessionIDs = append(e.sessionIDs, sid)
		e.mu.Unlock()
		ctx := c.CloseRead(context.Background())
		welcome := strings.Replace(string(fixture(t, "welcome.json")), "AQoQILE98gtqShGmLD7AM6yJThAB", sid, 1)
		// Twitch refleja en el welcome el keepalive que se pidió por query: los tests lo usan
		// para forzar el vencimiento sin esperar los 10 s por defecto.
		if k := r.URL.Query().Get("keepalive_timeout_seconds"); k != "" {
			welcome = strings.Replace(welcome, `"keepalive_timeout_seconds": 10`, `"keepalive_timeout_seconds": `+k, 1)
		}
		c.Write(ctx, websocket.MessageText, []byte(welcome))
		// Espera la suscripción (o 2 s) antes de seguir el guion.
		deadline := time.Now().Add(2 * time.Second)
		for e.subs.Load() < int32(n+1) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		e.mu.Lock()
		var pasos []string
		if n < len(e.guion) {
			pasos = e.guion[n]
		}
		cerrar := e.cerrarCon
		e.mu.Unlock()
		for _, p := range pasos {
			if p == "sleep" {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			msg := strings.Replace(string(fixture(t, p)), "wss://eventsub.wss.twitch.tv?...", "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/ws", 1)
			if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
				return
			}
		}
		if cerrar != 0 && n == 0 {
			c.Close(websocket.StatusCode(cerrar), "cierre del guion")
			return
		}
		<-ctx.Done()
	})
	mux.HandleFunc("POST /helix/eventsub/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-acceso-fixture" {
			w.WriteHeader(401)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.subsBody = append(e.subsBody, body)
		e.mu.Unlock()
		e.subs.Add(1)
		w.WriteHeader(202)
		w.Write([]byte(`{"data":[{"id":"sub-1","status":"enabled","type":"channel.chat.message","version":"1","cost":0}],"total":1,"total_cost":0,"max_total_cost":10}`))
	})
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func proveedorChat(t *testing.T, e *eventSubFalso) *twitch.Provider {
	return twitch.New(twitch.Options{ClientID: "cid", HTTPClient: e.srv.Client(), AuthBase: e.srv.URL, HelixBase: e.srv.URL,
		EventSubURL: "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/ws", ChatBackoffMax: 50 * time.Millisecond})
}

func tokenFijo(ctx context.Context) (crypto.Secret, error) { return "tok-acceso-fixture", nil }

func leer(t *testing.T, out <-chan platforms.ChatMessage, plazo time.Duration) platforms.ChatMessage {
	t.Helper()
	select {
	case m := <-out:
		return m
	case <-time.After(plazo):
		t.Fatal("no llegó ningún mensaje")
		return platforms.ChatMessage{}
	}
}

func TestReadChatSubscribesAfterWelcomeAndDeliversMessages(t *testing.T) {
	e := nuevoEventSub(t)
	e.guion = [][]string{{"keepalive.json", "chat_message.json"}}
	p := proveedorChat(t, e)
	out := make(chan platforms.ChatMessage, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hecho := make(chan error, 1)
	go func() { hecho <- p.ReadChat(ctx, cuenta(), tokenFijo, out) }()

	m := leer(t, out, 3*time.Second)
	if m.Platform != platforms.Twitch || m.AccountID != 7 || m.Author != "viewer32" || m.AuthorID != "4145994" ||
		m.Text != "Hi chat" || m.Color != "#00FF7F" || len(m.Badges) != 2 || m.Badges[0] != "moderator/1" ||
		m.MessageID != "cc106a89-1814-919d-454c-f4f2f970aae7" || m.At.IsZero() {
		t.Errorf("mensaje = %+v", m)
	}
	e.mu.Lock()
	sub := e.subsBody[0]
	e.mu.Unlock()
	cond := sub["condition"].(map[string]any)
	tr := sub["transport"].(map[string]any)
	if sub["type"] != "channel.chat.message" || sub["version"] != "1" || cond["broadcaster_user_id"] != "141981764" ||
		cond["user_id"] != "141981764" || tr["method"] != "websocket" || tr["session_id"] != "sesion-A" {
		t.Errorf("suscripción = %v", sub)
	}
	cancel()
	select {
	case err := <-hecho:
		if err != nil && ctx.Err() == nil {
			t.Errorf("ReadChat: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadChat no volvió al cancelar")
	}
}

// session_reconnect: se conecta a la URL nueva ANTES de cerrar la vieja, y la nueva
// conexión NO vuelve a suscribirse (Twitch conserva las suscripciones).
func TestReadChatFollowsReconnectWithoutResubscribing(t *testing.T) {
	e := nuevoEventSub(t)
	e.guion = [][]string{{"reconnect.json", "sleep"}, {"chat_message.json"}}
	p := proveedorChat(t, e)
	out := make(chan platforms.ChatMessage, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.ReadChat(ctx, cuenta(), tokenFijo, out)

	// La segunda conexión no se suscribe, así que el servidor falso agota su espera de 2 s
	// antes de mandar el mensaje: el plazo va holgado para -race con GOMAXPROCS=2.
	leer(t, out, 6*time.Second)
	e.mu.Lock()
	conns, subs := len(e.conns), e.subs.Load()
	e.mu.Unlock()
	if conns != 2 || subs != 1 {
		t.Errorf("conexiones = %d, suscripciones = %d; quería 2 y 1", conns, subs)
	}
}

// Sin keepalive dentro del plazo, se reconecta y se vuelve a suscribir.
func TestReadChatReconnectsWhenKeepaliveStops(t *testing.T) {
	e := nuevoEventSub(t)
	e.guion = [][]string{{}, {"chat_message.json"}}
	p := twitch.New(twitch.Options{ClientID: "cid", HTTPClient: e.srv.Client(), AuthBase: e.srv.URL, HelixBase: e.srv.URL,
		EventSubURL: "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/ws?keepalive_timeout_seconds=1", ChatBackoffMax: 50 * time.Millisecond,
		KeepaliveGrace: 200 * time.Millisecond})
	out := make(chan platforms.ChatMessage, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.ReadChat(ctx, cuenta(), tokenFijo, out)
	leer(t, out, 5*time.Second)
	if e.subs.Load() != 2 {
		t.Errorf("suscripciones = %d, quería 2 (una por conexión)", e.subs.Load())
	}
}

// revocation: se para sin reintentar y ReadChat devuelve ErrUnauthorized.
func TestReadChatStopsOnRevocation(t *testing.T) {
	e := nuevoEventSub(t)
	e.guion = [][]string{{"revocation.json"}}
	p := proveedorChat(t, e)
	out := make(chan platforms.ChatMessage, 8)
	err := p.ReadChat(context.Background(), cuenta(), tokenFijo, out)
	if err == nil || !strings.Contains(err.Error(), "revoc") {
		t.Errorf("err = %v, quería la revocación", err)
	}
}

// Cierre 4003 (sin suscripción a tiempo) u otro: reconexión con backoff, hasta ctx.
func TestReadChatRetriesOnServerClose(t *testing.T) {
	e := nuevoEventSub(t)
	e.guion = [][]string{{}, {"chat_message.json"}}
	e.cerrarCon = 4003
	p := proveedorChat(t, e)
	out := make(chan platforms.ChatMessage, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go p.ReadChat(ctx, cuenta(), tokenFijo, out)
	leer(t, out, 5*time.Second)
}

func TestReadChatNeedsATokenAndAClientID(t *testing.T) {
	e := nuevoEventSub(t)
	p := proveedorChat(t, e)
	err := p.ReadChat(context.Background(), cuenta(), func(context.Context) (crypto.Secret, error) {
		return "", platforms.ErrUnauthorized
	}, make(chan platforms.ChatMessage, 1))
	if err == nil {
		t.Error("sin token no debería empezar")
	}
	sinID := twitch.New(twitch.Options{})
	if err := sinID.ReadChat(context.Background(), cuenta(), tokenFijo, make(chan platforms.ChatMessage, 1)); err == nil {
		t.Error("sin client_id tampoco")
	}
}
