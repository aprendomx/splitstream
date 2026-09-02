// Package store envuelve la base SQLite: apertura, migraciones y repositorios.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // driver "sqlite", puro Go
)

// SchemaVersion es la última migración incluida en el binario.
const SchemaVersion = 1

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB es la base de datos del servicio.
type DB struct {
	db *sql.DB
}

// SQL expone el *sql.DB subyacente. Solo para tests y para los repositorios
// de este paquete; el resto del programa usa los métodos tipados.
func (d *DB) SQL() *sql.DB { return d.db }

// Close cierra la base de datos.
func (d *DB) Close() error { return d.db.Close() }

// Open abre (o crea) la base en path, aplica los pragmas y corre las migraciones
// pendientes. Es idempotente: reabrir una base ya migrada no toca los datos.
func Open(ctx context.Context, dbPath string) (*DB, error) {
	dsn := "file:" + dbPath + "?" + url.Values{
		"_pragma": {
			"journal_mode(WAL)",
			"busy_timeout(5000)",
			"foreign_keys(1)",
			"synchronous(NORMAL)",
		},
	}.Encode()

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrir %s: %w", dbPath, err)
	}
	// SQLite tolera mal la escritura concurrente; una sola conexión evita
	// SQLITE_BUSY sin tener que reintentar en cada consulta.
	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("conectar a %s: %w", dbPath, err)
	}

	if err := migrate(ctx, sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}

	return &DB{db: sqlDB}, nil
}

type migration struct {
	version int
	name    string
	sql     string
}

// migrate aplica en orden las migraciones cuya versión supere PRAGMA user_version.
func migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	var current int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("leer user_version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migración %d: begin: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migración %d (%s): %w", m.version, m.name, err)
		}
		// PRAGMA no admite parámetros vinculados; m.version viene de strconv.Atoi
		// sobre el nombre del archivo embebido, así que es un entero de confianza.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migración %d: fijar user_version: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migración %d: commit: %w", m.version, err)
		}
	}
	return nil
}

// loadMigrations lee migrations/*.sql y las ordena por versión. Los nombres deben
// tener la forma NNNN_descripcion.sql.
func loadMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("leer migraciones: %w", err)
	}

	out := make([]migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migración %q: falta el prefijo NNNN_", e.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migración %q: prefijo no numérico: %w", e.Name(), err)
		}
		body, err := migrationsFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("leer %q: %w", e.Name(), err)
		}
		out = append(out, migration{version: version, name: e.Name(), sql: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })

	if len(out) > 0 && out[len(out)-1].version != SchemaVersion {
		return nil, fmt.Errorf(
			"SchemaVersion es %d pero la última migración es %d: actualiza la constante",
			SchemaVersion, out[len(out)-1].version)
	}
	return out, nil
}
