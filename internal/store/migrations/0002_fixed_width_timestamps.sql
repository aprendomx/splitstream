-- Reescribe los timestamps al formato de ancho fijo del spec §15.4.
--
-- Las filas viejas se escribieron con time.RFC3339Nano, que recorta los ceros finales de
-- la fracción, así que en la base hay tres formas: sin fracción ('...:00Z'), con fracción
-- recortada ('...:00.5Z') y con fracción completa ('...:00.123456789Z'). Esta expresión
-- normaliza las tres a nueve dígitos.
--
-- Todas estas columnas se escribieron en UTC, así que terminan en 'Z' y sus primeros 19
-- caracteres son 'YYYY-MM-DDTHH:MM:SS'. La fracción, si la hay, va de la posición 21 a la
-- 'Z' final.

UPDATE settings SET
    created_at = substr(created_at, 1, 19) || '.' || substr(
        CASE WHEN instr(created_at, '.') = 0 THEN '000000000'
             ELSE substr(created_at, 21, length(created_at) - 21) || '000000000' END, 1, 9) || 'Z',
    updated_at = substr(updated_at, 1, 19) || '.' || substr(
        CASE WHEN instr(updated_at, '.') = 0 THEN '000000000'
             ELSE substr(updated_at, 21, length(updated_at) - 21) || '000000000' END, 1, 9) || 'Z';

UPDATE destinations SET
    created_at = substr(created_at, 1, 19) || '.' || substr(
        CASE WHEN instr(created_at, '.') = 0 THEN '000000000'
             ELSE substr(created_at, 21, length(created_at) - 21) || '000000000' END, 1, 9) || 'Z',
    updated_at = substr(updated_at, 1, 19) || '.' || substr(
        CASE WHEN instr(updated_at, '.') = 0 THEN '000000000'
             ELSE substr(updated_at, 21, length(updated_at) - 21) || '000000000' END, 1, 9) || 'Z';

UPDATE sessions SET
    started_at = substr(started_at, 1, 19) || '.' || substr(
        CASE WHEN instr(started_at, '.') = 0 THEN '000000000'
             ELSE substr(started_at, 21, length(started_at) - 21) || '000000000' END, 1, 9) || 'Z';

UPDATE sessions SET
    ended_at = substr(ended_at, 1, 19) || '.' || substr(
        CASE WHEN instr(ended_at, '.') = 0 THEN '000000000'
             ELSE substr(ended_at, 21, length(ended_at) - 21) || '000000000' END, 1, 9) || 'Z'
WHERE ended_at IS NOT NULL;

UPDATE events SET
    created_at = substr(created_at, 1, 19) || '.' || substr(
        CASE WHEN instr(created_at, '.') = 0 THEN '000000000'
             ELSE substr(created_at, 21, length(created_at) - 21) || '000000000' END, 1, 9) || 'Z';
