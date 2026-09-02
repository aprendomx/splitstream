package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

func newDest(name string) store.NewDestination {
	return store.NewDestination{
		Name:     name,
		Platform: store.PlatformYouTube,
		RTMPURL:  "rtmp://a.rtmp.youtube.com/live2",
		Key:      crypto.Secret("abcd-efgh-ijkl-8765"),
		Enabled:  true,
	}
}

func TestCreateDestinationStoresKeyEncrypted(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	got, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	if got.ID == 0 {
		t.Error("CreateDestination no asignó ID")
	}
	if got.KeyMask != "••••8765" {
		t.Errorf("KeyMask = %q, quería \"••••8765\"", got.KeyMask)
	}

	var blob []byte
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT stream_key_encrypted FROM destinations WHERE id = ?`, got.ID).Scan(&blob); err != nil {
		t.Fatalf("select: %v", err)
	}
	if strings.Contains(string(blob), "abcd-efgh") {
		t.Error("la clave del destino está en claro en la base")
	}
}

func TestRevealDestinationKey(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	created, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	key, err := db.RevealDestinationKey(ctx, c, created.ID)
	if err != nil {
		t.Fatalf("RevealDestinationKey: %v", err)
	}
	if key.Reveal() != "abcd-efgh-ijkl-8765" {
		t.Errorf("clave revelada = %q", key.Reveal())
	}
}

func TestRevealDestinationKeyNotFound(t *testing.T) {
	db, c := bootstrapped(t)
	_, err := db.RevealDestinationKey(context.Background(), c, 999)
	if !errors.Is(err, store.ErrDestinationNotFound) {
		t.Fatalf("err = %v, quería ErrDestinationNotFound", err)
	}
}

func TestCreateDestinationRejectsUnknownPlatform(t *testing.T) {
	db, c := bootstrapped(t)
	in := newDest("Raro")
	in.Platform = "vimeo"
	if _, err := db.CreateDestination(context.Background(), c, in); err == nil {
		t.Fatal("quería error con una plataforma desconocida")
	}
}

func TestCreateDestinationAssignsIncreasingSortOrder(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	first, err := db.CreateDestination(ctx, c, newDest("A"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	second, err := db.CreateDestination(ctx, c, newDest("B"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	if second.SortOrder <= first.SortOrder {
		t.Errorf("sort_order = %d y %d, el segundo debería ser mayor", first.SortOrder, second.SortOrder)
	}
}

func TestListDestinationsOrderedBySortOrder(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	for _, name := range []string{"A", "B", "C"} {
		if _, err := db.CreateDestination(ctx, c, newDest(name)); err != nil {
			t.Fatalf("CreateDestination %s: %v", name, err)
		}
	}

	list, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d, quería 3", len(list))
	}
	for i, want := range []string{"A", "B", "C"} {
		if list[i].Name != want {
			t.Errorf("posición %d = %q, quería %q", i, list[i].Name, want)
		}
		if list[i].KeyMask != "••••8765" {
			t.Errorf("posición %d: KeyMask = %q", i, list[i].KeyMask)
		}
	}
}

func TestReorderDestinations(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	var ids []int64
	for _, name := range []string{"A", "B", "C"} {
		d, err := db.CreateDestination(ctx, c, newDest(name))
		if err != nil {
			t.Fatalf("CreateDestination: %v", err)
		}
		ids = append(ids, d.ID)
	}

	// C, A, B
	if err := db.ReorderDestinations(ctx, []int64{ids[2], ids[0], ids[1]}); err != nil {
		t.Fatalf("ReorderDestinations: %v", err)
	}

	list, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	for i, want := range []string{"C", "A", "B"} {
		if list[i].Name != want {
			t.Errorf("posición %d = %q, quería %q", i, list[i].Name, want)
		}
	}
}

func TestReorderDestinationsRejectsIncompleteList(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	a, err := db.CreateDestination(ctx, c, newDest("A"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	if _, err := db.CreateDestination(ctx, c, newDest("B")); err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	if err := db.ReorderDestinations(ctx, []int64{a.ID}); err == nil {
		t.Fatal("quería error: la lista no incluye todos los destinos")
	}
}

func TestUpdateDestinationPatchesOnlyGivenFields(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	created, err := db.CreateDestination(ctx, c, newDest("Antes"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	name := "Después"
	updated, err := db.UpdateDestination(ctx, c, created.ID, store.DestinationPatch{Name: &name})
	if err != nil {
		t.Fatalf("UpdateDestination: %v", err)
	}
	if updated.Name != "Después" {
		t.Errorf("Name = %q", updated.Name)
	}
	if updated.RTMPURL != created.RTMPURL {
		t.Errorf("RTMPURL cambió sin pedirlo: %q", updated.RTMPURL)
	}

	key, err := db.RevealDestinationKey(ctx, c, created.ID)
	if err != nil {
		t.Fatalf("RevealDestinationKey: %v", err)
	}
	if key.Reveal() != "abcd-efgh-ijkl-8765" {
		t.Error("la clave cambió sin pedirlo")
	}
}

func TestUpdateDestinationReplacesKey(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	created, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	nueva := crypto.Secret("zzzz-yyyy-xxxx-4321")
	updated, err := db.UpdateDestination(ctx, c, created.ID, store.DestinationPatch{Key: &nueva})
	if err != nil {
		t.Fatalf("UpdateDestination: %v", err)
	}
	if updated.KeyMask != "••••4321" {
		t.Errorf("KeyMask = %q, quería \"••••4321\"", updated.KeyMask)
	}

	key, err := db.RevealDestinationKey(ctx, c, created.ID)
	if err != nil {
		t.Fatalf("RevealDestinationKey: %v", err)
	}
	if key.Reveal() != "zzzz-yyyy-xxxx-4321" {
		t.Errorf("clave = %q", key.Reveal())
	}
}

func TestUpdateDestinationNotFound(t *testing.T) {
	db, c := bootstrapped(t)
	name := "x"
	_, err := db.UpdateDestination(context.Background(), c, 999, store.DestinationPatch{Name: &name})
	if !errors.Is(err, store.ErrDestinationNotFound) {
		t.Fatalf("err = %v, quería ErrDestinationNotFound", err)
	}
}

func TestDeleteDestination(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	created, err := db.CreateDestination(ctx, c, newDest("A"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	if err := db.DeleteDestination(ctx, created.ID); err != nil {
		t.Fatalf("DeleteDestination: %v", err)
	}

	list, err := db.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("quedaron %d destinos", len(list))
	}

	if err := db.DeleteDestination(ctx, created.ID); !errors.Is(err, store.ErrDestinationNotFound) {
		t.Fatalf("segundo delete = %v, quería ErrDestinationNotFound", err)
	}
}
