# SDD ledger — plan: docs/superpowers/plans/2026-09-02-fase-2-motor-rtmp.md

Spec: docs/superpowers/specs/2026-09-01-rtmp-relay-design.md (autoridad vinculante; §16 = resultado del spike)
Rama: feat/fase-2-motor-rtmp (desde feat/fase-1-esqueleto, que sigue sin fusionar a main por decisión del usuario)
Base: 0315bf1

## Lecciones de la fase 1 que se aplican a todos los despachos
1. NUNCA `go mod tidy` — recorre las deps de test de modernc/sqlite (libc→ccgo→gc/v2→gc/v3). Mató 3 agentes.
2. Los revisores NO deben crear módulos Go fuera del árbol del repo: eso dispara la descarga de sqlite por red y cuelga. Trabajar dentro del módulo es instantáneo.
3. Cualquier comando que pueda pasar de 2 min va en segundo plano o con timeout ampliado.
4. El controlador no usa TaskOutput sobre agentes locales (vuelca el transcript entero).

## Escaneo previo de conflictos

### Pares de tareas que comparten archivo o interfaz

| A → B | Produce / consume | Hallazgo |
|---|---|---|
| T1 → T2 | Ambas tocan `events.go`. T1 cambia `d.db.` por `d.ex.`; T2 añade `SessionByID` | OK con el orden 1→2. **Riesgo:** el implementador de T2 debe usar `d.ex`, no `d.db`. Se avisa en el brief. |
| T1 → T5,T8 | `InTx` | No lo usa la fase 2; queda listo para la 3. |
| T3 → T7 | `flv.InspectVideo/InspectAudio`, `CodecIDAVC`, `SoundFormatAAC` | OK. |
| T4 → T5,T6,T8 | `Message`, `Kind`, `Publisher`, `Preamble`, `timebase` | OK. `timebase` no exportado, solo lo usa `sink.go` del mismo paquete. |
| T5 → T8 | `Sink`, `Hub`, `State`, y los helpers de test `waitFor`, `videoKey`, `videoInter`, `audioRaw`, `preambleWith`, `fakePublisher` | OK. **Riesgo:** el test de T8 (`engine_test.go`) los consume; el implementador NO debe redefinirlos. Todos los tests del paquete son `package relay` (internos), no `relay_test`. |
| T4 → T5 | helper de test `msg(...)` en `preamble_test.go` | Sin colisión con `videoKey`/`videoInter`/`audioRaw` de T5. |
| T6 → T7 | Mismo paquete `rtmpio`; T6 añade go-rtmp a go.mod, T7 lo usa | OK. Helpers de test: `contains` (T6) y `recorder` (T7), sin colisión. |
| T6,T7 → T8 | `rtmpio.NewPublisher`, `PublisherConfig`, `NewIngest`, `IngestConfig`, `IngestHandler`, `ErrBadStreamKey` | OK. `*relay.Engine` satisface `rtmpio.IngestHandler` estructuralmente. |
| T8 → repo | `main.go`, `Makefile`, `test/integration/` | Ninguna otra tarea los toca. |

### Autoconsistencia por tarea

| Tarea | Hallazgo |
|---|---|
| T1 | Consistente. Ver la Ruling de abajo sobre `ReorderDestinations`. |
| T2 | Consistente. `Session.Width/Height/BitrateBPS` a `*int`; `database/sql` ya escanea a `**int` (verificado en la fase 1). |
| T3 | Consistente. 10 tests cubren keyframe, seq header, enhanced-RTMP, no-AVC, no-AAC y payload vacío. |
| T4 | Consistente. El `timebase` descarta lo anterior a la base, que es el punto del spec §3.2. |
| T5 | Consistente. `fakePublisher.write` espera en `blockWrites` ANTES de tomar el mutex; si lo hiciera después, `snapshot()` se bloquearía y el test de no-bloqueo colgaría. |
| T6 | Consistente. `var _ relay.Publisher = (*Publisher)(nil)` no choca con el tipo local `Publisher` porque va cualificado. |
| T7 | Consistente. Firmas verificadas contra go-rtmp v0.0.7 real, no supuestas. |
| T8 | Consistente. `EngineStore` es la indirección que mantiene a `internal/relay` sin importar `internal/store`. |

### Choques con las Global Constraints

| Constraint | Estado |
|---|---|
| Solo 3 directas al terminar (sqlite, x/crypto, go-rtmp) | OK: solo T6 añade una. |
| `internal/relay` sin `go-rtmp` ni `database/sql` | OK por diseño: `EngineStore` y `Publisher` son interfaces. El DoD lo verifica con `go list -deps`. |
| Piso `go 1.25.0` | Riesgo: `go get go-rtmp` podría subirlo. Se avisa en el brief de T6 de detenerse si ocurre. |
| Sin secretos en logs | OK: la clave viaja como `crypto.Secret` y el Publisher la loguea enmascarada. |

**Resultado del escaneo:** un solo hallazgo que exige resolución, abajo.

## Rulings previas a la ejecución

Task 1: Ruling: reescribir `ReorderDestinations` para que use `InTx` cambia su comportamiento en un caso: llamarla DENTRO de otro `InTx` ahora devuelve `ErrNestedTransaction` en vez de funcionar. Lo acepto. Razón: ningún llamador actual lo hace, la fase 4 la llamará directamente desde su handler, y con una sola conexión la alternativa a un error claro es un cuelgue silencioso — que es exactamente la clase de fallo que el `execer` viene a eliminar. COSTE SI ME EQUIVOCO: si la fase 4 necesitara reordenar dentro de una transacción mayor, habría que extraer el cuerpo a un método privado que reciba el `*DB` de la transacción; son ~10 líneas.

## Progreso

Task 1: complete (commits 0315bf1..bc4e602, spec ✅, calidad aprobada). El revisor verificó por ejecución llamando a los 17 métodos públicos del store dentro de un solo InTx con timeout de 4s: terminó en 0.01s, sin cuelgue. También confirmó rollback y commit reales observando el estado de la base desde fuera de la transacción, el rechazo del anidamiento sin colgarse, y que ReorderDestinations no sufrió regresión (incluido el caso de ids repetidos que la fase 1 arregló). 37/37 tests.
Task 1: minor (deferred): InTx descarta el error de tx.Rollback(). Idiom común en Go y sin escenario de fallo observable aquí.
Task 1: ⚠️ resuelto por el controlador — SessionByID no existía porque lo añade la Task 2. No es hueco.
Task 1: NOTA PARA LA FASE 3 — el revisor no pudo ejercitar contención real: varias goroutines abriendo InTx a la vez contra la única conexión. La fase 3 pone una goroutine por destino escribiendo eventos, así que ahí sí hará falta el test de concurrencia que la revisión de la fase 1 ya pedía (`-race` hoy no verifica nada porque ningún test lanza goroutines contra el store).
Task 2: complete (commits bc4e602..fbe4358, spec ✅, calidad aprobada, sin hallazgos). Verificado por ejecución: SessionByID usa d.ex y no se cuelga dentro de un InTx; los tres casos de nulabilidad correctos; sql.ErrNoRows traducido a ErrSessionNotFound sin repetir la fuga que la fase 1 señaló en Settings()/RevealIngestKey; sin regresión en FinishSession. El revisor confirmó además que ID y StartedAt siguen sin ser punteros porque sus columnas son NOT NULL: no queda ningún campo que no pueda representar su columna.
Task 3: complete (commits fbe4358..0c8701e, spec ✅, calidad aprobada). El revisor verificó las máscaras contra la especificación de FLV recorriendo los 256 valores del primer byte con una referencia calculada aparte del código bajo prueba. Confirmó que `(b>>4)&0x07` es correcto y no un bug: en enhanced-RTMP el FrameType es de 3 bits, así que usar 0x0f haría que NINGÚN keyframe enhanced se detectara. También confirmó que ninguna combinación clásica legítima activa el bit 0x80.
Task 3: minor (deferred) — CON ACCIÓN EN LA TASK 7: `VideoInfo.CodecID` no significa nada cuando `IsEnhanced == true`; ahí el nibble bajo es el PacketType de enhanced-RTMP. El byte 0x87 da CodecID=7, que coincide por casualidad con CodecIDAVC. Quien lea CodecID sin comprobar antes IsEnhanced trataría un tag HEVC como H.264 clásico. El código de la Task 7 en el plan YA comprueba IsEnhanced primero, pero depende del orden y no del sistema de tipos. ACCIÓN: avisar explícitamente al implementador de la Task 7.
Task 4: complete (commits 0c8701e..ba42199, spec ✅, calidad aprobada). Verificado por ejecución: `translate` no desborda con base=MaxUint32/ts=0 ni con base=0/ts=MaxUint32 (la comparación es en uint32 ANTES de restar, así que nunca se llega a la resta que subdesbordaría); `Preamble` limpio bajo -race con 2000 Observe concurrentes contra 4 Snapshot y 1 Reset; los dos switches de `Observe` son coherentes entre sí.
Task 4: minor (deferred) — CON ACCIÓN EN LA TASK 5: el contrato de inmutabilidad documenta explícitamente solo `Payload`, pero `Snapshot` devuelve el puntero VIVO al `*Message` cacheado. Un consumidor que reasignara `Timestamp` o `IsKeyframe` en vez de solo leer corrompería el preámbulo para todos los sinks, y sin mutex sería además una data race. ACCIÓN: avisar al implementador de la Task 5 de que el sink NO debe mutar los mensajes; debe construir uno nuevo o pasar solo el payload.
Task 4: NOTA PARA LA FASE 3 — nadie cubre el wraparound real de los timestamps RTMP de 32 bits, que ocurre a los ~24,8 días de sesión continua. Fuera de alcance para un servicio personal, pero conviene decidirlo conscientemente al escribir la reconexión.
Task 5: spec ✅, calidad NO aprobada en la primera pasada. 1 Critical (fuga de goroutine y de conexión cuando falla Connect o una escritura: `<-s.quit` sin seleccionar sobre ctx.Done(), y el defer que cierra el Publisher nunca corría) + 1 Important (Hub.Add dejaba una ventana de escritura doble al mismo endpoint RTMP porque `go old.Stop()` es asíncrono).
Task 5: Ruling: los dos son plan-mandated — el código defectuoso venía de mi brief y el implementador lo transcribió bien. Fallo A FAVOR DE AMBOS HALLAZGOS. Razón: los dos son load-bearing para la fase 3, que es donde los destinos se reconectan y se editan en caliente; la fuga acumularía una goroutine y una conexión por cada intento fallido, y la ventana de solapamiento se dispararía justo al editar credenciales de un destino. Ambos se reprodujeron por ejecución (NumGoroutine para el primero, State del sink viejo tras Add para el segundo), no por lectura. Plan corregido para no reintroducirlos. COSTE SI ME EQUIVOCO: el sink ya no espera en el camino de fallo, así que su estado se queda en error en vez de idle; si la fase 3 esperara idle ahí, son 3 líneas.
Task 5: fix round 1/5 (2 addressed, 0 open; commits cd8038a..4978ceb) — ADDRESSED verificado por ejecución: NumGoroutine 2→2 en 10 iteraciones sin llamar a Stop; el Publisher se cierra en ambos caminos de fallo; el estado de error se conserva tras un Stop posterior; el camino ordenado sigue acabando en StateIdle; el sink viejo ya no está live al volver de Add y su Publisher está cerrado; y SIN DEADLOCK con Publish concurrente mientras se reemplaza (Add suelta el mutex antes de llamar a Stop). Suite con -race -count=20 limpia en dos corridas.
Task 5: complete (commits ba42199..4978ceb, spec ✅, calidad aprobada tras 1 ronda)
Task 6: spec ✅, calidad con 1 Important. La revisión verificó por ejecución que NO hay fuga de la stream key (provocó errores de URL, DNS inexistente, handshake a medias, Close sin Connect, capturó slog a un buffer y revisó cada .Error()), que rtmps nunca puede acabar en Dial sin TLS, que ServerName se extrae sin el puerto, que no hay InsecureSkipVerify, y que Close es idempotente y sin data race.
Task 6: Ruling: el Important (Connect ignoraba el ctx) es plan-mandated. Fallo A FAVOR DEL HALLAZGO. Razón: un destino tras un firewall que descarta paquetes cuelga Connect durante el timeout TCP del sistema, cancelar el contexto no hace nada, y en la fase 3 eso bloquearía el bucle de reconexión entero. COSTE SI ME EQUIVOCO: ~35 líneas de dial envuelto en goroutine y select; se revierte a rtmp.Dial directo.
Task 6: fix round 1/5 — ADDRESSED parcial. Verificado 301 ms con ctx de 300 ms. PERO la re-revisión encontró que connectTimeout no acotaba nada por sí solo: Connect(context.Background()) no retornaba nunca, porque net.Dialer.Deadline solo cubre el dial TCP y la rama ctx.Done() jamás dispara sin plazo. Y ese ES el camino de producción: main.go pasa el contexto de señales, cancelable pero sin deadline.
Task 6: Ruling: abrir ronda 2 en vez de parquear. Razón: el mecanismo llevaba el nombre de un límite, tenía un comentario prometiéndolo, y no limitaba nada en producción; y el arreglo son dos líneas. COSTE SI ME EQUIVOCO: despreciable.
Task 6: fix round 2/5 — ADDRESSED, verificado por ejecución con los tres tiempos: 15,002 s sin plazo del llamante (acotado por connectTimeout), 0,30 s con plazo de 300 ms (la precedencia del llamante sigue mandando), y 2,16 ms en el camino feliz contra un servidor go-rtmp REAL con handshake, createStream, negociación de chunk size 128→4096, releaseStream/FCPublish, publish y WriteVideo. El comentario del código ya coincide con lo que hace.
Task 6: complete (commits 4978ceb..50f914d, spec ✅, calidad aprobada tras 2 rondas)
Task 6: NOTA — go-rtmp arrastra 2 transitivas más de las que el spec listaba (hashicorp/errwrap, hashicorp/go-multierror). `go mod why` confirma que llegan por go-rtmp. Spec actualizado.
Task 7: spec ✅, calidad NO aprobada. 1 Critical: Ingest.Close() no cierra las conexiones activas pese a que su comentario lo afirma. rtmp.Server.Close() de go-rtmp v0.0.7 solo cierra el listener; las conexiones aceptadas viven en goroutines que la librería no rastrea. Verificado por ejecución: tras Close(), el publisher escribió 10 frames en 1,5 s sin error y OnPublishEnd nunca se llamó.
Task 7: Ruling: plan-mandated (el código venía de mi brief). Fallo A FAVOR DEL HALLAZGO. Razón: con SIGTERM, un OBS conectado seguiría publicando contra un proceso cerrado, y el Hub y el Publisher de salida hacia la plataforma quedarían huérfanos. El arreglo es rastrear las conexiones nosotros desde ServerConfig.OnConnect, que sí nos entrega el net.Conn. COSTE SI ME EQUIVOCO: ~40 líneas de registro y un mapa; se revierte quitando el track/untrack.
Task 7: verificado limpio por el revisor: los 256 bytes con el bit 0x80 devuelven ErrUnsupportedCodec (incluido 0x87, que da CodecID=7 por casualidad); el payload se copia en los tres caminos; OnPublishEnd se llama exactamente una vez y NO se llama si el publish fue rechazado ni si la conexión nunca publicó; el error de autenticación no distingue app de clave y no loguea la clave.
Task 7: NOTA PARA LA FASE 3 — `Stream.Publish` de go-rtmp es fire-and-forget: no espera el `onStatus`. Si la plataforma rechaza el publish (clave mala), `Publisher.Connect` devuelve nil y el error solo aflora 1-2 escrituras después como "broken pipe". El sink lo verá como fallo de escritura y reconectará, pero con una clave incorrecta eso es un bucle de reintentos infinito. La fase 3 debería contar reintentos fallidos consecutivos y parar tras N, o mirar el onStatus.
Task 7: NOTA PARA LA FASE 2, TASK 8 — go-rtmp v0.0.7 tiene una carrera propia en su CLIENTE (client_conn.go, entre (*streams).Delete y su goroutine de lectura) que aflora al llamar a Publisher.Close() en un test de punta a punta. Puede hacer fallar el test de integración bajo -race. Si aparece, es de la librería, no nuestra: hay que decidir qué hacer, no parchearla a ciegas.
Task 7: fix round 1/5 — ADDRESSED verificado por ejecución. OnPublishEnd llega 6,4 ms después de Close (antes: nunca). 8/10 escrituras posteriores fallan (las 2 que pasan son buffering TCP del kernel antes del RST, esperado). untrack deja len(conns)=0 tras 8 ciclos connect/disconnect. La carrera track/Close aguantó ~11.000 intentos de conexión desde 6 goroutines bajo -race sin supervivientes ni reportes. OnPublishEnd sigue disparándose exactamente una vez.
Task 7: complete (commits 50f914d..57870b9, spec ✅, calidad aprobada tras 1 ronda)
Task 7: NOTA IMPORTANTE PARA LA TASK 8 — la carrera interna de go-rtmp NO se reprodujo: ~24 pasadas con -race entre implementador y re-revisor, sin un solo DATA RACE ni en nuestro código ni dentro de la librería. El aviso queda registrado por si aflora en el test de integración, pero no es un bloqueo esperado.
Task 8: spec ✅, calidad NO aprobada. 1 Critical: el apagado perdía el cierre de sesión. `ingest.Close()` corta los sockets, pero go-rtmp atiende cada conexión en su propia goroutine y es esa la que dispara OnPublishEnd; el wg solo cubría la de ListenAndServe. Reproducido en 3 corridas: una completa, una con FinishSession hecho pero evento perdido, y una donde el proceso salió ANTES de FinishSession dejando la sesión abierta para siempre con ended_at NULL, exit 0 y sin errores.
Task 8: el revisor VALIDÓ EL TEST DE INTEGRACIÓN rompiendo el motor a propósito (OnMessage a no-op) y confirmando que se pone rojo. No da falsa confianza.
Task 8: Ruling: plan-mandated. Fallo A FAVOR DEL HALLAZGO. Razón: es pérdida silenciosa de datos en cada reinicio con stream activo, y en la fase 4 el GET /api/status mostraría sesiones "en vivo" que terminaron hace semanas. COSTE SI ME EQUIVOCO: una espera de hasta 5 s en el apagado; verificado que sin publisher activo el apagado sigue siendo instantáneo.
Task 8: fix round 1/5 — ADDRESSED, 8 de 8 corridas manuales (mínimo pedido: 5). ended_at no nulo, exactamente 1 evento publisher_disconnected, exit 0, apagado en ~0,11 s. Sin publisher activo el apagado tarda 4,6–68 ms, no los 5 s del límite. Sin deadlock entre WaitIdle y OnPublishEnd bajo -race con un store lento y 8 goroutines consultando SessionID(). Test de integración sigue pasando con h264/aac/640x360.
Task 8: NOTA — el implementador encontró que el arreglo de mi archivo de hallazgos NO bastaba: OnPublishEnd ponía sessionID=0 al principio, así que WaitIdle daba el visto bueno con las escrituras en vuelo. Lo reprodujo y reordenó para que sessionID pase a 0 solo al final, soltando el mutex durante la I/O. Correcto y necesario; el re-revisor lo confirmó.
Task 8: complete (commits 57870b9..493af84, spec ✅, calidad aprobada tras 1 ronda)

## Revisión final de rama (fase 2)
Veredicto: NO fusionable. 3 bloqueantes, todos plan-mandated.
- B1 (Critical): el sink nunca se reinicia entre sesiones de ingesta. OBS Stop→Start —la acción más común— hace que el timeline salte hacia atrás (599967 → 0) y que el AVC sequence header de la segunda sesión NUNCA llegue al destino, porque tb.started() sigue true y el header nuevo con ts=0 muere en translate. Con cambio de resolución, vídeo corrupto. El sink queda en live, 0 descartes, sin error. Es una violación de la §6.5 del spec que no implementé.
- B2 (Critical): OnPublishStart no comprueba sessionID. Con TCP medio abierto (red caída sin FIN) y OBS reconectando, se aceptan dos sesiones: frames de dos codificadores intercalados en un solo stream de salida, la primera sesión con ended_at NULL para siempre, y el OnPublishEnd de la conexión muerta cerrando la sesión de la conexión viva.
- B3 (Important): la §15.6 del spec estaba asignada a la fase 2 y no se hizo. Sin validación de esquema, un destino con URL http:// queda silenciosamente sin retransmitir.
- C1: el comentario de la interfaz Publisher afirma que Close puede llamarse desde otra goroutine. Es FALSO: el ChunkStreamer de go-rtmp comparte un encoder sin mutex. La fase 3 se lo creería al implementar reconexión.
- C2: go-rtmp loguea las claves a nivel Info; solo estamos a salvo porque dejamos ConnConfig.Logger en nil. Propiedad de seguridad sostenida por una omisión: hay que escribirla.
- C3: NewPublisher loguea la URL del destino en claro. Si el usuario pega la clave dentro de la URL —error frecuente— acaba en todos los logs.
Ruling: fallo A FAVOR de los tres bloqueantes y aplico también C1-C3. Razón: B1 se dispara con la acción más común del usuario y produce vídeo corrupto sin ninguna señal; B2 corrompe el stream de salida con una caída de red normal; B3 estaba explícitamente asignada a esta fase; y C1-C3 son comentarios y logs que la fase 3 se creería o que filtran claves. COSTE SI ME EQUIVOCO: los sinks se reconectan a cada sesión en vez de mantenerse abiertos, lo que añade una latencia de conexión al empezar cada transmisión — que es exactamente lo que el spec §6.5 pide.
Oleada final: commit 4d1ddbe. Los 6 hallazgos ADDRESSED, verificados por ejecución por el re-revisor:
- B1: dos conexiones RTMP distintas publicando en mediamtx (conn 56100 y 41169), con "2 tracks (H264, MPEG-4 Audio)" reconocidos LAS DOS VECES. El AVC seq header de la segunda sesión sí llega y el timeline arranca en 0.
- B2: segundo publisher rechazado con ErrSessionInProgress; solo se acepta otro tras OnPublishEnd.
- B3: validateRTMPURL rechaza http://, sin esquema, sin host, sin app y vacío, en Create y Update.
- C1/C2/C3: comentarios corregidos y la URL cruda ya no se loguea (grep confirma que cfg.URL solo aparece en parseTarget).
Prueba de SIGTERM: 6 de 6 corridas con ended_at no NULL y el evento persistido. El orden de OnPublishEnd (FinishSession → logEvent → hub.Close() → sessionID=0) se mantuvo: no se reintrodujo la carrera de apagado.
Sin daño nuevo. Sin deadlock (Sink.run no toca el mutex del Engine; hub.Close() se llama fuera de e.mu). Un fallo del provider solo loguea y no rechaza al publisher.
VEREDICTO FINAL: lista para fusionar.
Fuera de alcance para el ledger: el test de integración imprime al final un "database is closed" al registrar el evento — carrera preexistente en el orden de los defer DEL PROPIO TEST, no del código. El test sigue en PASS. PARA LA FASE 3: ordenar los defer del test para que la base se cierre después de la ingesta.
