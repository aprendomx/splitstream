package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

func TestLiveTitleAppliesPerDestinationAndNeverAllOrNothing(t *testing.T) {
	p := &fakeProvider{configured: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaViaStore(t, srv, db)
	// crearDest fija siempre platform=custom (ver destinations_test.go); aquí hace falta
	// twitch para ejercitar TitleSetter/CategorySetter, así que se crean directamente por
	// el store, igual que "custom" más abajo.
	conCuenta, err := db.CreateDestination(context.Background(), srv.cipher, store.NewDestination{Name: "Twitch", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.LinkDestination(context.Background(), conCuenta.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	sinCuenta, err := db.CreateDestination(context.Background(), srv.cipher, store.NewDestination{Name: "Twitch 2", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	custom, _ := db.CreateDestination(context.Background(), srv.cipher, store.NewDestination{Name: "Otro", Platform: store.PlatformCustom, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})

	body := `{"title":"  Hola mundo ","category_id":"509670","destinations":[` + itoa(conCuenta.ID) + `,` + itoa(sinCuenta.ID) + `,` + itoa(custom.ID) + `,999]}`
	rec := do(t, srv, ck, http.MethodPost, "/api/live/title", body)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var res []liveResultDTO
	json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res) != 4 {
		t.Fatalf("resultados = %+v", res)
	}
	porID := map[int64]liveResultDTO{}
	for _, r := range res {
		porID[r.DestinationID] = r
	}
	if !porID[conCuenta.ID].OK || porID[sinCuenta.ID].OK || porID[custom.ID].OK || porID[999].OK {
		t.Errorf("resultados = %+v", res)
	}
	if !strings.Contains(porID[sinCuenta.ID].Message, "cuenta") || !strings.Contains(porID[custom.ID].Message, "no permite") {
		t.Errorf("mensajes = %+v", res)
	}
	if len(p.titulos) != 1 || !strings.HasPrefix(p.titulos[0], "twitchdev:Hola mundo:tok-acceso-fixture") || len(p.categorias) != 1 {
		t.Errorf("proveedor recibió %v / %v", p.titulos, p.categorias)
	}
	ev, _ := db.RecentEvents(context.Background(), 5)
	if ev[0].Kind != "channel_updated" || ev[0].DestinationID == nil || *ev[0].DestinationID != conCuenta.ID {
		t.Errorf("evento = %+v", ev[0])
	}

	// Sin título ni categoría → 400; una cuenta en reauth → resultado con mensaje, no 500.
	if rec := do(t, srv, ck, http.MethodPost, "/api/live/title", `{"destinations":[1]}`); rec.Code != 400 {
		t.Errorf("vacío = %d", rec.Code)
	}
	db.SetAccountStatus(context.Background(), a.ID, store.AccountStatusReauth)
	rec = do(t, srv, ck, http.MethodPost, "/api/live/title", `{"title":"x","destinations":[`+itoa(conCuenta.ID)+`]}`)
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || res[0].OK || !strings.Contains(res[0].Message, "reconect") {
		t.Errorf("reauth: %d %+v", rec.Code, res)
	}
}

func TestCategoriesSearchUsesTheFirstOKTwitchAccount(t *testing.T) {
	srv, db, ck := servidorPlataformas(t, &fakeProvider{configured: true})
	if rec := do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/categories?q=sci", ""); rec.Code != http.StatusConflict {
		t.Errorf("sin cuenta = %d, quería 409", rec.Code)
	}
	cuentaViaStore(t, srv, db)
	rec := do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/categories?q=sci", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "509670") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(t, srv, ck, http.MethodGet, "/api/platforms/twitch/categories?q=", ""); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("q vacía = %d %s", rec.Code, rec.Body)
	}
}

// TestLiveTitleReportsPlatformErrorsWithoutLeakingTheToken cubre I-5: un error de la
// plataforma —tanto el traducido (ErrUnauthorized) como uno genérico— tiene que llegar
// como un resultado legible (ok: false, mensaje) y nunca con el token de por medio.
func TestLiveTitleReportsPlatformErrorsWithoutLeakingTheToken(t *testing.T) {
	p := &fakeProvider{configured: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaViaStore(t, srv, db)
	d, err := db.CreateDestination(context.Background(), srv.cipher, store.NewDestination{
		Name: "Twitch", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.LinkDestination(context.Background(), d.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	body := `{"title":"x","destinations":[` + itoa(d.ID) + `]}`

	p.fallaTitle = platforms.ErrUnauthorized
	rec := do(t, srv, ck, http.MethodPost, "/api/live/title", body)
	var res []liveResultDTO
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || len(res) != 1 || res[0].OK || !strings.Contains(res[0].Message, "reconéctala") {
		t.Errorf("no autorizado: %d %+v", rec.Code, res)
	}
	if strings.Contains(rec.Body.String(), "tok-acceso-fixture") {
		t.Error("la respuesta lleva el token (ErrUnauthorized)")
	}

	p.fallaTitle = errors.New("boom")
	rec = do(t, srv, ck, http.MethodPost, "/api/live/title", body)
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || len(res) != 1 || res[0].OK || !strings.Contains(res[0].Message, "boom") {
		t.Errorf("error genérico: %d %+v", rec.Code, res)
	}
	if strings.Contains(rec.Body.String(), "tok-acceso-fixture") {
		t.Error("la respuesta lleva el token (error genérico)")
	}
}

// TestLiveTitleCapsDeduplicatesAndTimesOutPerDestination cubre el tope de destinos, la
// deduplicación de ids y el plazo por destino: sin ellos, una lista larga o una plataforma
// colgada dejaban la petición ocupada sin límite.
func TestLiveTitleCapsDeduplicatesAndTimesOutPerDestination(t *testing.T) {
	p := &fakeProvider{configured: true}
	srv, db, ck := servidorPlataformas(t, p)
	a := cuentaViaStore(t, srv, db)
	conCuenta, err := db.CreateDestination(context.Background(), srv.cipher, store.NewDestination{
		Name: "Twitch", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.LinkDestination(context.Background(), conCuenta.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	sinCuenta, err := db.CreateDestination(context.Background(), srv.cipher, store.NewDestination{
		Name: "Twitch 2", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// 21 destinos: uno por encima del tope.
	ids := make([]string, 0, 21)
	for i := 1; i <= 21; i++ {
		ids = append(ids, itoa(int64(i)))
	}
	rec := do(t, srv, ck, http.MethodPost, "/api/live/title",
		`{"title":"x","destinations":[`+strings.Join(ids, ",")+`]}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "20 destinos") {
		t.Errorf("21 destinos = %d %s, quería 400", rec.Code, rec.Body)
	}
	// Justo en el tope sí pasa.
	if rec := do(t, srv, ck, http.MethodPost, "/api/live/title",
		`{"title":"x","destinations":[`+strings.Join(ids[:20], ",")+`]}`); rec.Code != 200 {
		t.Errorf("20 destinos = %d %s, quería 200", rec.Code, rec.Body)
	}

	// Ids repetidos: un solo resultado por id y una sola llamada a la plataforma. Se
	// limpia lo anotado antes: la petición de 20 destinos de arriba ya incluía a este.
	p.mu.Lock()
	p.titulos = nil
	p.mu.Unlock()
	repetido := itoa(conCuenta.ID)
	rec = do(t, srv, ck, http.MethodPost, "/api/live/title",
		`{"title":"Hola","destinations":[`+repetido+`,`+repetido+`,`+repetido+`]}`)
	var res []liveResultDTO
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || len(res) != 1 || res[0].DestinationID != conCuenta.ID || !res[0].OK {
		t.Errorf("ids repetidos: %d %+v", rec.Code, res)
	}
	p.mu.Lock()
	llamadas := len(p.titulos)
	p.mu.Unlock()
	if llamadas != 1 {
		t.Errorf("llamadas a SetTitle = %d, quería 1", llamadas)
	}

	// Plazo por destino: la plataforma tarda más de lo permitido y el resto sigue.
	srv.liveTimeout = 50 * time.Millisecond
	p.lentoTitle = 2 * time.Second
	rec = do(t, srv, ck, http.MethodPost, "/api/live/title",
		`{"title":"Hola","destinations":[`+itoa(conCuenta.ID)+`,`+itoa(sinCuenta.ID)+`]}`)
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || len(res) != 2 {
		t.Fatalf("plazo: %d %+v", rec.Code, res)
	}
	if res[0].OK || !strings.Contains(res[0].Message, "tardó demasiado") {
		t.Errorf("el destino lento = %+v, quería ok:false y «tardó demasiado»", res[0])
	}
	if res[1].DestinationID != sinCuenta.ID || res[1].OK || !strings.Contains(res[1].Message, "cuenta") {
		t.Errorf("el resto no se procesó: %+v", res[1])
	}
}
