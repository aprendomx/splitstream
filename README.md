> Also in Spanish → [README.es.md](README.es.md)

# Splitstream

Self-hosted RTMP restreaming. It takes one stream from OBS and forwards it
simultaneously to YouTube, Twitch, Facebook, Kick, X, TikTok or any generic RTMP/RTMPS
endpoint.

A single binary: RTMP ingest server, HTTP API and web panel, all inside. No
transcoding — packets are forwarded as they arrive, so CPU usage is negligible and
upload usage is `bitrate × number of destinations`.

> **Language.** The web panel is bilingual (English and Spanish); pick your language from
> the selector in the top bar. The command line output, the server log and the persisted
> event log are still in Spanish — that is why the console examples below appear in
> Spanish, exactly as the program prints them.

## Status

**v1.0.** All six phases are complete, and the engine has been tested against real
platforms —YouTube, Twitch and Facebook at the same time, with no drops and no
reconnections for fifteen minutes straight— and the product installs by downloading one
file. v1.0 itself doesn't add features: it closes risk instead. The API contract is
frozen and documented ([`docs/api.md`](docs/api.md), generated from the code — a test
fails if they drift apart), CI is strict (linter, vulnerability scan and a nightly full
integration run block every change), and the RTMP library this depends on runs from a
patched, upstream-verified copy instead of the raw dependency. None of that changes
behavior for anyone updating: same data model, same API, same defaults.

| Phase | Contents | Status |
| --- | --- | --- |
| 1 | Config, encryption, SQLite with migrations, data model | ✅ |
| 2 | RTMP ingest, hub and one destination end to end (RTMP and RTMPS) | ✅ |
| 3 | N destinations, queue with GOP-level dropping, reconnection, metrics | ✅ |
| 4 | Complete HTTP API + WebSocket | ✅ |
| 5 | Web panel | ✅ |
| 6 | Docker, systemd, operations documentation | ✅ |

---

## Install

### Mac (Homebrew)

```bash
brew tap aprendomx/tap
brew trust aprendomx/tap      # Homebrew 6 requires it for third-party taps; earlier versions don't have it
brew install splitstream
splitstream
```

Homebrew clears the quarantine flag: no Gatekeeper warning. To keep it running all the
time, `brew services start splitstream` (database and key in
`$(brew --prefix)/var/splitstream`, log in `$(brew --prefix)/var/log/splitstream.log`).

### Windows (winget)

```powershell
winget install aprendomx.Splitstream
splitstream
```

No SmartScreen: winget verifies the package by its checksum. It installs as a portable
binary and ends up on your `PATH`.

### Linux, or macOS without Homebrew (script)

```bash
curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/install.sh | sh
```

The script detects your system, downloads the latest release, **verifies the checksum**
and copies the binary to `/usr/local/bin`. If that needs `sudo`, it shows you the command
and asks first. On Linux with systemd it offers to install it as a service. You can read
the whole thing in [`deploy/install.sh`](deploy/install.sh); to install into your own
folder without `sudo`:

```bash
curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/install.sh \
  | SPLITSTREAM_INSTALL_DIR=$HOME/.local/bin sh
```

### By hand

Go to [the releases](https://github.com/aprendomx/splitstream/releases) and download the
file for your platform:

| Your machine | File |
| --- | --- |
| Mac with Apple Silicon (M1 and later) | `…-macos-apple-silicon.tar.gz` |
| Mac with Intel | `…-macos-intel.tar.gz` |
| Desktop or server Linux | `…-linux-x86_64.tar.gz` |
| Raspberry Pi 4/5, ARM servers | `…-linux-arm64.tar.gz` |
| Windows | `…-windows-x86_64.zip` |

There is no installer and there are no dependencies: it is a single executable with the
panel inside.

**macOS and Linux**

```bash
tar xzf splitstream-*.tar.gz
cd splitstream-*/
chmod +x splitstream
```

**On macOS you will see this warning the first time:**

> "Apple could not verify "splitstream" is free of malware."

That is Gatekeeper. The binaries are not signed with an Apple developer certificate —that
costs a yearly subscription— so the system blocks them even when the program is fine. You
have two ways to unblock it:

```bash
# Remove the flag the browser set when downloading
xattr -dr com.apple.quarantine splitstream-*-macos-apple-silicon
```

Or without a terminal: **System Settings → Privacy & Security**, scroll down to the notice
about `splitstream` and click **Open Anyway**.

> The old "right click → Open" no longer always offers the option in recent versions of
> macOS. If the menu doesn't give it to you, use either of the two routes above.

**Windows**: unzip the `.zip`. SmartScreen will warn that the publisher is unknown; choose
**More info → Run anyway**.

### Run it

Double-click the executable, or from the terminal:

```bash
./splitstream
```

There is nothing to configure. The first time it creates its master key in a
`splitstream.key` file next to the database, and tells you so:

```
  Se ha creado tu clave maestra:

      splitstream.key

  Cifra las claves de tus canales. RESPÁLDALA junto a la base de datos:
  si la pierdes, tendrás que volver a pegar la clave de cada plataforma.
```

> **Back up both files together**, `splitstream.db` and `splitstream.key`. Copying the
> database alone is useless: without the key, what's inside it is unreadable.

They sit next to each other on purpose, so that they travel together. That also means
**whoever has access to that folder has everything**. On a shared machine or on a server,
pass the key through the environment and keep it somewhere else:

```bash
./splitstream -genkey                       # prints a new key
export SPLITSTREAM_MASTER_KEY="the-one-it-printed"
./splitstream                               # the variable wins over the file
```

Then you will see something like this:

```
  ┌───────────────────────────────────────────────────────────┐
  │  Splitstream todavía no está configurado                  │
  └───────────────────────────────────────────────────────────┘

  Abre el panel y elige tu contraseña:

      http://localhost:8080
```

Open that address and choose a password. That's it.

### On a server

When you open the panel **from another machine**, the wizard asks for a code that the
program itself prints at startup. It exists so that nobody who gets there before you can
take over your service: whoever can read the server console is whoever can claim it.

In practice: on a VPS, look for the code in the same terminal where you started the
program, or with `journalctl -u splitstream`.

If the panel is going to be reachable from the internet it has to go over HTTPS: without
TLS, the password travels in the clear. You have two routes: the built-in TLS (next
section) or a proxy in front (the one after that).

### On the internet

With a domain pointing at the machine and ports 80 and 443 open, the binary requests and
renews the certificate on its own, with Let's Encrypt:

```bash
SPLITSTREAM_TLS_DOMAIN=relay.ejemplo.com splitstream
```

With that the panel listens on `:443`, `:80` redirects to HTTPS, the session cookie is
sent `Secure` and the status view shows the public URL. Certificates are stored in
`tls-cache/` next to the database: back it up with it, and **don't delete it to "try
again"**: Let's Encrypt allows 5 certificates per week per domain, and a restart loop
without a cache burns through them.

- As a systemd service, uncomment `AmbientCapabilities=CAP_NET_BIND_SERVICE` in the unit:
  that is what allows binding 80 and 443 without root.
- With Docker nothing is needed: publish `80:80` and `443:443` (there is a commented
  example in `deploy/docker-compose.yml`).
- If the certificate never arrives, the panel's event log shows `tls_certificate_error`
  with the reason (almost always: the domain doesn't point here, or port 80 is closed).

With your own certificate, instead of the domain:

```bash
SPLITSTREAM_TLS_CERT_FILE=/etc/ssl/relay.crt SPLITSTREAM_TLS_KEY_FILE=/etc/ssl/relay.key splitstream
```

### Behind a proxy

If you prefer Caddy or nginx in front, they terminate TLS and the binary stays on `:8080`.
Two things:

1. `SPLITSTREAM_SECURE_COOKIES=true`, so that the cookie is not sent without `Secure`.
2. `SPLITSTREAM_TRUSTED_PROXIES` with the proxy's IP, so that the login rate limiter and
   the first-run wizard see the real IP and not the proxy's. Without this, one failed
   attempt by anyone punishes everyone, and the wizard thinks everything is remote.

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

With Docker and the proxy on the host, the bridge network is usually `172.16.0.0/12`.
**Never put `0.0.0.0/0`**: trusting everyone disables the rate limiter and turns anyone
into a "local" user with one header.

---

## Configuration

Everything is driven by environment variables:

| Variable | Default | What for |
| --- | --- | --- |
| `SPLITSTREAM_MASTER_KEY` | `.key` file next to the database | 32 bytes in base64. If you don't set it, a key file is created and used. The variable always wins |
| `SPLITSTREAM_HTTP_ADDR` | `:8080` | Where the panel listens |
| `SPLITSTREAM_RTMP_ADDR` | `:1935` | Where the OBS ingest listens |
| `SPLITSTREAM_DB_PATH` | `splitstream.db` | SQLite file |
| `SPLITSTREAM_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `SPLITSTREAM_SECURE_COOKIES` | `false`; `true` with built-in TLS | `true` if you serve the panel over HTTPS |
| `SPLITSTREAM_METRICS_TOKEN` | empty | With a value, `/metrics` accepts `Authorization: Bearer`. Empty: session cookie only |
| `SPLITSTREAM_RETENTION_DAYS` | `90` | Events and closed sessions older than this are deleted. `0` disables it |
| `SPLITSTREAM_RETENTION_MAX_EVENTS` | `50000` | Row cap in `events`. `0` disables it |
| `SPLITSTREAM_RETENTION_MAX_CHAT` | `200000` | Row cap in `chat_messages`. `0` disables it |
| `SPLITSTREAM_TWITCH_CLIENT_ID` | empty | Empty: the client_id built into the binary; set your own if you register your own app at dev.twitch.tv. It is public, not a secret. **Until the Splitstream app is registered, connecting Twitch accounts needs this variable** |
| `SPLITSTREAM_YOUTUBE_CHAT_BUDGET` | `6000` | Daily quota units after which YouTube chat pauses itself (see [`docs/youtube-credenciales.md`](docs/youtube-credenciales.md)) |
| `SPLITSTREAM_YOUTUBE_QUOTA` | `10000` | Daily quota of the Google Cloud project; the real limit is set by Google, here it is only declared so that the panel can show it next to the spend in the chat quota bar |
| `SPLITSTREAM_RECORDINGS_DIR` | `recordings/` next to the database | Where recording files are written |
| `SPLITSTREAM_TLS_DOMAIN` | empty | With a value, built-in TLS with Let's Encrypt for that domain; the panel moves to `:443` |
| `SPLITSTREAM_TLS_CACHE_DIR` | `tls-cache/` next to the database | Let's Encrypt account and certificates |
| `SPLITSTREAM_TLS_CERT_FILE` / `SPLITSTREAM_TLS_KEY_FILE` | empty | Your own certificate in PEM, instead of the domain |
| `SPLITSTREAM_TLS_REDIRECT_ADDR` | `:80` with TLS | Listener that redirects to HTTPS and serves the Let's Encrypt challenge; `none` turns it off |
| `SPLITSTREAM_TRUSTED_PROXIES` | empty | CIDRs or IPs, comma separated, whose `X-Forwarded-For` is believed |
| `SPLITSTREAM_UPDATE_CHECK` | `true` | `false` turns off the daily check for a new version |
| `SPLITSTREAM_RTMP_PRECOMMANDS` | `false` | Sends `releaseStream` and `FCPublish` over the control stream before publishing. `FCUnpublish` on close is always sent: what this variable changes is that all three go over the control stream instead of the data stream. Off by default — Twitch and YouTube work without it. Turn it on (`true`) **only if a platform asks for it** |

Commands:

```bash
splitstream -genkey        # prints a new master key
splitstream -version       # prints the version
splitstream -setpassword   # changes the panel password, reading it from stdin
splitstream -backup <path> # consistent copy of the database, then exits
splitstream -healthcheck   # exits 0 if /healthz answers 200, otherwise 1
```

To change the password without leaving it in your shell history:

```bash
read -rs PW && printf '%s' "$PW" | splitstream -setpassword && unset PW
```

### Watching it from outside

- `GET /healthz` answers `200` if the process is serving and the database responds. It
  needs no session. It is what the Docker image's `HEALTHCHECK` calls.
- `GET /metrics` exposes metrics in Prometheus format: state and bitrate of each channel,
  drops, reconnections, alert deliveries. It needs a session or
  `Authorization: Bearer $SPLITSTREAM_METRICS_TOKEN`.

```yaml
# prometheus.yml
scrape_configs:
  - job_name: splitstream
    authorization: { credentials: YOUR_TOKEN }
    static_configs: [{ targets: ['127.0.0.1:8080'] }]
```

---

### Recording your streams

It is enabled from **Settings → Recording**. As soon as it is on, every session that
arrives over RTMP is recorded into `SPLITSTREAM_RECORDINGS_DIR` (by default `recordings/`
next to the database), one `sesion-<id>/` directory per session with one or more `.flv`
files. There is no transcoding: it is the same mux that arrives from OBS, so recording
costs the other destinations no CPU.

- **Segments:** with "Minutes per segment" above 0, the session is cut into files of that
  size; a power cut or a `kill -9` costs you no more than the segment in progress, the
  previous ones are already closed and playable. With 0 minutes, one single file per
  session.
- **Cap and retention:** "Cap in GB" sets a hard limit; when it is reached, the daily
  `grabaciones` job deletes the oldest recordings until it is back under it. "Retention
  days" deletes by date, but if the two limits compete, the gigabyte one wins.
- **A slow disk never slows the live stream:** if the disk can't keep up with the incoming
  rate, the recording starts dropping video (just like a destination with a short upload)
  and the panel's "Recording" chip turns amber; the destinations that do keep up never
  notice.
- **Download and delete:** from the panel's "Recordings" page, segment by segment.
- **Convert to MP4** to edit or upload elsewhere:

  ```bash
  ffmpeg -i x.flv -c copy x.mp4
  ```

---

### Streaming from your phone

Open the panel on the phone or laptop, go to **Camera**, allow the camera and microphone,
pick the quality and press **Go live**. The browser encodes H.264 and AAC with WebCodecs
and sends them to the binary, which forwards them exactly as it forwards OBS: still no
transcoding, and recording, preview and metrics work the same. OBS and the camera take
turns: while one is live the other is refused.

What the browsers impose, and Splitstream cannot change:

- **Needs HTTPS.** Browsers only expose the camera and the encoders on a secure origin.
  Opening the panel over plain `http://` at a LAN address gives no camera; only
  `localhost` is exempt. Use the built-in TLS (`SPLITSTREAM_TLS_DOMAIN`), a proxy with a
  certificate, or a tunnel that terminates HTTPS (Tailscale with `tailscale cert`,
  Cloudflare Tunnel).
- **Chrome, Edge or Safari.** Firefox and Chrome on Linux encode H.264 but have no AAC
  encoder, and the platforms accept nothing else. iOS needs Safari 26 or later.
- **Keep the page in front.** Locking the screen or switching apps suspends the camera;
  the broadcast stops and the panel says why.
- **Pick orientation and quality before going live.** Changing them mid-stream stops it.
- **Not a webcam plugged into the server**, and not WebRTC/WHIP: both would need a
  capture or transcoding stack inside the binary.

---

## With Docker

```bash
mkdir splitstream && cd splitstream
curl -fsSLO https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/env.example -o .env

# Generate the master key and paste it into .env
docker compose run --rm splitstream -genkey

docker compose up -d
```

(the `compose` file already points at `ghcr.io/aprendomx/splitstream:latest`; there is no
need to clone the repo).

The image weighs about 18 MB and doesn't even carry a shell: it is the binary on
`scratch`, with the root certificates —needed for `rtmps://` destinations— and nothing
else. It runs as an unprivileged user and with a read-only filesystem except for its
database.

**From Docker, the wizard will ask you for the first-run code.** That is expected: the
request arrives over the container's bridge network and not over `localhost`, so the
service treats it as coming from another machine. Find it with:

```bash
docker compose logs | grep -A2 "te pedirá este código"
```

or put your Docker host's IP in `SPLITSTREAM_TRUSTED_PROXIES` if there is a proxy in front
sending `X-Forwarded-For`.

The panel is published only on `127.0.0.1:8080` on purpose: without TLS, your password
travels in the clear. To reach it from outside you have the same two routes as without
Docker: the built-in TLS (set `SPLITSTREAM_TLS_DOMAIN` in `.env` and uncomment `80:80` and
`443:443` in the compose file; see "On the internet") or a proxy with HTTPS in front (see
"Behind a proxy").

## Update

The panel warns you when there is a new version (one query to GitHub at startup and every
24 h, with the version as the only data; `SPLITSTREAM_UPDATE_CHECK=false` turns it off).
Nothing updates itself:

```bash
brew upgrade splitstream                    # Homebrew
winget upgrade aprendomx.Splitstream        # winget
curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/install.sh | sh   # script
docker compose pull && docker compose up -d # Docker
```

## Keep it running

### Linux with systemd

There is a ready-made unit in [`deploy/splitstream.service`](deploy/splitstream.service),
with the installation instructions in its header. The essentials:

`install.sh` offers to do all of this for you. By hand:

```bash
sudo install -d -o splitstream -g splitstream /var/lib/splitstream
sudo install -d -m 700 /etc/splitstream
printf 'SPLITSTREAM_MASTER_KEY=%s\n' "$(splitstream -genkey)" \
  | sudo tee /etc/splitstream/env > /dev/null
sudo chmod 600 /etc/splitstream/env
sudo install -m 644 deploy/splitstream.service /etc/systemd/system/
sudo systemctl enable --now splitstream

# The first-run code:
journalctl -u splitstream | grep -A2 "te pedirá este código"
```

The unit gives shutdown 30 seconds of grace. That is not decoration: on `SIGTERM`, the
service sends `FCUnpublish` to each destination, waits the 3-second grace period from the
design and closes the session in the database. Killing it earlier leaves sessions open
forever.

### macOS

Save this as `~/Library/LaunchAgents/mx.aprendo.splitstream.plist`, changing the paths and
the key, and load it with `launchctl load`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>mx.aprendo.splitstream</string>
  <key>ProgramArguments</key><array>
    <string>/usr/local/bin/splitstream</string>
  </array>
  <key>WorkingDirectory</key><string>/Users/YOUR_USER/splitstream</string>
  <key>EnvironmentVariables</key><dict>
    <key>SPLITSTREAM_MASTER_KEY</key><string>YOUR_MASTER_KEY</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
```

---

## How to use it

The [user manual](docs/manual-de-usuario.md) (in Spanish) explains how to set up OBS, how
to link channels and what to do when one fails. It includes the quirks of each platform
that we found by testing against them for real. It also covers how to connect your Twitch
account to change the title and the category live and read chat from the panel.

With YouTube and Kick you go further: by connecting your account, Splitstream creates the
broadcast (or reads the key, on Kick) and saves you from copying it by hand — see
[`docs/youtube-credenciales.md`](docs/youtube-credenciales.md) and
[`docs/kick-credenciales.md`](docs/kick-credenciales.md) for each console's steps.

How Splitstream compares with Restream, Castr, nginx-rtmp and MediaMTX, with prices and
sources: [`docs/comparison.md`](docs/comparison.md).

---

## Development

You need Go 1.26+ and Node 20+. Docker and ffmpeg only for the integration tests.

```bash
make build             # panel + binary
make build-go          # binary only, with the panel already built
make test              # tests with -race
make vet
make lint              # golangci-lint, same version as CI
make vuln              # govulncheck, same version as CI
make sinks-up          # brings up two local mediamtx
make test-integration  # end to end against them; needs ffmpeg and ffprobe
```

`lint` and `vuln` are required CI jobs (`.github/workflows/ci.yml`); exceptions in the
linter are justified one by one in `.golangci.yml`. A nightly workflow
(`.github/workflows/nightly.yml`, also runnable by hand with `workflow_dispatch`) runs
the full integration suite, `deploy/migrate-test.sh` against the previous release, and —
only if the platform test secrets exist — a five-minute smoke test against a real
platform.

To work on the panel with hot reload, start the binary and separately:

```bash
cd web && npm run dev
```

Vite proxies to the API on `:8099`, so the session works just like in production.

The [design document](docs/superpowers/specs/2026-09-01-rtmp-relay-design.md) explains the
architecture, and the [implementation plans](docs/superpowers/plans/) the detail of each
phase, including the mistakes we made and how we fixed them.

If you touch the database schema, [docs/migraciones.md](docs/migraciones.md) (Spanish) has
the rules and how to test the migration against a real database from the previous release.

If you touch a route or a DTO in `internal/httpapi`, regenerate the API contract doc:

```bash
go test ./internal/httpapi/ -run APIContract -update
```

`TestAPIContractDocIsCurrent` compares [docs/api.md](docs/api.md) against the route table
and the DTOs byte for byte, and fails CI if it's stale.

If you touch `third_party/go-rtmp`, edit it as a patch (`patches/000N-*.diff`) and update
[`third_party/go-rtmp/UPSTREAM.md`](third_party/go-rtmp/UPSTREAM.md) — its own
"Regenerar" section has the exact steps — so `TestGoRTMPCopyMatchesUpstreamPlusPatches`
keeps confirming the copy is upstream plus exactly those patches.

---

## Scope

Restreaming from OBS or from the browser camera, and local recording. It records in
FLV, without transcoding: what comes in over RTMP is muxed to disk as-is, the same way
it is forwarded as-is to each destination. No transcoding, **read-only** chat in the
panel, per platform and only where the API allows it (today Twitch, YouTube and Kick —
the last one only with the panel reachable at a public HTTPS URL); writing and moderating
are out of scope. Facebook, X and TikTok stay at "restream only": Facebook demands
business verification for its channel API, and X and TikTok have no viable API for this.
No multi-tenancy. If you need to change the resolution or the bitrate per destination,
this is not the tool: that requires transcoding, and that is a different product.

## License

MIT. The dependencies and their licenses are in the panel, under **Credits**.
