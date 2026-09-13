# Splitstream — v0.13 «Alcance internacional»

**Fecha:** 2026-09-12
**Estado:** aprobado por el plan maestro (§8, tareas F.1–F.3); pendiente de plan de implementación
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
**Spec previo:** `docs/superpowers/specs/2026-09-11-youtube-kick-design.md` (v0.12)
**Roadmap:** `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` §7 («Alcance internacional»)
**Plan maestro:** `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §8
**Versión de partida:** `v0.12.0` (`main` @ `1f2fdf7`)

## 1. Qué se construye

Tres cosas, en este orden de valor:

1. **El panel conmuta de idioma entero** (español e inglés), sin librería nueva: un `t()` propio con dos diccionarios, los paquetes de idioma de Quasar para sus componentes, y los mensajes de error de la API traducidos en el servidor según `Accept-Language`.
2. **README en inglés como principal**, `README.es.md` con el texto actual, y `docs/comparativa.md`: Splitstream frente a Restream, Castr, nginx-rtmp y MediaMTX, sin adjetivos, con la matriz de capacidades del roadmap §2 tal cual.
3. **Vista de historial y post-mortem**: lista de sesiones y ficha de cada sesión con línea de tiempo de eventos, chat, grabaciones y un resumen calculado en el cliente.

Lo que NO cambia: el motor (`internal/relay`, `internal/rtmpio`), el modelo de datos (ninguna migración: `SchemaVersion` sigue en 9), las cinco dependencias directas de Go y las dependencias del panel (`package.json` sin cambios). El idioma de los logs del servidor y de los eventos persistidos sigue siendo español: son evidencia escrita una vez, no interfaz.

## 2. Enmiendas al spec base

- §10 (Frontend): el panel es bilingüe (es/en). El idioma se elige por `navigator.language` la primera vez y se guarda en `localStorage` (`splitstream.idioma`) cuando la persona lo cambia a mano. Los textos viven en `web/src/i18n/{es,en}.json`; ningún componente lleva copy literal en la plantilla salvo nombres propios (plataformas, «OBS», «RTMP») y unidades.
- §9 (API): las respuestas de error (`{error: {code, message}}`) se traducen según `Accept-Language` (`es` por defecto; `en` si es el primer idioma aceptado que conocemos). El `code` no cambia nunca: es el contrato; el `message` es texto para personas. Rutas nuevas: `GET /api/sessions/{id}` (ficha con eventos, grabaciones y contadores de chat). Ninguna ruta existente cambia de forma.
- §12 (Operación): nada nuevo que configurar. No hay variable de idioma del servidor: el idioma es de quien mira, no del proceso.

## 3. Panel bilingüe (`web/src/i18n`)

### 3.1 `t()` propio

`web/src/i18n/index.js` exporta `t(clave, params)`, `idioma` (ref reactiva `'es' | 'en'`), `cambiarIdioma(l)` y `idiomas`. Implementación de ~40 líneas: busca `clave` en el diccionario del idioma activo, interpola `{nombre}` con `params`, y si falta la clave cae al español y lo registra una vez con `console.warn` en desarrollo. Plurales sencillos por sufijo: `clave` y `clave_plural`, elegido por `params.n` (basta para «1 destino / 3 destinos»). Fechas y números por `Intl` con el idioma activo (`Intl.DateTimeFormat`, `Intl.NumberFormat`), nunca a mano.

Los diccionarios son JSON planos con claves con punto por área (`panel.titulo`, `destino.probar`, `chat.pausado_cuota`), y son los únicos dos archivos que cambian para añadir un idioma. Quasar recibe su paquete con `Quasar.lang.set(lang)` (`quasar/lang/es` y `quasar/lang/en-US`, ya incluidos en el paquete instalado), y `document.documentElement.lang` sigue al idioma.

### 3.2 Selector

En `App.vue`, junto al botón de Ajustes: un `q-btn-dropdown` con las dos opciones (con su nombre en su propio idioma: «Español», «English»). Cambiar es instantáneo (todo es reactivo por `idioma`) y persiste. El asistente de configuración inicial (`Asistente.vue`) enseña el selector en su primer paso: es la primera pantalla que ve una persona nueva.

### 3.3 Migración del copy

Todos los componentes y páginas (`Panel`, `Ajustes`, `Grabaciones`, `Creditos`, `Asistente`, `AsistenteCredenciales`, `ConectarCuenta`, `Chat`, `DialogoDestino`, `DialogoWebhook`, `RegistroEventos`, `TarjetaDestino`, `TituloEnVivo`, `VistaPrevia`, `App`) pasan a `t()`. Es un cambio mecánico, y el riesgo es dejarse textos: la CI lo vigila (§6). `web/src/diagnostico.js` y `web/src/plataformas.js` (textos de diagnóstico y descripciones de plataformas) también pasan a claves.

Los textos que vienen de la API (`message` de errores, `message` de resultados de `live/title`, eventos del registro) se muestran tal cual: el servidor ya los manda en el idioma pedido (§4) salvo los eventos persistidos, que son evidencia y quedan en español con una nota en el manual.

### 3.4 Paridad de claves

Un script `web/scripts/i18n-check.mjs` (Node puro, sin dependencias) comprueba que `es.json` y `en.json` tienen exactamente el mismo conjunto de claves, que ninguna está vacía, y que todo `t('...')` literal de `web/src` existe en `es.json`. Corre en la CI en el job del panel antes del build y en `npm run build` como paso previo (`prebuild`).

## 4. Errores de la API según `Accept-Language`

### 4.1 Diseño

`internal/httpapi/i18n.go`: un `map[string]string` de mensaje en español → inglés, y una función `traducir(lang, msg) string` que devuelve el inglés si existe la entrada, y el original si no (nunca se pierde información: un mensaje sin traducción sale en español). `writeError` lee el idioma del `context` de la petición, puesto por un middleware ligero que parsea `Accept-Language` (solo se reconoce `en`; cualquier otra cosa es `es`). No cambia ninguna firma ni ningún sitio de llamada: los ~90 `writeError` literales y los mensajes de las tres clases del store (`ErrInvalidInput`, `ErrNotFound`, `ErrConflict`, que `writeStoreError` reenvía tal cual) se traducen por su texto.

Los mensajes con partes variables (`name+" debe ser un número"`, `"la cuenta de "+acct.DisplayName+" necesita reconectarse"`, `mensajePlataforma`) se traducen por **plantilla**: la tabla admite entradas con `{0}`, `{1}` y `traducir` prueba primero el literal exacto y después las plantillas (de más larga a más corta) extrayendo las partes variables. Las partes variables (nombres de destino, cuentas, campos) no se traducen.

### 4.2 Cobertura garantizada

Un test (`i18n_test.go`) recorre el AST de `internal/httpapi` (`go/ast` de la biblioteca estándar; ninguna dependencia) y de `internal/store` buscando literales de cadena pasados a `writeError`, a `fmt.Errorf("%w: ...", ErrInvalidInput|ErrConflict|ErrNotFound)` y a `errors.New` en las variables de error exportadas que llegan al cliente, y falla si alguno no tiene entrada en la tabla (o no casa con ninguna plantilla). Así, quien añade un mensaje en español sin su traducción rompe la CI, no la experiencia de alguien en inglés.

### 4.3 Qué NO se traduce

Logs (`slog`), eventos persistidos (`events.message`), mensajes de los webhooks salientes (Discord/JSON: son los eventos), `/metrics`, y los textos de los proveedores de plataforma (`internal/platforms/...`) que llegan envueltos en «la plataforma respondió con un error: …»: ese prefijo sí se traduce, el resto es lo que dijo la plataforma.

## 5. README en inglés y comparativa

- `README.md` pasa a inglés (traducción fiel de la estructura actual: Estado, Instalación con las tres vías, Configuración con la tabla completa, Docker, Actualizar, Dejarlo funcionando, Cómo se usa, Desarrollo). `README.es.md` conserva el texto en español. Primera línea de cada uno: enlace cruzado («Also in Spanish → README.es.md» / «También en inglés → README.md»). Los `deploy/*` y `docs/*` que enlazan al README no cambian (los anclajes se mantienen por nombre en inglés en `README.md`; `README.es.md` conserva los actuales).
- `docs/comparativa.md` (en español) y `docs/comparison.md` (en inglés): Splitstream frente a **Restream**, **Castr**, **nginx-rtmp** y **MediaMTX**. Una tabla por eje: qué hace cada uno (relay a N destinos, grabación, título/chat, panel, instalación), qué cuesta (precio público a fecha de escritura, con la fecha), y **dónde va tu vídeo** (tu máquina, su nube). Sin adjetivos ni comparativos de valor; hechos verificables con enlace a la fuente. Cierra con la matriz de capacidades por plataforma del roadmap §2, copiada tal cual.
- `docs/lanzamiento.md` gana la sección «English draft» con la versión en inglés del borrador (misma estructura de encabezados) y la cabecera se actualiza a v0.13.0.
- `docs/manual-de-usuario.md`: §«Ajustes» o su equivalente gana un párrafo sobre el idioma (dónde se cambia, qué queda en español y por qué). El manual sigue solo en español en esta entrega; su traducción es trabajo de la v1.0 o de demanda real.

## 6. Historial y post-mortem

### 6.1 API

`GET /api/sessions/{id}` → `sessionDTO`:

```
{
  "id", "started_at", "ended_at", "width", "height", "bitrate_bps",
  "duration_s": int,                 // ended_at - started_at; 0 si sigue viva
  "events": [eventDTO...],           // todos los de la sesión, ascendente por id (tope 2000)
  "recordings": [recordingDTO...],   // los de la sesión (ListRecordings con session_id)
  "chat_count": int,                 // COUNT(*) de chat_messages de la sesión
  "chat_by_platform": {"twitch": n, "youtube": n, "kick": n}
}
```

Store: `EventsBySession(ctx, sessionID, limit) ([]Event, error)` (índice nuevo NO: `events` ya se consulta por `session_id` en volúmenes pequeños; si el plan mide que hace falta índice, es una migración 0010 con solo `CREATE INDEX`, y entonces `SchemaVersion` 10) y `ChatCountBySession(ctx, sessionID) (total int, porPlataforma map[string]int, err)`. `SessionByID` ya existe. 404 si la sesión no existe. La lista `GET /api/sessions` no cambia (ya trae contadores por nivel y pagina por `before`); gana `has_recording bool` calculado con una subconsulta `EXISTS`, para que la lista pueda enseñar el icono sin N peticiones.

### 6.2 Panel

- `web/src/pages/Historial.vue` (ruta `/historial`, botón en `App.vue` junto a Grabaciones): tabla de sesiones con fecha, duración, resolución y bitrate, tres contadores de eventos (info/warn/error como chips), icono de grabación si `has_recording`, y «cargar más» por `before`. Clic → ficha.
- `web/src/pages/Sesion.vue` (ruta `/historial/:id`): cabecera con los datos de la sesión; **resumen** calculado en el cliente a partir de `events`: destinos que reconectaron (eventos `destination_reconnected`/`destination_disconnected` por `destination_id`), minutos degradados (suma de intervalos entre `destination_degraded` y su recuperación), mensajes de chat por plataforma (`chat_by_platform`), grabaciones y tamaño total; **línea de tiempo** de eventos (lista vertical con hora relativa al inicio, nivel y mensaje, filtrable por nivel); **chat** de la sesión reutilizando `Chat.vue` en modo lectura (ya existe `GET /api/sessions/{id}/chat` con `after`/`limit`; se pagina hacia atrás); **grabaciones** con descarga (`urlDescargaGrabacion`) reutilizando el listado de `Grabaciones.vue` filtrado por sesión.
- Sin gráficas de bitrate en el tiempo: las métricas no se persisten (spec base §6.6) y persistirlas es otra decisión. El resumen lo dice en una línea cuando no hay datos.

### 6.3 Nombres de eventos

El resumen depende de los `kind` que ya se emiten. El plan tiene que listar los `kind` reales del código (`grep -rho 'Kind: *"[a-z_]*"'`) y mapear cada bloque del resumen a ellos; si algún concepto (por ejemplo «degradado» y su recuperación) no tiene un `kind` de fin, el resumen lo cuenta como «episodios» y no como minutos, y lo dice.

## 7. Pruebas

- **Panel**: `i18n-check.mjs` en CI y en `prebuild`; `npm run build` limpio. No hay tests de componentes (no hay runner en el proyecto y no se añade uno: sería una dependencia nueva).
- **API**: `i18n_test.go` (AST, cobertura total de mensajes); test de `traducir` con literal, plantilla y sin entrada; test del middleware (`Accept-Language: en`, `en-US,es;q=0.8`, `fr` → `es`, ausente → `es`); test de `GET /api/sessions/{id}` con eventos, grabaciones y chat (404 si no existe; tope de eventos); `has_recording` en la lista; `TestDTOFieldNamesAreSnakeCase` con `sessionDTO`.
- **Store**: `EventsBySession` (orden, tope, solo esa sesión), `ChatCountBySession` (por plataforma).
- **Docs**: enlaces internos comprobados a mano en la revisión; la comparativa lleva fecha y fuentes.

## 8. Fuera de esta entrega

Manual de usuario en inglés; más idiomas (la infraestructura los admite: dos archivos JSON y una línea en `idiomas`); traducción de logs y eventos persistidos; gráficas de métricas en el tiempo; runner de tests de componentes en el panel; persistencia de métricas.
