package sinks_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/sinks"
	"github.com/aprendomx/splitstream/internal/store"
)

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	var k [32]byte
	for i := range k {
		k[i] = 7
	}
	c, err := crypto.NewCipher(k)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

// setup deja una base abierta y arrancada, con su cipher y la fábrica bajo prueba.
func setup(t *testing.T) (*store.DB, *crypto.Cipher, *sinks.Factory) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	c := testCipher(t)
	if err := db.Bootstrap(ctx, c); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return db, c, sinks.NewFactory(db, c, nil)
}

// crear mete un destino y devuelve su fila.
func crear(t *testing.T, db *store.DB, c *crypto.Cipher, nombre string, enabled bool) *store.Destination {
	t.Helper()
	d, err := db.CreateDestination(context.Background(), c, store.NewDestination{
		Name: nombre, Platform: store.PlatformCustom, RTMPURL: "rtmp://127.0.0.1:1935/live",
		Key: crypto.Secret("clave-de-" + nombre), Enabled: enabled,
	})
	if err != nil {
		t.Fatalf("CreateDestination(%s): %v", nombre, err)
	}
	return d
}

// romperURL mete una URL inválida por debajo de la validación del store.
//
// Se hace a mano porque el store ya rechaza http:// desde la fase 2, y lo que se prueba
// aquí es la defensa de la FÁBRICA: una fila puede haber llegado de una versión anterior
// del binario o de una edición a mano de la base.
func romperURL(t *testing.T, db *store.DB, id int64) {
	t.Helper()
	if _, err := db.SQL().ExecContext(context.Background(),
		`UPDATE destinations SET rtmp_url = 'http://no-es-rtmp/live' WHERE id = ?`, id); err != nil {
		t.Fatalf("forzar la URL mala: %v", err)
	}
}

func TestBuildRejectsAMisconfiguredDestination(t *testing.T) {
	db, c, f := setup(t)
	ctx := context.Background()

	d := crear(t, db, c, "malo", true)
	romperURL(t, db, d.ID)

	dests, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}

	if s, err := f.Build(ctx, dests[0]); err == nil {
		s.Stop()
		t.Error("se construyó un sink para una URL que no es RTMP")
	}
}

func TestBuildMissingDestination(t *testing.T) {
	_, _, f := setup(t)

	if s, err := f.Build(context.Background(), store.Destination{ID: 9999, Name: "fantasma"}); err == nil {
		s.Stop()
		t.Error("se construyó un sink para un destino que no existe")
	}
}

func TestBuildEnabledSkipsDisabledDestinations(t *testing.T) {
	db, c, f := setup(t)

	crear(t, db, c, "uno", true)
	crear(t, db, c, "dos", false)
	crear(t, db, c, "tres", true)

	got, err := f.BuildEnabled(context.Background())
	if err != nil {
		t.Fatalf("BuildEnabled: %v", err)
	}
	for _, s := range got {
		defer s.Stop()
	}
	if len(got) != 2 {
		t.Fatalf("sinks = %d, quería 2 (el apagado no cuenta)", len(got))
	}
}

// TestBuildEnabledSurvivesOneBadDestination: un destino roto no puede impedir que los
// demás salgan al aire. Con la política contraria, una URL mal pegada en un destino
// dejaría al usuario sin ninguna transmisión y sin entender por qué.
func TestBuildEnabledSurvivesOneBadDestination(t *testing.T) {
	db, c, f := setup(t)
	ctx := context.Background()

	crear(t, db, c, "bueno-1", true)
	malo := crear(t, db, c, "malo", true)
	crear(t, db, c, "bueno-2", true)
	romperURL(t, db, malo.ID)

	got, err := f.BuildEnabled(ctx)
	if err != nil {
		t.Fatalf("BuildEnabled devolvió error por un destino roto: %v", err)
	}
	for _, s := range got {
		defer s.Stop()
	}
	if len(got) != 2 {
		t.Fatalf("sinks = %d, quería 2 (los dos buenos)", len(got))
	}
	for _, s := range got {
		if s.ID() == malo.ID {
			t.Error("se construyó el sink del destino roto")
		}
	}
}

// TestBuildDoesNotAudit: construir sinks NO es revelar una clave a una persona (spec
// §15.5). Si alguien "simplifica" la fábrica volviendo a llamar a RevealDestinationKey, el
// log de auditoría se llena de ruido en cada arranque de transmisión y deja de servir para
// lo que existe.
func TestBuildDoesNotAudit(t *testing.T) {
	db, c, f := setup(t)
	ctx := context.Background()

	crear(t, db, c, "uno", true)
	crear(t, db, c, "dos", true)

	got, err := f.BuildEnabled(ctx)
	if err != nil {
		t.Fatalf("BuildEnabled: %v", err)
	}
	for _, s := range got {
		defer s.Stop()
	}

	eventos, err := db.RecentEvents(ctx, 100)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	for _, e := range eventos {
		if e.Kind == "key_revealed" {
			t.Error("construir los sinks generó un evento de auditoría de revelado")
		}
	}
}

// TestBuildEnabledWithNoDestinations: el caso de una instalación recién hecha. No es un
// error, es una lista vacía.
func TestBuildEnabledWithNoDestinations(t *testing.T) {
	_, _, f := setup(t)

	got, err := f.BuildEnabled(context.Background())
	if err != nil {
		t.Fatalf("BuildEnabled: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("sinks = %d, quería 0", len(got))
	}
}

// TestBuildEnabledPreservesSortOrder: el orden de la lista es el que el usuario fijó
// arrastrando en la interfaz, y es el que decide en qué orden se conecta.
func TestBuildEnabledPreservesSortOrder(t *testing.T) {
	db, c, f := setup(t)

	a := crear(t, db, c, "a", true)
	b := crear(t, db, c, "b", true)

	got, err := f.BuildEnabled(context.Background())
	if err != nil {
		t.Fatalf("BuildEnabled: %v", err)
	}
	for _, s := range got {
		defer s.Stop()
	}
	if len(got) != 2 {
		t.Fatalf("sinks = %d", len(got))
	}
	if got[0].ID() != a.ID || got[1].ID() != b.ID {
		t.Errorf("orden = [%d %d], quería [%d %d]", got[0].ID(), got[1].ID(), a.ID, b.ID)
	}
}

// TestBuiltSinkLogsEventsAfterTheCallerContextDies es el test del segundo fallo que
// encontró la prueba con Facebook.
//
// El OnEvent del sink capturaba el contexto de quien llamaba a Build. Desde el arranque de
// sesión eso da igual —es el del proceso—, pero desde un handler HTTP es el de la petición,
// que se cancela al devolver la respuesta. Resultado: un destino añadido en caliente no
// registraba NI UN evento en toda su vida, y el panel del spec §10 no tendría nada que
// enseñar de él. Lo delató un "context canceled" en el log justo al conectar Facebook.
//
// El destino apunta a un servidor RTMP real —el nuestro— para que la conexión PROSPERE y
// emita `destination_connected` enseguida. Con una dirección muerta el sink solo emitiría
// a los cinco intentos fallidos, que con el backoff son unos quince segundos, y además no
// sería el caso que ocurrió: Facebook conectó bien y el evento se perdió igual.
func TestBuiltSinkLogsEventsAfterTheCallerContextDies(t *testing.T) {
	db, c, f := setup(t)

	// Un servidor RTMP que acepta lo que le llegue, para que el sink conecte de verdad.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ing := rtmpio.NewIngest(rtmpio.IngestConfig{
		Addr: ln.Addr().String(), Handler: aceptaTodo{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	go ing.Serve(ln)
	defer ing.Close()
	time.Sleep(200 * time.Millisecond)

	d, err := db.CreateDestination(context.Background(), c, store.NewDestination{
		Name: "local", Platform: store.PlatformCustom,
		RTMPURL: "rtmp://" + ln.Addr().String() + "/live", Key: crypto.Secret("k"), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	// Se construye con un contexto de petición y se cancela justo después, como haría un
	// handler HTTP al responder.
	peticion, cancelar := context.WithCancel(context.Background())
	s, err := f.Build(peticion, *d)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cancelar()
	defer s.Stop()

	s.Start(context.Background(), nil)

	limite := time.Now().Add(10 * time.Second)
	for time.Now().Before(limite) {
		eventos, err := db.RecentEvents(context.Background(), 50)
		if err != nil {
			t.Fatalf("RecentEvents: %v", err)
		}
		for _, e := range eventos {
			if e.DestinationID != nil && *e.DestinationID == d.ID {
				return // registrado: el contexto de la petición no lo tumbó
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("el sink conectó pero no registró el evento: su OnEvent murió con el contexto " +
		"de quien lo construyó")
}

// aceptaTodo es un destino RTMP que acepta cualquier publisher y tira lo que reciba.
type aceptaTodo struct{}

func (aceptaTodo) OnPublishStart(app, key string) error { return nil }
func (aceptaTodo) OnMessage(msg *relay.Message)         {}
func (aceptaTodo) OnPublishEnd()                        {}
