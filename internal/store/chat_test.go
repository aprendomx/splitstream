package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

func mensajes(sesion int64, n int, desde time.Time) []store.NewChatMessage {
	out := make([]store.NewChatMessage, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, store.NewChatMessage{
			SessionID: sesion, Platform: "twitch", AuthorID: "u1", Author: "viewer",
			Text: "hola " + string(rune('a'+i%26)), Color: "#00FF7F", Badges: []string{"moderator/1"},
			MessageID: "m", At: desde.Add(time.Duration(i) * time.Second),
		})
	}
	return out
}

func TestInsertChatMessagesIsOneBatchAndPaginatesByID(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	s1, _ := db.StartSession(ctx)
	s2, _ := db.StartSession(ctx)
	if err := db.InsertChatMessages(ctx, mensajes(s1, 120, time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertChatMessages(ctx, mensajes(s2, 3, time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertChatMessages(ctx, nil); err != nil {
		t.Errorf("lote vacío: %v", err)
	}

	pag, err := db.ChatMessages(ctx, s1, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(pag) != 50 || pag[0].ID >= pag[49].ID {
		t.Fatalf("página = %d mensajes, orden ascendente por id esperado", len(pag))
	}
	if pag[0].SessionID != s1 || pag[0].Badges[0] != "moderator/1" || pag[0].Color != "#00FF7F" {
		t.Errorf("primer mensaje = %+v", pag[0])
	}
	resto, _ := db.ChatMessages(ctx, s1, pag[49].ID, 500)
	if len(resto) != 70 {
		t.Errorf("after = %d, quería los 70 restantes", len(resto))
	}
	if otros, _ := db.ChatMessages(ctx, s2, 0, 10); len(otros) != 3 {
		t.Errorf("sesión 2 = %d", len(otros))
	}
	if _, err := db.ChatMessages(ctx, s1, 0, 0); err == nil {
		t.Error("limit 0 debería ser inválido")
	}
}

func TestChatFallsWithItsSessionAndPruneChatKeepsTheNewest(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	s1, _ := db.StartSession(ctx)
	db.InsertChatMessages(ctx, mensajes(s1, 10, time.Now().Add(-48*time.Hour)))
	if err := db.FinishSession(ctx, s1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	// Sesión cerrada y vieja: la retención de sesiones la borra y el chat cae con ella.
	if _, err := db.SQL().ExecContext(ctx, `UPDATE sessions SET started_at = ? WHERE id = ?`, "2000-01-01T00:00:00.000Z", s1); err != nil {
		t.Fatal(err)
	}
	if n, err := db.PruneSessions(ctx, time.Now().Add(-24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("PruneSessions = %d, %v", n, err)
	}
	if got, _ := db.ChatMessages(ctx, s1, 0, 10); len(got) != 0 {
		t.Errorf("el chat sobrevivió a su sesión: %d", len(got))
	}

	s2, _ := db.StartSession(ctx)
	db.InsertChatMessages(ctx, mensajes(s2, 30, time.Now()))
	n, err := db.PruneChat(ctx, 12)
	if err != nil || n != 18 {
		t.Fatalf("PruneChat = %d, %v; quería 18", n, err)
	}
	got, _ := db.ChatMessages(ctx, s2, 0, 100)
	if len(got) != 12 || got[0].Text != "hola s" {
		t.Errorf("quedan %d; el primero es %q (se conservan los más nuevos)", len(got), got[0].Text)
	}
}
