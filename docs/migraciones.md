# Migraciones del esquema

La base de Splitstream es un único archivo SQLite que vive en la máquina de quien lo
instaló. No hay un DBA detrás, ni una ventana de mantenimiento, ni nadie a quien llamar si
una actualización deja el esquema a medias: hay una persona que ejecutó `splitstream` de
nuevo y espera que siga funcionando. Todo lo que sigue sale de ahí.

El runner de migraciones es `internal/store/db.go`; las migraciones, los archivos
`internal/store/migrations/NNNN_descripcion.sql` que el binario lleva embebidos con
`go:embed`. La versión aplicada se guarda en el `PRAGMA user_version` de la propia base:
no hay tabla de control.

Cada migración que se aplica deja una línea en el log —`migración aplicada version=N`—,
que es lo único que distingue «migró en este arranque» de «ya venía así»: el
`user_version` de después, por sí solo, no lo dice.

---

## Reglas

**1. Una migración publicada no se edita nunca.** En cuanto sale en una release, hay bases
en las que ya corrió. Cambiar el `.sql` no las cambia a ellas: solo consigue que una base
nueva quede con un esquema distinto del de una base vieja, y eso no se descubre hasta que
algo falla en la máquina de otro. Si la 0007 estaba mal, se arregla con la 0010.

**2. Solo hacia delante. No hay `down`.** El runner aplica en orden las migraciones cuya
versión supere `user_version` y no sabe deshacer ninguna. La marcha atrás de una
actualización es restaurar el respaldo, no revertir la migración.

**3. Idempotencia donde el SQL la permita.** `CREATE TABLE IF NOT EXISTS`,
`CREATE INDEX IF NOT EXISTS`, `INSERT OR IGNORE`. No porque el runner vaya a repetir una
migración —no lo hace, `user_version` se lo impide—, sino porque el día que alguien
restaure un respaldo a medias o copie una base entre máquinas, la diferencia es entre
arrancar y no arrancar. `ALTER TABLE ... ADD COLUMN` no admite `IF NOT EXISTS` en SQLite;
para eso está la regla 1.

**4. Una transacción por migración.** Cada archivo se ejecuta dentro de su propio
`BEGIN`/`COMMIT`, junto con el `PRAGMA user_version = N` que la marca como aplicada. Las
dos cosas se comitean a la vez: o la migración entera está y la versión subió, o no está
nada. Por eso el `.sql` no debe traer sus propios `BEGIN` ni `COMMIT`.

**5. Las claves ajenas están apagadas mientras se migra.** El runner ejecuta
`PRAGMA foreign_keys = OFF` antes de cada migración
([`db.go:166`](../internal/store/db.go)) y `PRAGMA foreign_keys = ON` al terminar todas
([`db.go:200`](../internal/store/db.go)).

No es un detalle de estilo. El procedimiento que SQLite prescribe para cambiar una tabla
—crear la nueva, copiar, `DROP` de la vieja, renombrar— es incompatible con las claves
activas: el `DROP TABLE` dispara las acciones referenciales, así que el
`ON DELETE SET NULL` de `events` convertiría en `NULL` el `destination_id` de todo el
historial. Le pasó a la 0003. Una consecuencia práctica: **durante la migración nadie
comprueba la integridad referencial por ti**, así que si tu `.sql` reconstruye una tabla,
te toca a ti no dejar filas huérfanas.

El `PRAGMA` va fuera de la transacción a propósito: dentro de una, SQLite lo ignora en
silencio.

**6. `SchemaVersion` es siempre la última migración.** La constante de
[`db.go:21`](../internal/store/db.go) y el número más alto de `migrations/` tienen que
coincidir; si no, el binario se niega a abrir la base con
«SchemaVersion es N pero la última migración es M» ([`db.go:238`](../internal/store/db.go)).
Es la red que atrapa el olvido más común: añadir el `.sql` y no tocar la constante.

---

## Cómo añadir una

1. **El archivo**: `internal/store/migrations/NNNN_descripcion.sql`, con `NNNN` a cuatro
   dígitos y el número siguiente al último. Sin `BEGIN`/`COMMIT` dentro (regla 4). El
   `go:embed` lo recoge solo: no hay que registrarlo en ningún sitio.
2. **La constante**: sube `SchemaVersion` en `internal/store/db.go` al mismo número.
3. **El test**: `internal/store/migrations_test.go` prueba lo que ninguna otra cosa
   prueba: que migrar una base **con datos dentro** no los rompe. Si tu migración
   reconstruye una tabla o reescribe filas, añade ahí un caso con el esquema anterior y
   una fila de cada tabla implicada (mira `baseEnVersion2` y
   `TestMigrarNoRompeLosVinculosDeLosEventos`). `TestOpenIsIdempotent` y
   `TestOpenSetsSchemaVersion`, en `db_test.go`, cubren el resto.
4. **La API**: si la migración cambia lo que sale por HTTP, actualiza
   [`docs/api.md`](api.md). El contrato de la API está verificado por test, así que un DTO
   nuevo sin documentar rompe la CI.
5. **La prueba de verdad**: la sección siguiente.

---

## Cómo probarla contra una base real de la versión anterior

Los tests de Go migran bases que ellos mismos construyen. Eso no es lo mismo que la base
que lleva meses en el servidor de alguien, creada por un binario que ya no existe en este
árbol. Para eso está `deploy/migrate-test.sh`:

```bash
make build-go
deploy/migrate-test.sh v0.13.0 ./splitstream
```

Qué hace, en orden:

1. Descarga la release `v0.13.0` de GitHub (con `gh` si está autenticado, si no con
   `curl`) y saca el binario del `.tar.gz`.
2. Genera una clave maestra de usar y tirar y **arranca el binario antiguo** sobre una
   base vacía en un directorio temporal. Ese binario crea el esquema de *entonces*, con
   sus migraciones y no las de hoy.
3. Espera a `/healthz`, lo para con `SIGTERM` y anota el `user_version` de la base.
4. **Arranca el binario nuevo sobre esa misma base.** Espera a `/healthz`, lo para y
   vuelve a leer el `user_version`.
5. Exige que el esquema no haya bajado, que el binario nuevo cerrara la base limpiamente
   —sin `-wal` huérfano: es la señal de que hizo checkpoint y soltó la base— y que su log
   no traiga ni `level=ERROR` ni un `panic`.

Termina con una línea del tipo:

```
migración OK: v0.11.0 → v0.12.0 (1 migración: esquema 8 → 9)
migración OK: v0.7.0 → v0.13.0 (5 migraciones: esquema 4 → 9)
```

Si las dos versiones tienen el mismo esquema dice «sin migraciones que aplicar», que
también es un resultado válido: el binario nuevo abrió una base antigua sin tocarla. En
cualquier fallo imprime el log entero de los dos arranques y sale con 1. Con argumentos
que falten o herramientas que no estén, sale con 2.

El `user_version` lo lee del encabezado del `.db` —cuatro bytes en el desplazamiento 60—
con `od`, y no con la CLI de `sqlite3`: esa CLI es una dependencia que el proyecto no tiene
ni va a tener, porque el binario lleva su propio driver en Go.

`SPLITSTREAM_MIGRATE_TEST_OFFLINE=1` usa como «anterior» una copia del binario nuevo, sin
descargar nada: sirve para probar el propio script, no para probar una migración.

La [nocturna](../.github/workflows/nightly.yml) lo corre cada noche contra la última
release publicada. No va en la CI de cada push porque depende de que GitHub sirva una
release, y eso no puede bloquear un commit.

---

## Si falla a medias

**No hay «a medias» dentro de una migración.** La migración y su `user_version` se
comitean juntos: si el `.sql` falla en la línea 30, SQLite deshace las 29 anteriores y la
versión no sube. La base queda exactamente como estaba antes de esa migración.

Lo que sí puede quedar a medias es una **serie** de migraciones: si la 0011 va bien y la
0012 falla, la base se queda en 11. El binario aborta el arranque —`store.Open` propaga el
error y `main` sale con código 1— e imprime cuál falló y por qué:

```
error: migración 12 (0012_algo.sql): no such column: foo
```

Qué hacer:

1. **No vuelvas a arrancar el binario nuevo esperando que se arregle.** Va a fallar en la
   misma migración.
2. **Vuelve al binario anterior.** La base está en una versión que él entiende, así que
   arranca y sigue funcionando mientras se investiga.
3. **Si la base quedó tocada** —una migración que reconstruye tablas puede dejarla en un
   estado que el binario viejo tampoco entienda—, restaura el respaldo: el `.db` que
   `POST /api/backup` bajó **antes** de actualizar. Se hace con `VACUUM INTO` y no con un
   `cp` del archivo —en modo WAL, una copia del `.db` a secas se deja fuera parte de los
   datos—, así que se restaura tal cual: para el servicio, sustituye el archivo de
   `SPLITSTREAM_DB_PATH` y arranca.
4. Y guarda el mensaje de error: dice el número de migración y el error de SQLite, que es
   lo único que hace falta para reproducirlo.

> Antes de cualquier actualización, un respaldo. `POST /api/backup` desde el panel, o
> `splitstream -backup /ruta/copia.db` desde la línea de órdenes. Es de esas cosas que solo
> parecen exageradas hasta la vez que no.
>
> Con el binario que ya tienes corriendo, no con el recién descargado: `-backup` abre la
> base *como el servicio*, migraciones incluidas, así que el binario nuevo migraría antes
> de copiar y el respaldo saldría con el esquema del que aún no sabes si funciona.

---

## Compatibilidad hacia atrás

**No la hay, y hay que decirlo claro: una base migrada por una versión nueva no vuelve a
la anterior.** El esquema solo va hacia delante (regla 2) y no existe ninguna migración de
bajada que deshaga lo que la 0012 hizo.

Lo que sí hay es una red: **el binario se niega a abrir una base del futuro.** El runner
solo aplica las migraciones cuya versión supere `user_version`, así que si la base está en
12 y el binario solo conoce hasta 11 no tendría nada que aplicar y —hasta la v1.0— abría la
base tan tranquilo; lo que fallaba era después, y de forma fea: una columna que ya no
existe, un `CHECK` que rechaza un valor que antes valía, una consulta que devuelve lo que
no debe, con el servicio ya emitiendo. Ahora `migrate` compara antes de tocar nada y aborta
el arranque:

```
error: la base es de una versión más nueva de Splitstream (esquema 12 > 11): actualiza el binario o restaura el respaldo
```

Las dos salidas son las que dice el mensaje, y no hay una tercera: volver a poner el
binario nuevo, o restaurar el `.db` de antes de actualizar. La comprobación está en
[`db.go:145`](../internal/store/db.go) y la fija `TestOpenRejectsANewerSchema`
(`internal/store/db_test.go`).

Un detalle que conviene tener claro: esto protege de **hoy en adelante**. Un binario
anterior a la v1.0 no lleva la comprobación, así que bajar a una versión vieja de verdad
sigue abriendo la base sin quejarse. Por eso el respaldo no deja de ser la defensa
principal.

En la práctica, entonces:

- **Bajar de versión = restaurar el respaldo.** No es «desinstalar y poner la anterior»:
  es parar el servicio, poner el binario anterior y poner también el `.db` de antes de
  actualizar. Lo que se haya emitido en medio se pierde, que es justo lo que hace que el
  respaldo sea de *antes* y no de hace un mes.
- **La clave maestra va aparte.** Restaurar la base sin la `SPLITSTREAM_MASTER_KEY` con la
  que se cifró no sirve de nada: el binario lo detecta al arrancar y se niega con «la
  master key no corresponde a esta base de datos». Respalda las dos cosas juntas.
- **Actualizar a lo largo de varias versiones seguidas es seguro.** Las migraciones se
  aplican todas en orden en un solo arranque: de la 0004 a la 0009 no hace falta pasar por
  las versiones intermedias del binario.
