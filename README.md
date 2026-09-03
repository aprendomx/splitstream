# Splitstream

Retransmisión RTMP self-hosted. Recibe un stream desde OBS y lo reenvía
simultáneamente a YouTube, Twitch, Facebook, Kick, X, TikTok o cualquier endpoint
RTMP/RTMPS genérico.

Un solo binario: servidor RTMP de ingesta, API HTTP y panel web, todo dentro. Sin
transcodificación — los paquetes se reenvían tal cual, así que el consumo de CPU es
despreciable y el de subida es `bitrate × número de destinos`.

## Estado

**Fase 5 de 6 en curso.** El motor está terminado y probado contra plataformas reales
—YouTube, Twitch y Facebook a la vez, sin descartes ni reconexiones—. La API y el panel
web funcionan; falta pulir el panel y la fase 6 de empaquetado.

| Fase | Contenido | Estado |
| --- | --- | --- |
| 1 | Config, cifrado, SQLite con migraciones, modelo de datos | ✅ |
| 2 | Ingesta RTMP, hub y un destino de punta a punta (RTMP y RTMPS) | ✅ |
| 3 | N destinos, cola con descarte por GOP, reconexión, métricas | ✅ |
| 4 | API HTTP completa + WebSocket | ✅ |
| 5 | Panel web | en curso |
| 6 | Docker, systemd, documentación de operación | pendiente |

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

En macOS, la primera vez el sistema lo bloqueará por no estar firmado. Ábrelo con
**clic derecho → Abrir**, o quítale la marca de cuarentena:

```bash
xattr -d com.apple.quarantine splitstream
```

**Windows**: descomprime el `.zip`. SmartScreen avisará de que el editor es desconocido;
elige **Más información → Ejecutar de todas formas**.

### Genera tu clave maestra

Cifra las claves de tus canales. Se genera una vez y **hay que guardarla**:

```bash
./splitstream -genkey
```

Copia lo que imprime. Es la variable `SPLITSTREAM_MASTER_KEY`.

> **Respáldala aparte de la base de datos.** Si la pierdes, las claves de tus canales son
> irrecuperables por diseño y hay que volver a pegarlas todas.

### Arranca

**macOS y Linux**

```bash
export SPLITSTREAM_MASTER_KEY="lo-que-imprimió-genkey"
./splitstream
```

**Windows (PowerShell)**

```powershell
$env:SPLITSTREAM_MASTER_KEY = "lo-que-imprimió-genkey"
.\splitstream.exe
```

La primera vez verás algo así:

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
| `SPLITSTREAM_MASTER_KEY` | — | **Obligatoria.** 32 bytes en base64. Genérala con `-genkey` |
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

## Dejarlo funcionando siempre

### Linux con systemd

```ini
# /etc/systemd/system/splitstream.service
[Unit]
Description=Splitstream
After=network-online.target

[Service]
Type=simple
User=splitstream
WorkingDirectory=/var/lib/splitstream
ExecStart=/usr/local/bin/splitstream
Restart=always
RestartSec=5

# La clave maestra va en un archivo con permisos 600, no en esta unidad: lo que se
# escribe aquí lo puede leer cualquiera con `systemctl cat`.
EnvironmentFile=/etc/splitstream/env

# Endurecimiento básico. Solo necesita escribir su base de datos.
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/splitstream

[Install]
WantedBy=multi-user.target
```

```bash
sudo install -d -o splitstream -g splitstream /var/lib/splitstream
sudo install -d -m 700 /etc/splitstream
printf 'SPLITSTREAM_MASTER_KEY=%s\n' "$(splitstream -genkey)" \
  | sudo tee /etc/splitstream/env > /dev/null
sudo chmod 600 /etc/splitstream/env
sudo systemctl enable --now splitstream
journalctl -u splitstream -f
```

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
