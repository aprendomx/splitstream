# SDD ledger — plan: docs/superpowers/plans/2026-09-02-fase-3-backpressure.md

Spec: docs/superpowers/specs/2026-09-01-rtmp-relay-design.md (autoridad vinculante)
Rama: feat/fase-3-backpressure (desde feat/fase-2-motor-rtmp)
Base: 8d5ae3a — Cierre: eaba24f, ya en `main`

> **Sobre este documento.** Las fases 1 y 2 llevaron su ledger al día durante la ejecución.
> El de la fase 3 no se escribió: la ejecución acabó sin él. Esto es una **reconstrucción
> a posteriori** hecha el 2026-09-02 a partir del historial de commits, más una
> **verificación fresca** de la definición de terminado ejecutada ese mismo día sobre
> `main` en 3ddda9e. Lo que aquí se afirma de las revisiones intermedias sale de los
> mensajes de commit, no de los transcripts de los revisores, que ya no existen: por eso
> este ledger no reproduce los rulings task por task de los anteriores. Lo que sale de una
> ejecución propia va marcado como tal, con su comando.

## Mapa de tasks a commits

| Task | Commits | Qué entró |
| --- | --- | --- |
| T1 Cola acotada con descarte por GOP | 6cc8b1b, 85ddeed, 7a20f77, 12ec1db | Deque acotado por bytes y por duración de media, descarte de GOP completo. Dos rondas de arreglo: la cola no se acotaba cuando la saturaba solo audio, y el límite duro tuvo que partirse en dos niveles para que el descarte de vídeo fuera atómico. |
| T2 Resolución desde el SPS | b327457, d4bdf6c | Parseo del SPS del AVC sequence header. Una ronda: validar `profile_idc` y `level_idc` para rechazar más basura. |
| T3 Backoff y métricas | 51cacc6 | Backoff exponencial 1 s→30 s con jitter ±20 %, métricas por destino. |
| T4 Sink con reconexión | 8bf82e8 | Bucle de reconexión, estados, `degraded`, backpressure. |
| T5 Hub multi-destino | 4ba49b7 | `Snapshot()` de métricas y `Stop` seguro sobre un sink nunca arrancado. |
| T6 Store bajo concurrencia | 38ee342 | El test de contención que la fase 1 pidió (§15.7), resolución y bitrate medido al cerrar sesión. |
| T7 N destinos y apagado | 00a553a | Varios destinos simultáneos, `FCUnpublish` y gracia de apagado. |
| T8 Tests de integración | e7b5ec8, de2b0c9, 52a64d8 | Fan-out a dos sinks y reconexión tras matar uno. Corregido para medir la tasa de A **durante** la caída de B, no el acumulado; y el Makefile asegura los contenedores arrancados. |

## Oleada de revisión final (14:50–16:38)

Trece commits de arreglo posteriores a la T8, con la forma de los hallazgos de una revisión
final de rama. Por orden:

- **Seguridad de claves (3).** `fix(publisher): que ni los errores ni los logs puedan llevar
  la clave`; `fix(config): enmascarar la master key también en JSON`; `fix(main): la clave
  de ingesta tampoco va al log de arranque`. El invariante del spec §8 —las claves no
  aparecen en ningún log, tampoco enmascaradas— se sostenía por costumbre en tres sitios
  donde no estaba forzado.
- **Cola (3).** `deduplicar los esenciales en vez de apilarlos`; `calcular el span sobre la
  media, no sobre los esenciales`; más el test `etiquetar (gop, seq) para detectar frames P
  huérfanos`, que es el que hacía visibles a los dos.
- **Apagado y ciclo de vida (2).** `una sola gracia de 3 s para todo el apagado (spec §6.5)`
  —antes cada sink tenía la suya, así que N destinos multiplicaban la espera— y `Start
  idempotente, como Stop`.
- **App de red del publisher (2).** `la app de red vuelve a ser el path entero; solo el log
  se recorta`, y su corrección `no loguear la app cuando el path no es anidado`. El primer
  arreglo recortaba el path de verdad y rompía los destinos con app anidada.
- **Huecos de test (3).** `cerrar los dos huecos de los tests de la ronda anterior`;
  `cubrir la deduplicación en régimen de producción`; `esperar a que Serve retorne en el
  test de la app anidada`.

## Notas de arrastre: todas pagadas

Las ocho de la tabla del plan, verificadas por lectura del código en 3ddda9e:

| Origen | Dónde quedó |
| --- | --- |
| Fase 1 §15.7 — `-race` no verificaba nada en el store | `internal/store/concurrency_test.go`: 8 escritores × 40 escrituras y 4 lectores |
| Fase 2 T4 — wraparound de 32 bits (~24,8 días) | Decidido explícitamente en `internal/relay/queue.go:232`: un span negativo se trata como 0 |
| Fase 2 T7 — `Stream.Publish` fire-and-forget | `suspectThreshold = 5` en `internal/relay/sink.go:48`: no se deja de reintentar (§6.5), pero se registra un evento de sospecha |
| Fase 2 final — falta `FCUnpublish` | `internal/rtmpio/publisher.go:387`, antes de `deleteStream` |
| Fase 2 final — sin los 3 s de gracia | `relay.ShutdownGrace` en `internal/relay/hub.go:11`, una sola para todo el apagado |
| Fase 2 final — `Hub.Add` se colgaba con un sink nunca arrancado | `TestHubAddNeverStartedSinkDoesNotHang` |
| Fase 2 final — carrera en los `defer` del test de integración | `test/integration/relay_test.go:82`: la base se cierra después de la ingesta |
| Fase 1 §15.2 / Spec §3.8 — resolución y bitrate | `internal/flv/sps.go` y la media móvil de 5 s de `internal/relay/metrics.go` |

## Verificación de la definición de terminado

Ejecutada el 2026-09-02 sobre `main` en 3ddda9e, en una sola pasada. Los once puntos, con
su evidencia:

1. **`go test ./... -race -count=1`** — exit 0. Siete paquetes `ok`, cero `DATA RACE` en la
   salida completa.
2. **`go vet ./...`** — exit 0, sin salida.
3. **`CGO_ENABLED=0 go build ./cmd/splitstream`** — exit 0.
4. **`go.mod`** — tres directas (`go-rtmp v0.0.7`, `golang.org/x/crypto v0.55.0`,
   `modernc.org/sqlite v1.57.0`) y `go 1.25.0`.
5. **`go list -deps ./internal/relay | grep -E 'go-rtmp|database/sql'`** — vacío. El motor
   sigue sin conocer ni la librería RTMP ni la base de datos.
6. **`make sinks-up && make test-integration`** — exit 0 con los tres tests contra
   `mediamtx` real: `TestFanOutToTwoSinks` (12,37 s), `TestReconnectAfterSinkDies` (8,64 s),
   `TestRelayEndToEnd` (8,56 s).
7. **Descarte por GOP completo, `degraded`, sin bloquear a nadie** —
   `TestQueueDropsWholeGOPOnByteOverflow`, `TestQueueHardLimitPreservesGOPIntegrity`,
   `TestQueueResyncsOnNextKeyframe`, `TestSinkDegradesUnderBackpressure`,
   `TestSinkEnqueueNeverBlocks`, `TestHubSlowSinkDoesNotBlockOthers`. En integración,
   `TestReconnectAfterSinkDies` mide la tasa de A mientras B está caído.
8. **Reconexión con backoff de 1 s a 30 s y reenvío del preámbulo** —
   `TestBackoffGrowsExponentiallyAndCaps`, `TestSinkReconnectsAfterConnectFailure`,
   `TestSinkResendsPreambleAfterReconnect`. Observado en la corrida de integración: reintentos
   a 866 ms y 1,81 s tras matar el contenedor de B.
9. **Métricas completas** — `relay.Metrics` lleva `BytesSent`, `BitrateBPS`,
   `DroppedFrames`, `Uptime`, `Reconnections` y `LastError`, más `State`, `Degraded` y el
   estado de la cola. Siete tests en `metrics_test.go`.
10. **Resolución del SPS y bitrate medido** — `TestEngineRecordsResolutionFromSPS` y
    `TestMetricsBitrateOverFiveSecondWindow`. En integración, el log emitió
    `resolución detectada ancho=640 alto=360` con el patrón de ffmpeg de 640×360.
11. **Store bajo concurrencia** — `TestStoreHandlesConcurrentWriters` (8 escritores × 40
    escrituras, 4 lectores) y `TestStoreConcurrentTransactions` (8 transacciones).

## Veredicto

**Fase 3 terminada** en lo que el plan define como terminado: los once puntos verificados
por ejecución, no por lectura. El trabajo está en `main` y etiquetado como `v0.3.0`.

**Con una salvedad que el propio plan levantó y que sigue abierta:** nada se ha probado
contra una plataforma real. Todo lo verde de arriba es contra `mediamtx` local. El spec §14
avisa de que cada plataforma tiene su dialecto de handshake, y en particular de que mandamos
`releaseStream`/`FCPublish` sobre el stream ya creado y con `TransactionID: 0`, mientras FMLE
—el cliente que las plataformas esperan— los manda sobre el stream 0 y antes de
`createStream`. Ese riesgo no se cierra con tests locales: hace falta una clave real de
YouTube, Twitch y Facebook. Queda como la primera tarea pendiente antes de dar la fase por
cerrada de cara al uso.

## Deuda que la fase 3 deja abierta

- Las cuatro ramas de fase (`feat/fase-1-esqueleto`, `feat/fase-2-motor-rtmp`,
  `feat/fase-3-backpressure`) están contenidas enteras en `main` y ya no aportan nada.
- No hay integración continua: ningún `push` ejecuta la suite ni el `-race`. La fase 3 es la
  primera cuya verificación cuesta minutos y depende de Docker; sin CI, cada regresión se
  descubre a mano.
- Lo listado en «Notas para la fase 4» del plan sigue tal cual: tags `json` de `Metrics`
  (§15.2), taxonomía de errores del store (§15.3), orden lexicográfico de los timestamps
  (§15.4), auditoría del revelado de claves (§15.5) y el nombre de `store.GenerateKey()`
  (§15.8).
