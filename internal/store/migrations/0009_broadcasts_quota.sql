-- v0.12: la emisión (o la clave por API) vinculada a un destino, y la cuota de YouTube por
-- cuenta y día. Sin ALTER TABLE: idempotente como las demás.
CREATE TABLE IF NOT EXISTS destination_broadcasts (
    destination_id INTEGER PRIMARY KEY REFERENCES destinations (id) ON DELETE CASCADE,
    account_id     INTEGER NOT NULL REFERENCES platform_accounts (id) ON DELETE CASCADE,
    platform       TEXT    NOT NULL,
    broadcast_ref  TEXT    NOT NULL DEFAULT '',
    stream_ref     TEXT    NOT NULL DEFAULT '',
    live_chat_id   TEXT    NOT NULL DEFAULT '',
    key_from_api   INTEGER NOT NULL DEFAULT 1 CHECK (key_from_api IN (0,1)),
    status         TEXT    NOT NULL DEFAULT 'created' CHECK (status IN ('created','live','complete')),
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS quota_usage (
    account_id INTEGER NOT NULL REFERENCES platform_accounts (id) ON DELETE CASCADE,
    day        TEXT    NOT NULL,
    units      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, day)
);
