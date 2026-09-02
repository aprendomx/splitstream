package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

// La fase 3 pone una goroutine por destino escribiendo eventos contra una base con
// SetMaxOpenConns(1). Hasta ahora ningún test lanzaba goroutines contra el store, así que
// `-race` no verificaba nada ahí.
func TestStoreHandlesConcurrentWriters(t *testing.T) {
	db, c := bootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	sessionID, err := db.StartSession(ctx)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	const writers, perWriter = 8, 40

	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := db.LogEvent(ctx, store.Event{
					SessionID:     &sessionID,
					DestinationID: &dest.ID,
					Level:         store.LevelInfo,
					Kind:          "prueba_concurrencia",
					Message:       "evento",
				}); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}

	// Lectores en paralelo con los escritores.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := db.RecentEvents(ctx, 10); err != nil {
					errs <- err
					return
				}
				if _, err := db.ListDestinations(ctx); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(25 * time.Second):
		t.Fatal("los escritores concurrentes no terminaron: contención o autobloqueo")
	}
	close(errs)
	for err := range errs {
		t.Fatalf("error de un escritor concurrente: %v", err)
	}

	events, err := db.RecentEvents(ctx, 1000)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	var n int
	for _, e := range events {
		if e.Kind == "prueba_concurrencia" {
			n++
		}
	}
	if n != writers*perWriter {
		t.Errorf("se persistieron %d eventos, quería %d: se perdieron escrituras",
			n, writers*perWriter)
	}
}

// Transacciones concurrentes contra la única conexión: deben serializarse, no bloquearse.
func TestStoreConcurrentTransactions(t *testing.T) {
	db, c := bootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				err := db.InTx(ctx, func(tx *store.DB) error {
					if _, err := tx.LogEvent(ctx, store.Event{
						DestinationID: &dest.ID,
						Level:         store.LevelWarn,
						Kind:          "tx_concurrente",
						Message:       "dentro de transacción",
					}); err != nil {
						return err
					}
					_, err := tx.ListDestinations(ctx)
					return err
				})
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		t.Fatal("las transacciones concurrentes se bloquearon")
	}
	close(errs)
	for err := range errs {
		t.Fatalf("error en una transacción concurrente: %v", err)
	}
}
