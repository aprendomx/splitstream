package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

// Sin InTx esto se autobloquea: la transacción retiene la única conexión.
func TestInTxAllowsNestedRepositoryCalls(t *testing.T) {
	db, c := bootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	err = db.InTx(ctx, func(tx *store.DB) error {
		if _, err := tx.LogEvent(ctx, store.Event{
			DestinationID: &dest.ID,
			Level:         store.LevelError,
			Kind:          "connect_failed",
			Message:       "connection refused",
		}); err != nil {
			return err
		}
		return tx.DeleteDestination(ctx, dest.ID)
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}

	events, err := db.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, quería 1", len(events))
	}
	list, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("el destino no se borró dentro de la transacción")
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	sentinel := errors.New("fallo del negocio")
	err = db.InTx(ctx, func(tx *store.DB) error {
		if err := tx.DeleteDestination(ctx, dest.ID); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx = %v, quería el error del callback", err)
	}

	list, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("el rollback no restauró el destino: %d destinos", len(list))
	}
}

// InTx anidado debe rechazarse en vez de autobloquearse.
func TestInTxRejectsNesting(t *testing.T) {
	db, _ := bootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := db.InTx(ctx, func(tx *store.DB) error {
		return tx.InTx(ctx, func(*store.DB) error { return nil })
	})
	if !errors.Is(err, store.ErrNestedTransaction) {
		t.Fatalf("InTx anidado = %v, quería ErrNestedTransaction", err)
	}
}
