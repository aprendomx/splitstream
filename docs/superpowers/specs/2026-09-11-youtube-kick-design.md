# Splitstream — v0.12 «Capa de plataformas, segunda mitad: YouTube y Kick»

**Fecha:** 2026-09-11
**Estado:** aprobado por el plan maestro (decisiones D2, D3 confirmadas; D4 extendida por los spikes de esta entrega); pendiente de plan de implementación
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
**Spec previo:** `docs/superpowers/specs/2026-09-11-twitch-design.md` (v0.11: cuentas, tokens, capacidades, chat)
**Roadmap:** `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` §2–§5
**Plan maestro:** `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §7
**Versión de partida:** `v0.11.0` (`main` @ `4d94238`)
**Spikes:** YouTube (OAuth, Live Streaming API, chat, cuota) y Kick (OAuth 2.1, API pública, webhooks), resueltos contra la documentación oficial el 2026-09-11; resumen en §10.

## 1. Qué se construye

Que desaparezca el copiar y pegar de claves (roadmap §5) y que el chat de YouTube sea viable
con presupuesto de cuota visible (roadmap §3), sobre la capa de plataformas de la v0.11:

1. **Credenciales propias del usuario** para YouTube y para Kick: `client_id` y
   `client_secret` cifrados en la cuenta (`own_app = 1`, columnas que la 0007 ya trae), un
   asistente en el panel con los pasos de cada consola y la explicación honesta de por qué
   (la cuota de YouTube es por proyecto de Google; Kick no admite clientes públicos).
2. **YouTube**: conectar por **flujo de dispositivo** (funciona igual en un PC y en un VPS),
   crear la emisión desde el panel (título, privacidad, hora), crear y vincular el
   *stream*, y **escribir en el destino la URL y la clave que devuelve la API**; la
   emisión sale al aire y termina sola cuando la señal llega y se va
   (`enableAutoStart`/`enableAutoStop`), con botones manuales por si acaso; título en vivo;
   chat por sondeo con **presupuesto de cuota** por cuenta y día.
3. **Kick**: conectar por **redirect con PKCE** al propio panel (Kick no tiene flujo de
   dispositivo), título y categoría, **clave y URL de ingesta por API** (`streamkey:read`),
   y chat por **webhook entrante firmado**, disponible solo cuando el panel tiene URL
   pública HTTPS (`panel.tls`), como anticipaba el roadmap §4.
4. **Capacidades completas en la API y el panel**: `GET /api/platforms` y
   `destination.capabilities` exponen `schedule`, `ingest_key`, `requires_own_app` y
   `requires_public_url`; la tarjeta enseña «Clave por API», «Salir al aire» y «Terminar»
   cuando aplica; «Probar destino» se salta cuando la clave vino por API.

**Las reglas de esta entrega:**

- **El motor no se entera.** Nada de esto entra en `internal/relay` ni `internal/rtmpio`.
  Salir al aire y terminar en YouTube son llamadas a la API que fallan solas; el relay
  sigue igual.
- **Un solo mecanismo de secretos**: `client_secret`, tokens y claves de stream van por
  `crypto.Cipher`; jamás en logs, eventos, errores, DTOs ni respuestas. El `client_id`
  de una app propia **tampoco** sale por la API una vez guardado (solo «configurado: sí»).
- **Cuota visible, nunca silenciosa** (roadmap §3): cada llamada a YouTube suma a un
  contador por cuenta y día que el panel enseña; el chat se pausa solo al llegar al
  presupuesto y lo dice.
- **Todo lo que necesita URL pública lo dice** en vez de fallar: el chat de Kick.

## 2. Enmiendas al spec base y al de la v0.11

- **v0.11 §4 `Provider`**: `BeginAuth(ctx, creds Credentials)` y
  `Refresh(ctx, acct store.Account, creds Credentials, refresh crypto.Secret)`; `PollAuth`
  recibe también `creds`. `Credentials{ClientID, ClientSecret crypto.Secret}` vacías para
  Twitch. Capacidad nueva `ChatWebhook` (§7.3) y `RedirectAuth` (§4.2).
- **v0.11 §3.2 `store.NewAccount`** gana `OwnApp bool`, `ClientID`, `ClientSecret
  crypto.Secret`; `AccountCredentials(ctx, c, id) (Credentials, error)` descifra.
- **Spec base §7**: migración `0009_broadcasts_quota.sql`: `destination_broadcasts`,
  `quota_usage`. `SchemaVersion` 9.
- **Spec base §9**: endpoints de §8. Dos rutas **públicas** nuevas (sin cookie):
  `GET /api/platforms/{p}/callback` (redirect OAuth; se protege con el `state` firmado) y
  `POST /api/platforms/kick/webhook` (se protege con la firma RSA de Kick).
- **Spec base §12**: `SPLITSTREAM_YOUTUBE_CHAT_BUDGET` (unidades diarias tras las que el
  chat de YouTube se pausa; 6000), `SPLITSTREAM_YOUTUBE_QUOTA` (cuota diaria del proyecto;
  10000, solo informativa). Sin variables para Kick: todo va por cuenta.
- **Catálogo de eventos**: `broadcast_created`, `broadcast_started`, `broadcast_ended`,
  `destination_key_from_api` (info, por destino), `chat_paused_quota` (warn),
  `webhook_rejected` (warn, sin cuerpo).
- **Spec base §1 / README «Alcance»**: chat de lectura «hoy Twitch, YouTube y Kick (este
  último con URL pública)»; Facebook, X y TikTok documentados como «no» con la razón.

## 3. Credenciales propias

### 3.1 Modelo

`platform_accounts.own_app`, `client_id_encrypted`, `client_secret_encrypted` (0007) pasan
a escribirse: `UpsertAccount` con `NewAccount.OwnApp = true` cifra ambas; `SaveCredentials`
no existe (para cambiarlas se reconecta). `AccountCredentials(ctx, c, id)` las descifra
para el manager y para los proveedores; ninguna otra función las lee. `accountDTO` gana
`own_app bool` y **no** `client_id`.

### 3.2 Flujo de conexión

`POST /api/platforms/{p}/auth` acepta cuerpo `{client_id, client_secret, origin}`:
obligatorio cuando `Capabilities.RequiresOwnApp` (YouTube, Kick), ignorado para Twitch. Las
credenciales viven en el `authFlow` en memoria hasta que el flujo termina y se guardan
con la cuenta. `origin` es `location.origin` del panel y solo lo usa Kick (§4.2).

### 3.3 Asistente en el panel

`AsistenteCredenciales.vue`, dentro del bloque «Cuenta» del diálogo de destino, con los
pasos por plataforma y un campo para pegar `client_id` y `client_secret`:

- **YouTube** (`docs/youtube-credenciales.md`, con capturas que el usuario aporta en la
  puerta): crear un proyecto en Google Cloud → habilitar «YouTube Data API v3» → pantalla
  de consentimiento «Externo», añadirse como usuario de prueba → credencial de tipo
  **«TVs and Limited Input devices»** → copiar `client_id` y `client_secret`. Y el aviso
  honesto: mientras el proyecto esté en «Testing», Google caduca la autorización a los 7
  días y hay que reconectar; publicar la app («In production») lo evita a cambio de una
  pantalla de «app no verificada» que solo ve el propio usuario.
- **Kick** (`docs/kick-credenciales.md`): Ajustes → Developer (2FA obligatoria) → nueva
  app → **Redirect URL exactamente** la que el panel enseña
  (`<origin>/api/platforms/kick/callback`, con botón de copiar) → si se quiere chat,
  «Enable Webhooks» con `<URL pública>/api/platforms/kick/webhook` → copiar `client_id`
  y `client_secret`.

## 4. Autorización

### 4.1 YouTube — flujo de dispositivo

`POST https://oauth2.googleapis.com/device/code` (`client_id`, `scope=https://www.googleapis.com/auth/youtube`)
→ `{device_code, user_code, verification_url, expires_in, interval}`; sondeo
`POST https://oauth2.googleapis.com/token` con `client_id`, **`client_secret`**,
`device_code`, `grant_type=urn:ietf:params:oauth:grant-type:device_code`; errores
`authorization_pending`, `slow_down` (doblar), `access_denied` (→ `error`),
`expired_token`/`invalid_grant` (→ `expired`). Solo el scope `youtube`: `youtube.force-ssl`
no está permitido en este flujo y `youtube` basta para todo lo de §5. La identidad sale
de `GET /youtube/v3/channels?part=snippet&mine=true` (1 unidad): `items[0].id` es el
`external_id`, `snippet.title` el nombre. `Refresh` con `client_id` + `client_secret` +
`refresh_token` (Google no rota el refresh token; si la respuesta trae uno, se guarda).
`Validate`: `GET https://oauth2.googleapis.com/tokeninfo?access_token=` (401/400 →
`ErrUnauthorized`). Reutiliza el sondeo en servidor de la v0.11 tal cual.

### 4.2 Kick — redirect con PKCE (`RedirectAuth`)

Interfaz nueva `RedirectAuth{ BeginRedirect(ctx, creds, redirectURI string) (AuthPrompt, error);
CompleteRedirect(ctx, creds, prompt AuthPrompt, code string) (store.NewAccount, error) }`.
`AuthPrompt` gana `RedirectURL` (la de `id.kick.com/oauth/authorize` con `code_challenge`
S256 y `state`) y `CodeVerifier` (secreto del flujo, no sale de la API). El `state` es
`<id del flujo>.<hmac>` firmado con el `sessionSigner` y caduca a 10 min.

- `POST /api/platforms/kick/auth` → `{state, redirect_url, expires_in}`; el panel abre
  `redirect_url` en una pestaña nueva y sondea `GET …/auth/{state}` como con Twitch.
- `GET /api/platforms/{p}/callback?code&state` (**pública**): verifica la firma y la
  caducidad del `state`, busca el flujo, intercambia el código
  (`POST https://id.kick.com/oauth/token` con `client_id`, `client_secret`, `code`,
  `code_verifier`, `redirect_uri`), guarda la cuenta (`GET /public/v1/users` da
  `user_id` y `name`) y responde una página mínima «Cuenta conectada; puedes cerrar esta
  pestaña». Un `state` inválido → 400 sin detalle. Un `error=` de Kick → el flujo pasa a
  `error` con el `error_description`.
- `redirect_uri = <origin>/api/platforms/kick/callback`, con `origin` del cuerpo del
  `POST`; el servidor lo acepta si es `PublicURL` (con TLS integrado) o un origen `http`
  de loopback, o cualquiera si no hay TLS integrado (proxy delante). Kick exige
  coincidencia exacta con la registrada: el asistente la enseña con botón de copiar.
- Scopes: `user:read channel:read channel:write streamkey:read events:subscribe`.
- `Refresh` con `client_secret` (rota el refresh token con ventana deslizante de 30 días:
  se guarda el nuevo). `Validate`: `GET /public/v1/users` (401 → `ErrUnauthorized`).
- Twitch no implementa `RedirectAuth`; YouTube tampoco.

## 5. YouTube — emisiones, clave por API y título

`internal/platforms/youtube` implementa `TitleSetter`, `BroadcastScheduler`,
`IngestKeyProvider` y `ChatReader`. Base `https://www.googleapis.com/youtube/v3`,
inyectable; `Authorization: Bearer`.

- **Crear emisión** (`BroadcastScheduler.CreateBroadcast`): `liveBroadcasts.insert`
  (`part=snippet,status,contentDetails`; `snippet.title`, `snippet.scheduledStartTime`
  (ahora si no se da), `status.privacyStatus` (`public|unlisted|private`, por defecto
  `unlisted`), `status.selfDeclaredMadeForKids=false`,
  `contentDetails.enableAutoStart=true`, `enableAutoStop=true`,
  `latencyPreference=normal`) → `liveStreams.insert` (`snippet.title`,
  `cdn.ingestionType=rtmp`, `cdn.resolution=variable`, `cdn.frameRate=variable`) →
  `liveBroadcasts.bind`. Devuelve `Broadcast{Ref: broadcastID, StreamRef, LiveChatID,
  IngestURL: cdn.ingestionInfo.ingestionAddress + "/" + streamName…}`: la URL RTMP del
  destino es `ingestionAddress` y la clave `streamName`.
- **Salir al aire / terminar**: `Start` → `liveBroadcasts.transition?broadcastStatus=live`;
  si responde `errorStreamInactive`, se reintenta cada 5 s hasta 60 s (la señal tarda en
  llegar a YouTube); `End` → `transition?broadcastStatus=complete`. Con `enableAutoStart`,
  YouTube lo hace sola cuando llega la señal; los botones son el respaldo.
- **Título**: `liveBroadcasts.update` (`part=snippet`, con `id` y `snippet.title`) sobre la
  emisión vinculada al destino; sin emisión, `SetTitle` devuelve `ErrNoBroadcast` y el
  resultado por destino lo dice («crea la emisión primero»). Categoría: no aplica
  (`Capabilities.Category=false`).
- **Cuota**: cada llamada suma en el contador (§6) con la tabla fija `list=1`,
  `insert=50`, `update=50`, `bind=50`, `transition=50`, `liveChatMessages.list=5`. Son
  las cifras de la Data API; la tabla pública no desglosa los métodos `live*`: se
  documenta como estimación conservadora.
- `Capabilities{Title, Schedule, IngestKey, ChatRead, RequiresOwnApp}`.

## 6. Cuota (`internal/platforms/quota`)

`Counter{Add(ctx, accountID, units); UsedToday(ctx, accountID) (int, error)}` sobre la
tabla `quota_usage(account_id, day TEXT, units INTEGER, PRIMARY KEY (account_id, day))`,
día en **hora del Pacífico** (la cuota de Google se reinicia a medianoche PT). El
proveedor de YouTube recibe un `QuotaSink` (`func(units int)`) por cuenta desde el
manager de cuota; `accountDTO.quota_used_today` y `splitstream_youtube_quota_units{account}`
lo exponen. Retención: filas de más de 7 días se podan en el job diario `cuota`.

## 7. Chat

### 7.1 YouTube — sondeo con presupuesto

`ReadChat` (bloqueante, como Twitch): al arrancar busca la emisión activa
(`liveBroadcasts.list?mine=true&broadcastStatus=active&part=snippet`, 1 unidad; sin
emisión activa reintenta cada 30 s hasta 10 min y luego se rinde con
`chat_disconnected` «no hay emisión activa en YouTube»), toma `snippet.liveChatId` y
sondea `liveChatMessages.list?part=snippet,authorDetails&liveChatId=&pageToken=`
respetando **siempre** `pollingIntervalMillis` (nunca por debajo; mínimo 2 s). Cada
sondeo suma 5. Si `UsedToday + 5 > SPLITSTREAM_YOUTUBE_CHAT_BUDGET`, se para con el
evento `chat_paused_quota` («el chat de YouTube se pausó al llegar a 6000 unidades; se
reanuda mañana o si subes el presupuesto») y no vuelve a intentarlo hasta la siguiente
sesión. `ChatMessage`: `authorDetails.displayName`, `channelId`, `snippet.displayMessage`,
`isChatModerator` → badge `moderator`, `isChatOwner` → `owner`.

### 7.2 Kick — webhook firmado (`ChatWebhook`)

Interfaz nueva `ChatWebhook{ SubscribeChat(ctx, acct, token) error; UnsubscribeChat(ctx,
acct, token) error; ParseWebhook(r *http.Request, body []byte) ([]platforms.ChatMessage,
error) }`. Kick implementa `ChatRead=true` **y** `RequiresPublicURL=true`, y no
`ChatReader`: el agregador no arranca goroutine para ella.

- `POST /api/platforms/kick/webhook` (**pública**, sin cookie): lee el cuerpo (tope
  256 KiB), verifica `Kick-Event-Signature` (RSA-SHA256 PKCS1v15 sobre
  `messageId.timestamp.body`, base64) con la clave pública de `GET /public/v1/public-key`
  cacheada 24 h (y refrescada una vez ante un fallo de verificación), rechaza marcas de
  tiempo con más de 5 min de desfase y `message_id` ya vistos (memoria, 10 min); solo
  `Kick-Event-Type: chat.message.sent` v1 se procesa, el resto responde 200 y se ignora.
  Responde 200 **antes** de tocar la base: los mensajes entran por
  `Aggregator.Ingest(msgs)` (§7.3). Un rechazo registra `webhook_rejected` con el motivo,
  nunca el cuerpo; se acota a uno por minuto.
- Suscripción: al conectar la cuenta, **si `panel.tls` y hay `PublicURL`**,
  `POST /public/v1/events/subscriptions` con `chat.message.sent` v1 y `method: webhook`.
  Sin URL pública no se suscribe y el panel lo explica en el bloque «Cuenta»:
  «El chat de Kick necesita que el panel sea público por HTTPS (Ponerlo en internet)».
  Al desconectar, `DELETE` de la suscripción (best-effort). Kick da de baja sola las
  suscripciones que fallan más de un día: el job diario `webhooks_kick` relista y vuelve
  a suscribir las cuentas con `ChatRead` y URL pública.
- Kick identifica los emotes como `[emote:id:nombre]` en el texto: se dejan tal cual
  (v0.11 §10: emotes como texto).

### 7.3 Agregador

`Aggregator.Ingest(msgs []platforms.ChatMessage) int` es público: con sesión viva mete los
mensajes por el mismo camino que `acumular` (bus, contador, lote) y devuelve cuántos
aceptó; sin sesión los descarta (cuenta `dropped`). Solo lo usa el webhook de Kick.
`cuentasConChat` sigue arrancando `ReadChat` para las que lo implementan.

## 8. Modelo de datos, API y panel

### 8.1 Migración `0009_broadcasts_quota.sql`

```sql
CREATE TABLE IF NOT EXISTS destination_broadcasts (
    destination_id INTEGER PRIMARY KEY REFERENCES destinations (id) ON DELETE CASCADE,
    account_id     INTEGER NOT NULL REFERENCES platform_accounts (id) ON DELETE CASCADE,
    platform       TEXT    NOT NULL,
    broadcast_ref  TEXT    NOT NULL DEFAULT '',   -- id de la emisión (YouTube); '' en Kick
    stream_ref     TEXT    NOT NULL DEFAULT '',   -- id del liveStream (YouTube)
    live_chat_id   TEXT    NOT NULL DEFAULT '',
    key_from_api   INTEGER NOT NULL DEFAULT 1 CHECK (key_from_api IN (0,1)),
    status         TEXT    NOT NULL DEFAULT 'created' CHECK (status IN ('created','live','complete')),
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
);
CREATE TABLE IF NOT EXISTS quota_usage (
    account_id INTEGER NOT NULL REFERENCES platform_accounts (id) ON DELETE CASCADE,
    day        TEXT    NOT NULL,
    units      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, day)
);
```

`Destination.KeyFromAPI bool` (LEFT JOIN, como `AccountID`).

### 8.2 Endpoints

| Método y ruta | Qué hace |
| --- | --- |
| `POST /api/platforms/{p}/auth` | Cuerpo `{client_id?, client_secret?, origin?}`; para Kick devuelve `{state, redirect_url, expires_in}` |
| `GET /api/platforms/{p}/callback` | **Pública**. Redirect OAuth (§4.2) |
| `POST /api/platforms/kick/webhook` | **Pública**. Webhook firmado (§7.2) |
| `POST /api/destinations/from-account` | `{account_id, name, title?, privacy?, scheduled_at?}` → crea la emisión (YouTube) o lee la clave (Kick), crea el destino con URL y clave reales, lo vincula y registra `destination_broadcasts`; eventos `broadcast_created` y `destination_key_from_api` |
| `POST /api/destinations/{id}/broadcast` | Igual, sobre un destino existente con cuenta: emisión nueva (YouTube) o clave releída (Kick); sustituye URL y clave |
| `GET /api/destinations/{id}/broadcast` | `{platform, broadcast_ref, status, live_chat_id, key_from_api, watch_url}` o 404 |
| `POST /api/destinations/{id}/broadcast/start` y `/end` | `transition` a `live`/`complete` (YouTube); 409 en Kick |
| `POST /api/destinations/{id}/test` | Con `key_from_api`, responde 200 `{skipped: true, message}` sin probar |
| `GET /api/accounts` | Gana `own_app`, `quota_used_today` (YouTube) |
| `GET /api/platforms` | `capabilities` completas (7 campos) y `requires_own_app`/`requires_public_url`; `public_url_ok` por plataforma (Kick: `panel.tls`) |

`capabilitiesDTO` pasa a los siete campos; `TestDTOFieldNamesAreSnakeCase` los vigila.

### 8.3 Panel

- `AsistenteCredenciales.vue` (§3.3) y `ConectarCuenta.vue` con la rama de redirect:
  «Abrir Kick para autorizar» en pestaña nueva + sondeo.
- `DialogoDestino.vue`: con una cuenta de YouTube o Kick, el campo de clave se sustituye
  por «Crear emisión y traer la clave» (YouTube: título, privacidad, hora) / «Traer la
  clave de Kick»; «pegar a mano» sigue detrás de un enlace. Chips nuevos «Clave por API»,
  «Programar».
- `TarjetaDestino.vue`: «clave por API» en el pie; con emisión de YouTube, botones «Salir
  al aire» / «Terminar» según `status`; «Probar» dice «no hace falta: la clave vino por
  API».
- `Chat.vue`: barra de presupuesto por cuenta de YouTube («3 420 / 6 000 unidades hoy»).
- `Ajustes.vue` «Cuentas conectadas»: «app propia» y cuota usada.
- `TituloEnVivo.vue`: sin cambios de estructura; YouTube entra por `capabilities.title`.

## 9. Pruebas

- **Store**: 0009 idempotente; `TestOpenCreatesSchema` con 13 tablas; `UpsertAccount` con
  `OwnApp` cifra credenciales y `AccountCredentials` las descifra; `Account` y `accountDTO`
  sin `ClientSecret` (reflexión); `destination_broadcasts` CRUD y `Destination.KeyFromAPI`;
  `quota_usage` suma y poda.
- **platforms/tokens**: `Refresh` recibe credenciales cuando `OwnApp`; Twitch sigue sin
  ellas.
- **youtube**: device flow con fixtures (pending → slow_down → done; access_denied;
  expired), `channels.list mine`, refresh, tokeninfo; crear emisión (insert → stream →
  bind) con la URL y la clave; `transition` con `errorStreamInactive` y reintento; `update`
  del título; chat: `list` respetando `pollingIntervalMillis`, `nextPageToken`, pausa por
  presupuesto con evento; contador de cuota por llamada; ningún test sale a internet.
- **kick**: `BeginRedirect` (PKCE S256, `state` firmado), `CompleteRedirect` con
  `client_secret` y `code_verifier`, refresh, `GET /users`, `PATCH /channels`, categorías,
  `GET /channels` con `stream.url`/`stream.key`; webhook: firma válida/inválida con un par
  RSA generado en el test, marca fuera de ventana, `message_id` repetido, tipo distinto,
  cuerpo grande; suscripción y baja.
- **httpapi**: callback público con `state` firmado (válido, caducado, alterado, flujo
  desconocido, `error=`); webhook público (200 con firma válida, 401 sin firma, no toca la
  base salvo `Ingest`); `from-account` y `{id}/broadcast` (YouTube y Kick, con
  `fakeProvider` que implementa `BroadcastScheduler`/`IngestKeyProvider`), `start`/`end`,
  `test` saltado con `key_from_api`, `capabilitiesDTO` completo, `own_app`/`quota` en
  cuentas, ningún `client_secret` ni `code_verifier` en respuestas.
- **chat**: `Ingest` con y sin sesión.
- **CI**: guards de fronteras extendidos a `internal/platforms/youtube`, `kick`, `quota`.

## 10. Spikes (resumen)

- **YouTube**: el flujo de dispositivo admite solo `youtube` y `youtube.readonly`
  (`force-ssl` no); exige `client_secret` en el sondeo; `verification_url`
  `https://www.google.com/device`; el loopback+PKCE no está documentado con el navegador
  en otra máquina (VPS). Proyecto en «Testing»: 100 usuarios y **tokens que caducan a los
  7 días**. `transition` a `live` exige `streamStatus=active` (`errorStreamInactive`).
  Cuota diaria 10 000 unidades por proyecto; los costes de los métodos `live*` no están
  desglosados en la tabla pública. Existe `liveChatMessages.streamList` (conexión en
  *streaming*), que queda fuera por transporte no documentado para HTTP plano.
- **Kick**: sin flujo de dispositivo; `client_secret` obligatorio en el intercambio y en
  el refresh; una `redirect URL` por app con coincidencia exacta (`localhost` recomendado
  para local); scopes documentados (sin `chat:read`: el chat es por `events:subscribe`);
  `GET /public/v1/channels` devuelve `stream.url` y `stream.key` con `streamkey:read`;
  `PATCH /public/v1/channels` (`stream_title`, `category_id`) → 204; categorías v1 con
  `q` (deprecada; se aísla para cambiar a v2) ; webhooks con URL configurada en el
  portal, firma RSA-SHA256 PKCS1v15 sobre `id.timestamp.body`, clave pública por API;
  baja automática tras un día de fallos; sin rate limits numéricos.

## 11. Fuera de esta entrega

- `liveChatMessages.streamList`, miniaturas (`thumbnails.set`), descripción y etiquetas
  de la emisión, varias emisiones por destino, reutilizar un *stream* existente de YouTube.
- Revocación de tokens al desconectar (queda anotada desde la v0.11; Google y Kick tienen
  endpoint, se hará con el resto de la limpieza en la v0.13).
- Escribir en el chat, moderar, emotes como imágenes.
- Facebook (verificación de negocio), X y TikTok (sin API viable): documentados como «no».
- Un túnel para el chat de Kick en un PC sin URL pública: se documenta cómo hacerlo con
  el TLS integrado en un VPS, no se automatiza.
