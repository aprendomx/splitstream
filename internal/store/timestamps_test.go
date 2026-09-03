package store_test

import (
	"context"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

// tsPattern es el formato persistente: RFC3339 en UTC con nueve dígitos de fracción.
var tsPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}Z$`)

// TestMigration0002RewritesExistingRows prueba la migración de datos sin tocar nada
// privado: escribe filas con el formato viejo, retrasa PRAGMA user_version a 1, cierra y
// vuelve a abrir. Open aplica la 0002 de verdad, que es el camino que corre en producción
// cuando alguien actualiza el binario sobre una base existente.
func TestMigration0002RewritesExistingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Las tres formas que time.RFC3339Nano podía producir: sin fracción, con fracción
	// recortada y con fracción completa. Cronológicamente van 1 < 2 < 3.
	viejos := []struct{ ts, msg string }{
		{"2026-09-02T10:00:00Z", "evento-1"},
		{"2026-09-02T10:00:00.5Z", "evento-2"},
		{"2026-09-02T10:00:00.987654321Z", "evento-3"},
	}
	for _, v := range viejos {
		if _, err := db.SQL().ExecContext(ctx,
			`INSERT INTO events (level, kind, message, created_at) VALUES ('info', 'test', ?, ?)`,
			v.msg, v.ts); err != nil {
			t.Fatalf("insertar %s: %v", v.msg, err)
		}
	}

	// Antes de migrar, el orden de texto está mal: es el bug que se está arreglando.
	if got := primerEvento(t, db); got != "evento-2" {
		t.Fatalf("el test no reproduce el bug: el primero por texto es %q, esperaba evento-2", got)
	}

	if _, err := db.SQL().ExecContext(ctx, `PRAGMA user_version = 1`); err != nil {
		t.Fatalf("retrasar user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	t.Cleanup(func() { db2.Close() })

	if got := primerEvento(t, db2); got != "evento-1" {
		t.Errorf("tras migrar, el primero por texto es %q; quería evento-1", got)
	}

	rows, err := db2.SQL().QueryContext(ctx, `SELECT message, created_at FROM events ORDER BY created_at`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var orden []string
	for rows.Next() {
		var msg, ts string
		if err := rows.Scan(&msg, &ts); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !tsPattern.MatchString(ts) {
			t.Errorf("%s quedó con created_at = %q, que no es de ancho fijo", msg, ts)
		}
		orden = append(orden, msg)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	quiero := []string{"evento-1", "evento-2", "evento-3"}
	if len(orden) != len(quiero) {
		t.Fatalf("orden = %v, quería %v", orden, quiero)
	}
	for i := range quiero {
		if orden[i] != quiero[i] {
			t.Errorf("orden = %v, quería %v", orden, quiero)
			break
		}
	}
}

// TestNewRowsAreWrittenWithFixedWidth comprueba que lo que escribe el código de hoy ya
// tiene la forma nueva, no solo lo que migró.
func TestNewRowsAreWrittenWithFixedWidth(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)

	if _, err := db.LogEvent(ctx, store.Event{Level: store.LevelInfo, Kind: "test", Message: "nuevo"}); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}

	var ts string
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT created_at FROM events WHERE message = 'nuevo'`).Scan(&ts); err != nil {
		t.Fatalf("leer created_at: %v", err)
	}
	if !tsPattern.MatchString(ts) {
		t.Errorf("created_at = %q, que no es de ancho fijo", ts)
	}
}

func primerEvento(t *testing.T, db *store.DB) string {
	t.Helper()
	var msg string
	if err := db.SQL().QueryRowContext(context.Background(),
		`SELECT message FROM events ORDER BY created_at LIMIT 1`).Scan(&msg); err != nil {
		t.Fatalf("consulta de orden: %v", err)
	}
	return msg
}

// TestTikTokIsAnAcceptedPlatform: TikTok se añadió al conjunto cerrado de plataformas en la
// migración 0003. Sin ella, el CHECK del esquema rechaza la fila y el error llega como un
// fallo de constraint del driver, indistinguible de un problema de disco.
func TestTikTokIsAnAcceptedPlatform(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)

	d, err := db.CreateDestination(ctx, testCipher(t, 1), store.NewDestination{
		Name: "TikTok", Platform: store.PlatformTikTok,
		// TikTok emite el servidor por emisión, así que no hay una URL fija que precargar.
		RTMPURL: "rtmp://push-rtmp-l1-va01.tiktokcdn.com/live",
		Key:     crypto.Secret("clave-de-tiktok"), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateDestination con TikTok: %v", err)
	}
	if d.Platform != store.PlatformTikTok {
		t.Errorf("platform = %q, quería tiktok", d.Platform)
	}
}

// TestMigration0003KeepsExistingDestinations: la 0003 reconstruye la tabla entera, porque
// SQLite no deja alterar un CHECK. Reconstruir es justo donde se pierden filas si se hace
// mal, así que se prueba por el camino real: retrasar user_version y reabrir.
func TestMigration0003KeepsExistingDestinations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	c := testCipher(t, 1)
	if err := db.Bootstrap(ctx, c); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	var creados []int64
	for _, nombre := range []string{"uno", "dos", "tres"} {
		d, err := db.CreateDestination(ctx, c, store.NewDestination{
			Name: nombre, Platform: store.PlatformCustom,
			RTMPURL: "rtmp://x/live", Key: crypto.Secret("clave-" + nombre), Enabled: true,
		})
		if err != nil {
			t.Fatalf("CreateDestination(%s): %v", nombre, err)
		}
		creados = append(creados, d.ID)
	}

	if _, err := db.SQL().ExecContext(ctx, `PRAGMA user_version = 2`); err != nil {
		t.Fatalf("retrasar user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	t.Cleanup(func() { db2.Close() })

	dests, err := db2.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("ListDestinations: %v", err)
	}
	if len(dests) != 3 {
		t.Fatalf("destinos tras migrar = %d, quería 3", len(dests))
	}
	for i, d := range dests {
		if d.ID != creados[i] {
			t.Errorf("posición %d: id = %d, quería %d", i, d.ID, creados[i])
		}
	}

	// Las claves siguen descifrándose: la copia no tocó los BLOB.
	k, err := db2.RevealDestinationKey(ctx, c, creados[0])
	if err != nil {
		t.Fatalf("RevealDestinationKey: %v", err)
	}
	if k.Reveal() != "clave-uno" {
		t.Errorf("la clave se corrompió al reconstruir la tabla: %q", k.Reveal())
	}

	// El índice de orden sobrevive: se recrea, porque DROP TABLE se lo lleva.
	var n int
	if err := db2.SQL().QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_destinations_sort'`).Scan(&n); err != nil {
		t.Fatalf("consultar el índice: %v", err)
	}
	if n != 1 {
		t.Error("idx_destinations_sort no sobrevivió a la reconstrucción")
	}

	// Y ahora sí acepta TikTok.
	if _, err := db2.CreateDestination(ctx, c, store.NewDestination{
		Name: "TikTok", Platform: store.PlatformTikTok,
		RTMPURL: "rtmp://x/live", Key: crypto.Secret("k"), Enabled: true,
	}); err != nil {
		t.Errorf("tras migrar sigue sin aceptar TikTok: %v", err)
	}
}
