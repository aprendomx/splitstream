# Splitstream — v0.10 «Que instalarlo no duela»

**Fecha:** 2026-09-10
**Estado:** aprobado por el plan maestro (decisión D7 confirmada); pendiente de plan de implementación
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
**Roadmap:** `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` §7
**Plan maestro:** `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §5
**Versión de partida:** `v0.9.0` (`main` @ `3ef4cba`)

## 1. Qué se construye

Que alguien sin manual pueda tener Splitstream instalado, en internet y con certificado en
tres líneas, y que se entere cuando salga una versión nueva. Cuatro piezas independientes
(C.1–C.4 del plan maestro), ninguna toca el motor:

1. **Instaladores**: fórmula de Homebrew en un tap propio, manifiestos de winget,
   `deploy/install.sh` para Linux y macOS, e imagen publicada en GHCR (D7). Todo se
   genera desde `release.yml` a partir de los mismos artefactos y `SHA256SUMS.txt` que ya
   se publican.
2. **TLS integrado**: con `SPLITSTREAM_TLS_DOMAIN`, el binario pide y renueva el
   certificado con Let's Encrypt (`golang.org/x/crypto/acme/autocert`, que ya está en el
   módulo `x/crypto` del proyecto: **cero dependencias nuevas**); con
   `SPLITSTREAM_TLS_CERT_FILE`/`_KEY_FILE`, usa un certificado propio. Sin ninguna de las
   dos, nada cambia.
3. **Proxies de confianza**: `SPLITSTREAM_TRUSTED_PROXIES` hace que el limitador del login
   y la detección de «local» del asistente vean la IP real detrás de Caddy, nginx o Docker,
   y solo entonces.
4. **Aviso de versión nueva**: una consulta a la última release de GitHub al arrancar y
   cada 24 h, un campo en el estado y un banner discreto en el panel. Sin telemetría.

**La regla de esta entrega:** todo es *opt-in* y el comportamiento por defecto en un PC
(`localhost:8080`, sin TLS, sin proxy) es idéntico al de la v0.9. Un test de regresión
sobre `run()` lo vigila.

## 2. Enmiendas al spec base

- **§5 Dependencias**: `golang.org/x/crypto/acme/autocert` entra como paquete de un módulo
  que ya se trae; no hay módulo nuevo. Sigue siendo `go mod` de cinco directas.
- **§12 Despliegue**: «el TLS lo termina un proxy» deja de ser la única opción. Se añaden
  `SPLITSTREAM_TLS_DOMAIN`, `SPLITSTREAM_TLS_CACHE_DIR`, `SPLITSTREAM_TLS_CERT_FILE`,
  `SPLITSTREAM_TLS_KEY_FILE`, `SPLITSTREAM_TLS_REDIRECT_ADDR`, `SPLITSTREAM_TRUSTED_PROXIES`
  y `SPLITSTREAM_UPDATE_CHECK`. `SPLITSTREAM_SECURE_COOKIES` pasa a valer `true` por
  defecto cuando hay TLS integrado.
- **§9 API**: `statusDTO` gana `panel {tls, public_url}` y `update {available, latest, url}`.
- **§8 Seguridad**: `X-Forwarded-For` se honra únicamente cuando `RemoteAddr` cae en un
  prefijo de `SPLITSTREAM_TRUSTED_PROXIES`; por defecto la lista está vacía y todo sigue
  como hoy.
- **Catálogo de eventos**: `update_available` (info, una vez por versión nueva vista) y
  `tls_certificate_error` (error, cuando autocert no consigue el certificado).

## 3. Instaladores

### 3.1 Artefactos de partida

`release.yml` ya publica `splitstream-<tag>-<nombre>.tar.gz` (`macos-apple-silicon`,
`macos-intel`, `linux-x86_64`, `linux-arm64`), `splitstream-<tag>-windows-x86_64.zip` y
`SHA256SUMS.txt`. Cada archivo contiene una carpeta `splitstream-<tag>-<nombre>/` con el
binario, `README.md`, `LICENSE` y `docs/manual-de-usuario.md`. Nada de eso cambia; los
instaladores se apoyan en ello.

### 3.2 Homebrew

- La fórmula vive **en este repo** como plantilla: `deploy/homebrew/splitstream.rb.tmpl`
  con `{{VERSION}}` y un `{{SHA256_<nombre>}}` por artefacto, y un script
  `deploy/homebrew/render.sh <tag> <SHA256SUMS.txt>` que la rellena. Así la fórmula se
  revisa aquí y el tap solo recibe archivos generados.
- Fórmula: `on_macos` / `on_linux` × `on_arm` / `on_intel`, `url` al `.tar.gz` de la
  release, `sha256` de `SHA256SUMS.txt`, `bin.install "splitstream"`, `test do` que ejecuta
  `splitstream -version` y compara con la versión, y bloque `service do` (`keep_alive true`,
  `working_dir var/"splitstream"`, `SPLITSTREAM_DB_PATH` en `var/"splitstream"`, logs en
  `var/"log"`). `brew` quita la cuarentena de Gatekeeper: el `xattr` del README deja de
  hacer falta por esta vía.
- Job `tap` de `release.yml` (`macos-latest`, después de `publicar`): descarga
  `SHA256SUMS.txt` de la release, renderiza la fórmula, la **instala de verdad** con
  `brew install --formula ./splitstream.rb`, comprueba `splitstream -version` y solo
  entonces la empuja a `aprendomx/homebrew-tap` (`Formula/splitstream.rb`) con un commit
  directo a `main` usando el secreto `TAP_TOKEN`. Sin PR: la fórmula es generada y
  validada, y un PR añadiría un paso humano por release. Si `TAP_TOKEN` no existe, el job
  valida y sube la fórmula como artefacto de la release (`splitstream.rb`) pero no empuja.
- **Del usuario:** crear el repo público `aprendomx/homebrew-tap` (vacío, con un README) y
  el secreto `TAP_TOKEN` (token de grano fino con `contents: write` sobre ese repo).

### 3.3 winget

- Plantillas en `deploy/winget/`: `aprendomx.Splitstream.yaml`,
  `aprendomx.Splitstream.installer.yaml`, `aprendomx.Splitstream.locale.es-MX.yaml`
  (más `locale.en-US.yaml`, que winget exige como `DefaultLocale`), con `{{VERSION}}`
  y `{{SHA256_windows}}`. `InstallerType: zip`, `NestedInstallerType: portable`,
  `RelativeFilePath: splitstream-<tag>-windows-x86_64/splitstream.exe`,
  `PortableCommandAlias: splitstream`. `PackageIdentifier` `aprendomx.Splitstream`,
  licencia MIT. `deploy/winget/render.sh` los rellena.
- Job `winget` de `release.yml` (`windows-latest`): renderiza, valida con `winget validate`,
  sube los manifiestos como artefacto de la release (`winget-manifests.zip`) y, si existe el
  secreto `WINGET_TOKEN`, los envía con `wingetcreate submit` (descargado de
  `https://aka.ms/wingetcreate/latest`). La primera publicación en
  `microsoft/winget-pkgs` la revisa una persona de su lado; después es automática.
- **Del usuario:** el secreto `WINGET_TOKEN` (PAT clásico con `public_repo`) cuando quiera
  automatizar; hasta entonces abre el PR a mano con el zip de manifiestos.

### 3.4 `deploy/install.sh`

- POSIX `sh` (corre en `dash`, `ash` de Alpine y `bash`), sin `set -e` sorpresas: cada
  paso comprueba y explica. Uso previsto en el README:
  `curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/install.sh | sh`.
- Hace, en orden: detecta SO (`linux`, `darwin`) y arquitectura (`x86_64`, `aarch64`/`arm64`);
  resuelve la versión (`SPLITSTREAM_VERSION` o la última release por
  `https://api.github.com/repos/aprendomx/splitstream/releases/latest`, con `curl` o
  `wget`); descarga el `.tar.gz` y `SHA256SUMS.txt` desde `SPLITSTREAM_RELEASE_URL`
  (por defecto `https://github.com/aprendomx/splitstream/releases/download/<tag>`);
  **verifica el checksum** con `sha256sum` o `shasum -a 256` y aborta si no coincide o si
  no hay con qué verificar; instala en `SPLITSTREAM_INSTALL_DIR` (por defecto
  `/usr/local/bin`); ejecuta `splitstream -version` para confirmar.
- **`sudo` nunca en silencio**: si el directorio no es escribible, imprime el comando
  exacto que va a ejecutar con `sudo` y lo lanza solo si hay terminal (`/dev/tty`) y la
  persona responde `s`; sin terminal, termina con el comando impreso para que lo copie.
- systemd: si hay `systemctl` y terminal, ofrece instalar `deploy/splitstream.service`
  (descargado del mismo tag), crear `/etc/splitstream/env` con una clave maestra generada
  por `splitstream -genkey` y el usuario `splitstream`, y habilitar el servicio. Sin
  terminal no hace nada de eso: imprime los tres comandos.
- Windows no es objetivo del script: winget.
- `deploy/install_test.sh` corre en la CI (job `install`): empaqueta el binario del commit
  como si fuera una release, lo sirve con un servidor HTTP local, y ejecuta `install.sh` en
  contenedores `ubuntu:24.04` y `alpine:3.20` con `SPLITSTREAM_RELEASE_URL` y
  `SPLITSTREAM_VERSION` apuntando a él. Comprueba que instala sin `sudo` en un directorio
  escribible, que `splitstream -version` responde, y que un `SHA256SUMS.txt` manipulado
  hace fallar la instalación sin dejar el binario.

### 3.5 Imagen en GHCR (D7)

- Job `ghcr` de `release.yml`: `docker/setup-qemu-action` + `docker/setup-buildx-action`,
  login a `ghcr.io` con `GITHUB_TOKEN` (`permissions: packages: write`), build de
  `deploy/Dockerfile` para `linux/amd64,linux/arm64` con `build-arg VERSION=<tag>`, y
  push con etiquetas `ghcr.io/aprendomx/splitstream:<tag>` y `:latest`.
- `deploy/Dockerfile`: la etapa de Go pasa a `FROM --platform=$BUILDPLATFORM` con
  `GOOS=$TARGETOS GOARCH=$TARGETARCH`, para que el build de arm64 no corra bajo QEMU (Go
  cross-compila). La etapa final `scratch` no cambia.
- `deploy/docker-compose.yml` usa `image: ghcr.io/aprendomx/splitstream:latest` y deja
  `build:` comentado para quien quiera construir. El job `docker` de la CI sigue
  construyendo desde el `Dockerfile`.
- **Del usuario:** tras la primera release, comprobar en GitHub → Packages que el paquete
  es público y está enlazado al repo.

## 4. TLS integrado

### 4.1 Configuración

| Variable | Por defecto | Efecto |
| --- | --- | --- |
| `SPLITSTREAM_TLS_DOMAIN` | vacío | Con valor: certificado de Let's Encrypt para ese dominio (solo ese; `HostWhitelist`). |
| `SPLITSTREAM_TLS_CACHE_DIR` | `tls-cache/` junto a la base | Dónde autocert guarda cuenta y certificados. Se respalda con la base. |
| `SPLITSTREAM_TLS_CERT_FILE` / `SPLITSTREAM_TLS_KEY_FILE` | vacíos | Certificado propio (PEM). Excluyente con el dominio: las dos cosas a la vez es error de configuración. |
| `SPLITSTREAM_TLS_REDIRECT_ADDR` | `:80` con TLS; sin TLS no aplica | Listener HTTP que responde el reto HTTP-01 y redirige el resto a HTTPS (301). `none` lo desactiva (entonces autocert usa TLS-ALPN-01 en el puerto TLS). |
| `SPLITSTREAM_HTTP_ADDR` | `:8080`; **`:443` cuando hay TLS** | El puerto del panel. Con TLS es el puerto HTTPS. Si la persona lo fija, se respeta tal cual. |
| `SPLITSTREAM_SECURE_COOKIES` | `false`; **`true` cuando hay TLS** | Igual que hoy, pero un `false` explícito con TLS se respeta y se avisa en el log. |

`Config` gana `TLSDomain`, `TLSCacheDir`, `TLSCertFile`, `TLSKeyFile`, `TLSRedirectAddr` y
un método `TLS() bool`. `LoadFrom` valida: dominio y archivos a la vez → error; un archivo
sin el otro → error; dominio con espacios o esquema (`https://`) → error.

### 4.2 Dónde vive

Paquete nuevo `internal/webtls` (stdlib + `x/crypto/acme/autocert`; no importa nada del
proyecto salvo `internal/config`):

- `func Build(cfg *config.Config, logger *slog.Logger) (*Setup, error)`, con
  `Setup{TLSConfig *tls.Config; Redirect http.Handler; PublicURL string}`.
  - Con dominio: `autocert.Manager{Prompt: AcceptTOS, HostPolicy: HostWhitelist(dom),
    Cache: DirCache(cacheDir)}`; `TLSConfig = m.TLSConfig()` (que ya incluye
    `acme-tls/1` en `NextProtos`); `Redirect = m.HTTPHandler(nil)` (reto HTTP-01 +
    redirección 302 por defecto de autocert); `PublicURL = "https://" + dom`.
  - Con archivos: `tls.LoadX509KeyPair`; `Redirect` es una redirección 301 a `https://` +
    `Host`; `PublicURL` vacío (no se sabe el nombre público).
  - `MinVersion: tls.VersionTLS12`.
- `cmd/splitstream/main.go`: con `cfg.TLS()`, el `http.Server` del panel hace
  `ServeTLS(ln, "", "")` con `TLSConfig` sobre un listener en `HTTPAddr`, y un segundo
  `http.Server` mínimo (`ReadHeaderTimeout` 5 s) sirve `Redirect` en `TLSRedirectAddr`
  salvo `none`. Ambos entran en el mismo `Shutdown` de 5 s. Sin TLS, el código que existe
  hoy, sin cambios.
- `internal/httpapi` **no importa `crypto/tls` ni `webtls`**: recibe `Config.PublicURL` y
  `Config.TLS bool` como datos para el DTO. La CI lo vigila con el mismo `go list -deps`
  que las demás fronteras (`internal/httpapi` no importa `internal/webtls`).

### 4.3 Comportamiento

- El primer certificado se pide en el primer `ClientHello`; hasta entonces el puerto
  escucha pero cada conexión falla. Se loguea `tls_certificate_error` (evento del bus,
  nivel error, sin secretos: dominio y el texto del error de ACME) como máximo una vez
  cada 10 minutos por dominio, para que el log de un dominio mal apuntado no sea un torrente.
- `-healthcheck` (Docker) lee también `SPLITSTREAM_TLS_DOMAIN` y `SPLITSTREAM_TLS_CERT_FILE`:
  con TLS golpea `https://127.0.0.1:<puerto>/healthz` con `InsecureSkipVerify` (es
  loopback y solo mide vida; el nombre del certificado no es 127.0.0.1 nunca).
- `statusDTO.panel = {tls bool, public_url string}`. Lo consumirá el chat de Kick en la
  v0.12 para saber si hay URL pública. Con proxy externo (sin TLS integrado) `tls` es
  `false` aunque haya HTTPS delante: es el TLS que *el binario* termina, no el que ve el
  navegador.
- Puertos bajos sin root: `deploy/splitstream.service` lleva
  `AmbientCapabilities=CAP_NET_BIND_SERVICE` comentado con la explicación; en Docker no
  hace falta (desde Docker 20.10 `ip_unprivileged_port_start=0` dentro del contenedor) y
  `docker-compose.yml` lleva la variante `80:80` / `443:443` comentada.
- README, sección nueva «Ponerlo en internet»: apuntar el dominio, abrir 80 y 443, las dos
  variables, y el aviso de que Let's Encrypt limita a 5 certificados por semana por
  dominio: no reiniciar en bucle con el `tls-cache` borrado.
- Fuera: HSTS (obliga al dominio a HTTPS en el navegador durante un año aunque después se
  vuelva atrás; se deja a quien ponga un proxy), varios dominios, wildcard, DNS-01.

## 5. Proxies de confianza

- `Config.TrustedProxies []netip.Prefix` desde `SPLITSTREAM_TRUSTED_PROXIES`, lista separada
  por comas de CIDR o IP sueltas (`10.0.0.5` equivale a `/32`). Vacía por defecto. Un valor
  que no parsea es error de arranque.
- `internal/httpapi/clientip.go`: `func (s *Server) clientIP(r *http.Request) netip.Addr`.
  Si `RemoteAddr` no cae en ningún prefijo de confianza, devuelve `RemoteAddr` y punto.
  Si cae: recorre `X-Forwarded-For` de **derecha a izquierda** saltando las direcciones de
  confianza y devuelve la primera que no lo es; si todas son de confianza o la cabecera
  no viene, devuelve `RemoteAddr`. Direcciones que no parsean cuentan como «no de
  confianza» (se devuelven tal cual, y el limitador las agrupa por texto).
- `esLocal(r)` pasa a ser `s.clientIP(r).IsLoopback()`; el `loginLimiter` se clava por el
  resultado de `clientIP`. Ambos comentarios se reescriben: la razón de ignorar la cabecera
  sigue siendo la misma, pero ahora es una decisión de configuración.
- El limitador purga las entradas que llevan más de 10 minutos sin intentos (hoy el mapa
  solo crece); se hace en el propio `allow`, sin goroutine.
- `deploy/env.example` y README, sección «Detrás de un proxy»: ejemplos con Caddy
  (`reverse_proxy 127.0.0.1:8080`, que ya manda `X-Forwarded-For`) y con nginx
  (`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for`), el valor
  `SPLITSTREAM_TRUSTED_PROXIES=127.0.0.1/32,::1/128` para un proxy en la misma máquina,
  el de Docker (`172.16.0.0/12`) y el aviso: `0.0.0.0/0` anula el limitador y convierte a
  cualquiera en «local» con una cabecera.
- Con Docker y proxy en el host ya no hace falta el código del asistente si el proxy manda
  la cabecera y está en la lista; el comentario del `docker-compose.yml` se actualiza.

## 6. Aviso de versión nueva

- Paquete nuevo `internal/update` (solo stdlib): `Checker{Current string; URL string;
  Client *http.Client; Now func() time.Time; Logger *slog.Logger}` con
  `Run(ctx, initialDelay, interval, onNew func(Info))` y `Latest() (Info, bool)`.
  `Info{Latest, URL string; Available bool}`.
- `GET https://api.github.com/repos/aprendomx/splitstream/releases/latest`, cabecera
  `Accept: application/vnd.github+json`, `User-Agent: splitstream/<version>` y **nada
  más** (sin identificadores, sin telemetría). Timeout 10 s. Lee `tag_name` y `html_url`.
- Comparación `semver` propia: `vX.Y.Z` en tres enteros; un `Current` que no parsea
  (`dev`, `docker`, `v0.10.0-dirty`) **no compara y no consulta**: el checker no arranca.
  Un `tag_name` que no parsea se ignora.
- Cadencia: primera consulta 30 s después de arrancar, después cada 24 h. Cualquier fallo
  (red, 403 por cuota de la API, JSON raro) se loguea a nivel debug y se reintenta en la
  siguiente vuelta. Jamás afecta al arranque ni al relay.
- `SPLITSTREAM_UPDATE_CHECK=false` lo desactiva (`Config.UpdateCheck bool`, `true` por
  defecto). En la imagen Docker funciona igual: `scratch` ya lleva los certificados raíz.
- Al ver una versión nueva por primera vez en el proceso (y cada vez que cambie), emite
  el evento `update_available` (info, mensaje «Hay una versión nueva: vX.Y.Z»), que llega
  al panel y a los webhooks por el camino de siempre.
- `statusDTO.update = {available bool, latest string, url string}`; `httpapi` lo recibe
  por un hook `UpdateInfo func() (latest, url string, available bool)` como hace con
  `ExtraMetrics`, para no importar `update`.
- Panel: `App.vue` muestra un `q-banner` discreto encima del `router-view` cuando
  `estado.update.available`: «Hay una versión nueva (vX.Y.Z) · Ver» con el enlace a la
  release, y un botón «Cerrar» que lo oculta hasta la siguiente versión distinta
  (`localStorage`, clave por versión). No se actualiza solo: el README explica cómo
  actualizar con cada instalador (`brew upgrade`, `winget upgrade`, volver a correr el
  script, `docker compose pull`).

## 7. Pruebas

- **`internal/config`**: TLS por dominio, por archivos, las tres combinaciones inválidas,
  `SecureCookies` implícito y explícito, `HTTPAddr` implícito `:443`, `TrustedProxies`
  válidos e inválidos, `UpdateCheck`.
- **`internal/webtls`**: con un certificado autofirmado generado en el test, `Build`
  devuelve un `TLSConfig` que un `httptest.NewUnstartedServer` sirve y un cliente con la
  CA del test valida; `Redirect` devuelve 301 a `https://host/ruta?query`; dominio y
  archivos a la vez → error; con dominio, `TLSConfig.GetCertificate` no es nil y
  `NextProtos` incluye `acme-tls/1` (sin hablar con Let's Encrypt).
- **`cmd/splitstream`** (`main_test.go`): regresión «sin TLS nada cambia» (arranca con la
  configuración de hoy, `GET /healthz` por HTTP); con `TLSCertFile` autofirmado el panel
  responde por HTTPS, la cookie de login sale `Secure` y el listener de redirección
  contesta 301; `-healthcheck` con TLS.
- **`internal/httpapi`**: `clientip_test.go` (sin proxies la cabecera se ignora; con
  `127.0.0.1/32`, loopback + `X-Forwarded-For: 203.0.113.9` cuenta como esa IP y **no es
  local**; cadena de dos proxies con el primero de confianza; cabecera con basura;
  IPv6); el limitador clava por IP real; la purga; `statusDTO.panel` y `.update` en el
  test de nombres snake_case y en `dto_test`.
- **`internal/update`**: `httptest` con `tag_name` mayor, igual, menor, pre-release, JSON
  inválido, 403, timeout; `dev` no consulta (el servidor de prueba no recibe nada);
  `onNew` una vez por versión; `User-Agent` exacto y ninguna otra cabecera identificable.
- **CI**: job `install` (§3.4); guard `internal/httpapi` no importa `internal/webtls`;
  `shellcheck` sobre `deploy/install.sh` y los `render.sh` (está en `ubuntu-latest`).
- **Integración**: no hay camino nuevo del motor; el job de mediamtx no cambia.

## 8. Fuera de esta entrega

- HSTS, varios dominios, wildcard, DNS-01, certificados de cliente.
- Actualización automática del binario (solo el aviso).
- Paquetes `.deb`/`.rpm`, AUR, Scoop, Chocolatey: el script y los tres canales cubren la
  puerta; el resto, si alguien lo pide.
- Ajustes de TLS o dominio desde el panel: todo va por entorno (sin migración en esta
  entrega; `SchemaVersion` sigue en 6).
- Un catálogo central de `kind` de eventos: se anota como deuda para la v1.0 (G.3).
