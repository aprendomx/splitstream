-- Mensajes de chat de una sesión (v0.11, spec §7). Caen con la sesión: la retención de
-- sesiones los arrastra por la clave ajena.
CREATE TABLE IF NOT EXISTS chat_messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    account_id INTEGER,
    platform   TEXT    NOT NULL,
    author_id  TEXT    NOT NULL,
    author     TEXT    NOT NULL,
    text       TEXT    NOT NULL,
    color      TEXT    NOT NULL DEFAULT '',
    badges     TEXT    NOT NULL DEFAULT '',
    message_id TEXT    NOT NULL DEFAULT '',
    at         TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS chat_messages_session ON chat_messages (session_id, id);
