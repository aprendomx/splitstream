package store_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"

	_ "modernc.org/sqlite"
)

// dsnComoOpen replica el DSN de store.Open, claves ajenas incluidas: el comportamiento que
// se prueba depende de ellas, así que abrir de otra forma no probaría nada.
func dsnComoOpen(path string) string {
	return "file:" + path + "?" + url.Values{
		"_pragma": {
			"journal_mode(WAL)", "busy_timeout(5000)",
			"foreign_keys(1)", "synchronous(NORMAL)",
		},
	}.Encode()
}

// baseEnVersion2 deja una base con el esquema de la migración 2 y una fila de cada tabla
// que importa: un destino y un evento que lo referencia.
func baseEnVersion2(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", dsnComoOpen(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, archivo := range []string{
		"migrations/0001_initial.sql",
		"migrations/0002_fixed_width_timestamps.sql",
	} {
		sqlTexto, err := os.ReadFile(archivo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(sqlTexto)); err != nil {
			t.Fatalf("%s: %v", archivo, err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}

	const ts = "2026-01-01T00:00:00.000000000Z"
	if _, err := db.Exec(`INSERT INTO destinations
		(id, name, platform, rtmp_url, stream_key_encrypted, stream_key_last4,
		 enabled, sort_order, created_at, updated_at)
		VALUES (7, 'Canal', 'custom', 'rtmp://a/b', x'00', '1234', 1, 0, ?, ?)`, ts, ts); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events (destination_id, level, kind, message, created_at)
		VALUES (7, 'info', 'conectado', 'ok', ?)`, ts); err != nil {
		t.Fatal(err)
	}
}

// TestMigrarNoRompeLosVinculosDeLosEventos es la regresión del fallo que la migración 0003
// introdujo sin que nadie lo viera.
//
// 0003 reconstruye destinations con el procedimiento que prescribe SQLite —crear la nueva,
// copiar, DROP de la vieja, renombrar— y su comentario daba por hecho que el binario nunca
// activa las claves ajenas. Sí las activa: vienen en el DSN de Open desde la fase 1. Y con
// ellas activas, un DROP TABLE ejecuta un borrado implícito de la tabla que dispara las
// acciones referenciales, así que el ON DELETE SET NULL de events convertía en NULL el
// destination_id de TODO el historial de quien actualizara.
//
// La prueba parte de una base en la versión 2 con datos, la abre con store.Open —que aplica
// las migraciones que falten— y comprueba que el evento sigue apuntando a su destino.
func TestMigrarNoRompeLosVinculosDeLosEventos(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v2.db")
	baseEnVersion2(t, path)

	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var destino *int64
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT destination_id FROM events WHERE kind = 'conectado'`).Scan(&destino); err != nil {
		t.Fatalf("leer el evento: %v", err)
	}
	if destino == nil {
		t.Fatal("el evento perdió su destino: las migraciones dispararon las acciones referenciales")
	}
	if *destino != 7 {
		t.Errorf("destination_id = %d, quería 7", *destino)
	}
}

// TestMigrarDejaLasClavesAjenasActivas: apagarlas para migrar es correcto, dejarlas
// apagadas no. Todo lo que el programa hace después depende de que estén puestas.
func TestMigrarDejaLasClavesAjenasActivas(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v2.db")
	baseEnVersion2(t, path)

	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var fk int
	if err := db.SQL().QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("PRAGMA foreign_keys = %d tras migrar, quería 1", fk)
	}
}
