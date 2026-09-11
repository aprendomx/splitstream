# SDD ledger — plan: docs/superpowers/plans/2026-09-11-twitch.md

Spec: docs/superpowers/specs/2026-09-11-twitch-design.md (autoridad vinculante), sobre el spec base 2026-09-01-rtmp-relay-design.md y el plan maestro §6.
Rama: feat/twitch (desde main @ e39d3c3, merge de la v0.10; v0.10.0 etiquetada ahí). Commits de documentación previos: ec18057 (spec), c0fb701 (plan).
Ejecución: 2026-09-11, subagent-driven, un implementador por tarea + revisión por tarea.

Ruling: se trabaja en el checkout actual sobre la rama, sin worktree (como en v0.8–v0.10) — coste si es erróneo: ninguno mientras no se toque main.
Ruling: la línea base es la CI en verde sobre ae7a2e3 (run 34525218766) más la release v0.10.0 (binarios, GHCR, winget en verde; la fórmula de Homebrew se publicó a mano y el job se arregla en el PR #15, pendiente de fusionar; no afecta a esta rama) — coste si es erróneo: ninguno.
Ruling: el spike D.0 se resolvió contra la documentación oficial sin app registrada; el `client_id` incluido queda vacío (`twitch.ClientID = ""`) hasta que el usuario registre la app; todo funciona con `SPLITSTREAM_TWITCH_CLIENT_ID` y así se ejecuta la puerta — coste si es erróneo: un commit de una línea al recibir el id.
Ruling (T3): el plan pedía que `twitch.New` haga `panic` bajo `testing.Testing()` si se usan los hosts reales; eso rompería `go test ./cmd/splitstream`, cuyo `run()` construye el proveedor con los defectos. Se ELIMINA ese guard y el test `TestDefaultHostsAreRefusedUnderTests`. La regla «ningún test habla con internet» se cumple porque sin cuentas en la base ni el manager ni el chat llaman a Twitch, y los tests del paquete inyectan hosts; la revisión lo vigila — coste si es erróneo: un test futuro con una cuenta real en `cmd` saldría a la red; se anota en el spec §8.
Ruling (T1): `UpsertAccount` usa `INSERT … ON CONFLICT DO UPDATE` (SQLite ≥ 3.24; modernc lo cumple) y relee el id por la clave única en vez de fiarse de LastInsertId — coste si es erróneo: ninguno.
Ruling (T4): la conexión nueva de `session_reconnect` se abre y recibe su welcome ANTES de cerrar la vieja; el brief lo indica en la nota final y el test observa «dos conexiones, una suscripción» — coste si es erróneo: pérdida de mensajes durante ~1 s en cada reconexión de Twitch.
Ruling (T5): `chat.Message` incrusta `platforms.ChatMessage` y añade `SessionID`; el snapshot del WS lleva `id` 0 (no persistido aún) y el panel no lo necesita — coste si es erróneo: ninguno.

## Preflight (tabla de conflictos)

| Par / tarea | Produce → consume | Hallazgo |
| --- | --- | --- |
| T1 → T2, T5, T6 | `Account` sin tokens, `Tokens`, `NewAccount`, `AccountTokens`, `SaveTokens`, `SetAccountStatus`, `Link/Unlink`, `AccountForDestination`, `DestinationsOfAccount`, `Destination.AccountID`, `InsertChatMessages`, `ChatMessages`, `PruneChat` | Coinciden en firma con T2 (manager), T5 (agregador) y T6 (API). |
| T1 texto | Tests usan `db.Raw()`, `StartSession`/`EndSession`, `DeleteDestination`, `UpdateDestination(ctx, nil, …)` | Nombres a comprobar en `sessions.go`/`destinations.go`; el brief lo pide. `Raw()` se añade si no existe. |
| T1 texto | `TestChatFallsWithItsSession…` depende de que `PruneSessions` no excluya la sesión por eventos | El test no registra eventos; `StartSession` tampoco. OK. |
| T1 ↔ Global | `TestOpenCreatesSchema` con 11 tablas; `SchemaVersion` 8 y la guardia de `loadMigrations` | Anotado. |
| T2 → T3 | `platforms.Provider` (con `Configured`, `PollAuth`), `TitleSetter`, `CategorySetter`, `ChatReader`, `TokenSource`, errores | T3 implementa exactamente esas firmas; T4 añade `ReadChat`. |
| T2 → T5, T7 | `NewManager(db, c, func(ID) (Provider, bool))`, `Source`, `Run`, `OnReauth` | T7 pasa `registro.Get` (method value con esa firma). T5 recibe `TokenSourcer{Source}`. |
| T3 texto | Guard `testing.Testing()` con `panic` | Ruling arriba: se elimina. |
| T3 ↔ T4 | `Options` gana `ChatBackoffMax`, `KeepaliveGrace` en T4 | Anotado en el brief de T4 (Step 4). |
| T4 texto | `sesionChat` devuelve la URL de reconexión y el `defer conn.CloseNow()` cierra la vieja antes de abrir la nueva | Nota final del brief: abrir la nueva dentro y devolver la conexión. Ruling arriba. |
| T5 texto | `a.mensajes[m.Platform]` puede ser nil con plataformas no registradas | Guard indicado en el brief. `db.Close()` doble en el último test: envolver. |
| T6 → T8 | DTOs `capabilities`, `account` en destino; `authStartDTO`, `authStatusDTO`, `liveResultDTO`, `chatMessageDTO`, `platformDTO`, `accountDTO` | El panel lee esos nombres (snake_case). |
| T6 texto | `destinationPatch.AccountID json.RawMessage` distingue ausente/`null`/número | `json.RawMessage("null")` tiene len 4: rama unlink. OK. |
| T6 texto | `servidorPlataformas` tiene un `srvRef` inútil | Anotado: simplificar. |
| T6 ↔ rutas | `GET /api/platforms/twitch/categories` vs `GET /api/platforms/{p}/auth/{state}` | Distinto número de segmentos: no compiten en el mux de Go 1.22. |
| T6 ↔ Global | `httpapi` importa `internal/platforms` e `internal/chat` (permitido), no `twitch` ni `tokens` | Guard en el Step 9. |
| T7 texto | `TestRunExposesPlatforms` hace setup + login como el test TLS de la v0.10 | Reutiliza `arrancaRun`; anotado. |
| T8 texto | Iconos `mdiMessageText`, `mdiAccountCircle`, `mdiLinkVariant`, `mdiFormatTitle`, `mdiCheck` | Comprobar en `@mdi/js`; el brief lo dice. |
| T9 texto | Renumerar secciones del manual si numera a mano | Anotado. |
| Puerta | Registrar la app en dev.twitch.tv (2FA, tipo Public, redirect `http://localhost`), `SPLITSTREAM_TWITCH_CLIENT_ID` en la máquina de prueba, cambiar título y ver el chat en una emisión real | Del usuario; push y PR los hace el controlador tras la revisión final. |

## Progreso
BASE T1: c0fb701
Task 1: complete (commits c0fb701..34aff7b, review clean; nombres reales: db.SQL(), FinishSession; diferibles: PruneChat con NOT IN completo, validación de espacios en UpsertAccount, chat_messages.account_id sin FK (por diseño del brief))
BASE T2: 34aff7b
Task 2: complete (commits 34aff7b..295cc3f, review clean; diferibles: guarda Interval<=0 en Run, test de error de red en Refresh, nombre Providers en el resumen del brief)
BASE T3: 295cc3f
Task 3: complete (commits 295cc3f..916efd9, review clean; diferibles: SearchCategories sin guard Configured (entra en T4, mismo paquete), rama de 400 desconocido sin test, channel.json sin uso, slow_down colapsado en ErrAuthPending (httpapi hace backoff genérico, ya previsto en el brief de T6))
BASE T4: 916efd9
Task 4: review WITH FIXES — Important Q1: el backoff no se reinicia tras una sesión establecida. Fix round 1 (resume implementer) con Q2 (plazo 8 s en suscribirChat), Q4 (select sin ctx.Done redundante), Q6 (plazo en el test de revocación) y errRevocado tipado como platforms.ErrChatRevoked para que T5 lo distinga. Diferidos: Q3 (contador de descartes aguas arriba; la métrica cuenta los del agregador), Q5, Q7, At desde message_timestamp, test de motivoCierre.
Task 4: complete (commits 916efd9..aef8f3d, review clean tras 1 fix round; ReadChat con reconexión nueva-antes-que-vieja, backoff reiniciado con sesión establecida, plazo 8 s de suscripción, platforms.ErrChatRevoked; diferidos: Q3 contador de descartes aguas arriba, Q5, Q7, At desde message_timestamp, test de motivoCierre)
BASE T5: aef8f3d
Ruling (T5): el test TestAggregatorDropsWhenTheStoreFails del plan era una carrera (db.Close antes de que arrancar() listara las cuentas). NO se cuenta como dropped un fallo de cuentasConChat (no pierde mensajes); el test se hace determinista con un lector falso que espera una señal para emitir, tras cerrar la base con el lector ya arrancado — coste si es erróneo: ninguno.
Task 5: DONE del implementador en 3178211; ronda de corrección previa a la revisión por el ruling anterior.
Task 5: complete (commits aef8f3d..5d05ed9, review clean tras 1 fix round del controlador: chat_connected antes del lector, drenaje de in al parar; diferibles: conectado por plataforma y no por cuenta, texto ambiguo en TestBusRecent, sleep en el test de cuentas ignoradas; nota para T7: Aggregator.Run en fondo antes de db.Close)
BASE T6: 5d05ed9
Task 6: review WITH FIXES — Important I-1 (limpieza TTL de authFlows nunca borra), I-2 (sin tope de flujos ni cancelación al apagar), I-3 (PATCH account_id no atómico: deja plataforma cambiada tras 400), I-4 (aserción vacía del done-una-vez), I-5 (camino error de plataforma sin test). Fix round 1 (resume implementer) + menores #1 (account_id null en chatDTO), #4 (WithoutCancel en delete), #8 (guard Interval 0). Ruling I-2: Config.BaseContext para colgar los sondeos y Server.Wait() para que main los espere; tope de 8 flujos vivos por plataforma → 409. Diferidos: el resto de menores.
Task 6: complete (commits 5d05ed9..52c1e58, review clean tras 1 fix round; nuevos: Config.BaseContext, Server.Wait(), tope de 8 flujos por plataforma; diferidos: 15 menores de la revisión (N+1 en cuentas, Accounts() por tick de estado, TitleSetter corta antes de la categoría, sin plazo por destino en live/title, etc.))
BASE T7: 52c1e58
Task 7: complete (commits 52c1e58..a0a37a3, review clean; los dos diferibles —validación de tokens al arrancar, suscripción del agregador en el constructor— aplicados por el controlador en ea31c78)
BASE T8: ea31c78
Task 8: complete (commits ea31c78..f3864f5, review clean tras 1 fix round del controlador en dd0a3f8: PATCH de cuenta tras el alta con aviso sin dejar el canal duplicable; backoff del chat reiniciado en onopen; diferible: ConectarCuenta colapsa cualquier error de sondeo en «se interrumpió»)
BASE T9: dd0a3f8
Task 9: complete (commits dd0a3f8..530725d, review clean; manual renumerado §10→11, §11→12, §12→13; diferible: la pestaña «Todos» del chat no se menciona en el manual)
Suite completa sobre 530725d (implementador de T9): go vet limpio, go test ./... -race exit 0, npm run build limpio.
Revisión final de la rama (c0fb701..530725d, 15 commits, 70 archivos): despachada (opus).
Revisión final de la rama (c0fb701..530725d, opus): «With fixes». 1 crítico, 3 importantes, 7 menores.
Ruling: crítico #1 (refresco abortado por el contexto del llamante pierde la rotación): doRefresh usa context.WithoutCancel con plazo de 30 s — coste si es erróneo: un refresco puede tardar hasta 30 s en el apagado, solo si coincide con la ventana de 5 min.
Ruling: importante #2 (cambiar la plataforma deja un enlace a una cuenta ajena): UpdateDestination borra el enlace en la misma transacción cuando cambia la plataforma — coste si es erróneo: ninguno; el panel vuelve a pedir la cuenta.
Ruling: importante #3 (TestRunExposesPlatforms depende del entorno): el test fija SPLITSTREAM_TWITCH_CLIENT_ID y afirma configured=true — coste si es erróneo: ninguno.
Ruling: importante #4 (live/title sin tope ni plazo): máximo 20 destinos, deduplicación y 10 s por destino con resultado «tardó demasiado» — coste si es erróneo: ninguno.
Ruling: menores #5 (guard de CI con twitch/tokens), #6 (GOMAXPROCS=2 en CI), #7 (plataforma del flujo en auth status) y #8 (nota en el manual sobre la revocación; la revocación real queda para la v0.12) entran en la ola. #9, #10, #11 y los diferibles por tarea esperan; «SearchCategories sin guard» ya estaba resuelto en T4 — coste si es erróneo: ninguno.
Ola final: brief en final-fix-brief.md; un solo implementador (opus), una re-revisión acotada después.
Ola final: commits 530725d..8e7a483 (7, uno por apartado A–G; la rama se reescribió una vez para un comentario de ci.yml antes de cualquier push). Suite del implementador: go test ./... -race exit 0, GOMAXPROCS=2 en platforms/chat/httpapi ok, npm run build ok, cinco guards limpios.
Ola final: re-revisión acotada 530725d..8e7a483 hecha por el controlador (los agentes sonnet y opus cayeron por el límite de sesión): A–G ADDRESSED; la reutilización de la transacción en UpdateDestination es correcta (InTx construye el *DB con ex: tx y aplicar recibe ese mismo *DB); sin roturas nuevas. Suite completa del controlador sobre 8e7a483: go vet limpio, go test ./... -race sin FAIL, npm run build exit 0, cinco guards limpios, YAML válido, go.mod intacto.
Cierre: rama lista para PR sobre main. Del usuario: registrar la app de Twitch (2FA, Public, redirect http://localhost, categoría Application Integration) y dar el client_id (SPLITSTREAM_TWITCH_CLIENT_ID para la puerta; luego un commit de una línea en twitch.ClientID); puerta: título cambiado en Twitch desde el panel y chat visible en una emisión real; CI; fusionar y etiquetar v0.11.0.
