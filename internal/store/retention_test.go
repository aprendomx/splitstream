package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

const anchoFijo = "2006-01-02T15:04:05.000000000Z07:00"

// insertarEventoCon mete un evento con un created_at concreto, saltándose LogEvent (que
// siempre pone "ahora").
func insertarEventoCon(t *testing.T, db *store.DB, cuando time.Time, kind string) {
	t.Helper()
	if _, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO events (level, kind, message, created_at) VALUES ('info', ?, 'x', ?)`,
		kind, cuando.UTC().Format(anchoFijo)); err != nil {
		t.Fatal(err)
	}
}

func contarEventos(t *testing.T, db *store.DB) int {
	t.Helper()
	var n int
	if err := db.SQL().QueryRowContext(context.Background(), `SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPruneEventsByAge(t *testing.T) {
	db := openTemp(t)
	ahora := time.Now()
	insertarEventoCon(t, db, ahora.Add(-100*24*time.Hour), "viejo")
	insertarEventoCon(t, db, ahora.Add(-1*time.Hour), "reciente")

	n, err := db.PruneEvents(context.Background(), ahora.Add(-90*24*time.Hour), 0)
	if err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	if n != 1 || contarEventos(t, db) != 1 {
		t.Errorf("borrados = %d, quedan %d; quería 1 y 1", n, contarEventos(t, db))
	}
}

// El tope de filas manda aunque los eventos sean recientes: es lo que protege el disco de
// un destino que aletea toda la noche.
func TestPruneEventsByCount(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelInfo, Kind: "k", Message: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.PruneEvents(ctx, time.Time{}, 4)
	if err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	if n != 6 || contarEventos(t, db) != 4 {
		t.Errorf("borrados = %d, quedan %d; quería 6 y 4", n, contarEventos(t, db))
	}
	// Se conservan los MÁS RECIENTES.
	ev, _ := db.RecentEvents(ctx, 10)
	if ev[len(ev)-1].ID != 7 {
		t.Errorf("el más antiguo que queda es %d, quería 7", ev[len(ev)-1].ID)
	}
}

// Ambos límites a la vez: muerde el que borre más.
func TestPruneEventsAppliesBothLimits(t *testing.T) {
	db := openTemp(t)
	ahora := time.Now()
	for i := 0; i < 5; i++ {
		insertarEventoCon(t, db, ahora.Add(-200*24*time.Hour), "viejo")
	}
	for i := 0; i < 5; i++ {
		insertarEventoCon(t, db, ahora, "nuevo")
	}
	if _, err := db.PruneEvents(context.Background(), ahora.Add(-90*24*time.Hour), 3); err != nil {
		t.Fatal(err)
	}
	if contarEventos(t, db) != 3 {
		t.Errorf("quedan %d, quería 3", contarEventos(t, db))
	}
}

func TestPruneEventsWithZeroLimitsDoesNothing(t *testing.T) {
	db := openTemp(t)
	insertarEventoCon(t, db, time.Now().Add(-1000*24*time.Hour), "viejo")
	n, err := db.PruneEvents(context.Background(), time.Time{}, 0)
	if err != nil || n != 0 || contarEventos(t, db) != 1 {
		t.Errorf("n=%d err=%v quedan=%d", n, err, contarEventos(t, db))
	}
}

// Solo se podan sesiones CERRADAS y sin eventos que sigan apuntándolas: la de ahora mismo
// y las que todavía tienen historia se quedan.
func TestPruneSessionsKeepsOpenAndReferencedOnes(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	vieja := time.Now().Add(-200 * 24 * time.Hour).UTC().Format(anchoFijo)

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.SQL().ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// 1: cerrada y sin eventos → se va. 2: cerrada con evento → se queda. 3: abierta → se queda.
	exec(`INSERT INTO sessions (id, started_at, ended_at) VALUES (1, ?, ?)`, vieja, vieja)
	exec(`INSERT INTO sessions (id, started_at, ended_at) VALUES (2, ?, ?)`, vieja, vieja)
	exec(`INSERT INTO events (session_id, level, kind, message, created_at) VALUES (2, 'info', 'k', 'x', ?)`, vieja)
	exec(`INSERT INTO sessions (id, started_at) VALUES (3, ?)`, vieja)

	n, err := db.PruneSessions(ctx, time.Now().Add(-90*24*time.Hour))
	if err != nil {
		t.Fatalf("PruneSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("borradas = %d, quería 1", n)
	}
	for _, id := range []int64{2, 3} {
		if _, err := db.SessionByID(ctx, id); err != nil {
			t.Errorf("la sesión %d desapareció: %v", id, err)
		}
	}
}
