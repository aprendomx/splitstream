package chat_test

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/chat"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/events"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// lectorFalso implementa Provider + ChatReader: manda n mensajes y espera ctx.
type lectorFalso struct {
	mu        sync.Mutex
	arranques atomic.Int32
	paradas   atomic.Int32
	n         int
}

func (l *lectorFalso) ID() platforms.ID { return platforms.Twitch }
func (l *lectorFalso) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{ChatRead: true}
}
func (l *lectorFalso) Configured() bool { return true }
func (l *lectorFalso) BeginAuth(context.Context) (platforms.AuthPrompt, error) {
	return platforms.AuthPrompt{}, nil
}
func (l *lectorFalso) PollAuth(context.Context, platforms.AuthPrompt) (store.NewAccount, error) {
	return store.NewAccount{}, nil
}
func (l *lectorFalso) Refresh(context.Context, crypto.Secret) (store.Tokens, error) {
	return store.Tokens{}, nil
}
func (l *lectorFalso) Validate(context.Context, crypto.Secret) (platforms.Identity, error) {
	return platforms.Identity{}, nil
}
func (l *lectorFalso) ReadChat(ctx context.Context, acct store.Account, tok platforms.TokenSource, out chan<- platforms.ChatMessage) error {
	l.arranques.Add(1)
	defer l.paradas.Add(1)
	if _, err := tok(ctx); err != nil {
		return err
	}
	for i := 0; i < l.n; i++ {
		out <- platforms.ChatMessage{Platform: platforms.Twitch, AccountID: acct.ID, AuthorID: "u", Author: "v", Text: "m", At: time.Now()}
	}
	<-ctx.Done()
	return nil
}

type fuenteFija struct{}

func (fuenteFija) Source(int64) platforms.TokenSource {
	return func(context.Context) (crypto.Secret, error) { return "t", nil }
}

func montar(t *testing.T, n int) (*store.DB, *events.Bus, *chat.Bus, *lectorFalso, *chat.Aggregator, *store.Account) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var k [32]byte
	c, _ := crypto.NewCipher(k)
	acct, _ := db.UpsertAccount(ctx, c, store.NewAccount{Platform: store.PlatformTwitch, ExternalID: "1", DisplayName: "uno",
		Tokens: store.Tokens{Access: "a"}})
	d, _ := db.CreateDestination(ctx, c, store.NewDestination{Name: "tw", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})
	db.LinkDestination(ctx, d.ID, acct.ID)

	bus := events.NewBus()
	db.SetEventHook(bus.Publish)
	cb := chat.NewBus()
	lector := &lectorFalso{n: n}
	ag := chat.NewAggregator(chat.Config{DB: db, Events: bus, Chat: cb, Registry: platforms.NewRegistry(lector),
		Tokens: fuenteFija{}, BatchEvery: 20 * time.Millisecond, BatchSize: 50})
	return db, bus, cb, lector, ag, acct
}

func sesion(t *testing.T, db *store.DB) int64 {
	t.Helper()
	id, err := db.StartSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// El motor registra publisher_connected con la sesión; aquí se imita.
	db.LogEvent(context.Background(), store.Event{SessionID: &id, Level: store.LevelInfo, Kind: "publisher_connected", Message: "x"})
	return id
}

func esperar(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatal(msg)
	}
}

func TestAggregatorStartsWithTheSessionPersistsInBatchesAndStopsWithIt(t *testing.T) {
	db, _, cb, lector, ag, _ := montar(t, 120)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hecho := make(chan struct{})
	go func() { ag.Run(ctx); close(hecho) }()

	ch, release := cb.Subscribe(256)
	defer release()
	sid := sesion(t, db)
	esperar(t, func() bool { return lector.arranques.Load() == 1 }, "el lector no arrancó con la sesión")

	recibidos := 0
	deadline := time.Now().Add(3 * time.Second)
	for recibidos < 120 && time.Now().Before(deadline) {
		select {
		case m := <-ch:
			if m.SessionID != sid {
				t.Errorf("SessionID = %d", m.SessionID)
			}
			recibidos++
		case <-time.After(100 * time.Millisecond):
		}
	}
	if recibidos != 120 {
		t.Fatalf("el bus repartió %d de 120", recibidos)
	}
	esperar(t, func() bool {
		got, _ := db.ChatMessages(context.Background(), sid, 0, 1000)
		return len(got) == 120
	}, "no se persistieron los 120 mensajes")
	if len(cb.Recent()) != 50 {
		t.Errorf("Recent = %d, quería 50", len(cb.Recent()))
	}
	msgs, _, _ := ag.Stats()
	if msgs[platforms.Twitch] != 120 {
		t.Errorf("Stats = %v", msgs)
	}

	db.LogEvent(context.Background(), store.Event{SessionID: &sid, Level: store.LevelInfo, Kind: "publisher_disconnected", Message: "x"})
	esperar(t, func() bool { return lector.paradas.Load() == 1 }, "el lector no paró al terminar la sesión")

	cancel()
	select {
	case <-hecho:
	case <-time.After(2 * time.Second):
		t.Fatal("Run no volvió")
	}
}

func TestAggregatorIgnoresAccountsWithoutEnabledLinkedDestinationOrInReauth(t *testing.T) {
	db, _, _, lector, ag, acct := montar(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx)
	db.SetAccountStatus(context.Background(), acct.ID, store.AccountStatusReauth)
	sid := sesion(t, db)
	time.Sleep(150 * time.Millisecond)
	if lector.arranques.Load() != 0 {
		t.Error("una cuenta en reauth no lee chat")
	}
	db.LogEvent(context.Background(), store.Event{SessionID: &sid, Level: store.LevelInfo, Kind: "publisher_disconnected", Message: "x"})
	db.SetAccountStatus(context.Background(), acct.ID, store.AccountStatusOK)
	// Destino apagado: tampoco.
	dests, _ := db.ListDestinations(context.Background())
	off := false
	db.UpdateDestination(context.Background(), nil, dests[0].ID, store.DestinationPatch{Enabled: &off})
	sesion(t, db)
	time.Sleep(150 * time.Millisecond)
	if lector.arranques.Load() != 0 {
		t.Error("un destino apagado no arranca el chat")
	}
}

func TestAggregatorDropsWhenTheStoreFailsInsteadOfBlocking(t *testing.T) {
	db, _, _, _, ag, _ := montar(t, 30)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx)
	sid := sesion(t, db)
	// Se cierra la base debajo del agregador: los lotes fallan, se cuentan y nada se cuelga.
	db.Close()
	time.Sleep(200 * time.Millisecond)
	_, _, dropped := ag.Stats()
	if dropped == 0 {
		t.Error("los mensajes no persistidos deberían contarse como descartados")
	}
	_ = sid
}
