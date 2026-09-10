package store_test

import (
	"context"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestListSessionsNewestFirstWithEventCounts(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := db.StartSession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := db.FinishSession(ctx, ids[0], 1280, 720, 3000); err != nil {
		t.Fatal(err)
	}
	for _, lvl := range []store.Level{store.LevelInfo, store.LevelWarn, store.LevelWarn, store.LevelError} {
		if _, err := db.LogEvent(ctx, store.Event{SessionID: &ids[2], Level: lvl, Kind: "k", Message: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.ListSessions(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 3 || got[0].ID != ids[2] || got[2].ID != ids[0] {
		t.Fatalf("orden = %v", got)
	}
	if got[0].Info != 1 || got[0].Warn != 2 || got[0].Error != 1 {
		t.Errorf("contadores = %d/%d/%d", got[0].Info, got[0].Warn, got[0].Error)
	}
	if got[2].EndedAt == nil || got[2].Width == nil || *got[2].Width != 1280 {
		t.Errorf("la sesión cerrada no trae sus datos: %+v", got[2])
	}
	if got[0].EndedAt != nil {
		t.Error("la sesión abierta trae ended_at")
	}
}

// La paginación es por id, no por texto de fecha (spec base §15.4).
func TestListSessionsPaginatesByID(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := db.StartSession(ctx); err != nil {
			t.Fatal(err)
		}
	}
	pag1, _ := db.ListSessions(ctx, 2, 0)
	if len(pag1) != 2 || pag1[0].ID != 5 {
		t.Fatalf("página 1 = %v", pag1)
	}
	pag2, _ := db.ListSessions(ctx, 2, pag1[1].ID)
	if len(pag2) != 2 || pag2[0].ID != 3 {
		t.Fatalf("página 2 = %v", pag2)
	}
}
