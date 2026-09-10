# v0.10 «Que instalarlo no duela» — Plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Que Splitstream se instale con `brew`, `winget` o `curl | sh`, salga a internet con certificado propio o de Let's Encrypt, vea la IP real detrás de un proxy y avise cuando haya versión nueva, sin cambiar nada del comportamiento por defecto en un PC.

**Architecture:** Cuatro piezas independientes que no tocan el motor. TLS vive en un paquete nuevo `internal/webtls` (stdlib + `x/crypto/acme/autocert`) que `main.go` cablea sobre el `http.Server` existente; `internal/httpapi` solo recibe datos (`TLS`, `PublicURL`, `TrustedProxies`, `UpdateInfo`). La IP real se resuelve en `internal/httpapi/clientip.go` y la usan el limitador del login y `esLocal`. El aviso de versión es `internal/update` (solo stdlib) con un hook hacia el DTO, como `ExtraMetrics`. Los instaladores son plantillas en `deploy/` que `release.yml` rellena, valida y publica.

**Tech Stack:** Go 1.25 (`net/netip`, `crypto/tls`, `golang.org/x/crypto/acme/autocert`), Vue 3 + Quasar (banner), POSIX `sh` (instalador), GitHub Actions (jobs `tap`, `winget`, `ghcr`, `instaladores`), Docker buildx multi-arquitectura.

**Spec:** `docs/superpowers/specs/2026-09-10-instalacion-design.md` (sobre el spec base `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` y el plan maestro `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §5).

## Global Constraints

- **Comportamiento por defecto intacto**: sin `SPLITSTREAM_TLS_*`, sin `SPLITSTREAM_TRUSTED_PROXIES`, con `SPLITSTREAM_UPDATE_CHECK` por defecto y versión `dev`, el binario hace exactamente lo de la v0.9. `TestRunServesTheAPI` y `TestRunShutsDownTheHTTPServerOnSignal` siguen pasando sin tocarlos.
- **Cero dependencias nuevas**: `golang.org/x/crypto/acme/autocert` es un paquete del módulo `x/crypto` que ya está en `go.mod`. **`go mod tidy` no se ejecuta**; si `go.sum` necesita entradas nuevas, `go build ./...` las añade (`GOFLAGS=-mod=mod` solo si hace falta) y se revisan en el diff.
- **Fronteras (CI)**: `internal/relay` no importa go-rtmp, `database/sql`, `internal/store`, `internal/events` ni `internal/record`; `internal/httpapi` no importa go-rtmp, `internal/rtmpio` **ni `internal/webtls`** (guard nuevo); `internal/record` solo importa relay, flv y record; `internal/webtls` solo importa stdlib, `x/crypto` e `internal/config`; `internal/update` solo stdlib.
- **Secretos**: nada de claves, tokens ni contraseñas en logs, eventos, errores ni respuestas. `TAP_TOKEN` y `WINGET_TOKEN` solo viven en secretos de Actions y nunca se imprimen (`git clone` con el token en la URL va en un paso sin `set -x`).
- **Sin telemetría**: la consulta de versión manda `User-Agent: splitstream/<versión>` y `Accept`, y nada más.
- **`statusDTO` es el mismo tipo para `GET /api/status` y el WebSocket**; todo campo nuevo entra por ahí, en snake_case (`TestDTOFieldNamesAreSnakeCase` recibe los tipos nuevos).
- **Errores de la API con `{"error":{"code","message"}}`** y los códigos de `internal/httpapi/errors.go`.
- **Tests con `-race`**, estables con `GOMAXPROCS=2`; ninguno habla con internet (Let's Encrypt, GitHub): todo con `httptest` o certificados generados en el test.
- **Comentarios, commits, copys y errores en español**; los comentarios explican el porqué.
- **Scripts de shell POSIX** (`#!/bin/sh`), limpios con `shellcheck`; `install.sh` corre en `dash`, `ash` y `bash`.
- **Sin migraciones**: `SchemaVersion` sigue en 6.
- **Rama `feat/instalacion` desde `main`; nada se fusiona con la CI en rojo.** Commits con los trailers de la sesión.

---

### Task 1: Configuración de TLS, proxies de confianza y aviso de versión

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nada nuevo.
- Produces: `Config.TLSDomain, TLSCacheDir, TLSCertFile, TLSKeyFile, TLSRedirectAddr string`, `Config.TrustedProxies []netip.Prefix`, `Config.UpdateCheck bool`, `Config.SecureCookiesDesactivadas bool`, `func (c *Config) TLS() bool`. Variables: `SPLITSTREAM_TLS_DOMAIN`, `SPLITSTREAM_TLS_CACHE_DIR`, `SPLITSTREAM_TLS_CERT_FILE`, `SPLITSTREAM_TLS_KEY_FILE`, `SPLITSTREAM_TLS_REDIRECT_ADDR`, `SPLITSTREAM_TRUSTED_PROXIES`, `SPLITSTREAM_UPDATE_CHECK`. Con TLS, `HTTPAddr` por defecto `:443` y `SecureCookies` por defecto `true`.

- [ ] **Step 1: Escribir los tests que fallan**

Añadir al final de `internal/config/config_test.go`:

```go
func TestTLSIsOffByDefaultAndNothingElseChanges(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{"SPLITSTREAM_MASTER_KEY": testKeyB64()}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.TLS() {
		t.Fatal("sin variables de TLS, TLS() debería ser false")
	}
	if cfg.HTTPAddr != ":8080" || cfg.SecureCookies || cfg.TLSRedirectAddr != "" {
		t.Errorf("sin TLS cambió algo: http=%q secure=%v redirect=%q", cfg.HTTPAddr, cfg.SecureCookies, cfg.TLSRedirectAddr)
	}
	if !cfg.UpdateCheck {
		t.Error("UpdateCheck debería ser true por defecto")
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, quería vacío", cfg.TrustedProxies)
	}
}

func TestTLSDomainTurnsOnHTTPSDefaults(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(),
		"SPLITSTREAM_DB_PATH":    filepath.Join("datos", "s.db"),
		"SPLITSTREAM_TLS_DOMAIN": " relay.ejemplo.com ",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !cfg.TLS() || cfg.TLSDomain != "relay.ejemplo.com" {
		t.Errorf("TLSDomain = %q, TLS() = %v", cfg.TLSDomain, cfg.TLS())
	}
	if cfg.HTTPAddr != ":443" {
		t.Errorf("HTTPAddr = %q, con TLS el defecto es :443", cfg.HTTPAddr)
	}
	if !cfg.SecureCookies || cfg.SecureCookiesDesactivadas {
		t.Errorf("con TLS la cookie debería salir Secure: secure=%v desactivadas=%v", cfg.SecureCookies, cfg.SecureCookiesDesactivadas)
	}
	if cfg.TLSRedirectAddr != ":80" {
		t.Errorf("TLSRedirectAddr = %q, quería :80", cfg.TLSRedirectAddr)
	}
	if want := filepath.Join("datos", "tls-cache"); cfg.TLSCacheDir != want {
		t.Errorf("TLSCacheDir = %q, quería %q", cfg.TLSCacheDir, want)
	}
}

func TestTLSExplicitValuesAreHonoured(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY":        testKeyB64(),
		"SPLITSTREAM_TLS_CERT_FILE":     "/etc/ssl/relay.crt",
		"SPLITSTREAM_TLS_KEY_FILE":      "/etc/ssl/relay.key",
		"SPLITSTREAM_HTTP_ADDR":         ":8443",
		"SPLITSTREAM_SECURE_COOKIES":    "false",
		"SPLITSTREAM_TLS_REDIRECT_ADDR": "none",
		"SPLITSTREAM_TLS_CACHE_DIR":     "/var/cache/tls",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !cfg.TLS() || cfg.TLSDomain != "" {
		t.Error("con certificado propio TLS() debería ser true y TLSDomain vacío")
	}
	if cfg.HTTPAddr != ":8443" {
		t.Errorf("HTTPAddr = %q: un valor explícito se respeta", cfg.HTTPAddr)
	}
	if cfg.SecureCookies || !cfg.SecureCookiesDesactivadas {
		t.Errorf("SECURE_COOKIES=false explícito debe respetarse y marcarse: secure=%v desactivadas=%v", cfg.SecureCookies, cfg.SecureCookiesDesactivadas)
	}
	if cfg.TLSRedirectAddr != "" {
		t.Errorf("TLSRedirectAddr = %q, `none` debería dejarlo vacío", cfg.TLSRedirectAddr)
	}
	if cfg.TLSCacheDir != "/var/cache/tls" {
		t.Errorf("TLSCacheDir = %q", cfg.TLSCacheDir)
	}
}

func TestTLSRejectsContradictoryConfiguration(t *testing.T) {
	casos := map[string]map[string]string{
		"dominio y certificado a la vez": {"SPLITSTREAM_TLS_DOMAIN": "a.ejemplo.com", "SPLITSTREAM_TLS_CERT_FILE": "x.crt", "SPLITSTREAM_TLS_KEY_FILE": "x.key"},
		"certificado sin clave":          {"SPLITSTREAM_TLS_CERT_FILE": "x.crt"},
		"clave sin certificado":          {"SPLITSTREAM_TLS_KEY_FILE": "x.key"},
		"dominio con esquema":            {"SPLITSTREAM_TLS_DOMAIN": "https://a.ejemplo.com"},
		"dominio con puerto":             {"SPLITSTREAM_TLS_DOMAIN": "a.ejemplo.com:443"},
		"dominio sin punto":              {"SPLITSTREAM_TLS_DOMAIN": "localhost"},
	}
	for nombre, env := range casos {
		env["SPLITSTREAM_MASTER_KEY"] = testKeyB64()
		if _, err := config.LoadFrom(lookup(env)); err == nil {
			t.Errorf("%s: LoadFrom aceptó la configuración", nombre)
		}
	}
}

func TestTrustedProxiesParseCIDRsAndBareIPs(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY":      testKeyB64(),
		"SPLITSTREAM_TRUSTED_PROXIES": "127.0.0.1/32, ::1, 10.0.0.0/8 ,",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	got := make([]string, 0, len(cfg.TrustedProxies))
	for _, p := range cfg.TrustedProxies {
		got = append(got, p.String())
	}
	want := []string{"127.0.0.1/32", "::1/128", "10.0.0.0/8"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("TrustedProxies = %v, quería %v", got, want)
	}

	for _, malo := range []string{"casa", "10.0.0.0/33", "127.0.0.1,,300.1.1.1"} {
		_, err := config.LoadFrom(lookup(map[string]string{
			"SPLITSTREAM_MASTER_KEY": testKeyB64(), "SPLITSTREAM_TRUSTED_PROXIES": malo,
		}))
		if err == nil || !strings.Contains(err.Error(), "SPLITSTREAM_TRUSTED_PROXIES") {
			t.Errorf("%q: err = %v, quería un error que nombre la variable", malo, err)
		}
	}
}

func TestUpdateCheckCanBeTurnedOff(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(), "SPLITSTREAM_UPDATE_CHECK": "false",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.UpdateCheck {
		t.Error("UPDATE_CHECK=false debería apagarlo")
	}
}

func TestConfigLogValueShowsTLSButNoSecrets(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(), "SPLITSTREAM_TLS_DOMAIN": "a.ejemplo.com",
		"SPLITSTREAM_TRUSTED_PROXIES": "127.0.0.1",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("cfg", "config", cfg)
	for _, s := range []string{"tls_domain=a.ejemplo.com", "trusted_proxies=127.0.0.1/32", "update_check=true"} {
		if !strings.Contains(buf.String(), s) {
			t.Errorf("el log no lleva %q: %s", s, buf.String())
		}
	}
	if strings.Contains(buf.String(), testKeyB64()) {
		t.Error("el log lleva la master key")
	}
}
```

- [ ] **Step 2: Correr los tests y ver que fallan**

Run: `go test ./internal/config/ -run 'TLS|TrustedProxies|UpdateCheck' -count=1`
Expected: FAIL por compilación (`cfg.TLS undefined`, campos inexistentes).

- [ ] **Step 3: Implementar en `internal/config/config.go`**

Añadir `"net/netip"` a los imports. Campos nuevos al final de `Config` (después de `RecordingsDir`):

```go
	// TLSDomain enciende el TLS integrado con Let's Encrypt para ese dominio y solo ese.
	// Vacío: el binario sirve HTTP y el TLS, si lo hay, lo termina un proxy (spec §12).
	TLSDomain string
	// TLSCacheDir es donde autocert guarda la cuenta ACME y los certificados. Junto a la
	// base por la misma razón que la clave: lo que hay que respaldar va junto, y borrarlo
	// sin querer cuesta certificados (Let's Encrypt limita a 5 por semana por dominio).
	TLSCacheDir string
	// TLSCertFile y TLSKeyFile son un certificado propio en PEM. Excluyentes con TLSDomain.
	TLSCertFile string
	TLSKeyFile  string
	// TLSRedirectAddr es el listener HTTP que atiende el reto HTTP-01 y redirige a HTTPS.
	// Solo tiene sentido con TLS; vacío lo desactiva (`none` en el entorno).
	TLSRedirectAddr string
	// SecureCookiesDesactivadas es true cuando hay TLS integrado pero la persona puso
	// SPLITSTREAM_SECURE_COOKIES=false a mano. Se respeta —quizá prueba con IP y sin
	// nombre— pero se avisa en el log al arrancar.
	SecureCookiesDesactivadas bool
	// TrustedProxies son las redes desde las que se cree X-Forwarded-For. Vacía por
	// defecto: la cabecera la puede inventar quien llega directo, y el limitador del
	// login y el «local» del asistente dejarían de significar nada.
	TrustedProxies []netip.Prefix
	// UpdateCheck consulta la última release de GitHub al arrancar y cada 24 h. Solo el
	// aviso: nunca se actualiza solo. `SPLITSTREAM_UPDATE_CHECK=false` lo apaga.
	UpdateCheck bool
```

Método nuevo, debajo de `MarshalJSON`:

```go
// TLS dice si el binario termina TLS él mismo, sea con Let's Encrypt o con certificado
// propio. Es lo que decide el puerto por defecto, la cookie Secure y el listener de
// redirección.
func (c *Config) TLS() bool { return c.TLSDomain != "" || c.TLSCertFile != "" }
```

`LogValue` gana, después de `recordings_dir`:

```go
		slog.String("tls_domain", c.TLSDomain),
		slog.String("tls_cert_file", c.TLSCertFile),
		slog.String("tls_redirect_addr", c.TLSRedirectAddr),
		slog.String("trusted_proxies", prefijos(c.TrustedProxies)),
		slog.Bool("update_check", c.UpdateCheck),
```

y la función auxiliar:

```go
// prefijos imprime la lista para el log; vacía se ve como "" y no como "[]".
func prefijos(ps []netip.Prefix) string {
	partes := make([]string, len(ps))
	for i, p := range ps {
		partes[i] = p.String()
	}
	return strings.Join(partes, ",")
}
```

En `LoadFrom`, **sustituir** el bloque `cfg := &Config{ ... }` y la línea de `RecordingsDir` por:

```go
	// El TLS se lee antes que el resto porque cambia dos valores por defecto: el puerto
	// (:443 en vez de :8080) y la cookie Secure.
	tlsDomain := strings.TrimSpace(get("SPLITSTREAM_TLS_DOMAIN", ""))
	tlsCert := get("SPLITSTREAM_TLS_CERT_FILE", "")
	tlsKey := get("SPLITSTREAM_TLS_KEY_FILE", "")
	if err := validarTLS(tlsDomain, tlsCert, tlsKey); err != nil {
		return nil, err
	}
	conTLS := tlsDomain != "" || tlsCert != ""

	httpDef := ":8080"
	if conTLS {
		httpDef = ":443"
	}

	cfg := &Config{
		HTTPAddr: get("SPLITSTREAM_HTTP_ADDR", httpDef),
		RTMPAddr: get("SPLITSTREAM_RTMP_ADDR", ":1935"),
		DBPath:   get("SPLITSTREAM_DB_PATH", "splitstream.db"),

		MetricsToken: get("SPLITSTREAM_METRICS_TOKEN", ""),
		TLSDomain:    tlsDomain,
		TLSCertFile:  tlsCert,
		TLSKeyFile:   tlsKey,
		UpdateCheck:  get("SPLITSTREAM_UPDATE_CHECK", "true") != "false",
	}
	cfg.RecordingsDir = get("SPLITSTREAM_RECORDINGS_DIR", filepath.Join(filepath.Dir(cfg.DBPath), "recordings"))
	cfg.TLSCacheDir = get("SPLITSTREAM_TLS_CACHE_DIR", filepath.Join(filepath.Dir(cfg.DBPath), "tls-cache"))
	if conTLS {
		if v := get("SPLITSTREAM_TLS_REDIRECT_ADDR", ":80"); v != "none" {
			cfg.TLSRedirectAddr = v
		}
	}

	// Secure por defecto solo cuando el propio binario termina TLS; con proxy delante no
	// se puede adivinar y sigue siendo una decisión de quien despliega. Un `false`
	// explícito con TLS se respeta y se marca para avisar.
	if v, ok := lookup("SPLITSTREAM_SECURE_COOKIES"); ok && v != "" {
		cfg.SecureCookies = v == "true"
		cfg.SecureCookiesDesactivadas = conTLS && !cfg.SecureCookies
	} else {
		cfg.SecureCookies = conTLS
	}

	if cfg.TrustedProxies, err = parseProxies(get("SPLITSTREAM_TRUSTED_PROXIES", "")); err != nil {
		return nil, err
	}
```

(`err` ya está declarado más abajo en la función original con `level, err := ...`; cambiar esa línea a `level, err := parseLevel(...)` → `var err error` al principio de `LoadFrom` y `level, err = parseLevel(...)`, o declarar `var err error` antes del bloque de proxies y usar `:=` donde toque. Que compile sin sombras: `go vet` lo dirá.)

Funciones nuevas al final del archivo:

```go
// validarTLS rechaza las combinaciones que no pueden querer decir nada: o Let's Encrypt
// o certificado propio, y el certificado siempre con su clave. El dominio va sin
// esquema ni puerto porque autocert lo compara con el SNI tal cual.
func validarTLS(dominio, cert, key string) error {
	if dominio != "" && (cert != "" || key != "") {
		return errors.New("SPLITSTREAM_TLS_DOMAIN y SPLITSTREAM_TLS_CERT_FILE/SPLITSTREAM_TLS_KEY_FILE son excluyentes: " +
			"o Let's Encrypt o certificado propio")
	}
	if (cert == "") != (key == "") {
		return errors.New("SPLITSTREAM_TLS_CERT_FILE y SPLITSTREAM_TLS_KEY_FILE van juntas")
	}
	if dominio != "" && (strings.ContainsAny(dominio, " /:@") || !strings.Contains(dominio, ".")) {
		return fmt.Errorf("SPLITSTREAM_TLS_DOMAIN inválido %q: solo el nombre, sin esquema ni puerto "+
			"(por ejemplo relay.ejemplo.com)", dominio)
	}
	return nil
}

// parseProxies lee una lista separada por comas de CIDR o IP sueltas. Una IP suelta es
// su /32 (o /128): es lo que quiere decir quien escribe "127.0.0.1".
func parseProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, campo := range strings.Split(s, ",") {
		campo = strings.TrimSpace(campo)
		if campo == "" {
			continue
		}
		if p, err := netip.ParsePrefix(campo); err == nil {
			out = append(out, p.Masked())
			continue
		}
		if a, err := netip.ParseAddr(campo); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		return nil, fmt.Errorf("SPLITSTREAM_TRUSTED_PROXIES: %q no es una IP ni un CIDR", campo)
	}
	return out, nil
}
```

- [ ] **Step 4: Correr los tests del paquete**

Run: `go test ./internal/config/ -race -count=1`
Expected: PASS (incluidos los anteriores: `TestLoadFromAppliesDefaults` sigue viendo `:8080` y `SecureCookies=false`).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): TLS integrado, proxies de confianza y aviso de versión"
```

---

### Task 2: IP real detrás de un proxy: `clientIP`, `esLocal` y el limitador

**Files:**
- Create: `internal/httpapi/clientip.go`, `internal/httpapi/clientip_test.go`
- Modify: `internal/httpapi/server.go` (campo `proxies`, `Config.TrustedProxies`), `internal/httpapi/auth.go` (limitador con purga y llamada a `s.clientIP`), `internal/httpapi/setup.go` (`s.esLocal`, `s.clientIP`), `deploy/env.example`
- Test: `internal/httpapi/auth_test.go` (purga del limitador)

**Interfaces:**
- Consumes: `config.Config.TrustedProxies []netip.Prefix` (Task 1).
- Produces: `httpapi.Config.TrustedProxies []netip.Prefix`; `func (s *Server) clientIP(r *http.Request) netip.Addr`; `func (s *Server) esLocal(r *http.Request) bool`. Las funciones libres `clientIP(r)` y `esLocal(r)` desaparecen.

- [ ] **Step 1: Escribir `internal/httpapi/clientip_test.go`**

```go
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func servidorConProxies(t *testing.T, cidrs ...string) *Server {
	t.Helper()
	s := &Server{}
	for _, c := range cidrs {
		s.proxies = append(s.proxies, netip.MustParsePrefix(c))
	}
	return s
}

func peticion(remota, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/setup", nil)
	r.RemoteAddr = remota
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// Sin proxies de confianza la cabecera se ignora: es la política de siempre, y la que
// protege a quien expone el puerto directamente.
func TestClientIPIgnoresForwardedForWithoutTrustedProxies(t *testing.T) {
	s := servidorConProxies(t)
	got := s.clientIP(peticion("127.0.0.1:5555", "203.0.113.9"))
	if got.String() != "127.0.0.1" {
		t.Errorf("clientIP = %s, quería 127.0.0.1", got)
	}
	if !s.esLocal(peticion("127.0.0.1:5555", "203.0.113.9")) {
		t.Error("loopback con cabecera inventada debería seguir siendo local sin proxies")
	}
}

func TestClientIPTrustsTheHeaderOnlyFromATrustedProxy(t *testing.T) {
	s := servidorConProxies(t, "127.0.0.1/32")
	r := peticion("127.0.0.1:5555", "203.0.113.9")
	if got := s.clientIP(r); got.String() != "203.0.113.9" {
		t.Errorf("clientIP = %s, quería 203.0.113.9", got)
	}
	if s.esLocal(r) {
		t.Error("una IP pública detrás del proxy no es local")
	}
	// Desde otra máquina, aunque traiga cabecera, no se le cree.
	if got := s.clientIP(peticion("198.51.100.4:1", "127.0.0.1")); got.String() != "198.51.100.4" {
		t.Errorf("clientIP = %s: la cabecera de un origen sin confianza se ignora", got)
	}
}

// Cadena de dos proxies: el último salto es de confianza, el anterior también, y la
// primera dirección que no lo es —de derecha a izquierda— es el cliente.
func TestClientIPWalksTheChainFromTheRight(t *testing.T) {
	s := servidorConProxies(t, "10.0.0.0/8", "127.0.0.1/32")
	got := s.clientIP(peticion("127.0.0.1:9", "198.51.100.7, 203.0.113.9, 10.1.2.3"))
	if got.String() != "203.0.113.9" {
		t.Errorf("clientIP = %s, quería 203.0.113.9 (198.51.100.7 lo puso el cliente y no vale)", got)
	}
	// Varias cabeceras X-Forwarded-For cuentan como una sola cadena.
	r := peticion("127.0.0.1:9", "")
	r.Header.Add("X-Forwarded-For", "203.0.113.9")
	r.Header.Add("X-Forwarded-For", "10.1.2.3")
	if got := s.clientIP(r); got.String() != "203.0.113.9" {
		t.Errorf("clientIP = %s con cabeceras repetidas", got)
	}
}

func TestClientIPFallsBackToTheProxyOnGarbageOrAllTrusted(t *testing.T) {
	s := servidorConProxies(t, "127.0.0.1/32")
	if got := s.clientIP(peticion("127.0.0.1:9", "no-es-una-ip")); got.String() != "127.0.0.1" {
		t.Errorf("basura en la cabecera: clientIP = %s, quería la del proxy", got)
	}
	if got := s.clientIP(peticion("127.0.0.1:9", "127.0.0.1")); got.String() != "127.0.0.1" {
		t.Errorf("todo de confianza: clientIP = %s, quería la del proxy", got)
	}
	if got := s.clientIP(peticion("127.0.0.1:9", "")); got.String() != "127.0.0.1" {
		t.Errorf("sin cabecera: clientIP = %s", got)
	}
}

func TestClientIPHandlesIPv6AndMappedAddresses(t *testing.T) {
	s := servidorConProxies(t, "::1/128")
	if got := s.clientIP(peticion("[::1]:9", "2001:db8::5")); got.String() != "2001:db8::5" {
		t.Errorf("clientIP = %s, quería 2001:db8::5", got)
	}
	// Una IPv4 mapeada en IPv6 se compara como IPv4.
	s = servidorConProxies(t, "127.0.0.1/32")
	if got := s.clientIP(peticion("[::ffff:127.0.0.1]:9", "203.0.113.9")); got.String() != "203.0.113.9" {
		t.Errorf("clientIP = %s con RemoteAddr mapeada", got)
	}
	if !s.esLocal(peticion("[::1]:9", "")) {
		t.Error("::1 es loopback")
	}
	// Un RemoteAddr que no parsea no es local ni de confianza.
	if s.esLocal(peticion("pipe", "")) {
		t.Error("un RemoteAddr sin IP no puede ser local")
	}
}
```

- [ ] **Step 2: Escribir el test de la purga del limitador**

Añadir a `internal/httpapi/auth_test.go` (comprobar los imports que ya tenga: hace falta `time`):

```go
// El mapa del limitador no puede crecer para siempre: con IPs efímeras, cada intento
// fallido dejaría una entrada eterna. Las que llevan 10 minutos calladas se van.
func TestLoginLimiterForgetsIdleAddresses(t *testing.T) {
	l := newLoginLimiter()
	ahora := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return ahora }

	l.allow("203.0.113.1")
	l.allow("203.0.113.2")
	if len(l.por) != 2 {
		t.Fatalf("entradas = %d, quería 2", len(l.por))
	}
	ahora = ahora.Add(11 * time.Minute)
	l.allow("203.0.113.3")
	if len(l.por) != 1 {
		t.Errorf("entradas = %d tras 11 min, quería solo la nueva", len(l.por))
	}
	// Una IP que sigue intentándolo no se olvida (y con ello tampoco su cuenta).
	for i := 0; i < 6; i++ {
		l.allow("203.0.113.3")
	}
	ahora = ahora.Add(9 * time.Minute)
	if l.allow("203.0.113.3") {
		t.Error("a los 9 minutos la IP que agotó la ráfaga sigue limitada")
	}
}
```

(`rate.Limiter` mide su propio tiempo con `time.Now`; el test solo controla la purga. La última aserción funciona porque en 9 minutos de reloj real no pasa nada: el limitador ve el mismo instante en las dos llamadas.)

> Nota: la última aserción depende de que `rate.Limiter` haya agotado la ráfaga en tiempo real: seis `allow` seguidos consumen los 5 tokens y el sexto y el séptimo fallan. Es determinista.

- [ ] **Step 3: Correr y ver que fallan**

Run: `go test ./internal/httpapi/ -run 'ClientIP|LoginLimiter' -count=1`
Expected: FAIL por compilación (`s.proxies`, `s.clientIP`, `l.now` no existen).

- [ ] **Step 4: Implementar `internal/httpapi/clientip.go`**

```go
package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP es la dirección desde la que llega la petición, tal como deben verla el
// limitador del login y el asistente del primer arranque.
//
// Por defecto es RemoteAddr y nada más: X-Forwarded-For la puede inventar quien llega
// directo, y entonces ni el límite limitaría ni «local» querría decir nada. Solo cuando
// RemoteAddr es uno de los proxies de confianza de la configuración se cree la cabecera,
// y aun así con cuidado: se recorre de derecha a izquierda —la derecha la escribió el
// proxy más cercano, la izquierda la trajo el cliente— saltando los proxies conocidos y
// quedándose con la primera dirección que no lo es. Si la cabecera trae basura, o todo
// lo que hay es de confianza, se devuelve la del proxy: es la opción conservadora, que
// nunca es local y agrupa en un solo cubo del limitador lo que venga mal formado.
func (s *Server) clientIP(r *http.Request) netip.Addr {
	remota := addrDe(r.RemoteAddr)
	if !remota.IsValid() || !s.esProxyDeConfianza(remota) {
		return remota
	}
	var cadena []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		cadena = append(cadena, strings.Split(v, ",")...)
	}
	for i := len(cadena) - 1; i >= 0; i-- {
		a := addrDe(strings.TrimSpace(cadena[i]))
		if !a.IsValid() {
			return remota
		}
		if !s.esProxyDeConfianza(a) {
			return a
		}
	}
	return remota
}

// esLocal dice si la petición viene de la propia máquina. Es lo que decide si el
// asistente del primer arranque pide el código de la consola: quien está en el teclado ya
// controla el equipo; quien llega por la red tiene que demostrar que puede leerla.
func (s *Server) esLocal(r *http.Request) bool {
	return s.clientIP(r).IsLoopback()
}

func (s *Server) esProxyDeConfianza(a netip.Addr) bool {
	for _, p := range s.proxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// addrDe saca la IP de "host:puerto" o de una IP sola. Unmap para que ::ffff:127.0.0.1
// sea 127.0.0.1 y caiga en los prefijos IPv4.
func addrDe(s string) netip.Addr {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}
```

- [ ] **Step 5: Cablear en `server.go`, `auth.go` y `setup.go`**

`server.go`: en `Config`, después de `SecureCookies`:

```go
	// TrustedProxies son las redes desde las que se cree X-Forwarded-For (ver clientip.go).
	TrustedProxies []netip.Prefix
```

En `Server`, un campo `proxies []netip.Prefix`; en `New`, `proxies: cfg.TrustedProxies`. Añadir `"net/netip"` a los imports. **Borrar** la función libre `clientIP` de `server.go` (líneas ~260-268) y su comentario.

`auth.go`: sustituir el tipo `loginLimiter`, `newLoginLimiter` y `allow` por:

```go
// loginLimiter acota los intentos de login por IP.
//
// Sin esto, la contraseña del panel se puede probar a fuerza bruta tan rápido como aguante
// el argon2id, que es lento pero no infinitamente. El límite es generoso a propósito: debe
// molestar a un script, no a alguien que se equivoca dos veces al escribir.
type loginLimiter struct {
	mu  sync.Mutex
	por map[string]*intentos
	// now se sustituye en los tests para ejercitar la purga sin esperar 10 minutos.
	now func() time.Time
}

type intentos struct {
	lim    *rate.Limiter
	ultimo time.Time
}

// limiterOlvido es cuánto tiempo sin intentos hace falta para soltar la entrada de una IP.
// Con IPs efímeras el mapa crecería para siempre; a los 10 minutos el limitador ya habría
// repuesto la ráfaga entera, así que olvidar no regala nada.
const limiterOlvido = 10 * time.Minute

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{por: make(map[string]*intentos), now: time.Now}
}

// allow consume un intento. Ráfaga de 5 y reposición de uno cada 10 s. Purga en el propio
// camino, sin goroutine: el mapa tiene una entrada por IP que lo intentó en los últimos
// diez minutos, que en este servicio son un puñado.
func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	ahora := l.now()
	for k, e := range l.por {
		if ahora.Sub(e.ultimo) > limiterOlvido {
			delete(l.por, k)
		}
	}
	e, ok := l.por[ip]
	if !ok {
		e = &intentos{lim: rate.NewLimiter(rate.Every(10*time.Second), 5)}
		l.por[ip] = e
	}
	e.ultimo = ahora
	return e.lim.Allow()
}
```

En `handleLogin`: `if !s.limiter.allow(s.clientIP(r).String()) {`.

`setup.go`: **borrar** la función libre `esLocal` y su comentario; en `handleSetupEstado` `local := s.esLocal(r)`; en `handleSetup` `s.limiter.allow(s.clientIP(r).String())` y `if !s.esLocal(r) {`. Buscar otros usos: `grep -rn "esLocal(\|clientIP(" internal/httpapi` y dejar todos como métodos.

- [ ] **Step 6: `deploy/env.example`**

Añadir después del bloque de `SECURE_COOKIES`:

```
# Redes desde las que se cree la cabecera X-Forwarded-For (CIDR o IP, separadas por comas).
# Solo si el panel va detrás de Caddy, nginx o un proxy de Docker en la misma máquina:
# sin esto el limitador del login castigaría a todos por uno y el asistente vería todo
# como remoto. 0.0.0.0/0 anula el limitador: no lo pongas.
#SPLITSTREAM_TRUSTED_PROXIES=127.0.0.1/32,::1/128
```

- [ ] **Step 7: Correr el paquete entero**

Run: `go test ./internal/httpapi/ -race -count=1`
Expected: PASS. Si algún test existente construye `&Server{}` a mano y llama a `esLocal(...)` como función libre, pasarlo al método.

- [ ] **Step 8: Commit**

```bash
git add internal/httpapi/clientip.go internal/httpapi/clientip_test.go internal/httpapi/server.go internal/httpapi/auth.go internal/httpapi/auth_test.go internal/httpapi/setup.go deploy/env.example
git commit -m "feat(httpapi): IP real detrás de proxies de confianza y purga del limitador"
```

---
### Task 3: `internal/webtls`: certificado de Let's Encrypt o propio, y la redirección

**Files:**
- Create: `internal/webtls/webtls.go`, `internal/webtls/webtls_test.go`

**Interfaces:**
- Consumes: `config.Config` (`TLSDomain`, `TLSCacheDir`, `TLSCertFile`, `TLSKeyFile`, `HTTPAddr`) de la Task 1.
- Produces: `type Setup struct { TLSConfig *tls.Config; Redirect http.Handler; PublicURL string }`, `func Build(cfg *config.Config, onError func(error)) (*Setup, error)` (devuelve `nil, nil` sin TLS). `onError` se llama como máximo una vez cada 10 minutos, solo con fallos de obtener el certificado del propio dominio.

- [ ] **Step 1: Escribir `internal/webtls/webtls_test.go`**

```go
package webtls_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/config"
	"github.com/aprendomx/splitstream/internal/webtls"
)

// certificadoDePrueba escribe un certificado autofirmado para 127.0.0.1 y devuelve las
// rutas y el pool con el que un cliente puede validarlo.
func certificadoDePrueba(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "splitstream-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "relay.crt")
	keyFile = filepath.Join(dir, "relay.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

func TestBuildReturnsNothingWithoutTLS(t *testing.T) {
	s, err := webtls.Build(&config.Config{HTTPAddr: ":8080"}, nil)
	if err != nil || s != nil {
		t.Fatalf("Build = %v, %v; sin TLS quería nil, nil", s, err)
	}
}

func TestOwnCertificateServesHTTPS(t *testing.T) {
	certFile, keyFile, pool := certificadoDePrueba(t)
	s, err := webtls.Build(&config.Config{HTTPAddr: ":443", TLSCertFile: certFile, TLSKeyFile: keyFile}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if s.PublicURL != "" {
		t.Errorf("PublicURL = %q; con certificado propio no se conoce el nombre público", s.PublicURL)
	}
	if s.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, quería TLS 1.2", s.TLSConfig.MinVersion)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hola"))
	}))
	srv.TLS = s.TLSConfig
	srv.StartTLS()
	defer srv.Close()

	cliente := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	resp, err := cliente.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET por HTTPS con nuestro certificado: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("código = %d", resp.StatusCode)
	}
}

func TestOwnCertificateRejectsABrokenPair(t *testing.T) {
	certFile, _, _ := certificadoDePrueba(t)
	_, keyFile, _ := certificadoDePrueba(t)
	if _, err := webtls.Build(&config.Config{HTTPAddr: ":443", TLSCertFile: certFile, TLSKeyFile: keyFile}, nil); err == nil {
		t.Fatal("Build aceptó un certificado con la clave de otro")
	}
}

func TestRedirectSendsToHTTPSKeepingPathAndQuery(t *testing.T) {
	certFile, keyFile, _ := certificadoDePrueba(t)
	casos := []struct{ addr, host, want string }{
		{":443", "relay.ejemplo.com", "https://relay.ejemplo.com/api/status?x=1"},
		{":443", "relay.ejemplo.com:80", "https://relay.ejemplo.com/api/status?x=1"},
		{":8443", "relay.ejemplo.com", "https://relay.ejemplo.com:8443/api/status?x=1"},
	}
	for _, c := range casos {
		s, err := webtls.Build(&config.Config{HTTPAddr: c.addr, TLSCertFile: certFile, TLSKeyFile: keyFile}, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		r := httptest.NewRequest(http.MethodGet, "http://"+c.host+"/api/status?x=1", nil)
		r.Host = c.host
		w := httptest.NewRecorder()
		s.Redirect.ServeHTTP(w, r)
		if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != c.want {
			t.Errorf("addr %s host %s: %d %q, quería 301 %q", c.addr, c.host, w.Code, w.Header().Get("Location"), c.want)
		}
	}
}

// Con dominio no se habla con Let's Encrypt en el test: se comprueba la forma del
// TLSConfig (GetCertificate, ALPN del reto) y que el listener de redirección sigue
// redirigiendo lo que no es un reto.
func TestDomainConfiguresAutocertWithoutTalkingToAnyone(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "tls-cache")
	s, err := webtls.Build(&config.Config{HTTPAddr: ":443", TLSDomain: "relay.ejemplo.com", TLSCacheDir: cache}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if s.PublicURL != "https://relay.ejemplo.com" {
		t.Errorf("PublicURL = %q", s.PublicURL)
	}
	if s.TLSConfig.GetCertificate == nil {
		t.Fatal("con dominio, GetCertificate debe venir de autocert")
	}
	if !slices.Contains(s.TLSConfig.NextProtos, "acme-tls/1") {
		t.Errorf("NextProtos = %v, falta acme-tls/1 (reto TLS-ALPN-01)", s.TLSConfig.NextProtos)
	}
	r := httptest.NewRequest(http.MethodGet, "http://relay.ejemplo.com/panel", nil)
	r.Host = "relay.ejemplo.com"
	w := httptest.NewRecorder()
	s.Redirect.ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "https://relay.ejemplo.com/panel" {
		t.Errorf("redirección: %d %q", w.Code, w.Header().Get("Location"))
	}
}

// Un ClientHello para otro nombre no debe avisar (los escáneres lo hacen todo el día); un
// fallo del propio dominio sí, y no más de una vez cada diez minutos.
func TestCertificateErrorsAreReportedOncePerTenMinutesForOurDomainOnly(t *testing.T) {
	var avisos []error
	s, err := webtls.Build(&config.Config{HTTPAddr: ":443", TLSDomain: "relay.ejemplo.com",
		TLSCacheDir: filepath.Join(t.TempDir(), "c")}, func(err error) { avisos = append(avisos, err) })
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// autocert.HostWhitelist rechaza cualquier otro nombre sin salir a la red.
	if _, err := s.TLSConfig.GetCertificate(&tls.ClientHelloInfo{ServerName: "otro.ejemplo.com"}); err == nil {
		t.Fatal("otro nombre debería fallar")
	}
	if len(avisos) != 0 {
		t.Fatalf("un nombre ajeno avisó: %v", avisos)
	}
	// Para el propio dominio, con un contexto ya cancelado, autocert falla antes de
	// llegar a la red. Dos fallos seguidos: un solo aviso.
	hello := &tls.ClientHelloInfo{ServerName: "relay.ejemplo.com", Conn: nil}
	for i := 0; i < 2; i++ {
		if _, err := s.TLSConfig.GetCertificate(hello); err == nil {
			t.Fatal("sin red ni cuenta, obtener el certificado debería fallar")
		}
	}
	if len(avisos) != 1 {
		t.Fatalf("avisos = %d, quería 1", len(avisos))
	}
	if errors.Is(avisos[0], nil) || avisos[0].Error() == "" {
		t.Error("el aviso debería llevar el error de ACME")
	}
}
```

> Si el último test resulta lento porque autocert intenta de verdad hablar con Let's Encrypt antes de fallar (sin red, el `Dial` puede tardar), fijar en el test `s.TLSConfig.GetCertificate` con un `hello.Context()` cancelado no es posible (`ClientHelloInfo.Context` viene de la conexión). En ese caso el implementador reduce la aserción a: nombre ajeno → sin aviso; y prueba la cadencia con el avisador aparte (`webtls.NewAvisador` exportado solo para tests vía `export_test.go`). Anotarlo en el reporte.

- [ ] **Step 2: Correr y ver que falla**

Run: `go test ./internal/webtls/ -count=1`
Expected: FAIL (paquete inexistente).

- [ ] **Step 3: Implementar `internal/webtls/webtls.go`**

```go
// Package webtls construye el TLS que el propio binario termina: con Let's Encrypt para
// un dominio (autocert, que ya viene en el módulo x/crypto del proyecto) o con un
// certificado propio. Es un paquete aparte para que httpapi siga sin saber nada del
// transporte y para que main.go solo tenga que enchufar un *tls.Config y un handler.
package webtls

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/aprendomx/splitstream/internal/config"
)

// Setup es lo que main.go necesita: la configuración TLS del servidor del panel, el
// handler del listener HTTP (reto HTTP-01 más redirección) y la URL pública si se sabe.
type Setup struct {
	TLSConfig *tls.Config
	Redirect  http.Handler
	// PublicURL es "https://<dominio>" con Let's Encrypt; vacía con certificado propio,
	// porque el binario no sabe con qué nombre lo alcanzan.
	PublicURL string
}

// avisoCadaMax es la cadencia máxima de onError: un dominio mal apuntado recibe un
// ClientHello por cada intento del navegador y del propio ACME, y avisar de cada uno
// convertiría el log en un torrente.
const avisoCadaMax = 10 * time.Minute

// Build devuelve nil, nil cuando la configuración no pide TLS. onError puede ser nil.
func Build(cfg *config.Config, onError func(error)) (*Setup, error) {
	switch {
	case cfg.TLSDomain != "" && cfg.TLSCertFile != "":
		return nil, errors.New("webtls: dominio y certificado propio a la vez")
	case cfg.TLSDomain != "":
		return conLetsEncrypt(cfg, onError), nil
	case cfg.TLSCertFile != "":
		return conCertificadoPropio(cfg)
	default:
		return nil, nil
	}
}

func conLetsEncrypt(cfg *config.Config, onError func(error)) *Setup {
	m := &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		// Solo este nombre: sin lista blanca, cualquiera que apunte un dominio a esta IP
		// haría que el servicio pidiera certificados en su nombre hasta agotar el cupo.
		HostPolicy: autocert.HostWhitelist(cfg.TLSDomain),
		Cache:      autocert.DirCache(cfg.TLSCacheDir),
	}
	tc := m.TLSConfig() // trae GetCertificate y acme-tls/1 en NextProtos
	tc.MinVersion = tls.VersionTLS12

	if onError != nil {
		interno := tc.GetCertificate
		avisar := avisador(onError)
		dominio := cfg.TLSDomain
		tc.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, err := interno(hello)
			// Solo los fallos del propio dominio interesan: los escáneres piden nombres
			// ajenos todo el día y HostWhitelist los rechaza sin salir a la red.
			if err != nil && hello.ServerName == dominio {
				avisar(err)
			}
			return cert, err
		}
	}

	return &Setup{
		TLSConfig: tc,
		// HTTPHandler atiende /.well-known/acme-challenge/ y delega el resto en nuestro
		// redirector, que sabe el puerto HTTPS (el de autocert supone 443).
		Redirect:  m.HTTPHandler(redirigirAHTTPS(puertoDe(cfg.HTTPAddr))),
		PublicURL: "https://" + cfg.TLSDomain,
	}
}

func conCertificadoPropio(cfg *config.Config) (*Setup, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("cargar el certificado de SPLITSTREAM_TLS_CERT_FILE: %w", err)
	}
	return &Setup{
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		Redirect:  redirigirAHTTPS(puertoDe(cfg.HTTPAddr)),
	}, nil
}

// avisador devuelve una función que reenvía a fn como máximo una vez por avisoCadaMax.
func avisador(fn func(error)) func(error) {
	var mu sync.Mutex
	var ultimo time.Time
	return func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if ahora := time.Now(); ahora.Sub(ultimo) >= avisoCadaMax {
			ultimo = ahora
			fn(err)
		}
	}
}

// redirigirAHTTPS manda un 301 a la misma ruta por HTTPS. Se conserva el puerto solo si
// no es el 443: "https://host:443" funciona pero queda feo en la barra del navegador.
func redirigirAHTTPS(puerto string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if puerto != "" && puerto != "443" {
			host = net.JoinHostPort(host, puerto)
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

func puertoDe(addr string) string {
	_, puerto, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return puerto
}
```

- [ ] **Step 4: Correr los tests**

Run: `go test ./internal/webtls/ -race -count=1`
Expected: PASS. Si `go build` pide entradas de `go.sum` para `acme/autocert`, correr `go build ./...` (no `go mod tidy`) y revisar que `go.mod` no cambió de directas.

- [ ] **Step 5: Commit**

```bash
git add internal/webtls go.sum
git commit -m "feat(webtls): TLS del propio binario con Let's Encrypt o certificado propio"
```

---

### Task 4: Cablear el TLS en `main.go`, `-healthcheck`, `statusDTO.panel` y los despliegues

**Files:**
- Modify: `cmd/splitstream/main.go`, `cmd/splitstream/main_test.go`
- Modify: `internal/httpapi/server.go` (`Config.TLS`, `Config.PublicURL`, campos), `internal/httpapi/dto.go` (`panelDTO`), `internal/httpapi/status.go`, `internal/httpapi/dto_test.go`
- Modify: `deploy/splitstream.service`, `deploy/docker-compose.yml`, `.github/workflows/ci.yml` (guard `httpapi` ↛ `webtls`)

**Interfaces:**
- Consumes: `webtls.Build` (Task 3), `config.Config.TLS()`, `TLSRedirectAddr`, `SecureCookiesDesactivadas` (Task 1).
- Produces: `httpapi.Config.TLS bool`, `httpapi.Config.PublicURL string`; `statusDTO.Panel panelDTO` (`json:"panel"`, campos `tls`, `public_url`); `healthcheckURL(addr string, tls bool) string`; `healthcheck(addr string, tls bool) error`.

- [ ] **Step 1: Tests en `internal/httpapi`**

En `dto.go`, debajo de `recordingStatusDTO` (o donde estén los DTO de estado):

```go
// panelDTO dice cómo se sirve el panel: si el propio binario termina TLS y con qué URL
// pública. Lo necesita el chat de Kick (v0.12) para saber si hay dirección HTTPS que dar
// a un webhook. Con un proxy delante `tls` es false aunque el navegador vea HTTPS: es el
// TLS del binario, no el del proxy.
type panelDTO struct {
	TLS       bool   `json:"tls"`
	PublicURL string `json:"public_url"`
}
```

En `statusDTO`, después de `Recording`: `Panel panelDTO `json:"panel"``. En `dto_test.go`, añadir `panelDTO{}` a la lista de `TestDTOFieldNamesAreSnakeCase`. En `status.go`, junto a `out.Version = s.version`: `out.Panel = panelDTO{TLS: s.tls, PublicURL: s.publicURL}`.

Test nuevo en `internal/httpapi/status_test.go` (o el archivo donde vivan los tests de `/api/status`; usar el helper de servidor autenticado que ya exista allí, p. ej. el que usan los tests de `recording`):

```go
func TestStatusReportsHowThePanelIsServed(t *testing.T) {
	srv, cliente := servidorAutenticado(t, func(c *Config) { c.TLS = true; c.PublicURL = "https://relay.ejemplo.com" })
	var dto statusDTO
	getJSON(t, cliente, srv.URL+"/api/status", &dto)
	if !dto.Panel.TLS || dto.Panel.PublicURL != "https://relay.ejemplo.com" {
		t.Errorf("panel = %+v", dto.Panel)
	}
}
```

Adaptar `servidorAutenticado`/`getJSON` a los helpers reales del paquete (leer `recording_test.go` para ver cómo se construye un servidor con sesión). En `server.go`: `Config` gana

```go
	// TLS y PublicURL describen cómo se sirve el panel (ver panelDTO). Son datos: este
	// paquete no termina TLS ni importa webtls.
	TLS       bool
	PublicURL string
```

y `Server` los campos `tls bool`, `publicURL string`, copiados en `New`.

Run: `go test ./internal/httpapi/ -run 'Status|SnakeCase' -race -count=1` → PASS.

- [ ] **Step 2: Tests en `cmd/splitstream/main_test.go`**

Cambiar las llamadas existentes `healthcheckURL(x)` → `healthcheckURL(x, false)` y `healthcheck(x)` → `healthcheck(x, false)` en `TestHealthcheckFollowsHealthz` y `TestHealthcheckURLFor`, y añadir:

```go
func TestHealthcheckURLUsesHTTPSWithTLS(t *testing.T) {
	if got := healthcheckURL(":443", true); got != "https://127.0.0.1:443/healthz" {
		t.Errorf("healthcheckURL con TLS = %q", got)
	}
	if got := healthcheckURL("", true); got != "https://127.0.0.1:443/healthz" {
		t.Errorf("sin puerto y con TLS = %q, el defecto es 443", got)
	}
}

// certificadoAutofirmado escribe un par para 127.0.0.1 y devuelve las rutas.
func certificadoAutofirmado(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	cert, clave := filepath.Join(dir, "c.crt"), filepath.Join(dir, "c.key")
	os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(clave, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return cert, clave
}

// Con certificado propio: el panel responde por HTTPS, la cookie de sesión sale Secure
// sin que nadie ponga SPLITSTREAM_SECURE_COOKIES, el listener de redirección manda a
// HTTPS y -healthcheck funciona. Sin TLS, TestRunServesTheAPI es la regresión.
func TestRunWithOwnCertificateServesHTTPS(t *testing.T) {
	cert, clave := certificadoAutofirmado(t)
	t.Setenv("SPLITSTREAM_TLS_CERT_FILE", cert)
	t.Setenv("SPLITSTREAM_TLS_KEY_FILE", clave)
	redirAddr := freeAddr(t)
	t.Setenv("SPLITSTREAM_TLS_REDIRECT_ADDR", redirAddr)

	addr, cancel, hecho := arrancaRun(t, io.Discard)
	defer cancel()

	cliente := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var resp *http.Response
	var err error
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = cliente.Get("https://" + addr + "/api/setup")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("el panel nunca respondió por HTTPS en %s: %v", addr, err)
	}
	resp.Body.Close()

	// Configurar la contraseña (desde loopback: sin código) y entrar: la cookie es Secure.
	resp, err = cliente.Post("https://"+addr+"/api/setup", "application/json",
		strings.NewReader(`{"password":"contraseña-larga-1"}`))
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	resp.Body.Close()
	resp, err = cliente.Post("https://"+addr+"/api/auth/login", "application/json",
		strings.NewReader(`{"password":"contraseña-larga-1"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	var secure bool
	for _, c := range resp.Cookies() {
		if c.Name == "splitstream_session" {
			secure = c.Secure
		}
	}
	if !secure {
		t.Error("con TLS integrado la cookie de sesión debería salir Secure")
	}

	// Redirección.
	resp, err = cliente.Get("http://" + redirAddr + "/api/status?x=1")
	if err != nil {
		t.Fatalf("listener de redirección: %v", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusMovedPermanently || !strings.HasPrefix(loc, "https://") || !strings.HasSuffix(loc, "/api/status?x=1") {
		t.Errorf("redirección = %d %q", resp.StatusCode, loc)
	}

	if err := healthcheck(addr, true); err != nil {
		t.Errorf("healthcheck con TLS: %v", err)
	}

	cancel()
	select {
	case err := <-hecho:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run no volvió tras cancelar con TLS")
	}
	// Los dos puertos quedan libres.
	for _, a := range []string{addr, redirAddr} {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			t.Errorf("%s sigue ocupado: %v", a, err)
			continue
		}
		ln.Close()
	}
}
```

Imports nuevos en `main_test.go`: `crypto/ecdsa`, `crypto/elliptic`, `crypto/rand`, `crypto/tls`, `crypto/x509`, `crypto/x509/pkix`, `encoding/pem`, `math/big`, `os`. Comprobar el código que devuelve el login (`grep -n "StatusNoContent\|StatusOK" internal/httpapi/auth.go`) y dejar una sola comparación. Comprobar también la longitud mínima de contraseña (`minPasswordLen` en `main.go`) y que la de prueba la cumple.

Run: `go test ./cmd/splitstream/ -run 'Healthcheck|OwnCertificate' -count=1` → FAIL por compilación.

- [ ] **Step 3: Implementar en `main.go`**

Imports: añadir `"crypto/tls"` y `"github.com/aprendomx/splitstream/internal/webtls"`.

`-healthcheck` en `main()`:

```go
	if *hc {
		// Solo el puerto y si hay TLS: config.Load crearía un archivo de clave si no lo
		// hubiera, y un healthcheck no debe tener efectos secundarios.
		addr := os.Getenv("SPLITSTREAM_HTTP_ADDR")
		conTLS := os.Getenv("SPLITSTREAM_TLS_DOMAIN") != "" || os.Getenv("SPLITSTREAM_TLS_CERT_FILE") != ""
		if err := healthcheck(addr, conTLS); err != nil {
			fmt.Fprintln(os.Stderr, "healthcheck:", err)
			os.Exit(1)
		}
		return
	}
```

`healthcheckURL` y `healthcheck`:

```go
func healthcheckURL(addr string, conTLS bool) string {
	esquema, puertoDef := "http", "8080"
	if conTLS {
		esquema, puertoDef = "https", "443"
	}
	_, puerto, err := net.SplitHostPort(addr)
	if err != nil || puerto == "" {
		puerto = puertoDef
	}
	return esquema + "://127.0.0.1:" + puerto + "/healthz"
}

func healthcheck(addr string, conTLS bool) error {
	client := &http.Client{Timeout: 3 * time.Second}
	if conTLS {
		// Es loopback y solo mide vida: el certificado es del dominio, nunca de
		// 127.0.0.1, así que validarlo fallaría siempre.
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	resp, err := client.Get(healthcheckURL(addr, conTLS))
	// ... igual que hoy
```

En `run()`, antes de construir `httpapi.Config`:

```go
	tlsSetup, err := webtls.Build(cfg, func(err error) {
		// Sin secretos: el dominio y el texto de ACME. Cadencia acotada por webtls.
		if _, e := db.LogEvent(context.Background(), store.Event{
			Level: store.LevelError, Kind: "tls_certificate_error",
			Message: "no se pudo obtener el certificado de " + cfg.TLSDomain + ": " + err.Error(),
		}); e != nil {
			logger.Error("no se pudo registrar el fallo de certificado", "err", e)
		}
	})
	if err != nil {
		return err
	}
	if cfg.SecureCookiesDesactivadas {
		logger.Warn("SPLITSTREAM_SECURE_COOKIES=false con TLS integrado: la cookie de sesión viaja sin Secure")
	}
	publicURL := ""
	if tlsSetup != nil {
		publicURL = tlsSetup.PublicURL
	}
```

En `httpapi.Config`: `TrustedProxies: cfg.TrustedProxies, TLS: cfg.TLS(), PublicURL: publicURL,`.

El servidor HTTP:

```go
	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	var redirSrv *http.Server
	if tlsSetup != nil {
		httpSrv.TLSConfig = tlsSetup.TLSConfig
		if cfg.TLSRedirectAddr != "" {
			// Mínimo: atiende el reto HTTP-01 y redirige. Sin Handler propio no hay nada
			// más que pueda hacer, así que el plazo es corto.
			redirSrv = &http.Server{Addr: cfg.TLSRedirectAddr, Handler: tlsSetup.Redirect, ReadHeaderTimeout: 5 * time.Second}
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if tlsSetup != nil {
			// Con TLSConfig puesto, los archivos vacíos son correctos: el certificado
			// sale de GetCertificate (autocert) o de Certificates (propio).
			err = httpSrv.ListenAndServeTLS("", "")
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("el servidor HTTP dejó de atender", "err", err)
		}
	}()
	if redirSrv != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := redirSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("el listener de redirección a HTTPS dejó de atender", "err", err)
			}
		}()
	}
```

Y en el apagado, justo después del `httpSrv.Shutdown`:

```go
	if redirSrv != nil {
		redirCtx, cancelRedir := context.WithTimeout(context.Background(), 2*time.Second)
		if err := redirSrv.Shutdown(redirCtx); err != nil {
			logger.Warn("el listener de redirección no cerró limpiamente", "err", err)
		}
		cancelRedir()
	}
```

El log de arranque gana `"tls", cfg.TLS()` y, con dominio, `"public_url", publicURL`.

- [ ] **Step 4: Despliegues y CI**

`deploy/splitstream.service`, después de `RestrictAddressFamilies=…`:

```
# Para el TLS integrado (SPLITSTREAM_TLS_DOMAIN) el servicio escucha en 443 y 80, que un
# usuario sin privilegios no puede abrir. Esta capacidad se lo permite sin darle root:
#AmbientCapabilities=CAP_NET_BIND_SERVICE
```

Comprobar que `ReadWritePaths=/var/lib/splitstream` cubre `tls-cache/` (vive junto a la base): sí, sin cambios. Añadir al comentario de cabecera: «Con TLS integrado, descomenta AmbientCapabilities y pon SPLITSTREAM_TLS_DOMAIN en /etc/splitstream/env.»

`deploy/docker-compose.yml`, en `ports`:

```yaml
      # TLS integrado: descomenta estos dos, pon SPLITSTREAM_TLS_DOMAIN en .env y quita
      # la línea de 127.0.0.1:8080. Dentro del contenedor no hace falta root para 80 y
      # 443: Docker permite puertos bajos a cualquier usuario desde la 20.10.
      #- "80:80"
      #- "443:443"
```

`.github/workflows/ci.yml`, el paso «internal/httpapi no conoce go-rtmp» añade el guard de `webtls` (mismo `go list -deps`, un `grep` más: `internal/webtls`). Renombrar el paso a «internal/httpapi no conoce go-rtmp ni el TLS».

- [ ] **Step 5: Correr todo lo tocado**

Run: `go vet ./... && go test ./cmd/splitstream/ ./internal/httpapi/ -race -count=1`
Expected: PASS. `TestRunServesTheAPI` y `TestRunShutsDownTheHTTPServerOnSignal` intactos.

- [ ] **Step 6: Commit**

```bash
git add cmd/splitstream internal/httpapi deploy/splitstream.service deploy/docker-compose.yml .github/workflows/ci.yml
git commit -m "feat: TLS integrado en el binario, -healthcheck por HTTPS y panel.tls en el estado"
```

---
### Task 5: `internal/update`: consultar la última release y comparar versiones

**Files:**
- Create: `internal/update/check.go`, `internal/update/check_test.go`

**Interfaces:**
- Consumes: nada del proyecto (solo stdlib).
- Produces: `type Info struct { Latest, URL string; Available bool }`; `type Checker struct { Current, URL string; Client *http.Client; Logger *slog.Logger }` con `func (c *Checker) Latest() Info`, `func (c *Checker) Run(ctx context.Context, initialDelay, interval time.Duration, onNew func(Info))`, `func (c *Checker) Check(ctx context.Context) (Info, error)`; `func ParseVersion(s string) ([3]int, bool)`.

- [ ] **Step 1: Escribir `internal/update/check_test.go`**

```go
package update_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/update"
)

func TestParseVersion(t *testing.T) {
	casos := map[string]struct {
		v  [3]int
		ok bool
	}{
		"v0.10.0":       {[3]int{0, 10, 0}, true},
		"1.2.3":         {[3]int{1, 2, 3}, true},
		"v0.10.0-dirty": {[3]int{}, false},
		"v0.11.0-rc1":   {[3]int{}, false},
		"dev":           {[3]int{}, false},
		"docker":        {[3]int{}, false},
		"v1.2":          {[3]int{}, false},
		"":              {[3]int{}, false},
	}
	for in, want := range casos {
		v, ok := update.ParseVersion(in)
		if ok != want.ok || v != want.v {
			t.Errorf("ParseVersion(%q) = %v, %v; quería %v, %v", in, v, ok, want.v, want.ok)
		}
	}
}

// servidorDeReleases responde como api.github.com y cuenta las peticiones.
func servidorDeReleases(t *testing.T, cuerpo string, codigo int, llamadas *atomic.Int32, cabeceras *http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas.Add(1)
		if cabeceras != nil {
			*cabeceras = r.Header.Clone()
		}
		w.WriteHeader(codigo)
		w.Write([]byte(cuerpo))
	}))
}

func TestCheckComparesAgainstTheLatestTag(t *testing.T) {
	casos := []struct {
		actual, tag string
		disponible  bool
	}{
		{"v0.9.0", "v0.10.0", true},
		{"v0.10.0", "v0.10.0", false},
		{"v0.11.0", "v0.10.0", false},
		{"v0.9.9", "v0.10.0", true},
		{"v0.9.0", "v0.10.0-rc1", false}, // una pre-release no cuenta
		{"v0.9.0", "raro", false},
	}
	for _, c := range casos {
		var n atomic.Int32
		srv := servidorDeReleases(t, `{"tag_name":"`+c.tag+`","html_url":"https://ejemplo/r/`+c.tag+`"}`, 200, &n, nil)
		chk := &update.Checker{Current: c.actual, URL: srv.URL, Client: srv.Client()}
		info, err := chk.Check(context.Background())
		srv.Close()
		if err != nil {
			t.Errorf("%s vs %s: %v", c.actual, c.tag, err)
			continue
		}
		if info.Available != c.disponible {
			t.Errorf("%s vs %s: available = %v, quería %v", c.actual, c.tag, info.Available, c.disponible)
		}
		if c.disponible && (info.Latest != c.tag || info.URL != "https://ejemplo/r/"+c.tag) {
			t.Errorf("%s vs %s: info = %+v", c.actual, c.tag, info)
		}
	}
}

func TestCheckSendsOnlyTheVersionAndNothingElse(t *testing.T) {
	var n atomic.Int32
	var h http.Header
	srv := servidorDeReleases(t, `{"tag_name":"v0.10.0","html_url":"u"}`, 200, &n, &h)
	defer srv.Close()
	chk := &update.Checker{Current: "v0.9.0", URL: srv.URL, Client: srv.Client()}
	if _, err := chk.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ua := h.Get("User-Agent"); ua != "splitstream/v0.9.0" {
		t.Errorf("User-Agent = %q", ua)
	}
	for k := range h {
		switch k {
		case "User-Agent", "Accept", "Accept-Encoding":
		default:
			t.Errorf("cabecera de más: %s (sin telemetría quiere decir sin identificadores)", k)
		}
	}
}

func TestCheckFailsSoftlyOnErrors(t *testing.T) {
	var n atomic.Int32
	for _, c := range []struct {
		cuerpo string
		codigo int
	}{
		{`{"message":"API rate limit exceeded"}`, 403},
		{`no es json`, 200},
		{`{"tag_name":123}`, 200},
	} {
		srv := servidorDeReleases(t, c.cuerpo, c.codigo, &n, nil)
		chk := &update.Checker{Current: "v0.9.0", URL: srv.URL, Client: srv.Client()}
		info, err := chk.Check(context.Background())
		srv.Close()
		if err == nil {
			t.Errorf("%d %q: Check no devolvió error", c.codigo, c.cuerpo)
		}
		if info.Available {
			t.Errorf("%d %q: un fallo no puede anunciar versión", c.codigo, c.cuerpo)
		}
	}
	// Timeout: el servidor no contesta nunca dentro del plazo.
	lento := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer lento.Close()
	chk := &update.Checker{Current: "v0.9.0", URL: lento.URL, Client: lento.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := chk.Check(ctx); err == nil {
		t.Error("un servidor colgado debería dar error, no bloquear")
	}
}

func TestRunDoesNothingForAnUnversionedBinary(t *testing.T) {
	var n atomic.Int32
	srv := servidorDeReleases(t, `{"tag_name":"v9.9.9","html_url":"u"}`, 200, &n, nil)
	defer srv.Close()
	chk := &update.Checker{Current: "dev", URL: srv.URL, Client: srv.Client(), Logger: slog.Default()}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	chk.Run(ctx, 0, time.Millisecond, func(update.Info) { t.Error("un binario dev no compara") })
	if n.Load() != 0 {
		t.Errorf("peticiones = %d, quería 0", n.Load())
	}
	if chk.Latest().Available {
		t.Error("Latest no puede anunciar nada")
	}
}

func TestRunNotifiesOncePerNewVersionAndRespectsTheContext(t *testing.T) {
	var n atomic.Int32
	srv := servidorDeReleases(t, `{"tag_name":"v0.10.0","html_url":"https://ejemplo/r/v0.10.0"}`, 200, &n, nil)
	defer srv.Close()
	chk := &update.Checker{Current: "v0.9.0", URL: srv.URL, Client: srv.Client(), Logger: slog.Default()}

	var avisos atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	hecho := make(chan struct{})
	go func() {
		chk.Run(ctx, 0, 20*time.Millisecond, func(i update.Info) {
			avisos.Add(1)
			if i.Latest != "v0.10.0" {
				t.Errorf("aviso con %+v", i)
			}
		})
		close(hecho)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for n.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-hecho:
	case <-time.After(2 * time.Second):
		t.Fatal("Run no volvió al cancelar el contexto")
	}
	if n.Load() < 3 {
		t.Fatalf("peticiones = %d, el intervalo no se respetó", n.Load())
	}
	if avisos.Load() != 1 {
		t.Errorf("avisos = %d, quería 1 (la misma versión no se anuncia dos veces)", avisos.Load())
	}
	if got := chk.Latest(); !got.Available || got.URL != "https://ejemplo/r/v0.10.0" {
		t.Errorf("Latest = %+v", got)
	}
}
```

- [ ] **Step 2: Correr y ver que falla**

Run: `go test ./internal/update/ -count=1`
Expected: FAIL (paquete inexistente).

- [ ] **Step 3: Implementar `internal/update/check.go`**

```go
// Package update consulta si hay una versión más nueva publicada. Solo avisa: nunca
// descarga ni instala nada, y no manda más que la versión que corre en el User-Agent.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LatestURL es la release más reciente del repositorio. La API pública de GitHub no
// necesita token para esto; su cuota (60 por hora por IP) sobra para una consulta al día.
const LatestURL = "https://api.github.com/repos/aprendomx/splitstream/releases/latest"

// Info es lo que sabe el checker de la última release.
type Info struct {
	Latest    string
	URL       string
	Available bool
}

// Checker compara la versión del binario con la última etiqueta publicada.
type Checker struct {
	// Current es la versión del binario. Si no es vX.Y.Z (dev, docker, -dirty), el
	// checker no consulta nada: no hay con qué comparar y avisar sería ruido.
	Current string
	// URL sustituye a LatestURL en los tests.
	URL    string
	Client *http.Client
	Logger *slog.Logger

	mu   sync.Mutex
	info Info
}

// Latest devuelve el último resultado. Vacío hasta la primera consulta con éxito.
func (c *Checker) Latest() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// Run consulta tras initialDelay y luego cada interval, hasta que ctx termine. onNew se
// llama la primera vez que se ve una versión nueva y cada vez que esa versión cambie.
// Ningún fallo sale de aquí: se loguea a debug y se reintenta en la siguiente vuelta.
func (c *Checker) Run(ctx context.Context, initialDelay, interval time.Duration, onNew func(Info)) {
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if _, ok := ParseVersion(c.Current); !ok {
		logger.Debug("aviso de versión desactivado: el binario no lleva versión", "version", c.Current)
		return
	}

	t := time.NewTimer(initialDelay)
	defer t.Stop()
	var anunciada string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		info, err := c.Check(ctx)
		if err != nil {
			logger.Debug("no se pudo consultar la última versión", "err", err)
		} else if info.Available && info.Latest != anunciada && onNew != nil {
			anunciada = info.Latest
			onNew(info)
		}
		t.Reset(interval)
	}
}

// Check hace una sola consulta. Exportado para probarlo sin esperas.
func (c *Checker) Check(ctx context.Context) (Info, error) {
	actual, ok := ParseVersion(c.Current)
	if !ok {
		return Info{}, fmt.Errorf("la versión del binario %q no es comparable", c.Current)
	}
	url := c.URL
	if url == "" {
		url = LatestURL
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Info{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// La versión, y nada más: es lo único que hace falta para que quien mire los logs de
	// GitHub sepa qué versiones hay en uso, y no identifica a nadie.
	req.Header.Set("User-Agent", "splitstream/"+c.Current)

	resp, err := client.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("GitHub respondió %d", resp.StatusCode)
	}
	var cuerpo struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&cuerpo); err != nil {
		return Info{}, fmt.Errorf("respuesta inesperada: %w", err)
	}
	ultima, ok := ParseVersion(cuerpo.TagName)
	if !ok {
		// Una pre-release o una etiqueta rara no es una versión que anunciar.
		return Info{}, errors.New("la última etiqueta no es una versión vX.Y.Z: " + cuerpo.TagName)
	}
	info := Info{Latest: cuerpo.TagName, URL: cuerpo.HTMLURL, Available: newer(ultima, actual)}
	c.mu.Lock()
	c.info = info
	c.mu.Unlock()
	return info, nil
}

// ParseVersion lee "vX.Y.Z" o "X.Y.Z" en tres enteros. Cualquier sufijo (-rc1, -dirty)
// lo hace incomparable a propósito: no es una release.
func ParseVersion(s string) ([3]int, bool) {
	var v [3]int
	partes := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(partes) != 3 {
		return v, false
	}
	for i, p := range partes {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
```

- [ ] **Step 4: Correr los tests**

Run: `go test ./internal/update/ -race -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/update
git commit -m "feat(update): consulta de la última release y comparación de versiones"
```

---

### Task 6: Cablear el aviso: evento, `statusDTO.update` y banner del panel

**Files:**
- Modify: `cmd/splitstream/main.go`, `internal/httpapi/server.go` (`Config.UpdateInfo`, tipo `UpdateStatus`), `internal/httpapi/dto.go` (`updateDTO`), `internal/httpapi/status.go`, `internal/httpapi/dto_test.go`
- Modify: `web/src/stores/panel.js` (getter `actualizacion`), `web/src/App.vue` (banner)
- Test: `internal/httpapi/status_test.go` (o donde viva el test de la Task 4)

**Interfaces:**
- Consumes: `update.Checker` (Task 5), `config.Config.UpdateCheck` (Task 1), `db.LogEvent`.
- Produces: `httpapi.UpdateStatus{Latest, URL string; Available bool}`, `httpapi.Config.UpdateInfo func() UpdateStatus`; `statusDTO.Update updateDTO` (`json:"update"`, campos `available`, `latest`, `url`); evento `update_available`; getter `panel.actualizacion`.

- [ ] **Step 1: Test del DTO**

En `dto.go`:

```go
// updateDTO es el aviso de versión nueva (spec v0.10 §6). Solo el aviso: el panel enseña
// un enlace y nada se actualiza solo.
type updateDTO struct {
	Available bool   `json:"available"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
}
```

`statusDTO` gana `Update updateDTO `json:"update"`` después de `Panel`. `TestDTOFieldNamesAreSnakeCase` recibe `updateDTO{}`. En `server.go`:

```go
// UpdateStatus es lo que el checker de versiones sabe; este paquete no lo importa, lo
// recibe como función igual que ExtraMetrics.
type UpdateStatus struct {
	Latest    string
	URL       string
	Available bool
}
```

y en `Config`: `UpdateInfo func() UpdateStatus` (nil: sin aviso). `Server` guarda `updateInfo`. En `status.go`:

```go
	if s.updateInfo != nil {
		u := s.updateInfo()
		out.Update = updateDTO{Available: u.Available, Latest: u.Latest, URL: u.URL}
	}
```

Test junto al de la Task 4:

```go
func TestStatusCarriesTheUpdateNotice(t *testing.T) {
	srv, cliente := servidorAutenticado(t, func(c *Config) {
		c.UpdateInfo = func() UpdateStatus { return UpdateStatus{Latest: "v0.11.0", URL: "https://r", Available: true} }
	})
	var dto statusDTO
	getJSON(t, cliente, srv.URL+"/api/status", &dto)
	if !dto.Update.Available || dto.Update.Latest != "v0.11.0" || dto.Update.URL != "https://r" {
		t.Errorf("update = %+v", dto.Update)
	}
}
```

Run: `go test ./internal/httpapi/ -run 'Status|SnakeCase' -race -count=1` → PASS.

- [ ] **Step 2: `main.go`**

Import `"github.com/aprendomx/splitstream/internal/update"`. Después del bloque del `maintenance.Scheduler` (donde ya existe `fondo`):

```go
	// Aviso de versión: una consulta a GitHub 30 s después de arrancar y luego cada 24 h.
	// Va en `fondo` para que el apagado la espere como a los webhooks; Run vuelve en
	// cuanto el contexto termina.
	var updateInfo func() httpapi.UpdateStatus
	if cfg.UpdateCheck {
		chk := &update.Checker{Current: version, Logger: logger}
		fondo.Add(1)
		go func() {
			defer fondo.Done()
			chk.Run(ctx, 30*time.Second, 24*time.Hour, func(i update.Info) {
				if _, err := db.LogEvent(context.Background(), store.Event{
					Level: store.LevelInfo, Kind: "update_available",
					Message: "Hay una versión nueva: " + i.Latest,
				}); err != nil {
					logger.Error("no se pudo registrar el aviso de versión", "err", err)
				}
			})
		}()
		updateInfo = func() httpapi.UpdateStatus { return httpapi.UpdateStatus(chk.Latest()) }
	}
```

y en `httpapi.Config`: `UpdateInfo: updateInfo,`. (Si `fondo` se declara después de este punto en el archivo, mover el bloque debajo de su declaración; el orden relativo con el resto no importa.)

- [ ] **Step 3: Panel**

`web/src/stores/panel.js`, getters: `actualizacion: (s) => s.estado?.update ?? null,`.

`web/src/App.vue`, en `<script setup>`:

```js
import { ref, computed, onMounted } from 'vue'
// ...
// El aviso de versión se cierra por versión: si sale otra, vuelve a aparecer.
const CLAVE_AVISO = 'splitstream.aviso-version-cerrado'
const avisoCerrado = ref(leerAvisoCerrado())
function leerAvisoCerrado() {
  try { return localStorage.getItem(CLAVE_AVISO) } catch { return null }
}
const avisoVersion = computed(() => {
  const u = panel.actualizacion
  return panel.autenticado && u?.available && avisoCerrado.value !== u.latest ? u : null
})
function cerrarAviso() {
  avisoCerrado.value = panel.actualizacion?.latest ?? null
  try { localStorage.setItem(CLAVE_AVISO, avisoCerrado.value) } catch { /* sin almacenamiento, se repite al recargar */ }
}
```

En el template, dentro de `<q-page-container>` y **antes** del `q-page v-if="panel.cargando"`:

```vue
      <q-banner v-if="avisoVersion" dense class="bg-primary text-white" role="status">
        Hay una versión nueva de Splitstream ({{ avisoVersion.latest }}).
        <template #action>
          <q-btn flat no-caps label="Ver" :href="avisoVersion.url" target="_blank" rel="noopener" />
          <q-btn flat no-caps label="Cerrar" @click="cerrarAviso" />
        </template>
      </q-banner>
```

`router-view` y los `q-page` siguen igual: el banner es un hermano por encima, no envuelve nada.

Run: `cd web && npm run build` → limpio.

- [ ] **Step 4: Suite**

Run: `go vet ./... && go test ./cmd/splitstream/ ./internal/httpapi/ -race -count=1`
Expected: PASS. `TestRunStartupLogNeverContainsTheIngestKey` y los de `run()` no ven ninguna petición a GitHub: el binario de test es `dev`.

- [ ] **Step 5: Commit**

```bash
git add cmd/splitstream/main.go internal/httpapi web/src/stores/panel.js web/src/App.vue
git commit -m "feat: aviso de versión nueva en el estado, el registro y el panel"
```

---
### Task 7: `deploy/install.sh` y su prueba en contenedores

**Files:**
- Create: `deploy/install.sh`, `deploy/install_test.sh`
- Modify: `.github/workflows/ci.yml` (job `instaladores`), `.github/workflows/release.yml` (la unidad de systemd viaja en el `.tar.gz`)

**Interfaces:**
- Consumes: el layout de la release (`splitstream-<tag>-<nombre>.tar.gz` con carpeta del mismo nombre; `SHA256SUMS.txt`).
- Produces: `deploy/install.sh` con variables `SPLITSTREAM_VERSION`, `SPLITSTREAM_INSTALL_DIR`, `SPLITSTREAM_RELEASE_URL`, `SPLITSTREAM_SERVICE`; `deploy/install_test.sh` (exige Docker y Go); job `instaladores` en la CI. La Task 8 añade a ese job los `render_test.sh`.

- [ ] **Step 1: Escribir `deploy/install.sh`**

```sh
#!/bin/sh
# Instala Splitstream en Linux o macOS: descarga la release, verifica el checksum, copia
# el binario y, si hay systemd y una terminal, ofrece dejarlo como servicio.
#
#   curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/install.sh | sh
#
# Variables opcionales:
#   SPLITSTREAM_VERSION      etiqueta a instalar (por defecto, la última release)
#   SPLITSTREAM_INSTALL_DIR  dónde dejar el binario (por defecto /usr/local/bin)
#   SPLITSTREAM_RELEASE_URL  base de descarga (para probar el script contra un servidor local)
#   SPLITSTREAM_SERVICE      "no" para no ofrecer la unidad de systemd
#
# Nunca usa sudo sin decirlo antes y sin que se conteste que sí. Sin terminal —el caso
# de `curl | sh` en un script— imprime el comando y termina.
set -u

REPO="aprendomx/splitstream"
INSTALL_DIR="${SPLITSTREAM_INSTALL_DIR:-/usr/local/bin}"
TMP=""

decir() { printf '%s\n' "$*"; }
fallar() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }
limpiar() { [ -n "$TMP" ] && rm -rf "$TMP"; }
trap limpiar EXIT
tiene() { command -v "$1" >/dev/null 2>&1; }

descargar() { # url destino
  if tiene curl; then curl -fsSL --retry 3 -o "$2" "$1"
  elif tiene wget; then wget -q -O "$2" "$1"
  else fallar "hace falta curl o wget"
  fi
}

preguntar() { # texto → 0 si la persona dice que sí
  [ -r /dev/tty ] || return 1
  printf '%s [s/N] ' "$1"
  read -r resp < /dev/tty
  case "$resp" in s|S|si|sí|y|Y) return 0 ;; *) return 1 ;; esac
}

# ── 1. Plataforma ────────────────────────────────────────────────────────────
SO=$(uname -s | tr '[:upper:]' '[:lower:]')
ARQ=$(uname -m)
case "$ARQ" in
  x86_64|amd64) ARQ=amd64 ;;
  aarch64|arm64) ARQ=arm64 ;;
  *) fallar "arquitectura no soportada: $ARQ" ;;
esac
case "$SO-$ARQ" in
  darwin-arm64) NOMBRE=macos-apple-silicon ;;
  darwin-amd64) NOMBRE=macos-intel ;;
  linux-amd64)  NOMBRE=linux-x86_64 ;;
  linux-arm64)  NOMBRE=linux-arm64 ;;
  *) fallar "sistema no soportado: $SO. En Windows: winget install aprendomx.Splitstream" ;;
esac

# ── 2. Versión ───────────────────────────────────────────────────────────────
TMP=$(mktemp -d) || fallar "no se pudo crear un directorio temporal"
VERSION="${SPLITSTREAM_VERSION:-}"
if [ -z "$VERSION" ]; then
  descargar "https://api.github.com/repos/$REPO/releases/latest" "$TMP/latest.json" \
    || fallar "no se pudo consultar la última release"
  VERSION=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$TMP/latest.json" | head -n 1)
  [ -n "$VERSION" ] || fallar "no se pudo leer la versión de la respuesta de GitHub"
fi
BASE="${SPLITSTREAM_RELEASE_URL:-https://github.com/$REPO/releases/download/$VERSION}"
ARCHIVO="splitstream-$VERSION-$NOMBRE.tar.gz"
decir "Splitstream $VERSION para $NOMBRE"

# ── 3. Descarga y verificación ───────────────────────────────────────────────
descargar "$BASE/$ARCHIVO" "$TMP/$ARCHIVO" || fallar "no se pudo descargar $BASE/$ARCHIVO"
descargar "$BASE/SHA256SUMS.txt" "$TMP/SHA256SUMS.txt" || fallar "no se pudo descargar SHA256SUMS.txt"
ESPERADA=$(awk -v f="$ARCHIVO" '$2 == f { print $1 }' "$TMP/SHA256SUMS.txt")
[ -n "$ESPERADA" ] || fallar "SHA256SUMS.txt no lista $ARCHIVO"
if tiene sha256sum; then REAL=$(sha256sum "$TMP/$ARCHIVO" | awk '{ print $1 }')
elif tiene shasum; then REAL=$(shasum -a 256 "$TMP/$ARCHIVO" | awk '{ print $1 }')
else fallar "hace falta sha256sum o shasum para verificar la descarga"
fi
[ "$REAL" = "$ESPERADA" ] || fallar "el checksum de $ARCHIVO no coincide: descarga corrupta o manipulada. No se instaló nada."
decir "checksum verificado"

# ── 4. Extraer ───────────────────────────────────────────────────────────────
tar -xzf "$TMP/$ARCHIVO" -C "$TMP" || fallar "no se pudo extraer $ARCHIVO"
CARPETA="$TMP/splitstream-$VERSION-$NOMBRE"
BIN="$CARPETA/splitstream"
[ -f "$BIN" ] || fallar "el archivo no contiene el binario esperado"
chmod +x "$BIN"

# ── 5. Instalar ──────────────────────────────────────────────────────────────
[ -d "$INSTALL_DIR" ] || mkdir -p "$INSTALL_DIR" 2>/dev/null || true
if [ -d "$INSTALL_DIR" ] && [ -w "$INSTALL_DIR" ]; then
  install -m 755 "$BIN" "$INSTALL_DIR/splitstream" || fallar "no se pudo copiar a $INSTALL_DIR"
elif tiene sudo; then
  decir "Copiar el binario a $INSTALL_DIR necesita permiso de administrador. El comando:"
  decir "    sudo install -d -m 755 $INSTALL_DIR && sudo install -m 755 $BIN $INSTALL_DIR/splitstream"
  if preguntar "¿Lo ejecuto con sudo?"; then
    sudo install -d -m 755 "$INSTALL_DIR" && sudo install -m 755 "$BIN" "$INSTALL_DIR/splitstream" \
      || fallar "sudo install falló"
  else
    fallar "cancelado. Ejecuta ese comando tú, o vuelve a correr el script con SPLITSTREAM_INSTALL_DIR=\$HOME/.local/bin"
  fi
else
  fallar "$INSTALL_DIR no es escribible y no hay sudo. Usa SPLITSTREAM_INSTALL_DIR=\$HOME/.local/bin"
fi

"$INSTALL_DIR/splitstream" -version >/dev/null 2>&1 || fallar "el binario instalado no arranca"
decir "Instalado: $INSTALL_DIR/splitstream ($("$INSTALL_DIR/splitstream" -version))"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) decir "Aviso: $INSTALL_DIR no está en tu PATH; añádelo o llama al binario con la ruta completa." ;;
esac

# ── 6. systemd ───────────────────────────────────────────────────────────────
instrucciones_servicio() {
  decir ""
  decir "Para dejarlo como servicio de systemd (arranque automático, usuario propio):"
  decir "    sudo useradd --system --home /var/lib/splitstream --shell /usr/sbin/nologin splitstream"
  decir "    sudo install -d -o splitstream -g splitstream /var/lib/splitstream"
  decir "    sudo install -d -m 700 /etc/splitstream"
  decir "    printf 'SPLITSTREAM_MASTER_KEY=%s\\n' \"\$(splitstream -genkey)\" | sudo tee /etc/splitstream/env >/dev/null"
  decir "    sudo chmod 600 /etc/splitstream/env"
  decir "    sudo install -m 644 $CARPETA/splitstream.service /etc/systemd/system/   # o deploy/splitstream.service del repo"
  decir "    sudo systemctl enable --now splitstream"
}

instalar_servicio() {
  UNIT="$CARPETA/splitstream.service"
  if [ ! -f "$UNIT" ]; then
    descargar "https://raw.githubusercontent.com/$REPO/$VERSION/deploy/splitstream.service" "$UNIT" \
      || fallar "no se pudo descargar la unidad de systemd"
  fi
  decir "Voy a ejecutar con sudo: useradd splitstream, crear /var/lib/splitstream y /etc/splitstream/env, instalar la unidad y systemctl enable --now."
  id splitstream >/dev/null 2>&1 \
    || sudo useradd --system --home /var/lib/splitstream --shell /usr/sbin/nologin splitstream \
    || fallar "useradd falló"
  sudo install -d -o splitstream -g splitstream /var/lib/splitstream || fallar "no se pudo crear /var/lib/splitstream"
  sudo install -d -m 700 /etc/splitstream || fallar "no se pudo crear /etc/splitstream"
  if ! sudo test -f /etc/splitstream/env; then
    CLAVE=$("$INSTALL_DIR/splitstream" -genkey) || fallar "no se pudo generar la clave maestra"
    printf 'SPLITSTREAM_MASTER_KEY=%s\n' "$CLAVE" | sudo tee /etc/splitstream/env >/dev/null \
      || fallar "no se pudo escribir /etc/splitstream/env"
    unset CLAVE
    sudo chmod 600 /etc/splitstream/env
    decir "Clave maestra nueva en /etc/splitstream/env. RESPÁLDALA: sin ella, las claves de tus canales son irrecuperables."
  fi
  sudo install -m 644 "$UNIT" /etc/systemd/system/splitstream.service || fallar "no se pudo instalar la unidad"
  sudo systemctl daemon-reload && sudo systemctl enable --now splitstream || fallar "systemctl falló"
  decir "Servicio arrancado. El código del primer arranque: journalctl -u splitstream | grep -A2 'te pedirá este código'"
}

if [ "$SO" = linux ] && tiene systemctl && [ "${SPLITSTREAM_SERVICE:-}" != no ]; then
  if [ -f /etc/systemd/system/splitstream.service ]; then
    decir "La unidad de systemd ya existe; reinicia el servicio para usar el binario nuevo: sudo systemctl restart splitstream"
  elif [ "$INSTALL_DIR" != /usr/local/bin ]; then
    decir "La unidad de systemd espera el binario en /usr/local/bin; ajusta ExecStart si lo instalas como servicio."
    instrucciones_servicio
  elif preguntar "¿Instalar el servicio de systemd (usuario splitstream, /var/lib/splitstream, arranque automático)? Usa sudo."; then
    instalar_servicio
  else
    instrucciones_servicio
  fi
fi

decir ""
decir "Listo. Arranca con: splitstream   y abre http://localhost:8080"
```

Run: `shellcheck deploy/install.sh` → sin avisos (si `shellcheck` no está instalado en local, `brew install shellcheck`; la CI lo exige).

- [ ] **Step 2: Escribir `deploy/install_test.sh`**

```sh
#!/bin/sh
# Prueba deploy/install.sh contra una release falsa: empaqueta el binario del commit
# como lo hace release.yml, lo sirve por HTTP dentro de una red de Docker y corre el
# instalador en ubuntu (con curl) y en alpine (con el wget de busybox). Después
# manipula SHA256SUMS.txt y comprueba que el instalador se niega.
#
# Necesita Docker y Go. Uso: sh deploy/install_test.sh
set -eu
cd "$(dirname "$0")/.."

VERSION=v0.0.0-test
NOMBRE=linux-x86_64
ART=$(mktemp -d)
RED=splitstream-install-test
limpiar() {
  docker rm -f "$RED-server" >/dev/null 2>&1 || true
  docker network rm "$RED" >/dev/null 2>&1 || true
  rm -rf "$ART"
}
trap limpiar EXIT

# 1. Una release como la de verdad.
CARPETA="splitstream-$VERSION-$NOMBRE"
mkdir -p "$ART/$CARPETA"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
  -o "$ART/$CARPETA/splitstream" ./cmd/splitstream
cp README.md LICENSE deploy/splitstream.service "$ART/$CARPETA/"
(cd "$ART" && tar czf "$CARPETA.tar.gz" "$CARPETA" && rm -r "$CARPETA" && sha256sum splitstream-* > SHA256SUMS.txt)
chmod -R a+rX "$ART"

# 2. Servida por HTTP en una red propia.
docker network create "$RED" >/dev/null
docker run -d --rm --name "$RED-server" --network "$RED" -v "$ART:/srv:ro" busybox:1.36 \
  httpd -f -p 80 -h /srv >/dev/null

instalar() { # imagen preparación
  docker run --rm --network "$RED" -v "$PWD/deploy/install.sh:/install.sh:ro" \
    -e SPLITSTREAM_VERSION="$VERSION" -e SPLITSTREAM_RELEASE_URL="http://$RED-server" \
    -e SPLITSTREAM_INSTALL_DIR=/opt/bin -e SPLITSTREAM_SERVICE=no \
    "$1" sh -c "$2; cat /install.sh | sh && /opt/bin/splitstream -version | grep -F '$VERSION' && ! command -v sudo"
}

echo "== ubuntu (curl)"
instalar ubuntu:24.04 "apt-get update -qq >/dev/null && apt-get install -qq -y curl ca-certificates >/dev/null"
echo "== alpine (wget de busybox)"
instalar alpine:3.20 "true"

# 3. Checksum manipulado: no instala y no deja el binario.
echo "== checksum manipulado"
printf '%064d  %s.tar.gz\n' 0 "$CARPETA" > "$ART/SHA256SUMS.txt"
if docker run --rm --network "$RED" -v "$PWD/deploy/install.sh:/install.sh:ro" \
    -e SPLITSTREAM_VERSION="$VERSION" -e SPLITSTREAM_RELEASE_URL="http://$RED-server" \
    -e SPLITSTREAM_INSTALL_DIR=/opt/bin -e SPLITSTREAM_SERVICE=no \
    alpine:3.20 sh -c 'sh /install.sh; rc=$?; [ ! -e /opt/bin/splitstream ] && [ $rc -ne 0 ]'; then
  echo "el instalador rechazó el checksum manipulado"
else
  echo "FALLO: el instalador aceptó un checksum manipulado o dejó el binario" >&2
  exit 1
fi
echo "install_test: ok"
```

(`! command -v sudo` dentro del contenedor confirma que la instalación en un directorio escribible no necesitó sudo y que, de haberlo intentado, habría fallado.)

Run: `shellcheck deploy/install_test.sh` → limpio. En local **no** se ejecuta (no hay Docker): lo verifica la CI.

- [ ] **Step 3: La unidad viaja en el `.tar.gz`**

En `.github/workflows/release.yml`, paso «empaquetar», en la rama no-Windows: `cp splitstream deploy/splitstream.service "$NOMBRE"/`.

- [ ] **Step 4: Job `instaladores` en `.github/workflows/ci.yml`**

Después del job `docker`:

```yaml
  # Los instaladores son scripts: sin esto, un error de sintaxis en install.sh solo se
  # vería cuando alguien lo ejecutara con `curl | sh` en su servidor.
  instaladores:
    name: instaladores
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - name: shellcheck
        run: shellcheck deploy/*.sh deploy/homebrew/*.sh deploy/winget/*.sh
      - name: install.sh en ubuntu y alpine contra una release local
        run: sh deploy/install_test.sh
```

(`deploy/homebrew/*.sh` y `deploy/winget/*.sh` los crea la Task 8; hasta entonces el glob fallaría: en esta tarea dejar `shellcheck deploy/*.sh` y la Task 8 amplía la línea.)

- [ ] **Step 5: Commit**

```bash
git add deploy/install.sh deploy/install_test.sh .github/workflows/ci.yml .github/workflows/release.yml
git commit -m "feat(deploy): install.sh con verificación de checksum y prueba en contenedores"
```

---

### Task 8: Release: imagen en GHCR, fórmula de Homebrew y manifiestos de winget

**Files:**
- Modify: `.github/workflows/release.yml` (jobs `ghcr`, `tap`, `winget`), `deploy/Dockerfile` (build cruzado), `deploy/docker-compose.yml` (`image: ghcr.io/…`), `.github/workflows/ci.yml` (shellcheck de los `render.sh`, `render_test.sh` en `instaladores`)
- Create: `deploy/homebrew/splitstream.rb.tmpl`, `deploy/homebrew/render.sh`, `deploy/winget/aprendomx.Splitstream.yaml.tmpl`, `deploy/winget/aprendomx.Splitstream.installer.yaml.tmpl`, `deploy/winget/aprendomx.Splitstream.locale.en-US.yaml.tmpl`, `deploy/winget/aprendomx.Splitstream.locale.es-MX.yaml.tmpl`, `deploy/winget/render.sh`, `deploy/render_test.sh`

**Interfaces:**
- Consumes: `SHA256SUMS.txt` de la release (formato `sha256sum`: `<hash>  <archivo>`).
- Produces: `deploy/homebrew/render.sh <tag> <SHA256SUMS.txt>` → fórmula por stdout; `deploy/winget/render.sh <tag> <SHA256SUMS.txt> <dir>` → cuatro manifiestos en `<dir>`; imagen `ghcr.io/aprendomx/splitstream:<tag>` y `:latest`.

- [ ] **Step 1: Dockerfile multi-arquitectura**

En `deploy/Dockerfile`, la etapa 2:

```dockerfile
# --platform=$BUILDPLATFORM: la etapa corre en la arquitectura del runner y Go cruza a la
# de destino. Sin esto, buildx emularía arm64 con QEMU y el build tardaría diez veces más.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS binario
ARG TARGETOS TARGETARCH
WORKDIR /src
```

y el `go build`: `RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath ...`. Nada más cambia (certificados, zoneinfo y `/data` no dependen de la arquitectura).

`deploy/docker-compose.yml`, servicio:

```yaml
    # La imagen publicada; no hace falta clonar el repo. Para construirla tú, comenta
    # `image` y descomenta `build`.
    image: ghcr.io/aprendomx/splitstream:latest
    #build:
    #  context: ..
    #  dockerfile: deploy/Dockerfile
```

y en el comentario de cabecera, el paso 2 pasa a `docker compose -f deploy/docker-compose.yml run --rm splitstream -genkey` (igual) y se añade que `docker compose pull` actualiza. El job `docker` de la CI construye con `docker build -f deploy/Dockerfile` directamente (comprobarlo en `ci.yml`; si usaba `compose build`, cambiarlo a `docker build -t splitstream:ci -f deploy/Dockerfile .` y ajustar el nombre de imagen en los pasos siguientes).

- [ ] **Step 2: Plantilla de Homebrew y `render.sh`**

`deploy/homebrew/splitstream.rb.tmpl`:

```ruby
# Fórmula generada por deploy/homebrew/render.sh en cada release; no editar en el tap.
class Splitstream < Formula
  desc "Retransmite una señal RTMP de OBS a varias plataformas a la vez"
  homepage "https://github.com/aprendomx/splitstream"
  version "{{VERSION_SIN_V}}"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/aprendomx/splitstream/releases/download/{{VERSION}}/splitstream-{{VERSION}}-macos-apple-silicon.tar.gz"
      sha256 "{{SHA256_macos-apple-silicon}}"
    end
    on_intel do
      url "https://github.com/aprendomx/splitstream/releases/download/{{VERSION}}/splitstream-{{VERSION}}-macos-intel.tar.gz"
      sha256 "{{SHA256_macos-intel}}"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/aprendomx/splitstream/releases/download/{{VERSION}}/splitstream-{{VERSION}}-linux-arm64.tar.gz"
      sha256 "{{SHA256_linux-arm64}}"
    end
    on_intel do
      url "https://github.com/aprendomx/splitstream/releases/download/{{VERSION}}/splitstream-{{VERSION}}-linux-x86_64.tar.gz"
      sha256 "{{SHA256_linux-x86_64}}"
    end
  end

  def install
    bin.install "splitstream"
    doc.install "manual-de-usuario.md" if File.exist?("manual-de-usuario.md")
  end

  def post_install
    (var/"splitstream").mkpath
  end

  # `brew services start splitstream`: la base y la clave en var/splitstream, el log en var/log.
  service do
    run [opt_bin/"splitstream"]
    keep_alive true
    working_dir var/"splitstream"
    environment_variables SPLITSTREAM_DB_PATH: var/"splitstream/splitstream.db"
    log_path var/"log/splitstream.log"
    error_log_path var/"log/splitstream.log"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/splitstream -version")
  end
end
```

`deploy/homebrew/render.sh`:

```sh
#!/bin/sh
# Rellena la fórmula con la etiqueta y los checksums de SHA256SUMS.txt.
#   deploy/homebrew/render.sh v0.10.0 SHA256SUMS.txt > splitstream.rb
set -eu
TAG=$1
SUMAS=$2
DIR=$(dirname "$0")

suma() { awk -v f="splitstream-$TAG-$1.tar.gz" '$2 == f { print $1 }' "$SUMAS"; }

EXPR="s/{{VERSION_SIN_V}}/${TAG#v}/g; s/{{VERSION}}/$TAG/g"
for n in macos-apple-silicon macos-intel linux-x86_64 linux-arm64; do
  s=$(suma "$n")
  [ -n "$s" ] || { echo "render.sh: falta $n en $SUMAS" >&2; exit 1; }
  EXPR="$EXPR; s/{{SHA256_$n}}/$s/g"
done
sed -e "$EXPR" "$DIR/splitstream.rb.tmpl"
```

- [ ] **Step 3: Plantillas de winget y `render.sh`**

`deploy/winget/aprendomx.Splitstream.yaml.tmpl`:

```yaml
# yaml-language-server: $schema=https://aka.ms/winget-manifest.version.1.6.0.schema.json
PackageIdentifier: aprendomx.Splitstream
PackageVersion: {{VERSION_SIN_V}}
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.6.0
```

`deploy/winget/aprendomx.Splitstream.installer.yaml.tmpl`:

```yaml
# yaml-language-server: $schema=https://aka.ms/winget-manifest.installer.1.6.0.schema.json
PackageIdentifier: aprendomx.Splitstream
PackageVersion: {{VERSION_SIN_V}}
InstallerType: zip
NestedInstallerType: portable
NestedInstallerFiles:
  - RelativeFilePath: splitstream-{{VERSION}}-windows-x86_64\splitstream.exe
    PortableCommandAlias: splitstream
Installers:
  - Architecture: x64
    InstallerUrl: https://github.com/aprendomx/splitstream/releases/download/{{VERSION}}/splitstream-{{VERSION}}-windows-x86_64.zip
    InstallerSha256: {{SHA256_windows}}
ManifestType: installer
ManifestVersion: 1.6.0
```

`deploy/winget/aprendomx.Splitstream.locale.en-US.yaml.tmpl`:

```yaml
# yaml-language-server: $schema=https://aka.ms/winget-manifest.defaultLocale.1.6.0.schema.json
PackageIdentifier: aprendomx.Splitstream
PackageVersion: {{VERSION_SIN_V}}
PackageLocale: en-US
Publisher: aprendomx
PublisherUrl: https://github.com/aprendomx
PublisherSupportUrl: https://github.com/aprendomx/splitstream/issues
PackageName: Splitstream
PackageUrl: https://github.com/aprendomx/splitstream
License: MIT
LicenseUrl: https://github.com/aprendomx/splitstream/blob/main/LICENSE
ShortDescription: Relay one OBS RTMP stream to several platforms at once, from your own machine.
Tags:
  - obs
  - rtmp
  - streaming
  - twitch
  - youtube
ManifestType: defaultLocale
ManifestVersion: 1.6.0
```

`deploy/winget/aprendomx.Splitstream.locale.es-MX.yaml.tmpl`: igual con `PackageLocale: es-MX`, `ShortDescription: Retransmite una señal RTMP de OBS a varias plataformas a la vez, desde tu propio equipo.` y `ManifestType: locale` (sin `Tags` ni URLs: solo `PackageIdentifier`, `PackageVersion`, `PackageLocale`, `Publisher`, `PackageName`, `ShortDescription`, `ManifestType`, `ManifestVersion`).

`deploy/winget/render.sh`:

```sh
#!/bin/sh
# Rellena los manifiestos de winget en <dir>.
#   deploy/winget/render.sh v0.10.0 SHA256SUMS.txt manifests/a/aprendomx/Splitstream/0.10.0
set -eu
TAG=$1
SUMAS=$2
DEST=$3
DIR=$(dirname "$0")

SUMA=$(awk -v f="splitstream-$TAG-windows-x86_64.zip" '$2 == f { print $1 }' "$SUMAS")
[ -n "$SUMA" ] || { echo "render.sh: falta windows-x86_64.zip en $SUMAS" >&2; exit 1; }

mkdir -p "$DEST"
for t in "$DIR"/*.yaml.tmpl; do
  salida="$DEST/$(basename "$t" .tmpl)"
  sed -e "s/{{VERSION_SIN_V}}/${TAG#v}/g; s/{{VERSION}}/$TAG/g; s/{{SHA256_windows}}/$SUMA/g" "$t" > "$salida"
done
ls "$DEST"
```

- [ ] **Step 4: `deploy/render_test.sh`**

```sh
#!/bin/sh
# Los render.sh rellenan todo y no dejan ningún {{marcador}}.
set -eu
cd "$(dirname "$0")/.."
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
TAG=v1.2.3
i=0
for n in macos-apple-silicon.tar.gz macos-intel.tar.gz linux-x86_64.tar.gz linux-arm64.tar.gz windows-x86_64.zip; do
  i=$((i + 1))
  printf '%064d  splitstream-%s-%s\n' "$i" "$TAG" "$n" >> "$T/SHA256SUMS.txt"
done

deploy/homebrew/render.sh "$TAG" "$T/SHA256SUMS.txt" > "$T/splitstream.rb"
grep -q 'version "1.2.3"' "$T/splitstream.rb"
grep -q "releases/download/$TAG/splitstream-$TAG-linux-arm64.tar.gz" "$T/splitstream.rb"
grep -q "$(printf '%064d' 4)" "$T/splitstream.rb"
! grep -q '{{' "$T/splitstream.rb"

deploy/winget/render.sh "$TAG" "$T/SHA256SUMS.txt" "$T/winget" > /dev/null
[ "$(ls "$T/winget" | wc -l)" -eq 4 ]
grep -q 'PackageVersion: 1.2.3' "$T/winget/aprendomx.Splitstream.installer.yaml"
grep -q "InstallerSha256: $(printf '%064d' 5)" "$T/winget/aprendomx.Splitstream.installer.yaml"
! grep -rq '{{' "$T/winget"

# Sin el checksum de una plataforma, se niega.
head -n 2 "$T/SHA256SUMS.txt" > "$T/parcial.txt"
! deploy/homebrew/render.sh "$TAG" "$T/parcial.txt" > /dev/null 2>&1
echo "render_test: ok"
```

Run: `chmod +x deploy/*.sh deploy/homebrew/render.sh deploy/winget/render.sh && sh deploy/render_test.sh && shellcheck deploy/*.sh deploy/homebrew/*.sh deploy/winget/*.sh` → `render_test: ok`, sin avisos. En `ci.yml`, job `instaladores`: ampliar el `shellcheck` a los tres directorios y añadir el paso `- name: render.sh de Homebrew y winget` / `run: sh deploy/render_test.sh` antes del de `install_test.sh`.

- [ ] **Step 5: Jobs de `release.yml`**

Añadir al final del archivo:

```yaml
  # D7: la imagen se publica en GHCR con GITHUB_TOKEN; no hace falta ningún secreto. Va en
  # paralelo con los binarios: construye desde el Dockerfile, no desde los artefactos.
  ghcr:
    name: imagen en GHCR
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/build-push-action@v6
        with:
          context: .
          file: deploy/Dockerfile
          platforms: linux/amd64,linux/arm64
          push: true
          build-args: VERSION=${{ github.ref_name }}
          tags: |
            ghcr.io/aprendomx/splitstream:${{ github.ref_name }}
            ghcr.io/aprendomx/splitstream:latest

  # La fórmula se instala de verdad en un Mac antes de empujarla al tap. Sin TAP_TOKEN
  # se valida y se adjunta a la release, y el tap se actualiza a mano.
  tap:
    name: fórmula de Homebrew
    needs: publicar
    runs-on: macos-latest
    env:
      TAP_TOKEN: ${{ secrets.TAP_TOKEN }}
    steps:
      - uses: actions/checkout@v4
      - name: renderizar
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh release download "${GITHUB_REF_NAME}" --pattern SHA256SUMS.txt
          deploy/homebrew/render.sh "${GITHUB_REF_NAME}" SHA256SUMS.txt > splitstream.rb
          cat splitstream.rb
      - name: instalar la fórmula de verdad
        run: |
          brew install --formula ./splitstream.rb
          splitstream -version | grep -F "${GITHUB_REF_NAME}"
      - name: adjuntar a la release
        env:
          GH_TOKEN: ${{ github.token }}
        run: gh release upload "${GITHUB_REF_NAME}" splitstream.rb --clobber
      - name: empujar al tap
        if: env.TAP_TOKEN != ''
        run: |
          git clone --quiet "https://x-access-token:${TAP_TOKEN}@github.com/aprendomx/homebrew-tap.git" tap
          mkdir -p tap/Formula
          cp splitstream.rb tap/Formula/splitstream.rb
          cd tap
          git config user.name "github-actions[bot]"
          git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
          git add Formula/splitstream.rb
          git commit --quiet -m "splitstream ${GITHUB_REF_NAME}" || echo "la fórmula no cambió"
          git push --quiet

  # Los manifiestos se validan si el runner trae winget, se adjuntan a la release y, con
  # WINGET_TOKEN, se envían a microsoft/winget-pkgs (la primera vez la revisa una persona).
  winget:
    name: manifiestos de winget
    needs: publicar
    runs-on: windows-latest
    env:
      WINGET_TOKEN: ${{ secrets.WINGET_TOKEN }}
    steps:
      - uses: actions/checkout@v4
      - name: renderizar
        id: render
        shell: bash
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh release download "$GITHUB_REF_NAME" --pattern SHA256SUMS.txt
          DIR="manifests/a/aprendomx/Splitstream/${GITHUB_REF_NAME#v}"
          deploy/winget/render.sh "$GITHUB_REF_NAME" SHA256SUMS.txt "$DIR"
          echo "dir=$DIR" >> "$GITHUB_OUTPUT"
          7z a winget-manifests.zip manifests > /dev/null
      - name: validar
        shell: pwsh
        run: |
          if (Get-Command winget -ErrorAction SilentlyContinue) {
            winget validate --manifest "${{ steps.render.outputs.dir }}"
          } else {
            Write-Warning "winget no está en este runner; se omite la validación"
          }
      - name: adjuntar a la release
        shell: bash
        env:
          GH_TOKEN: ${{ github.token }}
        run: gh release upload "$GITHUB_REF_NAME" winget-manifests.zip --clobber
      - name: enviar a winget-pkgs
        if: env.WINGET_TOKEN != ''
        shell: pwsh
        run: |
          Invoke-WebRequest https://aka.ms/wingetcreate/latest -OutFile wingetcreate.exe
          .\wingetcreate.exe submit --token $env:WINGET_TOKEN "${{ steps.render.outputs.dir }}"
```

Comprobar con `actionlint` si está disponible (`brew install actionlint`); si no, revisar a ojo la indentación y que `permissions` de nivel superior (`contents: write`) sigue valiendo para `publicar`, `tap` y `winget` (el job `ghcr` la reduce a `contents: read` + `packages: write`).

- [ ] **Step 6: Commit**

```bash
git add deploy/Dockerfile deploy/docker-compose.yml deploy/homebrew deploy/winget deploy/render_test.sh .github/workflows/release.yml .github/workflows/ci.yml
git commit -m "feat(release): imagen en GHCR, fórmula de Homebrew y manifiestos de winget"
```

---
### Task 9: Documentación: README, spec base, manual y entrada de lanzamiento

**Files:**
- Modify: `README.md`, `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (§5, §8, §9, §12), `docs/manual-de-usuario.md` (§10 «Avisos»), `docs/lanzamiento.md` (cabecera), `deploy/env.example` (TLS y aviso de versión)

**Interfaces:**
- Consumes: todo lo anterior. Nombres exactos de variables y comandos de las Tasks 1–8.
- Produces: la documentación de la v0.10.

- [ ] **Step 1: README — sección «Instalación»**

Sustituir el bloque desde `## Instalación` hasta justo antes de `### Arranca` por:

````markdown
## Instalación

### Mac (Homebrew)

```bash
brew install aprendomx/tap/splitstream
splitstream
```

Homebrew quita la marca de cuarentena: no hay aviso de Gatekeeper. Para dejarlo
funcionando siempre, `brew services start splitstream` (base y clave en
`$(brew --prefix)/var/splitstream`, log en `$(brew --prefix)/var/log/splitstream.log`).

### Windows (winget)

```powershell
winget install aprendomx.Splitstream
splitstream
```

Sin SmartScreen: winget verifica el paquete por su checksum. Se instala como binario
portátil y queda en el `PATH`.

### Linux, o macOS sin Homebrew (script)

```bash
curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/install.sh | sh
```

El script detecta tu sistema, descarga la última release, **verifica el checksum** y copia
el binario a `/usr/local/bin`. Si eso necesita `sudo`, te enseña el comando y te pregunta
antes. En Linux con systemd te ofrece instalarlo como servicio. Puedes leerlo entero en
[`deploy/install.sh`](deploy/install.sh); para instalar en tu carpeta sin `sudo`:

```bash
curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/install.sh \
  | SPLITSTREAM_INSTALL_DIR=$HOME/.local/bin sh
```

### A mano

Ve a [las releases](https://github.com/aprendomx/splitstream/releases) y descarga el
archivo de tu plataforma:
````

y conservar, a continuación, la tabla de plataformas, «No hay instalador ni dependencias…», el bloque de `tar xzf`, el aviso de Gatekeeper (cambiar `splitstream-v0.8.0-macos-apple-silicon` por `splitstream-*-macos-apple-silicon` en el `xattr`) y el párrafo de Windows/SmartScreen, tal como están.

- [ ] **Step 2: README — «Ponerlo en internet» y «Detrás de un proxy»**

Después de la sección `### Si lo instalas en un servidor`, **sustituir** su último párrafo («Y si el panel va a ser accesible desde internet…») por:

````markdown
Si el panel va a ser accesible desde internet tiene que ir por HTTPS: sin TLS, la
contraseña viaja en claro. Tienes dos caminos: el TLS integrado (siguiente sección) o un
proxy delante (la de después).

### Ponerlo en internet

Con un dominio apuntando a la máquina y los puertos 80 y 443 abiertos, el binario pide y
renueva el certificado solo, con Let's Encrypt:

```bash
SPLITSTREAM_TLS_DOMAIN=relay.ejemplo.com splitstream
```

Con eso el panel escucha en `:443`, el `:80` redirige a HTTPS, la cookie de sesión sale
`Secure` y el estado enseña la URL pública. Los certificados se guardan en `tls-cache/`
junto a la base: respáldalo con ella y **no lo borres para «reintentar»**: Let's Encrypt
limita a 5 certificados por semana por dominio, y un reinicio en bucle sin caché los agota.

- Como servicio de systemd, descomenta `AmbientCapabilities=CAP_NET_BIND_SERVICE` en la
  unidad: es lo que permite abrir 80 y 443 sin root.
- En Docker no hace falta nada: publica `80:80` y `443:443` (hay un ejemplo comentado en
  `deploy/docker-compose.yml`).
- Si el certificado no llega, el registro del panel muestra `tls_certificate_error` con
  el motivo (casi siempre: el dominio no apunta aquí, o el 80 está cerrado).

Con un certificado propio, en vez del dominio:

```bash
SPLITSTREAM_TLS_CERT_FILE=/etc/ssl/relay.crt SPLITSTREAM_TLS_KEY_FILE=/etc/ssl/relay.key splitstream
```

### Detrás de un proxy

Si prefieres Caddy o nginx delante, ellos terminan el TLS y el binario sigue en `:8080`.
Dos cosas:

1. `SPLITSTREAM_SECURE_COOKIES=true`, para que la cookie no salga sin `Secure`.
2. `SPLITSTREAM_TRUSTED_PROXIES` con la IP del proxy, para que el limitador del login y el
   asistente del primer arranque vean la IP real y no la del proxy. Sin esto, un intento
   fallido de cualquiera castiga a todos, y el asistente cree que todo es remoto.

```caddyfile
relay.ejemplo.com {
    reverse_proxy 127.0.0.1:8080
}
```

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
}
```

```bash
SPLITSTREAM_SECURE_COOKIES=true SPLITSTREAM_TRUSTED_PROXIES=127.0.0.1/32,::1/128 splitstream
```

Con Docker y el proxy en el host, la red puente suele ser `172.16.0.0/12`. **Nunca pongas
`0.0.0.0/0`**: confiar en todo el mundo anula el limitador y convierte a cualquiera en
«local» con una cabecera.
````

- [ ] **Step 3: README — tabla de configuración, Docker y actualizar**

En la tabla de `## Configuración`, añadir filas:

```markdown
| `SPLITSTREAM_TLS_DOMAIN` | vacío | Con valor, TLS integrado con Let's Encrypt para ese dominio; el panel pasa a `:443` |
| `SPLITSTREAM_TLS_CACHE_DIR` | `tls-cache/` junto a la base | Cuenta y certificados de Let's Encrypt |
| `SPLITSTREAM_TLS_CERT_FILE` / `SPLITSTREAM_TLS_KEY_FILE` | vacíos | Certificado propio en PEM, en vez del dominio |
| `SPLITSTREAM_TLS_REDIRECT_ADDR` | `:80` con TLS | Listener que redirige a HTTPS y atiende el reto de Let's Encrypt; `none` lo apaga |
| `SPLITSTREAM_TRUSTED_PROXIES` | vacío | CIDR o IP, separadas por comas, desde las que se cree `X-Forwarded-For` |
| `SPLITSTREAM_UPDATE_CHECK` | `true` | `false` apaga la consulta diaria de versión nueva |
```

y cambiar la fila de `SPLITSTREAM_SECURE_COOKIES` a «`false`; `true` con TLS integrado».

En `## Con Docker`, sustituir el primer bloque por:

```bash
mkdir splitstream && cd splitstream
curl -fsSLO https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/env.example -o .env

# Genera la clave maestra y pégala en .env
docker compose run --rm splitstream -genkey

docker compose up -d
```

(el `compose` ya apunta a `ghcr.io/aprendomx/splitstream:latest`; no hace falta clonar el repo). Ajustar las rutas `-f deploy/docker-compose.yml` del resto de la sección a `docker compose …` sin `-f`, y el párrafo del código del primer arranque: «…o pon la IP del host de Docker en `SPLITSTREAM_TRUSTED_PROXIES` si hay un proxy delante que manda `X-Forwarded-For`».

Nueva subsección al final de `## Instalación` (antes de `### Arranca`) o, mejor, después de `## Con Docker`:

````markdown
## Actualizar

El panel avisa cuando hay una versión nueva (una consulta a GitHub al arrancar y cada
24 h, con la versión como único dato; `SPLITSTREAM_UPDATE_CHECK=false` la apaga). Nada se
actualiza solo:

```bash
brew upgrade splitstream                    # Homebrew
winget upgrade aprendomx.Splitstream        # winget
curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/install.sh | sh   # script
docker compose pull && docker compose up -d # Docker
```
````

En `## Dejarlo funcionando siempre` → `### Linux con systemd`, añadir antes del bloque de comandos: «`install.sh` ofrece hacer todo esto por ti. A mano:».

- [ ] **Step 4: Spec base**

`docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`:

- §5 Dependencias: al final, «**Desde la v0.10 (2026-09-10):** `golang.org/x/crypto/acme/autocert`, un paquete más del módulo `x/crypto` que ya se traía. Sigue sin haber módulo nuevo.»
- §8 Seguridad: «**Desde la v0.10:** `X-Forwarded-For` se honra solo desde `SPLITSTREAM_TRUSTED_PROXIES`; por defecto se ignora, como siempre. El binario puede terminar TLS él mismo (`internal/webtls`); `SecureCookies` pasa a `true` por defecto en ese caso.»
- §9 API: `statusDTO` gana `panel {tls, public_url}` y `update {available, latest, url}`.
- §12 Operación: después del párrafo de la v0.9, «**Desde la v0.10 (2026-09-10):** `SPLITSTREAM_TLS_DOMAIN`, `SPLITSTREAM_TLS_CACHE_DIR`, `SPLITSTREAM_TLS_CERT_FILE`, `SPLITSTREAM_TLS_KEY_FILE`, `SPLITSTREAM_TLS_REDIRECT_ADDR`, `SPLITSTREAM_TRUSTED_PROXIES`, `SPLITSTREAM_UPDATE_CHECK`. Instalación por Homebrew (`aprendomx/tap`), winget (`aprendomx.Splitstream`), `deploy/install.sh` e imagen `ghcr.io/aprendomx/splitstream`. Eventos nuevos: `update_available`, `tls_certificate_error`.»

- [ ] **Step 5: Manual, `env.example` y entrada de lanzamiento**

`docs/manual-de-usuario.md`, al final de `## 10. Avisos` (antes de `## 11. Respaldo`):

```markdown
### Cuando hay una versión nueva

Una vez al día Splitstream mira si hay una release más reciente y, si la hay, enseña una
franja arriba del panel con la versión y un enlace. Puedes cerrarla; vuelve a salir solo
con la siguiente versión. No se actualiza solo: cómo hacerlo depende de cómo lo
instalaste, y está en el README («Actualizar»). Es la única conexión que el programa hace
por su cuenta, y solo manda su propia versión.
```

`deploy/env.example`: bloques comentados para `SPLITSTREAM_TLS_DOMAIN` (con el aviso de los 5 certificados por semana), `SPLITSTREAM_TLS_CERT_FILE`/`_KEY_FILE` y `SPLITSTREAM_UPDATE_CHECK=false`.

`docs/lanzamiento.md`, línea 4: «Escrito para la v0.10.0, la primera que se instala con `brew`, `winget` o un script, con TLS integrado y aviso de versión nueva; si cambian las plataformas soportadas o lo que la herramienta no hace, hay que revisarlo. Son las dos cosas que el texto promete.» Revisar el resto del archivo por si algún párrafo dice que hay que poner un proxy para tener HTTPS y matizarlo.

- [ ] **Step 6: Comprobar enlaces y nombres**

Run: `grep -rn "SPLITSTREAM_TLS_\|TRUSTED_PROXIES\|UPDATE_CHECK" README.md deploy/env.example internal/config/config.go | sort` y confirmar que cada variable del README existe en `config.go` con el mismo nombre. `cd web && npm run build` (el README no lo toca, pero cierra la tarea con la suite verde): `go vet ./... && go test ./... -race -count=1`.

- [ ] **Step 7: Commit**

```bash
git add README.md docs/superpowers/specs/2026-09-01-rtmp-relay-design.md docs/manual-de-usuario.md docs/lanzamiento.md deploy/env.example
git commit -m "docs: instalación por brew, winget y script; TLS integrado; proxies; aviso de versión"
```

---

## Autorrevisión

**Cobertura del spec.** §3.1 (layout de la release): Task 7 añade la unidad al `.tar.gz`; §3.2 Homebrew: Task 8; §3.3 winget: Task 8; §3.4 `install.sh` + `install_test.sh` + job: Task 7; §3.5 GHCR + Dockerfile + compose: Task 8; §4.1 configuración: Task 1; §4.2 `webtls` y cableado: Tasks 3 y 4; §4.3 comportamiento (`tls_certificate_error`, `-healthcheck`, `panel`, capacidades, compose, README): Tasks 3, 4 y 9; §5 proxies: Tasks 1, 2 y 9; §6 aviso: Tasks 5, 6 y 9; §7 pruebas: cada tarea lleva las suyas y la CI (guard `webtls`, `instaladores`, shellcheck) está en las Tasks 4, 7 y 8; §8 fuera: nada lo contradice.

**Marcadores.** Ningún «TBD»; los pasos de documentación llevan el texto. Dos avisos explícitos al implementador donde el plan no puede saber el detalle sin abrir el archivo (código de respuesta del login, helpers de `status_test.go`): se resuelven leyendo el archivo, no inventando.

**Consistencia de tipos.** `config.Config.TLS()` (T1) ↔ `webtls.Build` y `main.go` (T3, T4). `httpapi.Config.TrustedProxies []netip.Prefix` (T2) ← `cfg.TrustedProxies` (T1). `httpapi.Config.TLS bool`, `PublicURL string` (T4) ← `tlsSetup.PublicURL`. `httpapi.UpdateStatus{Latest, URL string; Available bool}` (T6) es convertible desde `update.Info{Latest, URL string; Available bool}` (T5): mismos campos, mismo orden, mismos tipos. `healthcheck(addr, tls)` en T4 y en `main()`. `deploy/homebrew/render.sh` y `deploy/winget/render.sh` (T8) reciben el mismo `SHA256SUMS.txt` que `release.yml` ya produce y que `render_test.sh` imita con el formato `sha256sum`.

**Riesgos anotados.** (1) `TestCertificateErrorsAreReportedOncePerTenMinutesForOurDomainOnly` puede tocar la red si autocert intenta el `Dial` antes de fallar: el plan da la alternativa (`export_test.go` para el avisador). (2) `winget validate` puede no existir en el runner: el job avisa y sigue. (3) La primera publicación en el tap y en winget-pkgs necesita secretos y un repo que solo el usuario puede crear; los jobs degradan a «adjuntar a la release». (4) `docker/build-push-action` y compañía son Actions, no dependencias del binario.
