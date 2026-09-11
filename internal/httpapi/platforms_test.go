package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// fakeProvider: flujo de dispositivo que termina tras `pendientes` sondeos.
type fakeProvider struct {
	mu         sync.Mutex
	pendientes int
	sondeos    int
	configured bool
	titulos    []string
	categorias []string
	fallaTitle error
	// lentoTitle hace que SetTitle tarde: sirve para ejercitar el plazo por destino de
	// POST /api/live/title.
	lentoTitle time.Duration
	// estados cuenta las llamadas a BeginAuth: cada una da un state distinto, para poder
	// tener varios flujos vivos a la vez sobre el mismo proveedor (tope I-2).
	estados int
	// id permite tener un segundo proveedor en el registro; vacío significa twitch.
	id platforms.ID
}

func (f *fakeProvider) ID() platforms.ID {
	if f.id != "" {
		return f.id
	}
	return platforms.Twitch
}
func (f *fakeProvider) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{Title: true, Category: true, ChatRead: true}
}
func (f *fakeProvider) Configured() bool { return f.configured }
func (f *fakeProvider) BeginAuth(context.Context, platforms.Credentials) (platforms.AuthPrompt, error) {
	if !f.configured {
		return platforms.AuthPrompt{}, platforms.ErrNoClientID
	}
	f.mu.Lock()
	f.estados++
	state := fmt.Sprintf("estado-%d", f.estados)
	f.mu.Unlock()
	return platforms.AuthPrompt{State: state, VerificationURI: "https://www.twitch.tv/activate", UserCode: "ABCDEFGH",
		DeviceCode: "secreto-dispositivo", ExpiresAt: time.Now().Add(30 * time.Minute), Interval: time.Millisecond}, nil
}
func (f *fakeProvider) PollAuth(context.Context, platforms.Credentials, platforms.AuthPrompt) (store.NewAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sondeos++
	if f.sondeos <= f.pendientes {
		return store.NewAccount{}, platforms.ErrAuthPending
	}
	if f.pendientes < 0 {
		return store.NewAccount{}, platforms.ErrAuthExpired
	}
	return store.NewAccount{Platform: store.PlatformTwitch, ExternalID: "141981764", DisplayName: "twitchdev",
		Scopes: []string{"user:read:chat"}, Tokens: store.Tokens{Access: "tok-acceso-fixture", Refresh: "tok-refresco-fixture",
			ExpiresAt: time.Now().Add(4 * time.Hour)}}, nil
}
func (f *fakeProvider) Refresh(context.Context, store.Account, platforms.Credentials, crypto.Secret) (store.Tokens, error) {
	return store.Tokens{}, nil
}
func (f *fakeProvider) Validate(context.Context, crypto.Secret) (platforms.Identity, error) {
	return platforms.Identity{}, nil
}
func (f *fakeProvider) SetTitle(ctx context.Context, acct store.Account, tok crypto.Secret, title string) error {
	if f.fallaTitle != nil {
		return f.fallaTitle
	}
	if f.lentoTitle > 0 {
		// Como cualquier cliente HTTP real: si el contexto muere antes, se abandona.
		select {
		case <-time.After(f.lentoTitle):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.titulos = append(f.titulos, acct.DisplayName+":"+title+":"+tok.Reveal())
	return nil
}
func (f *fakeProvider) SearchCategories(context.Context, crypto.Secret, string) ([]platforms.Category, error) {
	return []platforms.Category{{ID: "509670", Name: "Science & Technology", BoxArtURL: "https://x/144x192.jpg"}}, nil
}
func (f *fakeProvider) SetCategory(_ context.Context, acct store.Account, _ crypto.Secret, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.categorias = append(f.categorias, acct.DisplayName+":"+id)
	return nil
}

type tokensFalsos struct {
	db *store.DB
	c  *crypto.Cipher
}

func (t tokensFalsos) Token(ctx context.Context, id int64) (crypto.Secret, error) {
	tk, err := t.db.AccountTokens(ctx, t.c, id)
	return tk.Access, err
}

func servidorPlataformas(t *testing.T, p *fakeProvider) (*Server, *store.DB, []*http.Cookie) {
	t.Helper()
	srv, db := newTestServer(t, func(c *Config) {
		c.Platforms = platforms.NewRegistry(p)
		c.Tokens = tokensFalsos{db: c.DB, c: c.Cipher}
	})
	return srv, db, login(t, srv)
}

func TestPlatformsListsCapabilitiesAndConfiguration(t *testing.T) {
	srv, _, ck := servidorPlataformas(t, &fakeProvider{configured: false})
	rec := do(t, srv, ck, http.MethodGet, "/api/platforms", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out []platformDTO
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out) != 1 || out[0].ID != "twitch" || !out[0].Capabilities.Title || !out[0].Capabilities.Chat || out[0].Configured {
		t.Errorf("platforms = %+v", out)
	}
}

func TestAuthFlowPollsUntilDoneAndCreatesTheAccount(t *testing.T) {
	p := &fakeProvider{configured: true, pendientes: 2}
	srv, db, ck := servidorPlataformas(t, p)
	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/twitch/auth", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var inicio authStartDTO
	json.Unmarshal(rec.Body.Bytes(), &inicio)
	if inicio.State == "" || inicio.UserCode != "ABCDEFGH" || inicio.ExpiresIn <= 0 || strings.Contains(rec.Body.String(), "secreto-dispositivo") {
		t.Fatalf("inicio = %+v (%s)", inicio, rec.Body)
	}
	var estado authStatusDTO
	deadline := time.Now().Add(3 * time.Second)
	for estado.Status != "done" && time.Now().Before(deadline) {
		rec = do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/auth/"+inicio.State, "")
		json.Unmarshal(rec.Body.Bytes(), &estado)
		time.Sleep(10 * time.Millisecond)
	}
	if estado.Status != "done" || estado.Account == nil || estado.Account.DisplayName != "twitchdev" {
		t.Fatalf("estado = %+v", estado)
	}
	if strings.Contains(rec.Body.String(), "tok-acceso-fixture") {
		t.Error("la respuesta lleva el token")
	}
	cuentas, _ := db.Accounts(context.Background())
	if len(cuentas) != 1 || cuentas[0].Status != store.AccountStatusOK {
		t.Errorf("cuentas = %+v", cuentas)
	}
	// El flujo terminado ya se entregó una vez arriba —la propia consulta que lo vio
	// "done" lo borró—, así que esta consulta siguiente tiene que dar 404, no 200.
	rec = do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/auth/"+inicio.State, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("tras done: %d, quería 404", rec.Code)
	}
	if rec := do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/auth/inventado", ""); rec.Code != 404 {
		t.Errorf("state desconocido = %d", rec.Code)
	}
}

func TestAuthFlowReportsExpiredAndNoClientID(t *testing.T) {
	srv, _, ck := servidorPlataformas(t, &fakeProvider{configured: true, pendientes: -1})
	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/twitch/auth", "")
	var inicio authStartDTO
	json.Unmarshal(rec.Body.Bytes(), &inicio)
	var estado authStatusDTO
	deadline := time.Now().Add(3 * time.Second)
	for estado.Status != "expired" && time.Now().Before(deadline) {
		rec = do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/auth/"+inicio.State, "")
		json.Unmarshal(rec.Body.Bytes(), &estado)
		time.Sleep(10 * time.Millisecond)
	}
	if estado.Status != "expired" {
		t.Errorf("estado = %+v", estado)
	}

	srv2, _, ck2 := servidorPlataformas(t, &fakeProvider{configured: false})
	if rec := do(t, srv2, ck2, http.MethodPost, "/api/platforms/twitch/auth", ""); rec.Code != http.StatusConflict {
		t.Errorf("sin client_id = %d, quería 409: %s", rec.Code, rec.Body)
	}
	if rec := do(t, srv2, ck2, http.MethodPost, "/api/platforms/facebook/auth", ""); rec.Code != 404 {
		t.Errorf("plataforma sin proveedor = %d", rec.Code)
	}
}

func cuentaViaStore(t *testing.T, srv *Server, db *store.DB) *store.Account {
	t.Helper()
	a, err := db.UpsertAccount(context.Background(), srv.cipher, store.NewAccount{Platform: store.PlatformTwitch, ExternalID: "1",
		DisplayName: "twitchdev", Scopes: []string{"user:read:chat"},
		Tokens: store.Tokens{Access: "tok-acceso-fixture", Refresh: "r", ExpiresAt: time.Now().Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAccountsListAndDeleteWithoutLeakingTokens(t *testing.T) {
	srv, db, ck := servidorPlataformas(t, &fakeProvider{configured: true})
	a := cuentaViaStore(t, srv, db)
	d := crearDest(t, db, srv, "Twitch", "clave", true)
	patch := do(t, srv, ck, http.MethodPatch, "/api/destinations/"+itoa(d.ID), `{"platform":"twitch","account_id":`+itoa(a.ID)+`}`)
	if patch.Code != 200 {
		t.Fatalf("PATCH account_id: %d %s", patch.Code, patch.Body)
	}
	var dto destinationDTO
	json.Unmarshal(patch.Body.Bytes(), &dto)
	if dto.Account == nil || dto.Account.ID != a.ID || !dto.Capabilities.Title || !dto.Capabilities.Chat {
		t.Errorf("destino = %+v", dto)
	}

	rec := do(t, srv, ck, http.MethodGet, "/api/accounts", "")
	var cuentas []accountDTO
	json.Unmarshal(rec.Body.Bytes(), &cuentas)
	if len(cuentas) != 1 || cuentas[0].DisplayName != "twitchdev" || len(cuentas[0].Destinations) != 1 || cuentas[0].Destinations[0] != d.ID {
		t.Errorf("cuentas = %+v", cuentas)
	}
	if strings.Contains(rec.Body.String(), "tok-") {
		t.Error("la lista lleva tokens")
	}
	// Plataformas distintas → 400; desvincular con null.
	yt := crearDest(t, db, srv, "YouTube", "clave", true)
	if rec := do(t, srv, ck, http.MethodPatch, "/api/destinations/"+itoa(yt.ID), `{"platform":"youtube","account_id":`+itoa(a.ID)+`}`); rec.Code != 400 {
		t.Errorf("plataformas distintas = %d", rec.Code)
	}
	// El 400 no deja el patch a medias: la plataforma del destino sigue siendo la
	// original, no "youtube" (I-3: la validación va ANTES de tocar la base).
	if tras, err := db.DestinationByID(context.Background(), yt.ID); err != nil || tras.Platform != store.PlatformCustom {
		t.Errorf("yt.Platform tras el 400 = %v (err=%v), quería %q sin tocar", tras, err, store.PlatformCustom)
	}
	// Se reasigna la `rec` de fuera (sin `:=`): si se declarara una nueva aquí, el
	// Unmarshal de abajo seguiría leyendo la respuesta del GET /api/accounts de más
	// arriba en vez de la de este PATCH.
	rec = do(t, srv, ck, http.MethodPatch, "/api/destinations/"+itoa(d.ID), `{"account_id":null}`)
	if rec.Code != 200 {
		t.Errorf("desvincular = %d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(rec.Body.Bytes(), &dto)
	if dto.Account != nil {
		t.Error("sigue vinculado tras null")
	}

	if rec := do(t, srv, ck, http.MethodDelete, "/api/accounts/"+itoa(a.ID), ""); rec.Code != 204 {
		t.Errorf("DELETE = %d", rec.Code)
	}
	if rec := do(t, srv, ck, http.MethodDelete, "/api/accounts/"+itoa(a.ID), ""); rec.Code != 404 {
		t.Errorf("segundo DELETE = %d", rec.Code)
	}
	ev, _ := db.RecentEvents(context.Background(), 5)
	if len(ev) == 0 || ev[0].Kind != "account_disconnected" {
		t.Errorf("eventos = %+v", ev)
	}
}

// TestAuthFlowSweepRemovesExpiredEntriesRegardlessOfStatus cubre I-1: antes, la barrida
// solo miraba `pending`, así que un flujo ya terminado (done/expired/error) —que es como
// sondear() los deja mucho antes de que venza su código— nunca se borraba. Se inserta la
// entrada ya vencida directamente en el mapa (una de las dos formas que pedía la ronda de
// arreglo) para no depender de esperar de verdad.
func TestAuthFlowSweepRemovesExpiredEntriesRegardlessOfStatus(t *testing.T) {
	srv, _, ck := servidorPlataformas(t, &fakeProvider{configured: true})

	srv.auths.mu.Lock()
	srv.auths.flows["vencido"] = &authFlow{status: "done", expira: time.Now().Add(-2 * time.Hour), platform: platforms.Twitch}
	srv.auths.mu.Unlock()

	// Cualquier GET a /auth/{state} —incluso a un state que no existe— dispara la
	// barrida en handleAuthStatus.
	if rec := do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/auth/otro-inexistente", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("consulta de control = %d", rec.Code)
	}
	if rec := do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/auth/vencido", ""); rec.Code != http.StatusNotFound {
		t.Errorf("el flujo vencido (status=done) no se limpió: %d", rec.Code)
	}
}

// TestAuthFlowCapsLiveFlowsPerPlatform cubre I-2: con un proveedor que nunca resuelve el
// sondeo (PollAuth siempre "pendiente"), el noveno POST sobre la misma plataforma debe
// rechazarse con 409 en vez de arrancar una novena goroutine de sondeo.
func TestAuthFlowCapsLiveFlowsPerPlatform(t *testing.T) {
	p := &fakeProvider{configured: true, pendientes: 1 << 30} // "nunca" termina
	ctx, cancel := context.WithCancel(context.Background())
	srv, _ := newTestServer(t, func(c *Config) {
		c.Platforms = platforms.NewRegistry(p)
		c.Tokens = tokensFalsos{db: c.DB, c: c.Cipher}
		c.BaseContext = ctx
	})
	ck := login(t, srv)
	// Cancelar BaseContext y esperar a que las goroutines de sondeo salgan: si no, siguen
	// vivas sondeando cada pocos milisegundos durante el resto de la suite.
	t.Cleanup(func() {
		cancel()
		srv.Wait()
	})

	for i := 0; i < maxFlujosPorPlataforma; i++ {
		if rec := do(t, srv, ck, http.MethodPost, "/api/platforms/twitch/auth", ""); rec.Code != http.StatusOK {
			t.Fatalf("POST %d = %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/twitch/auth", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("noveno POST = %d, quería 409: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "demasiadas conexiones") {
		t.Errorf("mensaje = %s", rec.Body)
	}
}

// TestServerWaitReturnsAfterBaseContextCancelWithAPendingFlow cubre la última pata de I-2:
// Wait() no debe bloquear para siempre si BaseContext se cancela mientras un flujo sigue
// pendiente — es justo lo que hace main.go al apagar el proceso.
func TestServerWaitReturnsAfterBaseContextCancelWithAPendingFlow(t *testing.T) {
	p := &fakeProvider{configured: true, pendientes: 1 << 30}
	ctx, cancel := context.WithCancel(context.Background())
	srv, _ := newTestServer(t, func(c *Config) {
		c.Platforms = platforms.NewRegistry(p)
		c.Tokens = tokensFalsos{db: c.DB, c: c.Cipher}
		c.BaseContext = ctx
	})
	ck := login(t, srv)

	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/twitch/auth", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body)
	}
	var inicio authStartDTO
	json.Unmarshal(rec.Body.Bytes(), &inicio)

	rec = do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/auth/"+inicio.State, "")
	var estado authStatusDTO
	json.Unmarshal(rec.Body.Bytes(), &estado)
	if estado.Status != "pending" {
		t.Fatalf("estado = %+v, quería pending", estado)
	}

	cancel()
	done := make(chan struct{})
	go func() { srv.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Wait() no volvió tras cancelar BaseContext con un flujo pendiente")
	}
}

// Cambiar la plataforma del destino por la API suelta la cuenta vinculada: si no, el DTO
// seguiría anunciando una cuenta de Twitch en un destino de YouTube y el título en vivo
// iría a la plataforma equivocada.
func TestPatchDestinationPlatformUnlinksAccount(t *testing.T) {
	srv, db, ck := servidorPlataformas(t, &fakeProvider{configured: true})
	a := cuentaViaStore(t, srv, db)
	d := crearDest(t, db, srv, "Twitch", "clave", true)
	if rec := do(t, srv, ck, http.MethodPatch, "/api/destinations/"+itoa(d.ID),
		`{"platform":"twitch","account_id":`+itoa(a.ID)+`}`); rec.Code != 200 {
		t.Fatalf("vincular: %d %s", rec.Code, rec.Body)
	}

	rec := do(t, srv, ck, http.MethodPatch, "/api/destinations/"+itoa(d.ID), `{"platform":"youtube"}`)
	if rec.Code != 200 {
		t.Fatalf("cambiar de plataforma: %d %s", rec.Code, rec.Body)
	}
	var dto destinationDTO
	json.Unmarshal(rec.Body.Bytes(), &dto)
	if dto.Account != nil {
		t.Errorf("account = %+v tras cambiar a youtube, quería null", dto.Account)
	}
	if _, err := db.AccountForDestination(context.Background(), d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("el enlace sigue en la base: err = %v", err)
	}
}

// El estado de un flujo solo se consulta desde la plataforma en la que se inició:
// preguntar por otra ruta devuelve 404 y —lo importante— no se lleva por delante el flujo
// bueno, que sigue consultable donde le corresponde.
func TestAuthStatusChecksThePlatformOfTheFlow(t *testing.T) {
	tw := &fakeProvider{configured: true, pendientes: 2}
	kick := &fakeProvider{configured: true, id: platforms.Kick}
	srv, _ := newTestServer(t, func(c *Config) {
		c.Platforms = platforms.NewRegistry(tw, kick)
		c.Tokens = tokensFalsos{db: c.DB, c: c.Cipher}
	})
	ck := login(t, srv)

	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/twitch/auth", "")
	if rec.Code != 200 {
		t.Fatalf("inicio: %d %s", rec.Code, rec.Body)
	}
	var inicio authStartDTO
	json.Unmarshal(rec.Body.Bytes(), &inicio)
	if inicio.State == "" {
		t.Fatalf("sin state: %s", rec.Body)
	}

	if rec := do(t, srv, ck, http.MethodGet, "/api/platforms/kick/auth/"+inicio.State, ""); rec.Code != http.StatusNotFound {
		t.Errorf("consulta por kick = %d %s, quería 404", rec.Code, rec.Body)
	}
	// El 404 de kick no borró nada: por twitch sigue respondiendo.
	rec = do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/auth/"+inicio.State, "")
	if rec.Code != 200 {
		t.Fatalf("consulta por twitch = %d %s, quería 200", rec.Code, rec.Body)
	}
	var estado authStatusDTO
	json.Unmarshal(rec.Body.Bytes(), &estado)
	if estado.Status != "pending" && estado.Status != "done" {
		t.Errorf("estado = %+v", estado)
	}
}
