package store_test

import (
	"context"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestStartAndFinishSession(t *testing.T) {
	db, _ := bootstrapped(t)
	ctx := context.Background()

	id, err := db.StartSession(ctx)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if id == 0 {
		t.Fatal("StartSession no devolvió id")
	}
	if err := db.FinishSession(ctx, id, 1920, 1080, 6_000_000); err != nil {
		t.Fatalf("FinishSession: %v", err)
	}

	var (
		ended   *string
		width   int
		height  int
		bitrate int
	)
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT ended_at, width, height, bitrate_bps FROM sessions WHERE id = ?`, id).
		Scan(&ended, &width, &height, &bitrate); err != nil {
		t.Fatalf("select: %v", err)
	}
	if ended == nil {
		t.Error("ended_at sigue en NULL tras FinishSession")
	}
	if width != 1920 || height != 1080 || bitrate != 6_000_000 {
		t.Errorf("got %dx%d @ %d", width, height, bitrate)
	}
}

func TestLogEventAndRecentEventsAreNewestFirst(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	sessionID, err := db.StartSession(ctx)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	for _, kind := range []string{"primero", "segundo", "tercero"} {
		if _, err := db.LogEvent(ctx, store.Event{
			SessionID:     &sessionID,
			DestinationID: &dest.ID,
			Level:         store.LevelInfo,
			Kind:          kind,
			Message:       "mensaje de " + kind,
		}); err != nil {
			t.Fatalf("LogEvent %s: %v", kind, err)
		}
	}

	events, err := db.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len = %d, quería 3", len(events))
	}
	if events[0].Kind != "tercero" {
		t.Errorf("el primero debería ser el más reciente, es %q", events[0].Kind)
	}
	if events[0].SessionID == nil || *events[0].SessionID != sessionID {
		t.Error("session_id no se persistió")
	}
	if events[0].DestinationID == nil || *events[0].DestinationID != dest.ID {
		t.Error("destination_id no se persistió")
	}
}

func TestRecentEventsRespectsLimit(t *testing.T) {
	db, _ := bootstrapped(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := db.LogEvent(ctx, store.Event{
			Level:   store.LevelWarn,
			Kind:    "prueba",
			Message: "x",
		}); err != nil {
			t.Fatalf("LogEvent: %v", err)
		}
	}

	events, err := db.RecentEvents(ctx, 2)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("len = %d, quería 2", len(events))
	}
}

func TestLogEventRejectsUnknownLevel(t *testing.T) {
	db, _ := bootstrapped(t)
	_, err := db.LogEvent(context.Background(), store.Event{
		Level:   "crítico",
		Kind:    "prueba",
		Message: "x",
	})
	if err == nil {
		t.Fatal("quería error con un nivel desconocido")
	}
}

// Borrar un destino no debe borrar la evidencia de lo que le pasó.
func TestDeleteDestinationKeepsItsEvents(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()

	dest, err := db.CreateDestination(ctx, c, newDest("YouTube"))
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}
	if _, err := db.LogEvent(ctx, store.Event{
		DestinationID: &dest.ID,
		Level:         store.LevelError,
		Kind:          "connect_failed",
		Message:       "connection refused",
	}); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}

	if err := db.DeleteDestination(ctx, dest.ID); err != nil {
		t.Fatalf("DeleteDestination: %v", err)
	}

	events, err := db.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len = %d, quería 1: el evento debe sobrevivir al destino", len(events))
	}
	if events[0].DestinationID != nil {
		t.Errorf("destination_id = %v, quería NULL tras ON DELETE SET NULL", *events[0].DestinationID)
	}
	if events[0].Message != "connection refused" {
		t.Errorf("el mensaje se perdió: %q", events[0].Message)
	}
}
