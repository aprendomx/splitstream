-- Fila única (id = 1) con la configuración persistente del servicio.
CREATE TABLE settings (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    ingest_app           TEXT    NOT NULL DEFAULT 'live',
    ingest_key_encrypted BLOB    NOT NULL,
    ingest_key_last4     TEXT    NOT NULL,
    password_hash        TEXT    NOT NULL DEFAULT '',
    master_key_check     BLOB    NOT NULL,
    created_at           TEXT    NOT NULL,
    updated_at           TEXT    NOT NULL
);

-- stream_key_last4 está desnormalizado a propósito: permite enmascarar el listado
-- sin descifrar nada, de forma que la master key solo se usa al revelar.
CREATE TABLE destinations (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    name                 TEXT    NOT NULL,
    platform             TEXT    NOT NULL
                                 CHECK (platform IN ('youtube','twitch','facebook','kick','x','custom')),
    rtmp_url             TEXT    NOT NULL,
    stream_key_encrypted BLOB    NOT NULL,
    stream_key_last4     TEXT    NOT NULL,
    enabled              INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    sort_order           INTEGER NOT NULL,
    created_at           TEXT    NOT NULL,
    updated_at           TEXT    NOT NULL
);

CREATE INDEX idx_destinations_sort ON destinations (sort_order);

CREATE TABLE sessions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at  TEXT    NOT NULL,
    ended_at    TEXT,
    width       INTEGER,
    height      INTEGER,
    bitrate_bps INTEGER
);

CREATE INDEX idx_sessions_started ON sessions (started_at DESC);

-- Las referencias son ON DELETE SET NULL: borrar un destino no debe borrar la
-- evidencia de lo que pasó con él.
CREATE TABLE events (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id     INTEGER REFERENCES sessions (id) ON DELETE SET NULL,
    destination_id INTEGER REFERENCES destinations (id) ON DELETE SET NULL,
    level          TEXT    NOT NULL CHECK (level IN ('info', 'warn', 'error')),
    kind           TEXT    NOT NULL,
    message        TEXT    NOT NULL,
    created_at     TEXT    NOT NULL
);

CREATE INDEX idx_events_created ON events (created_at DESC);
