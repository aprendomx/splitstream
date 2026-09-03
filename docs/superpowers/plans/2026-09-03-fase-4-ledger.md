# SDD ledger — plan: docs/superpowers/plans/2026-09-03-fase-4-api-http.md

Spec: docs/superpowers/specs/2026-09-01-rtmp-relay-design.md (autoridad vinculante)
Rama: feat/fase-4-api-http (desde main, en `a4b8007`)
Ejecución: 2026-09-03, inline (sin subagentes, por instrucción del usuario)

Este ledger sí se escribe durante la ejecución, no a posteriori como el de la fase 3.

## Decisiones previas

Tres las decidió el usuario antes de escribir el plan; las otras dos salieron de leer el
código.

1. **La primera contraseña se fija con `-setpassword` por stdin.** Frente a un setup en el
   primer arranque desde la interfaz, que dejaría una ventana en la que quien llegue
   primero al panel se lo queda; y frente a una variable de entorno, que acabaría en el
   unit de systemd y en el historial del shell.
2. **Sesión en cookie HMAC sin estado.** Sobrevive a los reinicios: reiniciar el servicio
   no debe echarte del panel a mitad de transmisión. El precio es que no se puede revocar
   una sesión suelta — revocar es rotar la master key.
3. **DTOs en la capa HTTP, sin tags `json` en el motor** (§15.2).
4. **Los cambios de destino se aplican en caliente si hay sesión viva.** Si no, el toggle
   de la interfaz mentiría durante todo el directo.
5. **Revelar y auditar dejan de ser dos cosas.** `DestinationKeyForRelay` para el motor,
   `RevealDestinationKey` —auditado en su propia transacción— para la API.

## Progreso

**Task 1** (`f95f3a5`): complete. Rojo comprobado antes de arreglar: con el formato viejo,
tres eventos escritos en orden salían `[evento-2, evento-3, evento-1]`. El test de la
migración la ejerce por el camino de producción —retrasa `user_version` y reabre la base—
sin tocar nada privado. Desviación: el plan ponía los tests en `package store_test`, pero
`formatTime` es privada y probarlo desde fuera solo daría una comprobación probabilística;
se añadió un test interno, justificado en su comentario.

**Task 2** (`58faa8d`): complete, con **dos desviaciones**.
- El plan proponía `fmt.Errorf("%w: destino", ErrNotFound)`, que produce `"no encontrado:
  destino"` — y ese texto va tal cual al cliente en `writeStoreError`. Se usa un tipo
  `classified` que separa mensaje y clase.
- Al reescribir los errores de `validateRTMPURL` como errores nuevos se rompió la cadena
  hacia `ErrInvalidDestinationURL` y **cinco tests de la fase 1 se pusieron rojos**. El
  arreglo es seguir envolviendo el centinela concreto. Lo cazaron los tests, no yo.
- Hallazgo menor: las validaciones de nombre y plataforma YA existían con errores pelados,
  y `Platform.Valid()` ya estaba escrito. El plan proponía duplicar ese switch; se reutilizó
  y se corrigió el plan.

**Task 3** (`af2a7fb`): complete. El obstáculo real era que auditar siempre habría escrito
un evento por destino en cada arranque de transmisión, ahogando la auditoría en ruido justo
cuando importa. Se separan los dos usos por el nombre.

**Task 4** (`da910be`): complete. **La prueba a mano destapó lo que los tests no veían**: el
paquete `flag` interpreta las comillas invertidas del texto de ayuda como el nombre del
operando, y `-h` salía como `-setpassword read -rs PW && printf ...`. Desviación: el plan
citaba un helper `testKeyB64()` que no existe en ese paquete; se usó `generateMasterKey()`,
que es lo que usa el test que ya estaba.

**Task 5** (`96019d3`): complete. `crypto/hkdf` está en la stdlib desde Go 1.24, así que la
derivación no añade dependencias — verificado con `go doc` antes de escribir el código. Se
añadió a la CI la aserción simétrica: `internal/httpapi` no conoce `go-rtmp`.

**Task 6** (`081c248`): complete. Hallazgo al ejecutar: `go get golang.org/x/time` la añade
como **indirecta**, porque en ese momento nadie la importa; como `go mod tidy` está
prohibido en este repo, hay que moverla al bloque de directas a mano.

**Task 7** (`306f718`): complete. El test por reflexión es lo que sostiene la decisión
§15.2, así que **se comprobó en rojo y en verde**: añadir un campo a `relay.Metrics` lo pone
rojo con el mensaje que explica qué decidir; restaurado, verde. Sin esa comprobación el
guardián podría estar de adorno.

**Task 8** (`7289388` refactor + `2574eb1` endpoints): complete, en dos commits a propósito
para que `git bisect` pueda distinguirlos. El refactor se verificó con la suite entera Y con
los tres tests de integración, porque toca el camino de producción. **Un test del plan
estaba mal y el código tenía razón**: esperaba 400 de `GET /api/destinations/abc`, pero el
spec §9 no define un GET de un destino individual, así que 405 es correcto.

**Task 9** (`43ab14f`): complete, con un incidente que merece registro.
- El test de concurrencia de `DisconnectPublisher` **se colgaba** al usar `Publisher`
  completos. Es la carrera del CLIENTE de go-rtmp que el ledger de la fase 2 dejó
  avisada —entre `(*streams).Delete` y su goroutine de lectura—, que aflora al llamar a
  `Publisher.Close()` sobre una conexión ya muerta. Es un bug de la librería, no nuestro, y
  provocarlo solo lograría que la CI se cuelgue diez minutos sin decir nada útil. Se
  reescribió con `net.Dial` en crudo, que es lo que de verdad ejercita nuestro registro de
  conexiones, más un plazo duro para que un bloqueo FALLE en vez de colgar.
- Segundo tropiezo propio: el bucle de dial sin pausa agotó los puertos efímeros en
  TIME_WAIT y el test falló por su propia culpa. Con pausa, 4/4 estable.
- De paso, el hash argon2id de los tests se calcula una vez para todo el paquete: por test
  subía la corrida a 70 s sin probar nada que `internal/crypto` no pruebe ya.

**Task 10** (`87e22b4`): complete. Los dos tests de goroutines se repitieron 3 veces
seguidas antes de dar por buena la estabilidad. Con esta task `go.mod` queda con las cinco
directas exactas del spec §5 y desaparece el último `notImplemented`.

**Task 11** (`7aaac81`): complete. El apagado cierra el HTTP **primero** y la ingesta
después: al revés, podría entrar una petición que toque la base justo mientras se cierra la
sesión.

## Incidente de proceso: la CI que no cubría nada

Dos veces durante esta fase el PR se fusionó a los pocos minutos de abrirse, antes de que
llegaran los commits siguientes. Con el trigger original —`push` solo en `main`, más
`pull_request`— una rama sin PR abierto **no ejecutaba nada**, y `gh pr checks` seguía
mostrando el run del PR ya cerrado. El resultado es que llegué a informar «CI verde» sobre
las tasks 2 a 5 cuando lo verde era un run anterior a ellas.

Arreglado en `a77fabf`: la CI corre en push a cualquier rama, y el trigger de
`pull_request` se quita porque los PR de este repo ya quedan cubiertos por el push de su
rama. Lección para las fases siguientes: **verificar el `headSha` del run**, no fiarse de
`gh pr checks`.

## Verificación de la definición de terminado

Ejecutada el 2026-09-03 sobre la rama. Los diecisiete puntos:

1–3. `go test ./... -race -count=1` en verde los nueve paquetes; `go vet` limpio;
`CGO_ENABLED=0 go build` exit 0.
4. `go.mod`: cinco directas —`coder/websocket v1.8.15`, `go-rtmp v0.0.7`, `x/crypto
v0.55.0`, `x/time v0.15.0`, `modernc.org/sqlite v1.57.0`— y `go 1.25.0`.
5–6. `go list -deps` de `internal/relay` y de `internal/httpapi`: vacíos. La CI los vigila.
7. `make test-integration`: los tres tests contra `mediamtx` en verde (12,36 s / 8,74 s /
8,55 s).
8. CI verde en los dos jobs, verificada contra el `headSha` de cada commit.
9. Los catorce endpoints registrados, contados sobre `routes()` y comparados con el spec §9.
10–11. Cubierto por tests que recorren el cuerpo crudo y por
`TestProtectedEndpointsNeedASession`, que exige 401 en los doce protegidos.
12–13. `TestWebSocketPayloadMatchesTheRESTSnapshot` compara las claves de los dos JSON;
`TestWebSocketStopsWhenTheClientGoesAway` y `TestWebSocketSurvivesASlowClient` cuentan
goroutines.
14–16. Cubiertos por los tests de `cmd/splitstream` y de `internal/httpapi`.
17. **Probado a mano contra el binario**, que es lo único que prueba de verdad el apagado:
login → rotación para obtener la clave real → `ffmpeg` publicando → `GET /api/status`
devuelve `live=true, id=1` → `SIGTERM` → exit 0, `ended_at` no nulo
(`17:25:15.826319000Z`) y el evento `publisher_disconnected` persistido. De paso confirma la
Task 1 en producción: nueve dígitos fijos de fracción.

## Veredicto

**Fase 4 terminada** según su definición de terminado, verificada por ejecución. Con ella
queda **saldada entera la deuda del spec §15**: §15.1 y §15.6 en la fase 2, §15.7 en la 3, y
§15.2, §15.3, §15.4, §15.5 y §15.8 en esta.

## Prueba contra YouTube real (2026-09-03)

Hecha con la cuenta del usuario y su OBS local, contra `rtmp://a.rtmp.youtube.com/live2`.

**El riesgo del spec §14 NO se materializó.** YouTube aceptó el publish y recibió el stream
pese a que mandamos `releaseStream`/`FCPublish` sobre el stream ya creado y con
`TransactionID: 0`, en lugar del orden de FMLE. Queda pendiente repetirlo con `rtmps://` y
contra Twitch y Facebook, que pueden ser más estrictos.

Métricas tras 195 s de emisión a 720p:

| | |
| --- | --- |
| estado | `live`, `degraded=false` |
| descartes | **0** |
| cola | 0 bytes, 0 mensajes |
| bitrate | 3,12 Mbps sostenidos (76,9 MB) |
| reconexiones | 0, sin último error |

YouTube avisó de un intervalo de keyframes de 8,3 s, por encima de su máximo de 4. **No era
nuestro:** 8,3 s × 30 fps = 250 fotogramas, que es el `keyint` por defecto de x264 cuando
OBS está en modo de salida Simple y deja el GOP al encoder. El motor no descartó ni un
frame, y el descarte por GOP ni siquiera llegó a activarse porque no hubo contrapresión.

Verificado además que las propiedades del spec §8 se sostienen en producción: la clave del
destino quedó cifrada en la base (52 bytes de AES-256-GCM, solo `last4` en claro para la
máscara) y no aparece ni una vez en el log, con el nivel en `debug`.

### Hallazgo: la resolución no se expone mientras la sesión está viva

`session.width`, `height` y `bitrate_bps` salen `null` en `GET /api/status` durante toda la
emisión, aunque el motor detecta la resolución al primer segundo —lo loguea como
`resolución detectada ancho=1280 alto=720`—. Solo se persisten en `FinishSession`, al
cerrar la sesión.

El spec §10 pide que el panel enseñe «señal entrante, resolución, bitrate, tiempo
transmitiendo» como estado global **en vivo**, así que la fase 5 no podría pintarlo.

**Arreglado en el acto** (`relay.Engine.Session()`), y verificado contra el mismo directo
que lo destapó: `GET /api/status` pasó de `"width": null` a `"width": 1280, "height": 720,
"bitrate_bps": 2566136` mientras la fila de esa sesión en la base sigue con `NULL`, porque
solo se escribe al cerrar. La API ya no depende de esa fila.

Dos decisiones del arreglo:
- El bitrate en vivo se calcula con la **misma fórmula** que `OnPublishEnd` persiste, con un
  test que lo fija: si divergieran, el panel diría una cosa y el historial otra.
- La resolución sale como `null`, no como `0x0`, entre que el publisher conecta y llega el
  primer sequence header. La interfaz debe distinguir «todavía no se sabe» de un valor real.
- `started_at` se normaliza a UTC: el motor lo tiene en hora local y el resto de timestamps
  del JSON van en `Z`. Lo vi en el volcado del directo, no en un test.

## Prueba con Facebook y Twitch (2026-09-03)

**Facebook conecta por RTMPS.** La ruta TLS funciona contra una plataforma real: fan-out
simultáneo a YouTube y Facebook, `live` en ambos, 3,8 Mbps cada uno, cero descartes.
También quedó demostrado el aislamiento entre destinos de la fase 3: mientras Facebook
entraba en bucle de reconexión, YouTube siguió a 3,8 Mbps con cero reconexiones.

Pero la prueba destapó **tres fallos**, todos invisibles para los 72 tests del paquete y
todos del mismo tipo: cosas que solo pasan cuando el producto se usa de verdad.

### 1. El destino añadido en caliente nunca se conectaba

`applyHot` metía el sink en el hub con `Hub.Add`, que **solo registra**. Arrancar necesita
el contexto de vida del proceso y el preámbulo, y el único que llamaba a `Start` era el
motor. El destino se quedaba en `idle` acumulando mensajes y descartándolos al desbordar la
cola: 517 descartes, 0 bytes enviados, `DEGRADADO`. Desde fuera parecía un problema de
rendimiento, que es la pista equivocada.

Y si `applyHot` hubiera llamado a `Start`, lo habría hecho con `r.Context()`: el sink habría
muerto al devolver la respuesta HTTP.

Arreglado moviendo la responsabilidad a quien tiene las dos cosas: `Engine.AddSink()`. El
arranque de sesión usa ahora el mismo camino, para que no haya dos formas de hacerlo, y
`HubView` desaparece de la API.

**Por qué los tests no lo vieron:** el `fakeHub.Add` solo apuntaba el id. Comprobaba que la
API *pedía* añadir el destino, no que el destino *acabara transmitiendo*. Los tests nuevos
usan hub y sink de verdad y exigen que llegue a `live` con cero descartes; uno cancela un
contexto de petición justo después de añadir.

### 2. Los destinos en caliente no registraban ni un evento

El `OnEvent` de la fábrica capturaba el contexto de quien llamaba a `Build`. Desde el
arranque de sesión da igual —es el del proceso—, pero desde un handler HTTP es el de la
petición. Se vio como `"registrar evento: context canceled"` en el log y como un destino sin
ningún evento en el panel.

Arreglado con `context.Background()` para los eventos: ocurren a lo largo de toda la
transmisión, no pueden depender de la llamada que construyó el sink.

### 3. El bucle de reconexión agotó el cupo de streams de la cuenta

El más serio. `if transmitted { s.bo.reset() }` reiniciaba el backoff con que la conexión
hubiera escrito **un solo frame**. Facebook aceptaba la conexión, tragaba unos 200 KB y
cortaba, así que el backoff volvía a su suelo de 1 s y ahí se quedaba clavado: una conexión
nueva por segundo, indefinidamente. Facebook cuenta cada una como un stream ACTIVO, y acabó
respondiendo *«Alcanzaste el límite de streams activos permitidos»* — la cuenta del usuario
quedó sin poder emitir por culpa de nuestro reintento.

Arreglado con `minHealthySession = 30 s`: transmitir un poco no es señal de que la
configuración sea buena, solo lo es una sesión que dura. Y `flapThreshold = 3` emite
`destination_flapping` con un mensaje que nombra la causa probable, porque `suspectThreshold`
solo cubría el caso de "nunca llega a transmitir".

Medido: 13 conexiones en 12 s con el fallo, 4 o 5 con el arreglo. Treinta veces menos
streams creados por minuto contra la plataforma.

## La causa raíz: `releaseStream`/`FCPublish` (2026-09-03)

Twitch fallaba igual que Facebook: aceptaba `connect`, `createStream` y `publish`, y cortaba
en cuanto empezábamos a escribir. El log señalaba `sendPreamble` → `WriteAudio` → *broken
pipe*, que es el síntoma que el ledger de la fase 2 predijo para un rechazo de plataforma:
`Publish` es fire-and-forget, así que el rechazo no llega como error sino como escritura
rota una o dos operaciones después.

**El experimento que lo decidió:** publicar con `ffmpeg` DIRECTAMENTE a Twitch con la misma
clave. ffmpeg aguantó 45 s. Luego la clave y el canal estaban bien, y el problema era
nuestro. Sin ese experimento habríamos seguido culpando a la plataforma.

**La causa.** FMLE —el cliente que las plataformas esperan— manda `releaseStream` y
`FCPublish` sobre el stream 0 y ANTES de `createStream`. Nosotros solo podíamos mandarlos
sobre el stream ya creado y con `TransactionID: 0`, porque go-rtmp v0.0.7 no expone su
stream de control: es interno (`cc.conn.streams.At(ControlStreamID)`) y el único `Stream`
que su API pública devuelve es el de `CreateStream`. Es exactamente el riesgo que el spec
§14 anotó al empezar la fase 2.

**El arreglo:** no mandarlos. Medido contra las plataformas reales — Twitch pasa de cortar
siempre a transmitir sin un solo descarte, y YouTube funciona igual de las dos formas.
`FCUnpublish` se mantiene porque va al cerrar, cuando la conexión se tira igualmente.

### Todo encadenaba a esta causa

El desastre de Facebook fue una consecuencia, no un problema aparte:

1. `releaseStream`/`FCPublish` en el stream equivocado → la plataforma rechaza el publish.
2. El rechazo aflora como *broken pipe* una o dos escrituras después, no como error.
3. El sink había escrito algo, así que daba la conexión por buena y **reiniciaba el
   backoff** → una reconexión por segundo.
4. Facebook contaba cada una como stream activo → cupo agotado y cuenta bloqueada.

Los cuatro eslabones eran defectos reales y cada uno se arregló por separado, pero solo el
primero era la causa.

### Resultado final

Con los tres arreglos y el binario definitivo, **las tres plataformas a la vez**:

| Destino | Estado | Bitrate | Descartes | Reconexiones |
| --- | --- | --- | --- | --- |
| YouTube (RTMP) | live | 3696 kbps | 0 | 0 |
| Facebook (RTMPS) | live | 3696 kbps | 0 | 0 |
| Twitch (RTMP) | live | 3696 kbps | 0 | 0 |

78,6 MB por destino, tres minutos, **cero errores y cero avisos** en el log desde el
arranque de la sesión. El riesgo del spec §14 queda cerrado: no era una falsa alarma, era
real, y está resuelto.

## Lo que queda abierto

- **Las tres plataformas probadas y funcionando** (ver arriba). Queda por probar la ruta
  RTMPS de YouTube y Twitch, aunque Facebook ya valida esa ruta.

- **Un fallo intermitente sin identificar en `internal/relay`**, visto una vez durante una
  corrida de `go test ./...` y no reproducido en 19 intentos posteriores (16 del paquete
  aislado, 3 de la suite entera, cinco con los núcleos saturados). El log mostraba un
  reintento de sink de ~1 s y los `waitFor` de ese paquete tienen un plazo de 3 s, así que
  la hipótesis es un test sensible al tiempo bajo carga. No se ha tocado: cambiar un plazo
  sin reproducir el fallo es taparlo.
- **`gofmt` preexistente**: `internal/flv/sps.go` e `internal/crypto/password_test.go` no
  están formateados desde `d4bdf6c` (fase 3), y la CI no lo comprueba. Añadir `gofmt -l` al
  job rápido sería una línea.
- **Los timestamps del JSON no son de ancho fijo**, aunque los de la base sí. `time.Time`
  se serializa con `RFC3339Nano`. El frontend debe parsear a `Date`, no comparar cadenas.
