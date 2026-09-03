# Splitstream

Retransmisión RTMP self-hosted. Recibe un stream desde OBS y lo reenvía
simultáneamente a YouTube, Twitch, Facebook, Kick, X, TikTok o cualquier endpoint
RTMP/RTMPS genérico.

Un solo binario: servidor RTMP de ingesta, API HTTP y panel web, todo dentro. Sin
transcodificación — los paquetes se reenvían tal cual, así que el consumo de CPU es
despreciable y el de subida es `bitrate × número de destinos`.

## Estado

**Las seis fases están completas.** El motor está probado contra plataformas reales
—YouTube, Twitch y Facebook a la vez, sin descartes ni reconexiones durante quince minutos
seguidos— y el producto se instala descargando un archivo.

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

### Descarga el binario

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
xattr -dr com.apple.quarantine splitstream-v0.5.0-macos-apple-silicon
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

Y si el panel va a ser accesible desde internet, **ponlo detrás de HTTPS** con un proxy
como Caddy o nginx, y activa `SPLITSTREAM_SECURE_COOKIES=true`. Sin TLS, la contraseña
viaja en claro.

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
| `SPLITSTREAM_SECURE_COOKIES` | `false` | `true` si sirves el panel por HTTPS |

Comandos:

```bash
splitstream -genkey        # imprime una clave maestra nueva
splitstream -version       # imprime la versión
splitstream -setpassword   # cambia la contraseña del panel, leyéndola de stdin
```

Para cambiar la contraseña sin que quede en el historial del shell:

```bash
read -rs PW && printf '%s' "$PW" | splitstream -setpassword && unset PW
```

---

## Con Docker

```bash
git clone https://github.com/aprendomx/splitstream && cd splitstream
cp deploy/env.example deploy/.env

# Genera la clave maestra y pégala en deploy/.env
docker compose -f deploy/docker-compose.yml run --rm splitstream -genkey

docker compose -f deploy/docker-compose.yml up -d
```

La imagen pesa unos 18 MB y no lleva ni shell: es el binario sobre `scratch`, con los
certificados raíz —que hacen falta para los destinos `rtmps://`— y nada más. Corre como
usuario sin privilegios y con el sistema de archivos en solo lectura salvo su base de
datos.

**Desde Docker, el asistente te pedirá el código del primer arranque.** Es normal: la
petición llega por la red puente del contenedor y no por `localhost`, así que el servicio
la trata como si viniera de otra máquina. Míralo con:

```bash
docker compose -f deploy/docker-compose.yml logs | grep -A2 "te pedirá este código"
```

El panel se publica solo en `127.0.0.1:8080` a propósito. Si quieres alcanzarlo desde
fuera, ponlo detrás de un proxy con HTTPS en lugar de abrir el puerto: sin TLS, tu
contraseña viaja en claro.

## Dejarlo funcionando siempre

### Linux con systemd

Hay una unidad lista en [`deploy/splitstream.service`](deploy/splitstream.service), con
las instrucciones de instalación en su cabecera. Lo esencial:

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
descubrimos probando contra ellas de verdad.

---

## Desarrollo

Hace falta Go 1.25+ y Node 20+. Docker y ffmpeg solo para los tests de integración.

```bash
make build             # panel + binario
make build-go          # solo el binario, con el panel ya compilado
make test              # tests con -race
make vet
make sinks-up          # levanta dos mediamtx locales
make test-integration  # punta a punta contra ellos; necesita ffmpeg y ffprobe
```

Para trabajar en el panel con recarga en caliente, arranca el binario y aparte:

```bash
cd web && npm run dev
```

Vite hace de proxy hacia la API en `:8099`, así que la sesión funciona igual que en
producción.

El [documento de diseño](docs/superpowers/specs/2026-09-01-rtmp-relay-design.md) explica
la arquitectura, y los [planes de implementación](docs/superpowers/plans/) el detalle de
cada fase, incluidos los errores que cometimos y cómo se corrigieron.

---

## Alcance

Solo retransmisión. Sin transcodificación, sin grabación, sin chat unificado y sin
multi-tenant. Si necesitas cambiar la resolución o el bitrate por destino, esto no es la
herramienta: hace falta transcodificar, y eso es otro producto.

## Licencia

MIT. Las dependencias y sus licencias están en el panel, en **Créditos**.
