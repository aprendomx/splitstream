package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/probe"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// fakeTester devuelve un resultado fijo y apunta a quién sondeó.
type fakeTester struct {
	res      probe.Result
	err      error
	sondeado []int64
}

func (f *fakeTester) Test(ctx context.Context, d store.Destination) (probe.Result, error) {
	f.sondeado = append(f.sondeado, d.ID)
	return f.res, f.err
}

func decodeProbe(t *testing.T, body []byte) probeDTO {
	t.Helper()
	var out probeDTO
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decodificar: %v — %s", err, body)
	}
	return out
}

func TestTestDestinationReportsTheOutcome(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "clave-inconfundible", true)
	ft := &fakeTester{res: probe.Result{Outcome: probe.Plausible, Stage: "grace", Elapsed: 3100 * time.Millisecond}}
	srv.tester = ft

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodeProbe(t, rec.Body.Bytes())
	if got.Outcome != "plausible" || got.Stage != "grace" || got.ElapsedMS != 3100 {
		t.Errorf("dto = %+v", got)
	}
	if got.Message == "" {
		t.Error("falta el mensaje para personas")
	}
	if len(ft.sondeado) != 1 || ft.sondeado[0] != d.ID {
		t.Errorf("se sondeó %v, quería [%d]", ft.sondeado, d.ID)
	}

	eventos, err := db.RecentEvents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventos) == 0 || eventos[0].Kind != "destination_tested" || eventos[0].Level != store.LevelInfo {
		t.Errorf("evento = %+v, quería destination_tested info", eventos)
	}
}

// Un resultado malo queda como warn, y el mensaje explica qué revisar.
func TestTestDestinationClosedEarlyIsAWarning(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "tw", "k", true)
	srv.tester = &fakeTester{res: probe.Result{Outcome: probe.ClosedEarly, Stage: "grace", Err: errors.New("EOF")}}

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", "")
	got := decodeProbe(t, rec.Body.Bytes())
	if got.Outcome != "closed_early" || !strings.Contains(got.Message, "clave") {
		t.Errorf("dto = %+v", got)
	}
	eventos, _ := db.RecentEvents(context.Background(), 1)
	if eventos[0].Level != store.LevelWarn {
		t.Errorf("nivel = %s, quería warn", eventos[0].Level)
	}
}

// Probar un destino que está emitiendo abriría una segunda publicación con la misma
// clave, y la plataforma cortaría la que va en vivo.
func TestTestDestinationRefusesALiveDestination(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)
	ft := &fakeTester{res: probe.Result{Outcome: probe.Plausible}}
	srv.tester = ft
	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{d.ID: {State: "live"}})

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("código = %d, quería 409: %s", rec.Code, rec.Body.String())
	}
	if len(ft.sondeado) != 0 {
		t.Error("se sondeó un destino en vivo")
	}
}

// Con sesión viva pero el destino apagado o suspendido, sí se puede probar.
func TestTestDestinationAllowedWhenNotLive(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)
	srv.tester = &fakeTester{res: probe.Result{Outcome: probe.Plausible}}
	eng.setLive(7)
	eng.setMetrics(map[int64]relay.Metrics{d.ID: {State: "suspended"}})

	if rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", ""); rec.Code != http.StatusOK {
		t.Fatalf("código = %d, quería 200: %s", rec.Code, rec.Body.String())
	}
}

func TestTestDestinationUnknownIs404(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.tester = &fakeTester{}
	if rec := do(t, srv, cookies, http.MethodPost, destPath(9999)+"/test", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("código = %d, quería 404", rec.Code)
	}
}

func TestTestDestinationWithoutATesterIsAConflict(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "yt", "k", true)
	srv.tester = nil
	if rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", ""); rec.Code != http.StatusConflict {
		t.Fatalf("código = %d, quería 409", rec.Code)
	}
}

// El evento y la respuesta no pueden llevar la URL ni la clave: el error de la sonda
// tampoco las lleva, pero esto lo comprueba en la frontera HTTP.
func TestTestDestinationNeverLeaksTheKey(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	const clave = "clave-inconfundible-9x7"
	d := crearDest(t, db, srv, "yt", clave, true)
	srv.tester = &fakeTester{res: probe.Result{Outcome: probe.Rejected, Stage: "publish", Err: errors.New("publish en host: rechazado")}}

	rec := do(t, srv, cookies, http.MethodPost, destPath(d.ID)+"/test", "")
	if strings.Contains(rec.Body.String(), clave) {
		t.Error("la respuesta lleva la clave")
	}
	eventos, _ := db.RecentEvents(context.Background(), 1)
	if strings.Contains(eventos[0].Message, clave) {
		t.Error("el evento lleva la clave")
	}
}
