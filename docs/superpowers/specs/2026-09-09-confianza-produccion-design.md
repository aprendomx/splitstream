# Splitstream — v0.8 «Confianza en producción»

**Fecha:** 2026-09-09
**Estado:** aprobado (decisiones D1–D7 del plan maestro confirmadas por el usuario)
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
**Roadmap:** `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` §7, primera entrega
**Plan maestro:** `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §3
**Versión de partida:** `v0.7.0` + vista previa (`main` @ `ac70a98`)

## 1. Qué se construye

Lo que hace razonable dejar Splitstream corriendo en un VPS sin mirarlo:

1. **Probar destino.** Un botón que comprueba una configuración sin emitir.
2. **Un hook de eventos** por el que pasa todo lo que se persiste en `events`, para que
   cualquier consumidor —alertas hoy, chat mañana— se enganche en un solo sitio.
3. **Alertas**: el log de eventos en el panel (que el spec base §10 pedía y nunca se
   construyó), avisos en el panel por cada error, y webhooks salientes firmados con tres
   formatos: JSON genérico, Discord y Slack.
4. **`/healthz` y `/metrics`**, más un `-healthcheck` para la imagen `scratch`.
5. **Retención** de eventos y sesiones, **respaldo** descargable, y el listado de sesiones
   que la vista de historial (v0.13) necesitará.
6. **Suspender tras N fallos**: un destino que no consigue transmitir deja de reintentar y
   lo dice, en vez de martillear a la plataforma para siempre.

Nada de esto toca el reparto de mensajes del `Hub`, ni la cola, ni el `Publisher`.

## 2. Enmiendas al spec base

Se aplican en el spec base con la fecha de esta entrega.

### 2.1 §6.5 — los reintentos ya no son indefinidos

> **Antes:** «Reintentos indefinidos mientras la sesión siga viva.»
>
> **Desde el 2026-09-09:** reintentos hasta `SuspendAfterAttempts` (10) intentos seguidos
> sin llegar a transmitir, o `SuspendAfterFlaps` (5) sesiones cortas seguidas. Alcanzado
> cualquiera de los dos, el sink pasa a `suspended`, deja de reintentar y emite
> `destination_suspended`. Sigue registrado en el hub: su cola recibe y descarta según su
> política normal, así que no crece. Sale del estado cuando el usuario pulsa «Reintentar»
> —que reconstruye el sink— o cuando empieza la siguiente sesión. `enabled` no se toca:
> la suspensión es de la sesión, no de la configuración.

**Por qué así y no apagando el destino en la base:** el motor no importa `store` y no
debe hacerlo; y apagar la configuración del usuario a sus espaldas sorprende más que
suspender. El caso que motivó la regla —Facebook agotando el cupo de emisiones activas
por nuestros reintentos— se cubre igual: diez intentos con backoff topado a 30 s son unos
dos minutos y diez conexiones, no una por segundo hasta el infinito.

### 2.2 §3.7 — un estado más

`idle | connecting | live | reconnecting | error | suspended`, más el `degraded` aparte.

### 2.3 §9 — endpoints nuevos

```
POST   /api/destinations/{id}/test    → sonda sin emitir (§3)
POST   /api/destinations/{id}/retry   → reconstruye un destino suspendido
GET    /healthz                       → público; 200 ok / 503 degraded
GET    /metrics                       → Prometheus; cookie o Bearer SPLITSTREAM_METRICS_TOKEN
GET    /api/webhooks                  → listado
POST   /api/webhooks
PATCH  /api/webhooks/{id}
DELETE /api/webhooks/{id}
POST   /api/webhooks/{id}/test        → manda un evento sintético
POST   /api/backup                    → descarga un .db consistente
GET    /api/sessions?limit=50&before= → sesiones, de la más reciente a la más antigua
```

`GET /api/status` (y por tanto el WebSocket) gana `recent_events` con los últimos 20
eventos, para que el panel no tenga que sondear `GET /api/events`.

### 2.4 §7 — tabla nueva

`webhooks` (migración 0005). Ver §5.

### 2.5 §12 — variables nuevas

| Variable | Por defecto | Para qué |
| --- | --- | --- |
| `SPLITSTREAM_METRICS_TOKEN` | vacío | Con valor, `/metrics` acepta `Authorization: Bearer`. Vacío: solo cookie de sesión |
| `SPLITSTREAM_RETENTION_DAYS` | `90` | Eventos y sesiones cerradas más viejos se borran. `0` desactiva |
| `SPLITSTREAM_RETENTION_MAX_EVENTS` | `50000` | Tope de filas en `events`. `0` desactiva |

Comandos nuevos: `splitstream -healthcheck` (sale 0 si `/healthz` responde 200) y
`splitstream -backup <ruta>`.

## 3. Probar destino

Una **sonda**: resuelve, conecta (TLS si `rtmps://`), `connect`, `createStream`,
`publish`, espera una gracia de 3 s y cierra con `FCUnpublish`. Vive en `internal/rtmpio`
como `Probe`, y su resultado en un paquete de tipos sin dependencias, `internal/probe`,
para que `httpapi` pueda hablar de sondas sin importar go-rtmp (la CI lo vigila).

Cuatro resultados, con la etapa en la que se decidió:

| Resultado | Significa | Etapas posibles |
| --- | --- | --- |
| `unreachable` | No se llegó a hablar RTMP | `dns`, `tcp`, `tls` |
| `rejected` | La plataforma rechazó el handshake, o la URL no vale | `url`, `connect`, `createStream`, `publish` |
| `closed_early` | Aceptó `publish` y cerró dentro de la gracia | `grace` |
| `plausible` | Aceptó `publish` y seguía abierta al terminar la gracia | `grace` |

**Lo que no promete, y la interfaz lo dice:** `Stream.Publish` de go-rtmp no espera el
`onStatus`, así que una clave mala solo se ve si la plataforma cierra el socket en la
gracia. Twitch lo hace al instante; YouTube tarda más. Por eso el resultado bueno se llama
«plausible» y no «correcta»: solo emitir de verdad confirma la clave. La señal de cierre se
lee de `ClientConn.LastError()`, que go-rtmp fija cuando su bucle de lectura muere.

**Dos reglas de seguridad:**

- Con sesión viva y el destino `live`, **no se prueba** (409). Una segunda publicación con
  la misma clave hace que la plataforma expulse a la que está emitiendo.
- Cada prueba contra Facebook cuenta como emisión activa. El panel lo avisa antes de
  probar un destino de esa plataforma.

Cada prueba deja un evento `destination_tested` (info si plausible, warn si no) sin la
URL ni la clave en el mensaje.

## 4. El hook de eventos

`store.(*DB).SetEventHook(func(Event))`. `LogEvent` lo llama con el evento completo —`ID`
y `CreatedAt` ya asignados— después de que el `INSERT` tenga éxito. Es **un solo punto**:
todos los caminos que escriben eventos (el `storeAdapter` del motor, el `OnEvent` de la
fábrica de sinks, los handlers HTTP, la auditoría de `RevealDestinationKey`) pasan por
`LogEvent`, así que no hay que tocar ninguno.

Dentro de una transacción el hook se dispara antes del `commit`. Se acepta: el único
camino transaccional (`RevealDestinationKey`) solo puede fallar antes del `LogEvent`.

El consumidor es `events.Bus` (`internal/events`): suscriptores con canal con buffer y
**entrega no bloqueante**. Un suscriptor que no lee pierde eventos —se cuentan en
`Dropped()` y salen por `/metrics`— pero jamás frena a la goroutine de un sink. Es la misma
disciplina que el tap de la vista previa.

## 5. Alertas y webhooks

### 5.1 En el panel

- `recent_events` en el estado → un componente `RegistroEventos` al pie del panel, con
  nivel, hora, destino y mensaje. Es el «panel de log en vivo» del spec base §10.
- Un aviso (`$q.notify`) por cada evento de nivel `error` **nuevo** desde el último push.
  El primer estado tras cargar no avisa: son eventos viejos.

### 5.2 Webhooks salientes

```sql
CREATE TABLE IF NOT EXISTS webhooks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT    NOT NULL,
    url              TEXT    NOT NULL,
    format           TEXT    NOT NULL CHECK (format IN ('json','discord','slack')),
    secret_encrypted BLOB,
    min_level        TEXT    NOT NULL CHECK (min_level IN ('info','warn','error')),
    enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    last_status      INTEGER,
    last_error       TEXT    NOT NULL DEFAULT '',
    created_at       TEXT    NOT NULL,
    updated_at       TEXT    NOT NULL
);
```

- La URL debe ser `https://`, salvo `http://localhost` y `http://127.0.0.1` para probar en
  local: la firma no viaja en claro por internet.
- El secreto (opcional, solo tiene sentido en `json`) se cifra con `crypto.Cipher` y no se
  devuelve nunca: la API dice `has_secret`.
- Entrega: una goroutine suscrita al bus con buffer de 256; hasta 4 envíos en paralelo;
  plazo de 10 s por intento; reintentos a 1 s, 4 s y 16 s solo con error de red o 5xx (un
  4xx es configuración, no transitorio). `last_status` y `last_error` se actualizan en cada
  intento final para que el panel enseñe si el aviso llega.
- Formato `json`:
  ```json
  {"id":42,"kind":"destination_suspended","level":"error","message":"…",
   "session_id":7,"destination":{"id":3,"name":"YouTube","platform":"youtube"},
   "at":"2026-09-09T20:15:03.123456789Z"}
  ```
  con cabeceras `X-Splitstream-Event: <kind>`, `User-Agent: splitstream/<versión>` y, si
  hay secreto, `X-Splitstream-Signature: sha256=<hex del HMAC-SHA256 del cuerpo>`.
- Formato `discord`: `{"content":"**[error]** YouTube · <mensaje>"}`. Formato `slack`:
  `{"text":"[error] YouTube · <mensaje>"}`. Sin firma: esas URL llevan su propio token.

## 6. `/healthz`, `/metrics`, `-healthcheck`

- `GET /healthz` es público y responde `{"status":"ok","db":"ok"}` o 503 con
  `"degraded"`. Hace `SELECT 1`; no toca el motor ni revela la versión.
- `GET /metrics` en formato de exposición de Prometheus, escrito a mano (~80 líneas; el
  spec base §5 no quiere una librería para esto). Autenticación: cookie de sesión, o
  `Authorization: Bearer <token>` comparado en tiempo constante. Métricas:

```
splitstream_build_info{version="…"} 1
splitstream_session_live 0|1
splitstream_session_bitrate_bps
splitstream_session_uptime_seconds
splitstream_destination_state{destination="3",name="…",platform="…",state="live"} 1
splitstream_destination_degraded{destination,name,platform}
splitstream_destination_bytes_sent_total{…}
splitstream_destination_bitrate_bps{…}
splitstream_destination_dropped_frames_total{…}
splitstream_destination_reconnections_total{…}
splitstream_destination_queued_bytes{…}
splitstream_events_bus_dropped_total
splitstream_webhook_deliveries_total{result="ok"|"failed"}
```

  `destination_state` es una serie por estado con `1` en el activo y `0` en los demás,
  que es como se consulta cómodo en PromQL. Los valores de etiqueta se escapan (`\`,
  `"`, salto de línea): el nombre del destino lo escribe el usuario.

- `splitstream -healthcheck` hace `GET http://127.0.0.1:<puerto>/healthz` y sale 0 o 1.
  Existe porque la imagen es `scratch` y no tiene `curl`. El `Dockerfile` gana su
  `HEALTHCHECK`.

## 7. Retención, sesiones y respaldo

- `internal/maintenance.Scheduler`: una pasada al minuto de arrancar y otra cada día a las
  04:00 hora local; **nunca con sesión viva** (si la hay, reintenta en 10 minutos). Cada
  pasada deja un evento `maintenance_ran` (info) con lo que borró.
- Jobs: `prune_events` (por fecha y por tope de filas, el que muerda primero) y
  `prune_sessions` (solo cerradas, más viejas que la retención y sin eventos que sigan
  apuntándolas).
- `store.ListSessions(limit, before)` pagina por `id`, nunca por texto de fecha (spec base
  §15.4), y trae los contadores de eventos por nivel.
- Respaldo con `VACUUM INTO` a un archivo temporal y `rename` atómico: copiar el `.db` a
  mano en modo WAL puede dar un archivo inconsistente. `POST /api/backup` lo descarga como
  `splitstream-AAAAMMDD-HHMMSS.db` y deja un evento `backup_downloaded` (warn: es un
  archivo con todas las claves, aunque cifradas). `-backup <ruta>` hace lo mismo desde la
  consola.

## 8. Pruebas

Toda la suite con `-race`, como siempre. Lo específico:

- **relay:** se suspende tras N conexiones fallidas y no hay intento N+1; se suspende tras
  M aleteos; `Stop` despierta a un sink suspendido; la cola de un sink suspendido no crece
  por encima de sus límites; el estado sale como `suspended` en `Metrics()`.
- **rtmpio:** la sonda da `plausible` contra un servidor que acepta, `closed_early` contra
  uno que acepta y cierra, `rejected` contra uno que rechaza `connect`, `unreachable/tcp`
  contra un puerto cerrado, `unreachable/tls` contra un certificado autofirmado.
- **store:** el hook recibe el evento con `ID` y `CreatedAt`; dentro de `InTx` también;
  no se llama si el `INSERT` falla. CRUD de webhooks con URL validada y secreto nunca
  devuelto. Poda por los dos límites. `ListSessions` pagina por `id`. El respaldo abre con
  `store.Open` y pasa `Bootstrap` con la misma clave.
- **events:** N suscriptores reciben; uno que no lee no bloquea `Publish`; `release`
  idempotente.
- **alerts:** firma verificable; reintenta con 5xx y no con 4xx; `min_level` filtra; un
  endpoint que tarda no bloquea el bus.
- **maintenance:** no corre con sesión viva; calcula bien la próxima pasada.
- **httpapi:** `test` da 409 con el destino `live`; `retry` da 409 si no está `suspended`
  y reconstruye si lo está; `/healthz` sin cookie; `/metrics` con token bueno, malo y sin
  nada; el texto de `/metrics` parsea; `recent_events` va en REST y en WS con la misma
  forma; `backup` descarga un archivo que abre; `sessions` pagina.
- **Puerta de salida** (plan maestro §1): 30 minutos contra YouTube, Twitch y Facebook
  reales, con un Prometheus local scrapeando y un webhook de Discord recibiendo.

## 9. Fuera de esta entrega

Alertas por correo (exige SMTP y credenciales: otro mecanismo de secretos que hoy no
existe); métricas persistidas en el tiempo (spec base §6.6: en memoria); `/readyz` aparte
de `/healthz` (un solo proceso, un solo estado); revocar una sesión suelta del panel.
