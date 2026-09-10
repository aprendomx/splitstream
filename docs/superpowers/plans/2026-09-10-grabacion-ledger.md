# SDD ledger — plan: docs/superpowers/plans/2026-09-10-grabacion.md

Spec: docs/superpowers/specs/2026-09-10-grabacion-design.md (autoridad vinculante), sobre el spec base 2026-09-01-rtmp-relay-design.md y el plan maestro §4.
Rama: feat/grabacion (desde main @ 9f4e704, merge de la v0.8). Commit de documentación previo: abda572 (spec + plan v0.9).
Ejecución: 2026-09-10, subagent-driven, un implementador por tarea + revisión por tarea.

Ruling: se trabaja en el checkout actual sobre la rama, sin worktree (misma razón que en la v0.8) — coste si es erróneo: ninguno mientras no se toque main.
Ruling: la línea base de record es la CI en verde sobre 9f4e704 (run 34485345869: vet, -race, panel, Docker e integración); la corrida local se lanza en segundo plano como confirmación pero la Task 1 no la espera, porque el código es idéntico — coste si es erróneo: ninguno.
Ruling: fMP4 (B.4 del plan maestro) queda para la v0.9.1 con spec propio; esta entrega es FLV — coste si es erróneo: los archivos requieren `ffmpeg -c copy` para pasar a MP4, documentado.

## Preflight (tabla de conflictos)

| Par / tarea | Produce → consume | Hallazgo |
| --- | --- | --- |
| T1 → T6, T7, T9 | `relay.RecorderSinkID`, `SinkProvider(sessionID)` → `BuildRecorder`, `recordingStatus`/`fakeRecorder`, test de integración | Coinciden. T1 cambia `main.go` y T6 lo vuelve a tocar en el mismo sitio (previsto: T6 «sustituye al SetSinkProvider de la Task 1»). |
| T2 → T4 | `WriteHeader`, `WriteTag`, `leerFLV` (test) → `FLVWriter` y sus tests | Coinciden; `leerFLV` vive en `flv_test.go` del mismo paquete. |
| T3 → T4, T6 | `Quota.Check(dir, extra)`, `ErrDiskFull`, `FreeSpace` → writer y fábrica; `FreeSpace` también en T7 (`freeBytes`) | Coinciden. `Check` ignora un dir no consultable: T3 lo testea. |
| T4 → T6 | `Options{OnOpen, OnSegment, OnDiskWarning}`, `Segment` → `BuildRecorder` | Coinciden. `OnOpen(path, index, startedAt)` y `Segment{Path, Index, StartedAt, EndedAt, Bytes, DurationMS}`. |
| T5 → T6, T7 | `OpenRecording/FinishRecording(path…)`, `ListRecordings`, `RecordingByID`, `DeleteRecording`, `RecordingsTotalBytes`, `CountSessionRecordings`, `PruneRecordings(…, remove)`, `RecordingSettings/Update…`, `ErrRecordingInProgress` | Coinciden; `DeleteRecording` no borra archivos (lo dice el comentario) y T7 los borra antes. |
| T5 ↔ Global | 0006 sin ALTER; `INSERT OR IGNORE`; `SchemaVersion=6`; `db_test` lista de tablas | Consistente con los tests que rebobinan `user_version`. |
| T5 texto | `abrirYcerrar` con `sesion == 0` → `session_id` NULL; `OpenRecording` lo implementa | Consistente. `TestListRecordingsFiltersBySessionAndPaginatesByID` usa `string(rune('0'+i))`: válido para i ≤ 9. |
| T6 → T7, T9 | `Factory.SetRecordingsDir`, `BuildRecorder`, `PruneRecordings` → `httpapi.RecorderBuilder`, `main.go`, integración | Coinciden. |
| T6 texto | `recorderEvent`: `destination_disconnected` con `ErrDiskFull` en el mensaje → `recording_stopped_disk_full` una vez | El mensaje del sink es «el destino se desconectó: » + err.Error(); `ErrDiskFull.Error()` es un substring: OK. |
| T7 → T8 | `statusDTO.recording{…}`, endpoints → `panel.grabacion`, `Ajustes.vue`, `Grabaciones.vue` | Coinciden (campos `active, degraded, bytes, segments, used_bytes, max_bytes, free_bytes, dir`). |
| T7 texto | `recordingPath` comprueba el prefijo del raíz; `s.recDir == ""` en los tests que no lo fijan → descarga 404 | Solo los tests que fijan `srv.recDir` descargan: OK. |
| T7 ↔ Global | `internal/httpapi` importa `internal/record` (por `FreeSpace`): permitido por el plan; `record` no importa rtmpio/go-rtmp | OK. |
| T8 texto | `Ajustes.vue` usa `computed` (añadir al import) y `bytesLegibles`; `Panel.vue` importa `iGrabar` | Anotado en el brief. |
| T9 ↔ CI | El job de integración pasa a exigir 4 tests | Anotado. En local solo corre el de grabación (sin mediamtx). |
| T14-like | La puerta (1 h, kill -9, disco lento) y la etiqueta son del usuario | Ruling: como en la v0.8, el push y el PR los hace el controlador tras la revisión final; puerta, fusión y etiqueta quedan para el usuario — coste si es erróneo: ninguno. |

## Progreso
v0.8.0 etiquetada sobre 9f4e704 y publicada por release.yml (run 34485725935, 6 artefactos).
Línea base local sobre abda572 (con la Task 1 ya en marcha en paralelo): go vet limpio, 13 paquetes ok con -race, exit 0.
Task 1: complete (commits abda572..9d9d9f7, review clean)
Task 2: complete (commits 9d9d9f7..1bd11ff, review clean)
Task 3: complete (commits 1bd11ff..fcfa0f5, review clean; GOOS=windows go vet verificado por el revisor)
Task 4: complete (commits fcfa0f5..af91ad9, review clean; la nota «nueve tests» del enunciado de revisión era errónea: son ocho)
Task 5: complete (commits af91ad9..6c45b9c, review clean)
Task 6: minor (deferred): la variable local sinks sombrea al paquete en el closure del provider de main.go (heredado del plan); el evento recording_pruned del catálogo no existe: el job lo resume dentro de maintenance_ran (corregir el spec §4.2 en la Task 9).
Task 6: complete (commits 6c45b9c..952fab8, review clean)
Task 7: minor (deferred): handleDeleteRecording borra la fila sin intentar el archivo si recordingPath rechaza el path (heredado del plan); fakeRecorder.nilSink sin usar en los tests.
Task 7: complete (commits 952fab8..7cc472f, review clean)
Task 8: minor (deferred): variable tope sin usar en usoGrabacion (heredada del plan); validación numérica solo por atributos HTML.
Task 8: complete (commits 7cc472f..83c78e1, review clean; el implementador corrigió un import de bytesLegibles que el brief daba por hecho)
Task 9: minor (deferred): docs/lanzamiento.md conserva «Escrito para la v0.8.0» (se cambiará al etiquetar la v0.9.0).
Task 9: complete (commits 83c78e1..7e7e031, review clean)
Revisión final de la rama (abda572..7e7e031): «With fixes». 1 crítico, 2 importantes, 9 menores; diferidos: solo la variable tope antes de fusionar.
Ruling: crítico #1 (filas en curso huérfanas tras kill -9) se resuelve con una reconciliación al arrancar: store.CloseDanglingRecordings + fábrica, que cierra la fila con el tamaño y la fecha del archivo o la borra si el archivo no está — coste si es erróneo: ninguno, solo toca filas con ended_at NULL al arrancar, cuando no hay sesión.
Ruling: importante #2 (sin re-comprobación de cuota con segment_min=0): checkQuota también cada 64 MiB escritos en media(); el spec §4 se enmienda para decirlo — coste si es erróneo: un statfs por cada 64 MiB, despreciable.
Ruling: importante #3: la CI gana un paso que exige que internal/record dependa solo de relay, flv y record dentro del módulo, y un build cruzado GOOS=windows (menor #12) — coste si es erróneo: ninguno.
Ruling: menores #4 (underflow uint32), #5 (MaxBytes() en RecordingSettings), #7 (aviso 80 % una vez por sesión, en la fábrica), #8+#9 (índice de segmento continuado desde la cuenta de la sesión, que además evita la colisión de nombre en el mismo segundo), #11 (encender con la grabación ya encendida pero sin sink vivo la arranca), y la variable tope entran en la misma ola — coste si es erróneo: ninguno.
Ruling: menor #6 (tres consultas por push de estado) y #10 (carrera entre liveSession y el fin de sesión, patrón preexistente del alta en caliente) quedan diferidos con nota — coste si es erróneo: trabajo extra por segundo con muchas pestañas abiertas; residuo de archivo en una carrera de milisegundos que la reconciliación del arranque limpia.
Ola final: commits 7e7e031..4984923 (6). Suite completa sobre 4984923: go vet limpio, 14 paquetes ok con -race (httpapi 111 s, relay 39 s), 543 tests, npm run build limpio.
Ola final: re-revisión acotada 7e7e031..4984923 (sonnet): crítico #1, importantes #2 y #3, menores #4, #5, #7, #8+#9, #11, la variable tope y las enmiendas del spec: ADDRESSED; sin roturas nuevas. Diferidos con nota: menores #6 y #10.
Cierre: rama lista para PR sobre main. Puerta (1 h, segmentos de 10 min, kill -9, cero descartes con disco lento), jobs integration y docker de la CI, fusión y etiqueta v0.9.0 quedan para el usuario; al etiquetar, actualizar la cabecera de docs/lanzamiento.md.
