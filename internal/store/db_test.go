package store_test

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func openTemp(t *testing.T) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesSchema(t *testing.T) {
	db := openTemp(t)

	rows, err := db.SQL().QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	sort.Strings(got)

	want := []string{"destination_logos", "destinations", "events", "sessions", "settings"}
	if len(got) != len(want) {
		t.Fatalf("tablas = %v, quería %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tablas = %v, quería %v", got, want)
		}
	}
}

func TestOpenSetsSchemaVersion(t *testing.T) {
	db := openTemp(t)

	var version int
	if err := db.SQL().QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version != store.SchemaVersion {
		t.Errorf("user_version = %d, quería %d", version, store.SchemaVersion)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	first, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("primer Open: %v", err)
	}
	if _, err := first.SQL().ExecContext(ctx,
		`INSERT INTO sessions (started_at) VALUES ('2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	first.Close()

	second, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("segundo Open: %v", err)
	}
	defer second.Close()

	var n int
	if err := second.SQL().QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("reabrir la base perdió datos: %d filas, quería 1", n)
	}
}

func TestOpenEnablesWALAndForeignKeys(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	var mode string
	if err := db.SQL().QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, quería \"wal\"", mode)
	}

	var fk int
	if err := db.SQL().QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, quería 1", fk)
	}
}

func TestSchemaRejectsUnknownPlatform(t *testing.T) {
	db := openTemp(t)
	_, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO destinations
		 (name, platform, rtmp_url, stream_key_encrypted, stream_key_last4, sort_order, created_at, updated_at)
		 VALUES ('x', 'vimeo', 'rtmp://x', X'00', '', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("quería que el CHECK rechazara la plataforma 'vimeo'")
	}
}
