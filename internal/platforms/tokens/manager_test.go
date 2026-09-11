package tokens_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/platforms/tokens"
	"github.com/aprendomx/splitstream/internal/store"
)

// fakeProvider cuenta los refrescos y devuelve lo que se le diga.
type fakeProvider struct {
	mu        sync.Mutex
	refreshes atomic.Int32
	refreshFn func(refresh string) (store.Tokens, error)
	validate  func(access string) (platforms.Identity, error)
	lento     time.Duration
}

func (f *fakeProvider) ID() platforms.ID                     { return platforms.Twitch }
func (f *fakeProvider) Capabilities() platforms.Capabilities { return platforms.Capabilities{} }
func (f *fakeProvider) Configured() bool                     { return true }
func (f *fakeProvider) BeginAuth(context.Context) (platforms.AuthPrompt, error) {
	return platforms.AuthPrompt{}, nil
}
func (f *fakeProvider) PollAuth(context.Context, platforms.AuthPrompt) (store.NewAccount, error) {
	return store.NewAccount{}, nil
}
func (f *fakeProvider) Refresh(ctx context.Context, r crypto.Secret) (store.Tokens, error) {
	f.refreshes.Add(1)
	time.Sleep(f.lento)
	return f.refreshFn(r.Reveal())
}
func (f *fakeProvider) Validate(ctx context.Context, a crypto.Secret) (platforms.Identity, error) {
	if f.validate == nil {
		return platforms.Identity{}, nil
	}
	return f.validate(a.Reveal())
}

func montar(t *testing.T, expira time.Duration) (*store.DB, *crypto.Cipher, *store.Account, *fakeProvider, *tokens.Manager) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var k [32]byte
	c, _ := crypto.NewCipher(k)
	a, err := db.UpsertAccount(context.Background(), c, store.NewAccount{
		Platform: store.PlatformTwitch, ExternalID: "1", DisplayName: "uno", Scopes: []string{"x"},
		Tokens: store.Tokens{Access: "viejo", Refresh: "r1", ExpiresAt: time.Now().Add(expira)},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{refreshFn: func(r string) (store.Tokens, error) {
		return store.Tokens{Access: crypto.Secret("nuevo-" + r), Refresh: crypto.Secret("r-" + r), ExpiresAt: time.Now().Add(4 * time.Hour)}, nil
	}}
	m := tokens.NewManager(db, c, func(id platforms.ID) (platforms.Provider, bool) { return p, id == platforms.Twitch })
	return db, c, a, p, m
}

func TestTokenIsReturnedWithoutRefreshWhenItIsFresh(t *testing.T) {
	_, _, a, p, m := montar(t, 3*time.Hour)
	tok, err := m.Token(context.Background(), a.ID)
	if err != nil || tok.Reveal() != "viejo" {
		t.Fatalf("Token = %q, %v", tok.Reveal(), err)
	}
	if p.refreshes.Load() != 0 {
		t.Error("refrescó sin hacer falta")
	}
}

func TestTokenRefreshesWhenExpiringSoonAndPersistsBeforeReturning(t *testing.T) {
	db, c, a, p, m := montar(t, 2*time.Minute)
	tok, err := m.Token(context.Background(), a.ID)
	if err != nil || tok.Reveal() != "nuevo-r1" {
		t.Fatalf("Token = %q, %v", tok.Reveal(), err)
	}
	guardado, _ := db.AccountTokens(context.Background(), c, a.ID)
	if guardado.Access.Reveal() != "nuevo-r1" || guardado.Refresh.Reveal() != "r-r1" {
		t.Errorf("no se persistió el par nuevo: %+v", guardado)
	}
	if p.refreshes.Load() != 1 {
		t.Errorf("refrescos = %d", p.refreshes.Load())
	}
	// La segunda llamada ya no refresca.
	m.Token(context.Background(), a.ID)
	if p.refreshes.Load() != 1 {
		t.Error("refrescó dos veces")
	}
}

// Dos llamadas concurrentes con el token a punto de caducar: un solo refresco. Twitch
// rota el refresh token; dos refrescos en paralelo dejarían uno inválido.
func TestConcurrentTokenCallsShareOneRefresh(t *testing.T) {
	_, _, a, p, m := montar(t, time.Minute)
	p.lento = 50 * time.Millisecond
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, err := m.Token(context.Background(), a.ID); err != nil || tok.Reveal() != "nuevo-r1" {
				t.Errorf("Token = %q, %v", tok.Reveal(), err)
			}
		}()
	}
	wg.Wait()
	if p.refreshes.Load() != 1 {
		t.Errorf("refrescos = %d, quería 1", p.refreshes.Load())
	}
}

func TestFailedRefreshMarksReauthAndNotifies(t *testing.T) {
	db, _, a, p, m := montar(t, time.Minute)
	p.refreshFn = func(string) (store.Tokens, error) { return store.Tokens{}, platforms.ErrUnauthorized }
	var avisada atomic.Int64
	m.OnReauth = func(acct store.Account) { avisada.Store(acct.ID) }
	if _, err := m.Token(context.Background(), a.ID); !errors.Is(err, tokens.ErrReauth) {
		t.Fatalf("err = %v, quería ErrReauth", err)
	}
	got, _ := db.AccountByID(context.Background(), a.ID)
	if got.Status != store.AccountStatusReauth || avisada.Load() != a.ID {
		t.Errorf("status = %s, avisada = %d", got.Status, avisada.Load())
	}
	// Una cuenta en reauth no vuelve a intentar refrescar: devuelve ErrReauth directamente.
	m.Token(context.Background(), a.ID)
	if p.refreshes.Load() != 1 {
		t.Errorf("refrescos = %d; una cuenta en reauth no se reintenta sola", p.refreshes.Load())
	}
}

func TestRunValidatesHourlyAndRefreshesOn401(t *testing.T) {
	db, c, a, p, m := montar(t, 3*time.Hour)
	var validaciones atomic.Int32
	p.validate = func(access string) (platforms.Identity, error) {
		validaciones.Add(1)
		if access == "viejo" {
			return platforms.Identity{}, platforms.ErrUnauthorized
		}
		return platforms.Identity{ExternalID: "1"}, nil
	}
	ahora := time.Now()
	m.Now = func() time.Time { return ahora }
	m.Interval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	hecho := make(chan struct{})
	go func() { m.Run(ctx); close(hecho) }()
	deadline := time.Now().Add(3 * time.Second)
	for validaciones.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-hecho:
	case <-time.After(2 * time.Second):
		t.Fatal("Run no volvió al cancelar")
	}
	guardado, _ := db.AccountTokens(context.Background(), c, a.ID)
	if guardado.Access.Reveal() != "nuevo-r1" {
		t.Errorf("tras un 401 en la validación debería haber refrescado: %q", guardado.Access.Reveal())
	}
	if got, _ := db.AccountByID(context.Background(), a.ID); got.Status != store.AccountStatusOK {
		t.Errorf("status = %s", got.Status)
	}
}
