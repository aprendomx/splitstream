# Splitstream — v0.11 «Capa de plataformas, primera mitad: Twitch»

**Fecha:** 2026-09-11
**Estado:** aprobado por el plan maestro (decisiones D2, D3 y D4 confirmadas); pendiente de plan de implementación
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
**Roadmap:** `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` §2–§5 y §8
**Plan maestro:** `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §6
**Versión de partida:** `v0.10.0` (`main` @ `e39d3c3`)
**Spike D.0:** resuelto contra la documentación oficial de Twitch el 2026-09-10; las respuestas están en §9 y los JSON de ejemplo son los fixtures de los tests.

## 1. Qué se construye

La arquitectura de cuentas, tokens y capacidades por plataforma, validada con Twitch:
conectar la cuenta con un código de dispositivo, cambiar título y categoría desde el panel
y leer el chat en vivo, persistido con la sesión. Si esto queda bien, YouTube y Kick (v0.12)
son trabajo repetido.

Entra en esta entrega (D.1–D.3 del plan maestro):

1. **Almacén de cuentas** (`platform_accounts`, migración 0007) con tokens cifrados por el
   mismo `crypto.Cipher` de siempre; un enlace destino ↔ cuenta; un `tokens.Manager` que
   refresca y valida solo.
2. **`internal/platforms`**: una interfaz por **capacidad** (`TitleSetter`,
   `CategorySetter`, `ChatReader`; `BroadcastScheduler` e `IngestKeyProvider` declaradas
   para la v0.12), un registro, y el proveedor `twitch` con Device Code Grant.
3. **API y panel**: capacidades por plataforma, «Conectar cuenta» dentro del diálogo de
   destino, campo «Título en vivo» que aplica a todos los que puedan, chat de solo lectura
   en una columna.
4. **Chat de Twitch** por EventSub WebSocket, persistido en `chat_messages` (migración
   0008) y empujado al panel por un WebSocket propio.

**Las reglas de esta entrega:**

- **El motor no se entera.** `internal/relay` y `internal/rtmpio` no importan
  `internal/platforms` ni `internal/chat`; la CI lo vigila. Cualquier fallo de Twitch (API
  caída, token revocado, chat cortado) es un evento y un aviso, nunca una interrupción del
  relay.
- **Un solo mecanismo de secretos** (roadmap §8): los tokens van cifrados con el `Cipher`
  de la clave maestra, en memoria como `crypto.Secret`, y jamás aparecen en logs, eventos,
  errores ni respuestas de la API, ni enmascarados.
- **Credenciales incluidas, cliente público** (D4): el `client_id` de la app de Twitch de
  Splitstream va en el binario; no hay `client_secret`. Quien quiera su propia app pone
  `SPLITSTREAM_TWITCH_CLIENT_ID`.
- **Solo lectura del chat**; escribir y moderar quedan fuera (roadmap §4).

## 2. Enmiendas al spec base

- **§1**: «sin chat unificado» → «chat de **lectura** agregado en el panel, por plataforma y
  solo donde la API lo permite; escribir y moderar quedan fuera». El README «Alcance»
  cambia igual.
- **§4**: gana `internal/platforms/` (proveedores por capacidad), `internal/platforms/twitch`,
  `internal/platforms/tokens` e `internal/chat` (agregador y bus del chat).
- **§5**: sin módulo nuevo. `coder/websocket` sirve de cliente (`websocket.Dial`) para
  EventSub; el Device Code Grant se hace con `net/http`: no entra `golang.org/x/oauth2`.
- **§7**: tablas `platform_accounts`, `destination_accounts` (0007) y `chat_messages` (0008).
  `SchemaVersion` 8.
- **§9**: endpoints de §6.
- **§12**: `SPLITSTREAM_TWITCH_CLIENT_ID` (vacío: el incluido), `SPLITSTREAM_RETENTION_MAX_CHAT`
  (por defecto 200000 filas).
- **Catálogo de eventos**: `account_connected`, `account_disconnected`, `account_reauth_required`
  (warn), `channel_updated` (info, por destino), `chat_connected`, `chat_disconnected`
  (warn si no fue por fin de sesión). Los mensajes de chat **no** son eventos.

## 3. Cuentas y tokens

### 3.1 Modelo de datos (migración `0007_platform_accounts.sql`)

```sql
CREATE TABLE IF NOT EXISTS platform_accounts (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    platform                TEXT    NOT NULL CHECK (platform IN ('twitch','youtube','kick')),
    external_id             TEXT    NOT NULL,          -- id de usuario en la plataforma
    display_name            TEXT    NOT NULL,          -- login/canal, para el panel
    access_token_encrypted  BLOB    NOT NULL,
    refresh_token_encrypted BLOB,
    expires_at              TEXT,                      -- RFC 3339 UTC, ancho fijo
    scopes                  TEXT    NOT NULL,          -- separados por espacio
    status                  TEXT    NOT NULL DEFAULT 'ok' CHECK (status IN ('ok','reauth')),
    own_app                 INTEGER NOT NULL DEFAULT 0 CHECK (own_app IN (0,1)),
    client_id_encrypted     BLOB,                      -- solo con own_app = 1 (v0.12, YouTube)
    client_secret_encrypted BLOB,
    created_at              TEXT    NOT NULL,
    updated_at              TEXT    NOT NULL,
    UNIQUE (platform, external_id)
);
CREATE TABLE IF NOT EXISTS destination_accounts (
    destination_id INTEGER PRIMARY KEY REFERENCES destinations (id) ON DELETE CASCADE,
    account_id     INTEGER NOT NULL REFERENCES platform_accounts (id) ON DELETE CASCADE
);
```

Sin `ALTER TABLE` (los tests rebobinan `user_version` y reaplican; `ADD COLUMN` no es
idempotente): el enlace destino ↔ cuenta es una tabla aparte. Borrar la cuenta desvincula;
borrar el destino también. `status = 'reauth'` es «el refresco falló o el token fue
revocado»: el panel enseña «Reconectar» y nada más usa esa cuenta hasta entonces.

### 3.2 Store

`internal/store/accounts.go`: `Account{ID, Platform, ExternalID, DisplayName, Scopes []string,
Status, ExpiresAt *time.Time, OwnApp bool, CreatedAt, UpdatedAt}` — **sin ningún campo de
token**, como `Destination` no lleva la clave: serializarla no puede filtrar nada.
`NewAccount{Platform, ExternalID, DisplayName, Scopes, Tokens}`; `Tokens{Access, Refresh
crypto.Secret; ExpiresAt time.Time}`. Funciones: `UpsertAccount(ctx, c, NewAccount) (Account,
error)` (misma `platform+external_id` → actualiza tokens y nombre, vuelve a `ok`),
`Accounts(ctx)`, `AccountByID`, `AccountTokens(ctx, c, id) (Tokens, error)` (descifra; sin
auditar: lo usa el manager a cada rato), `SaveTokens(ctx, c, id, Tokens)`, `SetAccountStatus`,
`DeleteAccount`, `LinkDestination(ctx, destID, accountID)`, `UnlinkDestination`,
`AccountForDestination(ctx, destID) (*Account, error)`, `DestinationsOfAccount`.
`Destination` gana `AccountID *int64` (JOIN con `destination_accounts`) para que el DTO lo
enseñe sin una consulta más.

### 3.3 `tokens.Manager` (`internal/platforms/tokens`)

- `Token(ctx, accountID) (crypto.Secret, error)`: si caduca en menos de 5 minutos,
  refresca **con single-flight por cuenta** (dos llamadas concurrentes no gastan dos
  refresh tokens: Twitch los rota y el usado deja de valer) y persiste el par nuevo **antes**
  de devolverlo. Si el refresco falla con 400/401, marca `reauth`, emite
  `account_reauth_required` y devuelve `ErrReauth`.
- `Run(ctx)`: cada hora valida el token de cada cuenta (`Validator.Validate`), como exige
  Twitch; 401 → intenta refrescar; si tampoco, `reauth`. Corre en `fondo` y vuelve con el
  contexto. Un fallo de red se loguea a debug y se reintenta a la hora.
- `Refresher`/`Validator` son interfaces que implementa cada proveedor; el manager no sabe
  de Twitch.
- El manager es lo único que lee `AccountTokens`; los proveedores reciben el token ya
  resuelto en cada llamada.

## 4. `internal/platforms`

```go
type ID string // "twitch" | "youtube" | "kick"

type Capabilities struct {
    Title, Category, ChatRead, Schedule, IngestKey bool
    RequiresOwnApp    bool // YouTube (v0.12)
    RequiresPublicURL bool // chat de Kick (v0.12)
}

type Provider interface {
    ID() ID
    Capabilities() Capabilities
    // BeginAuth arranca el flujo; devuelve lo que la interfaz enseña.
    BeginAuth(ctx context.Context) (AuthPrompt, error)
    // PollAuth pregunta una vez; ErrAuthPending mientras la persona no termina,
    // ErrAuthExpired si el código venció.
    PollAuth(ctx context.Context, prompt AuthPrompt) (store.NewAccount, error)
    Refresh(ctx context.Context, refresh crypto.Secret) (store.Tokens, error)
    Validate(ctx context.Context, access crypto.Secret) (Identity, error)
}
type AuthPrompt struct{ State, VerificationURI, UserCode, DeviceCode string; ExpiresAt time.Time; Interval time.Duration }
type Identity struct{ ExternalID, DisplayName string; Scopes []string; ExpiresIn time.Duration }

type TitleSetter interface{ SetTitle(ctx context.Context, acct store.Account, token crypto.Secret, title string) error }
type CategorySetter interface {
    SearchCategories(ctx context.Context, token crypto.Secret, q string) ([]Category, error)
    SetCategory(ctx context.Context, acct store.Account, token crypto.Secret, id string) error
}
type ChatReader interface{ ReadChat(ctx context.Context, acct store.Account, token TokenSource, out chan<- ChatMessage) error } // bloquea hasta ctx.Done()
type BroadcastScheduler interface{ /* v0.12 */ }
type IngestKeyProvider interface{ /* v0.12 */ }
```

`ChatMessage{Platform ID; AccountID int64; AuthorID, Author, Text, Color string; Badges []string;
MessageID string; At time.Time}`. `TokenSource` es `func(ctx) (crypto.Secret, error)`: el
lector de chat lo llama al conectar y al reconectar, así un token renovado a mitad de
sesión se usa sin reiniciar nada. `registry.go`: `Register(Provider)`, `Get(ID) (Provider,
bool)`, `AllCapabilities() map[ID]Capabilities`. Las capacidades se declaran y la interfaz
las enseña por destino (roadmap §2): un destino `custom`, TikTok o X no tiene ninguna, y
eso se ve, no se insinúa lo contrario.

**Fronteras (CI):** `internal/relay` no importa `internal/platforms` ni `internal/chat`;
`internal/platforms/...` y `internal/chat` no importan go-rtmp, `internal/relay` ni
`internal/rtmpio`; `internal/httpapi` sigue sin importar go-rtmp, `internal/rtmpio` ni
`internal/webtls`.

## 5. Twitch (`internal/platforms/twitch`)

- **Client ID**: `var ClientID = ""` en el paquete, rellenado con el de la app de Splitstream
  (lo registra el usuario en dev.twitch.tv: nombre, «OAuth Redirect URLs» `http://localhost`
  aunque el flujo de dispositivo no lo use, categoría «Application Integration», tipo
  **Public**, cuenta con 2FA); `SPLITSTREAM_TWITCH_CLIENT_ID` lo sustituye. Vacío: el
  proveedor devuelve `ErrNoClientID` y el panel lo explica en vez de fallar.
- **Scopes**: `channel:manage:broadcast user:read:chat`. Con un token de usuario del propio
  broadcaster, `channel.chat.message` no necesita `user:bot` ni `channel:bot` (§9.4).
- **Device Code Grant**: `BeginAuth` → `POST https://id.twitch.tv/oauth2/device`
  (`client_id`, `scopes`); `PollAuth` → `POST /oauth2/token` con
  `grant_type=urn:ietf:params:oauth:grant-type:device_code`, respetando `interval`;
  `400 {"message":"authorization_pending"}` → `ErrAuthPending`; cualquier otro 400 no
  reconocido tras vencer `expires_in` → `ErrAuthExpired`; `slow_down` no está documentado
  por Twitch pero, si llega, se duplica el intervalo. Al obtener el token, `Validate` da
  `user_id`, `login` y `scopes` sin llamar a Helix: con eso se construye `NewAccount`.
- **Refresh**: `POST /oauth2/token` con `grant_type=refresh_token` sin `client_secret`
  (cliente público). El refresh token **rota**: se guarda el nuevo par en una sola escritura.
  Un refresh token de cliente público caduca a los 30 días sin uso: el `Run` horario del
  manager lo mantiene vivo mientras el servicio corra.
- **Validate**: `GET https://id.twitch.tv/oauth2/validate` con `Authorization: OAuth <token>`.
- **Helix**: `PATCH https://api.twitch.tv/helix/channels?broadcaster_id=` con `{title}` o
  `{game_id}` (204; el título se recorta a 140 caracteres antes de enviar, `"0"` quita la
  categoría), `GET /helix/search/categories?query=` (con `box_art_url` ya con
  `{width}x{height}` sustituidos por `144x192`). Cabeceras `Authorization: Bearer` y
  `Client-Id`. Un 429 se devuelve como `ErrRateLimited` con `Ratelimit-Reset`.
- **Hosts configurables** en el cliente (`AuthBase`, `HelixBase`, `EventSubURL`) para que
  todos los tests vayan contra `httptest`; ningún test habla con Twitch.

## 6. API y panel

### 6.1 Endpoints

| Método y ruta | Qué hace |
| --- | --- |
| `GET /api/platforms` | `[{id, name, capabilities{…}, configured bool}]` para las tres; `configured` false sin `client_id` |
| `POST /api/platforms/{p}/auth` | Arranca el flujo; `{state, verification_uri, user_code, expires_in}`. El servidor sondea en segundo plano |
| `GET /api/platforms/{p}/auth/{state}` | `{status: pending\|done\|expired\|error, account?, message?}`; el panel lo consulta cada 3 s |
| `GET /api/accounts` | Lista sin tokens (`Account` + `destinations: [ids]`) |
| `DELETE /api/accounts/{id}` | Borra tokens y enlaces; evento `account_disconnected` |
| `PATCH /api/destinations/{id}` | Gana `account_id` (`null` desvincula); la plataforma del destino y la de la cuenta deben coincidir (400 si no) |
| `POST /api/live/title` | `{title?, category_id?, destinations:[ids]}` → `[{destination_id, ok, message}]`; aplica a cada destino con cuenta y capacidad, uno a uno, **nunca todo o nada**; evento `channel_updated` por acierto |
| `GET /api/platforms/twitch/categories?q=` | Búsqueda para el selector (usa la primera cuenta de Twitch en `ok`) |
| `GET /api/chat/ws` | Push de `ChatMessage` de la sesión viva como JSON; al conectar manda los últimos 50 |
| `GET /api/sessions/{id}/chat?after=<id>&limit=` | Historial paginado por id (para la v0.13) |

Los flujos de autorización viven en memoria (`map[state]*authFlow`) con TTL de `expires_in`;
un sondeo del panel a un `state` desconocido es 404. `state` es aleatorio (16 bytes) y no
se deriva del `device_code`. `statusDTO.destinations[i]` gana `account {id, display_name,
platform, status}` (nil sin cuenta) y `capabilities {title, category, chat}`.

### 6.2 Panel

- `web/src/plataformas.js`: las capacidades salen de `GET /api/platforms`, no del catálogo
  estático; el catálogo sigue dando URL de ingesta, icono y avisos.
- `DialogoDestino.vue`: para una plataforma con proveedor, un bloque «Cuenta» con «Conectar
  cuenta de Twitch» → muestra el código grande, enlace a `twitch.tv/activate` y sondea hasta
  `done`; o elige una cuenta ya conectada. Chips «Título», «Categoría», «Chat» en gris cuando
  no aplica.
- `TituloEnVivo.vue` en `Panel.vue`, sobre la lista de destinos: un campo de título, un
  selector de categoría (Twitch), botón «Aplicar en todos los que puedan», resultado por
  destino.
- `Chat.vue`: columna lateral que se abre con un botón como la vista previa; pestaña por
  plataforma y «Todos»; solo lectura; se conecta a `/api/chat/ws` y se desconecta al cerrar.
- Página Ajustes: bloque «Cuentas conectadas» con «Desconectar».

## 7. Chat (`internal/chat`)

- **Ciclo de vida**: el agregador se suscribe al `events.Bus`; en `publisher_connected`
  (con `SessionID`) arranca, por cada cuenta en `ok` con `ChatRead` y al menos un destino
  habilitado vinculado, una goroutine `ReadChat`; en `publisher_disconnected` las cancela. Un
  destino que se vincula o habilita en caliente **no** arranca el chat hasta la siguiente
  sesión (regla simple; se documenta). Sin sesión no se lee chat: el chat pertenece a la
  sesión, como la grabación (`session_id NOT NULL`).
- **Persistencia** (`0008_chat.sql`): `chat_messages(id, session_id NOT NULL REFERENCES
  sessions ON DELETE CASCADE, account_id, platform, author_id, author, text, color,
  badges, message_id, at)` con índice `(session_id, id)`. Escritura **por lotes**: cada
  100 ms o 50 mensajes, una sola transacción (`SetMaxOpenConns(1)`: una fila por mensaje
  competiría con los sinks). Si la base no responde, los mensajes se descartan con un
  contador y un aviso, jamás se bloquea al lector.
- **Bus del chat** (`chat.Bus`, aparte del de eventos: `store.Event` no tiene autor ni texto):
  fan-out no bloqueante a los WebSockets del panel, con los últimos 50 mensajes en memoria
  para el snapshot inicial.
- **EventSub WebSocket** (`twitch/chat.go`): `websocket.Dial` a `wss://eventsub.wss.twitch.tv/ws`;
  con `session_welcome` crea la suscripción por Helix (`channel.chat.message` v1, condición
  `broadcaster_user_id = user_id = external_id`, transporte `websocket` con `session_id`)
  dentro de los 10 s; `session_keepalive` reinicia el temporizador
  (`keepalive_timeout_seconds` + 5 s: si no llega nada, se reconecta); `session_reconnect`
  → conectar a `reconnect_url` **antes** de cerrar la vieja; `revocation` → parar y evento
  `chat_disconnected`; códigos 4000–4007 se loguean con su significado. Reconexión con
  backoff (1 s → 30 s) hasta `ctx.Done()`. Nunca se envía nada por el socket (4001).
- **Retención**: las filas caen con su sesión (`ON DELETE CASCADE` en `PruneSessions`) y un
  tope `SPLITSTREAM_RETENTION_MAX_CHAT` (200000) como el de eventos; job `chat` en el
  planificador.
- **Métricas**: `splitstream_chat_messages_total{platform}`, `splitstream_chat_connected{platform}`,
  `splitstream_chat_dropped_total`.

## 8. Pruebas

- **Store**: migraciones 0007/0008 idempotentes; `TestOpenCreatesSchema` con las tres tablas;
  `UpsertAccount` actualiza tokens y vuelve a `ok`; `AccountTokens` descifra y `Account` no
  serializa nada del token (test por reflexión: ningún campo con `Token`); cascadas de
  borrado; `Destination.AccountID`.
- **tokens**: refresco solo cuando caduca en < 5 min; single-flight (dos llamadas
  concurrentes, un solo `Refresh`); rotación persistida antes de devolver; `reauth` tras
  400/401; `Run` valida cada hora con reloj falso y no bloquea el apagado.
- **twitch**: DCF completo contra `httptest` con los JSON de §9 (pending → done; expired;
  `slow_down`); refresh sin `client_secret`; validate; `PATCH channels` con título > 140
  recortado y `game_id "0"`; búsqueda de categorías con `box_art_url` sustituido; 429; y
  **ningún test sale a internet** (hosts inyectados; un guard en el paquete falla si las
  URL por defecto se usan bajo `testing`).
- **twitch/chat**: contra un servidor WebSocket falso con `coder/websocket`: welcome →
  suscripción por REST falsa → notificación → `ChatMessage` correcto; keepalive vencido →
  reconexión; `session_reconnect` con la nueva antes de cerrar la vieja; `revocation`;
  cierre 4003.
- **chat**: agregador arranca/para con los eventos de sesión; lotes de 100 ms/50; descarte
  sin bloqueo; bus con snapshot de 50.
- **httpapi**: cada endpoint de §6.1 con `fakeProvider`; el flujo de auth en memoria (TTL,
  404 de `state` desconocido, `done` una sola vez); `PATCH` con plataformas que no
  coinciden → 400; `POST /api/live/title` con un destino sin cuenta, otro sin capacidad y
  otro que falla → resultado por destino; `/api/chat/ws` recibe el snapshot y un mensaje
  nuevo; tokens ausentes de **todas** las respuestas (grep en el cuerpo por el token
  del fixture).
- **CI**: los dos guards nuevos de fronteras; `go test ./... -race` con `GOMAXPROCS=2`.
- **Integración con mediamtx**: no cambia; el chat no toca el camino de producción del motor.

## 9. Spike D.0 (respuestas, 2026-09-10)

1. **DCF**: `POST https://id.twitch.tv/oauth2/device` (`client_id`, `scopes`) →
   `{device_code, user_code, verification_uri, expires_in: 1800, interval: 5}`; sondeo
   `POST /oauth2/token` (`client_id`, `scopes`, `device_code`, `grant_type=urn:ietf:params:oauth:grant-type:device_code`)
   → `{access_token, refresh_token, expires_in, scope[], token_type: "bearer"}`; errores
   documentados, todos HTTP 400 `{status, message}`: `authorization_pending`,
   `invalid device code`, `Invalid refresh token`. `slow_down`/`expired_token` **no
   documentados**. La app puede ser pública (sin secret). Registro: nombre único, OAuth
   Redirect URL (obligatoria en el formulario aunque DCF no la use), categoría; cuenta con
   2FA. Fuente: dev.twitch.tv/docs/authentication/getting-tokens-oauth/.
2. **Refresh** sin `client_secret` para clientes públicos; el refresh token **rota** y el
   usado se invalida; caduca a los 30 días sin uso; access token ≈ 4 h.
3. **Validate** cada hora, `Authorization: OAuth`, respuesta `{client_id, login, scopes,
   user_id, expires_in}`; incumplir lo audita Twitch.
4. **Scopes**: `channel:manage:broadcast` (título/categoría), `user:read:chat` (chat);
   `user:bot`/`channel:bot` solo con app access token o canal ajeno.
5. **EventSub WS**: `wss://eventsub.wss.twitch.tv/ws?keepalive_timeout_seconds=`; welcome,
   keepalive, notification, reconnect (30 s de gracia, 4004), revocation; 10 s para
   suscribirse (4003); nada de tráfico saliente (4001). Suscripción por
   `POST /helix/eventsub/subscriptions` con `transport.session_id`; `channel.chat.message` v1
   con `condition {broadcaster_user_id, user_id}`; coste 0 con el scope del usuario.
6. **Límites**: 3 conexiones WS con suscripciones, 300 por conexión, coste total 10.
7. **Helix**: `PATCH channels` (título ≤ 140, `game_id "0"`, 204), `GET channels`,
   `GET search/categories` (`box_art_url` con `{width}x{height}`); rate limit por
   `Ratelimit-Limit/Remaining/Reset`, 429 al exceder.
8. Los tokens de DCF son tokens de usuario normales; «trátalos como una contraseña».

## 10. Fuera de esta entrega

- Escribir en el chat, moderar, emotes como imágenes (los `fragments` se guardan como
  texto plano), cheers y respuestas enlazadas (se muestran como texto).
- Programar emisiones, traer la clave de ingesta, YouTube, Kick (v0.12).
- Historial de chat en el panel (v0.13, sobre `GET /api/sessions/{id}/chat`).
- Varias cuentas de la misma plataforma para el chat: se leen todas las vinculadas a un
  destino habilitado; la pestaña las mezcla por plataforma.
- Persistir el `state` del flujo de dispositivo: un reinicio a mitad obliga a empezar de
  nuevo (el código dura 30 min).
