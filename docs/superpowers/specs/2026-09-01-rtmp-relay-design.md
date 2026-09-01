# Splitstream — Diseño

**Fecha:** 2026-09-01
**Estado:** aprobado, pendiente de plan de implementación

## 1. Qué es

Servicio de **retransmisión RTMP** self-hosted para un solo usuario. Recibe un stream
desde OBS y lo reenvía simultáneamente a varias plataformas (YouTube, Twitch, Facebook,
Kick, X o cualquier endpoint RTMP/RTMPS genérico), cada una con su URL y clave.

**Solo retransmisión.** Sin transcodificación, sin grabación, sin chat unificado, sin
multi-tenant. Un binario, un usuario, un stream de entrada.

Fuera de alcance de forma explícita y permanente: transcodificar, generar ABR, grabar a
disco, agregar chats, cuentas múltiples, y cualquier ingesta que no sea RTMP.

## 2. Decisiones tomadas

| Decisión | Elegido | Por qué |
| --- | --- | --- |
| Motor de fan-out | In-process en Go | Métricas de primera mano, ~1 goroutine y unos KB por destino en vez de ~60 MB de RSS por proceso ffmpeg, y control total del arranque en keyframe al reconectar |
| Librería RTMP | `github.com/yutopp/go-rtmp` | Servidor y cliente, MIT, y expone `TLSDial` (imprescindible, ver §3.1) |
| Persistencia | SQLite vía `modernc.org/sqlite` | Driver puro Go, sin CGO, binario estático |
| Despliegue | Dev local en macOS + deploy a VPS Linux | Misma config por env en ambos; Docker y systemd para el VPS |
| Drag & drop | `vuedraggable` (SortableJS) | Librería de comportamiento, no de UI; funciona en táctil, que es el caso de uso real |
| Rotar clave en vivo | El diálogo decide | Checkbox "desconectar ahora" para el caso de fuga real; por defecto no corta |

## 3. Correcciones al spec original

Se documentan porque cada una cambia el código y es fácil revertirlas por error.

### 3.1 RTMPS es obligatorio, no un extra

Facebook migró a RTMPS y ya no acepta RTMP plano. X (vía `pscp.tv`) y Kick (infra tipo
IVS) también son `rtmps://…:443`. El publisher saliente habla TLS desde el día uno.
`go-rtmp` lo cubre con `TLSDial(protocol, addr, config, tlsConfig)` y
`DialWithTLSDialer`. El esquema (`rtmp://` vs `rtmps://`) sale de la URL guardada del
destino, no de la plataforma, para que `custom` funcione con ambos.

### 3.2 El rebase de timestamps usa una sola base para audio y video

El spec original decía "tomar como base el primer mensaje que ese destino recibió". Si
eso se aplica por pista, audio y video acaban con bases distintas y el destino
desincroniza — el clásico "el audio va adelantado tras reconectar".

Regla: al conectar, `base = timestamp del keyframe con el que se arranca`, **compartida
por las dos pistas**. El audio con `timestamp < base` se descarta en vez de emitirse
negativo. Ver §6.3.

### 3.3 El descarte es por GOP completo, no por frame

Descartar P-frames sueltos corrompe la decodificación hasta el siguiente IDR: el
destino muestra bloques, que es peor que un salto limpio. Política correcta (la de
nginx-rtmp y SRS): al desbordar la cola se descarta **todo** el video hasta el siguiente
keyframe. El audio se conserva siempre — es barato y su corte se nota mucho más.

### 3.4 La cola se acota por bytes y duración, no por número de mensajes

512 mensajes son 0.3 s a 8 Mbps o 20 s a 500 kbps. Lo que importa es latencia acumulada
y RAM. Límite: **16 MB o 3 s de media**, lo que llegue antes.

### 3.5 El `onMetaData` se reenvía envuelto en `@setDataFrame`

Al republicar hacia un destino, la metadata va como mensaje AMF0 de datos con
`@setDataFrame` de primer elemento. Sin eso las plataformas la ignoran y algunas
rechazan el stream.

### 3.6 Enhanced-RTMP se rechaza en la ingesta

OBS 30+ puede publicar HEVC o AV1 vía enhanced-RTMP (cabecera con FourCC). Un relay puro
no puede convertirlos y Twitch no los acepta, así que el fan-out sería imposible aunque
se parsearan. Se detecta en el primer tag de video y se rechaza la publicación con un
error legible ("configura H.264 + AAC en OBS") en vez de fallar de forma opaca a mitad
de transmisión.

### 3.7 `degraded` es un atributo, no un estado

Estando degradado la conexión sigue arriba. Estados:
`idle | connecting | live | reconnecting | error`, más un `degraded bool` independiente.

### 3.8 La resolución sale del SPS

`onMetaData` es declarativo y puede mentir. La resolución de la sesión se parsea del SPS
del AVC sequence header. El bitrate se mide, no se lee de la metadata.

## 4. Estructura del proyecto

```
splitstream/
├── cmd/splitstream/main.go              # wiring, env, arranque, SIGTERM
├── internal/
│   ├── config/                    # env → struct, defaults, validación
│   ├── crypto/
│   │   ├── secret.go              # AES-256-GCM + key check value
│   │   └── password.go            # argon2id
│   ├── store/
│   │   ├── migrations/*.sql       # embebidas, versionadas
│   │   ├── db.go                  # sqlite, WAL, busy_timeout, migrate()
│   │   ├── settings.go
│   │   ├── destinations.go
│   │   ├── sessions.go
│   │   └── events.go
│   ├── flv/                       # inspección de tags, parse de SPS
│   ├── rtmpio/
│   │   ├── ingest.go              # servidor :1935, auth del publisher
│   │   └── publisher.go           # cliente saliente rtmp:// y rtmps://
│   ├── relay/
│   │   ├── hub.go                 # fan-out, alta/baja de sinks
│   │   ├── session.go             # metadata + sequence headers cacheados
│   │   ├── sink.go                # goroutine por destino: estado, reconexión
│   │   ├── queue.go               # cola acotada, descarte por GOP
│   │   ├── timebase.go            # rebase de timestamps
│   │   └── metrics.go             # bytes, bitrate 5s, drops, uptime
│   ├── api/
│   │   ├── router.go  auth.go  destinations.go  status.go  events.go  ws.go
│   │   └── apierr/                # {"error":{"code","message"}}
│   └── web/embed.go               # go:embed de web/dist/spa
├── web/                           # proyecto Quasar (Vite)
│   └── src/{pages,components,stores,boot}
├── deploy/
│   ├── Dockerfile
│   ├── docker-compose.yml
│   ├── test-compose.yml           # 2× mediamtx para integración
│   └── splitstream.service
├── test/integration/
├── Makefile  go.mod  README.md  LICENSE
```

**Frontera clave:** `internal/relay/` no importa `go-rtmp` ni `database/sql`. Consume
`Message` y escribe contra la interfaz `Publisher`. Eso permite testear el hub completo
con un publisher en memoria, sin Docker y sin red.

## 5. Dependencias

**Go** (deliberadamente pocas):

| Dependencia | Uso |
| --- | --- |
| `github.com/yutopp/go-rtmp` | ingesta y publicación (arrastra `go-flv`, `go-amf0`) |
| `modernc.org/sqlite` | driver SQLite puro Go |
| `golang.org/x/crypto` | argon2id |
| `golang.org/x/time` | rate limit del login |
| `github.com/coder/websocket` | WebSocket mínimo, puro Go |

Sin router HTTP (`net/http.ServeMux` de Go 1.22+ ya hace patrones con método y
wildcards), sin librería de migraciones (runner propio de ~50 líneas sobre `embed.FS`),
sin testify. Logging con `log/slog`.

**Frontend:** Vue 3, Quasar 2, Pinia, `vuedraggable`. Nada más.

**Herramientas de desarrollo:** Go 1.23+, Node 20+, Docker (solo para tests de
integración y build de producción), ffmpeg (solo para tests).

## 6. Arquitectura del motor

### 6.1 Ingesta

El servidor `go-rtmp` escucha en `:1935`. En `OnPublish` se valida `app` + stream key
contra la configuración; si no coincide, se rechaza la conexión sin revelar cuál de las
dos falló.

Por cada mensaje de media el handler inspecciona el primer byte del payload (video:
`frameType`/`codecID` y `AVCPacketType`; audio: `AACPacketType`) y construye:

```go
type Message struct {
    Kind        Kind   // KindAudio | KindVideo | KindMeta
    Timestamp   uint32
    Payload     []byte // inmutable, compartido entre sinks
    IsKeyframe  bool
    IsSeqHeader bool
}
```

El payload es **inmutable y compartido** entre todos los sinks: sin pool ni refcount. A
8 Mbps son ~1 MB/s de asignaciones, irrelevante para el GC. Si algún día se vuelve
medible, se añade un pool detrás de la misma interfaz.

Aquí se aplica también el rechazo de enhanced-RTMP (§3.6).

### 6.2 Hub

`Hub.Publish(msg)` recorre los sinks registrados y hace un encolado **no bloqueante** en
cada uno. Un sink lento nunca bloquea al publisher ni a sus hermanos: la contención se
resuelve dentro de la cola del sink (§6.4).

El hub guarda el `onMetaData` y los dos sequence headers (AVC y AAC) de la sesión. Un
sink que se registre o reconecte a mitad de sesión los obtiene al engancharse.

Alta y baja de sinks en caliente: activar un destino desde la UI lo registra sin tocar
la sesión en curso.

### 6.3 Sink

Una goroutine por destino. Posee su conexión saliente, su máquina de estados, su
timebase y sus métricas.

Secuencia al conectar o reconectar:

1. `connecting` — se abre la conexión (TLS si el esquema es `rtmps`), `connect`,
   `releaseStream` + `FCPublish` si la plataforma los requiere, `createStream`,
   `publish`. Se fija el chunk size de salida en 4096 para no pagar overhead de
   cabeceras.
2. `waitingKeyframe` — se descarta todo lo que llegue hasta el primer keyframe de video.
3. En ese keyframe: `base = keyframe.Timestamp`. Se envían metadata (`@setDataFrame`),
   AVC sequence header y AAC sequence header, los tres con `ts=0`, y luego el keyframe
   con `ts=0`.
4. `live` — `out = msg.Timestamp - base`. El audio con `msg.Timestamp < base` se
   descarta.

En cada reconexión la base se recalcula desde cero: es una sesión RTMP nueva y la
plataforma espera un timeline que arranca en 0, igual que hace OBS al reconectar solo.

### 6.4 Cola y política de descarte

No es un channel: es un deque con mutex, porque la decisión de descarte necesita
inspeccionar lo ya encolado. Límite **16 MB o 3 s de media**, lo que llegue antes.

Al desbordar:

1. Se descarta todo el video ya encolado. Se conservan audio y sequence headers.
2. `droppingVideo = true`; se sigue descartando el video entrante.
3. Al llegar el siguiente keyframe, `droppingVideo = false` y se reanuda desde ahí.
4. `degraded = true` mientras haya habido algún descarte en los últimos 10 s.

### 6.5 Errores y reconexión

Un sink nunca propaga su error al hub. Error → fila en `events` + `state = error` →
backoff `1s × 2ⁿ` topado a 30 s con jitter ±20% → `reconnecting`. Reintentos indefinidos
mientras la sesión siga viva.

Cuando el publisher (OBS) se desconecta, la sesión se cierra y todos los sinks hacen
`FCUnpublish` + `deleteStream` de forma ordenada; pasan a `idle`.

SIGTERM: dejar de aceptar conexiones nuevas, cerrar los sinks con 3 s de gracia, cerrar
la base de datos.

### 6.6 Métricas por destino

En memoria, sin persistir: bytes enviados, bitrate (media móvil de 5 s), frames de video
descartados, uptime de la conexión actual, número de reconexiones, último error.

## 7. Modelo de datos

- **`settings`** — fila única. App y clave de ingesta (la clave cifrada, más sus
  últimos 4 caracteres en claro para poder enmascararla sin descifrar), hash argon2id
  de la contraseña, y key check value de la master key. El puerto RTMP **no** vive
  aquí: es configuración de despliegue y viene de `SPLITSTREAM_RTMP_ADDR` (§12).
- **`destinations`** — `id`, `name`, `platform` (`youtube|twitch|facebook|kick|x|custom`),
  `rtmp_url`, `stream_key_encrypted`, `stream_key_last4`, `enabled`, `sort_order`,
  `created_at`, `updated_at`. `stream_key_last4` está desnormalizado a propósito: el
  listado enmascara sin necesitar la master key, así que descifrar queda confinado al
  único endpoint que revela.
- **`sessions`** — `id`, `started_at`, `ended_at`, resolución (del SPS) y bitrate medido
  del ingest.
- **`events`** — log persistente de conexiones, desconexiones y errores por destino y
  sesión.

Migraciones versionadas en `internal/store/migrations/*.sql`, embebidas y aplicadas al
arranque por un runner propio que lleva la versión en `PRAGMA user_version`. SQLite en
modo WAL con `busy_timeout`.

## 8. Seguridad

**Claves de destino:** AES-256-GCM, nonce aleatorio de 12 bytes prefijado al ciphertext.
Master key de 32 bytes desde `SPLITSTREAM_MASTER_KEY` (base64). Un **key check value** cifrado
se guarda en `settings` y se verifica al arrancar: con la master key equivocada el
binario falla al inicio con un mensaje claro, en vez de devolver basura descifrada.

Dicho sin adornos: el cifrado en reposo protege contra una fuga del `.db` o de un
backup. No protege contra alguien con acceso al proceso o a las variables de entorno.

**Exposición de claves:** la API devuelve siempre la clave enmascarada (`••••1234`).
`GET /api/destinations/:id/key` es el único endpoint que la revela, y cada llamada deja
una fila en `events`. Las claves nunca llegan a los logs — el logger tiene un tipo
`Secret` cuyo `String()` devuelve la máscara.

**Autenticación:** contraseña única con hash argon2id. Cookie httpOnly, `SameSite=Lax`,
`Secure` cuando la petición llega por TLS, firmada con HMAC derivado de
`SPLITSTREAM_MASTER_KEY` — sin tabla de sesiones, porque es un solo usuario. Rate limit del
login con `x/time/rate`: 5 intentos por minuto por IP, más un límite global.

## 9. API HTTP

```
POST   /api/auth/login            → cookie de sesión httpOnly
POST   /api/auth/logout
GET    /api/ingest                → URL de ingesta + key enmascarada
POST   /api/ingest/rotate-key     → body: {"disconnect_now": bool}
GET    /api/destinations
POST   /api/destinations
PATCH  /api/destinations/:id
DELETE /api/destinations/:id
POST   /api/destinations/:id/toggle
POST   /api/destinations/reorder  → body: {"ids": [...]}
GET    /api/destinations/:id/key  → revela la clave en claro
GET    /api/status                → snapshot completo del estado
GET    /api/events?limit=100
GET    /ws                        → push de estado y métricas cada 1s
```

Errores siempre con la forma `{"error": {"code": "...", "message": "..."}}`.

`POST /api/destinations/reorder` no estaba en el spec original; se añade porque el drag
& drop necesita persistir el orden completo en una sola operación en vez de N `PATCH`.

## 10. Frontend

SPA Quasar servida por el binario vía `go:embed`. Dark mode por defecto, responsive
(uso real desde el móvil durante la transmisión).

- **Login** — una contraseña.
- **Dashboard:**
  - Tarjeta de ingesta: URL RTMP y stream key con copiar y mostrar/ocultar, y botón de
    rotar que abre un diálogo con checkbox "desconectar la sesión actual".
  - Estado global: señal entrante, resolución, bitrate, tiempo transmitiendo.
  - Lista de destinos ordenable por drag & drop, con toggle, badge de estado por color
    (incluido el ámbar de `degraded`), bitrate en vivo y menú editar/eliminar.
  - Diálogo de alta/edición con presets por plataforma que precargan la URL conocida
    (`rtmp://a.rtmp.youtube.com/live2`, `rtmp://live.twitch.tv/app`,
    `rtmps://live-api-s.facebook.com:443/rtmp/`, …) dejando solo pegar la clave.
  - Panel de log en vivo con los eventos recientes.

Estado en Pinia alimentado por el WebSocket, con reconexión automática y backoff. El
snapshot inicial viene de `GET /api/status` para que la UI no dependa de que el WS
conecte primero.

## 11. Pruebas

**Unitarias (sin Docker, sin red):**

- Fan-out del hub a N sinks.
- Un patrón sintético de GOPs se descarta **por GOP completo** bajo backpressure, nunca
  a la mitad.
- Un suscriptor tardío recibe metadata + los dos sequence headers antes que cualquier
  media.
- Rebase de timestamps: base compartida A/V, y el audio previo a la base se descarta.
- Los límites del jitter del backoff.

**Integración (`deploy/test-compose.yml`):** dos `mediamtx` más el binario.
`ffmpeg -re -f lavfi -i testsrc -f lavfi -i sine` publica al ingest; `ffprobe` verifica
video y audio en ambas salidas.

**Reconexión:** `docker kill` a un `mediamtx` a media transmisión. Se comprueba que el
contador de bytes del otro sink siguió creciendo de forma monótona y que el destino
matado vuelve a `live` por sí solo al levantarse.

## 12. Operación

Configuración por variables de entorno con defaults sensatos: `SPLITSTREAM_MASTER_KEY`
(obligatoria), `SPLITSTREAM_HTTP_ADDR`, `SPLITSTREAM_RTMP_ADDR`, `SPLITSTREAM_DB_PATH`, `SPLITSTREAM_LOG_LEVEL`.

`Dockerfile` multi-etapa (build de la SPA + build de Go + imagen final distroless),
`docker-compose.yml`, y unidad de systemd como alternativa.

README con instalación, configuración de OBS, y la nota de ancho de banda: **el subida
necesario es bitrate × número de destinos**. Sin transcodificación no hay nada que hacer
si el enlace no da: la única palanca es bajar el bitrate en OBS o desactivar destinos.

## 13. Fases

1. Esqueleto, config, migraciones, modelo de datos, cripto con key check value.
2. **Spike previo (~30 min):** publisher `go-rtmp` contra `mediamtx` local, verificando
   `TLSDial` y si hacen falta `releaseStream`/`FCPublish`. Si el cliente de `go-rtmp` no
   aguanta, el plan B es un publisher propio (~600 líneas: handshake, chunker, AMF0)
   detrás de la misma interfaz `Publisher`, sin tocar el hub. Después: ingesta + hub +
   un destino de punta a punta.
3. N destinos, cola con descarte por GOP, reconexión, métricas.
4. API HTTP completa + WebSocket.
5. Frontend Quasar.
6. Docker, systemd, README, tests.

Parada para revisión al final de cada fase.

## 14. Riesgos

| Riesgo | Mitigación |
| --- | --- |
| El cliente de `go-rtmp` no publica bien contra plataformas reales | Spike al inicio de la fase 2; plan B con publisher propio detrás de la interfaz `Publisher` |
| Cada plataforma tiene su propio dialecto de handshake | Probar contra `mediamtx` primero, luego contra un destino real por plataforma antes de cerrar la fase 3 |
| El VPS no tiene subida suficiente para N destinos | Documentado en el README; la UI muestra el bitrate real por destino para diagnosticarlo |
| Pérdida de `SPLITSTREAM_MASTER_KEY` | Irrecuperable por diseño; el README lo dice explícitamente y recomienda respaldarla aparte del `.db` |
