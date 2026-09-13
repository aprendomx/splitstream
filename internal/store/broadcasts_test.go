package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestBroadcastIsUpsertedPerDestinationAndMarksTheKey(t *testing.T) {
	db := openTemp(t)
	c := cifradorDePrueba(t)
	ctx := context.Background()
	a := cuentaDePrueba(t, db, c, "42")
	d, _ := db.CreateDestination(ctx, c, store.NewDestination{Name: "yt", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})

	if _, err := db.BroadcastFor(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sin emisión: err = %v", err)
	}
	if err := db.SetBroadcast(ctx, store.Broadcast{DestinationID: d.ID, AccountID: a.ID, Platform: store.PlatformTwitch,
		BroadcastRef: "b1", StreamRef: "s1", LiveChatID: "chat1", KeyFromAPI: true}); err != nil {
		t.Fatal(err)
	}
	b, err := db.BroadcastFor(ctx, d.ID)
	if err != nil || b.BroadcastRef != "b1" || b.Status != store.BroadcastCreated || !b.KeyFromAPI || b.LiveChatID != "chat1" {
		t.Fatalf("broadcast = %+v, %v", b, err)
	}
	dd, _ := db.DestinationByID(ctx, d.ID)
	if !dd.KeyFromAPI {
		t.Error("Destination.KeyFromAPI debería ser true")
	}
	if err := db.SetBroadcastStatus(ctx, d.ID, store.BroadcastLive); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBroadcastStatus(ctx, d.ID, "raro"); !errors.Is(err, store.ErrInvalidInput) {
		t.Errorf("status inválido: %v", err)
	}
	// Upsert: una emisión nueva sustituye la anterior y vuelve a created.
	db.SetBroadcast(ctx, store.Broadcast{DestinationID: d.ID, AccountID: a.ID, Platform: store.PlatformTwitch, BroadcastRef: "b2", KeyFromAPI: true})
	b, _ = db.BroadcastFor(ctx, d.ID)
	if b.BroadcastRef != "b2" || b.Status != store.BroadcastCreated {
		t.Errorf("tras upsert = %+v", b)
	}
	if err := db.ClearBroadcast(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	dd, _ = db.DestinationByID(ctx, d.ID)
	if dd.KeyFromAPI {
		t.Error("tras Clear, KeyFromAPI debería ser false")
	}
	// Cae con el destino y con la cuenta.
	db.SetBroadcast(ctx, store.Broadcast{DestinationID: d.ID, AccountID: a.ID, Platform: store.PlatformTwitch, KeyFromAPI: true})
	db.DeleteAccount(ctx, a.ID)
	if _, err := db.BroadcastFor(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("tras borrar la cuenta: %v", err)
	}
}

func TestBroadcastsByDestinationReturnsAllRows(t *testing.T) {
	db := openTemp(t)
	c := cifradorDePrueba(t)
	ctx := context.Background()
	a := cuentaDePrueba(t, db, c, "42")
	d1, _ := db.CreateDestination(ctx, c, store.NewDestination{Name: "d1", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})
	d2, _ := db.CreateDestination(ctx, c, store.NewDestination{Name: "d2", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})

	if got, err := db.BroadcastsByDestination(ctx); err != nil || len(got) != 0 {
		t.Fatalf("sin emisiones: %v, %v", got, err)
	}
	if err := db.SetBroadcast(ctx, store.Broadcast{DestinationID: d1.ID, AccountID: a.ID, Platform: store.PlatformTwitch, BroadcastRef: "b1"}); err != nil {
		t.Fatal(err)
	}
	got, err := db.BroadcastsByDestination(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("emisiones = %d, quería 1", len(got))
	}
	if b, ok := got[d1.ID]; !ok || b.BroadcastRef != "b1" {
		t.Errorf("got[d1.ID] = %+v, ok = %v", b, ok)
	}
	if _, ok := got[d2.ID]; ok {
		t.Errorf("d2 no debería tener emisión")
	}
}
