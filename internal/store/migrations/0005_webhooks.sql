-- Webhooks salientes (spec v0.8 §5.2). El secreto va cifrado con la clave maestra, como
-- las claves de destino, y NULL cuando el formato no firma (discord, slack: la URL ya
-- lleva su token). last_status y last_error son para que el panel enseñe si el aviso
-- llega, sin tener que consultar el log.
-- IF NOT EXISTS por la misma razón que 0004: los tests rebobinan user_version.
CREATE TABLE IF NOT EXISTS webhooks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT    NOT NULL,
    url              TEXT    NOT NULL,
    format           TEXT    NOT NULL CHECK (format IN ('json', 'discord', 'slack')),
    secret_encrypted BLOB,
    min_level        TEXT    NOT NULL CHECK (min_level IN ('info', 'warn', 'error')),
    enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    last_status      INTEGER,
    last_error       TEXT    NOT NULL DEFAULT '',
    created_at       TEXT    NOT NULL,
    updated_at       TEXT    NOT NULL
);
