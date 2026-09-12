package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/probe"
	"github.com/aprendomx/splitstream/internal/store"
)

// cuentaDePlataforma mete una cuenta conectada directamente por el store, para preparar el
// estado sin pasar por el flujo de autorización.
func cuentaDePlataforma(t *testing.T, srv *Server, db *store.DB, plat store.Platform, externalID, nombre string) *store.Account {
	t.Helper()
	a, err := db.UpsertAccount(context.Background(), srv.cipher, store.NewAccount{
		Platform: plat, ExternalID: externalID, DisplayName: nombre, Scopes: []string{"chat:read"},
		OwnApp: true, Credentials: store.Credentials{ClientID: "ci", ClientSecret: "ksec"},
		Tokens: store.Tokens{Access: "tok-acceso-fixture", Refresh: "r", ExpiresAt: time.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	return a
}

// eventosPorTipo cuenta los eventos recientes de cada clase.
func eventosPorTipo(t *testing.T, db *store.DB) map[string]int {
	t.Helper()
	ev, err := db.RecentEvents(context.Background(), 50)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	out := map[string]int{}
	for _, e := range ev {
		out[e.Kind]++
	}
	return out
}

// El alta desde una cuenta de YouTube: la emisión la crea la plataforma y ni la URL ni la
// clave se escriben a mano. La clave solo sale enmascarada.
func TestCreateDestinationFromAccountWithABroadcast(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true, requiresOwnApp: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")

	rec := do(t, srv, ck, http.MethodPost, "/api/destinations/from-account",
		`{"account_id":`+itoa(a.ID)+`,"name":"YouTube"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("from-account = %d %s", rec.Code, rec.Body)
	}
	dto := decodeDest(t, rec)
	if dto.RTMPURL != "rtmp://a.rtmp.youtube.com/live2" {
		t.Errorf("rtmp_url = %q", dto.RTMPURL)
	}
	if dto.KeyMask != crypto.Secret("clave-api").Mask() || !dto.KeyFromAPI {
		t.Errorf("clave = %q, key_from_api = %v", dto.KeyMask, dto.KeyFromAPI)
	}
	if dto.Broadcast == nil || dto.Broadcast.BroadcastRef != "b1" || dto.Broadcast.LiveChatID != "c1" ||
		dto.Broadcast.Status != store.BroadcastCreated {
		t.Fatalf("emisión = %+v", dto.Broadcast)
	}
	if dto.Broadcast.WatchURL != "https://www.youtube.com/watch?v=b1" {
		t.Errorf("watch_url = %q", dto.Broadcast.WatchURL)
	}
	if dto.Account == nil || dto.Account.ID != a.ID {
		t.Errorf("cuenta = %+v", dto.Account)
	}
	if strings.Contains(rec.Body.String(), "clave-api") || strings.Contains(rec.Body.String(), "tok-") {
		t.Errorf("la respuesta lleva la clave o el token: %s", rec.Body)
	}

	p.mu.Lock()
	peticiones := append([]platforms.BroadcastRequest(nil), p.peticiones...)
	p.mu.Unlock()
	if len(peticiones) != 1 || peticiones[0].Title != "YouTube" || peticiones[0].Privacy != "unlisted" {
		t.Errorf("petición = %+v", peticiones)
	}

	kinds := eventosPorTipo(t, db)
	if kinds["broadcast_created"] != 1 || kinds["destination_key_from_api"] != 1 {
		t.Errorf("eventos = %+v", kinds)
	}

	// Y el listado lo enseña igual: la emisión viaja con cada destino.
	rec = do(t, srv, ck, http.MethodGet, "/api/destinations", "")
	var lista []destinationDTO
	json.Unmarshal(rec.Body.Bytes(), &lista)
	if len(lista) != 1 || lista[0].Broadcast == nil || lista[0].Broadcast.BroadcastRef != "b1" || !lista[0].KeyFromAPI {
		t.Errorf("listado = %+v", lista)
	}
	if !lista[0].Capabilities.Schedule || !lista[0].Capabilities.RequiresOwnApp {
		t.Errorf("capacidades = %+v", lista[0].Capabilities)
	}
}

// En Kick no hay emisión que crear: la clave es del canal y se lee tal cual.
func TestCreateDestinationFromAccountWithTheChannelKey(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.Kick, ingest: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformKick, "123", "kickdev")

	rec := do(t, srv, ck, http.MethodPost, "/api/destinations/from-account",
		`{"account_id":`+itoa(a.ID)+`,"name":"Kick"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("from-account = %d %s", rec.Code, rec.Body)
	}
	dto := decodeDest(t, rec)
	if dto.RTMPURL != "rtmps://stream.kick.com/1" || dto.KeyMask != crypto.Secret("kick-key").Mask() {
		t.Errorf("destino = %+v", dto)
	}
	if dto.Broadcast == nil || dto.Broadcast.BroadcastRef != "" || !dto.Broadcast.KeyFromAPI {
		t.Errorf("emisión = %+v", dto.Broadcast)
	}
	// Sin emisión no hay evento de emisión creada, pero sí el de la clave por API.
	kinds := eventosPorTipo(t, db)
	if kinds["broadcast_created"] != 0 || kinds["destination_key_from_api"] != 1 {
		t.Errorf("eventos = %+v", kinds)
	}
	if strings.Contains(rec.Body.String(), "kick-key") {
		t.Errorf("la respuesta lleva la clave: %s", rec.Body)
	}
}

// Una cuenta que hay que reconectar no puede pedir nada a la plataforma, y una plataforma
// que no da la clave por API no tiene de dónde sacar el destino.
func TestCreateDestinationFromAccountRefusesWhatItCannotDo(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")
	if err := db.SetAccountStatus(context.Background(), a.ID, store.AccountStatusReauth); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, ck, http.MethodPost, "/api/destinations/from-account", `{"account_id":`+itoa(a.ID)+`,"name":"YouTube"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("cuenta en reauth = %d, quería 409: %s", rec.Code, rec.Body)
	}

	// Twitch: sabe título y categoría, pero no da ni emisión ni clave.
	tw := &fakeProvider{configured: true}
	srv2, db2, ck2 := servidorPlataformas(t, tw)
	a2 := cuentaDePlataforma(t, srv2, db2, store.PlatformTwitch, "1", "twitchdev")
	rec = do(t, srv2, ck2, http.MethodPost, "/api/destinations/from-account", `{"account_id":`+itoa(a2.ID)+`,"name":"Twitch"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no da la clave por API") {
		t.Errorf("sin capacidad = %d %s", rec.Code, rec.Body)
	}
	// Y sin nombre no hay destino que crear.
	rec = do(t, srv2, ck2, http.MethodPost, "/api/destinations/from-account", `{"account_id":`+itoa(a2.ID)+`,"name":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("sin nombre = %d, quería 400", rec.Code)
	}
}

// Crear una emisión nueva sobre un destino que ya existe sustituye su URL y su clave: es lo
// que se hace al día siguiente, sin volver a dar de alta el destino.
func TestCreateBroadcastReplacesURLAndKey(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")

	rec := do(t, srv, ck, http.MethodPost, "/api/destinations/from-account", `{"account_id":`+itoa(a.ID)+`,"name":"YouTube"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("from-account = %d %s", rec.Code, rec.Body)
	}
	d := decodeDest(t, rec)

	rec = do(t, srv, ck, http.MethodPost, "/api/destinations/"+itoa(d.ID)+"/broadcast", `{"title":"Mañana","privacy":"public"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("broadcast = %d %s", rec.Code, rec.Body)
	}
	nueva := decodeDest(t, rec)
	if nueva.RTMPURL != "rtmp://b.rtmp.youtube.com/live2" || nueva.KeyMask != crypto.Secret("clave-api-2").Mask() {
		t.Errorf("destino tras la emisión nueva = %+v", nueva)
	}
	if nueva.Broadcast == nil || nueva.Broadcast.BroadcastRef != "b2" {
		t.Fatalf("emisión = %+v", nueva.Broadcast)
	}
	p.mu.Lock()
	peticiones := append([]platforms.BroadcastRequest(nil), p.peticiones...)
	p.mu.Unlock()
	if len(peticiones) != 2 || peticiones[1].Title != "Mañana" || peticiones[1].Privacy != "public" {
		t.Errorf("peticiones = %+v", peticiones)
	}

	// Y se puede consultar suelta.
	rec = do(t, srv, ck, http.MethodGet, "/api/destinations/"+itoa(d.ID)+"/broadcast", "")
	var b broadcastDTO
	json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != http.StatusOK || b.BroadcastRef != "b2" || b.WatchURL != "https://www.youtube.com/watch?v=b2" {
		t.Errorf("GET broadcast = %d %+v", rec.Code, b)
	}
	// Un destino sin emisión no tiene qué devolver.
	otro := crearDest(t, db, srv, "custom", "clave", true)
	if rec := do(t, srv, ck, http.MethodGet, "/api/destinations/"+itoa(otro.ID)+"/broadcast", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET sin emisión = %d, quería 404", rec.Code)
	}
}

// Sacar la emisión al aire y cerrarla. Donde el canal sale solo (Kick) no hay nada que
// pulsar y la API lo dice en vez de fingir que hizo algo.
func TestStartAndEndBroadcast(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")
	rec := do(t, srv, ck, http.MethodPost, "/api/destinations/from-account", `{"account_id":`+itoa(a.ID)+`,"name":"YouTube"}`)
	d := decodeDest(t, rec)

	rec = do(t, srv, ck, http.MethodPost, "/api/destinations/"+itoa(d.ID)+"/broadcast/start", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	var b broadcastDTO
	json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Status != store.BroadcastLive {
		t.Errorf("estado tras start = %q", b.Status)
	}
	guardada, err := db.BroadcastFor(context.Background(), d.ID)
	if err != nil || guardada.Status != store.BroadcastLive {
		t.Errorf("emisión guardada = %+v (err=%v)", guardada, err)
	}

	rec = do(t, srv, ck, http.MethodPost, "/api/destinations/"+itoa(d.ID)+"/broadcast/end", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("end = %d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Status != store.BroadcastComplete {
		t.Errorf("estado tras end = %q", b.Status)
	}
	p.mu.Lock()
	arrancadas, cerradas := p.arrancadas, p.cerradas
	p.mu.Unlock()
	if arrancadas != 1 || cerradas != 1 {
		t.Errorf("arrancadas = %d, cerradas = %d", arrancadas, cerradas)
	}
	kinds := eventosPorTipo(t, db)
	if kinds["broadcast_started"] != 1 || kinds["broadcast_ended"] != 1 {
		t.Errorf("eventos = %+v", kinds)
	}

	// Kick: la clave es del canal y no hay emisión que arrancar.
	k := &fakeProvider{configured: true, id: platforms.Kick, ingest: true}
	srv2, db2, ck2 := servidorPlataformas(t, k)
	a2 := cuentaDePlataforma(t, srv2, db2, store.PlatformKick, "123", "kickdev")
	rec = do(t, srv2, ck2, http.MethodPost, "/api/destinations/from-account", `{"account_id":`+itoa(a2.ID)+`,"name":"Kick"}`)
	dk := decodeDest(t, rec)
	rec = do(t, srv2, ck2, http.MethodPost, "/api/destinations/"+itoa(dk.ID)+"/broadcast/start", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "sale al aire sola") {
		t.Errorf("start en Kick = %d %s", rec.Code, rec.Body)
	}
}

// Con la clave traída por API no hay clave mal pegada posible: «probar destino» se salta la
// sonda en vez de gastar una conexión contra la emisión recién creada.
func TestTestDestinationSkipsWhenTheKeyCameFromTheAPI(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")
	rec := do(t, srv, ck, http.MethodPost, "/api/destinations/from-account", `{"account_id":`+itoa(a.ID)+`,"name":"YouTube"}`)
	d := decodeDest(t, rec)

	ft := &fakeTester{res: probe.Result{Outcome: probe.Plausible, Stage: "grace"}}
	srv.tester = ft
	rec = do(t, srv, ck, http.MethodPost, "/api/destinations/"+itoa(d.ID)+"/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("test = %d %s", rec.Code, rec.Body)
	}
	var out testSkippedDTO
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.Skipped || out.Message == "" {
		t.Errorf("respuesta = %+v", out)
	}
	if len(ft.sondeado) != 0 {
		t.Errorf("se sondeó %v con la clave por API", ft.sondeado)
	}
}

// El título de YouTube es de la EMISIÓN: con una emisión vinculada se cambia por su id, no
// buscando el directo del canal.
func TestLiveTitleUsesTheBroadcastWhenThereIsOne(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")
	rec := do(t, srv, ck, http.MethodPost, "/api/destinations/from-account", `{"account_id":`+itoa(a.ID)+`,"name":"YouTube"}`)
	d := decodeDest(t, rec)

	rec = do(t, srv, ck, http.MethodPost, "/api/live/title", `{"title":"Hola","destinations":[`+itoa(d.ID)+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("title = %d %s", rec.Code, rec.Body)
	}
	var res []liveResultDTO
	json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("resultado = %+v", res)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.titulosEmision) != 1 || p.titulosEmision[0] != "b1:Hola" {
		t.Errorf("títulos de emisión = %v", p.titulosEmision)
	}
	if len(p.titulos) != 0 {
		t.Errorf("se cambió el título del canal en vez del de la emisión: %v", p.titulos)
	}
}

// destinoDesdeCuenta crea un destino con emisión por el camino normal y devuelve su id.
func destinoDesdeCuenta(t *testing.T, srv *Server, ck []*http.Cookie, a *store.Account, nombre string) int64 {
	t.Helper()
	rec := do(t, srv, ck, http.MethodPost, "/api/destinations/from-account",
		`{"account_id":`+itoa(a.ID)+`,"name":"`+nombre+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("from-account = %d %s", rec.Code, rec.Body)
	}
	return decodeDest(t, rec).ID
}

// TestFromAccountNoDejaDestinosHuerfanos: entre crear el destino y vincularle la cuenta
// caben fallos (la cuenta se desconectó desde otra pestaña, la base falló). Antes, el
// destino quedaba creado —con la clave que la plataforma acababa de dar— pero sin cuenta
// ni emisión, y quien pedía el alta recibía un error sin saber que se le había quedado un
// destino a medias en el panel.
func TestFromAccountNoDejaDestinosHuerfanos(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")
	// La cuenta desaparece justo después de que la plataforma diera la emisión: el destino
	// llega a crearse, pero LinkDestination ya no encuentra a quién vincularlo.
	p.trasEmision = func() {
		if err := db.DeleteAccount(context.Background(), a.ID); err != nil {
			t.Errorf("DeleteAccount: %v", err)
		}
	}

	rec := do(t, srv, ck, http.MethodPost, "/api/destinations/from-account",
		`{"account_id":`+itoa(a.ID)+`,"name":"YouTube"}`)
	if rec.Code < 400 {
		t.Fatalf("from-account = %d %s, quería un error", rec.Code, rec.Body)
	}

	dests, err := db.ListDestinations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dests) != 0 {
		t.Errorf("quedaron %d destinos a medio crear: %+v", len(dests), dests)
	}
}

// TestCreateBroadcastNoDejaLaClaveSinSuEmision: la clave nueva del destino y la emisión
// que la justifica viven en tablas distintas. Escritas por separado, un fallo de la
// segunda dejaba el destino emitiendo con una clave cuya emisión no conocíamos: el panel
// enseñaba el broadcast_ref viejo y «terminar emisión» habría cerrado la que no era.
func TestCreateBroadcastNoDejaLaClaveSinSuEmision(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")
	id := destinoDesdeCuenta(t, srv, ck, a, "YouTube")

	antes, err := db.DestinationByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}

	// La segunda emisión sale bien de la plataforma, pero la cuenta se desconecta antes de
	// que se escriba: SetBroadcast se topa con la clave ajena y la transacción cae entera.
	p.trasEmision = func() {
		if err := db.DeleteAccount(context.Background(), a.ID); err != nil {
			t.Errorf("DeleteAccount: %v", err)
		}
	}
	rec := do(t, srv, ck, http.MethodPost, destPath(id)+"/broadcast", "")
	if rec.Code < 400 {
		t.Fatalf("broadcast = %d %s, quería un error", rec.Code, rec.Body)
	}

	despues, err := db.DestinationByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if despues.RTMPURL != antes.RTMPURL || despues.KeyMask != antes.KeyMask {
		t.Errorf("el destino se quedó con la clave de una emisión que no se guardó: antes %s/%s, después %s/%s",
			antes.RTMPURL, antes.KeyMask, despues.RTMPURL, despues.KeyMask)
	}
}

// TestPatchDestinationQuitaLaEmisionAlCambiarDeCuenta: la emisión es de la cuenta que la
// creó. Si el destino pasa a otra cuenta o se queda sin ninguna, lo que quedaba en
// destination_broadcasts ya no es suyo.
func TestPatchDestinationQuitaLaEmisionAlCambiarDeCuenta(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")
	otra := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "456", "otro canal")
	id := destinoDesdeCuenta(t, srv, ck, a, "YouTube")

	// A otra cuenta: la emisión de la primera se va.
	rec := do(t, srv, ck, http.MethodPatch, destPath(id), `{"account_id":`+itoa(otra.ID)+`}`)
	if rec.Code != 200 {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	if dto := decodeDest(t, rec); dto.Broadcast != nil {
		t.Errorf("la emisión sobrevivió al cambio de cuenta: %+v", dto.Broadcast)
	}
	if _, err := db.BroadcastFor(context.Background(), id); err == nil {
		t.Errorf("destination_broadcasts sigue teniendo la fila de la cuenta anterior")
	}

	// Y desvincular del todo también la quita.
	id2 := destinoDesdeCuenta(t, srv, ck, a, "YouTube 2")
	rec = do(t, srv, ck, http.MethodPatch, destPath(id2), `{"account_id":null}`)
	if rec.Code != 200 {
		t.Fatalf("patch null = %d %s", rec.Code, rec.Body)
	}
	if _, err := db.BroadcastFor(context.Background(), id2); err == nil {
		t.Errorf("desvincular dejó la emisión en pie")
	}
	// La clave NO se toca: desvincular una cuenta no es dejar de poder emitir.
	d, err := db.DestinationByID(context.Background(), id2)
	if err != nil {
		t.Fatal(err)
	}
	if d.KeyMask == "" || d.RTMPURL == "" {
		t.Errorf("desvincular se llevó por delante la clave del destino: %+v", d)
	}
}

// TestDeleteAccountQuitaLasEmisionesDeSusDestinos: desconectar la cuenta se lleva las
// emisiones que había creado. La clave del destino se queda: quien desconecta una cuenta
// no está pidiendo dejar de emitir.
func TestDeleteAccountQuitaLasEmisionesDeSusDestinos(t *testing.T) {
	p := &fakeProvider{configured: true, id: platforms.YouTube, schedule: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaDePlataforma(t, srv, db, store.PlatformYouTube, "123", "canal")
	id := destinoDesdeCuenta(t, srv, ck, a, "YouTube")

	if rec := do(t, srv, ck, http.MethodDelete, "/api/accounts/"+itoa(a.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if _, err := db.BroadcastFor(context.Background(), id); err == nil {
		t.Errorf("la emisión sobrevivió a la desconexión de la cuenta")
	}
	d, err := db.DestinationByID(context.Background(), id)
	if err != nil {
		t.Fatalf("el destino tenía que seguir ahí: %v", err)
	}
	if d.KeyMask == "" || d.KeyFromAPI {
		t.Errorf("destino tras desconectar la cuenta = %+v", d)
	}
}
