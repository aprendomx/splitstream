# Splitstream — Cámara del navegador

**Fecha:** 2026-09-17
**Estado:** borrador, pendiente de aprobación
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
**Spec hermano:** `docs/superpowers/specs/2026-09-08-vista-previa-design.md`
**Versión de partida:** v1.1.0 → esta es la v1.2

## 1. Qué se construye

Una segunda fuente de ingesta, además de OBS: la cámara y el micrófono del dispositivo
desde el que se abre el panel. El usuario entra en la pestaña «Cámara» del panel desde su
teléfono o portátil, elige cámara y calidad, pulsa «Emitir», y lo que captura el navegador
sale hacia todos los destinos configurados, se graba si la grabación está activa y se
mide como cualquier otra sesión.

El navegador es el publisher. Codifica H.264 y AAC con WebCodecs y manda los frames por
un WebSocket al binario, que los envuelve en tags FLV y los entrega al mismo `Engine` y al
mismo `Hub` que reciben a OBS. **Nada aguas abajo cambia**: sinks, colas, reconexión,
grabación, vista previa, métricas y eventos ven una sesión igual que las demás.

Decisiones de producto:

1. **Sin transcodificar, como todo lo demás.** El navegador entrega H.264 + AAC, que es lo
   único que las plataformas aceptan, o no emite. Si el navegador no puede codificar AAC
   (Firefox, Chrome en Linux), la página lo dice con esas palabras y no ofrece el botón.
2. **Una emisión a la vez.** OBS y la cámara comparten el `Engine`, y el `Engine` ya
   rechaza una segunda sesión (`ErrSessionInProgress`). Si OBS está emitiendo, el botón
   «Emitir» de la cámara está deshabilitado con el motivo, y al revés: mientras la cámara
   emite, OBS es rechazado igual que lo sería un segundo OBS.
3. **Configuración fija por emisión.** Resolución, bitrate y cámara se eligen antes de
   pulsar «Emitir». Cambiarlos es parar y volver a emitir. Sin renegociación a mitad.
4. **La página tiene que quedarse delante.** En el móvil, bloquear la pantalla o cambiar
   de app suspende la cámara; la emisión se para limpiamente en cuanto la página deja de
   ser visible, y se dice por qué. Es preferible a una emisión medio viva que las
   plataformas cortan por su cuenta un minuto después.

Fuera de alcance: bitrate adaptativo, escenas, superposiciones, texto en pantalla,
compartir pantalla, cámara USB conectada al servidor, ingesta WebRTC/WHIP, codificador
AAC en WebAssembly para los navegadores que no lo traen, y guardar en la base de datos de
dónde vino cada sesión (§10).

## 2. Lo que el spike comprobó

Antes de diseñar se midió qué entregan los navegadores (macOS 26.6, página en
`localhost`; iOS y Android por la tabla de compatibilidad de MDN):

| Navegador | H.264 | AAC | Notas |
| --- | --- | --- | --- |
| Chrome 152 | sí | sí | `description` del audio = AudioSpecificConfig de 2 bytes |
| Safari 26.6 | sí | sí | `description` del audio = ES_Descriptor completo (esds, 39 bytes) con el ASC dentro; sin `MediaStreamTrackProcessor` en `Window` |
| Firefox 155 | sí | **no** | `isConfigSupported` devuelve false para `mp4a.40.2` |
| Chrome por `http://192.168.x.x` | no | no | `isSecureContext=false`: no hay `getUserMedia` ni codificadores |
| Chrome Android, Edge | sí | sí | por MDN |
| Chrome Linux | sí | **no** | por MDN |
| Safari iOS | sí | 26+ | `AudioEncoder` llegó en Safari 26; antes solo vídeo |

Lo que entregan los dos que sirven es exactamente lo que FLV necesita: los chunks de vídeo
vienen en AVCC con longitud prefijada de 4 bytes y el `avcC` completo en la configuración
del decodificador; los chunks de audio son AAC crudo sin ADTS. No hay que reempaquetar
nada más que ponerles delante la cabecera de tag. El `avcC` es el mismo formato que la
vista previa manda al navegador; aquí viaja en sentido contrario.

Los tres límites que salieron del spike están en el README desde antes de este spec:
HTTPS obligatorio, ni Firefox ni Chrome en Linux, y iOS 26+.

## 3. Protocolo del WebSocket

`GET /api/camera/ws`. Mensajes **binarios** del cliente al servidor, 1 byte de tipo
delante. El servidor solo habla para confirmar el arranque y para cerrar con motivo.

| Tipo | Nombre | Cuerpo |
| --- | --- | --- |
| `0x00` | start | JSON `{"width","height","framerate","video_bitrate","audio_bitrate","sample_rate","channels"}` |
| `0x01` | video config | El `avcC` tal cual sale de `decoderConfig.description` |
| `0x02` | video frame | `[1 byte flags (bit 0 = keyframe)] [4 bytes big-endian timestamp ms] [NALUs AVCC]` |
| `0x03` | audio config | `decoderConfig.description` del `AudioEncoder`: un AudioSpecificConfig desnudo o un ES_Descriptor (esds) que lo contiene |
| `0x04` | audio frame | `[4 bytes big-endian timestamp ms] [AAC crudo]` |

- **`start` es siempre el primer mensaje** y abre la sesión en el `Engine`. El servidor
  responde con un mensaje de texto `{"session_id": N}`; hasta entonces el cliente no
  manda media. Con `start` el servidor construye y publica el `onMetaData` (§4).
- Las dos configs llegan antes que su primer frame, cada una en cuanto el codificador la
  entrega. El servidor las convierte en sequence headers y las publica; el `Preamble` las
  cachea como hace con las de OBS.
- Los timestamps son milisegundos desde el arranque de la emisión, con **un solo reloj**
  para audio y vídeo, monótonos. Es lo que hace OBS; el `timebase` de cada sink hace el
  resto. El servidor no los corrige ni los valida: pasan tal cual.
- Los frames de vídeo (`0x02`) copian byte a byte el formato del frame de la vista previa,
  a propósito: el mismo recorte, en la otra dirección.
- Códigos de cierre de aplicación que el cliente enseña como motivo, traducidos en el
  handshake como en la vista previa:

  | Código | Cuándo |
  | --- | --- |
  | `4002` | ya hay una emisión en curso (OBS u otra cámara) |
  | `4003` | el primer mensaje no fue `start`, o un mensaje está mal formado |
  | `4004` | el servidor se está apagando |

## 4. El servidor

**`internal/flv` crece con el inverso de `Inspect`.** Hoy sabe leer cabeceras de tag;
pasa a saber escribirlas:

- `WrapVideo(nalus []byte, keyframe bool) []byte` → `[0x17|0x27] 0x01 00 00 00` + NALUs.
  Composition time siempre 0: los codificadores de navegador en `latencyMode: realtime`
  no producen B-frames, y la config lo fija así (§5).
- `WrapVideoSeqHeader(avcC []byte) []byte` → `0x17 0x00 00 00 00` + avcC.
- `WrapAudio(aac []byte) []byte` → `0xAF 0x01` + AAC. Para AAC la cabecera FLV es
  siempre `0xAF` (formato 10, marca de 44 kHz, 16 bits, estéreo) sin importar la tasa
  real: la tasa real va en el ASC, y es lo que leen las plataformas.
- `WrapAudioSeqHeader(asc []byte) []byte` → `0xAF 0x00` + ASC.
- `AudioSpecificConfig(description []byte) ([]byte, error)`: si empieza por `0x03` es
  un ES_Descriptor y se extrae el DecoderSpecificInfo (tag `0x05`, longitud en base 128
  extensible); si no, se devuelve tal cual. Los bytes que Safari entregó en el spike son
  el fixture del test.
- `OnMetaData(m Meta) []byte`: `"onMetaData"` + ECMA array en AMF0 con `width`, `height`,
  `framerate`, `videocodecid` 7, `videodatarate`, `audiocodecid` 10, `audiodatarate`,
  `audiosamplerate`, `audiosamplesize` 16, `stereo` y `encoder` (`splitstream-camera/<versión>`).
  Se codifica con `go-amf0`, que ya está en el módulo como dependencia de go-rtmp; pasa
  de indirecta a directa sin descargar nada nuevo. El payload que sale es el mismo que
  `OnSetDataFrame` recibe de OBS, así que `WriteMeta` lo envuelve en `@setDataFrame`
  sin tocarse.

**`relay.Engine` gana `StartLocalSession() error`.** Es `OnPublishStart` sin el
validador: la cookie del panel ya autenticó a quien está al otro lado, y la clave de
ingesta es cosa de RTMP. Los dos comparten un `startSession` interno que abre la sesión en
la base, arranca los sinks y loguea `publisher_connected`; solo cambia el mensaje del
evento («la cámara del navegador conectó»). `OnMessage` y `OnPublishEnd` se usan tal
cual. `LiveSession` gana `Source` (`"rtmp"` o `"browser"`) para que el panel sepa qué
está en el aire; no se persiste (§10).

**`EngineView` (httpapi) gana `StartLocalSession`, `OnMessage` y `OnPublishEnd`.** El fake
de los tests los implementa registrando llamadas.

**`GET /api/camera/ws`, en `internal/httpapi/camera.go`.** Detrás de `requireSession` y
con la misma comprobación de Origin que los otros dos WebSockets. El handler:

1. Negocia el idioma antes del Accept, como la vista previa.
2. Sube el límite de lectura del WebSocket a 4 MiB: el de la librería es 32 KiB y un
   keyframe 1080p lo pasa de sobra.
3. Lee el primer mensaje con un plazo de 5 s. Si no es `start`, cierra con `4003`.
4. Llama a `StartLocalSession`. `ErrSessionInProgress` → `4002`. Otro error → cierre
   normal con el texto del error.
5. Publica el `onMetaData` y confirma con `{"session_id"}`.
6. Bucle de lectura con plazo de 10 s por mensaje: un teléfono que se queda sin red no
   siempre manda la trama de cierre, y sin plazo la sesión quedaría abierta hasta que el
   TCP se rinda, con los destinos colgando de ella. Cada mensaje bien formado se envuelve
   y va a `OnMessage`; uno mal formado cierra con `4003`.
7. **Pase lo que pase, `OnPublishEnd` en el `defer`**: cierre del cliente, plazo vencido,
   error de lectura o apagado del servidor. Es lo que `WaitIdle` necesita para que el
   apagado sea limpio, y lo que cierra el `Hub` para los sinks.

Lo que no cambia y conviene decir:

- **La rotación de clave con `disconnect_now` no toca a la cámara.** `DisconnectPublisher`
  corta conexiones RTMP; la cámara no usa la clave. Es coherente: rotar la clave protege
  la ingesta RTMP, y la cámara está protegida por la contraseña del panel.
- **La vista previa funciona con la cámara como fuente**, porque lee del `Hub`. En el
  teléfono no aporta nada (la vista local es el monitor), pero en un portátil que mira el
  panel mientras el teléfono emite, sí.
- **La grabación graba la cámara sin saberlo.** Mismos tags, mismo mux.

## 5. El cliente

Página `Camara.vue` en `/camara`, con pestaña propia en la barra entre «Panel» e
«Historial». Sin librerías nuevas: `getUserMedia`, WebCodecs, `AudioWorklet` y
`WebSocket` son del navegador.

**Detección de soporte al entrar**, en este orden, y el primer fallo decide el aviso:

1. `isSecureContext` falso → «La cámara necesita HTTPS» con enlace a la sección del README.
2. Sin `getUserMedia`, `VideoEncoder` o `AudioEncoder` → «Tu navegador no puede emitir».
3. `VideoEncoder.isConfigSupported(avc1.42001f)` falso → «Tu navegador no codifica H.264».
4. `AudioEncoder.isConfigSupported(mp4a.40.2)` falso → «Tu navegador no codifica AAC, que
   las plataformas exigen. Funciona en Chrome, Edge y Safari, salvo en Linux».

**Controles**, todos deshabilitados mientras se emite:

- Cámara (tras el permiso, `enumerateDevices` da los nombres) y micrófono. En el móvil la
  predeterminada es la frontal.
- Calidad: 720p a 2,5 Mbps (predeterminada) o 1080p a 4,5 Mbps, 30 fps. Audio 128 kbps
  estéreo, o mono a 96 kbps si el micrófono es mono.
- «Emitir» / «Parar». Deshabilitado con motivo si el estado dice que ya hay sesión en vivo.

**Captura y codificación**, cuando se pulsa «Emitir»:

- Vídeo: `<video muted playsinline>` con el stream, y en cada `requestVideoFrameCallback`
  se construye `new VideoFrame(videoEl, {timestamp})` y se le pasa al `VideoEncoder`. Se
  usa esto y no `MediaStreamTrackProcessor` porque Safari no lo expone en `Window` y
  Firefox no lo tiene; el camino por el elemento de vídeo funciona en los tres. Config:
  `avc1.42001f` (baseline, sin B-frames), `avc: {format: 'avc'}`,
  `latencyMode: 'realtime'`, `hardwareAcceleration: 'prefer-hardware'`. Keyframe forzado
  cada 2 s.
- Audio: `AudioContext` a 48 kHz con un `AudioWorkletNode` que acumula 1024 muestras por
  canal (el tamaño de frame de AAC) y las manda al hilo principal, donde se construyen
  `AudioData` f32-planar y se encolan al `AudioEncoder`. Codificar en el hilo principal
  es suficiente para v1; moverlo a un Worker queda para cuando se mida que hace falta.
- Un solo reloj: `performance.now()` en el instante de pulsar «Emitir» es el cero; cada
  frame de vídeo y cada bloque de audio llevan su timestamp en microsegundos desde ahí, y
  el envío por WebSocket los convierte a milisegundos.
- `wakeLock.request('screen')` mientras se emite; `beforeunload` avisa; al perder
  `visibilitychange` la emisión se para con el aviso de §1.4.

**Control de subida**: antes de mandar un frame de vídeo se mira `ws.bufferedAmount`. Por
encima de 1 s de bitrate se descartan los deltas hasta el siguiente keyframe (que se
fuerza). Por encima de 5 s sostenidos, se para con «tu conexión no da para este bitrate;
prueba 720p». El audio nunca se descarta: es pequeño y su hueco se nota más.

**Parar**, por cualquiera de los cuatro caminos (botón, cierre del servidor, página
oculta, error del codificador), siempre igual: `flush()` de los codificadores, cerrar el
WebSocket con 1000, parar las pistas del stream, soltar el wake lock, volver a reposo con
el motivo visible si no fue el botón. Sin reconexión automática: una emisión que se cayó
es algo que el usuario tiene que ver, no algo que se reanude sola a la mitad.

**Estado en vivo**: la página enseña el mismo chip de estado por destino que el panel,
leído del WebSocket de estado que ya existe, para que desde el teléfono se vea si YouTube
o Twitch están recibiendo. Nada nuevo en el servidor para esto.

## 6. Casos borde

- **OBS conecta mientras la cámara emite:** rechazado con `ErrSessionInProgress`, igual
  que un segundo OBS. El log lo dice.
- **Se abre la página de cámara en dos dispositivos:** el segundo ve «Emitir»
  deshabilitado por el estado; si aun así llega (carrera), `4002`.
- **Permiso de micrófono denegado:** no se emite. Las plataformas rechazan vídeo sin
  audio pasado un tiempo y la experiencia sería «funcionaba y se cortó». Se explica.
- **El codificador falla a mitad** (`error` callback): parar con aviso genérico. Sin
  reintento en bucle.
- **Cambio de cámara o de orientación a mitad:** no se soporta en caliente. Girar el
  teléfono cambia la resolución de captura; el `<video>` se sigue viendo pero el
  codificador recibe frames de otro tamaño y falla → se para con aviso. Se fija la
  orientación de captura al arrancar y se avisa en la interfaz de que gire antes.
- **El servidor se reinicia:** el WebSocket se cierra sin motivo de aplicación; la página
  para con «se cortó la conexión».
- **La página se cierra sin `Parar`:** el navegador manda cierre o no; en el peor caso el
  plazo de 10 s del bucle de lectura cierra la sesión.
- **Timestamps con salto** (el navegador congeló la pestaña unos segundos y volvió): pasan
  tal cual, como pasarían de OBS; las plataformas toleran huecos, y la página ya se paró
  al ocultarse en el caso móvil.

## 7. Seguridad

- Misma autenticación que el resto de la API: cookie de sesión y comprobación de Origin.
  Quien puede abrir este WebSocket ya puede rotar la clave y editar destinos.
- Sin secreto nuevo. La clave de ingesta no se enseña ni se usa.
- Límite de lectura de 4 MiB por mensaje y plazos de 5 s / 10 s: un cliente que abre el
  socket y no habla no retiene una sesión abierta.
- El navegador solo entrega cámara y codificadores en HTTPS o `localhost`, así que este
  camino no existe en HTTP plano aunque la API lo sirva. La documentación lo dice; el
  servidor no lo impone porque no hace falta.

## 8. Pruebas

**`flv`** — tablas de bytes, sin mocks:

- `WrapVideo` / `WrapAudio` y sus sequence headers producen las cabeceras exactas, y
  `InspectVideo` / `InspectAudio` de lo producido devuelven keyframe, seq header y códec
  correctos: el inverso cierra el círculo.
- `AudioSpecificConfig` devuelve `11 90` tanto para el ASC desnudo de Chrome como para
  el esds de Safari capturado en el spike; rechaza un esds truncado.
- `OnMetaData` se decodifica con `go-amf0` a un mapa con los campos y valores esperados.

**`relay`** — con `-race`:

- `StartLocalSession` con sesión en curso → `ErrSessionInProgress`; sin ella abre sesión,
  arranca los sinks del proveedor y `Session().Source == "browser"`.
- Tras `OnPublishEnd` se puede abrir otra sesión, local o RTMP.

**`httpapi`** — con el fake de `EngineView`:

- Sin sesión → rechazo. Origin ajeno → rechazo.
- Primer mensaje no `start` → cierre `4003`. `start` con sesión en curso → `4002`.
- `start` válido → `StartLocalSession` llamado, `onMetaData` publicado, `{"session_id"}`
  recibido.
- Config de vídeo → mensaje `KindVideo` con `IsSeqHeader`; frame con flag → `IsKeyframe`;
  config de audio esds → `KindAudio` seq header con el ASC de 2 bytes; los timestamps
  llegan intactos.
- Cliente que desaparece sin cerrar → `OnPublishEnd` dentro del plazo.
- Mensaje mayor que el límite → cierre y `OnPublishEnd`.

**`test/integration`** — un cliente Go que hace de navegador. Las pruebas de relay ya
generan su patrón de prueba con `ffmpeg`; esta le pide además un archivo FLV corto
(H.264 baseline + AAC), lee sus tags con un lector mínimo (cabecera de 11 bytes + cuerpo),
les quita la cabecera FLV para quedarse con lo que entregaría WebCodecs —el `avcC`, los
NALUs AVCC, el ASC y el AAC crudo— y lo manda por `/api/camera/ws`. Un destino RTMP falso
recibe el preámbulo y los mismos cuerpos de tag en el mismo orden. Es la prueba de que la
cámara es «otro OBS» para todo lo que hay detrás, y de que el envoltorio de §4 reconstruye
byte a byte lo que `Inspect` desmonta.

**Navegador** — como en todas las fases: el binario real con TLS, la página abierta en un
iPhone y en un Android, emitiendo contra YouTube y Twitch a la vez, y mirando el vídeo en
las plataformas. Los fallos de sincronía audio/vídeo y de orientación solo aparecen así.
QA de móvil en el iframe de 375 px para la maquetación.

## 9. Documentación

- README (en/es): la subsección «planeado» de Alcance pasa a ser una sección de uso,
  «Emitir desde el teléfono», con los mismos límites y un paso a paso de HTTPS en LAN.
- `docs/api.md` regenerado por el test de contrato.
- Manual de usuario: página de la cámara.
- Comparativa: fila nueva.

## 10. Lo que este diseño no resuelve

- **Bitrate adaptativo.** El control de subida descarta y avisa; no baja el bitrate solo.
  `VideoEncoder.configure` admite cambiar el bitrate en caliente, así que el camino existe
  cuando se quiera.
- **AAC donde el navegador no lo trae.** Un codificador en WebAssembly (FAAC es LGPL,
  fdk-aac tiene licencia propia) daría Firefox y Chrome en Linux. Pesa cientos de KB y
  es otra pieza que mantener; primero hay que ver si alguien lo pide.
- **Fuente de la sesión en el historial.** `Source` vive en memoria; persistirlo es una
  columna con valor por defecto y una migración, cuando el historial lo necesite.
- **Codificar en un Worker.** Si en teléfonos de gama baja el hilo principal no da,
  los codificadores se mueven a un Worker con `transferControlToOffscreen` para el vídeo.
- **Compartir pantalla.** `getDisplayMedia` entrega un stream igual que `getUserMedia`;
  es el mismo camino con otro botón. Queda fuera solo por alcance.
- **Emitir con la pantalla bloqueada.** No hay API web para eso; es una app nativa.
