# SDD ledger — plan: docs/superpowers/plans/2026-09-12-internacional.md

Spec: docs/superpowers/specs/2026-09-12-internacional-design.md (autoridad vinculante), sobre el spec base y el plan maestro §8.
Rama: feat/internacional (desde main @ 1f2fdf7, merge de la v0.12; v0.12.0 etiquetada ahí, release 9/9 en verde). Commits previos: cf28ca5 (spec), 5f82f78 (plan).
Ejecución: 2026-09-12, subagent-driven, un implementador por tarea + revisión por tarea.

Ruling: se trabaja en el checkout actual sobre la rama, sin worktree (como en v0.8–v0.12) — coste si es erróneo: ninguno mientras no se toque main.
Ruling: la línea base es la CI en verde sobre 1f2fdf7 (PR #17) y la release v0.12.0 completa — coste si es erróneo: ninguno.
Ruling: el idioma de los errores viaja en el ResponseWriter envuelto (no en el context) para que `writeError` no cambie de firma en ~90 sitios; el envoltorio reexpone Flush/Hijack — coste si es erróneo: los tests de WebSocket lo detectan en la Task 1.
Ruling: el DTO de la ficha de sesión se llama `sessionDetailDTO` porque `sessionDTO` ya es «la sesión viva» del estado — coste si es erróneo: ninguno.
Ruling: «degradado» no se persiste como evento (solo métrica): el resumen habla de episodios de reconexión, no de minutos, y lo dice — coste si es erróneo: ninguno; persistir métricas es otra decisión (spec §6.2).
Ruling: sin runner de tests de componentes en el panel (sería una dependencia nueva): la red es el script de paridad + `npm run build` + revisión — coste si es erróneo: un texto olvidado en español se ve en la puerta.

## Preflight (tabla de conflictos)

| Par / tarea | Produce → consume | Hallazgo |
| --- | --- | --- |
| T1 texto | `writeError` sin cambio de firma; middleware en `Handler()`; envoltorio con `Flush`/`Hijack` | `webhook.go:83` hace `w.(http.Flusher)`: lo cubre el método `Flush` del envoltorio. Los WebSockets pasan por `srv.Handler()` en `chat_test.go`/`preview_ws_test.go`: si la subida falla, se ve ahí. |
| T1 texto | Test de AST exige ≥ 80 literales | Hay ~90 `writeError` literales + 19 mensajes del store: OK. Las plantillas con `+` se reconstruyen con `{n}`. |
| T2 → T3 | `SessionSummary.HasRecording`, `EventsBySession`, `ChatCountBySession` | Coinciden con `handleSessionDetail` y `newSessionSummaryDTO`. |
| T3 → T7 | `sessionDetailDTO{…, duration_s, events, recordings, chat_count, chat_by_platform}`, `has_recording` | El panel lee esos nombres (T7 Step 3). `recordingDTO` ya existe (`recording.go`). |
| T3 texto | `GET /api/sessions/{id}` junto a `GET /api/sessions/{id}/chat` | El mux de Go 1.22+ resuelve por especificidad: sin conflicto. |
| T4 → T5, T6, T7 | `t`, `idioma`, `cambiarIdioma`, `idiomas`, `formatearFecha`, `formatearNumero` | Coinciden. `quasar/lang/es` existe en `node_modules` (comprobado). |
| T4 texto | `package.json` gana solo `scripts.prebuild` | La restricción global dice «sin cambios» en `package.json`: se lee como «sin cambios en dependencias». Ruling: `prebuild` está permitido — coste si es erróneo: ninguno. |
| T5 ↔ T6 | `TarjetaDestino.vue` (T5) enseña el diagnóstico; `diagnosticar` cambia a `tituloKey` (T6) | T6 unifica y actualiza `TarjetaDestino.vue`: anotado en ambos briefs. |
| T7 texto | `Chat.vue` con `sesionId`; `api.chatSesion(id, after, limit)` existente | Coinciden (api.js:133). |
| T8 texto | `README.md` → inglés; `README.es.md` copia; anclajes | El plan pide `grep -rn "README.md#" docs deploy` y arreglar. |
| Puerta | Cambiar el idioma en el panel real; ver el historial de una sesión real; leer la comparativa | Del usuario; push y PR los hace el controlador tras la revisión final. |

## Progreso
BASE T1: 5f82f78
Ruling (T1): los `message` para personas que salen por writeJSON (liveResultDTO, testSkippedDTO, authStatusDTO, resultado de probar destino) también se traducen (spec §3.3): se añade en la ronda de arreglo de la T1 — coste si es erróneo: ninguno.
Task 1: review Spec ✅ / WITH FIXES — negociarIdioma sin normalizar «Q=»; writeJSON con texto para personas en live.go (liveResultDTO), platforms.go (authStatusDTO), test_destination.go (testSkippedDTO, probeDTO). Fix round 1 (resume implementer) + menores: inglés de «towards localhost», recolector con *ast.Ident.
Task 1: complete (commits 5f82f78..e080553, fix round 1 → re-review CLEAN; AST cubre 168 literales)
BASE T2: e080553
Task 2: complete (commits e080553..e474c2e, review clean por el controlador; scanEvent extraído en events.go)
BASE T3: e474c2e
Task 3: complete (commits e474c2e..4f528a0, review clean por el controlador)
BASE T4: 4f528a0
Task 4: complete (commits 4f528a0..546bd1d, review Spec ✅ / WITH FIXES solo diferible: import de @/i18n en cabecera de main.js → se hace en la Task 5)
BASE T5: 546bd1d
Task 5: implementada (546bd1d..d4de78f). Diferible para la ola final: el motivo de cierre del WebSocket de la vista previa (`ev.reason`, preview.go) llega en español fijo; traducirlo con el idioma negociado en la subida o mandarlo como código.
Task 5: complete (commits 546bd1d..d4de78f, review APPROVED; menores absorbidos por la Task 6: plurales de descartes/reconexiones en TarjetaDestino, aviso de VistaPrevia como computed)
BASE T6: d4de78f
Task 6: implementada (d4de78f..694b087). Ruling: `plataformas.js` `donde` («YouTube Studio → Crear → …») es copy para personas y se traduce (`dondeKey`) en la ronda de arreglo — coste si es erróneo: ninguno.
Task 6: review Spec ✅ / WITH FIXES — `donde` de plataformas.js en español dentro de frases en inglés (DialogoDestino 441/462). Fix round 1 (resume implementer).
Task 6: complete (commits d4de78f..e867fbb, fix round 1 → re-review CLEAN por el controlador; 363 claves)
BASE T7: e867fbb
Task 7: implementada (e867fbb..0a6cfa2). Ruling: bitrateLegible en el historial; panel.cargar() solo sin estado; iHistorial comparte glifo con iRegistro → cambiar a mdiClockOutline (o similar) en la ola final — coste si es erróneo: ninguno.
Task 7: review Spec ❌ / WITH FIXES — Sesion.vue sin watch sobre props.id (mezcla fichas al navegar); chat_count ausente de la cabecera. Fix round 1 (resume implementer) + glifo de iHistorial → mdiClockOutline.
Task 7: complete (commits e867fbb..fad8b12, fix round 1 → re-review CLEAN por el controlador; Chat con :key por sesión)
BASE T8: fad8b12
Task 8: implementada (fad8b12..573cda2). Ruling: siete celdas «no documentado» en la comparativa quedan así hasta que la persona que fusiona las verifique con navegador (está en la puerta del PR) — coste si es erróneo: ninguno; mejor vacío que inventado.
Task 8: complete (commits fad8b12..573cda2, review clean)
Revisión final de rama: despachada (opus) sobre 5f82f78..573cda2 (11 commits).
Revisión final: WITH FIXES — Important: envoltorio sin ReadFrom (A.3 sube), «Otro (RTMP/RTMPS)» sin traducir, recolector de AST permisivo con CallExpr/SelectorExpr. Ola final única despachada (opus) con final-fix-brief.md (A.1–A.3 + 4–14). Ruling: índice events(session_id,id) diferido a v0.14 (migración) — coste si es erróneo: fichas lentas con muchos eventos. BASE ola final: 573cda2
Ola final: implementada (573cda2..1eceb3a, 5 commits, 14/14 puntos). Rulings: los cuatro `html: true` de Panel.vue escapan sus datos (la premisa «único sitio» era falsa); store.EventsBySessionLimit exportada para el flag de truncado; recordings_truncated viaja en la API sin pintarse. Re-revisión acotada despachada.
Ola final: re-review CLEAN (573cda2..1eceb3a). Rama lista para PR.
