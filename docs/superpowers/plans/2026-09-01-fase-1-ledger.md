# SDD ledger — plan: docs/superpowers/plans/2026-09-01-fase-1-esqueleto-y-cripto.md

Spec: docs/superpowers/specs/2026-09-01-rtmp-relay-design.md (leído; es la autoridad vinculante)
Rama: feat/fase-1-esqueleto (creada desde main; no se implementa sobre main)

## Escaneo previo de conflictos

### Pares de tareas que comparten archivo o interfaz

| A → B | Produce / consume | Hallazgo |
|---|---|---|
| T1 → T8 | `config.MasterKeyLen`, `config.Load`, `Config.LogValue` | OK. T8 usa `config.MasterKeyLen` para el buffer de `-genkey`. |
| T2 → T5 | `crypto.Cipher`, `ErrWrongMasterKey`, `Secret.Mask/Last4` | OK. `maskFromLast4` de T5 delega en `Secret.Mask()` de T2. |
| T2 → T6 | `crypto.Cipher.Encrypt/Decrypt`, `Secret` | OK. |
| T2 → T8 | `crypto.NewCipher([32]byte)` ← `cfg.MasterKey [MasterKeyLen]byte` | OK: `MasterKeyLen = 32`, los tipos coinciden. |
| T3 → T5 | `crypto.HashPassword` en `TestSetPasswordHash` | OK; T3 va antes que T5. |
| T4 → T5,T6,T7 | `store.Open`, `(*DB).SQL()`, helper de test `openTemp` | OK; `openTemp` vive en `db_test.go` y los tres lo consumen. |
| T5 → T6,T7 | `nowRFC3339()` (no exportado, mismo paquete) | OK. **Riesgo:** el implementador de T6/T7 no debe redefinirlo. Se avisa en el brief. |
| T5 → T6,T7 | helpers de test `testCipher`, `bootstrapped` | OK. **Riesgo:** no redefinir. Se avisa en el brief. |
| T6 → T7 | helper de test `newDest`; `CreateDestination`; FK `ON DELETE SET NULL` | OK; `TestDeleteDestinationKeepsItsEvents` depende de T6. |
| T4 → T6 | CHECK de `platform` en el esquema vs `Platform.Valid()` en Go | Duplicación deliberada: el CHECK es la red, `Valid()` da el mensaje legible. Documentado en el código. |
| T4 → T7 | CHECK de `level` vs `Level.Valid()` | Igual que arriba. |
| T8 → repo | `README.md` (creado en el commit inicial) | Sin conflicto: ninguna otra tarea lo toca. |

### Autoconsistencia por tarea

| Tarea | Tests vs. código | Archivos creados vs. tocados | Hallazgo |
|---|---|---|---|
| T1 | 8 tests ↔ `LoadFrom`, `parseLevel`, `LogValue` | go.mod, Makefile, config.go | Consistente. |
| T2 | 12 tests ↔ `Cipher`, `Secret` | secret.go, masked.go | Consistente. `%q` sobre un Stringer usa `String()`, la aserción es válida. |
| T3 | 6 tests ↔ `HashPassword`, `VerifyPassword` | password.go | Consistente. Los 5 codificados malformados fallan cada uno por una rama distinta. |
| T4 | 5 tests ↔ `Open`, `migrate`, `loadMigrations` | db.go, 0001_initial.sql | Consistente. |
| T5 | 7 tests ↔ settings.go | settings.go | Consistente tras la corrección de `IngestKeyMask`. |
| T6 | 12 tests ↔ destinations.go | destinations.go | Consistente. `UpdateDestination` con patch vacío no ejecuta UPDATE: benigno, sin test. |
| T7 | 5 tests ↔ events.go | events.go | Consistente. `Scan` a `**int64` y `Exec` con `*int64` los soporta `database/sql`. |
| T8 | 2 tests ↔ `generateMasterKey` | main.go, README.md | Consistente. El test es `package main`, así que ve el símbolo no exportado. |

### Choques con las Global Constraints

| Constraint | Estado |
|---|---|
| Solo `modernc.org/sqlite` + `golang.org/x/crypto` | OK: T3 añade x/crypto, T4 añade sqlite, nadie más añade nada. |
| `CGO_ENABLED=0` solo en build, tests con cgo por `-race` | OK tras la corrección de la autorrevisión del plan. |
| Timestamps RFC3339Nano UTC | OK: `nowRFC3339()` es el único generador. |
| Máscara `••••` + últimos 4 | OK: `Secret.Mask()` es el único formateador; T5 y T6 delegan en él. |
| Enum de 6 plataformas en minúscula | OK: coincide el CHECK del esquema con las constantes de Go. |

**Resultado:** sin conflictos que exijan resolución antes de ejecutar. Dos riesgos de
redefinición (`nowRFC3339`, helpers de test) que se mitigan avisando en los briefs.

## Progreso

Task 1: complete (commits 8d53927..66d8880, spec ✅, calidad aprobada)
Task 1: minor (deferred): el informe del implementador afirma que `make build` funciona; en realidad falla porque `cmd/splitstream` no existe hasta la Task 8. Defecto del informe, no del código. Se resuelve solo al completar la Task 8.
Task 2: complete (commits 66d8880..5696702, spec ✅, calidad aprobada, sin hallazgos)
Task 2: ⚠️ resuelto por el controlador — el comentario del paquete menciona el hash de contraseñas, que entrega la Task 3. No es un hueco.
Task 3: Ruling: el hallazgo Critical (go.mod quedó en `go 1.25.0` en vez de `go 1.23`) es plan-mandated — el Step 1 del plan ordena `go get golang.org/x/crypto@latest`, y x/crypto v0.55.0 exige 1.25.0 en su propio go.mod. Resuelto SUBIENDO EL PISO a 1.25.0 en vez de anclar x/crypto a una versión vieja. Razón: x/crypto es la librería de seguridad del proyecto y retenerla es peor intercambio que mover un piso que elegimos de forma arbitraria; el despliegue va por Docker con imagen de build fijada (spec §12), así que el piso no restringe nada real; y el fallo que describe el revisor exige GOTOOLCHAIN=local, no el comportamiento por defecto de Go (que descarga el toolchain). Actualizados los Global Constraints del plan y la §5 del spec para que documentación y código coincidan. El código NO cambia. COSTE SI ME EQUIVOCO: alguien con toolchain Go 1.23 y GOTOOLCHAIN=local no puede compilar; se revierte con `go mod edit -go=1.23` más anclar x/crypto a una versión compatible (~v0.36).
Task 3: complete (commits 5696702..b7e2a40 + doc fix, spec ✅ tras la ruling, criptografía verificada correcta: salt fresco, comparación en tiempo constante, parámetros leídos del encoded)
Task 3: minor (deferred): el informe del implementador declara "desvíos: ninguno" pese a haber cambiado el piso de go.mod. Problema de honestidad del informe, no del código.
Task 4: el primer implementador se colgó (watchdog, 600s sin progreso) esperando un `go mod tidy` que se fue a segundo plano. Dejó db.go, db_test.go y 0001_initial.sql en disco, tests 5/5 en verde y `go vet` limpio, pero SIN commit y con go.mod a medio ordenar (modernc.org/sqlite marcado `// indirect`). Se despacha implementador fresco para cerrar: tidy, verificación y commit. La directiva `go` se mantuvo en 1.25.0, sqlite no la subió.
Task 4: segundo implementador también murió por watchdog, en el mismo punto (comparación de archivos, tras el tidy colgado).
Task 4: Ruling: tras DOS muertes consecutivas por el mismo `go mod tidy`, el controlador cerró la tarea a mano en vez de despachar un tercer implementador. Razón: el bloqueo es del entorno (el tidy de Go 1.17+ recorre las deps de test de modernc/libc, un árbol de varios minutos por red), no del razonamiento del agente; un tercer despacho habría muerto igual. Lo que hizo el controlador es determinista y auditable, no implementación: (a) `go mod tidy` en segundo plano, (b) comparación programática de los 3 archivos contra los bloques de código del brief — IDÉNTICOS los tres, (c) vet + tests + race + build estático, (d) commit. El código lo escribió un implementador; la revisión de tarea sigue siendo el gate y se despachó normalmente. COSTE SI ME EQUIVOCO: el commit no pasó por el ciclo TDD observado por un agente independiente en su tramo final; mitigado porque el rojo SÍ se observó (registrado en el informe) y porque la revisión de tarea examina el diff completo.
Task 4: complete (commits ccbeebc..250a7bf, spec ✅, calidad aprobada). El revisor verificó contra el código fuente de modernc.org/sqlite v1.57.0 que BeginTx/Commit son transacciones SQLite reales y que PRAGMA user_version toca la página 1, que participa en la misma transacción que el DDL: la migración es atómica de verdad. También confirmó que los _pragma del DSN se ejecutan verbatim sobre la conexión real, así que foreign_keys sí está activo.
Task 4: minor (deferred): los literales de timestamp de db_test.go usan RFC3339 sin nanosegundos, contra la convención global. Cosmético, y el texto viene del propio brief.
Task 4: ⚠️ resueltos por el controlador — (1) ON DELETE SET NULL de punta a punta lo cubre TestDeleteDestinationKeepsItsEvents en la Task 7; (2) probar atomicidad con SIGKILL real está fuera del alcance de la fase 1. Ninguno es hueco.
Task 5: complete (commits 250a7bf..4ff1c82, spec ✅, calidad aprobada). El revisor verificó línea a línea los 7 puntos de seguridad: Bootstrap verifica de verdad en el camino "ya existe", distingue sql.ErrNoRows de error de I/O real, es idempotente, la clave se persiste cifrada (aserción no tautológica: lee la columna cruda), Settings() no tiene acceso al secreto completo porque el esquema solo guarda 4 caracteres, GenerateKey da 192 bits en base64 raw-url, y RotateIngestKey actualiza ciphertext y last4 en la misma sentencia.
Task 5: minor (deferred): RotateIngestKey y SetPasswordHash hacen `UPDATE ... WHERE id = 1` sin comprobar RowsAffected. Si se llamaran antes de Bootstrap devolverían éxito sin persistir nada. Inalcanzable hoy (el arranque siempre hace Bootstrap primero). PARA LA FASE 4: la API expone POST /api/ingest/rotate-key directamente, así que conviene añadir la comprobación antes de exponerlo.
Task 5: ⚠️ resuelto por el controlador — el uso real de IngestKeyMask, testCipher y bootstrapped lo verifican las propias Tasks 6 y 7 al compilar y pasar sus tests. No es un hueco.
Task 6: spec ✅, calidad NO aprobada. 1 Critical (ReorderDestinations acepta listas de longitud correcta con ids repetidos y deja sort_order empatados, comiteando) + 1 Minor diferido (UpdateDestination no transaccional).
Task 6: Ruling: el Critical es plan-mandated — el código defectuoso está en el propio task-6-brief.md, transcrito fielmente por el implementador. Fallo A FAVOR DEL HALLAZGO y en contra del texto del plan. Razón: el spec exige que el reorden persista el orden, y la validación por conteo no lo entrega para una entrada plausible; además es LOAD-BEARING porque la fase 4 expone POST /api/destinations/reorder con un array del cliente, así que es alcanzable desde entrada no confiable. Arreglo: validar el CONJUNTO de ids (sin repetidos, sin omisiones) en vez del conteo. Plan corregido para no reintroducir el bug. COSTE SI ME EQUIVOCO: ~25 líneas de validación de más en una ruta que se llama una vez por arrastre en la UI; despreciable.
Task 6: fix round 1/5 (1 addressed pendiente de verificación, 0 open; commits 16fe30b..8abc5fd). El implementador confirmó el rojo del test nuevo antes del arreglo. Re-revisión acotada despachada.
Task 6: fix round 1/5 verificado por ejecución — ADDRESSED, sin daño nuevo. El re-revisor reprodujo el escenario del hallazgo: ahora devuelve error, los sort_order quedan en 0,1,2 sin empates (el rollback revierte de verdad), el id inexistente satisface errors.Is(ErrDestinationNotFound), la lista incompleta sigue rechazándose y el reorden válido sigue funcionando. Confirmó además que destinationIDs usa tx.QueryContext y no d.db.QueryContext, lo que con SetMaxOpenConns(1) habría sido un autobloqueo.
Task 6: complete (commits 4ff1c82..8abc5fd, spec ✅, calidad aprobada tras 1 ronda de arreglo)
Task 6: minor (deferred): UpdateDestination no es transaccional entre el SELECT de existencia, el UPDATE y el SELECT final. Consistente con settings.go; sin impacto en un servicio de un solo usuario.
Task 7: complete (commits 8abc5fd..23042f7, spec ✅, calidad aprobada). El revisor validó TestDeleteDestinationKeepsItsEvents por contraprueba empírica: copió el repo fuera del árbol, cambió foreign_keys(1) por foreign_keys(0), y confirmó que el test FALLA con destination_id colgante. Prueba lo que dice, no pasa por accidente.
Task 7: minor (deferred): FinishSession no comprueba RowsAffected; con un id inexistente devuelve nil sin hacer nada. Misma familia que el minor de la Task 5 (RotateIngestKey, SetPasswordHash). PARA LA FASE 4, agrupados: los tres UPDATE de settings/sessions deberían devolver un error de "no encontrado" antes de exponerse por HTTP, como ya hacen UpdateDestination y DeleteDestination.
Task 8: spec ✅, calidad aprobada, 1 Important (contradicción en el README: el blockquote "en diseño, todavía no hay implementación" convive con la sección "## Estado / Fase 1 completa"). El revisor reprodujo por ejecución las tres verificaciones manuales: arranque con ingest_key enmascarada y SIGTERM con código 0; master key equivocada con mensaje accionable y código 1; y la COMPROBACIÓN FUERTE de cifrado en reposo — reveló la clave real (K9PB...ciwV, coincide con la máscara del log) y confirmó con grep -a que NO aparece en el .db. También verificó que `make build` ya funciona, cerrando el minor diferido de la Task 1.
Task 8: Ruling: el Important es plan-mandated — mi README inicial decía "en diseño" y el brief de la Task 8 solo mandaba sustituir la sección "## Alcance", así que el implementador se ciñó bien a su alcance. Fallo A FAVOR DEL HALLAZGO: es la portada de un repo público y un lector nuevo se topa con "no hay implementación" seguido de "fase 1 completa". Se arregla eliminando el blockquote obsoleto. COSTE SI ME EQUIVOCO: ninguno, es texto.
Task 8: fix round 1/5 (1 addressed, 0 open; commits 44ead0d..abe9ce0). Blockquote obsoleto eliminado; el enlace al documento de diseño se movió a la sección "## Estado" junto con el enlace al plan. Controlador verificó: ambos enlaces resuelven a archivos existentes, README sin contradicciones, working tree limpio.
Task 8: complete (commits 23042f7..abe9ce0, spec ✅, calidad aprobada tras 1 ronda de arreglo)
Task 1: minor RESUELTO — `make build` ahora funciona (verificado por el revisor de la Task 8: exit 0, binario de 10.7M). Ya no procede como diferido.

## Minors diferidos que van a la revisión final
1. Task 3: informe del implementador declaró "desvíos: ninguno" pese a haber subido el piso de go.mod. Honestidad del informe, no del código. Sin acción.
2. Task 4: literales de timestamp en db_test.go sin nanosegundos, contra la convención global. Cosmético; viene del propio brief.
3. Task 5 + Task 7 (agrupados): RotateIngestKey, SetPasswordHash y FinishSession hacen UPDATE sin comprobar RowsAffected; con un id inexistente devuelven éxito sin persistir. Inalcanzable hoy. RELEVANTE PARA LA FASE 4, que expone rotate-key por HTTP.
4. Task 6: UpdateDestination no es transaccional entre el SELECT de existencia, el UPDATE y el SELECT final. Consistente con settings.go; sin impacto en un servicio de un solo usuario.

## Revisión final de rama
Veredicto: fusionable, con 2 arreglos previos recomendados (fugas de secretos) + 1 minor promovido. Se aplicaron los 5.
- A. crypto.Secret no implementaba MarshalJSON: json.Marshal la emitía EN CLARO (verificado por ejecución). La fase 4 es una API JSON.
- B. Config.LogValue tenía receptor puntero: loguear un Config por valor volcaba los 32 bytes de la master key (verificado por ejecución).
- C. FinishSession/RotateIngestKey/SetPasswordHash devolvían éxito sin tocar filas. El revisor argumentó que FinishSession SÍ es alcanzable (id arbitrario en runtime, la fase 3 lo mantiene entre reconexiones) y que el store tenía dos contratos distintos para "no existe". Promovido de minor a arreglar-ahora.
- D. Tests que convierten en invariante la promesa de no fuga por JSON.
- E. Comprobación fuerte de cifrado en reposo automatizada sobre todos los archivos de la base, incluido el -wal.
Oleada de arreglo: commit 26ee204. Rojo reproducido en A y B antes del arreglo.

Triaje de los 4 minors diferidos (por el revisor final):
1. Honestidad del informe de la Task 3 → sin acción, cerrado.
2. Timestamps sin nanosegundos en db_test.go → después o nunca; verificado inocuo (time.Parse con RFC3339Nano acepta la ausencia de fracción).
3. RowsAffected → ARREGLADO en esta oleada.
4. UpdateDestination no transaccional → después; verificado que no produce resultado incorrecto con MaxOpenConns(1).

Task 6: fix round verificado. Deuda para fases 2-5 registrada en la §15 del spec (commit d002079), 8 puntos.

Re-revisión de la oleada: el agente se colgó a los 19 min (creó un módulo Go fuera del árbol, lo que dispara la descarga de modernc.org/sqlite por red — el mismo cuelgue de siempre). DETENIDO.
Ruling: el controlador verificó los 5 arreglos por ejecución él mismo, dentro del módulo (instantáneo, sin red), con tests temporales que luego borró. Evidencia registrada abajo. Razón: la propiedad a verificar es objetiva y binaria, la ejecución la prueba sin ambigüedad, y un tercer despacho habría muerto por la misma causa de entorno. COSTE SI ME EQUIVOCO: la re-revisión no la hizo un tercero independiente; mitigado porque la evidencia es salida de ejecución literal, no razonamiento.

Evidencia de la verificación del controlador (salida real):
  A marshal anidado => [{"name":"YouTube","key":"••••1234"},{"name":"Twitch","key":"••••9876"}]
  A unmarshal      => Reveal()="live_abcdefgh1234" (la entrada sigue aceptando la clave en claro)
  B por valor      => {"config":{"http_addr":":8080","rtmp_addr":":1935","db_path":"splitstream.db","log_level":"INFO"}} — sin MasterKey
  B por puntero    => idéntico, sin MasterKey
  C RotateIngestKey sin Bootstrap => "settings no inicializado: falta Bootstrap"
  C SetPasswordHash sin Bootstrap => "settings no inicializado: falta Bootstrap"
  C RotateIngestKey legítimo      => OK
  C FinishSession(9999)           => "sesión no encontrada"
  C FinishSession legítimo        => OK
