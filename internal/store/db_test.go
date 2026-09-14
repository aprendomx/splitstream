package store_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
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

	want := []string{"chat_messages", "destination_accounts", "destination_broadcasts", "destination_logos", "destinations", "events", "platform_accounts", "quota_usage", "recording_settings", "recordings", "sessions", "settings", "webhooks"}
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

// Una base migrada por una versión más nueva de Splitstream no se abre.
//
// El runner solo aplica lo que supere user_version, así que sin la comprobación un binario
// viejo sobre una base nueva no tendría nada que aplicar y arrancaría: el fallo llegaría
// después, con el servicio ya escribiendo, en forma de columna que no existe o de CHECK
// que rechaza algo que antes valía. Aquí se exige que se niegue al abrir y que el mensaje
// diga las dos salidas reales (subir el binario o restaurar el respaldo).
func TestOpenRejectsANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "futuro.db")

	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// 99 es "cualquier versión que este binario no conoce": el número exacto da igual
	// mientras supere SchemaVersion.
	if _, err := db.SQL().ExecContext(context.Background(), `PRAGMA user_version = 99`); err != nil {
		t.Fatalf("fijar user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = store.Open(context.Background(), path)
	if err == nil {
		t.Fatal("Open abrió una base con un esquema más nuevo que el del binario")
	}
	for _, quiero := range []string{"más nueva", "99", "actualiza el binario", "respaldo"} {
		if !strings.Contains(err.Error(), quiero) {
			t.Errorf("el error no dice %q: %v", quiero, err)
		}
	}
}

// Cada migración aplicada deja una línea en el log. No es cosmético: el user_version de
// después no distingue "migró ahora" de "ya venía así", así que es lo único que le dice a
// quien actualiza en un servidor que el esquema se tocó. deploy/migrate-test.sh se apoya
// en ella.
func TestMigrationsAreLogged(t *testing.T) {
	var buf bytes.Buffer
	anterior := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(anterior) })

	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "nueva.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	log := buf.String()
	if !strings.Contains(log, "migración aplicada") {
		t.Fatalf("el log de una base nueva no dice que migró: %q", log)
	}
	// La última migración es SchemaVersion, así que su línea tiene que estar.
	if quiero := fmt.Sprintf("version=%d", store.SchemaVersion); !strings.Contains(log, quiero) {
		t.Errorf("el log no trae %q: %q", quiero, log)
	}
}
