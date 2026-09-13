package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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
	// rechaza hace que PollAuth devuelva ErrAuthDenied al primer sondeo: la persona dijo
	// que no en la plataforma.
	rechaza    bool
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

	// Las capacidades opcionales se piden por bandera y las monta envolverProveedor: en Go
	// un tipo implementa una interfaz o no la implementa, así que "a veces sí" solo se
	// puede expresar componiendo tipos distintos (ver capRedirect y compañía).
	redirect       bool
	requiresOwnApp bool
	webhook        bool
	schedule       bool
	ingest         bool

	// Lo que registra el flujo con redirect.
	redirectURI string
	state       string
	creds       platforms.Credentials
	completados int
	// entroCanje avisa de que CompleteRedirect empezó y liberaCanje lo deja terminar: con
	// los dos, un test puede tener dos callbacks dentro del mismo canje a la vez.
	entroCanje  chan struct{}
	liberaCanje chan struct{}

	// Lo que registra el chat por webhook.
	suscripciones   int
	desuscripciones int
	webhookURL      string
	fallaSubscribe  error

	// Lo que registran las emisiones.
	emisiones      int
	peticiones     []platforms.BroadcastRequest
	arrancadas     int
	cerradas       int
	titulosEmision []string
	fallaEmision   error
	// trasEmision se llama cuando CreateBroadcast ya devolvió, sin el candado puesto: deja
	// que un test sabotee la base justo entre la llamada a la plataforma y las escrituras
	// del handler, que es donde viven los fallos a medias.
	trasEmision func()
}

// envolverProveedor compone el proveedor con las capacidades que pidan sus banderas. Los
// tests que no piden ninguna reciben el fakeProvider pelado, que es lo que ya esperaban.
func envolverProveedor(f *fakeProvider) platforms.Provider {
	switch {
	case f.redirect && f.webhook:
		return provRedirectWebhook{f, capRedirect{f}, capWebhook{f}}
	case f.redirect:
		return provRedirect{f, capRedirect{f}}
	case f.webhook:
		return provWebhook{f, capWebhook{f}}
	case f.schedule:
		return provSchedule{f, capSchedule{f}}
	case f.ingest:
		return provIngest{f, capIngest{f}}
	}
	return f
}

type (
	provRedirect struct {
		*fakeProvider
		capRedirect
	}
	provWebhook struct {
		*fakeProvider
		capWebhook
	}
	provRedirectWebhook struct {
		*fakeProvider
		capRedirect
		capWebhook
	}
	provSchedule struct {
		*fakeProvider
		capSchedule
	}
	provIngest struct {
		*fakeProvider
		capIngest
	}
)

// capRedirect es platforms.RedirectAuth: guarda la redirect_uri y el state que le llegan
// —el test los necesita para simular la vuelta del navegador— y solo acepta el código "ok".
type capRedirect struct{ p *fakeProvider }

func (c capRedirect) BeginRedirect(_ context.Context, creds platforms.Credentials, redirectURI, state string) (platforms.AuthPrompt, error) {
	if !c.p.configured {
		return platforms.AuthPrompt{}, platforms.ErrNoClientID
	}
	c.p.mu.Lock()
	c.p.redirectURI, c.p.state, c.p.creds = redirectURI, state, creds
	c.p.mu.Unlock()
	return platforms.AuthPrompt{State: state, RedirectURL: "https://kick.test/authorize?state=" + url.QueryEscape(state),
		CodeVerifier: "verificador-secreto", RedirectURI: redirectURI,
		ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}

func (c capRedirect) CompleteRedirect(ctx context.Context, creds platforms.Credentials, _ platforms.AuthPrompt, code string) (store.NewAccount, error) {
	c.p.mu.Lock()
	c.p.completados++
	entro, libera := c.p.entroCanje, c.p.liberaCanje
	c.p.mu.Unlock()
	if entro != nil {
		entro <- struct{}{}
		select {
		case <-libera:
		case <-ctx.Done():
			return store.NewAccount{}, ctx.Err()
		}
	}
	if code != "ok" {
		return store.NewAccount{}, errors.New("la plataforma rechazó el código")
	}
	return store.NewAccount{Platform: store.Platform(c.p.ID()), ExternalID: "123", DisplayName: "kickdev",
		Scopes: []string{"chat:read"}, OwnApp: creds.ClientID.Reveal() != "", Credentials: creds,
		Tokens: store.Tokens{Access: "tok-acceso-fixture", Refresh: "tok-refresco-fixture",
			ExpiresAt: time.Now().Add(time.Hour)}}, nil
}

// capWebhook es platforms.ChatWebhook. ParseWebhook decide por una cabecera de prueba: "ok"
// trae un mensaje del canal 123, "bad" lo rechaza y cualquier otra cosa es un evento que
// no nos interesa.
type capWebhook struct{ p *fakeProvider }

func (c capWebhook) SubscribeChat(_ context.Context, _ store.Account, _ crypto.Secret, webhookURL string) error {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	c.p.suscripciones++
	c.p.webhookURL = webhookURL
	return c.p.fallaSubscribe
}

func (c capWebhook) UnsubscribeChat(context.Context, store.Account, crypto.Secret) error {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	c.p.desuscripciones++
	return nil
}

func (c capWebhook) ParseWebhook(hdr http.Header, _ []byte) ([]platforms.ChatMessage, error) {
	switch hdr.Get("X-Test-Signature") {
	case "ok":
		return []platforms.ChatMessage{{Platform: platforms.Kick, BroadcasterID: "123", AuthorID: "a1",
			Author: "alguien", Text: "hola", At: time.Now()}}, nil
	case "bad":
		return nil, fmt.Errorf("%w: firma inválida", platforms.ErrWebhookRejected)
	case "lote":
		// Un lote más grande de lo que se acepta de una entrega.
		msgs := make([]platforms.ChatMessage, maxMensajesPorEntrega+5)
		for i := range msgs {
			msgs[i] = platforms.ChatMessage{Platform: platforms.Kick, BroadcasterID: "123",
				AuthorID: "a1", Author: "alguien", Text: "hola", At: time.Now()}
		}
		return msgs, nil
	}
	return nil, nil
}

// capSchedule es platforms.BroadcastScheduler (y el título por emisión que lo acompaña).
// Cada emisión creada devuelve valores distintos: así un test puede comprobar que la
// segunda sustituyó de verdad a la primera.
type capSchedule struct{ p *fakeProvider }

func (c capSchedule) CreateBroadcast(_ context.Context, _ store.Account, _ crypto.Secret, req platforms.BroadcastRequest) (platforms.Broadcast, error) {
	if c.p.trasEmision != nil {
		defer c.p.trasEmision()
	}
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	c.p.peticiones = append(c.p.peticiones, req)
	if c.p.fallaEmision != nil {
		return platforms.Broadcast{}, c.p.fallaEmision
	}
	c.p.emisiones++
	if c.p.emisiones > 1 {
		n := strconv.Itoa(c.p.emisiones)
		return platforms.Broadcast{Ref: "b" + n, StreamRef: "s" + n, LiveChatID: "c" + n,
			IngestURL: "rtmp://b.rtmp.youtube.com/live2", Key: crypto.Secret("clave-api-" + n),
			WatchURL: "https://www.youtube.com/watch?v=b" + n}, nil
	}
	return platforms.Broadcast{Ref: "b1", StreamRef: "s1", LiveChatID: "c1",
		IngestURL: "rtmp://a.rtmp.youtube.com/live2", Key: "clave-api",
		WatchURL: "https://www.youtube.com/watch?v=b1"}, nil
}

func (c capSchedule) StartBroadcast(_ context.Context, _ store.Account, _ crypto.Secret, ref string) error {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	if ref == "" {
		return platforms.ErrNoBroadcast
	}
	c.p.arrancadas++
	return nil
}

func (c capSchedule) EndBroadcast(_ context.Context, _ store.Account, _ crypto.Secret, ref string) error {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	if ref == "" {
		return platforms.ErrNoBroadcast
	}
	c.p.cerradas++
	return nil
}

func (c capSchedule) SetBroadcastTitle(_ context.Context, _ store.Account, _ crypto.Secret, ref, title string) error {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	c.p.titulosEmision = append(c.p.titulosEmision, ref+":"+title)
	return nil
}

// capIngest es platforms.IngestKeyProvider: la clave es del canal, sin emisión que crear.
type capIngest struct{ p *fakeProvider }

func (c capIngest) IngestKey(context.Context, store.Account, crypto.Secret) (string, crypto.Secret, error) {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	if c.p.fallaEmision != nil {
		return "", "", c.p.fallaEmision
	}
	c.p.emisiones++
	return "rtmps://stream.kick.com/1", "kick-key", nil
}

func (f *fakeProvider) ID() platforms.ID {
	if f.id != "" {
		return f.id
	}
	return platforms.Twitch
}
func (f *fakeProvider) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{Title: true, Category: true, ChatRead: true,
		Schedule: f.schedule, IngestKey: f.ingest,
		RequiresOwnApp: f.requiresOwnApp, RequiresPublicURL: f.webhook}
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
	if f.rechaza {
		return store.NewAccount{}, fmt.Errorf("%w: access_denied", platforms.ErrAuthDenied)
	}
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

func servidorPlataformas(t *testing.T, p *fakeProvider, ajustes ...func(*Config)) (*Server, *store.DB, []*http.Cookie) {
	t.Helper()
	srv, db := newTestServer(t, func(c *Config) {
		c.Platforms = platforms.NewRegistry(envolverProveedor(p))
		c.Tokens = tokensFalsos{db: c.DB, c: c.Cipher}
		for _, ajustar := range ajustes {
			ajustar(c)
		}
	})
	return srv, db, login(t, srv)
}

// estadoFirmado devuelve el state que el proveedor recibió en BeginRedirect: es lo que la
// plataforma le devolvería al navegador en el callback.
func (f *fakeProvider) estadoFirmado() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
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

// TestAuthFlowTerminaSiLaPersonaRechaza: ErrAuthDenied es definitivo. Antes volvía como
// error pelado, `sondear` lo tomaba por transitorio y seguía preguntando: el panel se
// quedaba «esperando a que autorices…» hasta que venciera el código, media hora después.
func TestAuthFlowTerminaSiLaPersonaRechaza(t *testing.T) {
	f := &fakeProvider{configured: true, rechaza: true}
	srv, _, ck := servidorPlataformas(t, f)
	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/twitch/auth", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var inicio authStartDTO
	json.Unmarshal(rec.Body.Bytes(), &inicio)

	var estado authStatusDTO
	deadline := time.Now().Add(3 * time.Second)
	for estado.Status == "" || estado.Status == "pending" {
		if !time.Now().Before(deadline) {
			t.Fatalf("el flujo seguía en %q: un rechazo no debe esperar a que venza el código", estado.Status)
		}
		rec = do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/auth/"+inicio.State, "")
		json.Unmarshal(rec.Body.Bytes(), &estado)
		time.Sleep(10 * time.Millisecond)
	}
	if estado.Status != "error" {
		t.Errorf("estado = %q, quería \"error\"", estado.Status)
	}
	if estado.Message != "rechazaste la autorización" {
		t.Errorf("mensaje = %q", estado.Message)
	}
	if estado.Account != nil {
		t.Errorf("un rechazo no debe traer cuenta: %+v", estado.Account)
	}
	// Y no siguió sondeando después de la negativa.
	f.mu.Lock()
	sondeos := f.sondeos
	f.mu.Unlock()
	if sondeos != 1 {
		t.Errorf("sondeos = %d, quería 1", sondeos)
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

// --- v0.12: credenciales propias, flujo con redirect y callback público ---

// cuotaFalsa es un QuotaReader de mentira: devuelve siempre lo mismo.
type cuotaFalsa struct{ n int }

func (c cuotaFalsa) UsedToday(context.Context, int64) (int, error) { return c.n, nil }

// Una plataforma cuya app la pone el usuario (YouTube, Kick) no puede empezar el flujo sin
// las credenciales: sin ellas la petición a la plataforma fallaría con un error suyo que no
// explica nada.
func TestAuthStartDemandsOwnAppCredentials(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.Kick, redirect: true, requiresOwnApp: true}
	srv, _, ck := servidorPlataformas(t, p)

	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/kick/auth", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("sin credenciales = %d, quería 400: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "credenciales de tu propia app") {
		t.Errorf("mensaje = %s", rec.Body)
	}
	// Con una sola de las dos tampoco.
	rec = do(t, srv, ck, http.MethodPost, "/api/platforms/kick/auth", `{"client_id":"ci","origin":"http://localhost:5173"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("solo client_id = %d, quería 400: %s", rec.Code, rec.Body)
	}
}

// El flujo con redirect entero: el panel pide la URL de autorización, el navegador vuelve
// por el callback —que es PÚBLICO, sin cookie— y el panel se entera consultando el estado
// del flujo como en el de dispositivo.
func TestRedirectAuthFlowCompletesThroughThePublicCallback(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.Kick, redirect: true, requiresOwnApp: true}
	srv, db, ck := servidorPlataformas(t, p)

	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/kick/auth",
		`{"client_id":"ci","client_secret":"ksec","origin":"http://localhost:5173"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("inicio = %d %s", rec.Code, rec.Body)
	}
	var inicio authStartDTO
	json.Unmarshal(rec.Body.Bytes(), &inicio)
	if inicio.State == "" || !strings.Contains(inicio.RedirectURL, "kick.test/authorize") {
		t.Fatalf("inicio = %+v", inicio)
	}
	// Ni el secreto de la app ni el de PKCE salen por la API.
	if strings.Contains(rec.Body.String(), "ksec") || strings.Contains(rec.Body.String(), "verificador-secreto") {
		t.Errorf("la respuesta del inicio lleva un secreto: %s", rec.Body)
	}
	// El flujo se guarda bajo su propio identificador, no bajo el state firmado.
	if inicio.State == p.estadoFirmado() {
		t.Error("el state firmado se está usando como clave del flujo")
	}
	if p.redirectURI != "http://localhost:5173/api/platforms/kick/callback" {
		t.Errorf("redirect_uri = %q", p.redirectURI)
	}

	// La vuelta del navegador: sin cookie, porque puede ser otro navegador.
	cb := do(t, srv, nil, http.MethodGet,
		"/api/platforms/kick/callback?state="+url.QueryEscape(p.estadoFirmado())+"&code=ok", "")
	if cb.Code != http.StatusOK {
		t.Fatalf("callback = %d %s", cb.Code, cb.Body)
	}
	if ct := cb.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	if csp := cb.Header().Get("Content-Security-Policy"); csp != "default-src 'none'" {
		t.Errorf("CSP = %q", csp)
	}
	cuerpo := cb.Body.String()
	if !strings.Contains(cuerpo, "Cuenta conectada") || strings.Contains(cuerpo, "<script") {
		t.Errorf("página = %s", cuerpo)
	}
	if strings.Contains(cuerpo, "tok-acceso-fixture") || strings.Contains(cuerpo, "ksec") {
		t.Errorf("la página lleva un secreto: %s", cuerpo)
	}

	// Y el panel, que solo sondea el estado, ve la cuenta conectada con su app propia.
	rec = do(t, srv, ck, http.MethodGet, "/api/platforms/kick/auth/"+inicio.State, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("estado = %d %s", rec.Code, rec.Body)
	}
	var estado authStatusDTO
	json.Unmarshal(rec.Body.Bytes(), &estado)
	if estado.Status != "done" || estado.Account == nil || estado.Account.DisplayName != "kickdev" || !estado.Account.OwnApp {
		t.Fatalf("estado = %+v (%+v)", estado, estado.Account)
	}
	if strings.Contains(rec.Body.String(), "tok-") || strings.Contains(rec.Body.String(), "ksec") {
		t.Errorf("el estado lleva un secreto: %s", rec.Body)
	}
	cuentas, _ := db.Accounts(context.Background())
	if len(cuentas) != 1 || !cuentas[0].OwnApp || cuentas[0].Platform != store.PlatformKick {
		t.Errorf("cuentas = %+v", cuentas)
	}
	// Sin URL pública no se suscribe el chat, aunque la plataforma sepa hacerlo.
	if p.suscripciones != 0 {
		t.Errorf("suscripciones = %d sin URL pública", p.suscripciones)
	}

	// El resto de las rutas de plataformas siguen pidiendo sesión: la pública es SOLO el
	// callback.
	if rec := do(t, srv, nil, http.MethodGet, "/api/platforms", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/platforms sin cookie = %d, quería 401", rec.Code)
	}
}

// Un state que no lleva nuestra firma —o que la lleva pero ya venció— no vale, y el flujo
// que estaba esperando NO se toca: sigue pendiente para el callback bueno.
func TestCallbackRejectsTamperedAndExpiredState(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.Kick, redirect: true}
	srv, _, ck := servidorPlataformas(t, p)

	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/kick/auth", `{"origin":"http://localhost:5173"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("inicio = %d %s", rec.Code, rec.Body)
	}
	var inicio authStartDTO
	json.Unmarshal(rec.Body.Bytes(), &inicio)

	manipulado := p.estadoFirmado() + "x"
	cb := do(t, srv, nil, http.MethodGet, "/api/platforms/kick/callback?state="+url.QueryEscape(manipulado)+"&code=ok", "")
	if cb.Code != http.StatusBadRequest {
		t.Fatalf("state manipulado = %d, quería 400", cb.Code)
	}
	if ct := cb.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(cb.Body.String(), "solicitud inválida") || strings.Contains(cb.Body.String(), "firma") {
		t.Errorf("el error cuenta demasiado: %s", cb.Body)
	}
	rec = do(t, srv, ck, http.MethodGet, "/api/platforms/kick/auth/"+inicio.State, "")
	var estado authStatusDTO
	json.Unmarshal(rec.Body.Bytes(), &estado)
	if estado.Status != "pending" {
		t.Errorf("tras el state manipulado el flujo está %q, quería pending", estado.Status)
	}

	// Caducado: se firma con el reloj once minutos atrás y se verifica con el de verdad.
	srv.now = func() time.Time { return time.Now().Add(-11 * time.Minute) }
	caducado := srv.firmarState("da39a3ee5e6b4b0d")
	srv.now = time.Now
	cb = do(t, srv, nil, http.MethodGet, "/api/platforms/kick/callback?state="+url.QueryEscape(caducado)+"&code=ok", "")
	if cb.Code != http.StatusBadRequest {
		t.Errorf("state caducado = %d, quería 400: %s", cb.Code, cb.Body)
	}
	// Y una cookie de sesión no vale como state ni al revés: llevan prefijos distintos.
	cb = do(t, srv, nil, http.MethodGet, "/api/platforms/kick/callback?state="+url.QueryEscape(srv.signer.issue(time.Now()))+"&code=ok", "")
	if cb.Code != http.StatusBadRequest {
		t.Errorf("cookie usada como state = %d, quería 400", cb.Code)
	}
	if err := srv.signer.verify(p.estadoFirmado(), time.Now()); err == nil {
		t.Error("un state vale como cookie de sesión")
	}
}

// Si la plataforma dice que no, el flujo queda en error con lo que ella cuente —saneado— y
// la página lo enseña sin ejecutar nada.
func TestCallbackReportsThePlatformError(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.Kick, redirect: true}
	srv, _, ck := servidorPlataformas(t, p)

	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/kick/auth", `{"origin":"http://localhost:5173"}`)
	var inicio authStartDTO
	json.Unmarshal(rec.Body.Bytes(), &inicio)

	cb := do(t, srv, nil, http.MethodGet, "/api/platforms/kick/callback?state="+url.QueryEscape(p.estadoFirmado())+
		"&error=access_denied&error_description="+url.QueryEscape("<script>malo()</script>"), "")
	if cb.Code != http.StatusOK {
		t.Fatalf("callback con error = %d %s", cb.Code, cb.Body)
	}
	if strings.Contains(cb.Body.String(), "<script>") {
		t.Errorf("la descripción se reflejó sin escapar: %s", cb.Body)
	}
	if !strings.Contains(cb.Body.String(), "No se pudo conectar") {
		t.Errorf("página = %s", cb.Body)
	}
	rec = do(t, srv, ck, http.MethodGet, "/api/platforms/kick/auth/"+inicio.State, "")
	var estado authStatusDTO
	json.Unmarshal(rec.Body.Bytes(), &estado)
	if estado.Status != "error" || estado.Message == "" {
		t.Errorf("estado = %+v", estado)
	}
}

// Con TLS integrado y URL pública, conectar la cuenta suscribe el chat a nuestra URL de
// webhook; sin ella no hay a dónde suscribirse y no se intenta.
func TestCallbackSubscribesTheChatOnlyWithAPublicURL(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.Kick, redirect: true, webhook: true, requiresOwnApp: true}
	srv, _, ck := servidorPlataformas(t, p, func(c *Config) {
		c.TLS, c.PublicURL = true, "https://relay.ejemplo.com"
	})

	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/kick/auth",
		`{"client_id":"ci","client_secret":"ksec","origin":"https://relay.ejemplo.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("inicio = %d %s", rec.Code, rec.Body)
	}
	cb := do(t, srv, nil, http.MethodGet, "/api/platforms/kick/callback?state="+url.QueryEscape(p.estadoFirmado())+"&code=ok", "")
	if cb.Code != http.StatusOK {
		t.Fatalf("callback = %d %s", cb.Code, cb.Body)
	}
	p.mu.Lock()
	suscripciones, webhookURL := p.suscripciones, p.webhookURL
	p.mu.Unlock()
	if suscripciones != 1 || webhookURL != "https://relay.ejemplo.com/api/platforms/kick/webhook" {
		t.Errorf("suscripciones = %d, url = %q", suscripciones, webhookURL)
	}
	// Y la plataforma anuncia que aquí sí se puede usar su chat.
	rec = do(t, srv, ck, http.MethodGet, "/api/platforms", "")
	var lista []platformDTO
	json.Unmarshal(rec.Body.Bytes(), &lista)
	if len(lista) != 1 || !lista[0].Capabilities.RequiresPublicURL || !lista[0].PublicURLOK {
		t.Errorf("plataformas = %+v", lista)
	}
}

// Con TLS integrado el servidor sabe por qué nombre lo alcanzan, así que un origen ajeno no
// puede colarse como redirect_uri.
func TestAuthStartRejectsAnOriginOutsideThePublicURL(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.Kick, redirect: true}
	srv, _, ck := servidorPlataformas(t, p, func(c *Config) {
		c.TLS, c.PublicURL = true, "https://relay.ejemplo.com"
	})

	for _, origen := range []string{`"https://malo.ejemplo.com"`, `"http://relay.ejemplo.com"`, `""`, `"no-es-una-url"`} {
		rec := do(t, srv, ck, http.MethodPost, "/api/platforms/kick/auth", `{"origin":`+origen+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("origen %s = %d, quería 400: %s", origen, rec.Code, rec.Body)
		}
	}
	// La propia URL pública y el equipo local sí valen.
	for _, origen := range []string{"https://relay.ejemplo.com", "http://localhost:5173", "http://127.0.0.1:8080"} {
		rec := do(t, srv, ck, http.MethodPost, "/api/platforms/kick/auth", `{"origin":"`+origen+`"}`)
		if rec.Code != http.StatusOK {
			t.Errorf("origen %q = %d, quería 200: %s", origen, rec.Code, rec.Body)
		}
	}
}

// Desconectar una cuenta cancela la suscripción al chat: dejarla viva haría que la
// plataforma siguiera llamando a nuestro webhook para siempre.
func TestDeleteAccountUnsubscribesTheChat(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.Kick, webhook: true}
	srv, db, ck := servidorPlataformas(t, p, func(c *Config) {
		c.TLS, c.PublicURL = true, "https://relay.ejemplo.com"
	})
	a := cuentaDePlataforma(t, srv, db, store.PlatformKick, "123", "kickdev")

	if rec := do(t, srv, ck, http.MethodDelete, "/api/accounts/"+itoa(a.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", rec.Code)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.desuscripciones != 1 {
		t.Errorf("desuscripciones = %d, quería 1", p.desuscripciones)
	}
}

// La cuota es de YouTube y solo aparece si el servidor arrancó con contador: en el resto de
// las cuentas el campo es null, que el panel distingue de un cero.
func TestAccountsCarryOwnAppAndQuota(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true, requiresOwnApp: true}
	srv, db, ck := servidorPlataformas(t, p, func(c *Config) { c.Quota = cuotaFalsa{n: 4321} })
	cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")

	rec := do(t, srv, ck, http.MethodGet, "/api/accounts", "")
	var cuentas []accountDTO
	json.Unmarshal(rec.Body.Bytes(), &cuentas)
	if len(cuentas) != 1 || cuentas[0].QuotaUsedToday == nil || *cuentas[0].QuotaUsedToday != 4321 {
		t.Fatalf("cuentas = %+v", cuentas)
	}
	if !cuentas[0].OwnApp {
		t.Errorf("own_app = false en una cuenta con app propia")
	}

	rec = do(t, srv, ck, http.MethodGet, "/metrics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `splitstream_youtube_quota_units{account="canal"} 4321`) {
		t.Errorf("métricas sin la cuota: %s", rec.Body)
	}
}

// provEmisionSinCanal es un proveedor que SOLO sabe poner el título de una emisión, sin
// forma de tocar el del canal (que es como se comporta YouTube). Hace falta un tipo aparte
// porque fakeProvider siempre trae SetTitle y en Go una capacidad no se puede quitar.
type provEmisionSinCanal struct{}

func (provEmisionSinCanal) ID() platforms.ID { return platforms.YouTube }
func (provEmisionSinCanal) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{Title: true, Schedule: true, RequiresOwnApp: true}
}
func (provEmisionSinCanal) Configured() bool { return true }
func (provEmisionSinCanal) BeginAuth(context.Context, platforms.Credentials) (platforms.AuthPrompt, error) {
	return platforms.AuthPrompt{}, platforms.ErrNoClientID
}
func (provEmisionSinCanal) PollAuth(context.Context, platforms.Credentials, platforms.AuthPrompt) (store.NewAccount, error) {
	return store.NewAccount{}, platforms.ErrAuthExpired
}
func (provEmisionSinCanal) Refresh(context.Context, store.Account, platforms.Credentials, crypto.Secret) (store.Tokens, error) {
	return store.Tokens{}, nil
}
func (provEmisionSinCanal) Validate(context.Context, crypto.Secret) (platforms.Identity, error) {
	return platforms.Identity{}, nil
}
func (provEmisionSinCanal) SetBroadcastTitle(context.Context, store.Account, crypto.Secret, string, string) error {
	return nil
}

// Dos llegadas del mismo callback —un doble clic, un reintento del navegador— no pueden
// canjear el código dos veces: la segunda encuentra el flujo ya reclamado.
func TestCallbackExchangesTheCodeOnlyOnce(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.Kick, redirect: true,
		entroCanje: make(chan struct{}), liberaCanje: make(chan struct{})}
	srv, db, ck := servidorPlataformas(t, p)

	rec := do(t, srv, ck, http.MethodPost, "/api/platforms/kick/auth", `{"origin":"http://localhost:5173"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("inicio = %d %s", rec.Code, rec.Body)
	}
	var inicio authStartDTO
	json.Unmarshal(rec.Body.Bytes(), &inicio)
	ruta := "/api/platforms/kick/callback?state=" + url.QueryEscape(p.estadoFirmado()) + "&code=ok"

	primera := make(chan int, 1)
	go func() { primera <- do(t, srv, nil, http.MethodGet, ruta, "").Code }()

	// Se espera a que el canje haya empezado de verdad: la segunda llegada tiene que caer
	// justo dentro de esa ventana, que es donde estaba el problema.
	select {
	case <-p.entroCanje:
	case <-time.After(3 * time.Second):
		t.Fatal("CompleteRedirect no llegó a empezar")
	}
	// Mientras se canjea, el panel ve el flujo como pendiente y no como un estado raro.
	rec = do(t, srv, ck, http.MethodGet, "/api/platforms/kick/auth/"+inicio.State, "")
	var enCurso authStatusDTO
	json.Unmarshal(rec.Body.Bytes(), &enCurso)
	if enCurso.Status != "pending" {
		t.Errorf("durante el canje el estado es %q, quería pending", enCurso.Status)
	}

	segunda := do(t, srv, nil, http.MethodGet, ruta, "").Code
	close(p.liberaCanje)

	var codigoPrimera int
	select {
	case codigoPrimera = <-primera:
	case <-time.After(3 * time.Second):
		t.Fatal("la primera llegada no terminó")
	}
	if codigoPrimera != http.StatusOK || segunda != http.StatusBadRequest {
		t.Errorf("códigos = %d y %d, quería 200 y 400", codigoPrimera, segunda)
	}
	p.mu.Lock()
	completados := p.completados
	p.mu.Unlock()
	if completados != 1 {
		t.Errorf("CompleteRedirect se llamó %d veces, quería 1", completados)
	}
	if n := eventosPorTipo(t, db)["account_connected"]; n != 1 {
		t.Errorf("eventos account_connected = %d, quería 1", n)
	}
	cuentas, _ := db.Accounts(context.Background())
	if len(cuentas) != 1 {
		t.Errorf("cuentas = %d, quería 1", len(cuentas))
	}
	// Y el flujo terminó en done pese al ir y venir.
	rec = do(t, srv, ck, http.MethodGet, "/api/platforms/kick/auth/"+inicio.State, "")
	var estado authStatusDTO
	json.Unmarshal(rec.Body.Bytes(), &estado)
	if estado.Status != "done" || estado.Account == nil {
		t.Errorf("estado final = %+v", estado)
	}
}

// El presupuesto de cuota del chat de YouTube viaja en el estado del panel: sin él, las
// unidades gastadas de cada cuenta no dicen si queda mucho o poco.
func TestStatusCarriesTheYouTubeChatBudget(t *testing.T) {
	srv, _, ck := servidorPlataformas(t, &fakeProvider{configured: true}, func(c *Config) {
		c.ChatBudget, c.YouTubeQuota = 5000, 10000
	})
	rec := do(t, srv, ck, http.MethodGet, "/api/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var out statusDTO
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Panel.YouTubeChatBudget != 5000 {
		t.Errorf("youtube_chat_budget = %d, quería 5000", out.Panel.YouTubeChatBudget)
	}
	// La cuota diaria del proyecto viaja al lado: el presupuesto del chat solo se entiende
	// como una parte de ella.
	if out.Panel.YouTubeQuota != 10000 {
		t.Errorf("youtube_quota = %d, quería 10000", out.Panel.YouTubeQuota)

	}
}

// Sin emisión creada, el título de YouTube no tiene dónde ponerse: el mensaje dice qué
// hacer en vez de hablar de una capacidad que la plataforma sí tiene.
func TestLiveTitleAsksForTheBroadcastFirst(t *testing.T) {
	srv, db := newTestServer(t, func(c *Config) {
		c.Platforms = platforms.NewRegistry(provEmisionSinCanal{})
		c.Tokens = tokensFalsos{db: c.DB, c: c.Cipher}
	})
	ck := login(t, srv)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")
	d, err := db.CreateDestination(context.Background(), srv.cipher, store.NewDestination{
		Name: "YouTube", Platform: store.PlatformYouTube, RTMPURL: "rtmp://a.rtmp.youtube.com/live2",
		Key: "clave", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.LinkDestination(context.Background(), d.ID, a.ID); err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, ck, http.MethodPost, "/api/live/title", `{"title":"Hola","destinations":[`+itoa(d.ID)+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("title = %d %s", rec.Code, rec.Body)
	}
	var res []liveResultDTO
	json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res) != 1 || res[0].OK {
		t.Fatalf("resultado = %+v", res)
	}
	if !strings.Contains(res[0].Message, "crea la emisión primero") {
		t.Errorf("mensaje = %q", res[0].Message)
	}
}
