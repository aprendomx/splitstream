# Splitstream

Retransmisión RTMP self-hosted. Recibe un stream desde OBS y lo reenvía
simultáneamente a YouTube, Twitch, Facebook, Kick, X o cualquier endpoint
RTMP/RTMPS genérico.

Un solo binario: servidor RTMP de ingesta, API HTTP y panel web embebido.
Sin transcodificación — los paquetes se reenvían tal cual, así que el consumo de
CPU es despreciable y el de subida es `bitrate × número de destinos`.

## Estado

Fase 1 completa: configuración, cifrado, base de datos y esqueleto del binario.
Todavía no hay servidor RTMP ni panel web.

Ver [el documento de diseño](docs/superpowers/specs/2026-09-01-rtmp-relay-design.md)
para la arquitectura completa y [el plan de la fase 1](docs/superpowers/plans/2026-09-01-fase-1-esqueleto-y-cripto.md)
para el detalle de lo ya implementado.

## Desarrollo

```bash
make test    # tests con -race
make build   # binario en ./splitstream
make vet
```

## Configuración

| Variable | Default | Descripción |
| --- | --- | --- |
| `SPLITSTREAM_MASTER_KEY` | — | **Obligatoria.** 32 bytes en base64. Genérala con `splitstream -genkey`. |
| `SPLITSTREAM_HTTP_ADDR` | `:8080` | Dirección del panel y la API |
| `SPLITSTREAM_RTMP_ADDR` | `:1935` | Dirección del servidor RTMP de ingesta |
| `SPLITSTREAM_DB_PATH` | `splitstream.db` | Ruta del archivo SQLite |
| `SPLITSTREAM_LOG_LEVEL` | `info` | `debug`, `info`, `warn` o `error` |

> **Respalda `SPLITSTREAM_MASTER_KEY` aparte de la base de datos.** Cifra las claves
> de tus destinos: si la pierdes, son irrecuperables y hay que volver a pegarlas todas.

## Alcance

Solo retransmisión. Sin transcodificación, sin grabación, sin chat unificado y
sin multi-tenant.

## Licencia

MIT.
