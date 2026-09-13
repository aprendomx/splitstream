# v0.13 «Alcance internacional» — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Panel bilingüe (es/en) con errores de la API traducidos por `Accept-Language`, README en inglés con comparativa honesta, y vista de historial y post-mortem por sesión.

**Architecture:** Un `t()` propio con dos JSON en `web/src/i18n` (sin librería); en el servidor, una tabla español→inglés en `internal/httpapi/i18n.go` que `writeError` aplica según un idioma puesto en el `context` por un middleware, con un test de AST que garantiza cobertura total; el historial reutiliza `GET /api/sessions`, `GET /api/sessions/{id}/chat` y `GET /api/recordings?session_id=`, y añade `GET /api/sessions/{id}` con eventos, grabaciones y contadores de chat. Sin migraciones, sin dependencias nuevas.

**Tech Stack:** Go 1.25 (`go/ast` de la biblioteca estándar para el test de cobertura), SQLite (modernc), Vue 3 + Quasar 2 + Pinia + Vite, Node 22 para el script de paridad.

**Spec:** `docs/superpowers/specs/2026-09-12-internacional-design.md` (autoridad), sobre `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` y el plan maestro `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §8.

## Global Constraints

- Cinco dependencias directas de Go, cero nuevas; `go.mod`/`go.sum` sin cambios. `web/package.json` y `package-lock.json` sin cambios (el script de paridad es Node puro).
- Ninguna migración: `SchemaVersion` sigue en 9 (spec §1). Si el plan de una tarea creyera necesitar un índice, es un ruling del controlador, no una decisión del implementador.
- El motor no cambia: nada en `internal/relay` ni `internal/rtmpio`. Fronteras de CI intactas (`internal/httpapi` no importa go-rtmp, rtmpio, webtls, `platforms/{twitch,youtube,kick,tokens}`).
- El `code` de los errores de la API no cambia nunca; solo el `message` se traduce (spec §2). Idiomas reconocidos: `es` (defecto) y `en`.
- No se traducen: logs (`slog`), eventos persistidos (`events.message`), webhooks salientes, `/metrics`, ni el texto de los proveedores tras «la plataforma respondió con un error:» (spec §4.3).
- Ningún componente del panel lleva copy literal salvo nombres propios (plataformas, «OBS», «RTMP») y unidades; fechas y números por `Intl` con el idioma activo (spec §3.1/§3.3).
- `es.json` y `en.json` con el mismo conjunto de claves, ninguna vacía; `web/scripts/i18n-check.mjs` corre en `prebuild` y en la CI (spec §3.4).
- Sin secretos en logs, errores, DTOs ni respuestas; DTOs `snake_case` (el test `TestDTOFieldNamesAreSnakeCase` recibe cada DTO nuevo).
- Comentarios del código y textos de usuario en español; los archivos `en.json`, `README.md` y `docs/comparison.md` en inglés natural, no traducción literal palabra por palabra.
- Tests: `go vet ./... && go test ./... -race -count=1` en verde; `cd web && npm run build` limpio; ningún test toca la red.

---

## Mapa de archivos

| Archivo | Responsabilidad |
| --- | --- |
| `internal/httpapi/i18n.go` | Idioma en el `context`, middleware `Accept-Language`, tabla es→en, `traducir` con plantillas |
| `internal/httpapi/i18n_en.go` | Solo la tabla `traducciones` (mensaje español → inglés), agrupada por archivo de origen |
| `internal/httpapi/i18n_test.go` | Test de AST de cobertura, `traducir`, middleware |
| `internal/httpapi/errors.go` | `writeError` gana el idioma del `context` (firma sin cambios: recibe `w`; el idioma viaja en el `ResponseWriter` envuelto) |
| `internal/store/sessions.go` | `has_recording` en `ListSessions`; `EventsBySession`; `ChatCountBySession` |
| `internal/httpapi/sessions.go`, `dto.go` | `GET /api/sessions/{id}` → `sessionDetailDTO`; `sessionSummaryDTO.has_recording` |
| `web/src/i18n/{index.js,es.json,en.json}` | `t()`, `idioma`, `cambiarIdioma`, diccionarios |
| `web/scripts/i18n-check.mjs` | Paridad de claves y claves usadas |
| `web/src/pages/Historial.vue`, `web/src/pages/Sesion.vue` | Historial y ficha de sesión |
| `README.md` (en), `README.es.md`, `docs/comparativa.md`, `docs/comparison.md`, `docs/lanzamiento.md`, `docs/manual-de-usuario.md` | Documentación |

---

### Task 1: Errores de la API según `Accept-Language`

**Files:**
- Create: `internal/httpapi/i18n.go`, `internal/httpapi/i18n_en.go`, `internal/httpapi/i18n_test.go`
- Modify: `internal/httpapi/errors.go` (`writeError`), `internal/httpapi/server.go` (`Handler()` envuelve el mux con el middleware)

**Interfaces:**
- Produces: `type idioma string` (`idiomaES = "es"`, `idiomaEN = "en"`); `func negociarIdioma(acceptLanguage string) idioma`; `func conIdioma(next http.Handler) http.Handler` (middleware: envuelve `w` en `*respuestaConIdioma{ResponseWriter, idioma}`); `func idiomaDe(w http.ResponseWriter) idioma` (desenvuelve; `es` si no está envuelto, para que los tests que llaman a handlers a pelo sigan funcionando); `func traducir(l idioma, msg string) string`; `var traducciones map[string]string` (español → inglés; las claves pueden llevar `{0}`, `{1}` como comodines).

- [ ] **Step 1: Test de negociación y traducción (`i18n_test.go`)**

```go
func TestNegociarIdioma(t *testing.T) {
	casos := map[string]idioma{
		"": idiomaES, "es": idiomaES, "fr": idiomaES, "en": idiomaEN, "en-US": idiomaEN,
		"en-GB,en;q=0.9,es;q=0.8": idiomaEN, "es-MX,en;q=0.5": idiomaES,
		"fr-FR,en;q=0.7,de;q=0.6": idiomaEN, "*": idiomaES, "EN": idiomaEN,
	}
	for in, want := range casos {
		if got := negociarIdioma(in); got != want {
			t.Errorf("negociarIdioma(%q) = %q, quería %q", in, got, want)
		}
	}
}

func TestTraducirLiteralPlantillaYSinEntrada(t *testing.T) {
	if got := traducir(idiomaEN, "destino no encontrado"); got != "destination not found" {
		t.Errorf("literal: %q", got)
	}
	if got := traducir(idiomaEN, "limit debe ser un número"); got != "limit must be a number" {
		t.Errorf("plantilla: %q", got)
	}
	if got := traducir(idiomaEN, "la cuenta de Ana necesita reconectarse"); got != "the account Ana needs to be reconnected" {
		t.Errorf("plantilla con nombre: %q", got)
	}
	if got := traducir(idiomaEN, "texto que no existe"); got != "texto que no existe" {
		t.Errorf("sin entrada debe devolver el original: %q", got)
	}
	if got := traducir(idiomaES, "destino no encontrado"); got != "destino no encontrado" {
		t.Errorf("es no traduce: %q", got)
	}
}
```

- [ ] **Step 2: Test de cobertura por AST**

```go
// TestTodoMensajeDeErrorTieneTraduccion recorre el código de httpapi y del store y exige
// que cada literal que puede llegar al cliente tenga entrada en `traducciones` (o case con
// una plantilla). Quien añade un mensaje sin traducción rompe este test, no el panel de
// alguien en inglés.
func TestTodoMensajeDeErrorTieneTraduccion(t *testing.T) {
	literales := append(literalesDeWriteError(t, "."), literalesDeErroresDelStore(t, "../store")...)
	if len(literales) < 80 {
		t.Fatalf("solo se encontraron %d literales: el recolector está roto", len(literales))
	}
	for _, l := range literales {
		if traducir(idiomaEN, l.texto) == l.texto {
			t.Errorf("%s: sin traducción: %q", l.pos, l.texto)
		}
	}
}
```

`literalesDeWriteError(dir)`: `parser.ParseDir` con `parser.ParseComments` saltando `_test.go`; `ast.Inspect` buscando `*ast.CallExpr` cuyo `Fun` sea el identificador `writeError` y cuyo cuarto argumento sea (a) un `*ast.BasicLit` de tipo STRING → texto sin comillas; (b) un `*ast.BinaryExpr` de `+` con al menos un literal → se reconstruye la plantilla sustituyendo cada parte no literal por `{n}` (así `name+" debe ser un número"` produce `{0} debe ser un número`); (c) cualquier otra expresión (`err.Error()`, `mensajePlataforma(err)`) → se ignora aquí porque sus textos llegan por el store o por `mensajePlataforma`, cubiertos aparte. Además recoge los literales de `mensajePlataforma`, `mensajeTitulo`, `textoSeguro`-wrapped y `errSinTitulo` buscando `*ast.ReturnStmt` en esas funciones (`funcs := map[string]bool{"mensajePlataforma": true, "mensajeTitulo": true}`) y las cadenas de `errors.New` asignadas a variables de paquete que empiecen por `err` en httpapi. `literalesDeErroresDelStore(dir)`: `fmt.Errorf` cuyo primer argumento empieza por `"%w: "` y cuyo segundo es `ErrInvalidInput`, `ErrConflict` o `ErrNotFound` → la parte tras `%w: ` (con `%s`/`%d`/`%q` sustituidos por `{n}`), más los `errors.New` de `internal/store/errors.go` (`ErrNotFound`, `ErrInvalidInput`, `ErrConflict` tienen texto propio: «no encontrado», etc.). Posición con `fset.Position`.

- [ ] **Step 3: Test del middleware**

```go
func TestAcceptLanguageTraduceLosErrores(t *testing.T) {
	srv, _ := newTestServer(t)
	cookies := login(t, srv)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	pedir := func(lang string) errorBody {
		req, _ := http.NewRequest("GET", ts.URL+"/api/destinations/999999", nil)
		for _, c := range cookies { req.AddCookie(c) }
		if lang != "" { req.Header.Set("Accept-Language", lang) }
		resp, err := http.DefaultClient.Do(req)
		if err != nil { t.Fatal(err) }
		defer resp.Body.Close()
		var body errorBody
		json.NewDecoder(resp.Body).Decode(&body)
		return body
	}
	if b := pedir("en"); b.Error.Code != codeNotFound || !strings.Contains(b.Error.Message, "not found") {
		t.Errorf("en: %+v", b)
	}
	if b := pedir(""); b.Error.Code != codeNotFound || !strings.Contains(b.Error.Message, "no encontrado") {
		t.Errorf("es: %+v", b)
	}
	if b := pedir("fr"); !strings.Contains(b.Error.Message, "no encontrado") {
		t.Errorf("fr cae a es: %+v", b)
	}
}
```

(Comprueba en `destinations.go` qué ruta y mensaje devuelve un 404 de destino y ajusta la URL y las subcadenas al texto real: el test tiene que ejercitar un `writeStoreError` con `ErrNotFound`.)

- [ ] **Step 4: Correr los tests → fallan** (`go test ./internal/httpapi/ -run 'Negociar|Traducir|TodoMensaje|AcceptLanguage'`: símbolos sin definir).

- [ ] **Step 5: Implementar `i18n.go`**

```go
package httpapi

// El idioma de una respuesta lo decide quien mira, no el proceso: viaja con la petición
// (Accept-Language) y se aplica solo a los `message` de error, que son texto para
// personas. El `code` es el contrato y no se toca (spec v0.13 §2 y §4).

type idioma string

const (
	idiomaES idioma = "es"
	idiomaEN idioma = "en"
)

// negociarIdioma lee Accept-Language: el primer idioma reconocido en orden de aparición
// (los q= se respetan por orden estable de mayor a menor). Solo se conoce `en`; todo lo
// demás es `es`.
func negociarIdioma(h string) idioma {
	type cand struct{ tag string; q float64; pos int }
	var cs []cand
	for i, parte := range strings.Split(h, ",") {
		parte = strings.TrimSpace(parte)
		if parte == "" { continue }
		tag, resto, _ := strings.Cut(parte, ";")
		q := 1.0
		if strings.HasPrefix(strings.TrimSpace(resto), "q=") {
			if v, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(resto), "q="), 64); err == nil { q = v }
		}
		cs = append(cs, cand{strings.ToLower(strings.TrimSpace(tag)), q, i})
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].q > cs[j].q })
	for _, c := range cs {
		if c.q <= 0 { continue }
		switch {
		case c.tag == "en" || strings.HasPrefix(c.tag, "en-"):
			return idiomaEN
		case c.tag == "es" || strings.HasPrefix(c.tag, "es-"):
			return idiomaES
		}
	}
	return idiomaES
}

type respuestaConIdioma struct {
	http.ResponseWriter
	lang idioma
}

// Unwrap deja que http.ResponseController llegue al ResponseWriter original (Flush,
// Hijack para los WebSockets, SetWriteDeadline).
func (r *respuestaConIdioma) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func conIdioma(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&respuestaConIdioma{ResponseWriter: w, lang: negociarIdioma(r.Header.Get("Accept-Language"))}, r)
	})
}

func idiomaDe(w http.ResponseWriter) idioma {
	for w != nil {
		if r, ok := w.(*respuestaConIdioma); ok { return r.lang }
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok { break }
		w = u.Unwrap()
	}
	return idiomaES
}

// traducir devuelve msg en el idioma pedido. Prueba primero el literal exacto y luego las
// plantillas con {n}, de la más larga a la más corta; las partes variables (nombres,
// campos) no se traducen. Sin entrada devuelve el original: nunca se pierde información.
func traducir(l idioma, msg string) string {
	if l != idiomaEN { return msg }
	if t, ok := traducciones[msg]; ok { return t }
	for _, p := range plantillas() {
		if vars, ok := p.casar(msg); ok { return p.aplicar(vars) }
	}
	return msg
}
```

El envoltorio también reexpone lo que los WebSockets y el webhook necesitan del `ResponseWriter` original, porque una aserción directa `w.(http.Hijacker)` o `w.(http.Flusher)` no atraviesa un struct que embebe la interfaz:

```go
func (r *respuestaConIdioma) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok { f.Flush() }
}

func (r *respuestaConIdioma) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok { return h.Hijack() }
	return nil, nil, http.ErrNotSupported
}
```

Con eso `webhook.go:83` (`w.(http.Flusher)`) y la subida a WebSocket de `ws.go`/`preview.go` siguen funcionando sin tocarlos; los tests `chat_test.go` y `preview_ws_test.go` lo confirman (pasan por `srv.Handler()`).

`plantillas()` compila una vez (`sync.Once`) las claves de `traducciones` que contienen `{`: cada una se convierte en una `regexp` con `regexp.QuoteMeta` por trozos y `(.+?)` por comodín, anclada (`^…$`), ordenadas por longitud del literal descendente; `aplicar` sustituye `{n}` en el valor inglés. Los WebSockets (`/api/chat/ws`, vista previa) hacen `Hijack` a través de `http.ResponseController` o de una aserción `http.Hijacker`: comprueba `internal/httpapi/ws.go` y `preview.go`; si usan la aserción directa `w.(http.Hijacker)`, cámbialos a `http.NewResponseController(w).Hijack()` (biblioteca estándar) para que atraviesen el envoltorio. Lo mismo con `Flush` (`webhook.go`, SSE si lo hay).

- [ ] **Step 6: `writeError` y `Handler()`**

```go
func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: traducir(idiomaDe(w), msg)}})
}
```

`server.go`: `func (s *Server) Handler() http.Handler { return conIdioma(s.mux) }`. Nada más cambia: los ~90 sitios de llamada quedan igual.

- [ ] **Step 7: `i18n_en.go` — la tabla**

Genera el primer volcado con un `go test -run TestTodoMensajeDeErrorTieneTraduccion -v` que imprima los literales sin entrada (deja ese `t.Logf` en el test: sirve para la próxima persona), y escribe la tabla a mano en inglés natural, agrupada por archivo con un comentario por grupo. Incluye las plantillas: `"{0} debe ser un número": "{0} must be a number"`, `"la cuenta de {0} necesita reconectarse": "the account {0} needs to be reconnected"`, `"la plataforma respondió con un error: {0}": "the platform answered with an error: {0}"`, `"crea la emisión primero en {0}": "create the broadcast first on {0}"`, etc., y los textos de `store.ErrNotFound/ErrInvalidInput/ErrConflict` con sus sufijos (`"no encontrado: destino"` → `"not found: destination"`; mira `internal/store/errors.go` para la forma exacta). Los mensajes del setup, login (`auth.go`), backup, logos, recording, webhooks, live, platforms, broadcasts, destinations, toggleall, test_destination y sessions: todos.

- [ ] **Step 8: Correr y commit**

Run: `go vet ./internal/httpapi/ && go test ./internal/httpapi/ -race -count=1` (≈2 min) y `go test ./internal/httpapi/ -run TestTodoMensaje -v` (sin `Errorf`).

```bash
git add internal/httpapi
git commit -m "feat(httpapi): mensajes de error en inglés según Accept-Language, con cobertura garantizada por AST"
```

---

### Task 2: Store — `has_recording`, `EventsBySession`, `ChatCountBySession`

**Files:**
- Modify: `internal/store/sessions.go`, `internal/store/sessions_test.go`

**Interfaces:**
- Produces: `SessionSummary.HasRecording bool`; `func (d *DB) EventsBySession(ctx, sessionID int64, limit int) ([]Event, error)` (ascendente por `id`; `limit` ≤ 0 → 2000; tope duro 2000); `func (d *DB) ChatCountBySession(ctx, sessionID int64) (total int, porPlataforma map[string]int, err error)`.

- [ ] **Step 1: Tests**

```go
func TestListSessionsSaysWhetherARecordingExists(t *testing.T) {
	db := abrir(t) // el helper de este archivo
	s1 := sesion(t, db); s2 := sesion(t, db)
	if _, err := db.OpenRecording(ctx, s1, "a.flv", 0, time.Now()); err != nil { t.Fatal(err) }
	ss, err := db.ListSessions(ctx, 10, 0)
	// s2 es la más reciente: sin grabación; s1 con grabación
	if ss[0].ID != s2 || ss[0].HasRecording { t.Errorf("s2 = %+v", ss[0]) }
	if ss[1].ID != s1 || !ss[1].HasRecording { t.Errorf("s1 = %+v", ss[1]) }
}

func TestEventsBySessionIsAscendingScopedAndCapped(t *testing.T) {
	// 3 eventos en s1, 1 en s2 y 1 sin sesión → EventsBySession(s1, 0) devuelve 3 en orden id asc;
	// EventsBySession(s1, 2) devuelve los 2 primeros; sesión inexistente → [] sin error.
}

func TestChatCountBySessionGroupsByPlatform(t *testing.T) {
	// InsertChatMessages: 2 twitch + 1 kick en s1, 1 youtube en s2 → total 3, {"twitch":2,"kick":1}; s2 → 1; s3 → 0 y mapa vacío (no nil).
}
```

- [ ] **Step 2: Correr → fallan.**

- [ ] **Step 3: Implementar**

`ListSessions`: añadir a la SELECT `EXISTS (SELECT 1 FROM recordings r WHERE r.session_id = s.id)` y escanearlo en `HasRecording`. `EventsBySession`: `SELECT id, session_id, destination_id, level, kind, message, created_at FROM events WHERE session_id = ? ORDER BY id ASC LIMIT ?` reutilizando el escaneo de `RecentEvents` (extrae `scanEvent(rows)` si `RecentEvents` lo tiene en línea, para no duplicar). `ChatCountBySession`: `SELECT platform, count(*) FROM chat_messages WHERE session_id = ? GROUP BY platform`; total = suma. Comentarios en español explicando el tope (2000: una sesión de 8 h con reconexiones no pasa de cientos; el tope es un cinturón, no un límite de diseño).

- [ ] **Step 4: Correr y commit**

Run: `go vet ./internal/store/ && go test ./internal/store/ -race -count=1`.

```bash
git add internal/store
git commit -m "feat(store): grabación por sesión en el listado, eventos y contadores de chat por sesión"
```

---

### Task 3: `GET /api/sessions/{id}` y `has_recording` en el listado

**Files:**
- Modify: `internal/httpapi/sessions.go`, `internal/httpapi/dto.go`, `internal/httpapi/dto_test.go`, `internal/httpapi/server.go` (ruta), `internal/httpapi/sessions_test.go`

**Interfaces:**
- Consumes: Task 2.
- Produces: `sessionSummaryDTO.HasRecording bool \`json:"has_recording"\``; `sessionDetailDTO{ID, StartedAt, EndedAt, Width, Height, BitrateBPS, DurationS int \`json:"duration_s"\`, Events []eventDTO, Recordings []recordingDTO, ChatCount int \`json:"chat_count"\`, ChatByPlatform map[string]int \`json:"chat_by_platform"\`}` (el nombre es `sessionDetailDTO` porque `sessionDTO` ya existe en el estado del panel y significa «la sesión viva»); ruta `GET /api/sessions/{id}` protegida.

- [ ] **Step 1: Tests (`sessions_test.go`)**

`TestSessionDetailCarriesEventsRecordingsAndChat`: sesión con 2 eventos (uno con `destination_id`), 1 grabación cerrada y 3 mensajes de chat (2 twitch, 1 kick) → 200 con `events` en orden ascendente, `recordings` con `bytes`/`duration_ms`, `chat_count 3`, `chat_by_platform {"twitch":2,"kick":1}`, `duration_s` = `ended_at − started_at` en segundos (fija `ended_at` con `EndSession` o el helper que exista; si la sesión sigue viva, `duration_s 0`). `TestSessionDetailNotFound`: id inexistente → 404 `not_found`; id no numérico → 400. `TestSessionsListCarriesHasRecording`: lista con una sesión con grabación y otra sin → el campo cuadra. `TestDTOFieldNamesAreSnakeCase` gana `sessionDetailDTO{}`.

- [ ] **Step 2: Correr → fallan.**

- [ ] **Step 3: Implementar**

`handleSessionDetail`: `strconv.ParseInt(r.PathValue("id"))` (400 «id debe ser un número» como en `destinations.go`), `SessionByID` (`ErrNotFound` → `writeStoreError`), `EventsBySession(ctx, id, 0)`, `ListRecordings(ctx, id, 500, 0)` (convertidos con el `newRecordingDTO` existente en `recording.go`), `ChatCountBySession`. `newSessionSummaryDTO` copia `HasRecording`. Ruta: `protegida("GET /api/sessions/{id}", s.handleSessionDetail)` junto a las de sesiones (cuidado con el orden: `GET /api/sessions/{id}/chat` ya existe y el mux de Go 1.22+ resuelve por especificidad, así que no hay conflicto).

- [ ] **Step 4: Correr y commit**

Run: `go vet ./internal/httpapi/ && go test ./internal/httpapi/ -run 'Session|DTO' -race -count=1`.

```bash
git add internal/httpapi
git commit -m "feat(httpapi): ficha de sesión con eventos, grabaciones y chat; has_recording en el historial"
```

---

### Task 4: Infraestructura i18n del panel — `t()`, diccionarios, selector, paridad en CI

**Files:**
- Create: `web/src/i18n/index.js`, `web/src/i18n/es.json`, `web/src/i18n/en.json`, `web/scripts/i18n-check.mjs`
- Modify: `web/src/main.js` (paquete de idioma de Quasar), `web/src/App.vue` (selector + todo su copy por `t()`), `web/src/components/Asistente.vue` (selector en el primer paso + su copy), `web/package.json` (**solo** `scripts.prebuild`; nada en `dependencies`), `.github/workflows/ci.yml` (paso «paridad de idiomas» antes de compilar), `web/index.html` (sin cambios: `lang` lo pone `index.js` en runtime)

**Interfaces:**
- Produces: `import { t, idioma, cambiarIdioma, idiomas } from '@/i18n'`; `t(clave: string, params?: object): string` (con `params.n` elige `clave_plural`); `idioma: Ref<'es'|'en'>`; `cambiarIdioma('es'|'en')`; `idiomas = [{ id: 'es', nombre: 'Español' }, { id: 'en', nombre: 'English' }]`; `formatearFecha(iso, opciones)`, `formatearNumero(n)`; `CLAVE_IDIOMA = 'splitstream.idioma'`. Claves con punto por área: `app.*`, `asistente.*`, `panel.*`, `destino.*`, `dialogo_destino.*`, `chat.*`, `ajustes.*`, `grabaciones.*`, `creditos.*`, `historial.*`, `sesion.*`, `diagnostico.*`, `plataformas.*`, `errores.*`, `comun.*` (botones: `guardar`, `cancelar`, `cerrar`, `cargar_mas`, `si`, `no`).

- [ ] **Step 1: `web/src/i18n/index.js`**

```js
import { ref, watch } from 'vue'
import { Quasar } from 'quasar'
import langEs from 'quasar/lang/es'
import langEn from 'quasar/lang/en-US'
import es from './es.json'
import en from './en.json'

// Un t() propio en vez de vue-i18n: el spec base §5 cierra el frontend a Vue, Quasar,
// Pinia y vuedraggable, y dos diccionarios planos con interpolación y plural por sufijo
// cubren todo lo que el panel necesita. Añadir un idioma es un JSON y una línea aquí.
export const CLAVE_IDIOMA = 'splitstream.idioma'
export const idiomas = [
  { id: 'es', nombre: 'Español' },
  { id: 'en', nombre: 'English' },
]
const diccionarios = { es, en }
const quasarLang = { es: langEs, en: langEn }

function idiomaInicial() {
  try {
    const guardado = localStorage.getItem(CLAVE_IDIOMA)
    if (guardado && diccionarios[guardado]) return guardado
  } catch { /* sin localStorage: se decide por el navegador */ }
  const nav = (navigator.language || 'es').toLowerCase()
  return nav.startsWith('en') ? 'en' : 'es'
}

export const idioma = ref(idiomaInicial())

const avisadas = new Set()
export function t(clave, params = {}) {
  let k = clave
  if (typeof params.n === 'number' && params.n !== 1 && diccionarios[idioma.value][`${clave}_plural`]) k = `${clave}_plural`
  let texto = diccionarios[idioma.value][k] ?? diccionarios.es[k]
  if (texto === undefined) {
    if (import.meta.env.DEV && !avisadas.has(clave)) { avisadas.add(clave); console.warn(`i18n: falta la clave ${clave}`) }
    return clave
  }
  return texto.replace(/\{(\w+)\}/g, (_, nombre) => (params[nombre] ?? `{${nombre}}`))
}

export function cambiarIdioma(id) {
  if (!diccionarios[id]) return
  idioma.value = id
  try { localStorage.setItem(CLAVE_IDIOMA, id) } catch { /* se pierde al recargar; no pasa nada */ }
}

export const formatearFecha = (iso, opciones = { dateStyle: 'medium', timeStyle: 'short' }) =>
  new Intl.DateTimeFormat(idioma.value, opciones).format(new Date(iso))
export const formatearNumero = (n, opciones = {}) => new Intl.NumberFormat(idioma.value, opciones).format(n)

watch(idioma, (id) => {
  Quasar.lang.set(quasarLang[id])
  document.documentElement.lang = id
}, { immediate: true })
```

(Si `quasar/lang/es` no resuelve con Vite en este proyecto, usa `quasar/lang/es.js`; comprueba con `ls web/node_modules/quasar/lang/`.) `t()` es reactivo porque lee `idioma.value` dentro de una `computed`/plantilla: en las plantillas se usa como `{{ t('app.titulo') }}` y Vue lo recalcula al cambiar `idioma`.

- [ ] **Step 2: `es.json` y `en.json` iniciales**

Con las claves de `App.vue` y `Asistente.vue` (todas: título, aria-labels de los botones de la barra, aviso de versión, formulario de entrada, mensajes de error del login, pasos del asistente). Formato: JSON plano ordenado alfabéticamente por clave, dos espacios. Ejemplo:

```json
{
  "app.aviso_version": "Hay una versión nueva de Splitstream ({version}).",
  "app.cerrar_sesion": "Cerrar sesión",
  "app.creditos": "Créditos y licencias",
  "app.idioma": "Idioma",
  "comun.cancelar": "Cancelar",
  "comun.cargar_mas": "Cargar más",
  "comun.cerrar": "Cerrar"
}
```

- [ ] **Step 3: Selector en `App.vue` y en `Asistente.vue`**

En la barra, antes de Grabaciones: `<q-btn-dropdown flat dense no-caps :label="idioma.toUpperCase()" :aria-label="t('app.idioma')"><q-list><q-item v-for="l in idiomas" :key="l.id" clickable v-close-popup @click="cambiarIdioma(l.id)"><q-item-section>{{ l.nombre }}</q-item-section></q-item></q-list></q-btn-dropdown>` (visible también sin sesión iniciada: la pantalla de entrada también se traduce). En el primer paso del asistente, el mismo selector como `q-btn-toggle` con las dos opciones.

- [ ] **Step 4: `web/scripts/i18n-check.mjs`**

```js
#!/usr/bin/env node
// Paridad de idiomas: es.json y en.json con las mismas claves, ninguna vacía, y toda
// clave t('...') literal del código existe. Sin dependencias: corre en prebuild y en CI.
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const raiz = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const es = JSON.parse(readFileSync(join(raiz, 'i18n', 'es.json'), 'utf8'))
const en = JSON.parse(readFileSync(join(raiz, 'i18n', 'en.json'), 'utf8'))
const errores = []
for (const k of Object.keys(es)) if (!(k in en)) errores.push(`falta en en.json: ${k}`)
for (const k of Object.keys(en)) if (!(k in es)) errores.push(`sobra en en.json (no está en es.json): ${k}`)
for (const [k, v] of [...Object.entries(es), ...Object.entries(en)]) if (!String(v).trim()) errores.push(`vacía: ${k}`)

function archivos(dir) {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n)
    return statSync(p).isDirectory() ? archivos(p) : /\.(vue|js)$/.test(n) ? [p] : []
  })
}
const usadas = new Set()
for (const f of archivos(raiz)) {
  const src = readFileSync(f, 'utf8')
  for (const m of src.matchAll(/\bt\(\s*'([a-z0-9_.]+)'/g)) usadas.add(m[1])
}
for (const k of usadas) if (!(k in es)) errores.push(`clave usada sin definir: ${k}`)
const sinUsar = Object.keys(es).filter((k) => !usadas.has(k) && !usadas.has(k.replace(/_plural$/, '')))
if (errores.length) { console.error(errores.join('\n')); process.exit(1) }
console.log(`i18n: ${Object.keys(es).length} claves, ${usadas.size} usadas, ${sinUsar.length} sin uso literal`)
```

(Las claves construidas dinámicamente —por ejemplo `t(\`diagnostico.${estado}\`)`— no se detectan como usadas: se listan en «sin uso literal» a título informativo, no como error.) `package.json`: `"prebuild": "node scripts/i18n-check.mjs"`. `ci.yml`, job `web`, antes de «compilar»: `- name: paridad de idiomas` / `run: cd web && node scripts/i18n-check.mjs`.

- [ ] **Step 5: `main.js`**

Importar `'@/i18n'` una vez (el `watch` inmediato aplica el paquete de Quasar al arrancar): `import '@/i18n'` tras `app.use(Quasar, …)`. Sin `lang` en la config de Quasar (lo pone el watch).

- [ ] **Step 6: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build` (el `prebuild` corre solo). Cambia el idioma en un `npm run dev` con el binario en `:8099` si tienes cómo; si no, revisa a mano que ninguna plantilla de `App.vue`/`Asistente.vue` conserve texto literal (`grep -n "[a-záéíóúñ]" web/src/App.vue` sobre las líneas de plantilla).

```bash
git add web/src/i18n web/scripts web/src/main.js web/src/App.vue web/src/components/Asistente.vue web/package.json .github/workflows/ci.yml
git commit -m "feat(panel): infraestructura de idiomas con t() propio, selector y paridad en CI"
```

---

### Task 5: Migrar el copy de los componentes a `t()`

**Files:**
- Modify: `web/src/components/{AsistenteCredenciales,ConectarCuenta,Chat,DialogoDestino,DialogoWebhook,RegistroEventos,TarjetaDestino,TituloEnVivo,VistaPrevia}.vue`, `web/src/i18n/{es,en}.json`

**Interfaces:**
- Consumes: Task 4.
- Produces: claves `asistente_credenciales.*`, `conectar.*`, `chat.*`, `dialogo_destino.*`, `dialogo_webhook.*`, `registro.*`, `destino.*`, `titulo.*`, `vista_previa.*`, `comun.*`.

- [ ] **Step 1: Componente a componente**

Para cada archivo: (1) `import { t, formatearFecha } from '@/i18n'` en `<script setup>`; (2) cada texto de plantilla (`>texto<`, `label=`, `title=`, `caption=`, `placeholder=`, `hint=`, `aria-label=`, `message=`) pasa a `:label="t('…')"` / `{{ t('…') }}`; (3) los textos en `script` (notificaciones `$q.notify`, mensajes de validación, `confirm`) pasan a `t()`; (4) `toLocaleTimeString('es', …)` y similares pasan a `formatearFecha(iso, { timeStyle: 'medium' })`; (5) los plurales con `params.n` (`t('destino.n_destinos', { n })` con `destino.n_destinos` = «{n} destino» y `destino.n_destinos_plural` = «{n} destinos»). Los nombres de plataformas, «OBS», «RTMP», «FLV», «MP4», URLs y comandos (`ffmpeg …`) NO se traducen. En `AsistenteCredenciales.vue` los pasos de las consolas se traducen enteros (son ~40 cadenas: es el archivo más largo; hazlo con calma y clave por paso: `asistente_credenciales.youtube.paso1.titulo`, `.texto`).

- [ ] **Step 2: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build`; y `grep -nE "^\s*<[^>]*>[^<{]*[a-záéíóúñA-Z]{3,}" web/src/components/*.vue | grep -v "t('" | grep -vi "obs\|rtmp\|flv\|mp4\|http\|ffmpeg"` para cazar restos (revisa a mano cada línea que salga).

```bash
git add web/src/components web/src/i18n
git commit -m "feat(panel): los componentes hablan por t()"
```

---

### Task 6: Migrar el copy de las páginas, el diagnóstico y el catálogo de plataformas

**Files:**
- Modify: `web/src/pages/{Panel,Ajustes,Grabaciones,Creditos}.vue`, `web/src/diagnostico.js`, `web/src/plataformas.js`, `web/src/stores/panel.js` (si tiene textos de notificación), `web/src/i18n/{es,en}.json`

**Interfaces:**
- Consumes: Task 4.
- Produces: claves `panel.*`, `ajustes.*`, `grabaciones.*`, `creditos.*`, `diagnostico.*`, `plataformas.*`.

- [ ] **Step 1: Páginas** — mismo procedimiento que la Task 5. `Creditos.vue` traduce sus propios textos (títulos, explicaciones) pero NO los nombres ni licencias de las dependencias (son datos). `Ajustes.vue` gana, junto a la sección de cuentas, un párrafo `ajustes.idioma_nota`: «Los eventos del registro y los logs del servidor quedan en español: son evidencia de lo que pasó, no interfaz» (en inglés en `en.json`).

- [ ] **Step 2: `diagnostico.js` y `plataformas.js`** — los textos de diagnóstico (título y acción por estado) pasan a claves `diagnostico.<estado>.titulo` / `.accion` y `diagnosticar` devuelve **claves** (`{ tituloKey, accionKey, tono, icono }`); el componente que los enseña (`TarjetaDestino.vue`, ya migrado en la Task 5) hace `t(d.tituloKey)`. Ajusta lo que `TarjetaDestino.vue` espere (puede que la Task 5 haya dejado `t()` alrededor de `d.titulo`: unifica aquí a `tituloKey` y actualiza el componente). `plataformas.js`: `nota`/`ayuda` de cada plataforma pasan a `notaKey` (`plataformas.youtube.nota`, …) y quien las enseña usa `t()`.

- [ ] **Step 3: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build`; el mismo `grep` de restos de la Task 5 sobre `web/src/pages/*.vue web/src/*.js`.

```bash
git add web/src/pages web/src/diagnostico.js web/src/plataformas.js web/src/stores web/src/components/TarjetaDestino.vue web/src/i18n
git commit -m "feat(panel): páginas, diagnóstico y catálogo de plataformas por t()"
```

---

### Task 7: Historial y ficha de sesión

**Files:**
- Create: `web/src/pages/Historial.vue`, `web/src/pages/Sesion.vue`
- Modify: `web/src/api.js` (`sesion(id)`), `web/src/router.js` (rutas `/historial` y `/historial/:id`), `web/src/App.vue` (botón «Historial» con `iHistorial`), `web/src/iconos.js` (`mdiHistory as iHistorial`, `mdiFilterVariant as iFiltro`; comprobar en `node_modules/@quasar/extras/mdi-v7/index.mjs`), `web/src/components/Chat.vue` (modo lectura), `web/src/i18n/{es,en}.json`

**Interfaces:**
- Consumes: Task 3 (`GET /api/sessions/{id}` → `{id, started_at, ended_at, width, height, bitrate_bps, duration_s, events[], recordings[], chat_count, chat_by_platform}`; `GET /api/sessions` con `has_recording`), `GET /api/sessions/{id}/chat?after&limit` (existente), `api.urlDescargaGrabacion(id)`.
- Produces: `api.sesion(id)`; `Chat.vue` acepta `props.sesionId` (número): con él no abre WebSocket, carga `api.chatSesion(id, after, 200)` paginando con «cargar más» y oculta la barra de cuota.

- [ ] **Step 1: `api.js` y router** — `sesion: (id) => pedir('GET', \`/api/sessions/${id}\`)`; rutas `{ path: '/historial', name: 'historial', component: () => import('@/pages/Historial.vue') }` y `{ path: '/historial/:id', name: 'sesion', component: () => import('@/pages/Sesion.vue'), props: true }`.

- [ ] **Step 2: `Historial.vue`** — `q-table` (o `q-list` como `Grabaciones.vue`, siguiendo su estilo) con columnas: inicio (`formatearFecha`), duración (`ended_at − started_at`, o «en curso»), resolución (`{width}×{height}` o «—»), bitrate (`kbps` con `formatearNumero`), eventos (tres `q-badge`: `info` gris, `warn` warning, `error` negative, solo los > 0), grabación (`q-icon iGrabaciones` si `has_recording`). Fila clicable → `router.push({ name: 'sesion', params: { id } })`. «Cargar más» con `before = última.id` como `Grabaciones.vue`. Vacío: «Todavía no hay sesiones: empieza a emitir desde OBS».

- [ ] **Step 3: `Sesion.vue`** — carga `api.sesion(id)` (404 → notificación y vuelta al historial). Cabecera: inicio, duración, resolución, bitrate, `chat_count`. **Resumen** (`q-card` con cuatro líneas calculadas en el cliente):

```js
const KINDS_RECONEXION = ['destination_retry', 'destination_disconnected', 'connect_failed']
const KINDS_SUSPENSION = ['destination_suspended']
const resumen = computed(() => {
  const ev = ficha.value?.events ?? []
  const porDestino = new Map()
  for (const e of ev) {
    if (!e.destination_id) continue
    const d = porDestino.get(e.destination_id) ?? { reconexiones: 0, suspendido: false }
    if (KINDS_RECONEXION.includes(e.kind)) d.reconexiones++
    if (KINDS_SUSPENSION.includes(e.kind)) d.suspendido = true
    porDestino.set(e.destination_id, d)
  }
  return {
    destinosConReconexiones: [...porDestino.entries()].filter(([, d]) => d.reconexiones > 0),
    suspendidos: [...porDestino.entries()].filter(([, d]) => d.suspendido).length,
    chat: ficha.value?.chat_by_platform ?? {},
    grabaciones: ficha.value?.recordings ?? [],
    bytes: (ficha.value?.recordings ?? []).reduce((s, r) => s + r.bytes, 0),
  }
})
```

(Los `kind` reales del código: `destination_connected`, `destination_disconnected`, `destination_retry`, `destination_suspended`, `connect_failed`, `publisher_connected`, `publisher_disconnected`, `recording_*`, `chat_*`. «Degradado» no se persiste como evento —solo es una métrica—, así que el resumen habla de **episodios de reconexión**, no de minutos degradados, y lo dice en una línea: `sesion.sin_metricas`.) Nombres de destino por `panel.destinos` (`panel.cargar()` si está vacío); un `destination_id` sin destino vivo se enseña como «destino #id (borrado)». **Línea de tiempo**: lista vertical de `events` con hora relativa al inicio (`+mm:ss`), badge de nivel y mensaje, con un `q-btn-toggle` de filtro (todos / avisos y errores). **Chat**: `<Chat :sesion-id="id" />`. **Grabaciones**: lista con descarga (`type="a" :href="api.urlDescargaGrabacion(r.id)"`), sin borrar (eso vive en Grabaciones).

- [ ] **Step 4: `Chat.vue` en modo lectura** — `const props = defineProps({ sesionId: { type: Number, default: 0 } })`; si `props.sesionId`, `onMounted` carga `api.chatSesion(props.sesionId, 0, 200)` y no llama a `conectar()`; botón «cargar más» pide con `after = último.id`; sin botón «cerrar» ni barra de cuota; el resto (pestañas por plataforma, render de mensajes) se reutiliza tal cual.

- [ ] **Step 5: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build`; con el binario de desarrollo, abrir `/historial` y una ficha (si no hay entorno, revisar a mano el flujo de datos contra los DTOs de la Task 3).

```bash
git add web/src
git commit -m "feat(panel): historial de sesiones y ficha con resumen, línea de tiempo, chat y grabaciones"
```

---

### Task 8: README en inglés, comparativa, lanzamiento y manual

**Files:**
- Create: `README.es.md` (copia literal del `README.md` actual), `docs/comparativa.md`, `docs/comparison.md`
- Modify: `README.md` (→ inglés), `docs/lanzamiento.md` (cabecera v0.13.0 + «English draft»), `docs/manual-de-usuario.md` (párrafo del idioma en §«Ajustes»/donde vivan los ajustes, y línea en «Preguntas frecuentes»: «¿Por qué el registro sigue en español?»), `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (§9: `Accept-Language` y `GET /api/sessions/{id}`; §10: panel bilingüe), `deploy/install.sh` y `deploy/homebrew`/`winget` **no** cambian (los enlaces al README siguen válidos)

- [ ] **Step 1: `README.es.md`** — `git mv`-no: copia (`cp README.md README.es.md`) y añade como primera línea `> También en inglés → [README.md](README.md)`.

- [ ] **Step 2: `README.md` en inglés** — misma estructura y mismos anclajes en inglés: «Status», «Install» (Mac (Homebrew), Windows (winget), Linux or macOS without Homebrew (script), By hand, Run it, On a server, On the internet, Behind a proxy), «Configuration» (tabla completa con las mismas variables y valores), «With Docker», «Update», «Keep it running» (Linux with systemd, macOS), «How to use it», «Development». Primera línea: `> Also in Spanish → [README.es.md](README.es.md)`. Inglés natural; los comandos, rutas y valores idénticos. Comprueba que ningún doc enlaza a un anclaje español del README (`grep -rn "README.md#" docs deploy`) y arregla los que haya apuntando a `README.es.md#…`.

- [ ] **Step 3: `docs/comparativa.md` y `docs/comparison.md`** — mismo contenido en los dos idiomas. Estructura: (1) «Qué compara este documento y qué no» (fecha de escritura; precios públicos a esa fecha con enlace a la página de precios; sin juicios); (2) tabla «Qué hace cada uno»: filas Splitstream / Restream / Castr / nginx-rtmp (módulo `rtmp` de nginx) / MediaMTX; columnas: relay a varios destinos, grabación local, título/chat de plataformas, panel web, instalación (binario / servicio en la nube / compilar), dónde corre; (3) tabla «Qué cuesta»: plan gratuito (qué incluye), primer plan de pago (precio/mes) para los servicios; «tu máquina» para los autoalojados; (4) tabla «Dónde va tu vídeo»: tu ordenador → plataformas directamente / tu ordenador → su nube → plataformas; (5) «Capacidades por plataforma»: la matriz del roadmap `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` §2 copiada tal cual (en `comparison.md`, traducida celda a celda). Sin adjetivos («mejor», «fácil», «potente») en ninguna celda: cada hecho con enlace a la fuente (documentación o página de precios del producto). Si un dato no se puede verificar, la celda dice «no documentado» y no se inventa.

- [ ] **Step 4: `docs/lanzamiento.md`** — cabecera: «Escrito para la v0.13.0, la primera bilingüe…»; sección nueva al final «English draft» con la traducción del borrador (§«El borrador») manteniendo la estructura de encabezados; el resto (posicionamiento, palabras clave) sin cambios salvo una línea de palabras clave en inglés.

- [ ] **Step 5: Manual y spec base** — el párrafo del idioma y la pregunta frecuente; §9 y §10 del spec base.

- [ ] **Step 6: Comprobar y commit**

Run: `grep -c "" README.md README.es.md` (tamaños parecidos); `grep -rn "README.md#" docs deploy | head`; enlaces internos de los cuatro documentos nuevos/modificados abiertos a mano; `go build ./...` (no toca código; por si acaso).

```bash
git add README.md README.es.md docs
git commit -m "docs: README en inglés, comparativa honesta, borrador de lanzamiento en inglés y notas de idioma"
```

---

## Autorrevisión

**Cobertura del spec.** §1: Tasks 4–6 (panel), 8 (README/comparativa), 7 (historial). §2: Task 1 (§9 `Accept-Language`), Task 3 (`GET /api/sessions/{id}`), Task 8 (spec base §9/§10). §3.1 `t()`: Task 4. §3.2 selector: Task 4. §3.3 migración: Tasks 4–6 (incluye `diagnostico.js` y `plataformas.js`). §3.4 paridad: Task 4 (`prebuild` + CI). §4.1 diseño: Task 1 (`i18n.go`, middleware, `writeError`). §4.2 cobertura AST: Task 1. §4.3 qué no se traduce: restricción global. §5: Task 8 (README, comparativa en dos idiomas, lanzamiento, manual). §6.1 API: Tasks 2–3 (`has_recording`, `EventsBySession`, `ChatCountBySession`, `sessionDetailDTO`). §6.2 panel: Task 7. §6.3 kinds: Task 7 lista los reales y aclara que «degradado» no se persiste. §7 pruebas: cada tarea; sin runner de componentes (restricción). §8: nada lo contradice.

**Marcadores.** Sin «TBD». Los dos archivos de código largos (`i18n.go`, `index.js`, `i18n-check.mjs`) van completos; la tabla de traducciones se escribe a partir del volcado del test (documentado en la Task 1 Step 7), que es la única forma honesta de listarla sin copiar aquí 90 cadenas que el test ya garantiza.

**Consistencia de tipos.** `idioma`/`negociarIdioma`/`traducir`/`idiomaDe` (T1) ↔ `writeError` (T1). `SessionSummary.HasRecording` (T2) ↔ `sessionSummaryDTO.HasRecording` (T3) ↔ `has_recording` en `Historial.vue` (T7). `EventsBySession`/`ChatCountBySession` (T2) ↔ `handleSessionDetail` (T3) ↔ `api.sesion(id)` y `ficha.value.events/recordings/chat_by_platform` (T7). `t`/`idioma`/`cambiarIdioma`/`idiomas`/`formatearFecha`/`formatearNumero` (T4) ↔ Tasks 5–7. `diagnosticar` devuelve `tituloKey`/`accionKey` (T6) ↔ `TarjetaDestino.vue` (T5, unificado en T6). `Chat.vue` con `sesionId` (T7) ↔ `api.chatSesion` (existente).

**Riesgos.** (1) El envoltorio del `ResponseWriter` puede romper `Hijack`/`Flush` si algún sitio hace una aserción directa: el envoltorio reexpone ambos y los tests de WebSocket lo confirman. (2) La migración del copy es mecánica pero larga (≈240 cadenas): se parte en dos tareas y el script de paridad + el `grep` de restos son la red. (3) La comparativa depende de precios públicos que cambian: lleva fecha y enlaces, y no adjetiva. (4) `quasar/lang/es` como import en Vite: comprobado que existe `web/node_modules/quasar/lang/es.js`; si el import necesita la extensión, se ajusta en la Task 4.
