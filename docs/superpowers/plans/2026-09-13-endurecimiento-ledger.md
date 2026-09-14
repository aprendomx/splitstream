# SDD ledger — plan: docs/superpowers/plans/2026-09-13-endurecimiento.md

Spec: docs/superpowers/specs/2026-09-13-endurecimiento-design.md (autoridad vinculante), sobre el spec base (§15.9, §16) y el plan maestro §9–§10.
Rama: feat/endurecimiento (desde main @ 500c74c, merge de la v0.13; v0.13.0 etiquetada ahí, release 9/9 en verde). Commits previos: db80d59 (spec), plan (ver git log).
Ejecución: 2026-09-13, subagent-driven, un implementador por tarea + revisión por tarea.

Ruling: se trabaja en el checkout actual sobre la rama, sin worktree (como en v0.8–v0.13) — coste si es erróneo: ninguno mientras no se toque main.
Ruling: contingencia de go-rtmp como copia parcheada en `third_party/go-rtmp` con `replace` (mismo módulo), no como fork público: sin repos nuevos, parches aislados en `patches/*.diff` listos para aguas arriba; proponerlos allí queda para la persona que fusiona — coste si es erróneo: si un día se quiere fork, es `git subtree`/copiar y cambiar el `replace`.
Ruling: `PreCommands` (releaseStream/FCPublish por el stream de control) apagado por defecto: solo la puerta contra plataformas reales puede encenderlo — coste si es erróneo: ninguno (comportamiento actual intacto).
Ruling: `WriteTimeout` de la copia 0 → 5 s (compatibilidad); Splitstream fija 3 s — coste si es erróneo: un destino lento se da por perdido 2 s antes; el backoff lo recupera.
Ruling: las versiones de `golangci-lint` y `govulncheck` las fija el implementador con `@latest version` (el plan no tiene red) y quedan fijadas en Makefile y workflows — coste si es erróneo: ninguno.
Ruling: el gancho de subida de grabaciones queda documentado (spec base §13), sin código — coste si es erróneo: ninguno.

## Preflight (tabla de conflictos)

| Par / tarea | Produce → consume | Hallazgo |
| --- | --- | --- |
| T1 → T2, T3, T4 | `third_party/go-rtmp`, `patches/generar.sh`, `UPSTREAM.md`, test de integridad | Las tres tareas añaden un `.diff` y una fila; el test de T1 exige que cada archivo distinto tenga parche. `generar.sh` acepta varios archivos (T2 toca `conn.go` y `stream.go`). |
| T1 texto | `replace` sin `go mod tidy`; `go.sum` intacto | Las dependencias de la copia (`go-amf0`, `logrus`, `pkg/errors`) ya están en `go.sum`; `testify` solo para los tests de la copia, en su propio `go.sum`. |
| T2 texto | `s.conn.config.WriteTimeout` en `Stream.Write` | `newConn(rwc, config)` guarda `config` normalizada; el implementador confirma el nombre del campo. |
| T4 ↔ T6 | `SPLITSTREAM_RTMP_PRECOMMANDS` no la usa la nocturna | Correcto: el humo prueba el comportamiento por defecto. |
| T5 texto | Excepciones de `errcheck` por firma | Si una firma no casa, el linter lo dice; se ajusta. |
| T6 ↔ T8 | `nightly.yml` llama a `deploy/migrate-test.sh` (T8) | El plan ejecuta T8 después de T6 pero antes de que la nocturna corra de verdad (la valida el controlador con `workflow_dispatch` tras el push, cuando T8 ya existe). |
| T6 texto | `secrets.*` en `if:` | Alternativa con `env` documentada. |
| T7 texto | 11 públicas + 46 protegidas = 57 rutas; `GET /metrics` con `requireSessionOrToken` | `ruta.Envoltorio` opcional. `TestRutasPublicasSonLasDeSiempre` fija la lista. |
| T7 ↔ dto_test | `dtosDocumentados` compartida por dos tests del mismo paquete | OK (mismo paquete de test). |
| T8 texto | assets `splitstream-<ver>-<so>-<arq>.tar.gz` | Nombres confirmados en la release v0.12.0/v0.13.0. |
| Puerta | Lanzar la nocturna con `workflow_dispatch` y verla en verde; `migrate-test.sh v0.13.0` contra la release real; decidir si se encienden `PreCommands` en alguna plataforma | Del controlador (nocturna y migrate-test tras el push) y del usuario (PreCommands). |

## Progreso
BASE T1: e801472
Task 1: complete (commits e801472..0f2c44d, review clean por el controlador)
BASE T2: 0f2c44d
Task 2: complete (commits 0f2c44d..a6a7f5c, review clean por el controlador; 5,0 s → 3,0 s medido)
BASE T3: a6a7f5c
Task 3: complete (commits a6a7f5c..cc44f2e, review clean por el controlador). Nota: `third_party/go-rtmp/message` tiene un flake de upstream con -count≥2 (TestDecodeCommon); la CI corre -count=1 sobre la copia; no es nuestro y no se toca — coste si es erróneo: ninguno.
BASE T4: cc44f2e
Task 4: implementada (cc44f2e..bd2a080). Ruling: parche 4 (chunkSize de Stream a atomic.Uint32; carrera de upstream destapada por PreCommands) aceptado; su hunk vive en 0001-write-timeout.diff porque el test de integridad admite un diff por archivo y está anotado en UPSTREAM.md filas 1 y 4; la Task 9 documenta cuatro parches — coste si es erróneo: ninguno.
Task 4: review Spec ✅ / WITH FIXES — _result a transacción 0 tira la conexión (stream_handler.At); el Store de chunkSize en CreateStream puede adelantarse al FCPublish encolado (>128 B); parseBool con tercera semántica; docs pendientes (README fila, «cuatro parches»). Fix round 1 (resume implementer, opus): parche 5 tolerar _result/_error de transacción desconocida; parche 4 ampliado: chunkSize se aplica en la goroutine escritora tras escribir SetChunkSize; parseBool == "true"; comentarios corregidos. Docs → Task 9.
Task 4 fix round 1: commit 1902d4b (cerrado por el controlador: el implementador se quedó parado tras dejar el trabajo completo y verificado).
Task 4 fix round 1 → re-review WITH FINDINGS: TestStreamerAppliesTheNewChunkSizeOnlyAfterWritingSetChunkSize flaky ≈3 % (entrelazado no fijado); fila 1 de UPSTREAM.md imprecisa; falta test de _error con transacción conocida. Fix round 2 (implementador nuevo: el anterior se quedó parado).
Task 4: complete (commits cc44f2e..1d92af4, fix rounds 1–2 → re-review CLEAN por el controlador: 0/300 fallos con -race; cinco parches en la copia)
BASE T5: 1d92af4
Task 5: implementada (1d92af4..a1d833e). Rulings: misspell acotado con path-except a i18n_en.go (el vocabulario español no tiene lista finita); errcheck por tipos concretos; «cancelled» se queda (es también la etapa del sondeo). Diferible a la ola final: formatters gofmt en .golangci.yml + arreglar password_test.go y sps.go (preexistentes).
Task 5: review Spec ✅ / WITH FIXES — Close del archivo de la clave maestra tapado por la exclusión de errcheck (config.go:202); recuento de gosec del informe incorrecto. Fix round 1 (resume implementer).
Task 5: complete (commits 1d92af4..cd500ac, fix round 1 → re-review CLEAN por el controlador). Ruling: G710 (redirect HTTP→HTTPS con r.Host) aceptado como excepción: mismo patrón que autocert.HTTPHandler, esquema fijo, net/http rechaza Host malformado antes del handler — coste si es erróneo: redirección a un host que el propio cliente mandó.
BASE T6: cd500ac
Task 6: implementada (cd500ac..346f6f0). Rulings: el humo exige `live` en `.metrics.state`; la clave de ingesta por `POST /api/ingest/rotate-key`; secretos por archivo con umask 077 y log filtrado; HAY_* en env del job — coste si es erróneo: ninguno.
Task 6: review Spec ✅ / WITH FIXES — Critical: STREAM_KEY por --arg de jq (argv); dependabot groups.dependency-type: indirect inválido. Fix round 1 (resume implementer).
Task 6: complete (commits cd500ac..8dcb77a, fix round 1 → re-review CLEAN por el controlador)
BASE T7: 8dcb77a
Task 7: implementada (8dcb77a..063f2c8). Rulings: 54 rutas reales (7 públicas, 46 protegidas, /metrics con envoltorio propio) — el spec §5.1 decía 57/11: la Task 9 corrige la cifra; /api/status y /api/events en el grupo live; DTO ampliados a los cuerpos sin sufijo — coste si es erróneo: ninguno.
Task 7: complete (commits 8dcb77a..063f2c8, review APPROVED; diferible a la Task 9: spec §5.1 con el orden real de grupos incluido ws)
BASE T8: 063f2c8
Task 8: implementada (063f2c8..e4c2f9c; probada contra v0.13.0, v0.11.0 y v0.7.0 reales). Rulings: sin deploy/lib.sh (la auditoría de secretos del humo lee el texto del script); evidencia de migración por user_version del encabezado del .db. Ola final: db.go rechaza user_version > SchemaVersion con error claro + test; log Info por migración aplicada (el script ya busca «migraci»).
Task 8: complete (commits 063f2c8..e4c2f9c, review APPROVED)
BASE T9: e4c2f9c
Task 9: implementada (e4c2f9c..2c376a9; commit enmendado solo por el trailer). Revisión de docs y revisión final de rama despachadas en paralelo.
Task 9: complete (commits e4c2f9c..2c376a9, review Spec ✅ / WITH FIXES solo diferible: referencia §3.4→§3.6 en el spec → ola final)
Revisión final de rama: despachada (opus) sobre e801472..2c376a9 (13 commits).
Revisión final: WITH FIXES — Important: parche 5 demasiado ancho (tolera _error al publish por el stream de datos → destino escribiendo al vacío); menores listados. Ola final única despachada (opus) con final-fix-brief.md (A.1–A.3, 4–14; B.5 incluido; 15 diferido). BASE ola final: 2c376a9
Ola final: implementada (2c376a9..33fd58a, 6 commits, 14/14 puntos + B.5). Rulings: deleteStream no se manda (comentario con el motivo vigente); seam `crearArchivoDeClave` para probar el fallo de escritura de la clave maestra; §7 del spec rebajado a lo que hay. Re-revisión acotada despachada.
Ola final: re-review CLEAN salvo un nit diferible (sleep 200 ms en servidorRTMPQueNoDrena, publisher_test.go:556). Rama lista para PR.
