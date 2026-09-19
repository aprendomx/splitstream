# SDD ledger — plan: docs/superpowers/plans/2026-09-17-camara-navegador.md

Spec: docs/superpowers/specs/2026-09-17-camara-navegador-design.md (leído; es la autoridad).
Rama: feat/camara-navegador desde main (80d0873). Sin worktree.

Ruling: trabajar en la rama en el propio checkout, sin worktree — los seis planes SDD anteriores de este repo (.superpowers/sdd/*) lo hicieron así y no hay .worktrees; el usuario no está disponible para consentir uno — coste si es erróneo: ninguno en git (la rama aísla igual), solo el árbol de trabajo compartido.
Ruling: Tasks 1–3 se despachan en UN solo implementer (mismo paquete flv, código completo en el brief, tres commits) y se revisan como una unidad — son transcripción del mismo tipo — coste si es erróneo: una revisión más grande.

## Pre-flight scan

| Par / tarea | Produce → consume | Hallazgo |
| --- | --- | --- |
| T1 → T6 | Wrap{Video,VideoSeqHeader,Audio,AudioSeqHeader} firmas | coinciden con las llamadas de camera.go |
| T2 → T6 | AudioSpecificConfig([]byte) ([]byte, error) | coincide |
| T3 → T6 | flv.Meta campos y OnMetaData(Meta) ([]byte, error) | coinciden; T6 test importa go-amf0 (solo test; la frontera de CI mira go list -deps sin tests) |
| T4 → T5 | StartLocalSession/OnMessage/OnPublishEnd, LiveSession.Source, SourceBrowser/SourceRTMP | coinciden |
| T5 → T6 | fakeEngine: setStartErr, sesionesLocales, terminadas, mensajes; sessionDTO.Source | coinciden |
| T6 → T8 | tipos 0x00–0x04, JSON de start (width,height,framerate,video_bitrate,audio_bitrate,sample_rate,channels), confirmación texto, layouts de frame | coinciden |
| T7 → T8 | CODEC_VIDEO, CODEC_VIDEO_1080, CODEC_AUDIO exportados en soporte.js | coinciden |
| T7 → T9 | Camara.vue: T9 sustituye emitir()/parar() literales y añade tras `videoEl.value.srcObject = stream`; usa listo, conectando, motivoFin, videoEl, stream, calidad, emitiendo | todos definidos en T7; dos onMounted/onBeforeUnmount es válido en Vue |
| T5 → T10 | httpapi.Config{DB,Cipher,Engine,MasterKey}; Logger nil → slog.Default | coincide |
| T10 | adapter, sinkA, testCipher, requireTool, requireSink, probeStream existen en el paquete integration | sí |
| T1 | tests vs código: bytes exactos, round trip, ParseResolution con avcC del fixture | consistente |
| T2 | esds de Safari verificado a mano contra el parser; el caso corto suma 25/17 | consistente |
| T3 | go-amf0 codifica ECMAArray y decodifica en *amf0.ECMAArray (verificado en scratch) | consistente |
| T4 | test usa fakeStore.events/ended existentes | consistente |
| T6 | dialPreview(ctx,url,cookies,lang) existe en preview_ws_test.go | consistente |
| Rúbrica | ningún test sin aserciones; startSession de T4 evita duplicar OnPublishStart | limpio |

Baseline: go build/vet OK; go test ./... -race: 23 paquetes ok, 0 fallos (80d0873).
Tasks 1–3: implementer despachado (haiku), BASE 80d0873.
Task 1: complete (commits 80d0873..6eae288, review clean)
Task 2: complete (commits 6eae288..5cc7da6, review clean)
Task 2: minor (deferred): AudioSpecificConfig devuelve el slice de entrada sin copiar en el camino ASC desnudo (plan-mandated; WrapAudioSeqHeader copia después)
Task 2: minor (deferred): TestAudioSpecificConfigRejectsTruncatedESDS solo corta 1–31 de 39 bytes
Task 3: complete (commits 5cc7da6..996bd03, review clean)
Task 4: implementer despachado (sonnet), BASE 996bd03.
Ruling: los commits llevan el trailer Co-Authored-By vía commit -F (el hook bloquea comandos largos); los 4 commits ya hechos sin trailer (6eae288, 5cc7da6, 996bd03, 9dbe33b) se reescriben en Task 12 antes de cerrar la rama — coste si es erróneo: reescritura de historia local no publicada.
Task 4: complete (commits 996bd03..9dbe33b, review clean)
Task 4: minor (deferred): carrera preexistente en startSession entre la re-comprobación de sessionID y la escritura tras store.StartSession (ventana ahora más pequeña; con dos entradas, OBS y cámara, es más plausible) — candidata a bandera «arrancando» bajo el mutex
Task 4: minor (deferred): parámetro `app` de startSession recibe el literal "browser" para el log
Task 5: implementer despachado (sonnet), BASE 9dbe33b.
Nota: c5a4ea7 lleva trailer "Claude Sonnet 5"; se normaliza a Fable 5.1 en la reescritura de Task 12.
Task 5: complete (commits 9dbe33b..c5a4ea7, review clean)
Task 5: minor (deferred): sessionDTO.Source es string sin omitempty (serializa "" sin sesión); plan-mandated, cosmético
Task 6: implementer despachado (sonnet), BASE c5a4ea7.
Task 6: review ❌ — Important (plan-mandated): el código 4004 es inalcanzable porque r.Context() no se cancela tras el hijack ni en Shutdown; una cámara viva no tiene camino de apagado y WaitIdle agota su plazo.
Ruling: se acepta el hallazgo contra el texto del plan (que usaba r.Context()). Corrección: el bucle de lectura cuelga de un contexto que se cancela con s.baseCtx (cfg.BaseContext = sinkCtx en main) además de r.Context(); si s.baseCtx.Err() != nil al fallar la lectura → 4004; test nuevo que cancela BaseContext y espera 4004 + OnPublishEnd; corregir el comentario — porque el spec §3/§4 exige el camino de apagado — coste si es erróneo: un cierre 4004 de más si main cancelara sinkCtx antes de tiempo.
Task 6: minor (deferred): "sin motor" y err.Error() como motivo de cierre sin traducir (y >123 bytes pierde el motivo)
Task 6: minor (deferred): log Info indiferenciado en cada desconexión (preview usa Debug)
Task 6: minor (deferred): Session().ID leído dos veces (confirmación y log)
Task 6: minor (deferred): TestCameraAcceptsBigFramesAndClosesOnHugeOnes no comprueba el código de cierre 1009
Task 6: minor (deferred): sin test del start tardío (>5 s) ni del plazo de 10 s
Ruling (refina la anterior): main cancela sinkCtx DESPUÉS de WaitIdle, así que s.baseCtx no sirve para el apagado. Se sigue el patrón de ingest.Close(): httpapi.Server gana un contexto propio creado en New() y un método DisconnectCameras() que lo cancela; el handler de cámara cuelga su lectura de ese contexto (más r.Context()); main llama a api.DisconnectCameras() justo después de ingest.Close() y antes de WaitIdle — coste si es erróneo: un método público más en Server y dos líneas en main.
Task 6: fix round 1/5 despachado (1 hallazgo Important; commit 4b1fca4); re-review despachado. Task 7 implementer despachado en paralelo (haiku; árbol web/ disjunto), BASE 4b1fca4.
Task 6: fix round 1/5 (1 addressed, 0 open — 4004 vía DisconnectCameras + AfterFunc que cierra con handshake; commits 9d7af34..4b1fca4)
Task 6: complete (commits c5a4ea7..4b1fca4, review clean tras 1 ronda)
Task 7: implementer DONE_WITH_CONCERNS (38ba53c): el flujo real de getUserMedia y la maquetación a 375 px no se pudieron ejercitar en el navegador automatizado; quedan para la comprobación manual de Task 9/12. Reviewer despachado.
Task 7: review — Important (plan-mandated): cambiar de dispositivo dos veces seguidas mientras getUserMedia está en vuelo pierde la segunda elección (el watch ve stream=null y no reabre; al resolver, los ids vuelven a la primera).
Ruling: se acepta y se corrige en fix round 1 de Task 7 (antes de que Task 9 edite el mismo archivo): contador de generación en abrirCamara (si al resolver getUserMedia ya hay otra llamada más nueva, se paran las pistas recién obtenidas y se sale) y el watcher deja de exigir `stream` (basta `!emitiendo && !yaAbierto()`) — coste si es erróneo: una reapertura de más de la cámara.
Task 7: minor (deferred): el catch de getUserMedia siempre enseña permiso_denegado
Task 7: minor (deferred): pararStream es async sin await; opciones de los selects sin computed; q-select sin aria-label explícito
Task 7: fix round 1/5 despachado (1 hallazgo; commit 6eb8f2f); re-review despachado.
Task 7: fix round 1/5 (1 addressed, 0 open — contador de generación en abrirCamara; commits 38ba53c..6eb8f2f)
Task 7: complete (commits 4b1fca4..6eb8f2f, review clean tras 1 ronda)
Task 7: minor (deferred → se lleva a Task 9): tras el await de enumerateDevices en abrirCamara no se re-comprueba la generación; si otra llamada anuló `stream` entre medias, `stream.getVideoTracks()` lanza. Ruling: Task 9 añade `if (mia !== generacion) return` tras ese await — coste si es erróneo: ninguno.
Task 8: implementer despachado (haiku), BASE 6eb8f2f.
Task 8: el primer implementer murió por corte de conexión sin escribir nada; re-despachado (haiku), BASE 6eb8f2f.
Task 8: implementer DONE (4810d04); reviewer despachado (opus).
Task 8: review ❌ — 6 Important (4 plan-mandated): (1) iniciar() sin limpiar al fallar (fuga de AudioContext/worklet/ws); (2) parar() durante iniciar() deja codificadores huérfanos; (3) prepararAudio() puede colgar para siempre; (4) AudioWorkletNode con 0 salidas puede no ejecutarse en Safari; (5) micrófono >2 canales → start rechazado 4003; (6) resolución nominal de CALIDADES en vez de la real de la captura (cámaras 4:3).
Ruling: se aceptan las 6 y se corrigen en fix round 1 con código dictado por el controlador; además entran tres minors baratos y de corrección (description como BufferSource genérico; try/catch en la captura → fallo_codificar; soporte.js comprueba requestVideoFrameCallback). Micrófono siempre a 2 canales explícitos (se pierde el caso mono a 96 kbps del spec §5, aceptado) — coste si es erróneo: 32 kbps de más en subida.
Task 8: minor (deferred): anclaje del reloj de audio por hora de llegada del primer bloque (~20–40 ms tarde) y sin corrección de deriva
Task 8: minor (deferred): comentario «5 s sostenidos» vs lectura instantánea de bufferedAmount; umbrales sin contar audio
Task 8: minor (deferred): campos nodo/muestras/audioBase fuera del constructor; worklet asume cuanto de 128
Task 8: fix round 1/5 despachado (6 Important + 3 extras; commit 52953ed); re-review despachado (opus).
Task 8: fix round 1/5 (9 addressed, 0 open — arrancar()+limpiar, guardas terminado, timeout sin_audio, worklet vía GainNode 0, canales explícitos 2, tamaño real; bytesDe, try/catch captura, rVFC en soporte; commits 4810d04..52953ed)
Task 8: complete (commits 6eb8f2f..52953ed, review clean tras 1 ronda)
Task 8: minor (→ Task 9, Ruling): parar()/limpiar() durante el handshake WS deja iniciar() pendiente para siempre (limpiar anula ws.onclose antes de cerrar). Task 9 añade en emisor.js un `this.rechazarPendiente` que las dos promesas del handshake registran y que limpiar() invoca con Error(''); y `if (this.terminado) throw new Error('')` al inicio de iniciar() — coste si es erróneo: ninguno.
Task 8: minor (→ Task 9, Ruling): Camara.vue asigna `emisor = nuevo` ANTES de `await nuevo.iniciar()` para que onBeforeUnmount pueda pararlo en vuelo — coste si es erróneo: ninguno.
Task 8: minor (deferred): this.nodo no se inicializa en el constructor
Task 9: implementer despachado (sonnet), BASE 52953ed.
Task 9: implementer BLOCKED: Vite inlina worklet-audio.js (1,1 KB < assetsInlineLimit 4 KB) como data: URL; addModule con data: no es fiable en todos los navegadores.
Ruling: se permite tocar web/vite.config.js — `build.assetsInlineLimit: (ruta) => ruta.endsWith('worklet-audio.js') ? false : undefined` (función soportada desde Vite 4.4; undefined = lógica por defecto para el resto) con comentario en español; el asset debe salir como archivo propio en dist/spa/assets — coste si es erróneo: un archivo más en el embed.
Ruling: se acepta `t(`camara.${motivo}`)` en vez de `t('camara.' + motivo)` para que i18n-check no lea 'camara.' como clave literal — coste si es erróneo: ninguno.
Task 9: implementer DONE_WITH_CONCERNS (468ca1b, incluye vite.config.js). Concern: el proxy de desarrollo de Vite no reenvía upgrades WS bajo /api (preexistente: afecta también a preview y chat en dev). Ruling: se corrige en la ola final de fixes con `ws: true` en el proxy de /api — coste si es erróneo: ninguno (solo dev). Reviewer despachado (opus).
Task 10: implementer despachado (sonnet) en paralelo con la revisión de Task 9 (archivos disjuntos), BASE 468ca1b.
Task 9: review ❌ — Important: (1) wake lock puede quedar retenido si onFin llega durante el await de pedirWakeLock; (2, plan-mandated) los stops por página oculta y por pista terminada exigen emitiendo=true y no cubren la ventana de conexión (la sesión puede nacer en una pestaña oculta).
Ruling: se aceptan las dos; además entran dos minors baratos: textoFin cae a se_corto para claves desconocidas, y emitir() se guarda con conectando — coste si es erróneo: ninguno.
Task 9: minor (deferred): errores de prepararAudio distintos de sin_audio se enseñan con texto del navegador; clave camara.conectando sin uso; beforeunload registrado durante todo el montaje; comentario de rechazarPendiente describe el síntoma; videoEl.value tras await si el componente se desmontó (preexistente)
Task 10: implementer DONE (2084770); reviewer despachado (sonnet).
Task 9: fix round 1/5 despachado (2 Important + 2 minors; commit a0ebe28); re-review despachado.
Task 9: fix round 1/5 (4 addressed, 0 open; commits 2084770..a0ebe28)
Task 9: complete (commits 52953ed..468ca1b + a0ebe28, review clean tras 1 ronda)
Task 9: minor (deferred): parar() durante prepararAudio() (antes del WS) deja conectando hasta 3 s porque esa promesa no está en rechazarPendiente
Task 10: complete (commits 468ca1b..2084770, review clean)
Task 10: minor (deferred): no se comprueba sink.State()==live antes de probeStream; leerFLV trunca en silencio un tag final malformado
Task 11: implementer despachado (haiku), BASE a0ebe28.
Task 11: implementer DONE (e71a6e7); reviewer despachado (sonnet).
Task 12 (paso 1) verificación completa sobre e71a6e7: vet OK, lint 0 issues, go test -race 24 paquetes ok, i18n 446 claves, build web OK (worklet como asset propio), vet integration OK, contrato API OK, build linux/windows OK, fronteras OK.
Task 11: review ❌ — Important: la frase editada de Scope/Alcance quedó sin reajustar (línea de 123/134 columnas). Fix round 1 despachado.
Task 11: fix round 1/5 despachado (1 hallazgo; commit e569289); re-review despachado.
Task 11: fix round 1/5 (1 addressed, 0 open; commits e71a6e7..e569289)
Task 11: complete (commits a0ebe28..e569289, review clean tras 1 ronda)
Task 12: paso 1 hecho (verificación verde); paso 2 (prueba manual con teléfonos y TLS) NO se puede hacer aquí: se entrega al usuario; paso 3 tras la revisión final.
Revisión final de rama despachada (opus) sobre 80d0873..e569289.
Revisión final: «With fixes». 0 Critical; Important: (1) carrera en startSession (dos cámaras → dos filas de sesión); (2) controles y watcher no bloqueados durante conectando; (3) cambio de orientación a mitad no detectado aunque los docs lo prometen; (4) valida() sin rango de SampleRate.
Ruling: ola única de fixes con (1) bandera `arrancando` bajo el mutex + test; (2) `:disable="emitiendo || conectando"` en selects/toggle, watcher con `!conectando`, `ocupado` con `!conectando`; (3) en paso() de capturarVideo comparar videoWidth/Height con lo configurado y terminar('parada_dispositivo'); (4) servidor: SampleRate 8000–96000; cliente sin cambio (declara la tasa real del AudioContext, que es la del codificador) — coste si es erróneo: un stop de más al rotar; un 4003 para una tasa exótica.
Ruling: entran también los minors baratos: ws:true en el proxy /api; motivos de cierre traducidos y fijos («no se pudo abrir la sesión de la cámara» + log); test de esds hasta 38; test de frame enorme comprueba 1009; tests de start tardío y de plazo de lectura (con testing.Short skip); comentario en limpiar() sobre flush/pistas; AUDIO_BITRATE comentado; guarda de cuanto en el worklet; fusionar los dos onBeforeUnmount; comentario del test de status reformulado; comentario del test de integración sobre ffprobe vs bytes. Se difieren: deriva del reloj de audio (QA con teléfonos), catch de getUserMedia, camara.conectando sin uso, beforeunload permanente, videoEl tras await, parar() durante prepararAudio, sink.State() en integración.
Ola de fixes despachada (opus), FIX_BASE e569289.
Ola final de fixes: commit d096e6f (todo verde); re-review con alcance despachada (opus).
Re-review de la ola final: G1–G6, W1–W4 ADDRESSED; nueva rotura Important en W3: emisor.js compara videoWidth/Height crudos contra ancho/alto redondeados a par → una cámara de ancho impar (o metadata tardía con fallback nominal) se para en el primer frame.
Ruling (desvía de «no hay segunda ola»): es una rotura introducida por mi propia regla W3 y mata la emisión en el primer frame para ciertas cámaras; se despacha UNA corrección mínima acotada (redondear ambos lados; si arrancar() usó el tamaño nominal por videoWidth=0, adoptar el tamaño real del primer frame en vez de parar) y una comprobación con alcance — coste si es erróneo: una ronda más.
Out-of-scope (deferred): comprobación de tamaño tras el early-return por cola; goroutine del test G1 bloqueada si falla la aserción; OnPublishEnd no-op dentro de la ventana arrancando (preexistente); httpapi tarda ~147 s con -race.
Comprobación acotada de 19e37b6: ADDRESSED, sin roturas. Revisión final limpia.
Cierre: rama lista para PR sobre main. Verificación completa en verde sobre e71a6e7 + fixes finales (suites Go, lint, panel). La prueba manual con iPhone/Android por HTTPS contra YouTube/Twitch (spec §8, Task 12 paso 2), la fusión y la etiqueta v1.2.0 quedan para el usuario. Los SHA de este ledger son anteriores a la normalización de trailers Co-Authored-By hecha al cerrar (misma historia, mismos árboles).
