# Splitstream — Vista previa silenciada

**Fecha:** 2026-09-08
**Estado:** aprobado, pendiente de plan de implementación
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
**Versión de partida:** v0.7.0

## 1. Qué se construye

Un monitor de vídeo en vivo dentro del panel: el usuario pulsa «Vista previa» y ve, en la
misma página, lo que está saliendo hacia las plataformas. Sin sonido, por diseño.

Dos decisiones de producto que el ledger del logo dejó abiertas quedan tomadas aquí:

1. **Es un monitor de verdad, no una miniatura.** La pregunta que responde es «¿se ve
   bien?»: vídeo en movimiento, fluidez, calidad. Una imagen fija que se refresca habría
   respondido solo «¿está saliendo lo correcto?» y el usuario eligió lo primero.
2. **Bajo demanda, con gesto explícito.** La vista previa gasta en subida del VPS
   aproximadamente el bitrate del vídeo mientras está abierta — como un destino más, pero
   solo mientras se mira. El panel abierto en una pestaña de fondo no gasta nada. Esto
   mantiene la promesa de gasto predecible del producto (`bitrate × destinos`).

Fuera de alcance: audio (la vista es silenciada por definición), reconexión automática de
la vista, previsualizar antes de emitir, y cualquier forma de transcodificación.

## 2. La idea que lo hace barato

El payload de vídeo que circula por el `Hub` es el cuerpo del tag FLV: 5 bytes de cabecera
(frameType/codecID, AVCPacketType, composition time) y detrás los NALUs H.264 en formato
AVCC con longitud prefijada. Ese es **exactamente** el formato que acepta la API WebCodecs
del navegador (`VideoDecoder`) cuando se la configura con el `avcC` — y el `avcC` es el
cuerpo del sequence header que el `Preamble` ya cachea.

Así que el servidor no decodifica, no remuxea y no parsea: recorta 5 bytes y reenvía. El
navegador decodifica con su hardware y pinta en un `<canvas>`. Cero dependencias nuevas en
Go y cero librerías JS.

Alternativas descartadas:

| Alternativa | Por qué no |
| --- | --- |
| Remux a fMP4 + MSE | Cientos de líneas de boxes MP4 escritas a mano, la parte más propensa a errores de todo el diseño, y más latencia. Su única ventaja —navegadores viejos y audio futuro— no paga eso. |
| HTTP-FLV + flv.js | ~170 KB de librería JS; el panel renunció a una fuente de iconos por 700 KB. |
| WebRTC | Traería pion entero para un monitor local. |
| Imagen fija periódica | Requeriría decodificar H.264 en el servidor (ffmpeg o cgo), y no responde la pregunta elegida. |

El precio del enfoque: WebCodecs con H.264 pide navegador moderno — Chrome/Edge desde hace
años, Safari 16.4+, Firefox 130+. Si falta, la vista muestra un aviso y no hay más.

## 3. El tap en el Hub

Un **tap** es un consumidor de solo lectura del `Hub`: nace cuando el navegador abre el
WebSocket de la vista previa y muere al cerrarse. `*Sink` no se toca y no aparece ninguna
interfaz nueva.

- `Hub.Tap()` registra un canal con buffer de 64 mensajes y devuelve el canal y una función
  `release` idempotente que lo da de baja.
- `Publish` entrega a los taps **solo mensajes de vídeo** (el audio ni entra), con envío no
  bloqueante: la vista previa jamás frena al publisher ni a los sinks.
- Si el buffer está lleno, se descarta el mensaje y el tap pasa a «esperando keyframe»: no
  vuelve a recibir nada hasta el siguiente keyframe. Es la disciplina de las colas de los
  sinks reducida al mínimo, porque aquí perder frames solo congela la imagen de quien mira,
  un GOP como mucho.
- Todo tap empieza en «esperando keyframe»: el primer mensaje útil que sale de él es
  siempre un keyframe, porque un decodificador no puede arrancar en mitad de un GOP. Los
  sequence headers de vídeo **sí atraviesan el tap siempre** (renegociación en §7).
- `Hub.Close()` —fin de la sesión de ingesta— cierra los canales de todos los taps; el
  lado HTTP lo ve y cierra su WebSocket.

## 4. Protocolo del WebSocket

Mensajes **binarios**. El WebSocket de estado actual (JSON, un push por segundo) no se
toca; este es un endpoint aparte. Cada mensaje empieza con 1 byte de tipo:

| Tipo | Nombre | Cuerpo |
| --- | --- | --- |
| `0x01` | config | El `avcC` completo: el cuerpo del sequence header sin sus 5 bytes FLV |
| `0x02` | frame | `[1 byte flags (bit 0 = keyframe)] [4 bytes big-endian timestamp ms] [NALUs AVCC tal cual]` |

- La config es siempre lo primero que se manda, y se reenvía si el publisher renegocia a
  mitad de emisión.
- El timestamp viaja por si algún día hace falta; hoy el cliente pinta cada frame según lo
  decodifica, que ya llega paceado por la red.
- El string de códec (`avc1.PPCCLL`) lo deriva **el cliente** de los bytes 1–3 del propio
  `avcC`. El servidor no parsea nada.

## 5. El endpoint HTTP

`GET /api/preview/ws`, detrás de `requireSession` como todo lo demás, y con la misma
verificación de Origin que el WebSocket de estado: llegar ahí ya implica cookie válida.

- Si no hay emisión activa o el `Preamble` aún no tiene sequence header de vídeo, se
  acepta y se cierra en seguida con un código de aplicación («sin señal»). No se queda
  esperando: el panel ya sabe por el estado si hay ingesta, y el botón solo se habilita
  cuando la hay — el cierre inmediato cubre la carrera de pulsar justo cuando se corta.
- Bucle de escritura con el mismo `wsWriteTimeout` de 2 s del WebSocket de estado: un
  cliente que no lee se corta y su tap se libera. No se acumulan goroutines por pestañas
  abandonadas.
- **Sin límite artificial de taps.** Cada vista abierta cuesta ≈ el bitrate de vídeo en
  subida y es una decisión de quien la abre. El caso real es una persona con un panel.

## 6. Interfaz

- Botón «Vista previa» en el panel, habilitado solo cuando el estado dice que hay ingesta
  activa. Al pulsarlo aparece un `<canvas>` con botón de cierre; la proporción la fija el
  primer frame decodificado.
- Al abrir: comprobar que `VideoDecoder` existe y que `isConfigSupported()` acepta el códec
  recibido. Si no, aviso «tu navegador no soporta la vista previa» y se cierra todo.
- Flujo: config → `decoder.configure({codec, description: avcC, optimizeForLatency:
  true})`; cada frame → `EncodedVideoChunk` → `decode()`; cada `VideoFrame` de salida se
  pinta al canvas y se cierra con `frame.close()`, que es obligatorio para no fugar
  memoria de GPU.
- Cierre por cualquiera de tres caminos —botón, navegar a otra página, o el servidor
  cierra el WS— y siempre igual: cerrar WebSocket, `decoder.close()`, volver a reposo. Si
  el cierre vino del servidor, se muestra el motivo («la emisión terminó»).
- **Sin reconexión automática.** La vista es bajo demanda: si se corta, el usuario ve el
  aviso y decide si reabrir. Reconectar solo repondría el gasto sin que nadie lo pidiera.

## 7. Casos borde

- **Renegociación a mitad** (el publisher cambia resolución o perfil): el `Preamble`
  observa el sequence header nuevo, el tap lo deja pasar, el servidor manda una config
  nueva y el cliente cierra su decodificador, configura otro y espera keyframe.
- **Frames descartados por el tap:** el decodificador nunca ve un GOP roto, porque el
  reenganche es en keyframe. El peor caso visible es una congelación de un GOP.
- **Solo AVC clásico:** la ingesta ya rechaza HEVC/AV1/VP9 (spec base §3.6), así que por el
  tap no circula otra cosa. No hay caso enhanced-RTMP que manejar.
- **Error del decodificador** (frame corrupto, perfil AVC que el hardware no traga): el
  callback `error` cierra todo y muestra el aviso genérico. No se reintenta en bucle.

## 8. Pruebas

**`relay` (el tap)** — correr con `-race`, como toda la suite:

- Un tap recién abierto no recibe frames anteriores al primer keyframe.
- Con el buffer lleno se descarta, y tras el descarte no sale nada hasta el siguiente
  keyframe.
- El audio no entra al tap; los sequence headers de vídeo sí, siempre.
- `Close()` cierra el canal del tap; `release` es idempotente y da de baja.
- Un tap atascado no bloquea a `Publish` ni retrasa a los sinks.

**`httpapi` (el endpoint)**

- Sin sesión → rechazo.
- Sin emisión activa → se cierra con el código «sin señal».
- Con una emisión falsa: el primer mensaje es config, el primer frame es keyframe, y los
  bytes del frame son el payload FLV sin sus 5 bytes de cabecera.
- Una renegociación produce una segunda config por el mismo WebSocket.
- Un cliente que no lee se corta por timeout y su tap queda liberado.

**Navegador** — como en las fases anteriores: la interfaz se verifica ejecutando el binario
real con OBS delante, y la mira el usuario. Los dos fallos de las fases 5 y 6 y los tres
del logo aparecieron ejecutando, no en los tests.

## 9. Lo que este diseño no resuelve

- **Audio en la vista previa.** Si algún día se quiere, el camino natural es reevaluar
  fMP4+MSE, porque WebCodecs de audio obliga a sincronizar a mano. La vista silenciada es
  la razón de que este diseño sea pequeño; quitarle el silencio es otro diseño.
- **Previsualizar sin estar emitiendo.** No hay nada que enseñar.
- **Navegadores sin WebCodecs H.264.** Aviso y nada más; no hay fallback.
- **Multi-espectador con presupuesto.** Cada vista abierta gasta subida; si algún día hay
  varios operadores mirando a la vez, un contador en el panel («2 vistas abiertas») sería
  el primer paso, no un límite.
