# SDD ledger — plan: docs/superpowers/plans/2026-09-09-confianza-produccion.md

Spec: docs/superpowers/specs/2026-09-09-confianza-produccion-design.md (autoridad vinculante), sobre el spec base 2026-09-01-rtmp-relay-design.md.
Rama: feat/confianza-produccion (desde main @ ac70a98). Commits de documentación previos: c0b5bd4 (roadmap + plan maestro), 998e412 (spec + plan v0.8).
Ejecución: 2026-09-09, subagent-driven, un implementador por tarea + revisión por tarea.

Ruling: se trabaja en el checkout actual sobre la rama feat/confianza-produccion, sin worktree — es lo que manda el plan (Global Constraints) y la convención de todas las fases anteriores; los cuatro documentos nuevos estaban sin commitear y viajan con la rama — coste si es erróneo: ninguno mientras no se toque main.

## Preflight (tabla de conflictos)

| Par / tarea | Produce → consume | Hallazgo |
| --- | --- | --- |
| T2 → T8, T11 | `events.Bus.Subscribe/Publish/Dropped`, `store.SetEventHook` → `main.go` (`bus.Dropped()` en ExtraMetrics), `alerts.NewWebhookDispatcher(bus…)` | Coinciden. T2 deja `_ = bus` hasta T8: previsto en el texto. |
| T3 → T4, T6, T8 | `relay.StateSuspended.String()=="suspended"` → `handleRetryDestination`, `handleTestDestination`, lista `estados` de /metrics | Coinciden. |
| T4 → T6, T11 | `store.DestinationByID` → `handleTestDestination`, `alerts.Send` | Coinciden (T4 la crea; T6 y T11 la consumen). |
| T5 → T6 | `probe.Result{Outcome,Stage,Elapsed,Err}`, `rtmpio.Probe(ctx,cfg,grace)` → `Factory.Test`, `DestinationTester` | Coinciden. `Outcome.String()` da `closed_early`, que es lo que compara el test de T6. |
| T6 ↔ Global | `httpapi` no importa `rtmpio`: `Factory.Test` vive en `sinks`, `httpapi` solo ve `probe` | Coincide; T6 añade la frontera a la CI. |
| T7 → T8 | `/healthz` público, `requireSessionOrToken` en T8 | Sin solape de archivos salvo `server.go` (rutas distintas). |
| T8 → T11 | `httpapi.Metric/ExtraMetrics` → `main.go` rellena con `webhooks.Stats()` | Coinciden; T11 reemplaza el closure de ExtraMetrics de T8 (previsto). |
| T9 → T10 | `formatTime`, `Session` → `ListSessions` | Sin conflicto. Ambas tocan `main.go` en sitios distintos. |
| T8 → T10, T12 | `itoa` se define en `metrics_test.go` (T8) y lo usan `sessions_test.go` (T10) y `webhooks_test.go` (T12) | OK por orden: T8 va antes. |
| T11 → T12 | `store.Webhook*`, `WebhookSecret`, `RecordWebhookDelivery`, `alerts.Send` → handlers y `WebhookSender` | Coinciden. |
| T12 → T13 | `statusDTO.RecentEvents` (`recent_events`), `/api/webhooks*` → `panel.eventosRecientes`, `Ajustes.vue` | Coinciden. |
| T12 ↔ Global | `TestWebSocketPayloadMatchesTheRESTSnapshot`: WS y REST usan el mismo `status()`, así que `recent_events` sale por los dos | Sin conflicto. |
| T7 texto | Test de `/healthz` comprueba la ausencia de `version` sobre JSON crudo; `healthDTO` no tiene ese campo | Consistente. |
| T8 texto | `handleMetrics` usa `time.Since`; imports incluyen `time` | Consistente. |
| T11 texto | `webhooks.go` termina con `var _ = errors.New` para justificar un import que no se usa | Ruling: quitar esa línea y el import `errors` de `alerts/webhooks.go` — el plan la marca como opcional y un import de relleno es ruido — coste si es erróneo: ninguno. |
| T12 texto | `handleTestWebhook` responde 502 con `codeConflict` | El plan lo anota como decisión revisable; se mantiene (conjunto cerrado de códigos). |
| T14 ↔ proceso | La puerta de salida exige plataformas reales y OBS: no la puede hacer un subagente | Ruling: T14 se ejecuta hasta dejar el PR listo; la puerta (Step 4), la fusión y la etiqueta (Step 5) quedan para el usuario y se dejan en el ledger como pendientes — coste si es erróneo: ninguno, es lo que exige el proceso (push a rama compartida y publicación). |

Ruling: un hook del entorno bloquea saltarse los hooks de git; los implementadores commitean de forma normal, que es lo que el plan pide.

## Progreso
Línea base sobre 998e412: go vet limpio; go test ./... -race en verde en los 10 paquetes (httpapi 99 s, relay 24 s, rtmpio 22 s).
Task 1: complete (commits 998e412..0657ea9, review clean)
Task 2: minor (deferred): SetEventHook documentado como no seguro en concurrencia con LogEvent; solo se cablea en el arranque (hook.go:16).
Task 2: complete (commits 0657ea9..7b68ddf, review clean; ⚠️ GOMAXPROCS=2 y trailers verificados por el controlador)
Task 3: minor (deferred): TestSinkSuspendsAfterRepeatedFlaps no comprueba que tras suspender no haya una conexión más (heredado del plan); fallback de umbrales con <=0 en vez de ==0.
Task 3: complete (commits 7b68ddf..b43847b, review clean)
Task 4: minor (deferred): sin test del 409 por destino apagado en retry; el test no comprueba DestinationID del evento; el botón Reintentar depende solo de metrics.state (UX borde).
Task 4: complete (commits b43847b..2dbd5c9, review clean; ⚠️ codeConflict está en errors.go y npm run build verificado por el controlador)
Task 5: Ruling: la cancelación del contexto durante la gracia devolvía rejected/grace (heredado del plan, contradice la tabla del spec §3) — se decide unreachable con etapa cancelled («la sonda no concluyó»), con test propio y una línea nueva en la tabla del spec §3; Task 6 mapea la etapa cancelled a «La prueba se canceló antes de terminar» — coste si es erróneo: un mensaje de interfaz impreciso en un caso que en producción no ocurre (el plazo de la API cubre conexión + gracia).
Task 5: fix round 1/5 (2 addressed, 0 open — cancelación en gracia → unreachable/cancelled con test y fila en el spec; timer parado; commits a78211a..60e75b2)
Task 5: complete (commits 2dbd5c9..60e75b2, review clean tras 1 ronda)
Task 6: minor (deferred): probeMessage sin test directo para la etapa cancelled; el texto de rejected/publish mezcla «handshake» y «publish» (heredado del plan).
Task 6: complete (commits 60e75b2..39dd181, review clean)
Task 7: Ruling: no hay daemon de Docker en esta máquina; el paso 9 (docker build + inspect healthy) se salta en local y se anota como pendiente para la CI (job docker) y para el usuario — coste si es erróneo: un HEALTHCHECK mal formado se vería en la CI, no antes.
Task 7: minor (deferred): el test del 503 de /healthz no comprueba el cuerpo; mensaje de error de Ping terso.
Task 7: complete (commits 39dd181..eabd8b6, review clean; Docker verificación pendiente en CI por ruling)
Task 8: Ruling: TestMetricsWithoutATokenOnlyAcceptsTheCookie (heredado del plan) no mandaba una cabecera Authorization: Bearer vacía, así que la regla «Bearer vacío nunca pasa» quedaba sin cobertura — se corrige el test para mandar la cabecera de verdad — coste si es erróneo: ninguno.
Task 8: minor (deferred): estados connecting/reconnecting/error/suspended y degraded=0 sin aserción en el test de /metrics; HELP/TYPE no se comprueban automáticamente; escapes de barra y salto de línea sin test; comentario «ochenta líneas» desfasado.
Task 8: fix round 1/5 (1 addressed, 0 open — el test manda Authorization: Bearer vacío de verdad; commits 55170b8..0607681)
Task 8: complete (commits eabd8b6..0607681, review clean tras 1 ronda)
Task 9: minor (deferred): sin test del camino busy→retry sin OnDone en Scheduler.Run; time.After sin Stop en el bucle diario; comentario de RetentionMaxEvents no dice que 0 desactiva.
Task 9: complete (commits 0607681..0cce14f, review clean)
Task 10: minor (deferred): tres subconsultas correlacionadas en ListSessions; backup_downloaded se registra antes de os.Open; sin test de before/limit negativos.
Task 10: complete (commits 0cce14f..2e97e9f, review clean)
Task 11: minor (deferred): TestDispatcherDoesNotBlockTheBusOnASlowEndpoint tarda ~30 s por el orden de los defer (srv.Close antes que CloseClientConnections), heredado del plan; arreglo de una línea, PRIORIDAD para la ola final.
Task 11: complete (commits 2e97e9f..e853a43, review clean)
Task 12: minor (deferred): sin test del 409 (sin sender) ni del 404 en /api/webhooks/{id}/test; el test del evento sintético no comprueba Level; el test del 502 no comprueba el code.
Task 12: complete (commits e853a43..402a890, review clean)
Task 13: minor (deferred): comentario en DialogoWebhook que menciona un botón «quitar secreto» que no existe (heredado del plan); descarga por <a> no anclado al DOM y revokeObjectURL síncrono (frágil en Safari); copy «últimos N» ambiguo.
Task 13: complete (commits 402a890..9c21bf5, review clean; ⚠️ orden de recent_events, has_secret y created_at verificados contra los tests/DTO de la Task 12)
Task 14: Ruling: sin Docker no se puede correr make test-integration en local; se ejecuta el resto de la definición de terminado y la integración la corre la CI — coste si es erróneo: un fallo de integración se vería en el PR, no antes.
Task 14: Ruling: el push de la rama y la apertura del PR los hace el controlador después de la revisión final de la rama (para que el PR lleve la ola de arreglos), no el implementador de la Task 14 — coste si es erróneo: ninguno.
Task 14: minor (deferred): la fila «Rechazado» del manual funde dos ramas de probeMessage (URL inválida y handshake) en una frase.
Task 14: complete (commits 9c21bf5..e27a83b, review clean; puerta real, fusión y etiqueta pendientes del usuario por ruling)
Revisión final de la rama (998e412..e27a83b): «With fixes». 6 importantes, 9 menores, triage de diferidos (solo T11 antes de fusionar).
Ruling: POST /api/backup con sesión viva responde 409 «hay una emisión en curso» (misma regla que el planificador: VACUUM INTO retiene la única conexión) y se anota en el spec §7 — coste si es erróneo: el usuario tiene que esperar a terminar la emisión para respaldar.
Ruling: el 502 de /api/webhooks/{id}/test pasa de codeConflict a codeInternal (revierte el ruling de preflight: un cliente que ramifica por code no debe ver «conflict» en un fallo de entrega) — coste si es erróneo: ninguno, el conjunto de códigos no crece.
Ruling: el spec §2.1 se enmienda para decir que los intentos se cuentan desde el último reinicio del backoff (sesión sana ≥30 s), no «seguidos» en sentido literal — coste si es erróneo: ninguno, es describir lo implementado.
Ruling: los menores 7–15 de la revisión final entran en la misma ola de arreglos (todos son de una a cinco líneas); los diferidos por tarea que no marcó «antes de fusionar» se quedan diferidos.
Ola final: commits e27a83b..434afc4 (4). Ruling: el intento HTTP en vuelo del despachador usa context.WithoutCancel del contexto de Run (acotado por Timeout) para que el aviso de apagado se entregue; tras la cancelación no hay reintentos; run() espera hasta 10 s a las goroutines de fondo (presupuesto total de apagado 26 s < TimeoutStopSec=30) — coste si es erróneo: hasta 10 s más de apagado con un webhook colgado.
Ola final: extra aceptado: api.eventos() eliminado del cliente al quedarse sin usos (el endpoint GET /api/events se mantiene).
Ola final: re-revisión acotada e27a83b..57fb6ab: 16 hallazgos + 2b ADDRESSED, sin rotura nueva. Rama lista salvo puerta real, fusión y etiqueta (usuario).
Ruling: el directorio .superpowers/sdd de este plan se conserva (briefs, informes, paquetes de revisión), igual que los de la fase 3 y la vista previa; el ledger se copia además a docs/superpowers/plans/2026-09-09-confianza-produccion-ledger.md, que es lo que pide la Task 14 Step 5 — coste si es erróneo: ninguno.
Suite final sobre 57fb6ab: go vet limpio, 13 paquetes ok con -race (httpapi 108 s, relay 39 s), 495 tests, npm run build limpio.
