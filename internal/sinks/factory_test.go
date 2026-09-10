package sinks_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/probe"
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

// Test descifra la clave con DestinationKeyForRelay —no es una divulgación, no se
// audita— y sondea. Contra un puerto cerrado el resultado es "unreachable" y no un error:
// el error es para "no pude ni intentarlo".
func TestTestProbesTheDestination(t *testing.T) {
	db, c, f := setup(t)
	ctx := context.Background()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	d, err := db.CreateDestination(ctx, c, store.NewDestination{
		Name: "cerrado", Platform: store.PlatformCustom, RTMPURL: "rtmp://" + addr + "/live",
		Key: crypto.Secret("k"), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := f.Test(ctx, *d)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if res.Outcome != probe.Unreachable {
		t.Errorf("outcome = %v, quería unreachable", res.Outcome)
	}

	eventos, err := db.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range eventos {
		if e.Kind == "key_revealed" {
			t.Error("Test auditó como si hubiera revelado la clave a una persona")
		}
	}
}

func TestTestMissingDestination(t *testing.T) {
	_, _, f := setup(t)
	if _, err := f.Test(context.Background(), store.Destination{ID: 9999}); err == nil {
		t.Error("quería error para un destino que no existe")
	}
}

// encenderGrabacion activa la grabación con un tope en GB dado y devuelve el directorio.
func encenderGrabacion(t *testing.T, db *store.DB, f *sinks.Factory, maxGB float64) string {
	t.Helper()
	dir := t.TempDir()
	f.SetRecordingsDir(dir)
	on := true
	if _, err := db.UpdateRecordingSettings(context.Background(), store.RecordingSettingsPatch{Enabled: &on, MaxGB: &maxGB}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildRecorderIsNilWhenRecordingIsOff(t *testing.T) {
	_, _, f := setup(t)
	f.SetRecordingsDir(t.TempDir())
	s, err := f.BuildRecorder(context.Background(), 1)
	if err != nil || s != nil {
		t.Errorf("con la grabación apagada: sink=%v err=%v, quería nil, nil", s, err)
	}
}

// El sink de grabación es un sink normal con el id reservado. Arrancado contra un hub,
// conecta (abre el archivo), y sus eventos van SIN destination_id con kind recording_*.
func TestBuildRecorderProducesAWorkingSinkWithRecordingEvents(t *testing.T) {
	db, _, f := setup(t)
	ctx := context.Background()
	dir := encenderGrabacion(t, db, f, 1)
	sesion, _ := db.StartSession(ctx)

	s, err := f.BuildRecorder(ctx, sesion)
	if err != nil || s == nil {
		t.Fatalf("BuildRecorder: sink=%v err=%v", s, err)
	}
	if s.ID() != relay.RecorderSinkID {
		t.Errorf("id = %d, quería %d", s.ID(), relay.RecorderSinkID)
	}

	hub := relay.NewHub(nil)
	s.Start(ctx, hub.Preamble())
	esperar(t, 5*time.Second, func() bool { return s.State() == relay.StateLive }, "el recorder conectó")
	s.Stop()

	entradas, err := os.ReadDir(filepath.Join(dir, "sesion-"+strconv.FormatInt(sesion, 10)))
	if err != nil || len(entradas) != 1 {
		t.Fatalf("archivos de la sesión = %v, %v; quería 1", entradas, err)
	}
	grabaciones, _ := db.ListRecordings(ctx, sesion, 10, 0)
	if len(grabaciones) != 1 || grabaciones[0].EndedAt == nil || filepath.IsAbs(grabaciones[0].Path) {
		t.Errorf("fila de la grabación = %+v; quería cerrada y con path relativo", grabaciones)
	}

	eventos, _ := db.RecentEvents(ctx, 20)
	var visto bool
	for _, e := range eventos {
		if e.Kind == "recording_started" {
			visto = true
			if e.DestinationID != nil {
				t.Errorf("recording_started lleva destination_id=%d", *e.DestinationID)
			}
		}
		if strings.HasPrefix(e.Kind, "destination_") {
			t.Errorf("un evento del recorder salió como %s", e.Kind)
		}
	}
	if !visto {
		t.Errorf("no hubo recording_started; eventos: %+v", eventos)
	}
}

// Sin sitio, la grabación no arranca y lo dice; la sesión ni se entera.
func TestBuildRecorderSkipsWhenTheQuotaIsExhausted(t *testing.T) {
	db, _, f := setup(t)
	ctx := context.Background()
	encenderGrabacion(t, db, f, 0.000001) // ~1 KB
	// Un segmento EN CURSO ocupa más que la cuota: la poda no lo puede tocar.
	if _, err := db.OpenRecording(ctx, 0, "otra/abierta.flv", 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().ExecContext(ctx, `UPDATE recordings SET bytes = 100000`); err != nil {
		t.Fatal(err)
	}

	s, err := f.BuildRecorder(ctx, 7)
	if err != nil || s != nil {
		t.Fatalf("sink=%v err=%v, quería nil, nil", s, err)
	}
	eventos, _ := db.RecentEvents(ctx, 5)
	if len(eventos) == 0 || eventos[0].Kind != "recording_skipped_quota" || eventos[0].Level != store.LevelWarn {
		t.Errorf("evento = %+v, quería recording_skipped_quota warn", eventos)
	}
}

// Con la cuota superada por grabaciones VIEJAS, primero se poda y luego se arranca.
func TestBuildRecorderPrunesBeforeGivingUp(t *testing.T) {
	db, _, f := setup(t)
	ctx := context.Background()
	dir := encenderGrabacion(t, db, f, 0.000001)
	if err := os.WriteFile(filepath.Join(dir, "vieja.flv"), make([]byte, 100000), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.OpenRecording(ctx, 0, "vieja.flv", 1, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishRecording(ctx, "vieja.flv", time.Now().Add(-time.Hour), 100000, 1); err != nil {
		t.Fatal(err)
	}

	s, err := f.BuildRecorder(ctx, 8)
	if err != nil || s == nil {
		t.Fatalf("sink=%v err=%v, quería un sink tras podar", s, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vieja.flv")); !os.IsNotExist(err) {
		t.Error("la grabación vieja sigue en disco")
	}
}

func TestPruneRecordingsRemovesFilesAndRows(t *testing.T) {
	db, _, f := setup(t)
	ctx := context.Background()
	dir := encenderGrabacion(t, db, f, 20)
	ruta := filepath.Join(dir, "s", "a.flv")
	if err := os.MkdirAll(filepath.Dir(ruta), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ruta, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	vieja := time.Now().Add(-100 * 24 * time.Hour)
	if _, err := db.OpenRecording(ctx, 0, "s/a.flv", 1, vieja); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishRecording(ctx, "s/a.flv", vieja, 1, 1); err != nil {
		t.Fatal(err)
	}

	n, _, err := f.PruneRecordings(ctx)
	if err != nil || n != 1 {
		t.Fatalf("PruneRecordings: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(ruta); !os.IsNotExist(err) {
		t.Error("el archivo sigue")
	}
	if restantes, _ := db.ListRecordings(ctx, 0, 10, 0); len(restantes) != 0 {
		t.Error("la fila sigue")
	}
}

func esperar(t *testing.T, plazo time.Duration, cond func() bool, msg string) {
	t.Helper()
	fin := time.Now().Add(plazo)
	for time.Now().Before(fin) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tiempo agotado: %s", msg)
}

// Un kill -9 deja filas «en curso» que nadie cerrará jamás. Al arrancar, la fábrica las
// cierra con el tamaño y la fecha del archivo, borra las que ya no tienen archivo y lo
// cuenta en un evento.
func TestReconcileRecordingsClosesRowsFromACrashedSession(t *testing.T) {
	db, _, f := setup(t)
	ctx := context.Background()
	dir := encenderGrabacion(t, db, f, 20)

	contenido := make([]byte, 4096)
	abs := filepath.Join(dir, "sesion-1", "20260910-120000-01.flv")
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, contenido, 0o600); err != nil {
		t.Fatal(err)
	}
	conArchivo, err := db.OpenRecording(ctx, 0, "sesion-1/20260910-120000-01.flv", 1, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	sinArchivo, err := db.OpenRecording(ctx, 0, "sesion-1/20260910-120000-02.flv", 2, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	closed, removed, err := f.ReconcileRecordings(ctx)
	if err != nil || closed != 1 || removed != 1 {
		t.Fatalf("ReconcileRecordings: closed=%d removed=%d err=%v; quería 1, 1, nil", closed, removed, err)
	}

	r, err := db.RecordingByID(ctx, conArchivo)
	if err != nil {
		t.Fatalf("la fila con archivo desapareció: %v", err)
	}
	if r.EndedAt == nil || r.Bytes != int64(len(contenido)) {
		t.Errorf("fila cerrada = %+v; quería ended_at y bytes = %d", r, len(contenido))
	}
	if _, err := db.RecordingByID(ctx, sinArchivo); err == nil {
		t.Error("la fila sin archivo sigue")
	}

	eventos, _ := db.RecentEvents(ctx, 10)
	var visto bool
	for _, e := range eventos {
		if e.Kind == "recording_reconciled" {
			visto = true
			if e.Level != store.LevelWarn || e.DestinationID != nil {
				t.Errorf("evento = %+v", e)
			}
		}
	}
	if !visto {
		t.Errorf("no hubo recording_reconciled; eventos: %+v", eventos)
	}

	// Segunda pasada: ya no queda nada en curso, así que no hay evento nuevo.
	closed, removed, err = f.ReconcileRecordings(ctx)
	if err != nil || closed != 0 || removed != 0 {
		t.Errorf("segunda pasada: closed=%d removed=%d err=%v", closed, removed, err)
	}
}

// La numeración de segmentos continúa donde la dejó la sesión: apagar y encender la
// grabación en caliente construye otro writer, y volver a empezar en 1 daba dos «segmento
// 1» y podía chocar con el archivo anterior dentro del mismo segundo (O_EXCL).
func TestBuildRecorderContinuesSegmentNumbering(t *testing.T) {
	db, _, f := setup(t)
	ctx := context.Background()
	encenderGrabacion(t, db, f, 20)
	sesion, _ := db.StartSession(ctx)

	for i := 1; i <= 2; i++ {
		path := "sesion-" + strconv.FormatInt(sesion, 10) + "/anterior-0" + strconv.Itoa(i) + ".flv"
		if _, err := db.OpenRecording(ctx, sesion, path, i, time.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := db.FinishRecording(ctx, path, time.Now().Add(-time.Hour), 10, 10); err != nil {
			t.Fatal(err)
		}
	}

	s, err := f.BuildRecorder(ctx, sesion)
	if err != nil || s == nil {
		t.Fatalf("BuildRecorder: sink=%v err=%v", s, err)
	}
	hub := relay.NewHub(nil)
	s.Start(ctx, hub.Preamble())
	esperar(t, 5*time.Second, func() bool { return s.State() == relay.StateLive }, "el recorder conectó")
	s.Stop()

	grabaciones, err := db.ListRecordings(ctx, sesion, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(grabaciones) != 3 {
		t.Fatalf("grabaciones = %d, quería 3 (las dos previas y la nueva)", len(grabaciones))
	}
	nueva := grabaciones[0] // ListRecordings va de la más reciente a la más antigua
	if nueva.Segment != 3 {
		t.Errorf("segmento de la nueva = %d, quería 3", nueva.Segment)
	}
	if !strings.HasSuffix(nueva.Path, "-03.flv") {
		t.Errorf("archivo = %q, quería que terminara en -03.flv", nueva.Path)
	}
}
