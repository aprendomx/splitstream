# Roadmap de mejoras — Plan de ejecución

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ejecutar el roadmap de mejoras de Splitstream en siete entregas ordenadas por riesgo —confianza en producción, grabación, instalación, capa de plataformas en dos mitades, alcance internacional y endurecimiento— sin tocar la propiedad central del producto: la parte que retransmite funciona siempre.

**Architecture:** El motor (`internal/relay`) no cambia de forma. Todo lo nuevo entra por tres vías que ya existen: un `Publisher` más (la grabación es un sink que escribe a disco), un hook de eventos a la salida del `LogEvent` (alertas, webhooks, chat), y paquetes de composición que sí pueden importar store, crypto y rtmpio (`internal/sinks` hoy; `internal/alerts`, `internal/record`, `internal/platforms` después). La CI vigila las fronteras con `go list -deps`, igual que hoy.

**Tech Stack:** Go 1.25 stdlib + las cinco dependencias del spec §5 (`go-rtmp`, `modernc.org/sqlite`, `x/crypto`, `x/time`, `coder/websocket`); Vue 3 + Quasar 2 + Pinia + vuedraggable. Ninguna dependencia nueva salvo la que se justifique tarea por tarea y quede anotada en el spec base §5.

**Spec:** `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` (el roadmap, copiado al repo sin las marcas de cita) sobre `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (spec base, autoridad vinculante mientras este plan no diga que se enmienda).

## Cómo leer este plan

Este documento es el plan **maestro**: fija el orden, el alcance exacto de cada entrega, las decisiones que hay que tomar antes de cada una, las enmiendas al spec base, y la descomposición en tareas con archivos, interfaces y pruebas. Cada tarea está pensada para caber en un PR revisable.

Lo que **no** hace es bajar cada tarea al detalle de «escribe este test, córrelo en rojo, escribe esta función». Ese nivel es el de los planes por fase que ya usa este repositorio (3.000–5.000 líneas cada uno) y depende de decisiones que este plan deja tomadas pero que el usuario todavía tiene que confirmar (§«Decisiones pendientes»). El flujo por entrega es el mismo que en las fases 1 a 6 y en la vista previa:

1. **Spec de la entrega** en `docs/superpowers/specs/AAAA-MM-DD-<entrega>-design.md`, corto, con las enmiendas al spec base ya redactadas (fecha y razón, como pide el roadmap §1).
2. **Plan detallado** en `docs/superpowers/plans/AAAA-MM-DD-<entrega>.md`, tarea a tarea, con TDD y commits frecuentes, escrito con `superpowers:writing-plans` a partir de las tareas de este documento.
3. **Ejecución** en rama `feat/<entrega>` desde `main`, con `superpowers:subagent-driven-development`, y **ledger** en `docs/superpowers/plans/AAAA-MM-DD-<entrega>-ledger.md` registrando desviaciones y hallazgos.
4. **PR a `main`**, CI verde verificada por `headSha` (lección de la fase 4), etiqueta `vX.Y.0` que dispara `release.yml`.

## Global Constraints

Copiadas del spec base y del roadmap. Toda tarea las hereda.

- **Sin transcodificación, sin multi-tenant, una ingesta.** (roadmap §1). Grabar es muxear, no transcodificar.
- **`internal/relay` no importa `go-rtmp` ni `database/sql`** (spec §4, CI). Se añade: **no importa `internal/platforms` ni `internal/record`**; ambos entran al motor como `relay.Publisher` o como hook, nunca al revés.
- **`internal/httpapi` no importa `go-rtmp`** (CI). Se añade: **`internal/platforms` no importa `internal/relay` ni `internal/rtmpio`** (roadmap §8).
- **Un solo mecanismo de secretos:** `crypto.Cipher` (AES-256-GCM bajo la clave maestra) y `crypto.Secret` para enmascarar en logs y JSON. Los tokens OAuth, los secretos de webhooks y las credenciales de apps propias del usuario van por ahí (roadmap §8).
- **Las claves y los tokens jamás aparecen en los logs, ni enmascarados** (spec §8).
- **Dependencias deliberadamente pocas** (spec §5). Cada módulo Go o paquete npm nuevo se justifica en el spec de su entrega y se anota en el spec base §5.
- **`go mod tidy` no se ejecuta en este repo** (ledger fase 4, Task 6): las directas se mueven a mano.
- **Tests con `-race`** (`make test`), también estables con `GOMAXPROCS=2`. Tests de integración contra `mediamtx` (`make test-integration`) para todo lo que toque el camino de producción del motor.
- **Comentarios, commits, copys y errores en español**, y los comentarios explican el porqué.
- **Errores de la API con la forma `{"error":{"code","message"}}`** y el conjunto cerrado de códigos de `internal/httpapi/errors.go`; los del store clasificados con `notFound` / `invalidInput` / `conflict`.
- **`statusDTO` es el mismo tipo para `GET /api/status` y para el WebSocket** (spec §10). Todo campo nuevo del estado entra por ahí y se mapea en `dto.go`; `TestMetricsDTOCoversEveryEngineField` sigue vigilando.
- **Migraciones solo hacia delante**, `NNNN_nombre.sql`, `SchemaVersion` actualizada, tolerantes a volver a correr desde la versión 1 (`IF NOT EXISTS` donde aplique; ver 0004), y nunca reconstruir una tabla sin recordar que `migrate()` apaga las claves ajenas (ver 0003).
- **Rama por entrega desde `main`; nada se fusiona con la CI en rojo.**

---

## 0. Estado de partida y tres hallazgos que cambian el roadmap

Verificado sobre `main` @ `ac70a98` el 2026-09-09.

| Dato | Valor |
| --- | --- |
| Última etiqueta | `v0.7.0` (`git describe`: `v0.7.0-10-gac70a98`) |
| Etiquetas existentes | `v0.3.0`, `v0.5.0`, `v0.6.0`, `v0.7.0` |
| Tests unitarios | 411 funciones `Test*` en `internal/` y `cmd/`, todos con `-race` |
| Tests de integración | 3, contra dos `mediamtx` (`test/integration`) |
| Migraciones | 4 (`SchemaVersion = 4`) |
| Código Go | ~17.700 líneas en `internal/` |
| Panel | 5 componentes, 2 páginas, 712 KB compilado |

**Hallazgo 1 — los números de versión del roadmap ya están usados.** El roadmap parte de «v0.5.0» y numera las entregas v0.6 a v0.11, pero `v0.6.0` y `v0.7.0` ya existen como etiquetas (la vista previa y la clave maestra automática salieron después de la v0.5.0). Reutilizar un número rompería `release.yml` y confundiría a quien ya descargó esas versiones. **Este plan renumera** y conserva los nombres:

| Roadmap | Este plan | Nombre |
| --- | --- | --- |
| v0.6 | **v0.8** | Confianza en producción |
| v0.7 | **v0.9** | Grabación |
| v0.8 | **v0.10** | Que instalarlo no duela |
| v0.9 | **v0.11** | Plataformas, primera mitad (Twitch) |
| v0.10 | **v0.12** | Plataformas, segunda mitad (YouTube, Kick) |
| v0.11 | **v0.13** | Alcance internacional |
| v1.0 | **v1.0** | Endurecimiento |

El README todavía dice «v0.5.0» en el ejemplo de `xattr` y `docs/lanzamiento.md` dice «escrito para la v0.6.0»: ambos se corrigen en la Tarea A.0.

**Hallazgo 2 — el «roadmap base» no está en el repositorio.** El roadmap §7 dice «el roadmap base sigue vigente» y reordena sus puntos, pero ese documento no existe en `docs/`. Los puntos que cita (probar destino, alertas y webhooks, `/healthz` y `/metrics`, retención y respaldo, deshabilitar tras N fallos, Homebrew/winget/script, TLS integrado, rate limit tras proxy, aviso de versión, contingencia de go-rtmp, CI estricta, inglés, historial, comparativa) se han tomado **solo del texto del roadmap** y su alcance lo define este plan. Si el roadmap base existe fuera del repo y dice algo distinto, manda él: conviene copiarlo a `docs/superpowers/specs/` antes de escribir el spec de la v0.8.

**Hallazgo 3 — el panel no enseña el log de eventos.** El spec base §10 pide «panel de log en vivo con los eventos recientes»; el store de Pinia lo carga (`refrescarEventos`) pero ningún componente lo pinta. Los eventos `destination_suspect`, `destination_flapping` y `key_revealed` existen y no se ven. Se resuelve dentro de «alertas» en la v0.8 (Tarea A.3), porque sin log visible las alertas no tienen dónde vivir en el panel.

---

## 1. Secuencia, dependencias y puertas

```
v0.8 Confianza ──► v0.9 Grabación ──► v0.10 Instalación ──► v0.11 Twitch ──► v0.12 YouTube+Kick ──► v0.13 Internacional ──► v1.0
   │                    │                    │                    │
   │                    │                    └─ TLS integrado ────┴─► habilita el chat de Kick (v0.12)
   │                    └─ hook de eventos + retención (A.3, A.5) ──► el chat persistido reutiliza ambos (v0.11)
   └─ /metrics, webhooks, probar destino: base para "traer la clave por API" (v0.12 reutiliza probar)
```

Dependencias duras (no se puede empezar una sin la anterior):

- **v0.9 necesita A.5 (retención)**: la retención de grabaciones por días y gigas comparte el planificador diario con la de eventos.
- **v0.11 necesita A.3 (hook de eventos)**: el chat entra al panel por el mismo push que las alertas.
- **v0.12 (Kick chat) necesita C.2 (TLS integrado)**: sin URL pública HTTPS no hay webhook entrante.
- **v0.12 (YouTube) necesita D.1–D.3 (credenciales, almacén de tokens, capacidades)**: es la arquitectura que Twitch valida.

Puertas de salida de cada entrega (además de la CI verde):

| Entrega | Puerta |
| --- | --- |
| v0.8 | 30 minutos contra YouTube + Twitch + Facebook reales con `/metrics` scrapeado por un Prometheus local y un webhook recibido en un endpoint de prueba |
| v0.9 | Una emisión de 1 h grabada en segmentos de 10 min; `kill -9` a mitad; todos los segmentos previos reproducibles con ffplay; **cero descartes en los destinos** mientras se graba en un disco artificialmente lento (`nice`/`cgroup` o `dd` en paralelo) |
| v0.10 | `brew install` en un Mac limpio, `winget install` en una VM Windows, `curl … \| sh` en un VPS con dominio y certificado emitido solo |
| v0.11 | Cambiar el título en Twitch desde el panel y ver el chat de Twitch en el panel durante una emisión real |
| v0.12 | Crear una emisión de YouTube desde el panel, **sin pegar la clave a mano**, y salir al aire; chat de Kick recibido por webhook |
| v0.13 | README en inglés como principal; el panel entero conmuta de idioma; la vista de historial enseña una sesión con eventos, chat y grabación |
| v1.0 | `golangci-lint` y `govulncheck` limpios en CI; integración nocturna verde 7 días seguidos; `docs/api.md` publicado y protegido por test |

---

## 2. Decisiones pendientes (del usuario) y las que este plan toma

El roadmap §8 pide dos decisiones antes de escribir código. Este plan las toma y añade cinco más que el código obliga a tomar. Cada una lleva la recomendación; si el usuario no dice lo contrario, se ejecuta tal cual.

| # | Decisión | Recomendación de este plan | Por qué |
| --- | --- | --- | --- |
| D1 | Numeración de versiones | Renumerar v0.8…v0.13 (Hallazgo 1) | `v0.6.0` y `v0.7.0` ya existen |
| D2 | Almacén de tokens (roadmap §8) | Tabla `platform_accounts` cifrada con `crypto.Cipher`, `crypto.Secret` en memoria, refresco automático en `internal/platforms/tokens` | Un solo mecanismo de secretos |
| D3 | Dónde vive la capa de plataformas (roadmap §8) | `internal/platforms/` con una interfaz por **capacidad** (`TitleSetter`, `ChatReader`, `BroadcastScheduler`, `IngestKeyProvider`) y un paquete por plataforma debajo; CI: `relay` y `rtmpio` no la importan, y ella no importa a ninguno de los dos | Que se pueda borrar Facebook sin que el relay se entere |
| D4 | Flujo OAuth de Twitch con credenciales incluidas | **Device Code Grant**, no Authorization Code + PKCE | PKCE exige una `redirect_uri` registrada de antemano en la app de Twitch; con credenciales incluidas la app es una sola y el panel vive en `localhost:8080` en un PC y en `https://loquesea` en un VPS. El flujo de dispositivo no usa redirect: el usuario abre `twitch.tv/activate`, teclea un código y listo. Kick se evalúa en un spike (Tarea E.5) |
| D5 | «Deshabilitar tras N fallos» | El sink pasa a un estado nuevo `suspended` **dentro de la sesión** y deja de reintentar; **no** se cambia `enabled` en la base. Botón «Reintentar» en el panel. Enmienda al spec §6.5 | El motor no puede escribir en `destinations` (no importa store), y apagar la configuración del usuario desde el motor sorprendería más que suspender la sesión. El caso que motiva la regla —Facebook agotando el cupo por reintentos— se cubre igual |
| D6 | Identidad del sink de grabación | ID reservado `relay.RecorderSinkID = -1` en el hub, filtrado en `httpapi.metricsFor` y con su propio bloque en `statusDTO.Recording` | Reutiliza `Hub.Add` y toda la cola sin tocar el hub; un id negativo no colisiona con `AUTOINCREMENT` |
| D7 | Imagen Docker publicada | Publicar en GHCR (`ghcr.io/aprendomx/splitstream`) desde `release.yml` en la v0.10 | El script de instalación y `docker compose` sin clonar el repo lo necesitan; el ledger de la fase 6 lo dejó como «decisión del dueño» |

---

## 3. Entrega v0.8 — Confianza en producción

**Objetivo:** que dejarlo corriendo en un VPS sin mirar sea razonable: se sabe si está vivo, avisa cuando algo falla, no crece sin control y no insiste para siempre contra un destino roto.

**Enmiendas al spec base** (van en el spec de la entrega, con fecha):

- §6.5 «Reintentos indefinidos mientras la sesión siga viva» → «Reintentos hasta `SuspendAfterAttempts` intentos seguidos sin transmitir, o `SuspendAfterFlaps` sesiones cortas seguidas; después el destino queda `suspended` hasta que el usuario lo reintente o empiece otra sesión».
- §9 gana `POST /api/destinations/{id}/test`, `POST /api/destinations/{id}/retry`, `GET /healthz`, `GET /metrics`, `GET/POST/PATCH/DELETE /api/webhooks`, `POST /api/backup`, `GET /api/sessions`.
- §12 gana `SPLITSTREAM_METRICS_TOKEN`, `SPLITSTREAM_RETENTION_DAYS`, `SPLITSTREAM_RETENTION_MAX_EVENTS`.

### Tarea A.0: Corregir la deriva de versiones en la documentación

**Files:**
- Modify: `README.md` (ejemplo de `xattr` con `v0.5.0`; tabla de estado)
- Modify: `docs/lanzamiento.md` (cabecera «escrito para la v0.6.0»)
- Modify: `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` (nota de renumeración al pie del §7)

**Hecho cuando:** `grep -rn "v0\.[567]\.0" README.md docs/*.md` solo devuelve menciones históricas correctas, y el roadmap del repo lleva la tabla de correspondencia del Hallazgo 1.

### Tarea A.1: Probar destino

Comprueba una configuración de destino **sin emitir**: resuelve el host, abre TCP/TLS, hace `connect`, `createStream`, `publish`, espera una gracia corta y manda `FCUnpublish`. Si la plataforma corta la conexión dentro de la gracia, la clave o la emisión están mal; si aguanta, la configuración es plausible.

**Lo que no puede prometer, y hay que decirlo en la interfaz:** `Stream.Publish` de go-rtmp no espera el `onStatus` (ver `suspectThreshold` en `sink.go`), así que una clave mala solo se detecta si la plataforma cierra el socket en la gracia. Twitch lo hace al instante; YouTube tarda más. El resultado se llama «plausible», no «correcta».

**Regla de seguridad:** si hay sesión viva y el destino está `live`, **no se prueba** (409): una segunda publicación con la misma clave hace que la plataforma expulse a la que está emitiendo. Y cada prueba contra Facebook cuenta como emisión activa: el diálogo lo avisa.

**Files:**
- Create: `internal/rtmpio/probe.go`, `internal/rtmpio/probe_test.go` (contra un `rtmp.Server` de go-rtmp en un puerto efímero, como hace `publisher_test.go`)
- Create: `internal/sinks/tester.go`, `internal/sinks/tester_test.go`
- Create: `internal/httpapi/test_destination.go`, `internal/httpapi/test_destination_test.go`
- Modify: `internal/httpapi/server.go` (`Config.Tester`, ruta `POST /api/destinations/{id}/test`)
- Modify: `cmd/splitstream/main.go` (cablear `Tester: factory`)
- Modify: `web/src/api.js`, `web/src/components/TarjetaDestino.vue` (menú «Probar»), `web/src/pages/Panel.vue` (diálogo de resultado)
- Modify: `docs/manual-de-usuario.md` §5

**Interfaces:**
- Produces: `rtmpio.Probe(ctx context.Context, cfg PublisherConfig, grace time.Duration) ProbeResult` con
  ```go
  type ProbeOutcome uint8
  const (
      ProbeUnreachable ProbeOutcome = iota // DNS, TCP o TLS fallaron
      ProbeRejected                        // connect/createStream/publish devolvieron error
      ProbeClosedEarly                     // aceptó publish y cerró dentro de la gracia
      ProbePlausible                       // aceptó publish y seguía abierto al mandar FCUnpublish
  )
  type ProbeResult struct {
      Outcome  ProbeOutcome
      Stage    string        // "dns" | "tcp" | "tls" | "connect" | "createStream" | "publish" | "grace"
      Elapsed  time.Duration
      Err      error         // nunca contiene la URL ni la clave (misma regla que parseTarget)
  }
  ```
- Produces: `sinks.(*Factory).Test(ctx, d store.Destination) (rtmpio.ProbeResult, error)` (descifra con `DestinationKeyForRelay`, gracia 3 s) y la interfaz `httpapi.DestinationTester { Test(ctx, store.Destination) (rtmpio.ProbeResult, error) }`.
- Produces: DTO `{"outcome":"plausible|closed_early|rejected|unreachable","stage":"…","elapsed_ms":n,"message":"texto para personas"}`; el evento `destination_tested` (info o warn) queda en `events`.

**Pruebas:** un servidor go-rtmp falso que (a) acepta y mantiene, (b) acepta y cierra a los 200 ms, (c) rechaza `connect`; un puerto cerrado; un certificado autofirmado en `rtmps://` sin `InsecureSkipVerify` → `ProbeUnreachable` en `tls`. En la API: 409 con sesión viva y destino `live`; 404 con id inexistente; el evento se escribe.

**Hecho cuando:** contra Twitch real con una clave inventada devuelve `closed_early` en < 4 s, y con la clave buena `plausible`; el panel enseña el resultado con el consejo del diagnóstico.

### Tarea A.2: Hook de eventos a la salida del store

Un solo punto por el que pasan **todos** los eventos ya persistidos, para que alertas, webhooks y —en la v0.11— el chat no tengan que engancharse en tres sitios (el `storeAdapter` de `main`, el `OnEvent` de `sinks.Factory` y los handlers HTTP que escriben `LogEvent` a mano).

**Files:**
- Create: `internal/events/bus.go`, `internal/events/bus_test.go`
- Modify: `internal/store/events.go` (`LogEvent` devuelve el `Event` completo con `ID` y `CreatedAt`, además del id, para no releer)
- Modify: `cmd/splitstream/main.go` (`storeAdapter` publica en el bus tras escribir)
- Modify: `internal/sinks/factory.go` (`NewFactory` gana `Options{AfterLog func(store.Event)}`)
- Modify: `internal/httpapi/server.go` (`Config.AfterLog`), y los tres handlers que llaman a `LogEvent` (`ingest.go`, `setup.go`, `store.RevealDestinationKey` vía `destinations.go`) pasan por un helper `s.logEvent(ctx, ev)` que persiste y publica

**Interfaces:**
- Produces:
  ```go
  package events
  type Bus struct{ /* mutex + suscriptores */ }
  func NewBus() *Bus
  // Subscribe devuelve un canal con buffer y un release. La entrega es NO bloqueante: un
  // suscriptor lento pierde eventos (se cuenta en Dropped) pero jamás frena al motor.
  func (b *Bus) Subscribe(buffer int) (<-chan store.Event, func())
  func (b *Bus) Publish(ev store.Event)
  func (b *Bus) Dropped() uint64
  ```
- `internal/events` importa `internal/store` (por el tipo `Event`) y nada más. `relay` no lo conoce.

**Pruebas:** N suscriptores reciben el mismo evento; un suscriptor que no lee no bloquea `Publish` (medido con plazo); `release` idempotente; `-race` con publicación concurrente.

**Hecho cuando:** `grep -rn "LogEvent(" internal cmd | grep -v _test | grep -v "func (d \*DB)"` solo devuelve el helper de `httpapi`, el `storeAdapter` y el `OnEvent` de la factory, y los tres publican en el bus.

### Tarea A.3: Alertas en el panel y webhooks salientes

Dos consumidores del bus. El primero es el panel: el log de eventos que el spec §10 pedía y no existe (Hallazgo 3), más un aviso (`$q.notify`) por cada evento `error`. El segundo son webhooks HTTP con firma HMAC, con tres formatos: `json` (genérico), `discord` y `slack` (los dos que un streamer va a querer, y son un campo cada uno).

**Files:**
- Create: `internal/store/migrations/0005_webhooks.sql`:
  ```sql
  CREATE TABLE IF NOT EXISTS webhooks (
      id               INTEGER PRIMARY KEY AUTOINCREMENT,
      name             TEXT    NOT NULL,
      url              TEXT    NOT NULL,
      format           TEXT    NOT NULL CHECK (format IN ('json','discord','slack')),
      secret_encrypted BLOB,            -- NULL en discord/slack: firman con la URL
      min_level        TEXT    NOT NULL CHECK (min_level IN ('info','warn','error')),
      enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
      last_status      INTEGER,         -- último código HTTP, para el panel
      last_error       TEXT,
      created_at       TEXT    NOT NULL,
      updated_at       TEXT    NOT NULL
  );
  ```
  y `SchemaVersion = 5`.
- Create: `internal/store/webhooks.go`, `internal/store/webhooks_test.go` (CRUD; la URL se valida `https://` salvo `http://127.0.0.1` y `http://localhost`, para no mandar la firma en claro por internet; el secreto va por `crypto.Cipher` y nunca se devuelve, solo `HasSecret bool`).
- Create: `internal/alerts/webhooks.go`, `internal/alerts/webhooks_test.go` (con `httptest.Server`), `internal/alerts/payload.go`, `internal/alerts/payload_test.go`
- Create: `internal/httpapi/webhooks.go`, `internal/httpapi/webhooks_test.go` (CRUD + `POST /api/webhooks/{id}/test` que manda un evento sintético)
- Modify: `internal/httpapi/ws.go` y `dto.go`: `statusDTO` gana `RecentEvents []eventDTO` (últimos 20) para que el panel no tenga que sondear `GET /api/events`; `TestStatusDTOShape` se actualiza.
- Create: `web/src/components/RegistroEventos.vue`; Modify: `web/src/pages/Panel.vue` (columna/desplegable de log), `web/src/stores/panel.js` (aviso por evento `error` nuevo, comparando ids), `web/src/pages/Ajustes.vue` (nueva página: webhooks; en la v0.9 gana la grabación), `web/src/router.js`, `web/src/App.vue` (botón de ajustes)
- Modify: `cmd/splitstream/main.go` (arranca `alerts.NewWebhookDispatcher(bus, db, cipher, logger)`), `docs/manual-de-usuario.md` (§ nuevo «Avisos»)

**Interfaces:**
- Produces:
  ```go
  package alerts
  type WebhookDispatcher struct{ /* … */ }
  func NewWebhookDispatcher(bus *events.Bus, db *store.DB, c *crypto.Cipher, log *slog.Logger) *WebhookDispatcher
  func (d *WebhookDispatcher) Run(ctx context.Context) // una goroutine; cola acotada de 256; 3 reintentos con 1 s, 4 s, 16 s; plazo de 10 s por envío
  func (d *WebhookDispatcher) Send(ctx context.Context, w store.Webhook, ev store.Event) error // usado por /test
  ```
- Payload `json`: `{"id":n,"kind":"…","level":"…","message":"…","session_id":n|null,"destination":{"id":n,"name":"…","platform":"…"}|null,"at":"RFC3339"}`, cabeceras `X-Splitstream-Event: <kind>`, `X-Splitstream-Signature: sha256=<hex HMAC del cuerpo con el secreto>` cuando hay secreto, `User-Agent: splitstream/<version>`.
- Payload `discord`: `{"content":"**[error]** el destino YouTube falla siempre antes de transmitir…"}`; `slack`: `{"text":"…"}`.

**Pruebas:** el dispatcher no bloquea el bus aunque el endpoint tarde 30 s (plazo de 10 s, cola acotada); firma verificable con el mismo secreto; reintentos con 5xx y no con 4xx; `min_level` filtra; `last_status`/`last_error` se actualizan; el `statusDTO` lleva `recent_events` en REST y WS con la misma forma.

**Hecho cuando:** un webhook de Discord real recibe «el publisher conectó» al arrancar OBS y «el destino se desconectó» al apagar un canal; el panel enseña esos mismos eventos sin recargar.

### Tarea A.4: `/healthz`, `/metrics` y `-healthcheck`

Sin librería de Prometheus: la exposición en texto son ~80 líneas y el spec §5 no quiere más dependencias.

**Files:**
- Create: `internal/httpapi/health.go`, `internal/httpapi/health_test.go`
- Create: `internal/httpapi/metrics.go`, `internal/httpapi/metrics_test.go`
- Modify: `internal/httpapi/server.go` (`Config.MetricsToken`; rutas `GET /healthz` pública y `GET /metrics` con `requireSessionOrToken`)
- Modify: `internal/config/config.go` (`MetricsToken` desde `SPLITSTREAM_METRICS_TOKEN`; omitido en `LogValue` y `MarshalJSON` como la master key), `config_test.go`
- Modify: `cmd/splitstream/main.go` (flag `-healthcheck`: hace `GET http://127.0.0.1:<puerto>/healthz` y sale 0/1; existe porque la imagen es `scratch` y no hay `curl`), `main_test.go`
- Modify: `deploy/Dockerfile` (`HEALTHCHECK CMD ["/splitstream","-healthcheck"]`), `deploy/docker-compose.yml` (quitar el comentario «sin healthcheck»), `deploy/splitstream.service` (sin cambios; documentar `curl /healthz` en el README)
- Modify: `README.md` (§ Operación: los dos endpoints y el token)

**Interfaces:**
- `GET /healthz` → `200 {"status":"ok","db":"ok","version":"…"}` o `503 {"status":"degraded","db":"error"}` (hace `SELECT 1`; no toca el motor). Pública: no revela más que «existe».
- `GET /metrics` → `text/plain; version=0.0.4`. Autenticación: cookie de sesión **o** `Authorization: Bearer <SPLITSTREAM_METRICS_TOKEN>`; con el token vacío, solo cookie. Métricas:
  ```
  splitstream_build_info{version="…"} 1
  splitstream_session_live 0|1
  splitstream_session_bitrate_bps
  splitstream_session_uptime_seconds
  splitstream_destination_state{destination="<id>",name="…",platform="…",state="live"} 1   # una serie por estado, 1 en el activo
  splitstream_destination_degraded{…} 0|1
  splitstream_destination_bytes_sent_total{…}
  splitstream_destination_bitrate_bps{…}
  splitstream_destination_dropped_frames_total{…}
  splitstream_destination_reconnections_total{…}
  splitstream_destination_queued_bytes{…}
  splitstream_events_bus_dropped_total
  splitstream_webhook_deliveries_total{result="ok|failed"}
  ```
  Las etiquetas escapan `\`, `"` y `\n` según el formato; el nombre del destino lo elige el usuario y puede llevar cualquier cosa.

**Pruebas:** `/healthz` sin cookie da 200; con la base cerrada da 503; `/metrics` sin nada da 401, con token bueno 200, con token malo 401 en tiempo constante; el texto parsea con un parser mínimo del test (una línea por métrica, etiquetas escapadas); `-healthcheck` sale 1 con el servidor parado.

**Hecho cuando:** un Prometheus local scrapea `/metrics` cada 15 s durante una emisión y `docker compose ps` enseña `healthy`.

### Tarea A.5: Retención y respaldo

**Files:**
- Create: `internal/store/retention.go`, `internal/store/retention_test.go`
- Create: `internal/store/backup.go`, `internal/store/backup_test.go` (`VACUUM INTO ?` sobre una ruta temporal y rename atómico; no se copia el archivo porque en WAL una copia a mano puede quedar inconsistente)
- Create: `internal/store/sessions.go`, `internal/store/sessions_test.go` (`ListSessions(ctx, limit, before int64)` ordenadas por `id DESC`, con los contadores de eventos por nivel; base de la vista de historial de la v0.13)
- Create: `internal/maintenance/scheduler.go`, `internal/maintenance/scheduler_test.go` (un ticker diario a las 04:00 hora local + una pasada al arrancar; **nunca corre con sesión viva**: espera al siguiente tick; en la v0.9 gana la poda de grabaciones)
- Create: `internal/httpapi/backup.go`, `internal/httpapi/backup_test.go`, `internal/httpapi/sessions.go`, `internal/httpapi/sessions_test.go`
- Modify: `internal/config/config.go` (`RetentionDays` por defecto 90, `RetentionMaxEvents` por defecto 50.000; 0 desactiva), `cmd/splitstream/main.go` (flag `-backup <ruta>` y arranque del scheduler), `README.md`, `docs/manual-de-usuario.md`

**Interfaces:**
- `store.(*DB).PruneEvents(ctx, olderThan time.Time, keepAtMost int) (deleted int64, err error)`; `PruneSessions(ctx, olderThan time.Time) (int64, error)` (solo sesiones cerradas, y sus eventos ya podados).
- `store.(*DB).BackupTo(ctx, path string) error`.
- `maintenance.Scheduler{ Jobs []Job; IsBusy func() bool }` con `type Job struct{ Name string; Run func(ctx) error }`; cada corrida deja un evento `maintenance_ran` (info) con lo borrado.
- `POST /api/backup` → descarga `splitstream-AAAAMMDD-HHMMSS.db` (`Content-Disposition: attachment`), deja evento `backup_downloaded` (warn: es un archivo con todas las claves cifradas). La respuesta recuerda en una cabecera `X-Splitstream-Note` que sin `splitstream.key` el archivo es ilegible.
- `GET /api/sessions?limit=50&before=<id>` → `[{id, started_at, ended_at, width, height, bitrate_bps, events:{info,warn,error}}]`.

**Pruebas:** la poda respeta ambos límites y el que muerde primero; no corre con `IsBusy()` true; el backup abre con `store.Open` y pasa `Bootstrap` con la misma clave; `ListSessions` pagina por id y no por texto de fecha (spec §15.4).

**Hecho cuando:** con `SPLITSTREAM_RETENTION_DAYS=1` y una base con eventos viejos, el arranque los poda y lo dice en el log; el `.db` descargado desde el panel arranca en otra carpeta con la misma `.key`.

### Tarea A.6: Suspender tras N fallos

Enmienda al spec §6.5 (D5). El sink deja de reintentar y lo dice; el usuario decide.

**Files:**
- Modify: `internal/relay/sink.go` (`StateSuspended`; constantes `SuspendAfterAttempts = 10`, `SuspendAfterFlaps = 5`, sobreescribibles en `SinkConfig`; en `run()`, al alcanzar cualquiera de las dos: evento `destination_suspended` (error), `setState(StateSuspended)`, y la goroutine espera en `quit`/`ctx` **sin** consumir la cola —la cola sigue aplicando su política de descarte y no crece—)
- Modify: `internal/relay/sink_test.go`, `sink_reconnect_test.go` (con `FakePublisher` que falla siempre: tras 10 intentos el estado es `suspended`, no hay un intento 11, y `Metrics().State == "suspended"`)
- Modify: `internal/relay/engine.go` (`ResumeSink(id int64)` = `RemoveSink` + el `SinkProvider` reconstruye; en la práctica la API llama a `applyHot`)
- Modify: `internal/httpapi/destinations.go` (`POST /api/destinations/{id}/retry`: 409 si no está `suspended`; reconstruye con `sinks.Build` y `AddSink`), `dto_test.go`
- Modify: `web/src/diagnostico.js` (estado `suspended` → tono `fallo`, título «Suspendido», consejo «Revisa la clave y pulsa Reintentar»), `TarjetaDestino.vue` (botón «Reintentar» solo en ese estado), `docs/manual-de-usuario.md` §4 y §5
- Modify: spec base §6.5 (enmienda con fecha)

**Pruebas:** además de las del sink, `TestHubSnapshotIncludesSuspended`; en la API: `retry` sobre un destino `live` da 409; sobre uno `suspended` vuelve a `connecting`.

**Hecho cuando:** con una clave inventada en Twitch, el destino queda «Suspendido» en unos 2 minutos (10 intentos con backoff topado a 30 s), Facebook no recibe más de 10 conexiones, y «Reintentar» lo vuelve a intentar.

### Definición de terminado de la v0.8

- Spec de la entrega aprobado, con las tres enmiendas al spec base redactadas.
- `make test`, `make vet`, `make test-integration` en verde; CI verde por `headSha`.
- Puerta de la §1 superada y anotada en el ledger con fecha y plataformas.
- README y manual actualizados; `docs/lanzamiento.md` sigue diciendo la verdad.
- Etiqueta `v0.8.0`.

---

## 4. Entrega v0.9 — Grabación

**Objetivo:** grabar la sesión a disco como un sink más, con la regla que el roadmap §6 llama innegociable escrita en el spec **antes** del código: si el disco se atrasa se degrada la grabación, nunca el directo.

**Enmiendas al spec base:**

- §1 «Fuera de alcance de forma explícita y permanente: … grabar a disco» → revertido el 2026-09-09: «Grabar sin transcodificar es muxear; entra como un `Publisher` más detrás de la misma cola y la misma política de descarte que cualquier destino».
- §6.2: el hub admite un sink con `ID = RecorderSinkID (-1)` que no corresponde a ninguna fila de `destinations`.
- §7: tablas `recordings`; columnas de grabación en `settings`.
- §12: `SPLITSTREAM_RECORDINGS_DIR` (por defecto `recordings/` junto a la base).

**Decisión de diseño que este plan toma:** la grabación es un `relay.Publisher` (`record.FLVWriter`) envuelto en un `relay.Sink` normal. Así hereda cola acotada, descarte por GOP, métricas, `degraded` y el apagado ordenado **sin tocar `hub.go` ni `sink.go`**. La segmentación vive dentro del publisher: rota el archivo en el primer keyframe tras `SegmentMinutes`, reescribe el preámbulo (que cachea al recibirlo) y rebasa los timestamps del segmento nuevo a 0.

### Tarea B.1: Escritor FLV como `relay.Publisher`

**Files:**
- Create: `internal/record/flv.go`, `internal/record/flv_test.go` (escribe en un `io.Writer`; el test lee el archivo con un parser FLV mínimo del propio test y comprueba cabecera, `PreviousTagSize`, tipos 8/9/18 y timestamps)
- Create: `internal/record/writer.go`, `internal/record/writer_test.go` (`FLVWriter` implementa `relay.Publisher`; `Connect` abre `<dir>/<sesión>/<AAAAMMDD-HHMMSS>-<nn>.flv` con `O_EXCL`; `WriteMeta/WriteAudio/WriteVideo` añaden tags; `Close` hace `Sync` y cierra; segmentación por keyframe)
- Modify: `internal/relay/sink.go` solo para exportar `RecorderSinkID int64 = -1` (constante, sin lógica)

**Interfaces:**
```go
package record
type Options struct {
    Dir            string
    SessionID      int64
    SegmentMinutes int           // 0 = sin segmentar
    OnSegment      func(Segment) // se llama al cerrar cada archivo, para persistirlo
    Now            func() time.Time
}
type Segment struct{ Path string; Index int; StartedAt, EndedAt time.Time; Bytes int64; DurationMS uint32 }
func NewFLVWriter(o Options) *FLVWriter
var _ relay.Publisher = (*FLVWriter)(nil)
```
**Regla de no bloqueo:** cada `Write*` hace una sola llamada a `bufio.Writer.Write` (buffer de 1 MiB) y un `Flush` cada 2 s por timestamp de media; nunca `Sync` en el camino caliente. Si el disco se atrasa, el `Write` tarda, la cola del sink se llena y **descarta vídeo de la grabación**: exactamente la política de cualquier destino. El directo no se entera porque `Hub.Publish` es no bloqueante.

**Pruebas:** un patrón sintético de 3 GOPs produce un FLV que `ffprobe` (si está en el PATH; si no, `t.Skip`) reconoce con vídeo y audio; con `SegmentMinutes=1` y timestamps de 150 s salen 3 archivos, cada uno empezando por meta + 2 sequence headers + keyframe con `ts=0`; un `io.Writer` que bloquea 5 s no impide que un `Sink` hermano (en el mismo hub) siga recibiendo (reutiliza `TestHubSlowSinkDoesNotBlockOthers`).

### Tarea B.2: Cuota de disco y retención de grabaciones

**Files:**
- Create: `internal/record/disk_unix.go` (`syscall.Statfs`), `internal/record/disk_windows.go` (`syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")`; sin dependencias nuevas), `internal/record/disk_test.go`
- Create: `internal/record/quota.go`, `internal/record/quota_test.go`
- Create: `internal/store/migrations/0006_recordings.sql`:
  ```sql
  CREATE TABLE IF NOT EXISTS recordings (
      id          INTEGER PRIMARY KEY AUTOINCREMENT,
      session_id  INTEGER REFERENCES sessions (id) ON DELETE SET NULL,
      path        TEXT    NOT NULL UNIQUE,
      segment     INTEGER NOT NULL,
      started_at  TEXT    NOT NULL,
      ended_at    TEXT,
      bytes       INTEGER NOT NULL DEFAULT 0,
      duration_ms INTEGER NOT NULL DEFAULT 0
  );
  ALTER TABLE settings ADD COLUMN recording_enabled      INTEGER NOT NULL DEFAULT 0;
  ALTER TABLE settings ADD COLUMN recording_segment_min  INTEGER NOT NULL DEFAULT 10;
  ALTER TABLE settings ADD COLUMN recording_max_gb       REAL    NOT NULL DEFAULT 20;
  ALTER TABLE settings ADD COLUMN recording_keep_days    INTEGER NOT NULL DEFAULT 30;
  ```
  (`ALTER TABLE ADD COLUMN` es idempotente solo si se protege; el runner no tiene `IF NOT EXISTS` para columnas: el test de migraciones que rebobina `user_version` deberá restaurar el esquema desde cero, o la migración comprobará `pragma_table_info` antes. Decidirlo en el plan de la entrega.)
- Create: `internal/store/recordings.go`, `internal/store/recordings_test.go`, `internal/store/recording_settings.go`
- Modify: `internal/maintenance` (job `prune_recordings`: primero por días, luego por gigas hasta bajar del límite —«la de gigas manda»—; borra archivo y fila juntos, archivo primero)

**Interfaces:**
```go
package record
type Quota struct{ MaxBytes int64; WarnAt float64 /* 0.8 */ }
func FreeSpace(dir string) (free, total int64, err error)
// Check se llama ANTES de arrancar una sesión y cada segmento: devuelve si se puede grabar
// y si hay que avisar. Con menos de 2×segmento estimado de sitio, no se arranca.
func (q Quota) Check(usedByRecordings, free int64) (ok bool, warn bool)
```
**Eventos:** `recording_started`, `recording_segment` (info), `recording_disk_warning` (warn al 80 %), `recording_stopped_disk_full` (error: parada limpia del sink de grabación, **la sesión sigue**), `recording_pruned` (info).

### Tarea B.3: Cablear el sink de grabación en la sesión

**Files:**
- Modify: `internal/sinks/factory.go` (`BuildRecorder(ctx) (*relay.Sink, error)` que lee `settings`, comprueba cuota y devuelve `nil, nil` si la grabación está apagada o no hay sitio —con evento—)
- Modify: `cmd/splitstream/main.go` (el `SinkProvider` añade el recorder a la lista si procede)
- Modify: `internal/httpapi/destinations.go` (`metricsFor` ignora `RecorderSinkID`), `dto.go` (`statusDTO.Recording {Enabled, Active, Segment, Bytes, Degraded, FreeBytes, Dir}`), `status.go`
- Create: `internal/httpapi/recordings.go`, `_test.go`: `GET /api/recording/settings`, `PATCH /api/recording/settings` (aplica en caliente: encender con sesión viva arranca el recorder; apagar lo para), `GET /api/recordings?session_id=`, `GET /api/recordings/{id}/download` (`http.ServeContent`, `Content-Disposition`), `DELETE /api/recordings/{id}`
- Modify: `web/src/pages/Ajustes.vue` (bloque «Grabación»: interruptor, minutos por segmento, tope en GB, días), `web/src/pages/Panel.vue` (chip «Grabando · 1,2 GB · disco 63 %»), Create: `web/src/pages/Grabaciones.vue` (lista con descarga y borrado)
- Modify: `test/integration/relay_test.go` → nuevo `recording_test.go`: ffmpeg publica 20 s, se comprueba con `ffprobe` que el FLV resultante tiene vídeo y audio y ~20 s
- Modify: `README.md`, `docs/manual-de-usuario.md` (§ «Grabar»: dónde quedan los archivos, cómo pasarlos a MP4 con `ffmpeg -i x.flv -c copy x.mp4`)

**Hecho cuando:** puerta de la §1 (1 h, `kill -9`, disco lento, cero descartes en destinos).

### Tarea B.4: fMP4 fragmentado (segunda entrega dentro de la v0.9, puede ir a v0.9.1)

Spec propio: es la pieza con más código propenso a error de todo el roadmap (boxes `ftyp/moov/moof/mdat`, `avcC` desde el sequence header, `esds` desde el AudioSpecificConfig, `tfdt` por fragmento). Referencia: `bluenviron/mediamtx` (`internal/formats/fmp4`), que hace exactamente esto en Go.

**Files:** Create `internal/record/fmp4/{boxes.go,writer.go,avcc.go,esds.go}` con tests que verifican contra `ffprobe -show_packets`; `record.NewFMP4Writer` con la misma `Options`; la elección de formato es una columna más en `settings` (`recording_format` `flv|fmp4`, por defecto `flv` hasta que la fMP4 pase un mes en uso real).

**Hecho cuando:** un `.mp4` cortado con `kill -9` abre en QuickTime y en VLC y reproduce todo lo escrito hasta el corte.

### Definición de terminado de la v0.9

Spec con las cuatro enmiendas; B.1–B.3 fusionadas y probadas con la puerta; B.4 fusionada o explícitamente pospuesta a v0.9.1 en el ledger; `docs/lanzamiento.md` §«Qué no hace» deja de decir «no graba».

---

## 5. Entrega v0.10 — Que instalarlo no duela

### Tarea C.1: Homebrew, winget e `install.sh`

**Files:**
- Create (repo nuevo): `aprendomx/homebrew-tap` con `Formula/splitstream.rb` (descarga el `.tar.gz` de la release por arquitectura, `sha256` de `SHA256SUMS.txt`, `service do … end` para `brew services`)
- Create: `.github/workflows/release.yml` gana un job `tap` que abre un PR en el tap con la fórmula regenerada (token `TAP_TOKEN` en secretos) y un job `ghcr` que publica la imagen (D7) con etiquetas `vX.Y.Z` y `latest`
- Create: `deploy/winget/aprendomx.Splitstream.yaml` (+ `.installer.yaml`, `.locale.es-MX.yaml`) generados por `wingetcreate` en el job de release y subidos como artefacto; el PR a `microsoft/winget-pkgs` se abre a mano la primera vez (exige aprobación humana de su lado) y automatizado después
- Create: `deploy/install.sh` (detecta SO/arquitectura, descarga la última release, verifica el checksum, instala en `/usr/local/bin`, ofrece instalar la unidad de systemd si hay `systemctl`; **nunca `sudo` sin decirlo**); `deploy/install_test.sh` (corre en la CI dentro de un contenedor `ubuntu` y de `alpine`)
- Modify: `README.md` (instalación en tres líneas por plataforma; el `xattr` de macOS deja de hacer falta con Homebrew porque `brew` quita la cuarentena)

**Hecho cuando:** puerta de la §1.

### Tarea C.2: TLS integrado

Con `golang.org/x/crypto/acme/autocert`, que ya está en el módulo `x/crypto` que el proyecto trae: **cero dependencias nuevas**.

**Files:**
- Modify: `internal/config/config.go` (`TLSDomain` desde `SPLITSTREAM_TLS_DOMAIN`, `TLSCacheDir` por defecto `tls-cache/` junto a la base, `TLSCertFile`/`TLSKeyFile` para certificado propio; con dominio o certificado, `SecureCookies` pasa a `true` salvo que se ponga en `false` a mano)
- Modify: `cmd/splitstream/main.go` (con TLS: `:443` con `autocert.Manager{Prompt: AcceptTOS, HostPolicy: HostWhitelist(domain), Cache: DirCache}` y `:80` con `m.HTTPHandler(nil)` para el reto HTTP-01 y la redirección; sin TLS, igual que hoy), `main_test.go`
- Modify: `internal/httpapi/status.go` (`statusDTO.Panel {TLS bool, PublicURL string}`: lo necesita el chat de Kick en la v0.12 para saber si hay URL pública)
- Modify: `deploy/splitstream.service` (`AmbientCapabilities=CAP_NET_BIND_SERVICE` comentado, con la explicación), `deploy/docker-compose.yml` (variante con `443:443` y `80:80` comentada), `README.md` (§ «Ponerlo en internet»: dominio, puertos, y que Let's Encrypt limita a 5 certificados por semana por dominio —no reinicies en bucle con el `tls-cache` borrado—)

**Pruebas:** con `TLSCertFile` autofirmado en el test, el servidor sirve HTTPS y la cookie sale `Secure`; sin dominio ni certificado, nada cambia respecto a hoy (test de regresión sobre `run()`).

### Tarea C.3: Rate limit y detección de «local» detrás de un proxy

Hoy `clientIP` y `esLocal` ignoran `X-Forwarded-For` a propósito (bien: quien llega directo lo puede inventar). Detrás de Caddy o nginx **todas** las IP son la del proxy: el limitador del login castiga a todos por uno, y el asistente cree que todo es remoto (correcto) o —peor— si el proxy corre en la misma máquina, que todo es local.

**Files:**
- Modify: `internal/config/config.go` (`TrustedProxies []netip.Prefix` desde `SPLITSTREAM_TRUSTED_PROXIES`, lista separada por comas, vacía por defecto)
- Create: `internal/httpapi/clientip.go`, `clientip_test.go` (si `RemoteAddr` está en un prefijo de confianza, se toma el **último** `X-Forwarded-For` que no sea de confianza; si no, `RemoteAddr`), y `esLocal` usa la misma función
- Modify: `internal/httpapi/server.go`, `auth.go`, `setup.go`, `deploy/env.example`, `README.md` (§ proxy: ejemplo con Caddy y con nginx, y el aviso de que confiar en `0.0.0.0/0` anula el limitador)

**Pruebas:** sin proxies de confianza, `X-Forwarded-For` se ignora; con `127.0.0.1/32`, una petición desde loopback con `X-Forwarded-For: 203.0.113.9` cuenta como esa IP y **no** es local; cadena de dos proxies.

### Tarea C.4: Aviso de versión nueva

**Files:**
- Create: `internal/update/check.go`, `check_test.go` (con `httptest`): `GET https://api.github.com/repos/aprendomx/splitstream/releases/latest`, una vez al arrancar (tras 30 s) y cada 24 h; compara `tag_name` con `version` por `semver` simple (propio, tres enteros); nunca falla el arranque; se desactiva con `SPLITSTREAM_UPDATE_CHECK=false`; el `User-Agent` lleva la versión y **nada más** (no hay telemetría)
- Modify: `internal/httpapi/dto.go` (`statusDTO.Update {Available bool, Latest string, URL string}`), `web/src/App.vue` (banner discreto con enlace a la release), `README.md`

**Hecho cuando:** un binario `dev` no avisa (no compara); un `v0.9.0` con la `v0.10.0` publicada enseña el banner.

---

## 6. Entrega v0.11 — Capa de plataformas, primera mitad (Twitch)

**Objetivo:** la arquitectura de credenciales, tokens y capacidades, validada con la plataforma más barata. Si esta fase está bien, YouTube y Kick son trabajo repetido.

**Enmiendas al spec base:** §1 «sin chat unificado» → «chat de **lectura** agregado en el panel, por plataforma y solo donde la API lo permite; escribir y moderar quedan fuera»; §4 gana `internal/platforms/`; §5 anota lo que se decida en el spike D.0.

### Tarea D.0: Spike (medio día, sin fusionar código)

Preguntas con respuesta escrita en el ledger antes de nada:

1. ¿Twitch acepta Device Code Grant para una app pública con scopes `channel:manage:broadcast`, `user:read:chat`, `user:bot`? ¿Qué `client_id` se registra y a nombre de quién? (Con credenciales incluidas, el `client_id` es público en el binario; **no hay secret**.)
2. ¿EventSub por WebSocket entrega `channel.chat.message` con esos scopes sin que el usuario tenga que autorizar «bot» aparte?
3. Cuota: ¿cuántas suscripciones EventSub por `client_id` permite Twitch? (Es por app: N usuarios × 1 suscripción cada uno. Anotar el tope.)
4. Formato exacto de `PATCH /helix/channels` para título y `game_id`, y de `GET /helix/search/categories`.

### Tarea D.1: Almacén de cuentas y tokens

**Files:**
- Create: `internal/store/migrations/0007_platform_accounts.sql`:
  ```sql
  CREATE TABLE IF NOT EXISTS platform_accounts (
      id                      INTEGER PRIMARY KEY AUTOINCREMENT,
      platform                TEXT    NOT NULL,
      display_name            TEXT    NOT NULL,          -- login/canal, para el panel
      external_id             TEXT    NOT NULL,          -- id de usuario en la plataforma
      access_token_encrypted  BLOB    NOT NULL,
      refresh_token_encrypted BLOB,
      expires_at              TEXT,
      scopes                  TEXT    NOT NULL,          -- separados por espacio
      own_app                 INTEGER NOT NULL DEFAULT 0 CHECK (own_app IN (0,1)),
      client_id_encrypted     BLOB,                      -- solo con own_app = 1
      client_secret_encrypted BLOB,                      -- solo con own_app = 1
      created_at              TEXT    NOT NULL,
      updated_at              TEXT    NOT NULL,
      UNIQUE (platform, external_id)
  );
  ALTER TABLE destinations ADD COLUMN account_id INTEGER REFERENCES platform_accounts (id) ON DELETE SET NULL;
  ```
- Create: `internal/store/accounts.go`, `accounts_test.go` (`Account` sin tokens; `AccountTokens(ctx, c, id) (Tokens, error)` descifra; `SaveTokens`; `DeleteAccount`; ningún campo `Token` en `Account` para que serializarlo no pueda filtrar nada, igual que `Destination`)
- Create: `internal/platforms/tokens/manager.go`, `manager_test.go`: `Manager.Token(ctx, accountID) (crypto.Secret, error)` que refresca si caduca en < 5 min, con single-flight por cuenta y persistencia del refresco; `Refresher` es una interfaz que implementa cada plataforma
- Create: `internal/platforms/platform.go`:
  ```go
  package platforms
  type ID string // "twitch" | "youtube" | "kick" — reutiliza store.Platform pero sin importar relay
  type Capabilities struct {
      Title, Category, ChatRead, Schedule, IngestKey bool
      RequiresOwnApp   bool   // YouTube
      RequiresPublicURL bool  // Kick chat
  }
  type Provider interface {
      ID() ID
      Capabilities() Capabilities
      // Auth arranca el flujo que corresponda a la plataforma; devuelve lo que la interfaz
      // tiene que enseñar (URL + código de dispositivo, o URL de redirección).
      BeginAuth(ctx context.Context, opts AuthOptions) (AuthPrompt, error)
      CompleteAuth(ctx context.Context, state string) (store.NewAccount, error)
      Refresh(ctx context.Context, refreshToken crypto.Secret) (Tokens, error)
  }
  type TitleSetter interface{ SetTitle(ctx, acct Account, title string) error }
  type CategorySetter interface{ SearchCategories(ctx, acct, q string) ([]Category, error); SetCategory(ctx, acct, id string) error }
  type ChatReader interface{ ReadChat(ctx, acct Account, out chan<- ChatMessage) error } // bloquea hasta ctx.Done()
  type BroadcastScheduler interface{ CreateBroadcast(ctx, acct, Broadcast) (BroadcastRef, error); Start(ctx, acct, BroadcastRef) error; End(ctx, acct, BroadcastRef) error }
  type IngestKeyProvider interface{ IngestKey(ctx, acct, BroadcastRef) (url string, key crypto.Secret, err error) }
  ```
  y `internal/platforms/registry.go` (`Register(Provider)`, `Get(ID) (Provider, bool)`, `AllCapabilities() map[ID]Capabilities`).
- Modify: `.github/workflows/ci.yml`: `go list -deps ./internal/relay` tampoco contiene `internal/platforms`; `go list -deps ./internal/platforms/...` no contiene `go-rtmp` ni `internal/relay`.

### Tarea D.2: Twitch — auth por dispositivo, título, categoría

**Files:**
- Create: `internal/platforms/twitch/{auth.go,client.go,channel.go}` y tests con `httptest` (fixtures JSON reales del spike). `BeginAuth` → `POST https://id.twitch.tv/oauth2/device`; `CompleteAuth` sondea `POST /oauth2/token` con `grant_type=urn:ietf:params:oauth:grant-type:device_code` respetando `interval`; `Refresh`; `SetTitle`/`SetCategory` → `PATCH https://api.twitch.tv/helix/channels?broadcaster_id=`; `SearchCategories`.
- Create: `internal/httpapi/platforms.go`, `_test.go`: `GET /api/platforms` (capacidades por plataforma), `POST /api/platforms/{p}/auth` (devuelve `{verification_uri, user_code, expires_in}`), `GET /api/platforms/{p}/auth/{state}` (sondeo desde el panel: `pending|done|expired`), `GET /api/accounts`, `DELETE /api/accounts/{id}`, `PATCH /api/destinations/{id}` gana `account_id`, `POST /api/live/title` (`{"title":"…","destinations":[ids]}` → aplica a cada destino con cuenta y capacidad; devuelve resultado por destino, nunca «todo o nada»), `GET /api/platforms/twitch/categories?q=`
- Modify: `web/src/plataformas.js` (capacidades vienen de la API, no del catálogo estático), `DialogoDestino.vue` (paso «Conectar cuenta» con el código de dispositivo y un enlace a `twitch.tv/activate`; chips de capacidades: «Título», «Chat», «Programar» en gris cuando no aplica — roadmap §2, «el panel muestra capacidades por destino, no una lista uniforme»), Create: `web/src/components/TituloEnVivo.vue` (un campo, botón «Aplicar en todos los que puedan», resultado por destino)
- Modify: `docs/manual-de-usuario.md` (§ «Conectar tu cuenta de Twitch»)

**Hecho cuando:** título y categoría cambian en Twitch desde el panel, y el token se refresca solo tras caducar (probar acortando `expires_at` a mano en la base).

### Tarea D.3: Chat de Twitch por EventSub WebSocket, persistido

**Files:**
- Create: `internal/platforms/twitch/chat.go`, `chat_test.go` (contra un WebSocket falso con `coder/websocket`: `session_welcome`, suscripción por REST a `channel.chat.message`, `keepalive`, `reconnect` con la URL nueva **antes** de cerrar la vieja, como exige Twitch)
- Create: `internal/store/migrations/0008_chat.sql` (`chat_messages(id, session_id, account_id, platform, author, author_id, text, at)`, índice por `session_id, id`), `internal/store/chat.go`, `_test.go`, y la retención (A.5) gana `PruneChat`
- Create: `internal/chat/aggregator.go`, `_test.go`: por cada cuenta con `ChatRead` y un destino habilitado, arranca `ReadChat` al empezar la sesión y lo para al terminar; escribe en `chat_messages` por lotes (100 ms) y publica en el bus de eventos como `chat_message` **sin persistirlo en `events`** (canal aparte del bus, para no mezclar). Se engancha a `OnPublishStart`/`OnPublishEnd` a través del `events.Bus` (`publisher_connected`/`publisher_disconnected`), no del motor.
- Create: `internal/httpapi/chat.go`: `GET /api/chat/ws` (push de mensajes; mismo `wsWriteTimeout`), `GET /api/sessions/{id}/chat?after=<id>&limit=`
- Create: `web/src/components/Chat.vue` (columna con pestaña por plataforma y «Todos»; solo lectura; se abre con un botón como la vista previa), Modify: `Panel.vue`

**Hecho cuando:** puerta de la §1; el chat de una sesión sigue en «Historial» (v0.13) tras cerrarla.

### Definición de terminado de la v0.11

D.0 documentado; D.1–D.3 fusionadas; la CI vigila las dos fronteras nuevas; `docs/manual-de-usuario.md` explica qué puede hacer cada plataforma y por qué TikTok, X y «Otro» seguirán siendo «solo URL y clave» (roadmap §2, última línea).

---

## 7. Entrega v0.12 — Capa de plataformas, segunda mitad (YouTube y Kick)

**Objetivo:** que desaparezca el copiar y pegar de claves (roadmap §5) y que el chat de YouTube sea viable con presupuesto de cuota visible (roadmap §3).

### Tarea E.1: Asistente de credenciales propias de YouTube

Trabajo de documentación e interfaz, no de backend (roadmap §3). Es lo que decide cuánta gente termina la integración.

**Files:**
- Create: `docs/youtube-credenciales.md` con **capturas** de los cinco pasos (crear proyecto en Google Cloud, habilitar YouTube Data API v3, pantalla de consentimiento en «Externo» con el usuario como probador, credencial «Aplicación de escritorio» o «Web» con la `redirect_uri` exacta que el panel enseña, copiar `client_id` y `client_secret`), y el párrafo honesto de por qué se pide esto (la cuota es por proyecto de Google, no por usuario)
- Create: `web/src/components/AsistenteYouTube.vue` (los cinco pasos con la captura de cada uno, el campo para pegar `client_id` y `client_secret`, y la `redirect_uri` calculada desde `location.origin` con botón de copiar)
- Create: `internal/platforms/youtube/auth.go` (Authorization Code con PKCE **sobre la app del usuario**; `redirect_uri = <origen del panel>/api/platforms/youtube/callback`; `access_type=offline`, `prompt=consent` para garantizar `refresh_token`), `internal/httpapi/platforms.go` gana `GET /api/platforms/youtube/callback` (pública, valida `state` firmado con el `sessionSigner` y caduca a 10 min)
- Modify: `internal/store/accounts.go` (`own_app` con `client_id`/`client_secret` cifrados; el `Refresh` de YouTube los usa)

### Tarea E.2: YouTube — título, programar y traer la clave de ingesta

**Files:**
- Create: `internal/platforms/youtube/{client.go,broadcasts.go}` + tests con fixtures: `liveBroadcasts.insert` (título, descripción, `privacyStatus`, `scheduledStartTime`), `liveStreams.insert` (o reutilizar el «stream por defecto» del canal si existe: `liveStreams.list&mine=true`), `liveBroadcasts.bind`, `thumbnails.set` (opcional), `liveBroadcasts.transition` a `live` y a `complete`, `liveBroadcasts.update` para el título
- Create: `internal/platforms/quota/counter.go`, `_test.go`: contador por cuenta y día (unidades estimadas por llamada, tabla fija: `list`=1, `insert`=50, `update`=50, `bind`=50, `transition`=50, `liveChatMessages.list`=5); persistido en `settings`-like tabla `quota_usage(account_id, day, units)`; expuesto en `statusDTO.Accounts[i].QuotaUsedToday` y en `/metrics`
- Create: `internal/httpapi/broadcasts.go`: `POST /api/destinations/{id}/broadcast` (`{"title","description","privacy","scheduled_at","thumbnail":<multipart opcional>}` → crea, vincula, **y escribe `rtmp_url` + `key` en el destino** vía `store.UpdateDestination`; deja evento `broadcast_created` y `destination_key_from_api`), `POST /api/destinations/{id}/broadcast/start`, `/end`, `GET /api/destinations/{id}/broadcast`
- Modify: `DialogoDestino.vue` (con cuenta de YouTube: botón «Crear emisión y traer la clave» en lugar del campo de clave; el campo sigue disponible detrás de «pegar a mano»), `TarjetaDestino.vue` (botones «Salir al aire» / «Terminar» cuando hay `BroadcastRef`), `docs/manual-de-usuario.md`

**Regla:** «probar destino» (A.1) se salta cuando la clave vino por API (roadmap §5: «si la clave la trajo la API, no hay clave inválida que probar»); la tarjeta lo dice.

**Hecho cuando:** puerta de la §1, con la cuota mostrada en el panel y `broadcast_created` = 150 unidades contadas.

### Tarea E.3: Chat de YouTube con presupuesto visible

**Files:**
- Create: `internal/platforms/youtube/chat.go`, `_test.go`: `liveChatMessages.list` con `pageToken` y respetando `pollingIntervalMillis` (nunca por debajo); parada automática si la cuota estimada del día supera un umbral configurable (`quota_chat_budget`, por defecto 6.000 de las 10.000) con evento `chat_paused_quota`; reanudación manual
- Modify: `web/src/components/Chat.vue` (barra de presupuesto por cuenta de YouTube: «3.420 / 10.000 unidades hoy · el chat se pausará a 6.000»)

### Tarea E.4: Kick — título y categoría por OAuth 2.1

**Files:** `internal/platforms/kick/{auth.go,client.go}` (Authorization Code + PKCE; la `redirect_uri` la registra el usuario **o** se usa el flujo de credenciales incluidas si el spike E.5 concluye que Kick permite `http://localhost` y una `redirect_uri` comodín; si no, Kick pasa al modelo «app propia» como YouTube y el asistente se reutiliza), `PATCH /public/v1/channels`.

### Tarea E.5: Spike de Kick + chat por webhook entrante

1. ¿Qué `redirect_uri` permite Kick para apps públicas? ¿Hay Device Code?
2. Firma de los webhooks: cabeceras `Kick-Event-Signature`, `Kick-Event-Message-Id`, `Kick-Event-Message-Timestamp`, clave pública de Kick (`GET /public/v1/public-key`), algoritmo (RSA-PSS o PKCS1v15) y ventana anti-repetición.

**Files:** `internal/platforms/kick/webhook.go` (verificación de firma con clave pública cacheada 24 h; rechazo de mensajes con más de 5 min de antigüedad; idempotencia por `message_id` en memoria 10 min); `internal/httpapi/platforms.go` gana `POST /api/platforms/kick/webhook` (**pública**, sin cookie, solo firma); suscripción `chat.message.sent` creada al conectar la cuenta **solo si `statusDTO.Panel.TLS` es true**, y el panel explica que sin URL pública el chat de Kick no está disponible (roadmap §4).

### Definición de terminado de la v0.12

Puerta superada; `docs/youtube-credenciales.md` probado por alguien que no sea quien lo escribió (medir cuánto tarda); la matriz de capacidades del roadmap §2 reflejada en `GET /api/platforms` y en el manual; Facebook, X y TikTok documentados como «no» con la razón.

---

## 8. Entrega v0.13 — Alcance internacional

### Tarea F.1: Panel bilingüe sin librería

El spec §5 cierra el frontend a Vue, Quasar, Pinia y vuedraggable. Un `t()` propio de 40 líneas con dos diccionarios JSON cubre lo que hace falta; Quasar trae sus propios paquetes de idioma para sus componentes (`quasar/lang/en-US`).

**Files:** `web/src/i18n/{index.js,es.json,en.json}`; cada copy del panel pasa por `t('clave')` (es un cambio mecánico en 7 componentes; el test de la CI comprueba que `es.json` y `en.json` tienen las mismas claves); selector en `App.vue`; idioma por `navigator.language` con `localStorage` para la elección manual; **los mensajes de error de la API siguen viniendo del backend** (decisión de la fase 5) y para ellos la API gana `Accept-Language` con un mapa de códigos → texto en `internal/httpapi/i18n.go` (solo los mensajes de las clases 400/404/409; los de validación del store se traducen por `code` + campo, no por texto).

### Tarea F.2: README en inglés y comparativa honesta

`README.md` en inglés, `README.es.md` en español con enlace cruzado en la primera línea; `docs/comparativa.md` (Splitstream frente a Restream, Castr, nginx-rtmp y MediaMTX: qué hace cada uno, qué cuesta, dónde va tu vídeo; **sin adjetivos**, con la matriz de capacidades del roadmap §2 tal cual); `docs/lanzamiento.md` gana la versión en inglés del borrador.

### Tarea F.3: Vista de historial y post-mortem

**Files:** `web/src/pages/Historial.vue` (lista de sesiones de `GET /api/sessions` con duración, resolución, bitrate, contadores de eventos, grabación disponible) y `web/src/pages/Sesion.vue` (línea de tiempo de eventos, chat de la sesión, grabaciones descargables, y un «resumen» generado en el cliente: destinos con reconexiones, minutos degradados, mensajes de chat por plataforma); `GET /api/sessions/{id}` gana `events`, `recordings` y `chat_count`. Sin gráficas de bitrate en el tiempo: las métricas no se persisten (spec §6.6) y persistirlas es otra decisión.

---

## 9. Entrega v1.0 — Endurecimiento

### Tarea G.1: Contingencia de `go-rtmp`

Las tres deudas conocidas de la v0.0.7 (spec §16.2 y ledgers): el `Write` con 5 s cableados, la carrera en `streams.Delete`/`streams.At`, y el stream de control inaccesible (por lo que `releaseStream`/`FCPublish` no se pueden mandar bien).

**Decisión:** fork en `github.com/aprendomx/go-rtmp` con `replace` en `go.mod`, tres commits atómicos (uno por arreglo) y un PR abierto aguas arriba por cada uno. El fork se mantiene solo mientras aguas arriba no los acepte. Tests de `rtmpio` que fallen sin el fork (la carrera, con `-race`; el timeout, con un `net.Conn` que no drena).

### Tarea G.2: CI estricta

`.golangci.yml` (`errcheck`, `govet`, `staticcheck`, `gosec` con las excepciones justificadas en el archivo, `revive` con la regla de comentarios en español desactivada), job `lint`; job `vuln` con `govulncheck ./...`; workflow `nightly.yml` con `schedule: '0 4 * * *'` que corre la integración completa y, si hay secretos `TWITCH_TEST_KEY`/`YOUTUBE_TEST_KEY`, 5 minutos contra plataformas reales; `dependabot.yml` para Go, npm y Actions.

### Tarea G.3: Congelar el contrato de API y la política de migraciones

`docs/api.md` generado por un test (`TestAPIContractDocIsCurrent`) a partir de las rutas registradas en `routes()` y de los DTO por reflexión, que falla si el documento no coincide; `docs/migraciones.md` (nunca editar una migración aplicada; solo hacia delante; `IF NOT EXISTS`; claves ajenas apagadas por el runner; cómo probar una migración sobre una base real de la versión anterior —el script `deploy/migrate-test.sh` que descarga el `.db` de ejemplo de cada release—); versionado de la API: el prefijo `/api/` queda como `v1` implícito y cualquier ruptura exige `/api/v2/`.

---

## 10. Fuera, salvo demanda (roadmap §7)

No se planifican: Facebook (verificación de negocio), X y TikTok (sin vía), escritura y moderación de chat, subida en vivo al almacenamiento remoto, multi-tenant. **Sí se deja preparado** el gancho para la subida **al terminar** la sesión a un S3-compatible (roadmap §6): el job `upload_recordings` del scheduler de `internal/maintenance`, vacío, con su spec pendiente; entra cuando alguien lo pida.

---

## 11. Riesgos de este plan

| Riesgo | Señal | Mitigación |
| --- | --- | --- |
| Twitch no acepta Device Code para los scopes del chat | D.0 lo dice en medio día | Plan B: PKCE con `redirect_uri` = `http://localhost:<puerto del panel>/…` **solo** para instalaciones locales, y «app propia» para VPS |
| Google rechaza la verificación (no aplica: la app es del usuario) | — | Es la razón del modelo híbrido; el riesgo real es el abandono en el asistente, que se mide (E.1) |
| Kick cambia la API pública (es reciente) | E.5 | Kick es un paquete borrable sin tocar nada más (D3) |
| La grabación provoca descartes en los destinos | La puerta de la v0.9 lo mide | La regla está en el spec antes del código; el sink de grabación usa la misma cola; el test con disco lento es obligatorio |
| `ALTER TABLE ADD COLUMN` rompe el test que rebobina `user_version` | El test de 0002 | Decidir en el plan de la v0.9 entre `pragma_table_info` en la migración o reconstruir el esquema en el test |
| El panel crece y el binario pierde su argumento de 4 MB | `ls -la` del release | Presupuesto: el panel compilado no pasa de 1 MB; chat e historial se cargan por ruta (`import()` dinámico, como ya hace `router.js`) |

---

## Autorrevisión

**Cobertura del roadmap:** §1 (por capas, spec revertido con fecha) → §3 y §4 de este plan; §2 (capacidades por destino, orden Twitch→Kick→YouTube→nunca Facebook; nota: el roadmap pone Kick antes que YouTube en el orden recomendado pero YouTube antes en la secuencia §7 —este plan sigue la secuencia §7, que es la que el roadmap llama «revisada»—, y Kick título/categoría va con YouTube en la v0.12) → §6, §7; §3 (credenciales híbridas, cuota) → D.1, E.1, E.3, quota; §4 (chat leer, no escribir; Kick por webhook solo con TLS; persistencia con retención) → D.3, E.3, E.5, A.5; §5 (títulos, programar, clave por API, «probar» se salta) → D.2, E.2; §6 (sink, regla innegociable, FLV→fMP4, segmentación, cuota, retención, por sesión, S3 después) → §4 y §10; §7 (secuencia) → §1 renumerada; §8 (tokens con `crypto`, `internal/platforms` por capacidad) → D2, D3, D.1.

**Marcadores:** ningún «TBD»; las dos piezas que este plan deja deliberadamente para su propio spec (fMP4 en B.4 y el chat de Kick en E.5) lo dicen y tienen tarea de spike.

**Consistencia de nombres:** `events.Bus` (A.2) es lo que consumen `alerts.WebhookDispatcher` (A.3) y `chat.Aggregator` (D.3); `rtmpio.ProbeResult` (A.1) es lo que devuelve `sinks.(*Factory).Test` y lo que consume `httpapi.DestinationTester`; `relay.RecorderSinkID` (B.1) es lo que filtra `httpapi.metricsFor` (B.3); `statusDTO.Panel.TLS` (C.2) es lo que condiciona la suscripción de Kick (E.5); `store.ListSessions` (A.5) es lo que pinta `Historial.vue` (F.3).
