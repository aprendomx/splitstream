-- Logo opcional por destino, para que el panel identifique cada canal de un vistazo.
--
-- Va en tabla aparte y no como columna de destinations porque el listado de destinos se
-- consulta cada vez que se pinta el panel, y no debe arrastrar imágenes por la red ni por
-- memoria. Con tabla aparte, quien quiere los bytes los pide explícitamente.
--
-- Los bytes van dentro de la base y no en archivos junto a ella para que la base siga
-- siendo el estado completo: copiar splitstream.db copia los logos, y el volumen único de
-- Docker sigue bastando.
--
-- Lo que se guarda aquí es siempre un PNG ya normalizado por la capa HTTP (como máximo
-- 256x256), nunca el archivo tal cual lo subió el usuario.
--
-- El ON DELETE CASCADE SÍ se aplica: foreign_keys(1) viene en el DSN de Open. Aun así hay
-- un test que comprueba que borrar un destino se lleva su logo, para que apagar esa
-- pragma en el futuro no pase inadvertido.
-- IF NOT EXISTS porque en este repositorio las migraciones tienen que tolerar volver a
-- correr desde la versión 1: los tests de los timestamps rebobinan user_version para
-- ejercitar la 0002 sobre datos de verdad, y eso reaplica todas las posteriores.
CREATE TABLE IF NOT EXISTS destination_logos (
    destination_id INTEGER PRIMARY KEY
                           REFERENCES destinations (id) ON DELETE CASCADE,
    image          BLOB    NOT NULL,
    etag           TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
);
