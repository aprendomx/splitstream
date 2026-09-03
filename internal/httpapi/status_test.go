package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// fakeDisconnecter cuenta las veces que la API pidió cortar la publicación.
type fakeDisconnecter struct {
	mu      sync.Mutex
	llamado int
	corta   int
}

func (f *fakeDisconnecter) DisconnectPublisher() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.llamado++
	return f.corta
}

func (f *fakeDisconnecter) veces() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.llamado
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) statusDTO {
	t.Helper()
	var st statusDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decodificar status: %v — %s", err, rec.Body.String())
	}
	return st
}

// --- ingesta ---

// TestGetIngestNeverReturnsThePlainKey: la tarjeta de ingesta del panel enseña la máscara;
// la clave solo se ve al rotarla (spec §8).
func TestGetIngestNeverReturnsThePlainKey(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	real, err := srv.db.RevealIngestKey(context.Background(), srv.cipher)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}

	rec := do(t, srv, cookies, http.MethodGet, "/api/ingest", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), real.Reveal()) {
		t.Errorf("GET /api/ingest devuelve la clave en claro: %s", rec.Body.String())
	}

	var got ingestDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if got.KeyMask != real.Mask() {
		t.Errorf("key_mask = %q, quería %q", got.KeyMask, real.Mask())
	}
	if got.App == "" || got.URL == "" {
		t.Errorf("faltan app o url: %+v", got)
	}
	// La clave no puede ir incrustada en la URL: es lo que hace que la gente acabe
	// pegándola en sitios donde no debería estar.
	if strings.Contains(got.URL, real.Reveal()) {
		t.Error("la URL de ingesta lleva la clave dentro")
	}
	if !strings.HasPrefix(got.URL, "rtmp://") {
		t.Errorf("url = %q, quería que empezara por rtmp://", got.URL)
	}
	if !strings.HasSuffix(got.URL, "/"+got.App) {
		t.Errorf("url = %q, quería que acabara en /%s", got.URL, got.App)
	}
}

// TestIngestURLUsesTheRequestHostAndTheConfiguredPort: el panel se alcanza por algún
// nombre concreto —una IP, un dominio, tailscale— y la ingesta está en esa misma máquina,
// así que lo que el usuario tiene delante es lo que le va a funcionar en OBS.
func TestIngestURLUsesTheRequestHostAndTheConfiguredPort(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.rtmpPort = "1935"

	req := httptest.NewRequest(http.MethodGet, "/api/ingest", nil)
	req.Host = "mi-vps.example.com:8080"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var got ingestDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if !strings.HasPrefix(got.URL, "rtmp://mi-vps.example.com:1935/") {
		t.Errorf("url = %q, quería el host de la petición y el puerto RTMP", got.URL)
	}
}

func TestRotateKeyChangesTheKeyAndReturnsItOnce(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	ctx := context.Background()

	antes, err := srv.db.RevealIngestKey(ctx, srv.cipher)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}

	rec := do(t, srv, cookies, http.MethodPost, "/api/ingest/rotate-key", `{"disconnect_now":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	var body rotateKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if body.Key == "" {
		t.Fatal("la rotación no devolvió la clave nueva")
	}
	if body.Key == antes.Reveal() {
		t.Error("la clave no cambió")
	}

	despues, err := srv.db.RevealIngestKey(ctx, srv.cipher)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}
	if despues.Reveal() != body.Key {
		t.Error("la clave devuelta no es la que quedó guardada")
	}
	if body.KeyMask != despues.Mask() {
		t.Errorf("key_mask = %q, quería %q", body.KeyMask, despues.Mask())
	}
}

func TestRotateKeyAudits(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	if rec := do(t, srv, cookies, http.MethodPost, "/api/ingest/rotate-key", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d — %s", rec.Code, rec.Body.String())
	}

	eventos, err := srv.db.RecentEvents(context.Background(), 50)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	var visto bool
	for _, e := range eventos {
		if e.Kind == "ingest_key_rotated" {
			visto = true
		}
	}
	if !visto {
		t.Error("rotar la clave de ingesta no dejó evento")
	}
}

// TestRotateKeyEventDoesNotContainTheKey (spec §8): el evento dice que pasó, no qué clave
// quedó. El log de eventos se enseña entero en el panel.
func TestRotateKeyEventDoesNotContainTheKey(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodPost, "/api/ingest/rotate-key", `{}`)
	var body rotateKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar: %v", err)
	}

	eventos, err := srv.db.RecentEvents(context.Background(), 50)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	for _, e := range eventos {
		if strings.Contains(e.Message, body.Key) {
			t.Errorf("el evento %q lleva la clave nueva dentro: %s", e.Kind, e.Message)
		}
	}
}

// TestRotateKeyWithoutDisconnectLeavesTheSessionAlone: el default es NO cortar.
func TestRotateKeyWithoutDisconnectLeavesTheSessionAlone(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	dc := &fakeDisconnecter{corta: 1}
	srv.ingest = dc

	if rec := do(t, srv, cookies, http.MethodPost, "/api/ingest/rotate-key",
		`{"disconnect_now":false}`); rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d", rec.Code)
	}
	if dc.veces() != 0 {
		t.Errorf("se cortó la publicación sin pedirlo (%d llamadas)", dc.veces())
	}

	// Y con el cuerpo vacío, que es lo que manda un cliente descuidado: también false.
	if rec := do(t, srv, cookies, http.MethodPost, "/api/ingest/rotate-key", ""); rec.Code != http.StatusOK {
		t.Fatalf("rotate con cuerpo vacío: %d — %s", rec.Code, rec.Body.String())
	}
	if dc.veces() != 0 {
		t.Errorf("un cuerpo vacío cortó la publicación (%d llamadas)", dc.veces())
	}
}

// TestRotateKeyWithDisconnectCutsThePublisher: es el caso de "creo que se me filtró la
// clave".
func TestRotateKeyWithDisconnectCutsThePublisher(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	dc := &fakeDisconnecter{corta: 1}
	srv.ingest = dc

	if rec := do(t, srv, cookies, http.MethodPost, "/api/ingest/rotate-key",
		`{"disconnect_now":true}`); rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d — %s", rec.Code, rec.Body.String())
	}
	if dc.veces() != 1 {
		t.Errorf("llamadas a DisconnectPublisher = %d, quería 1", dc.veces())
	}

	eventos, err := srv.db.RecentEvents(context.Background(), 50)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	var visto bool
	for _, e := range eventos {
		if e.Kind == "ingest_disconnected" {
			visto = true
		}
	}
	if !visto {
		t.Error("cortar la publicación no dejó evento")
	}
}

// TestRotateKeyWithoutAnIngestConfigured: si el Disconnecter es nil —arranque parcial o
// test—, pedir el corte no debe entrar en pánico.
func TestRotateKeyWithoutAnIngestConfigured(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.ingest = nil

	if rec := do(t, srv, cookies, http.MethodPost, "/api/ingest/rotate-key",
		`{"disconnect_now":true}`); rec.Code != http.StatusOK {
		t.Errorf("código = %d, quería 200: %s", rec.Code, rec.Body.String())
	}
}

// --- estado ---

// TestStatusWithoutASessionSaysNotLive: es el estado en el que arranca el servicio y en el
// que pasa la mayor parte del tiempo.
func TestStatusWithoutASessionSaysNotLive(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodGet, "/api/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	st := decodeStatus(t, rec)
	if st.Session.Live {
		t.Error("dice que hay sesión viva sin haberla")
	}
	if st.Ingest.App == "" {
		t.Error("falta la app de ingesta")
	}
	if st.Destinations == nil {
		t.Error("destinations es null; quería un array vacío")
	}
}

// TestStatusIncludesMetricsForLiveDestinationsOnly: el DTO trae null en los destinos que no
// transmiten. La UI tiene que distinguir eso de un cero, o enseñará "0 kbps" en un destino
// apagado y parecerá que va mal.
func TestStatusIncludesMetricsForLiveDestinationsOnly(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	vivo := crearDest(t, db, srv, "vivo", "k1", true)
	quieto := crearDest(t, db, srv, "quieto", "k2", true)

	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{
		vivo.ID: {State: "live", BytesSent: 1234, BitrateBPS: 4000},
	})

	st := decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if len(st.Destinations) != 2 {
		t.Fatalf("destinos = %d, quería 2", len(st.Destinations))
	}
	for _, d := range st.Destinations {
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

// TestStatusReportsTheLiveSession: cuando hay sesión, el estado trae sus datos.
func TestStatusReportsTheLiveSession(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)

	id, err := db.StartSession(context.Background())
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	eng.setLive(id)

	st := decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if !st.Session.Live {
		t.Fatal("dice que no hay sesión habiéndola")
	}
	if st.Session.ID != id {
		t.Errorf("session.id = %d, quería %d", st.Session.ID, id)
	}
	if st.Session.StartedAt == nil {
		t.Error("falta started_at de la sesión viva")
	}
}

// TestStatusReportsResolutionWhileLive es el arreglo del hueco que apareció usando el
// producto: durante siete minutos de directo real a 720p, GET /api/status devolvía la
// resolución en null, porque solo se persistía al cerrar la sesión.
//
// El spec §10 pide que el panel enseñe «señal entrante, resolución, bitrate, tiempo
// transmitiendo» EN VIVO, así que sin esto la fase 5 no podría pintarlo.
func TestStatusReportsResolutionWhileLive(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	arranque := time.Now().Add(-90 * time.Second)
	eng.setSesion(relay.LiveSession{
		ID: 7, StartedAt: arranque, Width: 1280, Height: 720, BitrateBPS: 3_100_000,
	})

	st := decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if !st.Session.Live {
		t.Fatal("no dice que hay sesión")
	}
	if st.Session.Width == nil || *st.Session.Width != 1280 {
		t.Errorf("width = %v, quería 1280", st.Session.Width)
	}
	if st.Session.Height == nil || *st.Session.Height != 720 {
		t.Errorf("height = %v, quería 720", st.Session.Height)
	}
	if st.Session.BitrateBPS == nil || *st.Session.BitrateBPS != 3_100_000 {
		t.Errorf("bitrate_bps = %v, quería 3100000", st.Session.BitrateBPS)
	}
	if st.Session.StartedAt == nil || !st.Session.StartedAt.Equal(arranque.UTC()) {
		t.Errorf("started_at = %v, quería %v", st.Session.StartedAt, arranque)
	}
	// Y en UTC, como el resto de timestamps del contrato.
	if _, offset := st.Session.StartedAt.Zone(); offset != 0 {
		t.Errorf("started_at viene con desfase %d; el resto del JSON va en UTC", offset)
	}
}

// TestStatusResolutionIsNullUntilTheSequenceHeader: entre que el publisher conecta y llega
// el primer sequence header pasa un instante. En ese hueco la resolución debe ser null, no
// "0x0", para que la interfaz sepa que todavía no se sabe.
func TestStatusResolutionIsNullUntilTheSequenceHeader(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	eng.setSesion(relay.LiveSession{ID: 7, StartedAt: time.Now()})

	st := decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if !st.Session.Live {
		t.Fatal("no dice que hay sesión")
	}
	if st.Session.Width != nil || st.Session.Height != nil {
		t.Errorf("resolución = %v x %v, quería null antes del sequence header",
			st.Session.Width, st.Session.Height)
	}
}

// TestStatusSurvivesASessionRowThatIsNotThereYet: SessionID puede adelantarse a la fila.
// No es un error: se devuelve lo que se sabe.
func TestStatusSurvivesASessionRowThatIsNotThereYet(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	eng.setLive(9999) // un id que no tiene fila en sessions

	rec := do(t, srv, cookies, http.MethodGet, "/api/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	st := decodeStatus(t, rec)
	if !st.Session.Live {
		t.Error("debería seguir diciendo que hay sesión")
	}
}

// TestStatusNeverIncludesAKey (spec §8): el estado es lo que más viaja —va por el WebSocket
// cada segundo—, así que es donde más caro sale un descuido.
func TestStatusNeverIncludesAKey(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	const clave = "clave-de-destino-inconfundible"
	crearDest(t, db, srv, "yt", clave, true)

	ingesta, err := db.RevealIngestKey(context.Background(), srv.cipher)
	if err != nil {
		t.Fatalf("RevealIngestKey: %v", err)
	}

	cuerpo := do(t, srv, cookies, http.MethodGet, "/api/status", "").Body.String()
	if strings.Contains(cuerpo, clave) {
		t.Error("el estado lleva la clave de un destino")
	}
	if strings.Contains(cuerpo, ingesta.Reveal()) {
		t.Error("el estado lleva la clave de ingesta")
	}
}

// --- eventos ---

// TestEventsComeNewestFirst depende de la Task 1: sin el arreglo del orden lexicográfico,
// dos eventos del mismo segundo con fracciones de distinta longitud salen al revés.
func TestEventsComeNewestFirst(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	ctx := context.Background()

	// Seguidos a propósito: caen en el mismo segundo, que es el caso que rompía.
	for _, msg := range []string{"primero", "segundo", "tercero"} {
		if _, err := db.LogEvent(ctx, store.Event{
			Level: store.LevelInfo, Kind: "test", Message: msg,
		}); err != nil {
			t.Fatalf("LogEvent(%s): %v", msg, err)
		}
	}

	rec := do(t, srv, cookies, http.MethodGet, "/api/events", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}

	var got []eventDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(got) < 3 {
		t.Fatalf("eventos = %d, quería al menos 3", len(got))
	}
	if got[0].Message != "tercero" {
		t.Errorf("el primero es %q, quería tercero: van del más reciente al más antiguo",
			got[0].Message)
	}
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.After(got[i-1].CreatedAt) {
			t.Errorf("orden roto entre las posiciones %d y %d", i-1, i)
		}
	}
}

func TestEventsRespectsTheLimit(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if _, err := db.LogEvent(ctx, store.Event{
			Level: store.LevelInfo, Kind: "test", Message: "x",
		}); err != nil {
			t.Fatalf("LogEvent: %v", err)
		}
	}

	rec := do(t, srv, cookies, http.MethodGet, "/api/events?limit=3", "")
	var got []eventDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("eventos = %d, quería 3", len(got))
	}
}

// TestEventsClampsAnOutOfRangeLimit: RecentEvents ya acota por arriba y por abajo, así que
// un límite absurdo se ajusta en vez de ser un error.
func TestEventsClampsAnOutOfRangeLimit(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	if _, err := db.LogEvent(context.Background(), store.Event{
		Level: store.LevelInfo, Kind: "test", Message: "x",
	}); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}

	for _, limit := range []string{"0", "-5", "999999"} {
		t.Run("limit="+limit, func(t *testing.T) {
			rec := do(t, srv, cookies, http.MethodGet, "/api/events?limit="+limit, "")
			if rec.Code != http.StatusOK {
				t.Errorf("código = %d, quería 200: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestEventsRejectsANonNumericLimit: esto sí es una petición mal formada.
func TestEventsRejectsANonNumericLimit(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodGet, "/api/events?limit=muchos", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("código = %d, quería 400: %s", rec.Code, rec.Body.String())
	}
	if got := errorCodeDe(t, rec); got != codeInvalidInput {
		t.Errorf("code = %q", got)
	}
}

// TestEventsIsAnArrayEvenWhenEmpty
func TestEventsIsAnArrayEvenWhenEmpty(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodGet, "/api/events", "")
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("cuerpo = %q, quería []", got)
	}
}
