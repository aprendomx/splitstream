# Splitstream — v1.0 «Endurecimiento»

**Fecha:** 2026-09-13
**Estado:** aprobado por el plan maestro (§9, tareas G.1–G.3; §10 gancho de subida); pendiente de plan de implementación
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (§16: spike de `go-rtmp`; §15.9: `releaseStream`/`FCPublish`)
**Spec previo:** `docs/superpowers/specs/2026-09-12-internacional-design.md` (v0.13)
**Roadmap:** `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` §7 («v1.0 — Endurecimiento»)
**Plan maestro:** `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §9 y §10
**Versión de partida:** `v0.13.0` (`main` @ `500c74c`)

## 1. Qué se construye

La última entrega del roadmap no añade funciones: quita riesgo. Tres cosas:

1. **Contingencia de `go-rtmp`**: las cinco deudas conocidas de `github.com/yutopp/go-rtmp v0.0.7` (spec base §16.2 y §15.9, más dos carreras que aparecieron al implementar `PreCommands`: el tamaño de chunk y las respuestas huérfanas) se arreglan en una copia parcheada que vive en este repo (`third_party/go-rtmp`) y entra por `replace` en `go.mod`, con un parche por deuda, cada uno con su test que falla sin él, y listos para proponer aguas arriba.
2. **CI estricta**: `golangci-lint` con una configuración justificada, `govulncheck`, una integración nocturna completa (con prueba opcional contra plataformas reales si hay claves) y Dependabot para Go, npm y Actions.
3. **Contrato congelado**: `docs/api.md` generado por un test a partir de la tabla de rutas y de los DTO (falla si el documento no coincide), `docs/migraciones.md` con la política de migraciones y un script que prueba la migración desde la release anterior, y la regla de versionado: `/api/` es la v1 implícita y cualquier ruptura exige `/api/v2/`.

Lo que NO cambia: funciones del panel, modelo de datos (`SchemaVersion` 9; ninguna migración), dependencias directas (siguen siendo cinco: el `replace` apunta al mismo módulo), textos (ninguna clave nueva de i18n salvo las que un mensaje de error nuevo exija). La versión resultante es `v1.0.0`.

## 2. Enmiendas al spec base

- §5 (Dependencias): `github.com/yutopp/go-rtmp` sigue siendo dependencia directa, pero el código que compila es `third_party/go-rtmp` (copia de v0.0.7 + los parches de §3) por `replace`. La copia conserva `LICENCE.txt` (MIT) y lleva `UPSTREAM.md` (versión y commit de origen, lista de parches, cómo regenerarla) y `patches/*.diff` (un diff por parche, aplicables sobre la v0.0.7 limpia). `go mod verify` no cubre el `replace`: lo cubre un test (§3.4).
- §11 (Pruebas): la CI gana `lint` y `vuln` como jobs obligatorios y un workflow nocturno. Las excepciones del linter se justifican en `.golangci.yml`, una por una.
- §9 (API): el contrato queda documentado en `docs/api.md`, generado; el prefijo `/api/` es la v1. Añadir rutas o campos es compatible; quitar o renombrar exige `/api/v2/` y una decisión escrita.
- §7 (Modelo de datos): la política de migraciones pasa a `docs/migraciones.md` y a un script ejecutable (`deploy/migrate-test.sh`) que corre en la integración nocturna.
- §12 (Operación): ninguna variable nueva obligatoria. Opcional: `SPLITSTREAM_RTMP_PRECOMMANDS` (por defecto apagado; §3.3).
- §13 (Fases) y §10 del plan maestro: el gancho de subida de grabaciones al terminar la sesión queda **documentado como punto de extensión** (dónde engancharía y con qué contrato), sin código: un job vacío sería ruido en el registro de mantenimiento.

## 3. Contingencia de `go-rtmp` (`third_party/go-rtmp`)

### 3.1 Parche 1 — `Stream.Write` con plazo configurable

`Stream.Write` fija `context.WithTimeout(context.Background(), 5*time.Second)` (`stream.go:321`, con el `// TODO: Fix 5s` del propio autor). Parche: `ConnConfig.WriteTimeout time.Duration` (0 → 5 s, para no cambiar el comportamiento de nadie) y un `Stream.WriteContext(ctx, chunkStreamID, timestamp, msg)` que respeta el `ctx` del llamante; `Write` pasa a llamar a `WriteContext` con el plazo de la configuración. `internal/rtmpio/publisher.go` fija `WriteTimeout` a `writeTimeout` (constante nueva, 3 s: una conexión que no drena 3 s ya perdió más de un GOP; el backoff del §6.5 arranca antes) y sigue tratando el error de plazo como conexión perdida.

Test (en `internal/rtmpio`, con un `net.Conn` de prueba que no drena): sin el parche el `Write` tarda ≥ 5 s; con él, ≤ 3 s + margen. El test se salta si `WriteTimeout` no existe (build tag no: se comprueba en tiempo de compilación; el test simplemente falla sin el parche, que es lo que se quiere).

### 3.2 Parche 2 — `streams.At` bajo el candado

`streams.At` lee el mapa sin tomar `ss.m` mientras `Create`/`Delete` escriben con él (`streams.go:85`): carrera real con `-race` cuando un `Delete` de la conexión que cierra coincide con un mensaje entrante. Parche: `At` toma `ss.m` (`RLock` si se cambia `m` a `sync.RWMutex`; el resto de métodos siguen igual). Test en la copia (`streams_test.go`): `Create` + `At`/`Delete` concurrentes bajo `-race`, que falla sin el parche.

### 3.3 Parche 3 — el stream de control accesible

`ClientConn` guarda el stream de control (id 0) solo como `controlStreamWriter` (`client_conn.go:44`); no hay forma de mandarle `releaseStream`/`FCPublish`, que es donde OBS y ffmpeg los mandan. En la fase 4 se quitaron porque iban por el stream equivocado y rompían Twitch (spec base §15.9). Parche: `func (cc *ClientConn) ControlStream() *Stream`. En `internal/rtmpio/publisher.go`, detrás de `Options.PreCommands bool` (cableado a `SPLITSTREAM_RTMP_PRECOMMANDS`, por defecto **apagado**): antes de `createStream`, mandar `releaseStream(name)` y `FCPublish(name)` por el stream de control, y `FCUnpublish(name)` al cerrar. Apagado por defecto porque solo la puerta contra plataformas reales puede decir si conviene encenderlo; el manual lo documenta como «solo si una plataforma lo pide».

Test: con `PreCommands` el servidor de prueba (`mediamtx` en integración; en unidad, el `ingest` propio de `rtmpio`, que ya registra comandos) recibe `releaseStream`/`FCPublish` por el stream 0 antes de `createStream`; sin la opción, no llegan.

### 3.4 Parche 4 — el tamaño de chunk se aplica tras escribir `SetChunkSize`

Cambiar el tamaño de chunk de salida tenía dos fallos, los dos alcanzables en cuanto algo escribe por el stream de control sin esperar respuesta — justo lo que hace `PreCommands` con `releaseStream`/`FCPublish` (§3.3). (a) Carrera de datos: `Stream.CreateStream` escribía `selfState.chunkSize` mientras la goroutine del planificador lo leía en cada chunk. (b) Carrera de orden, la grave: lo escribía al encolar el `SetChunkSize`, así que un mensaje encolado un instante antes y todavía en el planificador podía salir troceado con un tamaño que el otro extremo aún no conocía. Parche: `chunkSize` pasa a `atomic.Uint32`, toda lectura va por `ChunkSize()`, y el tamaño nuevo lo aplica la goroutine escritora en `writeChunk` justo después de escribir el `SetChunkSize` en el hilo.

Test: `TestStreamerAppliesTheNewChunkSizeOnlyAfterWritingSetChunkSize` (en la copia) y `TestPreCommandsGoThroughTheControlStreamBeforeCreateStream` corrido con `-race` (`internal/rtmpio`), que falla sin el parche.

### 3.5 Parche 5 — una respuesta huérfana no tira la conexión

Un `_result`/`_error` para una transacción que este lado no registró tiraba toda la conexión: `handleCommand` devolvía error, y `runHandleMessageLoop` no lo distingue de uno de verdad. Pasa de verdad con `PreCommands`: `releaseStream` y `FCPublish` se mandan sin esperar respuesta, con `TransactionID 0`, y hay plataformas que contestan igual. Parche: la respuesta huérfana se registra a nivel debug y se ignora en vez de cerrar la conexión.

Test: `TestStreamHandlerIgnoresAResponseToAnUnknownTransaction` (en la copia), que falla sin el parche.

### 3.6 Integridad de la copia

`third_party/go-rtmp/UPSTREAM.md` fija `v0.0.7` (`h1:` del `go.sum` original) y lista los cinco parches. Un test en `internal/rtmpio` (`TestGoRTMPCopyMatchesUpstreamPlusPatches`) hace `go mod download -json github.com/yutopp/go-rtmp@v0.0.7` **solo si** el módulo ya está en la caché local (`go env GOMODCACHE`; si no está, el test se salta con un motivo claro: ningún test toca la red) y comprueba que `diff -ru` entre la caché y la copia produce exactamente los `patches/*.diff` (mismos archivos tocados, mismas líneas). Así nadie edita la copia sin dejar el parche. `go vet` y `golangci-lint` excluyen `third_party/` (código ajeno; solo se lintan los parches por revisión).

## 4. CI estricta

### 4.1 Linter

`.golangci.yml` (formato v2 de golangci-lint, versión fijada en el workflow por `version:` de la action, la misma que en `Makefile lint`): `errcheck`, `govet`, `staticcheck`, `gosec`, `revive`, `unused`, `ineffassign`, `misspell` (locale `US`, ignorando palabras en español por lista), `gocritic` (diagnóstico). Excepciones, cada una con un comentario de por qué: `errcheck` en `Close()`/`Flush()` de mejor esfuerzo y en `LogEvent` (fuego y olvido por diseño, spec base §6); `gosec` G304 (rutas de archivos que vienen de la configuración) y G404 (`math/rand` para jitter, no para secretos); `revive` con `exported` apagado para los comentarios en español y `var-naming` tolerando `ID`/`URL` como ya se usan. `third_party/` y `web/` excluidos. Si el primer barrido supera lo razonable en una sola entrega (el plan mide), se corrige lo real y lo demás se anota como excepción **temporal** con fecha, nunca en silencio.

### 4.2 Vulnerabilidades

Job `vuln`: `govulncheck ./...` (versión fijada con `go run golang.org/x/vuln/cmd/govulncheck@vX` en el workflow; no entra en `go.mod`). Falla el PR si hay una vulnerabilidad alcanzable; las no alcanzables se listan en el log.

### 4.3 Nocturna

`.github/workflows/nightly.yml` con `schedule: '0 4 * * *'` y `workflow_dispatch`: (1) la integración completa contra `mediamtx` (`make test-integration`, la misma del job `docker` de la CI, sin recortes de tiempo); (2) `deploy/migrate-test.sh` desde la última release publicada (§5.3); (3) si existen los secretos `TWITCH_TEST_KEY`, `YOUTUBE_TEST_KEY` o `KICK_TEST_KEY`, un humo de 5 minutos contra la plataforma real con el binario recién compilado: publicar un patrón de prueba (generado con `ffmpeg` desde `testsrc`) y afirmar que el destino llega a `connected` y se mantiene sin `destination_disconnected`; sin secretos, el paso se salta con un aviso. Los secretos nunca se imprimen; el paso corre con `set +x`.

### 4.4 Dependabot

`.github/dependabot.yml`: `gomod` (semanal; agrupa los indirectos), `npm` en `web/` (semanal; solo `devDependencies` y parches/menores; Quasar/Vue mayores a mano), `github-actions` (semanal). Las actualizaciones de `go-rtmp` aguas arriba no aplican mientras haya `replace`: Dependabot las propondrá igual y `UPSTREAM.md` explica cómo rebasar los parches.

## 5. Contrato de API y migraciones

### 5.1 `docs/api.md` generado

`internal/httpapi/server.go`: `routes()` deja de registrar a mano y recorre una tabla `var rutas = []ruta{{Metodo, Patron, Handler, Publica, Resumen}}` (las 54 rutas: 7 públicas, 46 protegidas y `/metrics` con sesión o token, sin cambiar ninguna); `protegida` sigue existiendo como envoltorio que la tabla aplica. `TestAPIContractDocIsCurrent` (`api_doc_test.go`) genera el Markdown: cabecera con la regla de versionado (§5.4), una tabla por grupo, en este orden — `auth`, `setup`, `health`, `metrics`, `ingest`, `destinations`, `live`, `platforms`, `accounts`, `sessions`, `recordings`, `chat`, `webhooks`, `backup`, `ws` — con método, ruta, «sesión» (no / sí / sí (o token)) y resumen, y una sección «Formas» con los 44 DTO exportados por reflexión (`reflect` sobre la lista que ya usa `TestDTOFieldNamesAreSnakeCase`, ampliada a los DTO de petición): nombre del campo JSON, tipo Go legible y si es puntero (opcional). El test compara byte a byte con `docs/api.md`; con `-update` (flag del paquete de test) lo reescribe. Falla en CI si alguien cambia una ruta o un DTO sin regenerar.

### 5.2 `docs/migraciones.md`

La política, en español y con ejemplos: nunca editar una migración ya publicada (se añade otra); solo hacia delante (sin `down`); cada migración idempotente con `IF NOT EXISTS` donde aplique; claves ajenas apagadas por el runner durante la migración y reactivadas después (ya es así: `db.go:151/176`); `SchemaVersion` debe igualar la última migración (ya lo comprueba el runner); cómo probar contra una base real de la versión anterior (§5.3); qué hacer si una migración falla a medias (SQLite: transacción por migración; el runner la aborta y el binario no arranca, con el mensaje de qué versión esperaba).

### 5.3 `deploy/migrate-test.sh`

Bash (con `shellcheck` limpio como `install.sh`): recibe la versión anterior (`v0.13.0` por defecto: la última etiqueta que no sea la actual) y el binario nuevo (`./bin/splitstream` o el que se le pase); descarga el `tar.gz` de la release anterior para el sistema actual (los nombres de asset ya existen: `splitstream-<ver>-linux-x86_64.tar.gz`, etc.), lo arranca con `SPLITSTREAM_DB_PATH` en un directorio temporal y `SPLITSTREAM_HTTP_ADDR`/`SPLITSTREAM_RTMP_ADDR` en puertos libres, espera `/healthz`, lo para con SIGTERM (crea la base con su esquema), arranca el binario nuevo sobre la misma base, espera `/healthz` y comprueba en el log que la migración aplicó las versiones que faltaban (o que no había ninguna, si el esquema no cambió) y que el proceso para limpio. Sale con 0/1 y deja el log en el temporal si falla. Corre en la nocturna (§4.3) y a mano antes de cada release.

### 5.4 Versionado de la API

En `docs/api.md` y en el spec base §9: `/api/` es la **v1**. Compatible: añadir rutas, añadir campos (siempre con valor por defecto), añadir valores a un enumerado si el panel los tolera. Incompatible: quitar o renombrar rutas o campos, cambiar tipos, cambiar el significado de un `code`. Lo incompatible exige `/api/v2/` conviviendo con `/api/` al menos una versión menor, y una decisión escrita en el spec.

## 6. Gancho de subida de grabaciones (documentado, sin código)

`docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` §13 gana un párrafo «Punto de extensión: subida al terminar»: el sitio es el cierre de sesión en `internal/record` (donde se cierra el último segmento) o un job de `internal/maintenance` que recorra grabaciones cerradas sin marca `uploaded_at`; el contrato mínimo (`Uploader.Upload(ctx, path) (url string, err error)`, configuración por `SPLITSTREAM_UPLOAD_*`, evento `recording_uploaded`, columna nueva → migración 0010). Entra cuando alguien lo pida; hasta entonces, nada.

## 7. Pruebas

- **go-rtmp**: los cinco tests de §3 (dos en `internal/rtmpio`, tres en la copia) que fallan sin su parche; `TestGoRTMPCopyMatchesUpstreamPlusPatches`; la integración contra `mediamtx` sigue en verde con y sin `PreCommands`.
- **CI**: `lint`, `vuln`, `test`, `web`, `docker` en verde en el PR; `nightly.yml` validado con `workflow_dispatch` una vez antes de fusionar (el controlador lo lanza tras el push y espera el resultado).
- **API**: `TestAPIContractDocIsCurrent` en verde y `docs/api.md` regenerado; `TestDTOFieldNamesAreSnakeCase` sigue.
- **Migraciones**: `deploy/migrate-test.sh` con `v0.13.0` → binario actual, en la nocturna y en local (el controlador lo ejecuta antes de abrir el PR).
- Sin dependencias nuevas en `go.mod` (el `replace` no añade módulos); `web/package.json` sin cambios.

## 8. Fuera de esta entrega

Proponer los parches aguas arriba (PRs en `yutopp/go-rtmp`): lo hace la persona que fusiona desde su propio fork, con los `patches/*.diff` listos; sustituir `go-rtmp` por otra implementación; subida real de grabaciones; `golangci-lint` sobre `third_party/`; manual en inglés.
