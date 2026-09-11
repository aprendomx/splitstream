-- Cuentas de plataforma (v0.11, spec §3.1). Los tokens van cifrados con la clave maestra,
-- como las claves de stream. Sin ALTER TABLE: los tests rebobinan user_version y
-- reaplican, y ADD COLUMN no es idempotente; el enlace destino ↔ cuenta es una tabla.
CREATE TABLE IF NOT EXISTS platform_accounts (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    platform                TEXT    NOT NULL CHECK (platform IN ('twitch','youtube','kick')),
    external_id             TEXT    NOT NULL,
    display_name            TEXT    NOT NULL,
    access_token_encrypted  BLOB    NOT NULL,
    refresh_token_encrypted BLOB,
    expires_at              TEXT,
    scopes                  TEXT    NOT NULL,
    status                  TEXT    NOT NULL DEFAULT 'ok' CHECK (status IN ('ok','reauth')),
    own_app                 INTEGER NOT NULL DEFAULT 0 CHECK (own_app IN (0,1)),
    client_id_encrypted     BLOB,
    client_secret_encrypted BLOB,
    created_at              TEXT    NOT NULL,
    updated_at              TEXT    NOT NULL,
    UNIQUE (platform, external_id)
);

CREATE TABLE IF NOT EXISTS destination_accounts (
    destination_id INTEGER PRIMARY KEY REFERENCES destinations (id) ON DELETE CASCADE,
    account_id     INTEGER NOT NULL REFERENCES platform_accounts (id) ON DELETE CASCADE
);
