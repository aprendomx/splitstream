> También en inglés → [README.md](README.md)

# Splitstream

Retransmisión RTMP self-hosted. Recibe un stream desde OBS y lo reenvía
simultáneamente a YouTube, Twitch, Facebook, Kick, X, TikTok o cualquier endpoint
RTMP/RTMPS genérico.

Un solo binario: servidor RTMP de ingesta, API HTTP y panel web, todo dentro. Sin
transcodificación — los paquetes se reenvían tal cual, así que el consumo de CPU es
despreciable y el de subida es `bitrate × número de destinos`.

> **Idioma.** El panel es bilingüe (español e inglés); se elige en el selector de la barra
> superior. La salida de la línea de comandos, el registro del servidor y el historial de
> eventos siguen en español.

## Estado

**v1.0.** Las seis fases están completas, y el motor está probado contra plataformas
reales —YouTube, Twitch y Facebook a la vez, sin descartes ni reconexiones durante quince
minutos seguidos— y el producto se instala descargando un archivo. La propia v1.0 no
añade funciones: quita riesgo. El contrato de la API queda congelado y documentado
([`docs/api.md`](docs/api.md), generado desde el código — un test falla si se
desalinean), la CI se vuelve estricta (linter, escaneo de vulnerabilidades y una
integración nocturna completa bloquean cada cambio), y la librería RTMP de la que depende
corre desde una copia parcheada y verificada contra el origen en vez de la dependencia
cruda. Nada de eso cambia el comportamiento de quien actualiza: mismo modelo de datos,
misma API, mismos valores por defecto.

| Fase | Contenido | Estado |
| --- | --- | --- |
| 1 | Config, cifrado, SQLite con migraciones, modelo de datos | ✅ |
| 2 | Ingesta RTMP, hub y un destino de punta a punta (RTMP y RTMPS) | ✅ |
| 3 | N destinos, cola con descarte por GOP, reconexión, métricas | ✅ |
| 4 | API HTTP completa + WebSocket | ✅ |
| 5 | Panel web | ✅ |
| 6 | Docker, systemd, documentación de operación | ✅ |

---

## Instalación

### Mac (Homebrew)

```bash
brew tap aprendomx/tap
brew trust aprendomx/tap      # Homebrew 6 lo exige para taps de terceros; en versiones anteriores no existe
brew install splitstream
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

| Tu equipo | Archivo |
| --- | --- |
| Mac con Apple Silicon (M1 y posteriores) | `…-macos-apple-silicon.tar.gz` |
| Mac con Intel | `…-macos-intel.tar.gz` |
| Linux de escritorio o servidor | `…-linux-x86_64.tar.gz` |
| Raspberry Pi 4/5, servidores ARM | `…-linux-arm64.tar.gz` |
| Windows | `…-windows-x86_64.zip` |

No hay instalador ni dependencias: es un único ejecutable con el panel dentro.

**macOS y Linux**

```bash
tar xzf splitstream-*.tar.gz
cd splitstream-*/
chmod +x splitstream
```

**En macOS verás este aviso la primera vez:**

> «Apple no pudo verificar que "splitstream" no contenga software malicioso.»

Es Gatekeeper. Los binarios no están firmados con un certificado de desarrollador de
Apple —eso cuesta una suscripción anual— así que el sistema los bloquea aunque el
programa sea correcto. Tienes dos formas de desbloquearlo:

```bash
# Quita la marca que el navegador puso al descargar
xattr -dr com.apple.quarantine splitstream-*-macos-apple-silicon
```

O sin terminal: **Ajustes del Sistema → Privacidad y seguridad**, baja hasta el aviso
sobre `splitstream` y pulsa **Abrir de todos modos**.

> El «clic derecho → Abrir» de toda la vida ya no siempre ofrece la opción en las
> versiones recientes de macOS. Si el menú no te la da, usa cualquiera de las dos vías de
> arriba.

**Windows**: descomprime el `.zip`. SmartScreen avisará de que el editor es desconocido;
elige **Más información → Ejecutar de todas formas**.

### Arranca

Doble clic sobre el ejecutable, o desde la terminal:

```bash
./splitstream
```

No hace falta configurar nada. La primera vez crea su clave maestra en un archivo
`splitstream.key` junto a la base de datos, y te lo dice:

```
  Se ha creado tu clave maestra:

      splitstream.key

  Cifra las claves de tus canales. RESPÁLDALA junto a la base de datos:
  si la pierdes, tendrás que volver a pegar la clave de cada plataforma.
```

> **Respalda los dos archivos juntos**, `splitstream.db` y `splitstream.key`. Copiar solo
> la base no sirve de nada: sin la clave, lo que hay dentro es ilegible.

Están uno al lado del otro a propósito, para que se muevan juntos. Eso también significa
que **quien tenga acceso a esa carpeta lo tiene todo**. En un equipo compartido o en un
servidor, pasa la clave por el entorno y guárdala en otro sitio:

```bash
./splitstream -genkey                       # imprime una clave nueva
export SPLITSTREAM_MASTER_KEY="la-que-imprimió"
./splitstream                               # la variable manda sobre el archivo
```

Después verás algo así:

```
  ┌───────────────────────────────────────────────────────────┐
  │  Splitstream todavía no está configurado                  │
  └───────────────────────────────────────────────────────────┘

  Abre el panel y elige tu contraseña:

      http://localhost:8080
```

Abre esa dirección y elige una contraseña. Ya está.

### Si lo instalas en un servidor

Cuando abres el panel **desde otro equipo**, el asistente pide un código que el propio
programa imprime al arrancar. Existe para que nadie que llegue antes que tú se quede con
tu servicio: quien puede leer la consola del servidor es quien puede reclamarlo.

Cámbialo mentalmente por esto: en un VPS, mira el código en la misma terminal donde
arrancaste el programa, o con `journalctl -u splitstream`.

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

---

## Configuración

Todo se controla con variables de entorno:

| Variable | Por defecto | Para qué |
| --- | --- | --- |
| `SPLITSTREAM_MASTER_KEY` | archivo `.key` junto a la base | 32 bytes en base64. Si no la pones, se crea un archivo de clave y se usa. La variable siempre manda |
| `SPLITSTREAM_HTTP_ADDR` | `:8080` | Dónde escucha el panel |
| `SPLITSTREAM_RTMP_ADDR` | `:1935` | Dónde escucha la ingesta de OBS |
| `SPLITSTREAM_DB_PATH` | `splitstream.db` | Archivo SQLite |
| `SPLITSTREAM_LOG_LEVEL` | `info` | `debug`, `info`, `warn` o `error` |
| `SPLITSTREAM_SECURE_COOKIES` | `false`; `true` con TLS integrado | `true` si sirves el panel por HTTPS |
| `SPLITSTREAM_METRICS_TOKEN` | vacío | Con valor, `/metrics` acepta `Authorization: Bearer`. Vacío: solo cookie de sesión |
| `SPLITSTREAM_RETENTION_DAYS` | `90` | Eventos y sesiones cerradas más viejos se borran. `0` desactiva |
| `SPLITSTREAM_RETENTION_MAX_EVENTS` | `50000` | Tope de filas en `events`. `0` desactiva |
| `SPLITSTREAM_RETENTION_MAX_CHAT` | `200000` | Tope de filas en `chat_messages`. `0` desactiva |
| `SPLITSTREAM_TWITCH_CLIENT_ID` | vacío | Vacío: el client_id incluido en el binario; pon el tuyo si registras tu propia app en dev.twitch.tv. Es público, no un secreto. **Hasta que la app de Splitstream esté registrada, conectar cuentas de Twitch necesita esta variable** |
| `SPLITSTREAM_YOUTUBE_CHAT_BUDGET` | `6000` | Unidades de cuota diarias tras las que el chat de YouTube se pausa solo (ver [`docs/youtube-credenciales.md`](docs/youtube-credenciales.md)) |
| `SPLITSTREAM_YOUTUBE_QUOTA` | `10000` | Cuota diaria del proyecto de Google Cloud; el límite real lo fija Google, aquí solo se declara para que el panel la enseñe junto al gasto en la barra de cuota del chat |
| `SPLITSTREAM_RECORDINGS_DIR` | `recordings/` junto a la base | Dónde se escriben los archivos de grabación |
| `SPLITSTREAM_TLS_DOMAIN` | vacío | Con valor, TLS integrado con Let's Encrypt para ese dominio; el panel pasa a `:443` |
| `SPLITSTREAM_TLS_CACHE_DIR` | `tls-cache/` junto a la base | Cuenta y certificados de Let's Encrypt |
| `SPLITSTREAM_TLS_CERT_FILE` / `SPLITSTREAM_TLS_KEY_FILE` | vacíos | Certificado propio en PEM, en vez del dominio |
| `SPLITSTREAM_TLS_REDIRECT_ADDR` | `:80` con TLS | Listener que redirige a HTTPS y atiende el reto de Let's Encrypt; `none` lo apaga |
| `SPLITSTREAM_TRUSTED_PROXIES` | vacío | CIDR o IP, separadas por comas, desde las que se cree `X-Forwarded-For` |
| `SPLITSTREAM_UPDATE_CHECK` | `true` | `false` apaga la consulta diaria de versión nueva |
| `SPLITSTREAM_RTMP_PRECOMMANDS` | `false` | Manda `releaseStream` y `FCPublish` por el stream de control antes de publicar. `FCUnpublish` al cerrar se manda siempre: lo que cambia la variable es que los tres salgan por el stream de control en vez de por el de datos. Apagado por defecto — Twitch y YouTube funcionan sin él. Enciéndelo (`true`) **solo si una plataforma lo pide** |

Comandos:

```bash
splitstream -genkey        # imprime una clave maestra nueva
splitstream -version       # imprime la versión
splitstream -setpassword   # cambia la contraseña del panel, leyéndola de stdin
splitstream -backup <ruta> # copia consistente de la base de datos, y sale
splitstream -healthcheck   # sale 0 si /healthz responde 200, si no 1
```

Para cambiar la contraseña sin que quede en el historial del shell:

```bash
read -rs PW && printf '%s' "$PW" | splitstream -setpassword && unset PW
```

### Vigilarlo desde fuera

- `GET /healthz` responde `200` si el proceso atiende y la base contesta. No necesita
  sesión. Es lo que consulta el `HEALTHCHECK` de la imagen de Docker.
- `GET /metrics` expone métricas en formato Prometheus: estado y bitrate de cada canal,
  descartes, reconexiones, entregas de avisos. Pide sesión o
  `Authorization: Bearer $SPLITSTREAM_METRICS_TOKEN`.

```yaml
# prometheus.yml
scrape_configs:
  - job_name: splitstream
    authorization: { credentials: TU_TOKEN }
    static_configs: [{ targets: ['127.0.0.1:8080'] }]
```

---

### Grabar las emisiones

Se activa desde **Ajustes → Grabación**. En cuanto se activa, cada sesión que llega por
RTMP se graba en `SPLITSTREAM_RECORDINGS_DIR` (por defecto `recordings/` junto a la
base), un directorio `sesion-<id>/` por sesión con uno o más archivos `.flv`. No hay
transcodificación: es el mismo mux que llega de OBS, así que grabar no le cuesta CPU al
resto de destinos.

- **Segmentos:** con «Minutos por segmento» en más de 0, la sesión se corta a archivos de
  ese tamaño; un corte de luz o un `kill -9` no cuesta más que el segmento en curso, los
  anteriores ya están cerrados y son reproducibles. Con 0 minutos, un solo archivo por
  sesión.
- **Tope y retención:** «Tope en GB» pone un límite duro; al llegar, el job diario
  `grabaciones` borra las grabaciones más antiguas hasta volver a estar debajo. «Días de
  retención» borra por fecha, pero si compiten los dos límites manda el de gigas.
- **El disco lento nunca frena el directo:** si el disco no da abasto para escribir al
  ritmo que entra, la grabación empieza a descartar vídeo (igual que un destino con la
  subida corta) y el chip «Grabando» del panel se pone en ámbar; los destinos que sí
  llegan a tiempo no se enteran.
- **Descargar y borrar:** desde la página «Grabaciones» del panel, por segmento.
- **Pasar a MP4** para editar o subir a otro sitio:

  ```bash
  ffmpeg -i x.flv -c copy x.mp4
  ```

---

### Emitir desde el teléfono

Abre el panel en el teléfono o el portátil, entra en **Cámara**, permite la cámara y el
micrófono, elige la calidad y pulsa **Emitir**. El navegador codifica H.264 y AAC con
WebCodecs y se los manda al binario, que los reenvía exactamente igual que a OBS: sigue
sin transcodificar, y la grabación, la vista previa y las métricas funcionan igual. OBS y
la cámara se turnan: mientras una emite, la otra es rechazada.

Lo que imponen los navegadores, y Splitstream no puede cambiar:

- **Necesita HTTPS.** Los navegadores solo exponen la cámara y los codificadores en un
  origen seguro. Abrir el panel por `http://` en una dirección de la LAN no da cámara;
  solo `localhost` se libra. Usa el TLS integrado (`SPLITSTREAM_TLS_DOMAIN`), un proxy
  con certificado o un túnel que termine HTTPS (Tailscale con `tailscale cert`,
  Cloudflare Tunnel).
- **Chrome, Edge o Safari.** Firefox y Chrome en Linux codifican H.264 pero no tienen
  codificador AAC, y las plataformas no aceptan otra cosa. iOS necesita Safari 26 o
  posterior.
- **La página, delante.** Bloquear la pantalla o cambiar de app suspende la cámara; la
  emisión se para y el panel dice por qué.
- **Orientación y calidad se eligen antes de emitir.** Cambiarlas a mitad la corta.
- **No es una webcam enchufada al servidor**, ni WebRTC/WHIP: las dos exigirían meter
  captura o transcodificación en el binario.

---

## Con Docker

```bash
mkdir splitstream && cd splitstream
curl -fsSLO https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/env.example -o .env

# Genera la clave maestra y pégala en .env
docker compose run --rm splitstream -genkey

docker compose up -d
```

(el `compose` ya apunta a `ghcr.io/aprendomx/splitstream:latest`; no hace falta clonar el repo).

La imagen pesa unos 18 MB y no lleva ni shell: es el binario sobre `scratch`, con los
certificados raíz —que hacen falta para los destinos `rtmps://`— y nada más. Corre como
usuario sin privilegios y con el sistema de archivos en solo lectura salvo su base de
datos.

**Desde Docker, el asistente te pedirá el código del primer arranque.** Es normal: la
petición llega por la red puente del contenedor y no por `localhost`, así que el servicio
la trata como si viniera de otra máquina. Míralo con:

```bash
docker compose logs | grep -A2 "te pedirá este código"
```

o pon la IP del host de Docker en `SPLITSTREAM_TRUSTED_PROXIES` si hay un proxy delante
que manda `X-Forwarded-For`.

El panel se publica solo en `127.0.0.1:8080` a propósito: sin TLS, tu contraseña viaja
en claro. Para alcanzarlo desde fuera tienes las mismas dos vías que sin Docker: el TLS
integrado (pon `SPLITSTREAM_TLS_DOMAIN` en `.env` y descomenta `80:80` y `443:443` en el
compose; ver «Ponerlo en internet») o un proxy con HTTPS delante (ver «Detrás de un
proxy»).

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

## Dejarlo funcionando siempre

### Linux con systemd

Hay una unidad lista en [`deploy/splitstream.service`](deploy/splitstream.service), con
las instrucciones de instalación en su cabecera. Lo esencial:

`install.sh` ofrece hacer todo esto por ti. A mano:

```bash
sudo install -d -o splitstream -g splitstream /var/lib/splitstream
sudo install -d -m 700 /etc/splitstream
printf 'SPLITSTREAM_MASTER_KEY=%s\n' "$(splitstream -genkey)" \
  | sudo tee /etc/splitstream/env > /dev/null
sudo chmod 600 /etc/splitstream/env
sudo install -m 644 deploy/splitstream.service /etc/systemd/system/
sudo systemctl enable --now splitstream

# El código del primer arranque:
journalctl -u splitstream | grep -A2 "te pedirá este código"
```

La unidad da 30 segundos de margen al apagado. No es adorno: al recibir `SIGTERM`, el
servicio manda `FCUnpublish` a cada destino, espera la gracia de 3 segundos del diseño y
cierra la sesión en la base. Matarlo antes deja sesiones abiertas para siempre.

### macOS

Guarda esto como `~/Library/LaunchAgents/mx.aprendo.splitstream.plist`, cambiando las
rutas y la clave, y cárgalo con `launchctl load`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>mx.aprendo.splitstream</string>
  <key>ProgramArguments</key><array>
    <string>/usr/local/bin/splitstream</string>
  </array>
  <key>WorkingDirectory</key><string>/Users/TU_USUARIO/splitstream</string>
  <key>EnvironmentVariables</key><dict>
    <key>SPLITSTREAM_MASTER_KEY</key><string>TU_CLAVE_MAESTRA</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
```

---

## Cómo se usa

El [manual de usuario](docs/manual-de-usuario.md) explica cómo configurar OBS, vincular
canales y qué hacer cuando uno falla. Incluye las particularidades de cada plataforma que
descubrimos probando contra ellas de verdad. También cómo conectar tu cuenta de Twitch
para cambiar el título y la categoría en vivo y leer el chat desde el panel.

Con YouTube y Kick vas más lejos: conectando tu cuenta, Splitstream crea la emisión (o lee
la clave, en Kick) y te ahorra copiarla a mano, ver
[`docs/youtube-credenciales.md`](docs/youtube-credenciales.md) y
[`docs/kick-credenciales.md`](docs/kick-credenciales.md) para los pasos de cada consola.

Cómo se compara Splitstream con Restream, Castr, nginx-rtmp y MediaMTX, con precios y
fuentes: [`docs/comparativa.md`](docs/comparativa.md).

---

## Desarrollo

Hace falta Go 1.26+ y Node 20+. Docker y ffmpeg solo para los tests de integración.

```bash
make build             # panel + binario
make build-go          # solo el binario, con el panel ya compilado
make test              # tests con -race
make vet
make lint              # golangci-lint, la misma versión que la CI
make vuln              # govulncheck, la misma versión que la CI
make sinks-up          # levanta dos mediamtx locales
make test-integration  # punta a punta contra ellos; necesita ffmpeg y ffprobe
```

`lint` y `vuln` son jobs obligatorios de la CI (`.github/workflows/ci.yml`); las
excepciones del linter están justificadas una a una en `.golangci.yml`. Un workflow
nocturno (`.github/workflows/nightly.yml`, también ejecutable a mano con
`workflow_dispatch`) corre la integración completa, `deploy/migrate-test.sh` contra la
release anterior y, solo si existen los secretos de prueba de plataforma, un humo de
cinco minutos contra una plataforma real.

Para trabajar en el panel con recarga en caliente, arranca el binario y aparte:

```bash
cd web && npm run dev
```

Vite hace de proxy hacia la API en `:8099`, así que la sesión funciona igual que en
producción.

El [documento de diseño](docs/superpowers/specs/2026-09-01-rtmp-relay-design.md) explica
la arquitectura, y los [planes de implementación](docs/superpowers/plans/) el detalle de
cada fase, incluidos los errores que cometimos y cómo se corrigieron.

Si tocas el esquema de la base, [docs/migraciones.md](docs/migraciones.md) tiene las reglas
y cómo probar la migración contra una base real de la versión anterior.

Si tocas una ruta o un DTO de `internal/httpapi`, regenera el contrato de la API:

```bash
go test ./internal/httpapi/ -run APIContract -update
```

`TestAPIContractDocIsCurrent` compara [docs/api.md](docs/api.md) byte a byte contra la
tabla de rutas y los DTO, y falla la CI si queda desactualizado.

Si tocas `third_party/go-rtmp`, edítalo como un parche (`patches/000N-*.diff`) y
actualiza [`third_party/go-rtmp/UPSTREAM.md`](third_party/go-rtmp/UPSTREAM.md) — su
propia sección «Regenerar» tiene los pasos exactos — para que
`TestGoRTMPCopyMatchesUpstreamPlusPatches` siga confirmando que la copia es el origen más
exactamente esos parches.

---

## Alcance

Retransmisión desde OBS o desde la cámara del navegador, y grabación local. Graba en FLV, sin transcodificar: lo que entra por RTMP
se muxea tal cual a disco, igual que se reenvía tal cual a cada destino. Sin
transcodificación, chat de **lectura** en el panel, por plataforma y solo donde la API lo
permite (hoy Twitch, YouTube y Kick — este último solo con el panel accesible por URL
pública HTTPS); escribir y moderar quedan fuera. Facebook, X y TikTok se quedan en «solo
retransmitir»: Facebook exige verificación de negocio para su API de canal, y X y TikTok
no tienen una API viable para esto. Sin multi-tenant. Si necesitas cambiar la resolución o
el bitrate por destino, esto no es la herramienta: hace falta transcodificar, y eso es
otro producto.

## Licencia

MIT. Las dependencias y sus licencias están en el panel, en **Créditos**.
