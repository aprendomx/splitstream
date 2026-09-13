package store_test

import (
	"context"
	"testing"
	"time"

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

func TestListSessionsSaysWhetherARecordingExists(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	s1, err := db.StartSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := db.StartSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.OpenRecording(ctx, s1, "a.flv", 0, time.Now()); err != nil {
		t.Fatal(err)
	}

	ss, err := db.ListSessions(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("sesiones = %v", ss)
	}
	// s2 es la más reciente: sin grabación; s1 con grabación.
	if ss[0].ID != s2 || ss[0].HasRecording {
		t.Errorf("s2 = %+v", ss[0])
	}
	if ss[1].ID != s1 || !ss[1].HasRecording {
		t.Errorf("s1 = %+v", ss[1])
	}
}

func TestEventsBySessionIsAscendingScopedAndCapped(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	s1, err := db.StartSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := db.StartSession(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := db.LogEvent(ctx, store.Event{SessionID: &s1, Level: store.LevelInfo, Kind: "k", Message: "x"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if _, err := db.LogEvent(ctx, store.Event{SessionID: &s2, Level: store.LevelInfo, Kind: "k", Message: "y"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelInfo, Kind: "k", Message: "z"}); err != nil {
		t.Fatal(err)
	}

	got, err := db.EventsBySession(ctx, s1, 0)
	if err != nil {
		t.Fatalf("EventsBySession: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("eventos = %v", got)
	}
	for i, e := range got {
		if e.ID != ids[i] {
			t.Errorf("orden = %v, quería %v", got, ids)
			break
		}
	}

	capped, err := db.EventsBySession(ctx, s1, 2)
	if err != nil {
		t.Fatalf("EventsBySession con límite: %v", err)
	}
	if len(capped) != 2 || capped[0].ID != ids[0] || capped[1].ID != ids[1] {
		t.Errorf("con límite = %v", capped)
	}

	none, err := db.EventsBySession(ctx, s1+999, 0)
	if err != nil {
		t.Fatalf("EventsBySession sesión inexistente: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("sesión inexistente = %v, quería vacío", none)
	}
}

func TestChatCountBySessionGroupsByPlatform(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	s1, err := db.StartSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := db.StartSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s3, err := db.StartSession(ctx)
	if err != nil {
		t.Fatal(err)
	}

	msgs := []store.NewChatMessage{
		{SessionID: s1, Platform: "twitch", AuthorID: "1", Author: "a", Text: "hola", At: time.Now()},
		{SessionID: s1, Platform: "twitch", AuthorID: "2", Author: "b", Text: "hola", At: time.Now()},
		{SessionID: s1, Platform: "kick", AuthorID: "3", Author: "c", Text: "hola", At: time.Now()},
		{SessionID: s2, Platform: "youtube", AuthorID: "4", Author: "d", Text: "hola", At: time.Now()},
	}
	if err := db.InsertChatMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}

	total, porPlataforma, err := db.ChatCountBySession(ctx, s1)
	if err != nil {
		t.Fatalf("ChatCountBySession s1: %v", err)
	}
	if total != 3 || porPlataforma["twitch"] != 2 || porPlataforma["kick"] != 1 {
		t.Errorf("s1 = %d, %v", total, porPlataforma)
	}

	total2, _, err := db.ChatCountBySession(ctx, s2)
	if err != nil {
		t.Fatalf("ChatCountBySession s2: %v", err)
	}
	if total2 != 1 {
		t.Errorf("s2 total = %d", total2)
	}

	total3, porPlataforma3, err := db.ChatCountBySession(ctx, s3)
	if err != nil {
		t.Fatalf("ChatCountBySession s3: %v", err)
	}
	if total3 != 0 {
		t.Errorf("s3 total = %d", total3)
	}
	if porPlataforma3 == nil {
		t.Error("s3 mapa = nil, quería mapa vacío")
	}
	if len(porPlataforma3) != 0 {
		t.Errorf("s3 mapa = %v, quería vacío", porPlataforma3)
	}
}
