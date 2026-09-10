-- Grabación (spec v0.9 §5). Sin ALTER TABLE: los tests rebobinan user_version y reaplican
-- las migraciones, y SQLite no tiene ADD COLUMN IF NOT EXISTS. Los ajustes van en una tabla
-- propia de fila única, como settings, con INSERT OR IGNORE para que reaplicarla no falle.
CREATE TABLE IF NOT EXISTS recording_settings (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    enabled     INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    segment_min INTEGER NOT NULL DEFAULT 10 CHECK (segment_min BETWEEN 0 AND 240),
    max_gb      REAL    NOT NULL DEFAULT 20 CHECK (max_gb > 0),
    keep_days   INTEGER NOT NULL DEFAULT 30 CHECK (keep_days >= 0),
    updated_at  TEXT    NOT NULL
);
INSERT OR IGNORE INTO recording_settings (id, updated_at) VALUES (1, '1970-01-01T00:00:00.000000000Z');

-- path es RELATIVO al directorio de grabaciones: mover la carpeta entera (o cambiar la
-- variable de entorno) no rompe el listado. ended_at NULL = segmento en curso.
CREATE TABLE IF NOT EXISTS recordings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  INTEGER REFERENCES sessions (id) ON DELETE SET NULL,
    path        TEXT    NOT NULL UNIQUE,
    segment     INTEGER NOT NULL,
    started_at  TEXT    NOT NULL,
    ended_at    TEXT,
    bytes       INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_recordings_session ON recordings (session_id, segment);
