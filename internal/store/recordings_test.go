package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

func abrirYcerrar(t *testing.T, db *store.DB, sesion int64, path string, seg int, bytes int64, dur int) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := db.OpenRecording(ctx, sesion, path, seg, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("OpenRecording(%s): %v", path, err)
	}
	if err := db.FinishRecording(ctx, path, time.Now(), bytes, dur); err != nil {
		t.Fatalf("FinishRecording(%s): %v", path, err)
	}
	return id
}

func TestRecordingLifecycle(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	sesion, _ := db.StartSession(ctx)

	id, err := db.OpenRecording(ctx, sesion, "sesion-1/a-01.flv", 1, time.Now())
	if err != nil {
		t.Fatalf("OpenRecording: %v", err)
	}
	r, err := db.RecordingByID(ctx, id)
	if err != nil || r.EndedAt != nil || r.Segment != 1 || r.SessionID == nil || *r.SessionID != sesion {
		t.Fatalf("recién abierta = %+v, %v", r, err)
	}
	// En curso: no se borra.
	if err := db.DeleteRecording(ctx, id); !errors.Is(err, store.ErrRecordingInProgress) && !errors.Is(err, store.ErrConflict) {
		t.Errorf("borrar en curso = %v, quería ErrRecordingInProgress", err)
	}

	if err := db.FinishRecording(ctx, "sesion-1/a-01.flv", time.Now(), 1234, 5000); err != nil {
		t.Fatalf("FinishRecording: %v", err)
	}
	r, _ = db.RecordingByID(ctx, id)
	if r.EndedAt == nil || r.Bytes != 1234 || r.DurationMS != 5000 {
		t.Errorf("cerrada = %+v", r)
	}
	if n, _ := db.CountSessionRecordings(ctx, sesion); n != 1 {
		t.Errorf("CountSessionRecordings = %d", n)
	}
	if total, _ := db.RecordingsTotalBytes(ctx); total != 1234 {
		t.Errorf("RecordingsTotalBytes = %d", total)
	}
	if err := db.DeleteRecording(ctx, id); err != nil {
		t.Fatalf("DeleteRecording: %v", err)
	}
	if _, err := db.RecordingByID(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("tras borrar = %v, quería ErrNotFound", err)
	}
	if err := db.FinishRecording(ctx, "no-existe.flv", time.Now(), 1, 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FinishRecording de un path desconocido = %v", err)
	}
}

func TestListRecordingsFiltersBySessionAndPaginatesByID(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	s1, _ := db.StartSession(ctx)
	s2, _ := db.StartSession(ctx)
	for i := 1; i <= 3; i++ {
		abrirYcerrar(t, db, s1, "s1-"+string(rune('0'+i))+".flv", i, 10, 100)
	}
	abrirYcerrar(t, db, s2, "s2-1.flv", 1, 10, 100)

	todas, err := db.ListRecordings(ctx, 0, 10, 0)
	if err != nil || len(todas) != 4 || todas[0].Path != "s2-1.flv" {
		t.Fatalf("todas = %v, %v", todas, err)
	}
	deS1, _ := db.ListRecordings(ctx, s1, 10, 0)
	if len(deS1) != 3 || deS1[0].Segment != 3 {
		t.Errorf("de s1 = %v", deS1)
	}
	pag, _ := db.ListRecordings(ctx, s1, 2, deS1[0].ID)
	if len(pag) != 2 || pag[0].Segment != 2 {
		t.Errorf("página = %v", pag)
	}
}

// Poda: primero por fecha, después por gigas hasta bajar del tope, la más antigua primero.
// El archivo se borra por el callback antes que la fila; un archivo que ya no existe cuenta
// como borrado.
func TestPruneRecordingsByAgeThenBySize(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	sesion, _ := db.StartSession(ctx)
	vieja := time.Now().Add(-40 * 24 * time.Hour)
	for i, r := range []struct {
		path  string
		ended time.Time
		bytes int64
	}{
		{"vieja.flv", vieja, 100},
		{"a.flv", time.Now().Add(-3 * time.Hour), 400},
		{"b.flv", time.Now().Add(-2 * time.Hour), 400},
		{"c.flv", time.Now().Add(-1 * time.Hour), 400},
	} {
		if _, err := db.OpenRecording(ctx, sesion, r.path, i+1, r.ended.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := db.FinishRecording(ctx, r.path, r.ended, r.bytes, 1000); err != nil {
			t.Fatal(err)
		}
	}

	var borrados []string
	remove := func(p string) error {
		borrados = append(borrados, p)
		if p == "a.flv" {
			return os.ErrNotExist // ya no estaba: se da por borrado
		}
		return nil
	}
	// 30 días de retención y tope de 900 bytes: se va "vieja" por fecha (quedan 1200),
	// y luego "a" por tamaño (quedan 800 < 900).
	n, freed, err := db.PruneRecordings(ctx, time.Now().Add(-30*24*time.Hour), 900, remove)
	if err != nil {
		t.Fatalf("PruneRecordings: %v", err)
	}
	if n != 2 || freed != 500 {
		t.Errorf("borradas = %d, liberados = %d; quería 2 y 500", n, freed)
	}
	if len(borrados) != 2 || borrados[0] != "vieja.flv" || borrados[1] != "a.flv" {
		t.Errorf("orden de borrado = %v", borrados)
	}
	restantes, _ := db.ListRecordings(ctx, 0, 10, 0)
	if len(restantes) != 2 {
		t.Errorf("quedan %d, quería 2", len(restantes))
	}
}

// Un fallo real al borrar el archivo deja la fila: mejor una fila huérfana visible que
// un archivo huérfano invisible.
func TestPruneRecordingsKeepsTheRowWhenTheFileCannotBeRemoved(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	abrirYcerrar(t, db, 0, "x.flv", 1, 100, 1)
	_, _, err := db.PruneRecordings(ctx, time.Time{}, 50, func(string) error { return errors.New("permiso denegado") })
	if err == nil {
		t.Error("quería el error del borrado")
	}
	if restantes, _ := db.ListRecordings(ctx, 0, 10, 0); len(restantes) != 1 {
		t.Errorf("la fila desapareció aunque el archivo sigue")
	}
}

func TestPruneRecordingsNeverTouchesOpenSegments(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if _, err := db.OpenRecording(ctx, 0, "abierta.flv", 1, time.Now().Add(-100*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	n, _, err := db.PruneRecordings(ctx, time.Now(), 1, func(string) error { return nil })
	if err != nil || n != 0 {
		t.Errorf("podó un segmento en curso: n=%d err=%v", n, err)
	}
}

// PruneSessions (v0.8) no puede llevarse una sesión que todavía tiene grabaciones: la
// vista de historial las cuelga de la sesión.
func TestPruneSessionsKeepsSessionsWithRecordings(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	vieja := time.Now().Add(-200 * 24 * time.Hour).UTC().Format(anchoFijo)
	if _, err := db.SQL().ExecContext(ctx, `INSERT INTO sessions (id, started_at, ended_at) VALUES (5, ?, ?)`, vieja, vieja); err != nil {
		t.Fatal(err)
	}
	abrirYcerrar(t, db, 5, "s5.flv", 1, 10, 10)
	if n, err := db.PruneSessions(ctx, time.Now()); err != nil || n != 0 {
		t.Errorf("borró la sesión con grabaciones: n=%d err=%v", n, err)
	}
}

// Un kill -9 no llama a OnSegment: la fila se queda «en curso» para siempre, y así no se
// puede descargar ni borrar (409), la poda la salta y la cuota la cuenta como 0 bytes.
// Al arrancar se cierra con lo que diga el archivo, o se borra si el archivo no está.
func TestCloseDanglingRecordingsUsesTheFileOrDropsTheRow(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	sesion, _ := db.StartSession(ctx)

	cerrada := abrirYcerrar(t, db, sesion, "sesion-1/cerrada.flv", 1, 123, 4567)
	conArchivo, err := db.OpenRecording(ctx, sesion, "sesion-1/con-archivo.flv", 2, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	sinArchivo, err := db.OpenRecording(ctx, sesion, "sesion-1/sin-archivo.flv", 3, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	// El archivo manda: tamaño y fecha de modificación salen de él.
	fin := time.Now().Add(-30 * time.Minute).UTC().Truncate(time.Second)
	stat := func(rel string) (int64, time.Time, bool) {
		if rel == "sesion-1/con-archivo.flv" {
			return 777, fin, true
		}
		return 0, time.Time{}, false
	}

	closed, removed, err := db.CloseDanglingRecordings(ctx, stat)
	if err != nil {
		t.Fatalf("CloseDanglingRecordings: %v", err)
	}
	if closed != 1 || removed != 1 {
		t.Fatalf("closed=%d removed=%d, quería 1 y 1", closed, removed)
	}

	r, err := db.RecordingByID(ctx, conArchivo)
	if err != nil {
		t.Fatalf("la fila con archivo desapareció: %v", err)
	}
	if r.EndedAt == nil || !r.EndedAt.Equal(fin) || r.Bytes != 777 {
		t.Errorf("fila cerrada = %+v; quería ended_at %v y bytes 777", r, fin)
	}
	if r.DurationMS != 0 {
		t.Errorf("duration_ms = %d, quería que se dejara como estaba", r.DurationMS)
	}

	if _, err := db.RecordingByID(ctx, sinArchivo); !errors.Is(err, store.ErrRecordingNotFound) {
		t.Errorf("la fila sin archivo sigue: %v", err)
	}

	// Las ya cerradas no se tocan.
	antes, err := db.RecordingByID(ctx, cerrada)
	if err != nil || antes.Bytes != 123 || antes.DurationMS != 4567 {
		t.Errorf("una fila cerrada cambió: %+v, %v", antes, err)
	}
}

// Sin filas en curso no hay nada que hacer y no es un error.
func TestCloseDanglingRecordingsWithNothingOpen(t *testing.T) {
	db := openTemp(t)
	abrirYcerrar(t, db, 0, "a.flv", 1, 10, 10)
	closed, removed, err := db.CloseDanglingRecordings(context.Background(),
		func(string) (int64, time.Time, bool) {
			t.Error("no había filas en curso: stat no debería llamarse")
			return 0, time.Time{}, false
		})
	if closed != 0 || removed != 0 || err != nil {
		t.Errorf("closed=%d removed=%d err=%v", closed, removed, err)
	}
}
