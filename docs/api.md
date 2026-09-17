# Contrato de la API

Este documento lo genera `TestAPIContractDocIsCurrent` a partir de la tabla de rutas de
`internal/httpapi/server.go` y de los tipos que viajan por la API, leídos por reflexión.
**No se edita a mano**: se regenera con

```
go test ./internal/httpapi/ -run APIContract -update
```

y el test falla mientras el archivo no coincida con el código, así que un cambio en la API
que no se refleje aquí no puede pasar de CI.

## Versionado

`/api/` es la **v1** de esta API.

Son cambios **compatibles**, y entran sin más: añadir rutas, añadir campos —siempre con un
valor por defecto— y añadir valores a un enumerado que el panel tolere.

Son cambios **incompatibles**: quitar o renombrar rutas o campos, cambiar el tipo de un
campo y cambiar el significado de un `code` de error. Cualquiera de ellos exige publicar
`/api/v2/` conviviendo con `/api/` durante al menos una versión menor, y dejar la decisión
escrita en el spec.

Todos los errores tienen la misma forma (`errorBody`): el cliente decide mirando el
`code`, que es un conjunto cerrado, y enseña el `message`, que es para personas.

## Rutas

La columna **Sesión** dice qué hace falta para entrar: `no` es público, `sí` pide la cookie
del panel y `sí (o token)` acepta además el token del recolector de métricas.

### auth — Sesión del panel

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| POST | `/api/auth/login` | no | Entrega la cookie de sesión a cambio de la contraseña del panel |
| POST | `/api/auth/logout` | no | Cierra la sesión y caduca la cookie |

### setup — Configuración inicial

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/setup` | no | Dice si falta poner contraseña y si hará falta el código de la consola |
| POST | `/api/setup` | no | Fija la contraseña inicial del panel |

### health — Salud

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/healthz` | no | Comprueba que el servidor y su base de datos responden |

### metrics — Métricas

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/metrics` | sí (o token) | Métricas del relay y de cada destino en formato Prometheus |

### ingest — Ingesta

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/ingest` | sí | Datos de la ingesta RTMP, con la clave enmascarada |
| POST | `/api/ingest/rotate-key` | sí | Genera una clave de ingesta nueva y, si se pide, corta la publicación en curso |

### destinations — Destinos

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/destinations` | sí | Lista los destinos con su estado y sus métricas |
| POST | `/api/destinations` | sí | Da de alta un destino con su URL RTMP y su clave |
| POST | `/api/destinations/from-account` | sí | Crea un destino a partir de una cuenta ya vinculada |
| POST | `/api/destinations/reorder` | sí | Reordena los destinos según la lista de ids que reciba |
| POST | `/api/destinations/toggle-all` | sí | Enciende o apaga todos los destinos de una vez |
| DELETE | `/api/destinations/{id}` | sí | Borra un destino |
| PATCH | `/api/destinations/{id}` | sí | Cambia los campos indicados de un destino |
| GET | `/api/destinations/{id}/broadcast` | sí | Consulta la emisión que la plataforma dio para el destino |
| POST | `/api/destinations/{id}/broadcast` | sí | Crea en la plataforma la emisión del destino |
| POST | `/api/destinations/{id}/broadcast/end` | sí | Termina la emisión del destino |
| POST | `/api/destinations/{id}/broadcast/start` | sí | Pone en directo la emisión del destino |
| GET | `/api/destinations/{id}/key` | sí | Devuelve la clave del destino en claro, para copiarla desde el panel |
| DELETE | `/api/destinations/{id}/logo` | sí | Borra el logotipo del destino |
| GET | `/api/destinations/{id}/logo` | sí | Devuelve el logotipo del destino |
| PUT | `/api/destinations/{id}/logo` | sí | Sube el logotipo del destino |
| POST | `/api/destinations/{id}/retry` | sí | Fuerza un reintento de conexión del destino sin esperar la espera creciente |
| POST | `/api/destinations/{id}/test` | sí | Prueba la conexión con el destino sin emitir nada |
| POST | `/api/destinations/{id}/toggle` | sí | Enciende o apaga un destino |

### live — Estado y emisión en curso

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/events` | sí | Lista los eventos del registro, de los más nuevos a los más viejos |
| POST | `/api/live/title` | sí | Cambia el título y la categoría en las plataformas de los destinos indicados |
| GET | `/api/status` | sí | Estado completo del panel: ingesta, sesión, destinos, grabación y eventos recientes |

### platforms — Plataformas y autorización

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/platforms` | sí | Lista las plataformas soportadas y lo que cada una permite hacer |
| POST | `/api/platforms/kick/webhook` | no | Recibe los mensajes de chat que Kick manda por webhook |
| GET | `/api/platforms/twitch/categories` | sí | Busca categorías de Twitch por texto |
| POST | `/api/platforms/{p}/auth` | sí | Arranca la autorización de una cuenta en la plataforma |
| GET | `/api/platforms/{p}/auth/{state}` | sí | Dice cómo va una autorización en curso |
| GET | `/api/platforms/{p}/callback` | no | Recoge la vuelta del navegador desde la plataforma y termina la autorización |

### accounts — Cuentas vinculadas

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/accounts` | sí | Lista las cuentas vinculadas y su estado |
| DELETE | `/api/accounts/{id}` | sí | Desvincula una cuenta y olvida sus tokens |

### sessions — Historial de sesiones

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/sessions` | sí | Historial de sesiones con el recuento de eventos de cada una |
| GET | `/api/sessions/{id}` | sí | Ficha de una sesión con eventos, grabaciones y chat |

### recordings — Grabaciones

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/recording/settings` | sí | Ajustes de grabación y espacio en disco |
| PATCH | `/api/recording/settings` | sí | Cambia los ajustes de grabación |
| GET | `/api/recordings` | sí | Lista las grabaciones guardadas |
| DELETE | `/api/recordings/{id}` | sí | Borra una grabación y su archivo |
| GET | `/api/recordings/{id}/download` | sí | Descarga el archivo de una grabación |

### chat — Chat

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/chat/ws` | sí | Canal WebSocket con el chat unificado de la sesión en vivo |
| GET | `/api/sessions/{id}/chat` | sí | Historial paginado del chat de una sesión |

### webhooks — Webhooks de notificación

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/webhooks` | sí | Lista los webhooks de notificación |
| POST | `/api/webhooks` | sí | Da de alta un webhook de notificación |
| DELETE | `/api/webhooks/{id}` | sí | Borra un webhook |
| PATCH | `/api/webhooks/{id}` | sí | Cambia los campos indicados de un webhook |
| POST | `/api/webhooks/{id}/test` | sí | Manda un evento de prueba al webhook |

### backup — Copia de seguridad

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| POST | `/api/backup` | sí | Descarga una copia de seguridad consistente de la base de datos |

### ws — Canales WebSocket

| Método | Ruta | Sesión | Qué hace |
| --- | --- | --- | --- |
| GET | `/api/preview/ws` | sí | Canal WebSocket con la vista previa silenciada de la ingesta |
| GET | `/ws` | sí | Canal WebSocket con el estado del panel en tiempo real |

## Formas

Los cuerpos de petición y de respuesta, leídos de los tipos Go.
**Opcional** marca los campos que pueden faltar o venir a `null`.

### accountDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `id` | int64 | no |
| `platform` | string | no |
| `display_name` | string | no |
| `status` | string | no |
| `scopes` | []string | no |
| `expires_at` | time (RFC 3339) | sí |
| `destinations` | []int64 | no |
| `own_app` | bool | no |
| `quota_used_today` | int | sí |
| `created_at` | time (RFC 3339) | no |

### accountRefDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `id` | int64 | no |
| `display_name` | string | no |
| `platform` | string | no |
| `status` | string | no |

### authStartDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `state` | string | no |
| `verification_uri` | string | no |
| `user_code` | string | no |
| `redirect_url` | string | no |
| `expires_in` | int | no |

### authStartRequest

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `client_id` | string | no |
| `client_secret` | string | no |
| `origin` | string | no |

### authStatusDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `status` | string | no |
| `account` | accountDTO | sí |
| `message` | string | no |

### broadcastDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `platform` | string | no |
| `broadcast_ref` | string | no |
| `status` | string | no |
| `live_chat_id` | string | no |
| `key_from_api` | bool | no |
| `watch_url` | string | no |

### broadcastRequest

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `title` | string | no |
| `privacy` | string | no |
| `scheduled_at` | time (RFC 3339) | sí |

### capabilitiesDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `title` | bool | no |
| `category` | bool | no |
| `chat` | bool | no |
| `schedule` | bool | no |
| `ingest_key` | bool | no |
| `requires_own_app` | bool | no |
| `requires_public_url` | bool | no |

### chatMessageDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `id` | int64 | no |
| `session_id` | int64 | no |
| `platform` | string | no |
| `account_id` | int64 | sí |
| `author_id` | string | no |
| `author` | string | no |
| `text` | string | no |
| `color` | string | no |
| `badges` | []string | no |
| `message_id` | string | no |
| `at` | time (RFC 3339) | no |

### destinationCreate

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `name` | string | no |
| `platform` | string | no |
| `rtmp_url` | string | no |
| `key` | string | no |
| `enabled` | bool | no |

### destinationDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `id` | int64 | no |
| `name` | string | no |
| `platform` | string | no |
| `rtmp_url` | string | no |
| `key_mask` | string | no |
| `enabled` | bool | no |
| `sort_order` | int | no |
| `logo_etag` | string | no |
| `created_at` | time (RFC 3339) | no |
| `updated_at` | time (RFC 3339) | no |
| `metrics` | metricsDTO | sí |
| `key_from_api` | bool | no |
| `broadcast` | broadcastDTO | sí |
| `account` | accountRefDTO | sí |
| `capabilities` | capabilitiesDTO | no |

### destinationPatch

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `name` | string | sí |
| `platform` | string | sí |
| `rtmp_url` | string | sí |
| `key` | string | sí |
| `enabled` | bool | sí |
| `account_id` | json (crudo) | sí |

### errorBody

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `error` | errorDetail | no |

### errorDetail

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `code` | string | no |
| `message` | string | no |

### eventDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `id` | int64 | no |
| `session_id` | int64 | sí |
| `destination_id` | int64 | sí |
| `level` | string | no |
| `kind` | string | no |
| `message` | string | no |
| `created_at` | time (RFC 3339) | no |

### fromAccountRequest

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `account_id` | int64 | no |
| `name` | string | no |
| `title` | string | no |
| `privacy` | string | no |
| `scheduled_at` | time (RFC 3339) | sí |

### healthDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `status` | string | no |
| `db` | string | no |

### ingestDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `url` | string | no |
| `app` | string | no |
| `key_mask` | string | no |

### liveResultDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `destination_id` | int64 | no |
| `ok` | bool | no |
| `message` | string | no |

### liveTitleRequest

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `title` | string | no |
| `category_id` | string | sí |
| `destinations` | []int64 | no |

### loginRequest

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `password` | string | no |

### metricsDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `state` | string | no |
| `degraded` | bool | no |
| `bytes_sent` | uint64 | no |
| `bitrate_bps` | uint64 | no |
| `dropped_frames` | uint64 | no |
| `uptime_seconds` | int64 | no |
| `reconnections` | uint64 | no |
| `last_error` | string | no |
| `queued_bytes` | int | no |
| `queued_messages` | int | no |

### panelDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `tls` | bool | no |
| `public_url` | string | no |
| `youtube_chat_budget` | int | no |
| `youtube_quota` | int | no |

### platformDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `id` | string | no |
| `name` | string | no |
| `capabilities` | capabilitiesDTO | no |
| `configured` | bool | no |
| `public_url_ok` | bool | no |

### probeDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `outcome` | string | no |
| `stage` | string | no |
| `elapsed_ms` | int64 | no |
| `message` | string | no |

### recordingDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `id` | int64 | no |
| `session_id` | int64 | sí |
| `segment` | int | no |
| `path` | string | no |
| `started_at` | time (RFC 3339) | no |
| `ended_at` | time (RFC 3339) | sí |
| `bytes` | int64 | no |
| `duration_ms` | int | no |
| `in_progress` | bool | no |

### recordingSettingsDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `enabled` | bool | no |
| `segment_min` | int | no |
| `max_gb` | float64 | no |
| `keep_days` | int | no |
| `dir` | string | no |
| `used_bytes` | int64 | no |
| `free_bytes` | int64 | no |

### recordingSettingsPatch

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `enabled` | bool | sí |
| `segment_min` | int | sí |
| `max_gb` | float64 | sí |
| `keep_days` | int | sí |

### recordingStatusDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `enabled` | bool | no |
| `active` | bool | no |
| `state` | string | no |
| `degraded` | bool | no |
| `bytes` | uint64 | no |
| `dropped_frames` | uint64 | no |
| `segments` | int | no |
| `used_bytes` | int64 | no |
| `max_bytes` | int64 | no |
| `free_bytes` | int64 | no |
| `dir` | string | no |

### reorderRequest

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `ids` | []int64 | no |

### rotateKeyRequest

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `disconnect_now` | bool | no |

### rotateKeyResponse

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `key` | string | no |
| `key_mask` | string | no |

### sessionDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `live` | bool | no |
| `id` | int64 | no |
| `started_at` | time (RFC 3339) | sí |
| `width` | int | sí |
| `height` | int | sí |
| `bitrate_bps` | int | sí |
| `source` | string | no |

### sessionDetailDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `id` | int64 | no |
| `started_at` | time (RFC 3339) | no |
| `ended_at` | time (RFC 3339) | sí |
| `width` | int | sí |
| `height` | int | sí |
| `bitrate_bps` | int | sí |
| `duration_s` | int | no |
| `events` | []eventDTO | no |
| `recordings` | []recordingDTO | no |
| `chat_count` | int | no |
| `chat_by_platform` | map[string]int | no |
| `events_truncated` | bool | no |
| `recordings_truncated` | bool | no |

### sessionSummaryDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `id` | int64 | no |
| `started_at` | time (RFC 3339) | no |
| `ended_at` | time (RFC 3339) | sí |
| `width` | int | sí |
| `height` | int | sí |
| `bitrate_bps` | int | sí |
| `has_recording` | bool | no |
| `events` | objeto {info, warn, error} | no |

### setupEstadoDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `necesario` | bool | no |
| `pide_codigo` | bool | no |
| `local` | bool | no |

### setupRequest

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `password` | string | no |
| `codigo` | string | no |

### statusDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `version` | string | no |
| `ingest` | ingestDTO | no |
| `session` | sessionDTO | no |
| `destinations` | []destinationDTO | no |
| `recent_events` | []eventDTO | no |
| `recording` | recordingStatusDTO | no |
| `panel` | panelDTO | no |
| `update` | updateDTO | no |

### testSkippedDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `skipped` | bool | no |
| `message` | string | no |

### toggleAllRequest

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `enabled` | bool | sí |

### updateDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `available` | bool | no |
| `latest` | string | no |
| `url` | string | no |

### webhookCreate

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `name` | string | no |
| `url` | string | no |
| `format` | string | no |
| `secret` | string | no |
| `min_level` | string | no |
| `enabled` | bool | no |

### webhookDTO

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `id` | int64 | no |
| `name` | string | no |
| `url` | string | no |
| `format` | string | no |
| `has_secret` | bool | no |
| `min_level` | string | no |
| `enabled` | bool | no |
| `last_status` | int | sí |
| `last_error` | string | no |
| `created_at` | time (RFC 3339) | no |
| `updated_at` | time (RFC 3339) | no |

### webhookPatch

| Campo | Tipo | Opcional |
| --- | --- | --- |
| `name` | string | sí |
| `url` | string | sí |
| `format` | string | sí |
| `secret` | string | sí |
| `min_level` | string | sí |
| `enabled` | bool | sí |
