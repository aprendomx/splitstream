package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// --- dobles ---

// fakeEngine simula el motor. sessionID a 0 significa que no hay nadie transmitiendo.
type fakeEngine struct {
	mu      sync.Mutex
	sesion  relay.LiveSession
	metrics map[int64]relay.Metrics
	added   []int64
	removed []int64
}

func (f *fakeEngine) Session() relay.LiveSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sesion
}

func (f *fakeEngine) Snapshot() map[int64]relay.Metrics {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[int64]relay.Metrics, len(f.metrics))
	for k, v := range f.metrics {
		out[k] = v
	}
	return out
}

func (f *fakeEngine) setLive(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sesion = relay.LiveSession{ID: id, StartedAt: time.Now()}
}

// setSesion fija la sesión entera, para los tests que miran resolución o bitrate.
func (f *fakeEngine) setSesion(s relay.LiveSession) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sesion = s
}

// AddSink y RemoveSink registran lo que la API pidió. Se apuntan sobre el fake del MOTOR y
// no sobre uno del hub porque arrancar el sink es responsabilidad del motor: cuando la API
// hablaba directamente con el hub, el destino se añadía sin arrancar y se quedaba en idle
// descartando mensajes. El fake de entonces solo miraba el id, así que no lo vio.
func (f *fakeEngine) AddSink(s *relay.Sink) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, s.ID())
}

func (f *fakeEngine) RemoveSink(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
}

func (f *fakeEngine) snapshotSinks() (added, removed []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.added...), append([]int64(nil), f.removed...)
}

func (f *fakeEngine) setMetrics(m map[int64]relay.Metrics) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metrics = m
}

// fakeSinks construye sinks que no conectan a ninguna parte: basta con que tengan el id
// correcto, que es lo único que el hub mira.
type fakeSinks struct{ err error }

func (f *fakeSinks) Build(ctx context.Context, d store.Destination) (*relay.Sink, error) {
	if f.err != nil {
		return nil, f.err
	}
	return relay.NewSink(relay.SinkConfig{ID: d.ID, Name: d.Name}), nil
}

// --- andamiaje ---

// newDestServer levanta un servidor con los dobles puestos y una sesión ya autenticada.
func newDestServer(t *testing.T) (*Server, *store.DB, *fakeEngine, *fakeEngine, []*http.Cookie) {
	t.Helper()
	srv, db := newTestServer(t)

	eng := &fakeEngine{}
	srv.engine, srv.sinks = eng, &fakeSinks{}

	// Se devuelve dos veces para no reescribir las llamadas de los tests: el motor hace
	// ahora también lo que antes se le pedía al hub.
	return srv, db, eng, eng, login(t, srv)
}

// do lanza una petición autenticada.
func do(t *testing.T, srv *Server, cookies []*http.Cookie, metodo, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(metodo, path, nil)
	} else {
		r = httptest.NewRequest(metodo, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

// crearDest mete un destino directamente por el store, para preparar el estado.
func crearDest(t *testing.T, db *store.DB, srv *Server, nombre, clave string, enabled bool) *store.Destination {
	t.Helper()
	d, err := db.CreateDestination(context.Background(), srv.cipher, store.NewDestination{
		Name: nombre, Platform: store.PlatformCustom,
		RTMPURL: "rtmp://127.0.0.1:1935/live", Key: crypto.Secret(clave), Enabled: enabled,
	})
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	return d
}

func decodeDest(t *testing.T, rec *httptest.ResponseRecorder) destinationDTO {
	t.Helper()
	var d destinationDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decodificar destino: %v — %s", err, rec.Body.String())
	}
	return d
}

func destPath(id int64) string { return "/api/destinations/" + strconv.FormatInt(id, 10) }

// --- listado ---

func TestListDestinationsReturnsThemInSortOrder(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "primero", "k1", true)
	crearDest(t, db, srv, "segundo", "k2", true)
	crearDest(t, db, srv, "tercero", "k3", true)

	rec := do(t, srv, cookies, http.MethodGet, "/api/destinations", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	var got []destinationDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	quiero := []string{"primero", "segundo", "tercero"}
	if len(got) != len(quiero) {
		t.Fatalf("destinos = %d, quería %d", len(got), len(quiero))
	}
	for i := range quiero {
		if got[i].Name != quiero[i] {
			t.Errorf("posición %d = %q, quería %q", i, got[i].Name, quiero[i])
		}
	}
}

// TestListDestinationsIsAnArrayEvenWhenEmpty: un null en vez de [] obliga al frontend a
// comprobarlo antes de iterar, y es el tipo de detalle que se descubre en la fase 5.
func TestListDestinationsIsAnArrayEvenWhenEmpty(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodGet, "/api/destinations", "")
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("cuerpo = %q, quería []", got)
	}
}

// TestListDestinationsNeverIncludesAKey recorre el cuerpo crudo: no basta con mirar los
// campos del DTO, porque lo que hay que impedir es que la clave salga por CUALQUIER vía
// (spec §8).
func TestListDestinationsNeverIncludesAKey(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	const clave = "una-clave-inconfundible-12345"
	crearDest(t, db, srv, "yt", clave, true)

	rec := do(t, srv, cookies, http.MethodGet, "/api/destinations", "")
	if strings.Contains(rec.Body.String(), clave) {
		t.Errorf("el listado lleva la clave: %s", rec.Body.String())
	}
}

// --- alta ---

func TestCreateDestinationPersistsAndReturns201(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations",
		`{"name":"twitch","platform":"twitch","rtmp_url":"rtmp://live.twitch.tv/app","key":"live_123","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("código = %d, quería 201: %s", rec.Code, rec.Body.String())
	}

	got := decodeDest(t, rec)
	if got.ID == 0 {
		t.Error("el destino creado no trae id")
	}
	if got.Name != "twitch" || got.Platform != "twitch" {
		t.Errorf("cuerpo inesperado: %+v", got)
	}
	if loc := rec.Header().Get("Location"); loc != destPath(got.ID) {
		t.Errorf("Location = %q, quería %q", loc, destPath(got.ID))
	}
	if strings.Contains(rec.Body.String(), "live_123") {
		t.Error("la respuesta del alta devuelve la clave")
	}

	dests, err := db.ListDestinations(context.Background())
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(dests) != 1 {
		t.Fatalf("persistidos = %d, quería 1", len(dests))
	}
}

func TestCreateDestinationRejectsBadInput(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	casos := []struct{ nombre, body string }{
		{"URL http", `{"name":"n","platform":"custom","rtmp_url":"http://x/live","key":"k"}`},
		{"URL sin app", `{"name":"n","platform":"custom","rtmp_url":"rtmp://x","key":"k"}`},
		{"sin nombre", `{"name":"","platform":"custom","rtmp_url":"rtmp://x/live","key":"k"}`},
		{"plataforma inventada", `{"name":"n","platform":"myspace","rtmp_url":"rtmp://x/live","key":"k"}`},
		{"clave vacía", `{"name":"n","platform":"custom","rtmp_url":"rtmp://x/live","key":""}`},
		{"JSON malformado", `{"name":`},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			rec := do(t, srv, cookies, http.MethodPost, "/api/destinations", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("código = %d, quería 400: %s", rec.Code, rec.Body.String())
			}
			if got := errorCodeDe(t, rec); got != codeInvalidInput {
				t.Errorf("code = %q, quería %q", got, codeInvalidInput)
			}
		})
	}
}

// --- edición ---

// TestPatchDestinationLeavesUnsetFieldsAlone: en un PATCH, un campo ausente y un campo
// vacío no son lo mismo. Sin esa distinción, editar el nombre borraría la clave.
func TestPatchDestinationLeavesUnsetFieldsAlone(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "viejo", "clave-original", true)
	antes := *d

	rec := do(t, srv, cookies, http.MethodPatch, destPath(d.ID), `{"name":"nuevo"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	got := decodeDest(t, rec)
	if got.Name != "nuevo" {
		t.Errorf("Name = %q, quería nuevo", got.Name)
	}
	if got.RTMPURL != antes.RTMPURL {
		t.Errorf("la URL cambió sin que se pidiera: %q", got.RTMPURL)
	}
	if got.Platform != string(antes.Platform) {
		t.Errorf("la plataforma cambió sin que se pidiera: %q", got.Platform)
	}
	if got.KeyMask != antes.KeyMask {
		t.Errorf("la clave cambió sin que se pidiera: %q", got.KeyMask)
	}
	if got.Enabled != antes.Enabled {
		t.Error("enabled cambió sin que se pidiera")
	}

	k, err := db.RevealDestinationKey(context.Background(), srv.cipher, d.ID)
	if err != nil {
		t.Fatalf("RevealDestinationKey: %v", err)
	}
	if k.Reveal() != "clave-original" {
		t.Errorf("la clave guardada es %q", k.Reveal())
	}
}

func TestPatchDestinationCanReplaceTheKey(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "clave-vieja-aaaa", true)
	maskAntes := d.KeyMask

	rec := do(t, srv, cookies, http.MethodPatch, destPath(d.ID), `{"key":"clave-nueva-bbbb"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	got := decodeDest(t, rec)
	if got.KeyMask == maskAntes {
		t.Error("la máscara no cambió al reemplazar la clave")
	}
	if strings.Contains(rec.Body.String(), "clave-nueva-bbbb") {
		t.Error("la respuesta devuelve la clave nueva en claro")
	}

	k, err := db.RevealDestinationKey(context.Background(), srv.cipher, d.ID)
	if err != nil {
		t.Fatalf("RevealDestinationKey: %v", err)
	}
	if k.Reveal() != "clave-nueva-bbbb" {
		t.Errorf("la clave guardada es %q", k.Reveal())
	}
}

func TestPatchDestinationRejectsBadInput(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)

	casos := []struct{ nombre, body string }{
		{"URL http", `{"rtmp_url":"http://x/live"}`},
		{"nombre vacío", `{"name":"  "}`},
		{"plataforma inventada", `{"platform":"myspace"}`},
		{"clave vacía", `{"key":""}`},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			rec := do(t, srv, cookies, http.MethodPatch, destPath(d.ID), c.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("código = %d, quería 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// --- borrado y toggle ---

func TestDeleteDestinationReturns204AndIsGone(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)

	rec := do(t, srv, cookies, http.MethodDelete, destPath(d.ID), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("código = %d, quería 204: %s", rec.Code, rec.Body.String())
	}

	dests, err := db.ListDestinations(context.Background())
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(dests) != 0 {
		t.Errorf("quedan %d destinos tras borrar", len(dests))
	}
}

func TestToggleFlipsEnabled(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/toggle", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	if decodeDest(t, rec).Enabled {
		t.Error("tras el primer toggle sigue encendido")
	}

	rec = do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/toggle", "")
	if !decodeDest(t, rec).Enabled {
		t.Error("tras el segundo toggle no volvió a encenderse")
	}
}

// --- reordenar ---

func TestReorderPersistsTheWholeOrder(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	a := crearDest(t, db, srv, "a", "k1", true)
	b := crearDest(t, db, srv, "b", "k2", true)
	c := crearDest(t, db, srv, "c", "k3", true)

	body := `{"ids":[` + strconv.FormatInt(c.ID, 10) + `,` +
		strconv.FormatInt(b.ID, 10) + `,` + strconv.FormatInt(a.ID, 10) + `]}`
	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations/reorder", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	dests, err := db.ListDestinations(context.Background())
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	quiero := []string{"c", "b", "a"}
	for i := range quiero {
		if dests[i].Name != quiero[i] {
			t.Errorf("posición %d = %q, quería %q", i, dests[i].Name, quiero[i])
		}
	}
}

func TestReorderRejectsUnknownIDs(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	a := crearDest(t, db, srv, "a", "k1", true)
	crearDest(t, db, srv, "b", "k2", true)

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations/reorder",
		`{"ids":[`+strconv.FormatInt(a.ID, 10)+`,9999]}`)
	if rec.Code == http.StatusOK {
		t.Fatal("se aceptó un reorden con un id inexistente")
	}

	dests, err := db.ListDestinations(context.Background())
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if dests[0].Name != "a" {
		t.Errorf("el orden anterior no se conservó tras el fallo: %q primero", dests[0].Name)
	}
}

// TestReorderRouteIsNotSwallowedByTheIDWildcard existe porque es el fallo de enrutado más
// probable de todo el mux: si "reorder" entrara por el handler de {id}, el usuario vería
// un 400 de "id inválido" sin ninguna pista de qué pasó.
func TestReorderRouteIsNotSwallowedByTheIDWildcard(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	a := crearDest(t, db, srv, "a", "k1", true)

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations/reorder",
		`{"ids":[`+strconv.FormatInt(a.ID, 10)+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s — ¿cayó en el handler de {id}?", rec.Code, rec.Body.String())
	}
}

// --- revelado ---

// TestRevealKeyReturnsTheKeyAndAudits: es el ÚNICO endpoint del listado que devuelve una
// clave en claro, y por eso es el único que tiene que dejar rastro (spec §15.5).
func TestRevealKeyReturnsTheKeyAndAudits(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	const clave = "la-clave-de-verdad"
	d := crearDest(t, db, srv, "yt", clave, true)

	rec := do(t, srv, cookies, http.MethodGet, destPath(d.ID)+"/key", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if body.Key != clave {
		t.Errorf("key = %q, quería %q", body.Key, clave)
	}

	eventos, err := db.RecentEvents(context.Background(), 50)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	var visto bool
	for _, e := range eventos {
		if e.Kind == "key_revealed" {
			visto = true
			if strings.Contains(e.Message, clave) {
				t.Error("el evento de auditoría lleva la clave dentro")
			}
		}
	}
	if !visto {
		t.Error("revelar la clave no dejó evento de auditoría")
	}
}

// --- no encontrado y mal formado ---

func TestNotFoundForUnknownID(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	casos := []struct{ metodo, path, body string }{
		{http.MethodPatch, "/api/destinations/9999", `{"name":"x"}`},
		{http.MethodDelete, "/api/destinations/9999", ""},
		{http.MethodPost, "/api/destinations/9999/toggle", ""},
		{http.MethodGet, "/api/destinations/9999/key", ""},
	}

	for _, c := range casos {
		t.Run(c.metodo+" "+c.path, func(t *testing.T) {
			rec := do(t, srv, cookies, c.metodo, c.path, c.body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("código = %d, quería 404: %s", rec.Code, rec.Body.String())
			}
			if got := errorCodeDe(t, rec); got != codeNotFound {
				t.Errorf("code = %q, quería %q", got, codeNotFound)
			}
		})
	}
}

// TestNonNumericIDIsBadRequestNotNotFound: la ruta existe; lo que no vale es lo que
// mandaron. Un 404 aquí haría pensar que el destino se borró.
func TestNonNumericIDIsBadRequestNotNotFound(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	// Cada ruta con su método real: el spec §9 no define un GET de un destino individual,
	// así que un GET a /api/destinations/{id} da 405, y eso es correcto.
	casos := []struct{ metodo, path, body string }{
		{http.MethodPatch, "/api/destinations/abc", `{"name":"x"}`},
		{http.MethodDelete, "/api/destinations/abc", ""},
		{http.MethodPost, "/api/destinations/abc/toggle", ""},
		{http.MethodGet, "/api/destinations/abc/key", ""},
	}

	for _, c := range casos {
		rec := do(t, srv, cookies, c.metodo, c.path, c.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: código = %d, quería 400: %s", c.metodo, c.path, rec.Code, rec.Body.String())
		}
	}
}

// --- efecto en caliente ---

// TestCreateDestinationAppliesToTheLiveSessionImmediately: si el usuario añade un destino
// mientras transmite, tiene que empezar a salir por ahí sin cortar la transmisión. Si no,
// el alta y el toggle de la interfaz mentirían durante todo el directo.
func TestCreateDestinationAppliesToTheLiveSessionImmediately(t *testing.T) {
	srv, _, eng, hub, cookies := newDestServer(t)
	eng.setLive(42)

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations",
		`{"name":"nuevo","platform":"custom","rtmp_url":"rtmp://x/live","key":"k","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	added, _ := hub.snapshotSinks()
	if len(added) != 1 {
		t.Fatalf("sinks añadidos al hub = %d, quería 1", len(added))
	}
	if added[0] != decodeDest(t, rec).ID {
		t.Errorf("se añadió el sink %d, quería el del destino creado", added[0])
	}
}

// TestCreateDestinationDoesNothingHotWhenThereIsNoSession: sin sesión no hay a qué
// añadirlo; el destino se persiste y entra en la próxima transmisión (spec §6.5).
func TestCreateDestinationDoesNothingHotWhenThereIsNoSession(t *testing.T) {
	srv, db, _, hub, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations",
		`{"name":"nuevo","platform":"custom","rtmp_url":"rtmp://x/live","key":"k","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	added, removed := hub.snapshotSinks()
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("se tocó el hub sin sesión viva: added=%v removed=%v", added, removed)
	}

	dests, err := db.ListDestinations(context.Background())
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(dests) != 1 {
		t.Error("el destino no se persistió")
	}
}

// TestCreateDestinationDisabledDoesNotGoLive: dar de alta un destino apagado no debe
// conectarlo, ni siquiera con una sesión en curso.
func TestCreateDestinationDisabledDoesNotGoLive(t *testing.T) {
	srv, _, eng, hub, cookies := newDestServer(t)
	eng.setLive(42)

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations",
		`{"name":"apagado","platform":"custom","rtmp_url":"rtmp://x/live","key":"k","enabled":false}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	if added, _ := hub.snapshotSinks(); len(added) != 0 {
		t.Errorf("se conectó un destino que se creó apagado: %v", added)
	}
}

func TestDeleteDestinationStopsItsSinkWhenLive(t *testing.T) {
	srv, db, eng, hub, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)
	eng.setLive(42)

	rec := do(t, srv, cookies, http.MethodDelete, destPath(d.ID), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	_, removed := hub.snapshotSinks()
	if len(removed) != 1 || removed[0] != d.ID {
		t.Errorf("removed = %v, quería [%d]", removed, d.ID)
	}
}

func TestToggleOffStopsTheSinkAndToggleOnStartsIt(t *testing.T) {
	srv, db, eng, hub, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)
	eng.setLive(42)

	if rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/toggle", ""); rec.Code != http.StatusOK {
		t.Fatalf("apagar: %d — %s", rec.Code, rec.Body.String())
	}
	added, removed := hub.snapshotSinks()
	if len(removed) != 1 || removed[0] != d.ID {
		t.Fatalf("al apagar, removed = %v", removed)
	}
	if len(added) != 0 {
		t.Fatalf("al apagar, added = %v", added)
	}

	if rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/toggle", ""); rec.Code != http.StatusOK {
		t.Fatalf("encender: %d — %s", rec.Code, rec.Body.String())
	}
	added, _ = hub.snapshotSinks()
	if len(added) != 1 || added[0] != d.ID {
		t.Errorf("al encender, added = %v, quería [%d]", added, d.ID)
	}
}

// TestPatchAppliesToTheLiveSessionByReplacingTheSink: cambiar la clave de un destino a
// mitad de transmisión tiene que reconectarlo con la nueva. Hub.Add reemplaza sin ventana
// de escritura doble (fase 2), así que basta con volver a añadirlo.
func TestPatchAppliesToTheLiveSessionByReplacingTheSink(t *testing.T) {
	srv, db, eng, hub, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "clave-vieja-aaaa", true)
	eng.setLive(42)

	if rec := do(t, srv, cookies, http.MethodPatch, destPath(d.ID), `{"key":"clave-nueva-bbbb"}`); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}

	added, _ := hub.snapshotSinks()
	if len(added) != 1 || added[0] != d.ID {
		t.Errorf("added = %v, quería [%d]: el destino editado debe reemplazarse en el hub", added, d.ID)
	}
}

// TestPatchDisablingRemovesTheSink: apagar por PATCH es lo mismo que apagar por toggle.
func TestPatchDisablingRemovesTheSink(t *testing.T) {
	srv, db, eng, hub, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)
	eng.setLive(42)

	if rec := do(t, srv, cookies, http.MethodPatch, destPath(d.ID), `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}

	added, removed := hub.snapshotSinks()
	if len(removed) != 1 || removed[0] != d.ID {
		t.Errorf("removed = %v, quería [%d]", removed, d.ID)
	}
	if len(added) != 0 {
		t.Errorf("added = %v, quería vacío", added)
	}
}

// TestHotApplyFailureDoesNotFailTheRequest: la petición hizo lo que pedía —persistir el
// cambio—. Devolver 500 haría que el usuario lo repitiera y creara un destino duplicado.
func TestHotApplyFailureDoesNotFailTheRequest(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	eng.setLive(42)
	srv.sinks = &fakeSinks{err: errors.New("no se pudo construir el sink")}

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations",
		`{"name":"nuevo","platform":"custom","rtmp_url":"rtmp://x/live","key":"k","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("código = %d, quería 201: un fallo del hub no debe tumbar la petición — %s",
			rec.Code, rec.Body.String())
	}
}

// TestHotApplyIsSkippedWithoutAHub: en un arranque parcial o en un test, hub y sinks
// pueden ser nil. No debe entrar en pánico.
func TestHotApplyIsSkippedWithoutAHub(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	eng.setLive(42)
	srv.sinks = nil

	rec := do(t, srv, cookies, http.MethodPost, "/api/destinations",
		`{"name":"nuevo","platform":"custom","rtmp_url":"rtmp://x/live","key":"k","enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("código = %d, quería 201: %s", rec.Code, rec.Body.String())
	}
}

// TestListIncludesMetricsOfTheLiveSession: el listado también cruza con el motor, no solo
// GET /api/status: la lista de destinos de la interfaz enseña el bitrate de cada uno.
func TestListIncludesMetricsOfTheLiveSession(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	vivo := crearDest(t, db, srv, "vivo", "k1", true)
	quieto := crearDest(t, db, srv, "quieto", "k2", true)

	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{
		vivo.ID: {State: "live", BytesSent: 1234, BitrateBPS: 4000},
	})

	rec := do(t, srv, cookies, http.MethodGet, "/api/destinations", "")
	var got []destinationDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decodificar: %v", err)
	}

	for _, d := range got {
		switch d.ID {
		case vivo.ID:
			if d.Metrics == nil {
				t.Fatal("el destino en vivo no trae métricas")
			}
			if d.Metrics.BytesSent != 1234 || d.Metrics.BitrateBPS != 4000 {
				t.Errorf("métricas mal compuestas: %+v", d.Metrics)
			}
		case quieto.ID:
			if d.Metrics != nil {
				t.Errorf("el destino sin métricas trae %+v en vez de null", d.Metrics)
			}
		}
	}
}
