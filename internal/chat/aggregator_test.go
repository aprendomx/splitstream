package chat_test

import (
	"context"
	"net/http"
	"path/filepath"
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
	arranques atomic.Int32
	paradas   atomic.Int32
	n         int
	// espera, si no es nil, hace que ReadChat bloquee antes de emitir sus n mensajes hasta
	// que se cierre el canal (o el contexto se cancele). Sirve para intercalar, en una
	// prueba, el cierre de la base entre el arranque del lector (cuentas ya listadas con
	// la base viva) y la escritura del lote: nil conserva el comportamiento de siempre,
	// emitir en cuanto arranca.
	espera chan struct{}
}

func (l *lectorFalso) ID() platforms.ID { return platforms.Twitch }
func (l *lectorFalso) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{ChatRead: true}
}
func (l *lectorFalso) Configured() bool { return true }
func (l *lectorFalso) BeginAuth(context.Context, platforms.Credentials) (platforms.AuthPrompt, error) {
	return platforms.AuthPrompt{}, nil
}
func (l *lectorFalso) PollAuth(context.Context, platforms.Credentials, platforms.AuthPrompt) (store.NewAccount, error) {
	return store.NewAccount{}, nil
}
func (l *lectorFalso) Refresh(context.Context, store.Account, platforms.Credentials, crypto.Secret) (store.Tokens, error) {
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
	if l.espera != nil {
		select {
		case <-l.espera:
		case <-ctx.Done():
			return nil
		}
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

// webhookFalso implementa Provider + ChatWebhook (Kick), pero NO ChatReader: arrancar no
// le lanza un lector, aunque cuentasConChat sí incluye su cuenta porque Capabilities.ChatRead
// es true. Es el doble que ejercita el camino de Ingest, sin depender de httpapi ni de un
// webhook real.
type webhookFalso struct{}

func (webhookFalso) ID() platforms.ID { return platforms.Kick }
func (webhookFalso) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{ChatRead: true, RequiresPublicURL: true}
}
func (webhookFalso) Configured() bool { return true }
func (webhookFalso) BeginAuth(context.Context, platforms.Credentials) (platforms.AuthPrompt, error) {
	return platforms.AuthPrompt{}, nil
}
func (webhookFalso) PollAuth(context.Context, platforms.Credentials, platforms.AuthPrompt) (store.NewAccount, error) {
	return store.NewAccount{}, nil
}
func (webhookFalso) Refresh(context.Context, store.Account, platforms.Credentials, crypto.Secret) (store.Tokens, error) {
	return store.Tokens{}, nil
}
func (webhookFalso) Validate(context.Context, crypto.Secret) (platforms.Identity, error) {
	return platforms.Identity{}, nil
}
func (webhookFalso) SubscribeChat(context.Context, store.Account, crypto.Secret, string) error {
	return nil
}
func (webhookFalso) UnsubscribeChat(context.Context, store.Account, crypto.Secret) error {
	return nil
}
func (webhookFalso) ParseWebhook(http.Header, []byte) ([]platforms.ChatMessage, error) {
	return nil, nil
}

// montarKick es montar pero con una cuenta de Kick y un webhookFalso registrado en vez del
// lectorFalso de Twitch: el destino de la prueba de Ingest, cuyo chat entra por webhook y
// no por ReadChat.
func montarKick(t *testing.T) (*store.DB, *events.Bus, *chat.Bus, *chat.Aggregator, *store.Account) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var k [32]byte
	c, _ := crypto.NewCipher(k)
	acct, _ := db.UpsertAccount(ctx, c, store.NewAccount{Platform: store.PlatformKick, ExternalID: "1", DisplayName: "uno",
		Tokens: store.Tokens{Access: "a"}})
	d, _ := db.CreateDestination(ctx, c, store.NewDestination{Name: "kk", Platform: store.PlatformKick, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})
	db.LinkDestination(ctx, d.ID, acct.ID)

	bus := events.NewBus()
	db.SetEventHook(bus.Publish)
	cb := chat.NewBus()
	ag := chat.NewAggregator(chat.Config{DB: db, Events: bus, Chat: cb, Registry: platforms.NewRegistry(webhookFalso{}),
		Tokens: fuenteFija{}, BatchEvery: 20 * time.Millisecond, BatchSize: 50})
	return db, bus, cb, ag, acct
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
	db, _, _, lector, ag, _ := montar(t, 30)
	// La señal retiene los 30 mensajes del lector hasta que la prueba la suelte: así el
	// cierre de la base cae entre que el lector arrancó (cuentas ya listadas con la base
	// viva) y que su lote se intenta insertar, que es el escenario que se quiere probar sin
	// dejarlo a una carrera contra Aggregator.arrancar.
	lector.espera = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx)
	sesion(t, db)
	esperar(t, func() bool { return lector.arranques.Load() == 1 }, "el lector no arrancó con la sesión")

	// Se cierra la base con el lector ya corriendo y se suelta la señal: sus 30 mensajes
	// llegan al lote, pero el INSERT falla porque la base ya no está.
	db.Close()
	close(lector.espera)

	esperar(t, func() bool {
		_, _, dropped := ag.Stats()
		return dropped == 30
	}, "los 30 mensajes del lote fallido deberían contarse como descartados")
}

func TestIngestFeedsTheSessionOrDropsWithoutOne(t *testing.T) {
	db, _, cb, ag, acct := montarKick(t)

	// Sin sesión viva: se descartan y se cuentan, no hay canal a donde meterlos.
	sinSesion := []platforms.ChatMessage{{Platform: platforms.Kick, AccountID: acct.ID, AuthorID: "u", Author: "v", Text: "hola", At: time.Now()}}
	if n := ag.Ingest(sinSesion); n != 0 {
		t.Errorf("Ingest sin sesión = %d, quería 0", n)
	}
	if _, _, dropped := ag.Stats(); dropped != uint64(len(sinSesion)) {
		t.Errorf("dropped = %d, quería %d", dropped, len(sinSesion))
	}

	// Con sesión: la cuenta de Kick tiene ChatWebhook, no ChatReader, así que arrancar no
	// le lanza un lector; aun así crea el canal y la escritora porque cuentasConChat la
	// cuenta (Capabilities.ChatRead == true).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx)
	ch, release := cb.Subscribe(256)
	defer release()
	sid := sesion(t, db)

	msgs := []platforms.ChatMessage{
		{Platform: platforms.Kick, AccountID: acct.ID, AuthorID: "u1", Author: "a1", Text: "hola1", At: time.Now()},
		{Platform: platforms.Kick, AccountID: acct.ID, AuthorID: "u2", Author: "a2", Text: "hola2", At: time.Now()},
		{Platform: platforms.Kick, AccountID: acct.ID, AuthorID: "u3", Author: "a3", Text: "hola3", At: time.Now()},
	}
	// El canal `in` se guarda bajo el mutex al arrancar la sesión, en una goroutine
	// aparte: se reintenta hasta que arrancar termine, sin dormir un tiempo fijo.
	// esperar puede volver a llamar a la condición después de que ya haya dado true (su
	// chequeo final), así que la condición se vuelve idempotente con `entregado`: sin
	// eso, Ingest metería los 3 mensajes dos veces.
	entregado := false
	esperar(t, func() bool {
		if entregado {
			return true
		}
		entregado = ag.Ingest(msgs) == len(msgs)
		return entregado
	}, "Ingest no entregó los mensajes a la sesión")

	recibidos := 0
	deadline := time.Now().Add(3 * time.Second)
	for recibidos < len(msgs) && time.Now().Before(deadline) {
		select {
		case m := <-ch:
			if m.SessionID != sid {
				t.Errorf("SessionID = %d", m.SessionID)
			}
			recibidos++
		case <-time.After(100 * time.Millisecond):
		}
	}
	if recibidos != len(msgs) {
		t.Fatalf("el bus repartió %d de %d", recibidos, len(msgs))
	}
	esperar(t, func() bool {
		got, _ := db.ChatMessages(context.Background(), sid, 0, 10)
		return len(got) == len(msgs)
	}, "no se persistieron los mensajes de Ingest")

	messages, _, _ := ag.Stats()
	if messages[platforms.Kick] != uint64(len(msgs)) {
		t.Errorf("Stats = %v, quería %d en kick", messages, len(msgs))
	}
}
