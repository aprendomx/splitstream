package quota_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms/quota"
	"github.com/aprendomx/splitstream/internal/store"
)

// abrir monta una base de prueba y le mete una cuenta real: quota_usage tiene FOREIGN KEY
// contra platform_accounts, así que sumar cuota de una cuenta inexistente falla en la base
// (y Add, que no propaga errores, se quedaría en 0 sin decir por qué).
func abrir(t *testing.T) (*store.DB, int64) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var k [32]byte
	c, err := crypto.NewCipher(k)
	if err != nil {
		t.Fatal(err)
	}
	a, err := db.UpsertAccount(context.Background(), c, store.NewAccount{
		Platform: store.PlatformYouTube, ExternalID: "1", DisplayName: "canal",
		Tokens: store.Tokens{Access: "tok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, a.ID
}

// El día de cuota es el del Pacífico, no el UTC: 2026-09-11T06:30:00Z son las 23:30 del
// 2026-09-10 en Los Ángeles, así que Day() todavía es el día anterior.
func TestDayUsesPacificTimeNotUTC(t *testing.T) {
	db, _ := abrir(t)
	c := quota.NewCounter(db)
	c.Now = func() time.Time { return time.Date(2026, 9, 11, 6, 30, 0, 0, time.UTC) }
	if got := c.Day(); got != "2026-09-10" {
		t.Errorf("Day() = %q, quería 2026-09-10", got)
	}
}

// DayBefore tiene que fijar el cruce de día en la misma zona que Day, no restar días sobre
// el UTC: 2026-09-12T03:00:00Z son las 20:00 del 2026-09-11 en Los Ángeles, así que tanto
// Day() como el punto de partida de DayBefore son ese 2026-09-11.
func TestDayBeforeUsesPacificTimeToo(t *testing.T) {
	db, _ := abrir(t)
	c := quota.NewCounter(db)
	c.Now = func() time.Time { return time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC) }
	if got := c.Day(); got != "2026-09-11" {
		t.Errorf("Day() = %q, quería 2026-09-11", got)
	}
	if got := c.DayBefore(7); got != "2026-09-04" {
		t.Errorf("DayBefore(7) = %q, quería 2026-09-04", got)
	}
}

func TestSinkAndAddAccumulateOnTheSameDay(t *testing.T) {
	db, id := abrir(t)
	c := quota.NewCounter(db)
	ahora := time.Date(2026, 9, 11, 6, 30, 0, 0, time.UTC)
	c.Now = func() time.Time { return ahora }

	sink := c.Sink(id)
	sink(50)
	c.Add(context.Background(), id, 1)

	usado, err := c.UsedToday(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if usado != 51 {
		t.Errorf("UsedToday = %d, quería 51", usado)
	}
}

// Add con 0 (o negativas) no toca la cuota: units <= 0 no es una llamada real a la API.
func TestAddWithZeroUnitsDoesNothing(t *testing.T) {
	db, id := abrir(t)
	c := quota.NewCounter(db)
	c.Now = func() time.Time { return time.Date(2026, 9, 11, 6, 30, 0, 0, time.UTC) }

	c.Add(context.Background(), id, 0)
	usado, err := c.UsedToday(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if usado != 0 {
		t.Errorf("UsedToday = %d, quería 0", usado)
	}
}

// Add nunca puede impedir la llamada que gasta la cuota: con la base cerrada, el fallo se
// loguea y Add no entra en pánico ni devuelve nada que el llamante tenga que mirar.
func TestAddWithClosedDBDoesNotPanic(t *testing.T) {
	db, id := abrir(t)
	c := quota.NewCounter(db)
	c.Now = func() time.Time { return time.Date(2026, 9, 11, 6, 30, 0, 0, time.UTC) }
	db.Close()

	c.Add(context.Background(), id, 5)
}
