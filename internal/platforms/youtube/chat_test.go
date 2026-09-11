package youtube_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/platforms/youtube"
	"github.com/aprendomx/splitstream/internal/store"
)

// servidorChat simula www.googleapis.com/youtube/v3 para las dos rutas que usa el lector
// de chat: GET /liveBroadcasts (buscar la emisión activa) y GET /liveChatMessages (el
// sondeo). Los dos son inyectables; por defecto no hay ninguna emisión activa ni ningún
// mensaje, para que un test que solo ejercita uno de los dos no tenga que pensar en el
// otro.
type servidorChat struct {
	*httptest.Server
	findCalls atomic.Int32
	chatCalls atomic.Int32

	findRes func(n int32) (int, []byte)
	chatRes func(n int32, pageToken string) (int, []byte)
}

func nuevoServidorChat(t *testing.T) *servidorChat {
	t.Helper()
	s := &servidorChat{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /liveBroadcasts", func(w http.ResponseWriter, r *http.Request) {
		n := s.findCalls.Add(1)
		if r.URL.Query().Get("broadcastStatus") != "active" || r.URL.Query().Get("mine") != "true" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"mal formado","errors":[{"reason":"invalidParameter"}]}}`))
			return
		}
		code, body := 200, []byte(`{"items":[]}`)
		if s.findRes != nil {
			code, body = s.findRes(n)
		}
		w.WriteHeader(code)
		w.Write(body)
	})
	mux.HandleFunc("GET /liveChatMessages", func(w http.ResponseWriter, r *http.Request) {
		n := s.chatCalls.Add(1)
		if r.URL.Query().Get("liveChatId") != "chat456" || r.URL.Query().Get("part") != "snippet,authorDetails" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"mal formado","errors":[{"reason":"invalidParameter"}]}}`))
			return
		}
		code, body := 200, []byte(`{"items":[]}`)
		if s.chatRes != nil {
			code, body = s.chatRes(n, r.URL.Query().Get("pageToken"))
		}
		w.WriteHeader(code)
		w.Write(body)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Server.Close)
	return s
}

func proveedorChat(s *servidorChat, opts youtube.Options) *youtube.Provider {
	opts.HTTPClient = s.Client()
	opts.APIBase = s.URL
	opts.OAuthBase = s.URL
	return youtube.New(opts)
}

func tokenFijo(secreto string) platforms.TokenSource {
	return func(ctx context.Context) (crypto.Secret, error) { return crypto.Secret(secreto), nil }
}

// recibir lee un mensaje de out con plazo, para que un lector de chat que se quedó
// callado por un bug no cuelgue la corrida entera.
func recibir(t *testing.T, out <-chan platforms.ChatMessage) platforms.ChatMessage {
	t.Helper()
	select {
	case m := <-out:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no llegó ningún mensaje de chat a tiempo")
	}
	return platforms.ChatMessage{}
}

func TestReadChatFindsTheActiveBroadcastAndRespectsPollingInterval(t *testing.T) {
	s := nuevoServidorChat(t)
	s.findRes = func(n int32) (int, []byte) { return 200, fixture(t, "broadcasts_active.json") }
	s.chatRes = func(n int32, pageToken string) (int, []byte) {
		if pageToken == "" {
			return 200, fixture(t, "chat_page1.json")
		}
		return 200, fixture(t, "chat_page2.json")
	}
	sinks, quota := cuentaCuota()
	p := proveedorChat(s, youtube.Options{Quota: quota, MinPoll: 20 * time.Millisecond})
	acct := cuentaYT()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan platforms.ChatMessage, 4)

	errCh := make(chan error, 1)
	go func() { errCh <- p.ReadChat(ctx, acct, tokenFijo("tok"), out) }()

	m1 := recibir(t, out)
	inicio := time.Now()
	m2 := recibir(t, out)
	transcurrido := time.Since(inicio)
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("ReadChat = %v, quería nil al cancelar", err)
	}

	if m1.Author != "Espectadora" || m1.AuthorID != "UCviewer" || m1.Text != "Hola desde YouTube" || m1.MessageID != "m1" {
		t.Errorf("m1 = %+v", m1)
	}
	if len(m1.Badges) != 1 || m1.Badges[0] != "moderator" {
		t.Errorf("m1.Badges = %v, quería [moderator]", m1.Badges)
	}
	if m2.Author != "Dueña" || m2.AuthorID != "UCowner" || m2.Text != "Hola de nuevo" || m2.MessageID != "m2" {
		t.Errorf("m2 = %+v", m2)
	}
	if len(m2.Badges) != 1 || m2.Badges[0] != "owner" {
		t.Errorf("m2.Badges = %v, quería [owner]", m2.Badges)
	}
	// chat_page1.json trae pollingIntervalMillis: 60 y MinPoll se inyectó a 20ms, así que
	// el piso no debe entrar en juego: el segundo sondeo tiene que esperar los 60ms del
	// fixture.
	if transcurrido < 60*time.Millisecond {
		t.Errorf("entre el primer y el segundo sondeo pasaron %s, quería >= 60ms (pollingIntervalMillis del fixture)", transcurrido)
	}
	// 1 (list, encontrar la emisión) + 5 (chat_list, primer sondeo) + 5 (chat_list,
	// segundo sondeo, ya cobrado: el gasto ocurre antes de que el mensaje llegue a out).
	if sinks[acct.ID] != 11 {
		t.Errorf("cuota = %d, quería 11 (1 list + 5 + 5 chat_list)", sinks[acct.ID])
	}
}

func TestReadChatPausesWhenTheBudgetIsReached(t *testing.T) {
	s := nuevoServidorChat(t)
	s.findRes = func(n int32) (int, []byte) { return 200, fixture(t, "broadcasts_active.json") }

	var pausas int
	var pausaUsed, pausaBudget int
	p := proveedorChat(s, youtube.Options{
		ChatBudget: func(accountID int64) (used, budget int, ok bool) { return 5996, 6000, true },
		OnChatPaused: func(acct store.Account, used, budget int) {
			pausas++
			pausaUsed, pausaBudget = used, budget
		},
	})
	acct := cuentaYT()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := make(chan platforms.ChatMessage, 1)

	err := p.ReadChat(ctx, acct, tokenFijo("tok"), out)
	if err == nil || !strings.Contains(err.Error(), "presupuesto") {
		t.Fatalf("ReadChat = %v, quería un error que contuviera «presupuesto»", err)
	}
	if pausas != 1 {
		t.Errorf("OnChatPaused se llamó %d veces, quería 1", pausas)
	}
	if pausaUsed != 5996 || pausaBudget != 6000 {
		t.Errorf("OnChatPaused(used=%d, budget=%d), quería (5996, 6000)", pausaUsed, pausaBudget)
	}
	// 5996+5 > 6000: no cabe, así que liveChatMessages.list nunca debió llamarse.
	if n := s.chatCalls.Load(); n != 0 {
		t.Errorf("liveChatMessages.list se llamó %d veces, quería 0 (el presupuesto no alcanza)", n)
	}
}

func TestReadChatWaitsForABroadcastThenGivesUp(t *testing.T) {
	s := nuevoServidorChat(t)
	s.findRes = func(n int32) (int, []byte) { return 200, []byte(`{"items":[]}`) }
	p := proveedorChat(s, youtube.Options{
		ChatNoBroadcastWait:   5 * time.Millisecond,
		ChatNoBroadcastGiveUp: 30 * time.Millisecond,
	})
	acct := cuentaYT()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := make(chan platforms.ChatMessage, 1)

	err := p.ReadChat(ctx, acct, tokenFijo("tok"), out)
	if err == nil || !strings.Contains(err.Error(), "no hay emisión activa") {
		t.Fatalf("ReadChat = %v, quería un error que contuviera «no hay emisión activa»", err)
	}
	if n := s.findCalls.Load(); n < 3 {
		t.Errorf("liveBroadcasts.list (active) se llamó %d veces, quería al menos 3", n)
	}
}

func TestReadChatStopsOnContext(t *testing.T) {
	s := nuevoServidorChat(t)
	s.findRes = func(n int32) (int, []byte) { return 200, fixture(t, "broadcasts_active.json") }
	s.chatRes = func(n int32, pageToken string) (int, []byte) { return 200, fixture(t, "chat_page1.json") }
	p := proveedorChat(s, youtube.Options{MinPoll: 20 * time.Millisecond})
	acct := cuentaYT()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan platforms.ChatMessage, 1)

	errCh := make(chan error, 1)
	go func() { errCh <- p.ReadChat(ctx, acct, tokenFijo("tok"), out) }()

	recibir(t, out) // el sondeo ya está en marcha
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ReadChat = %v, quería nil al cancelar el contexto", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadChat no volvió tras cancelar el contexto")
	}
}

func TestReadChatNeverPollsBelowTwoSeconds(t *testing.T) {
	s := nuevoServidorChat(t)
	s.findRes = func(n int32) (int, []byte) { return 200, fixture(t, "broadcasts_active.json") }
	s.chatRes = func(n int32, pageToken string) (int, []byte) {
		return 200, []byte(`{"nextPageToken":"tokx","pollingIntervalMillis":500,"items":[]}`)
	}
	var mu sync.Mutex
	var duraciones []time.Duration
	p := proveedorChat(s, youtube.Options{
		// No duerme de verdad: solo registra lo que se le pidió, como pide la resolución
		// de ambigüedad del brief para que este test no tarde 2s de verdad.
		Sleep: func(ctx context.Context, d time.Duration) error {
			mu.Lock()
			duraciones = append(duraciones, d)
			mu.Unlock()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		},
	})
	acct := cuentaYT()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan platforms.ChatMessage, 1)

	errCh := make(chan error, 1)
	go func() { errCh <- p.ReadChat(ctx, acct, tokenFijo("tok"), out) }()

	// Esperar a que se pida dormir al menos una vez (el sondeo, no la búsqueda: acá la
	// emisión se encuentra a la primera).
	limite := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(duraciones)
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(limite) {
			t.Fatal("nunca se pidió dormir entre sondeos")
		}
		time.Sleep(100 * time.Microsecond)
	}
	cancel()
	<-errCh

	mu.Lock()
	defer mu.Unlock()
	for _, d := range duraciones {
		if d < 2*time.Second {
			t.Errorf("se pidió dormir %s entre sondeos, quería >= 2s (MinPoll por defecto), aunque el fixture pida 500ms", d)
		}
	}
}
