# v1.0 «Endurecimiento» — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Quitar riesgo antes de la v1.0.0: copia parcheada de `go-rtmp` con sus tres deudas arregladas y verificables, CI estricta (lint, vulnerabilidades, nocturna, Dependabot), y contrato de API y política de migraciones congelados y comprobados por tests.

**Architecture:** `third_party/go-rtmp` es la v0.0.7 copiada tal cual más tres parches (un `.diff` cada uno) y entra por `replace` en `go.mod`; un test comprueba que copia = upstream + parches. La CI gana `lint` (`golangci-lint` v2 con `.golangci.yml` justificado) y `vuln` (`govulncheck`), un workflow nocturno con la integración completa, la prueba de migración desde la release anterior y un humo opcional contra plataformas reales, y `dependabot.yml`. `routes()` pasa a una tabla; un test genera `docs/api.md` desde esa tabla y los DTO por reflexión y falla si el archivo no coincide; `docs/migraciones.md` y `deploy/migrate-test.sh` fijan y prueban la política de migraciones.

**Tech Stack:** Go 1.25 (`go/ast` no hace falta aquí; `reflect` para los DTO), `golangci-lint` v2 y `govulncheck` vía `go run` con versión fijada (no entran en `go.mod`), GitHub Actions, Bash + `shellcheck`, `git diff --no-index` para verificar los parches.

**Spec:** `docs/superpowers/specs/2026-09-13-endurecimiento-design.md` (autoridad), sobre `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` §15.9 y §16, y el plan maestro `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §9–§10.

## Global Constraints

- Cinco dependencias directas de Go, cero nuevas: el `replace` apunta al mismo módulo `github.com/yutopp/go-rtmp`; `go.mod` solo gana la línea `replace`; `go.sum` no cambia (las dependencias de la copia ya están); nunca `go mod tidy`. `web/package.json` sin cambios.
- Ninguna migración: `SchemaVersion` sigue en 9. El motor (`internal/relay`) no cambia; `internal/rtmpio` solo en lo que dice el spec §3.
- Fronteras de CI intactas (`internal/relay`/`internal/rtmpio` sin plataformas, chat, store, events, record; `internal/httpapi` sin go-rtmp, rtmpio, webtls, `platforms/{twitch,youtube,kick,tokens}`; `internal/platforms`/`internal/chat` sin motor; `internal/record` aislado).
- `third_party/go-rtmp` conserva `LICENCE.txt` (MIT) y no se edita sin su `.diff` en `third_party/go-rtmp/patches/` y su línea en `UPSTREAM.md`; `go vet`/`golangci-lint` del módulo principal no lo cubren (módulo aparte).
- Ningún test toca internet (los módulos de Go ya en caché no cuentan; un test que necesite la caché de módulos se salta si no está). Sin flakes con `-race`.
- Comportamiento por defecto sin cambios para quien actualiza: `SPLITSTREAM_RTMP_PRECOMMANDS` apagado; `WriteTimeout` de `go-rtmp` 0 → 5 s en la librería (Splitstream fija 3 s en su `Publisher`).
- Las excepciones del linter se justifican una a una en `.golangci.yml`; las temporales llevan fecha. Sin secretos en logs, errores ni DTOs; DTOs `snake_case`; comentarios y textos en español; `en.json`/`es.json` solo cambian si un mensaje de error nuevo lo exige (y el test de AST lo vigila).
- El `code` de la API no cambia; `/api/` es la v1 (spec §5.4).
- Tests: `go vet ./... && go test ./... -race -count=1` en verde; `cd web && npm run build` limpio; `shellcheck deploy/*.sh` limpio; los workflows YAML válidos.

---

## Mapa de archivos

| Archivo | Responsabilidad |
| --- | --- |
| `third_party/go-rtmp/` (copia de v0.0.7), `third_party/go-rtmp/UPSTREAM.md`, `third_party/go-rtmp/patches/000{1,2,3}-*.diff` | La contingencia: código ajeno + parches |
| `go.mod` | `replace github.com/yutopp/go-rtmp => ./third_party/go-rtmp` |
| `internal/rtmpio/upstream_test.go` | `TestGoRTMPCopyMatchesUpstreamPlusPatches` |
| `internal/rtmpio/publisher.go`, `publisher_test.go`, `internal/rtmpio/ingest.go` | `writeTimeout` 3 s, `PublisherConfig.PreCommands`, test del plazo y de los comandos previos |
| `internal/config/config.go` | `RTMPPreCommands` (`SPLITSTREAM_RTMP_PRECOMMANDS`) |
| `cmd/splitstream/main.go` | cableado de `PreCommands` en la fábrica de sinks |
| `.golangci.yml`, `Makefile` (`lint`, `vuln`), `.github/workflows/ci.yml` (jobs `lint`, `vuln`) | CI estricta |
| `.github/workflows/nightly.yml`, `.github/dependabot.yml`, `deploy/nightly-smoke.sh` | Nocturna y Dependabot |
| `internal/httpapi/server.go` (tabla `rutas`), `internal/httpapi/api_doc_test.go`, `docs/api.md` | Contrato de API generado |
| `docs/migraciones.md`, `deploy/migrate-test.sh`, `deploy/migrate_test_test.sh` | Política y prueba de migraciones |
| `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (§5, §7, §9, §11, §12, §13), `README.md`/`README.es.md`, `docs/manual-de-usuario.md`, `docs/lanzamiento.md`, `deploy/env.example` | Documentación de la v1.0 |

---

### Task 1: Copia de `go-rtmp` en `third_party` con `replace` y test de integridad

**Files:**
- Create: `third_party/go-rtmp/**` (copia), `third_party/go-rtmp/UPSTREAM.md`, `third_party/go-rtmp/patches/.gitkeep`, `internal/rtmpio/upstream_test.go`
- Modify: `go.mod` (una línea `replace`), `.github/workflows/ci.yml` (paso «la copia de go-rtmp es upstream + parches» en el job `test`, y `go test -race ./...` dentro de `third_party/go-rtmp`)

**Interfaces:**
- Produces: `third_party/go-rtmp` como módulo `github.com/yutopp/go-rtmp` (mismo `go.mod`/`go.sum` que upstream); `UPSTREAM.md` con el formato de §Step 2; `patches/NNNN-nombre.diff` generados con `git diff --no-index --src-prefix=a/ --dst-prefix=b/ <upstream>/<f> <copia>/<f>`; el test `TestGoRTMPCopyMatchesUpstreamPlusPatches` que las Tasks 2–4 mantienen en verde añadiendo su `.diff`.

- [ ] **Step 1: Copiar upstream**

```bash
UP="$(go env GOMODCACHE)/github.com/yutopp/go-rtmp@v0.0.7"
test -d "$UP" || go mod download github.com/yutopp/go-rtmp@v0.0.7
mkdir -p third_party && cp -R "$UP" third_party/go-rtmp && chmod -R u+w third_party/go-rtmp
rm -rf third_party/go-rtmp/example   # ejemplos con dependencias propias; no se compilan
mkdir -p third_party/go-rtmp/patches && touch third_party/go-rtmp/patches/.gitkeep
```

(La caché de módulos deja los archivos de solo lectura: el `chmod` es necesario. Comprueba que `third_party/go-rtmp/LICENCE.txt` está.)

- [ ] **Step 2: `UPSTREAM.md`**

```markdown
# Copia parcheada de github.com/yutopp/go-rtmp

- **Origen:** `github.com/yutopp/go-rtmp v0.0.7` (2024-07-15), `h1:` de go.sum: <copiar la línea de `go.sum`>
- **Licencia:** MIT (`LICENCE.txt`, sin cambios)
- **Por qué una copia y no un fork:** el módulo compila igual (`replace` en `go.mod` al mismo path), no hace falta otro repositorio, los parches viven al lado del código que los usa y se pueden proponer aguas arriba tal cual.
- **Excluido:** `example/` (dependencias propias, no se compila).

## Parches (en orden; cada uno es un `patches/NNNN-*.diff` aplicable con `git apply -p1` sobre la v0.0.7 limpia)

| N.º | Archivo | Qué arregla | Test |
| --- | --- | --- | --- |
| (vacío hasta la Task 2) | | | |

## Regenerar

1. `UP=$(go env GOMODCACHE)/github.com/yutopp/go-rtmp@v0.0.7`
2. `cp -R "$UP" /tmp/go-rtmp && chmod -R u+w /tmp/go-rtmp && rm -rf /tmp/go-rtmp/example`
3. `for p in patches/*.diff; do (cd /tmp/go-rtmp && git apply -p1 "$OLDPWD/$p"); done`
4. `diff -r /tmp/go-rtmp third_party/go-rtmp` sin salida más allá de `UPSTREAM.md` y `patches/`.

Cuando aguas arriba publique una versión con estos arreglos: quitar el `replace`, subir la versión y borrar este directorio.
```

- [ ] **Step 3: `go.mod`**

Añadir al final: `replace github.com/yutopp/go-rtmp => ./third_party/go-rtmp`. Ejecuta `go build ./... && go test ./internal/rtmpio/ -count=1` (debe seguir igual). **No** ejecutes `go mod tidy`. Comprueba `git diff go.sum` vacío.

- [ ] **Step 4: Test de integridad (`internal/rtmpio/upstream_test.go`)**

```go
package rtmpio

// TestGoRTMPCopyMatchesUpstreamPlusPatches: third_party/go-rtmp tiene que ser exactamente
// la v0.0.7 de la caché de módulos más los parches de patches/, ni un byte más. Así nadie
// edita la copia sin dejar el diff que la explica (spec v1.0 §3.4). Si la caché no tiene
// el módulo (clon limpio sin red) el test se salta: no toca internet.
func TestGoRTMPCopyMatchesUpstreamPlusPatches(t *testing.T) {
	up := filepath.Join(goEnv(t, "GOMODCACHE"), "github.com", "yutopp", "go-rtmp@v0.0.7")
	if _, err := os.Stat(up); err != nil {
		t.Skip("go-rtmp v0.0.7 no está en la caché de módulos; sin red no se puede comparar")
	}
	copia := filepath.Join(raizDelRepo(t), "third_party", "go-rtmp")
	parches := map[string]string{} // archivo → contenido esperado del diff
	entries, _ := filepath.Glob(filepath.Join(copia, "patches", "*.diff"))
	for _, p := range entries {
		b, err := os.ReadFile(p)
		if err != nil { t.Fatal(err) }
		for _, f := range archivosDelDiff(string(b)) { parches[f] += cuerpoDelDiff(string(b), f) }
	}
	// 1) Todo archivo de upstream (salvo example/) existe en la copia y es igual, o tiene parche.
	filepath.WalkDir(up, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() { return err }
		rel, _ := filepath.Rel(up, path)
		if strings.HasPrefix(rel, "example"+string(filepath.Separator)) { return nil }
		a, _ := os.ReadFile(path)
		b, errB := os.ReadFile(filepath.Join(copia, rel))
		if errB != nil { t.Errorf("%s: falta en la copia", rel); return nil }
		if bytes.Equal(a, b) {
			if _, tiene := parches[rel]; tiene { t.Errorf("%s: tiene parche pero es idéntico a upstream", rel) }
			return nil
		}
		want, ok := parches[rel]
		if !ok { t.Errorf("%s: difiere de upstream sin parche en patches/", rel); return nil }
		got := diffNoIndex(t, path, filepath.Join(copia, rel), rel)
		if got != want { t.Errorf("%s: el parche no coincide con el diff real\n--- esperado\n%s\n--- real\n%s", rel, want, got) }
		return nil
	})
	// 2) Nada en la copia que no esté en upstream, salvo UPSTREAM.md y patches/.
	filepath.WalkDir(copia, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() { return err }
		rel, _ := filepath.Rel(copia, path)
		if rel == "UPSTREAM.md" || strings.HasPrefix(rel, "patches"+string(filepath.Separator)) { return nil }
		if _, err := os.Stat(filepath.Join(up, rel)); err != nil { t.Errorf("%s: no existe en upstream", rel) }
		return nil
	})
}
```

Helpers: `goEnv(t, k)` (`exec.Command("go", "env", k)`), `raizDelRepo(t)` (`git rev-parse --show-toplevel`), `diffNoIndex(t, a, b, rel)` ejecuta `git diff --no-index --src-prefix=a/ --dst-prefix=b/ a b`, ignora el código de salida 1, y **normaliza** las rutas de la cabecera a `a/<rel>` y `b/<rel>` (las de `diff --git`, `---` y `+++`), quitando la línea `index …`; `archivosDelDiff(s)` lee los `+++ b/<rel>`; `cuerpoDelDiff(s, rel)` devuelve el bloque de ese archivo con la misma normalización. Los `.diff` de las Tasks 2–4 se generan con el mismo comando y la misma normalización (script `third_party/go-rtmp/patches/generar.sh <rel> <NNNN-nombre>`: crea el diff normalizado; `shellcheck` limpio). Con `patches/` vacío el test pasa porque la copia es idéntica.

- [ ] **Step 5: CI**

En `.github/workflows/ci.yml`, job `test`, después de «tests con -race»: `- name: la copia de go-rtmp compila y pasa sus tests` / `run: cd third_party/go-rtmp && go vet ./... && go test -race -count=1 ./...` (necesita `testify` en la caché de CI: lo descarga; está en el `go.sum` de la copia). El test de integridad ya corre con `go test ./...` del módulo principal.

- [ ] **Step 6: Comprobar y commit**

Run: `go build ./... && go test ./internal/rtmpio/ -race -count=1 && (cd third_party/go-rtmp && go test -race -count=1 ./...)`; `git diff --stat go.sum` vacío; `git status` sin archivos de `example/`.

```bash
git add go.mod third_party internal/rtmpio/upstream_test.go .github/workflows/ci.yml
git commit -m "build: go-rtmp v0.0.7 copiado en third_party con replace y test de integridad"
```

---

### Task 2: Parche 1 — `Stream.Write` con plazo configurable; `writeTimeout` 3 s en `rtmpio`

**Files:**
- Modify: `third_party/go-rtmp/conn.go` (`ConnConfig.WriteTimeout`), `third_party/go-rtmp/stream.go` (`WriteContext`, `Write` usa el plazo), `third_party/go-rtmp/UPSTREAM.md` (fila), `internal/rtmpio/publisher.go`, `internal/rtmpio/publisher_test.go`
- Create: `third_party/go-rtmp/patches/0001-write-timeout.diff`

**Interfaces:**
- Produces: `rtmp.ConnConfig.WriteTimeout time.Duration` (0 → 5 s); `func (s *Stream) WriteContext(ctx context.Context, chunkStreamID int, timestamp uint32, msg message.Message) error`; `rtmpio.writeTimeout = 3 * time.Second` (constante) fijada en los dos `rtmp.ConnConfig{}` de `Publisher.connect`.

- [ ] **Step 1: Test en `rtmpio` (falla sin el parche)**

```go
// TestWriteToAStalledPeerFailsWithinThreeSeconds: un destino que acepta la conexión y no
// lee nunca. Sin el parche 1, go-rtmp tarda 5 s cableados en rendirse; con él, el
// Publisher fija 3 s (writeTimeout) y el sink recupera antes (spec v1.0 §3.1).
func TestWriteToAStalledPeerFailsWithinThreeSeconds(t *testing.T) {
	srv := servidorRTMPQueNoDrena(t) // acepta, completa handshake+connect+createStream+publish y deja de leer
	p, _ := NewPublisher(PublisherConfig{URL: srv.URL(), StreamKey: crypto.Secret("k"), ChunkSize: 4096})
	if err := p.Connect(context.Background()); err != nil { t.Fatal(err) }
	// Llenar el buffer del socket: escribir hasta que Write devuelva error.
	inicio := time.Now()
	var err error
	for i := 0; i < 100000 && err == nil; i++ {
		err = p.WriteVideo(0, bytes.Repeat([]byte{0}, 64<<10))
	}
	if err == nil { t.Fatal("el peer que no drena nunca produjo error") }
	if d := time.Since(inicio); d < 2*time.Second || d > 4*time.Second {
		t.Errorf("tardó %v en rendirse; quería ≈3 s (5 s sería el valor cableado de upstream)", d)
	}
}
```

(`servidorRTMPQueNoDrena` se construye con el `Ingest` del propio paquete y un handler que, tras `OnPublishStart`, deja de leer del socket: mira `ingest.go` para dónde se lee y cómo pararlo —o usa un `net.Listener` crudo con un handshake mínimo si es más simple; lo que importa es que el `Write` se bloquee—. `WriteVideo` es el nombre del método de escritura de vídeo del `Publisher`: confirma en `publisher.go`.)

- [ ] **Step 2: Correr → falla** (tarda ≈5 s o el test no acota).

- [ ] **Step 3: Parche**

`conn.go`: en `ConnConfig`, `WriteTimeout time.Duration` con comentario en inglés (es código aguas arriba): `// WriteTimeout bounds each Stream.Write; zero keeps the historical 5s.`; en `normalize()`, `if c.WriteTimeout <= 0 { c.WriteTimeout = 5 * time.Second }`. `stream.go`:

```go
func (s *Stream) WriteContext(ctx context.Context, chunkStreamID int, timestamp uint32, msg message.Message) error {
	s.cmsg.Message = msg
	return s.streamer().Write(ctx, chunkStreamID, timestamp, &s.cmsg)
}

func (s *Stream) Write(chunkStreamID int, timestamp uint32, msg message.Message) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.conn.config.WriteTimeout)
	defer cancel()
	return s.WriteContext(ctx, chunkStreamID, timestamp, msg)
}
```

(Comprueba cómo llega `config` al `Stream`: `s.conn.config` existe en `conn.go:84` `newConn`; si el campo se llama distinto, adapta.) `internal/rtmpio/publisher.go`: `const writeTimeout = 3 * time.Second` con comentario en español, y `&rtmp.ConnConfig{WriteTimeout: writeTimeout}` en los dos `Dial`. Generar el diff: `third_party/go-rtmp/patches/generar.sh conn.go 0001-write-timeout` y lo mismo para `stream.go` en el **mismo** archivo `.diff` (el script acepta varios archivos: `generar.sh 0001-write-timeout conn.go stream.go`; ajusta la firma del script en la Task 1 si la hiciste de un solo archivo). Fila en `UPSTREAM.md`.

- [ ] **Step 4: Correr y commit**

Run: `go test ./internal/rtmpio/ -race -count=1` (incluye el test de integridad, que ahora exige el `.diff`); `(cd third_party/go-rtmp && go test -race -count=1 ./...)`; `go vet ./...`.

```bash
git add third_party internal/rtmpio
git commit -m "fix(rtmpio): plazo de escritura configurable en go-rtmp (parche 1) y 3 s en el publisher"
```

---

### Task 3: Parche 2 — `streams.At` bajo el candado (carrera con `-race`)

**Files:**
- Modify: `third_party/go-rtmp/streams.go`, `third_party/go-rtmp/streams_test.go`, `third_party/go-rtmp/UPSTREAM.md`
- Create: `third_party/go-rtmp/patches/0002-streams-at-lock.diff`

- [ ] **Step 1: Test en la copia (`streams_test.go`; testify ya está en su go.sum)**

```go
func TestStreamsAtIsSafeAgainstConcurrentDelete(t *testing.T) {
	c := newConn(&rwcMock{}, &ConnConfig{ControlState: StreamControlStateConfig{MaxMessageStreams: 64}})
	ss := c.streams
	var wg sync.WaitGroup
	for i := uint32(1); i < 32; i++ {
		id := i
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = ss.Create(id); _ = ss.Delete(id) }()
		go func() { defer wg.Done(); for j := 0; j < 100; j++ { _, _ = ss.At(id) } }()
	}
	wg.Wait()
}
```

(`rwcMock` y `newConn` ya existen en los tests de la copia —`conn_test.go`—; usa el doble que haya. El test solo tiene valor con `-race`: sin el parche, `go test -race -run TestStreamsAtIsSafe` reporta DATA RACE.)

- [ ] **Step 2: Correr con `-race` → DATA RACE.**

- [ ] **Step 3: Parche**

`streams.go`: `m sync.Mutex` → `m sync.RWMutex`; `At` toma `ss.m.RLock()`/`defer ss.m.RUnlock()`. Los demás métodos (`Create`, `CreateIfAvailable`, `Delete`) siguen con `Lock`. Diff `0002-streams-at-lock.diff` (archivos `streams.go` y `streams_test.go`); fila en `UPSTREAM.md`.

- [ ] **Step 4: Correr y commit**

Run: `(cd third_party/go-rtmp && go test -race -count=3 ./...)`; `go test ./internal/rtmpio/ -race -count=1`.

```bash
git add third_party
git commit -m "fix(go-rtmp): streams.At lee bajo el candado (parche 2)"
```

---

### Task 4: Parche 3 — stream de control accesible; `PreCommands` opcional en el publisher

**Files:**
- Modify: `third_party/go-rtmp/client_conn.go` (`ControlStream()`), `third_party/go-rtmp/UPSTREAM.md`, `internal/rtmpio/publisher.go` (`PublisherConfig.PreCommands`, envío por el stream de control, `FCUnpublish` al cerrar por el mismo stream), `internal/rtmpio/publisher_test.go`, `internal/rtmpio/ingest.go` (solo si hace falta exponer los comandos al test), `internal/config/config.go` + `config_test.go` (`RTMPPreCommands`), `cmd/splitstream/main.go` (cableado), `deploy/env.example`
- Create: `third_party/go-rtmp/patches/0003-control-stream.diff`

**Interfaces:**
- Produces: `func (cc *ClientConn) ControlStream() *Stream` (el stream 0, creado en `newClientConnWithSetup`); `rtmpio.PublisherConfig.PreCommands bool`; `config.Config.RTMPPreCommands bool` (`SPLITSTREAM_RTMP_PRECOMMANDS`, `parseBool`, por defecto `false`, en `LogValue` como `rtmp_precommands`).

- [ ] **Step 1: Test en `rtmpio`**

```go
// TestPreCommandsGoThroughTheControlStreamBeforeCreateStream: con PreCommands, el
// publisher manda releaseStream y FCPublish por el stream 0 ANTES de createStream, como
// FMLE/OBS (spec base §15.9, spec v1.0 §3.3); sin la opción, no manda ninguno.
func TestPreCommandsGoThroughTheControlStreamBeforeCreateStream(t *testing.T) {
	for _, caso := range []struct{ nombre string; pre bool; want []string }{
		{"con precomandos", true, []string{"connect", "releaseStream", "FCPublish", "createStream", "publish"}},
		{"sin precomandos", false, []string{"connect", "createStream", "publish"}},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			srv := servidorQueRegistraComandos(t) // Ingest con un handler que anota, en orden, cada comando y su stream id
			p, _ := NewPublisher(PublisherConfig{URL: srv.URL(), StreamKey: crypto.Secret("k"), ChunkSize: 4096, PreCommands: caso.pre})
			if err := p.Connect(context.Background()); err != nil { t.Fatal(err) }
			p.Close()
			got := srv.comandos() // nombres en orden
			if !reflect.DeepEqual(got, caso.want) { t.Errorf("comandos = %v, quería %v", got, caso.want) }
			for _, c := range srv.registro() { // {nombre, streamID}
				if (c.nombre == "releaseStream" || c.nombre == "FCPublish") && c.streamID != 0 {
					t.Errorf("%s llegó por el stream %d, quería 0", c.nombre, c.streamID)
				}
			}
		})
	}
}
```

(`servidorQueRegistraComandos`: el `Ingest` del paquete construye el `rtmp.ConnConfig` en `ingest.go:103` con un handler; los hooks `OnConnect`, `OnCreateStream`, `OnReleaseStream`, `OnFCPublish`, `OnFCUnpublish`, `OnPublish` de `rtmp.Handler` (`third_party/go-rtmp/handler.go:16-32`) dan el nombre; el stream id lo da el `*rtmp.Conn`/`StreamContext` —si `OnReleaseStream`/`OnFCPublish` no exponen el id, registra solo el orden y afirma el id únicamente para `publish` (stream ≠ 0); anótalo—. Si hace falta un hook nuevo en `IngestHandler` para los tests, añádelo como opcional (`interface{ OnCommand(nombre string, streamID uint32) }` comprobado con aserción de tipo) sin cambiar a los consumidores.)

- [ ] **Step 2: Correr → falla** (no compila: `PreCommands`/`ControlStream` no existen).

- [ ] **Step 3: Parche y publisher**

`client_conn.go`: guardar el stream de control en el struct (`ctrl *Stream`) en `newClientConnWithSetup` donde hoy solo se toma `ctrlStream.Write`, y `func (cc *ClientConn) ControlStream() *Stream { return cc.ctrl }` con comentario en inglés. Diff `0003-control-stream.diff`; fila en `UPSTREAM.md`. `publisher.go`: campo `preCommands bool` desde `PublisherConfig.PreCommands`; en `connect`, tras `conn.Connect(...)` y **antes** de `CreateStream`: `if p.preCommands { ctrl := conn.ControlStream(); p.writeCommand(ctrl, "releaseStream"); p.writeCommand(ctrl, "FCPublish") }` (un error aquí se registra a nivel debug y no es fatal: los destinos que no los esperan los ignoran); `FCUnpublish` al cerrar pasa a ir por el stream de control cuando `preCommands` (y sigue por el stream de datos cuando no, como hoy, para no cambiar el comportamiento por defecto). Reescribe el comentario largo de `publisher.go:306-330` para que cuente la historia completa: por qué se quitaron, por qué ahora existen detrás de una opción, y que la puerta real decide. `config.go`: `RTMPPreCommands` + test (`SPLITSTREAM_RTMP_PRECOMMANDS=true` → true; ausente → false; `LogValue`). `main.go`: pasa `PreCommands: cfg.RTMPPreCommands` donde se construye `rtmpio.PublisherConfig` (búscalo: la fábrica de sinks). `env.example`: `#SPLITSTREAM_RTMP_PRECOMMANDS=false` con dos líneas de explicación.

- [ ] **Step 4: Correr y commit**

Run: `go vet ./... && go test ./internal/rtmpio/ ./internal/config/ ./cmd/splitstream/ -race -count=1`; `(cd third_party/go-rtmp && go test -race -count=1 ./...)`; guards de frontera en local.

```bash
git add third_party internal/rtmpio internal/config cmd/splitstream deploy/env.example
git commit -m "feat(rtmpio): releaseStream/FCPublish por el stream de control, opcionales (parche 3)"
```

---

### Task 5: `golangci-lint` con configuración justificada y job `lint`

**Files:**
- Create: `.golangci.yml`
- Modify: `Makefile` (`lint`), `.github/workflows/ci.yml` (job `lint`), y los archivos de Go que el linter señale (arreglos reales, mínimos, sin cambiar comportamiento)

**Interfaces:**
- Produces: `make lint` = `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<versión fijada> run ./...`; job `lint` con `golangci/golangci-lint-action@v8` y la **misma** versión; `.golangci.yml` en formato v2.

- [ ] **Step 1: Elegir y fijar la versión**

`go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest version` para saber la última v2 disponible; fíjala en `Makefile` (`GOLANGCI := v2.X.Y`) y en el workflow (`version: v2.X.Y`). No entra en `go.mod` (`go run` con `@versión` no lo toca; comprueba `git diff go.mod go.sum` vacío).

- [ ] **Step 2: `.golangci.yml`**

```yaml
version: "2"

# Cada excepción lleva su porqué. Una excepción sin motivo escrito es un hallazgo.
run:
  timeout: 5m

linters:
  default: none
  enable:
    - errcheck
    - govet
    - staticcheck
    - gosec
    - revive
    - unused
    - ineffassign
    - misspell
    - gocritic
  settings:
    errcheck:
      # Cierres y flushes de mejor esfuerzo, y LogEvent: fuego y olvido por diseño (spec
      # base §6): un evento que no se pudo persistir no debe tumbar la operación que lo
      # generó. El resto de errores se comprueban.
      exclude-functions:
        - (io.Closer).Close
        - (*os.File).Close
        - (net.Conn).Close
        - (*net.TCPConn).Close
        - (http.Flusher).Flush
        - (*github.com/aprendomx/splitstream/internal/store.DB).LogEvent
    gosec:
      excludes:
        - G304 # rutas de archivo que vienen de la configuración del operador, no de la red
        - G404 # math/rand solo para jitter de backoff, nunca para secretos (crypto/rand)
    revive:
      rules:
        - name: exported
          disabled: true # los comentarios son en español y no empiezan por el nombre
        - name: var-naming
          arguments: [["ID", "URL", "RTMP", "TLS", "DTO"], []]
    misspell:
      locale: US
      ignore-rules: [] # solo se revisan identificadores y comentarios en inglés; añade palabras si hay falsos positivos en español
    gocritic:
      enabled-tags: [diagnostic]
  exclusions:
    paths:
      - third_party
      - web
    rules:
      # Los tests pueden ignorar errores de limpieza y usar valores fijos.
      - path: _test\.go
        linters: [errcheck, gosec]
```

Ajusta las rutas exactas de `exclude-functions` a las firmas reales (el formato es `(pkg.Tipo).Método`); si alguna no casa, el linter lo dice.

- [ ] **Step 3: Primer barrido y arreglos**

`make lint`. Clasifica: (a) errores reales (un `err` ignorado que sí importa, un `defer` con recurso sin cerrar, un `Printf` con verbo equivocado): arréglalos; (b) ruido justificado: excepción con comentario; (c) si el total supera lo que cabe en esta tarea con criterio (más de ~40 arreglos de código), deja el resto como excepciones **temporales** con fecha en `.golangci.yml` (`# TEMPORAL 2026-09-13: …`) y lista en el informe qué son. Ningún arreglo cambia comportamiento observable; `go test ./... -race -count=1` sigue en verde.

- [ ] **Step 4: Job `lint`**

```yaml
  lint:
    name: golangci-lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - uses: golangci/golangci-lint-action@v8
        with:
          version: v2.X.Y
```

`Makefile`: target `lint` con la misma versión y `.PHONY`.

- [ ] **Step 5: Comprobar y commit**

Run: `make lint` limpio; `go test ./... -race -count=1`; YAML válido.

```bash
git add .golangci.yml Makefile .github/workflows/ci.yml <archivos arreglados>
git commit -m "ci: golangci-lint con excepciones justificadas; arreglos que el linter destapó"
```

---

### Task 6: `govulncheck`, nocturna y Dependabot

**Files:**
- Create: `.github/workflows/nightly.yml`, `.github/dependabot.yml`, `deploy/nightly-smoke.sh`, `deploy/nightly_smoke_test.sh`
- Modify: `.github/workflows/ci.yml` (job `vuln`), `Makefile` (`vuln`)

**Interfaces:**
- Consumes: `deploy/migrate-test.sh` (Task 8; la nocturna lo llama: si la Task 8 aún no existe cuando esta corre, el paso queda escrito y fallará hasta que exista — el orden del plan lo evita).
- Produces: `make vuln` = `go run golang.org/x/vuln/cmd/govulncheck@vX.Y.Z ./...`; `nightly.yml`; `deploy/nightly-smoke.sh <plataforma> <clave> <binario>` (5 min contra una plataforma real).

- [ ] **Step 1: `vuln`**

Job en `ci.yml`: checkout, setup-go, `go run golang.org/x/vuln/cmd/govulncheck@vX.Y.Z ./...` (fija la última versión que `go run …@latest version` muestre). `Makefile`: `vuln`. Si hoy hay una vulnerabilidad alcanzable, la tarea no la esconde: se anota en el informe con el paquete y la versión que la arregla (subir la dependencia es una decisión del controlador, no de esta tarea).

- [ ] **Step 2: `deploy/nightly-smoke.sh`**

Bash con `set -euo pipefail` y `set +x` desde el principio (nunca imprime la clave): recibe `PLATAFORMA` (`twitch|youtube|kick`), la clave por variable de entorno `STREAM_KEY` (no por argumento: los argumentos se ven en `ps`), y el binario. Arranca el binario con `SPLITSTREAM_DB_PATH` temporal, `SPLITSTREAM_MASTER_KEY=$(binario -genkey)`, puertos libres; espera `/healthz`; completa el setup (`POST /api/setup` con el código que el binario imprime en su log —léelo del log— y una contraseña aleatoria); inicia sesión (`POST /api/auth/login`, cookie en un archivo temporal); crea el destino (`POST /api/destinations` con la URL de la plataforma del catálogo: `rtmp://a.rtmp.youtube.com/live2`, `rtmp://live.twitch.tv/app`, `rtmps://…` de Kick según `web/src/plataformas.js`) con la clave; lee `GET /api/ingest` para la URL y clave de ingesta; lanza `ffmpeg -re -f lavfi -i testsrc=size=1280x720:rate=30 -f lavfi -i sine -c:v libx264 -preset veryfast -b:v 2500k -g 60 -c:a aac -f flv rtmp://127.0.0.1:<rtmp>/<app>/<clave>` en segundo plano; durante 5 minutos, cada 15 s consulta `GET /api/status` y exige que el destino esté `connected`; cualquier `destination_disconnected` en `GET /api/events` → falla. Al final mata ffmpeg y el binario (SIGTERM) y borra el temporal. Sale 0/1 con un resumen de una línea. `deploy/nightly_smoke_test.sh`: comprueba con `bash -n` y `shellcheck`, y que el script se niega a arrancar sin `STREAM_KEY` (mensaje claro, exit 2) — patrón de `deploy/install_test.sh`.

- [ ] **Step 3: `nightly.yml`**

```yaml
name: Nocturna
on:
  schedule: [{ cron: '0 4 * * *' }]
  workflow_dispatch: {}
jobs:
  integracion:
    name: integración completa contra mediamtx
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: sudo apt-get update && sudo apt-get install -y ffmpeg
      - run: make sinks-up
      - run: make test-integration
      - if: always()
        run: make sinks-down
  migracion:
    name: migración desde la última release
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - uses: actions/setup-node@v4
        with: { node-version: 22, cache: npm, cache-dependency-path: web/package-lock.json }
      - run: make build
      - run: deploy/migrate-test.sh "$(gh release list --limit 1 --json tagName --jq '.[0].tagName')" ./splitstream
        env: { GH_TOKEN: '${{ github.token }}' }
  plataformas:
    name: humo contra plataformas reales
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - uses: actions/setup-node@v4
        with: { node-version: 22, cache: npm, cache-dependency-path: web/package-lock.json }
      - run: make build
      - run: sudo apt-get update && sudo apt-get install -y ffmpeg
      - name: twitch
        if: ${{ secrets.TWITCH_TEST_KEY != '' }}
        run: deploy/nightly-smoke.sh twitch ./splitstream
        env: { STREAM_KEY: '${{ secrets.TWITCH_TEST_KEY }}' }
      - name: youtube
        if: ${{ secrets.YOUTUBE_TEST_KEY != '' }}
        run: deploy/nightly-smoke.sh youtube ./splitstream
        env: { STREAM_KEY: '${{ secrets.YOUTUBE_TEST_KEY }}' }
      - name: kick
        if: ${{ secrets.KICK_TEST_KEY != '' }}
        run: deploy/nightly-smoke.sh kick ./splitstream
        env: { STREAM_KEY: '${{ secrets.KICK_TEST_KEY }}' }
      - name: sin claves
        if: ${{ secrets.TWITCH_TEST_KEY == '' && secrets.YOUTUBE_TEST_KEY == '' && secrets.KICK_TEST_KEY == '' }}
        run: echo "::warning::sin TWITCH_TEST_KEY/YOUTUBE_TEST_KEY/KICK_TEST_KEY; el humo contra plataformas se salta"
```

(GitHub no permite `secrets.*` en `if:` directamente en todos los contextos: si el validador se queja, calcula un `env: HAY_TWITCH: ${{ secrets.TWITCH_TEST_KEY != '' }}` a nivel de job y usa `if: env.HAY_TWITCH == 'true'`. Comprueba con `gh workflow view` tras el push, o con `actionlint` si está disponible vía `go run github.com/rhysd/actionlint/cmd/actionlint@latest`.) `make test-integration` ya existe; `make sinks-up/down` también.

- [ ] **Step 4: `dependabot.yml`**

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule: { interval: weekly }
    groups: { indirectas: { dependency-type: indirect } }
  - package-ecosystem: npm
    directory: /web
    schedule: { interval: weekly }
    ignore:
      - dependency-name: "quasar"
        update-types: ["version-update:semver-major"]
      - dependency-name: "vue"
        update-types: ["version-update:semver-major"]
  - package-ecosystem: github-actions
    directory: /
    schedule: { interval: weekly }
```

- [ ] **Step 5: Comprobar y commit**

Run: `make vuln`; `bash -n deploy/nightly-smoke.sh && shellcheck deploy/nightly-smoke.sh deploy/nightly_smoke_test.sh && deploy/nightly_smoke_test.sh`; YAML válido (`python3 -c 'import yaml,sys; [yaml.safe_load(open(f)) for f in sys.argv[1:]]' .github/workflows/*.yml .github/dependabot.yml`).

```bash
git add .github Makefile deploy/nightly-smoke.sh deploy/nightly_smoke_test.sh
git commit -m "ci: govulncheck, nocturna con integración, migración y humo opcional, y Dependabot"
```

---

### Task 7: Tabla de rutas y `docs/api.md` generado por test

**Files:**
- Modify: `internal/httpapi/server.go` (`routes()` recorre `rutas`), `internal/httpapi/dto_test.go` (usa `dtosDocumentados`)
- Create: `internal/httpapi/api_doc_test.go`, `docs/api.md`

**Interfaces:**
- Produces: `type ruta struct{ Metodo, Patron string; Handler http.HandlerFunc; Publica bool; Grupo, Resumen string }`; `func (s *Server) rutas() []ruta` (con los 57 registros actuales: 11 públicos y 46 protegidos, mismos patrones y handlers); `routes()` registra recorriendo `rutas()` (`Publica` → `s.mux.HandleFunc`; si no → `s.requireSession`; `GET /metrics` conserva `requireSessionOrToken`: campo `Metrics bool` o un `Envoltorio func(http.Handler) http.Handler` opcional en `ruta`); `var dtosDocumentados = []any{…}` en `api_doc_test.go` (la lista de `TestDTOFieldNamesAreSnakeCase` más los DTO de petición); `TestAPIContractDocIsCurrent` con flag `-update`.

- [ ] **Step 1: Test**

```go
var actualizar = flag.Bool("update", false, "reescribe docs/api.md con el contrato actual")

// TestAPIContractDocIsCurrent: docs/api.md es el contrato público y se genera desde la
// tabla de rutas y los DTO, así que no puede quedarse atrás: cambiar una ruta o un campo
// sin regenerarlo rompe este test (spec v1.0 §5.1). `go test ./internal/httpapi/ -run
// APIContract -update` lo reescribe.
func TestAPIContractDocIsCurrent(t *testing.T) {
	srv, _ := newTestServer(t)
	got := generarDocAPI(srv.rutas(), dtosDocumentados)
	ruta := filepath.Join(raizDelRepo(t), "docs", "api.md")
	if *actualizar {
		if err := os.WriteFile(ruta, []byte(got), 0o644); err != nil { t.Fatal(err) }
	}
	want, err := os.ReadFile(ruta)
	if err != nil { t.Fatalf("falta docs/api.md: genera con -update (%v)", err) }
	if string(want) != got {
		t.Errorf("docs/api.md no coincide con el contrato actual; regenera con -update.\n%s", diffLineas(string(want), got))
	}
}
```

`generarDocAPI`: cabecera fija (título, la regla de versionado del spec §5.4 en un párrafo, «generado por `TestAPIContractDocIsCurrent`; no editar a mano»), luego por `Grupo` (orden fijo: `auth, setup, health, metrics, ingest, destinations, live, platforms, accounts, sessions, recordings, chat, webhooks, backup, ws`) una tabla `| Método | Ruta | Sesión | Qué hace |` ordenada por ruta y método; luego «## Formas» con cada tipo de `dtosDocumentados` ordenado por nombre: `### nombreDelTipo` y una tabla `| Campo | Tipo | Opcional |` con el nombre JSON (sin `,omitempty`), el tipo Go legible (`string`, `int64`, `[]eventDTO`, `map[string]int`, `*time.Time` → «time (RFC 3339)», punteros → opcional). `diffLineas` imprime las primeras 20 líneas distintas. `raizDelRepo` ya existe en `internal/rtmpio` (Task 1): copia el helper (paquetes distintos; 6 líneas).

- [ ] **Step 2: Correr → falla (no compila: `rutas`, `ruta`, `dtosDocumentados`).**

- [ ] **Step 3: Tabla de rutas**

En `server.go`, sustituye el cuerpo de `routes()` por `for _, r := range s.rutas() { … }` más el bloque final del SPA (que queda como está). `rutas()` devuelve el slice con **los mismos** 57 registros, cada uno con `Grupo` y `Resumen` de una línea en español (por ejemplo `{"GET", "/api/sessions/{id}", s.handleSessionDetail, false, "sessions", "Ficha de una sesión con eventos, grabaciones y chat"}`). Los comentarios largos que hoy explican por qué una ruta es pública o por qué un patrón no compite con otro se conservan junto a su entrada. `GET /metrics`: `ruta.Envoltorio = s.requireSessionOrToken` (campo opcional; el bucle lo prefiere a `requireSession`). Un test de humo existente por ruta ya cubre que nada cambió; añade `TestRutasNoRepiten` (ningún `Metodo+Patron` duplicado) y `TestRutasPublicasSonLasDeSiempre` (la lista exacta de las 11 públicas, para que añadir una pública sea una decisión visible).

- [ ] **Step 4: Generar y commit**

Run: `go test ./internal/httpapi/ -run 'APIContract|Rutas|DTO' -update -count=1` y después sin `-update`; lee `docs/api.md` una vez entero (que sea legible para una persona); `go test ./internal/httpapi/ -race -count=1` completo (≈2 min); guard de `httpapi`.

```bash
git add internal/httpapi docs/api.md
git commit -m "feat(httpapi): tabla de rutas y contrato de API generado y verificado por test"
```

---

### Task 8: Política de migraciones y `deploy/migrate-test.sh`

**Files:**
- Create: `docs/migraciones.md`, `deploy/migrate-test.sh`, `deploy/migrate_test_test.sh`
- Modify: `.github/workflows/ci.yml` (el paso `shellcheck` ya cubre `deploy/*.sh`; añadir `deploy/migrate_test_test.sh` al paso de tests de scripts si existe uno —mira cómo corre `install_test.sh`—)

**Interfaces:**
- Produces: `deploy/migrate-test.sh <versión-anterior> <binario-nuevo>`; exit 0 si el binario nuevo arranca sobre la base creada por la versión anterior y `/healthz` responde; 1 si no; 2 si faltan argumentos o herramientas (`curl`, `tar`, `gh` o `curl` para descargar).

- [ ] **Step 1: `docs/migraciones.md`**

Secciones: «Reglas» (nunca editar una migración publicada; solo hacia delante, sin `down`; idempotencia con `IF NOT EXISTS` donde aplique; una transacción por migración; claves ajenas apagadas durante la migración y reactivadas después —`internal/store/db.go:151/176`—; `SchemaVersion` igual a la última migración, comprobado al arrancar —`db.go:212`—); «Cómo añadir una» (nombre `NNNN_descripcion.sql`, subir `SchemaVersion`, test de esquema, actualizar `docs/api.md` si cambia un DTO); «Cómo probarla contra una base real de la versión anterior» (`deploy/migrate-test.sh v0.13.0 ./splitstream`, qué hace y qué mira); «Si falla a medias» (SQLite deshace la transacción; el binario no arranca y dice qué versión esperaba; restaurar el respaldo `POST /api/backup` de antes de actualizar); «Compatibilidad hacia atrás» (una base migrada por una versión nueva no la abre una antigua: `SchemaVersion` mayor → error claro; por eso el respaldo previo).

- [ ] **Step 2: `deploy/migrate_test_test.sh`** (antes que el script, como `install_test.sh`): comprueba `bash -n` y `shellcheck`; sin argumentos → exit 2 y mensaje; con un binario inexistente → exit 2; y, si `SPLITSTREAM_MIGRATE_TEST_OFFLINE=1`, usa un «binario anterior» local (el mismo binario nuevo) para ejercitar el flujo entero sin descargar nada → exit 0.

- [ ] **Step 3: `deploy/migrate-test.sh`**

`set -euo pipefail`. Argumentos y comprobaciones (exit 2). Sistema: `uname -s/-m` → asset (`linux-x86_64`, `linux-arm64`, `macos-intel`, `macos-apple-silicon`; Windows fuera). Descarga: `gh release download "$VER" --repo aprendomx/splitstream --pattern "splitstream-$VER-$ASSET.tar.gz" --dir "$TMP"` si hay `gh`, si no `curl -fsSL https://github.com/aprendomx/splitstream/releases/download/$VER/…`; `tar -xzf`; el binario anterior es `$TMP/splitstream` (comprueba el nombre dentro del tar con `tar -tzf`). Con `SPLITSTREAM_MIGRATE_TEST_OFFLINE=1`, el «anterior» es una copia del nuevo. Función `arrancar BIN`: `SPLITSTREAM_DB_PATH=$TMP/datos/splitstream.db SPLITSTREAM_MASTER_KEY=$CLAVE SPLITSTREAM_HTTP_ADDR=127.0.0.1:$HTTP SPLITSTREAM_RTMP_ADDR=127.0.0.1:$RTMP "$BIN" >"$LOG" 2>&1 & echo $!`; `esperar_healthz` con 30 intentos de 1 s; `parar PID` con SIGTERM y espera de 10 s. Secuencia: clave con `$NUEVO -genkey`; arrancar anterior → healthz → parar; comprobar que la base existe; arrancar nuevo → healthz → parar; en el log del nuevo, buscar «migraci» (aplicó) o aceptar que no haya nada si `SchemaVersion` no cambió; comprobar que el nuevo arrancó sin «error» ni «panic» en el log. Salida: una línea `migración OK: <ver-anterior> → <versión del nuevo> (<n> migraciones)` o el log completo en fallo. Limpieza con `trap`.

- [ ] **Step 4: Comprobar y commit**

Run: `shellcheck deploy/migrate-test.sh deploy/migrate_test_test.sh && deploy/migrate_test_test.sh`; `make build-go && SPLITSTREAM_MIGRATE_TEST_OFFLINE=1 deploy/migrate-test.sh v0.13.0 ./splitstream` (offline); si hay red, también sin `OFFLINE` contra la v0.13.0 real (anótalo en el informe).

```bash
git add docs/migraciones.md deploy/migrate-test.sh deploy/migrate_test_test.sh .github/workflows/ci.yml
git commit -m "docs: política de migraciones y prueba de migración desde la release anterior"
```

---

### Task 9: Documentación de la v1.0

**Files:**
- Modify: `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (§5 dependencias: `third_party/go-rtmp`; §7 política de migraciones → `docs/migraciones.md`; §9 contrato y versionado → `docs/api.md`; §11 jobs `lint`/`vuln`/nocturna; §12 `SPLITSTREAM_RTMP_PRECOMMANDS`; §13 párrafo «Punto de extensión: subida al terminar» del spec v1.0 §6; §14 fila del riesgo de go-rtmp → mitigado con la copia parcheada), `README.md` y `README.es.md` («Status/Estado»: v1.0, qué significa; tabla de configuración con `SPLITSTREAM_RTMP_PRECOMMANDS`; «Development/Desarrollo»: `make lint`, `make vuln`, la nocturna, cómo regenerar `docs/api.md`, `third_party/go-rtmp/UPSTREAM.md`), `docs/manual-de-usuario.md` (§5 «Cuando un canal falla»: una nota sobre `SPLITSTREAM_RTMP_PRECOMMANDS` «solo si una plataforma lo pide»), `docs/lanzamiento.md` (cabecera v1.0.0 en español e inglés: qué cambia para quien actualiza —nada—, y qué garantiza), `deploy/env.example` (si la Task 4 no lo cerró)

- [ ] **Step 1: Spec base, README (ambos idiomas, misma estructura), manual, lanzamiento.** Sin promesas que el código no cumpla: `PreCommands` es opcional y sin puerta real; la copia de go-rtmp es v0.0.7 + tres parches; la nocturna solo prueba plataformas si hay claves.

- [ ] **Step 2: Comprobar y commit**

Run: `grep -n "SPLITSTREAM_RTMP_PRECOMMANDS" README.md README.es.md deploy/env.example internal/config/config.go` (los cuatro); `grep -c "^## \|^### " README.md README.es.md` iguales; `go build ./...`.

```bash
git add README.md README.es.md docs deploy/env.example
git commit -m "docs: v1.0 — contingencia de go-rtmp, CI estricta, contrato de API y migraciones"
```

---

## Autorrevisión

**Cobertura del spec.** §1/§2: Tasks 1 (copia + `replace` + integridad), 5–6 (CI), 7–8 (contrato y migraciones), 9 (spec base). §3.1: Task 2. §3.2: Task 3. §3.3: Task 4 (parche, `PreCommands`, `SPLITSTREAM_RTMP_PRECOMMANDS`, manual en Task 9). §3.4: Task 1 (`UPSTREAM.md`, `patches/`, `generar.sh`, test). §4.1: Task 5. §4.2: Task 6 (`vuln`). §4.3: Task 6 (`nightly.yml`, `nightly-smoke.sh`) + Task 8 (`migrate-test.sh`). §4.4: Task 6. §5.1: Task 7. §5.2–§5.3: Task 8. §5.4: Task 7 (cabecera de `docs/api.md`) + Task 9 (§9 del spec base). §6: Task 9 (§13). §7 pruebas: cada tarea; la validación de `nightly.yml` por `workflow_dispatch` y `migrate-test.sh` contra la v0.13.0 real las hace el controlador tras el push (anotado en la puerta). §8: nada lo contradice.

**Marcadores.** Sin «TBD». El código de los tests y de los parches va completo; los YAML van completos; la versión exacta de `golangci-lint` y `govulncheck` la fija el implementador con `@latest version` porque el plan no puede saberla sin red (documentado en Task 5 Step 1 y Task 6 Step 1).

**Consistencia de tipos.** `ConnConfig.WriteTimeout`/`Stream.WriteContext` (T2) ↔ `rtmpio.writeTimeout` (T2). `ClientConn.ControlStream()` (T4) ↔ `Publisher.connect` (T4). `PublisherConfig.PreCommands` (T4) ↔ `config.RTMPPreCommands` ↔ `main.go` (T4). `patches/generar.sh` (T1) ↔ Tasks 2–4. `raizDelRepo` (T1, copiado en T7). `ruta`/`rutas()`/`dtosDocumentados` (T7) ↔ `dto_test.go` (T7). `deploy/migrate-test.sh` (T8) ↔ `nightly.yml` (T6; el orden del plan hace que exista antes de la primera nocturna real).

**Riesgos.** (1) El barrido de `golangci-lint` puede ser grande: la Task 5 fija el criterio (arreglar lo real, excepciones temporales con fecha para el resto) y el controlador decide si algo se difiere. (2) `secrets.*` en `if:` de Actions: la Task 6 da la alternativa con `env`. (3) La prueba de integración `-race` de la copia necesita `testify` en CI: está en su `go.sum` y CI tiene red. (4) `PreCommands` sin puerta real: apagado por defecto y documentado como tal.
