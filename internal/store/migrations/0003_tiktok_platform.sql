-- Añade 'tiktok' al conjunto cerrado de plataformas.
--
-- SQLite no permite alterar un CHECK, así que hay que reconstruir la tabla entera: crear
-- la nueva, copiar, tirar la vieja y renombrar. Es el procedimiento que la propia
-- documentación de SQLite prescribe para este caso.
--
-- ATENCIÓN, esta premisa era FALSA (corregido el 2026-09-04): decía que era seguro no
-- desactivar las claves ajenas porque el binario nunca las activa. Sí las activa,
-- foreign_keys(1) está en el DSN de Open desde la fase 1. Con ellas puestas, el DROP TABLE
-- de abajo ejecuta un borrado implícito que dispara el ON DELETE SET NULL de events, y a
-- quien actualizara desde una base anterior le dejó todo el historial de eventos sin
-- destino. Ese daño ya está hecho y no se puede deshacer.
--
-- Ahora migrate() apaga las claves ajenas mientras aplica cada migración y las vuelve a
-- encender al terminar, que es el procedimiento que prescribe SQLite. Si copias este
-- archivo como plantilla para reconstruir otra tabla, la protección viene de ahí, no de
-- este archivo.
--
-- El INSERT nombra las columnas en vez de usar SELECT *: si alguien añade una columna en
-- una migración futura y copia este archivo como plantilla, un SELECT * la desalinearía en
-- silencio.

CREATE TABLE destinations_new (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    name                 TEXT    NOT NULL,
    platform             TEXT    NOT NULL
                                 CHECK (platform IN ('youtube','twitch','facebook','kick','x','tiktok','custom')),
    rtmp_url             TEXT    NOT NULL,
    stream_key_encrypted BLOB    NOT NULL,
    stream_key_last4     TEXT    NOT NULL,
    enabled              INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    sort_order           INTEGER NOT NULL,
    created_at           TEXT    NOT NULL,
    updated_at           TEXT    NOT NULL
);

INSERT INTO destinations_new
    (id, name, platform, rtmp_url, stream_key_encrypted, stream_key_last4,
     enabled, sort_order, created_at, updated_at)
SELECT
    id, name, platform, rtmp_url, stream_key_encrypted, stream_key_last4,
    enabled, sort_order, created_at, updated_at
FROM destinations;

DROP TABLE destinations;

ALTER TABLE destinations_new RENAME TO destinations;

-- DROP TABLE se llevó el índice por delante.
CREATE INDEX idx_destinations_sort ON destinations (sort_order);
