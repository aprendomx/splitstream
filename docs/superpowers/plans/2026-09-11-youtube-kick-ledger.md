# SDD ledger — plan: docs/superpowers/plans/2026-09-11-youtube-kick.md

Spec: docs/superpowers/specs/2026-09-11-youtube-kick-design.md (autoridad vinculante), sobre el spec de la v0.11, el spec base y el plan maestro §7.
Rama: feat/youtube-kick (desde main @ 4d94238, merge de la v0.11; v0.11.0 etiquetada ahí). Commits de documentación previos: e14b783 (spec), 4e26cdb (plan).
Ejecución: 2026-09-11, subagent-driven, un implementador por tarea + revisión por tarea.

Ruling: se trabaja en el checkout actual sobre la rama, sin worktree (como en v0.8–v0.11) — coste si es erróneo: ninguno mientras no se toque main.
Ruling: la línea base es la CI en verde sobre 6827b26 (run 34637492449) y la release v0.11.0 completa (9 jobs) — coste si es erróneo: ninguno.
Ruling (spikes): Kick pasa al modelo «app propia» (exige client_secret, sin cliente público ni device flow, una redirect URL por app); YouTube usa el flujo de dispositivo con el scope `youtube` solo (force-ssl no está permitido en ese flujo) y credenciales propias por la cuota por proyecto — coste si es erróneo: ninguno; es lo que documentan ambas plataformas.
Ruling: las emisiones de YouTube se crean con enableAutoStart/enableAutoStop; los botones «Salir al aire»/«Terminar» son respaldo — coste si es erróneo: si YouTube no arranca sola, el botón lo hace con reintentos de 60 s.
Ruling: `Provider.BeginAuth/PollAuth/Refresh` cambian de firma para recibir `Credentials`; Twitch las ignora — coste si es erróneo: ninguno (todo compila en la Task 2).
Ruling: el chat de YouTube va por `liveChatMessages.list` (sondeo) y no por `streamList` (transporte no documentado para HTTP plano) — coste si es erróneo: cuota; el presupuesto lo acota.
Ruling: los costes de los métodos `live*` no están en la tabla pública de Google; se usa la tabla fija conservadora (insert/update/bind/transition 50, list 1, chat 5) documentada como estimación — coste si es erróneo: el contador se desvía de la cuota real; la pausa del chat sigue protegiendo.
Ruling: categorías de Kick por la v1 deprecada (`?q=`), aislada en una función para cambiar a v2 — coste si es erróneo: un cambio de una función cuando Kick retire la v1.
Ruling: el `state` del redirect lo firma `httpapi` con el `sessionSigner` (10 min) y el proveedor solo lo transporta; la ruta de callback y la del webhook son públicas y se protegen por criptografía — coste si es erróneo: ninguno.
Ruling: briefs de las Tasks 3–12 sin código completo: los proveedores se calcan de `internal/platforms/twitch` por instrucción explícita, con tests y fixtures especificados caso por caso — coste si es erróneo: más juicio en el implementador; las revisiones lo vigilan.

## Preflight (tabla de conflictos)

| Par / tarea | Produce → consume | Hallazgo |
| --- | --- | --- |
| T1 → T2, T9 | `store.Credentials`, `NewAccount.OwnApp/Credentials`, `AccountCredentials`, `Broadcast*`, `Destination.KeyFromAPI`, `AddQuota/QuotaUsed/PruneQuota` | Coinciden con el manager (T2), `quota.Counter` (T2) y la API (T9). `BroadcastsByDestination` se añade en T9 si T1 no lo trae: T1 lo trae mejor (añadido al brief por el controlador en el despacho). |
| T1 texto | `scanDestination` con un `LEFT JOIN` más y `COALESCE(db.key_from_api, 0)`; alias `db` choca con… nada (SQL) | OK; usar alias `br` por claridad. |
| T2 → T3, T6 | Firmas nuevas de `Provider`; `RedirectAuth`, `BroadcastScheduler`, `IngestKeyProvider`, `ChatWebhook`, `QuotaSink`; `AuthPrompt.RedirectURL/CodeVerifier` | T6 necesita además `AuthPrompt.RedirectURI` y T7 `ChatMessage.BroadcasterID`: se añaden en T2 directamente (ruling: evitar tocar `platform.go` tres veces). |
| T2 texto | `Credentials = store.Credentials` (alias): `platforms` ya importa `store` | OK. |
| T2 ↔ T4 | `BroadcastTitleSetter` se declara en T4 | Ruling: se declara en T2 con las demás. |
| T3 texto | `PollAuth` suma 1 unidad con cuenta sin id | Anotado: la identidad no se contabiliza (sink con id 0 se ignora en `quota.Counter.Sink`… no: `Options.Quota` se llama con `acct.ID`; en `PollAuth` no hay cuenta → no se gasta). Ruling: la consulta de identidad no cuenta. |
| T4 ↔ T9 | `SetTitle` de YouTube lista emisiones; `aplicarEnDestino` prefiere `BroadcastTitleSetter` | Coinciden. |
| T5 ↔ T10 | `Options.ChatBudget func(accountID) (used, budget int, ok bool)`, `OnChatPaused`, `Sleep`, `MinPoll` | T10 los cablea con esa firma; anotado en ambos briefs. |
| T6 texto | `SetCategory` con `category_id` entero: `platforms.CategorySetter.SetCategory(ctx, acct, token, id string)` | Kick convierte con `strconv.Atoi`; inválido → error. |
| T7 ↔ T9 | `ParseWebhook` devuelve `AccountID` 0 y `BroadcasterID`; T9 resuelve la cuenta con `AccountByExternalID` (nuevo en store) | T9 lo añade al store con test. |
| T8 ↔ T9/T10 | `Aggregator.Ingest([]ChatMessage) int` ↔ `Config.ChatIngest` | Coinciden. |
| T9 texto | Rutas públicas `GET /api/platforms/{p}/callback` y `POST /api/platforms/kick/webhook` fuera de `protegida`; `TestDTOFieldNamesAreSnakeCase` con `broadcastDTO`, `testSkippedDTO`; `panelDTO.youtube_chat_budget` | Anotado. |
| T9 ↔ T11 | `broadcastDTO`, `key_from_api`, `redirect_url`, `own_app`, `quota_used_today`, `public_url_ok`, `youtube_chat_budget` | El panel lee esos nombres. |
| T10 texto | `webtls.Build` va después del bloque de plataformas en `main.go`: reordenar | Anotado. |
| T12 | `docs/img/.gitkeep`; capturas las aporta el usuario en la puerta | Anotado. |
| Puerta | Proyecto de Google Cloud y app de Kick del usuario; VPS con TLS integrado para el chat de Kick; crear una emisión de YouTube desde el panel sin pegar la clave y salir al aire; chat de Kick por webhook | Del usuario; push y PR los hace el controlador tras la revisión final. |

## Progreso
BASE T1: 4e26cdb
Task 1: complete (commits 4e26cdb..7d334c1, review clean; diferibles: SetBroadcast sin pre-chequeo de existencia (error crudo de FK), test de cascada destino→broadcast, formato de day en AddQuota)
BASE T2: 7d334c1
Task 2: complete (commits 7d334c1..6795adb, review clean; diferibles: comentario en Counter.Sink sobre context.Background, aserciones var _ platforms.Provider en los dobles)
BASE T3: 6795adb
Task 3: complete (commits 6795adb..a14ae44, review clean; diferible: TestCapabilities sin hosts inyectados → lo arregla T4 de paso)
BASE T4: a14ae44
Task 4: review WITH FIXES — Important: updateTitle no reenvía snippet.scheduledStartTime (update reemplaza el snippet). Fix round 1 (resume implementer) + menores: OAuthBase en proveedorEmisiones, validar part=snippet,cdn en el fake de liveStreams.
Task 4: complete (commits a14ae44..9722ace, fix round 1 → re-review CLEAN)
BASE T5: 9722ace
Ruling (T5): el fixture chat_page1 usa pollingIntervalMillis 60 en vez de 4000 para que el test no duerma; ningún consumidor aguas abajo lee ese fixture — coste si es erróneo: ninguno.
Task 5: review Spec ✅ / WITH FIXES — Important: updateTitle manda liveChatId (solo lectura) en el PUT. Fix round 1 (resume implementer) + test de ChatBudget ok=false. Ruling: ChatNoBroadcastGiveUp no acota errores persistentes de red (patrón Twitch: solo ctx corta) — diferido — coste si es erróneo: un lector que reintenta con backoff 60 s hasta que el manager lo cancele.
Task 5: complete (commits 9722ace..618e32e, fix round 1 → re-review CLEAN por el controlador; diferible: cobertura de avisarPausaPorCuota con ChatBudget nil)
BASE T6: 618e32e
Task 6: complete (commits 618e32e..4b2e46d, review clean)
BASE T7: 4b2e46d
Ruling (T7): motivo de rechazo «json» para payload firmado que no deserializa (401, no 500: Kick no reintenta para siempre); created_at ilegible cae a la marca firmada de la cabecera; forma de la lista de suscripciones tolerante (event|name) — coste si es erróneo: un ajuste de una función cuando se vea la respuesta real en la puerta.
Task 7: complete (commits 4b2e46d..6770cd0, review APPROVED; diferibles para la ola final: clamp SeenTTL ≥ 2×ReplayWindow en init; marca ilegible → motivo «cabeceras»; DELETE 200 con message no vacío = error; digest por streaming; guardia edadMinRefresco dentro de clavePublica(forzar))
BASE T8: 6770cd0
Task 8: complete (commits 6770cd0..e53fffe, review clean por el controlador: diff igual al brief; test idempotente con bandera entregado)
BASE T9: e53fffe
Task 9: implementada (e53fffe..134c62e; el commit original bbff732 se enmendó solo para corregir el trailer Co-Authored-By). Ruling: Config.ChatBudget es el presupuesto diario del chat de YouTube y viaja como panelDTO.youtube_chat_budget (lo lee la Task 11); el tope de mensajes por entrega de webhook, si hace falta, es una constante aparte — coste si es erróneo: ninguno (la Task 10 lo cablea con cfg.YouTubeChatBudget). Ruling: Config.WebhookURL no existe; la URL se calcula en Server desde TLS+PublicURL (resolución g del despacho).
Task 9: review Spec ❌ / WITH FIXES — Important: Config.ChatBudget reinterpretado (sin panel.youtube_chat_budget); callback no idempotente ante dos llegadas concurrentes del mismo code. Fix round 1 (resume implementer) + menores baratos: textoSeguro en el fallo del canje, expira = min(prompt.ExpiresAt, now+stateTTL), test de «crea la emisión primero». Diferidos a la ola final: destino huérfano si LinkDestination/guardarEmision fallan tras CreateDestination; flush del webhook sin Content-Length.
Task 9: complete (commits e53fffe..b509c9a, fix round 1 → re-review CLEAN)
BASE T10: b509c9a
Task 10: review Spec ✅ / WITH FIXES — renovarWebhooksKick sin test; timeout por cuenta; test de DayBefore. Fix round 1 (resume implementer).
Task 10: complete (commits b509c9a..153ad65, fix round 1 → re-review CLEAN por el controlador). Ruling: renovarWebhooksKick decide solo por publicURL (webtls solo la rellena con TLS) — coste si es erróneo: ninguno.
BASE T11: 153ad65
Task 11: complete (commits 153ad65..fe61007, review APPROVED; diferibles para la ola final: enlace «cancelar» en el asistente; hayUrlPublica debe leer public_url_ok de /api/platforms)
BASE T12: fe61007
Task 12: implementada (fe61007..92eb46a). Ruling: renumeración del manual 10–13→11–14 y corrección de «Qué no hace» en lanzamiento.md aceptadas; capturas de Kick sin marcador — coste si es erróneo: ninguno.
Task 12: complete (commits fe61007..92eb46a, review clean)
Revisión final de rama: despachada (opus) sobre 4e26cdb..92eb46a (16 commits).
Revisión final: WITH FIXES — Critical: ConectarCuenta `:origin="location.origin"` en plantilla (asistente inservible); Important: caché de clave pública de Kick amplifica DoS (A.4 sube); access_denied no termina el flujo. Ola final única despachada (opus) con el brief final-fix-brief.md (A.1–A.9 + hallazgos 10–18). Ruling: la deduplicación sinQuery/do/httpx se difiere a la v0.13 — coste si es erróneo: deuda de tres copias, sin efecto funcional. BASE ola final: 92eb46a
Ola final: implementada (92eb46a..aaf3a27, 5 commits, 18/18 puntos). Re-revisión acotada despachada.
Ola final: re-review CLEAN (92eb46a..aaf3a27). Rama lista para PR.
