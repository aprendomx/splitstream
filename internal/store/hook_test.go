package store_test

import (
	"context"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

// El hook recibe el evento tal como quedó en la base: con id y fecha. Es lo que permite
// que alertas y webhooks no tengan que releer nada.
func TestLogEventCallsTheHookWithTheStoredEvent(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	var got []store.Event
	db.SetEventHook(func(e store.Event) { got = append(got, e) })

	id, err := db.LogEvent(ctx, store.Event{Level: store.LevelWarn, Kind: "prueba", Message: "hola"})
	if err != nil {
		t.Fatalf("LogEvent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("el hook se llamó %d veces, quería 1", len(got))
	}
	if got[0].ID != id || got[0].Kind != "prueba" || got[0].Level != store.LevelWarn {
		t.Errorf("el hook recibió %+v, quería id=%d kind=prueba level=warn", got[0], id)
	}
	if got[0].CreatedAt.IsZero() {
		t.Error("el hook recibió un evento sin CreatedAt")
	}
}

// El camino de RevealDestinationKey escribe su evento dentro de InTx: el hook tiene que
// llegar también por ahí, o la auditoría de las claves no avisaría a nadie.
func TestHookIsCalledInsideATransaction(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	llamadas := 0
	db.SetEventHook(func(store.Event) { llamadas++ })

	err := db.InTx(ctx, func(tx *store.DB) error {
		_, err := tx.LogEvent(ctx, store.Event{Level: store.LevelInfo, Kind: "en_tx", Message: "x"})
		return err
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if llamadas != 1 {
		t.Errorf("el hook se llamó %d veces dentro de la transacción, quería 1", llamadas)
	}
}

// Un evento que no se escribió no se anuncia.
func TestHookIsNotCalledWhenTheInsertFails(t *testing.T) {
	db := openTemp(t)

	llamadas := 0
	db.SetEventHook(func(store.Event) { llamadas++ })

	if _, err := db.LogEvent(context.Background(), store.Event{Level: "fatal", Kind: "x"}); err == nil {
		t.Fatal("quería error por nivel desconocido")
	}
	if llamadas != 0 {
		t.Errorf("el hook se llamó %d veces tras un INSERT fallido", llamadas)
	}
}
