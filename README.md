# Splitstream

Retransmisión RTMP self-hosted. Recibe un stream desde OBS y lo reenvía
simultáneamente a YouTube, Twitch, Facebook, Kick, X o cualquier endpoint
RTMP/RTMPS genérico.

Un solo binario: servidor RTMP de ingesta, API HTTP y panel web embebido.
Sin transcodificación — los paquetes se reenvían tal cual, así que el consumo de
CPU es despreciable y el de subida es `bitrate × número de destinos`.

## Estado

**Fase 3 de 6 completa.** El motor funciona de punta a punta: OBS publica en `:1935`,
el hub reparte a todos los destinos habilitados a la vez, cada uno con su cola acotada,
su reancle de timestamps y su reconexión con backoff. Falta la API HTTP, el WebSocket de
métricas y el panel web; hoy los destinos solo se pueden dar de alta por la base de datos.

| Fase | Contenido | Estado |
| --- | --- | --- |
| 1 | Config, cifrado, SQLite con migraciones, modelo de datos | ✅ |
| 2 | Ingesta RTMP, hub y un destino de punta a punta (RTMP y RTMPS) | ✅ |
| 3 | N destinos, cola con descarte por GOP, reconexión, métricas | ✅ |
| 4 | API HTTP completa + WebSocket | pendiente |
| 5 | Frontend Quasar | pendiente |
| 6 | Docker, systemd, documentación de operación | pendiente |

Ver [el documento de diseño](docs/superpowers/specs/2026-09-01-rtmp-relay-design.md)
para la arquitectura completa y los [planes de implementación](docs/superpowers/plans/)
para el detalle de cada fase.

## Desarrollo

```bash
make test              # tests con -race
make build             # binario en ./splitstream, con la versión del tag
make vet
make sinks-up          # mediamtx locales para los tests de integración
make test-integration  # requiere sinks-up, ffmpeg y ffprobe
```

## Configuración

| Variable | Default | Descripción |
| --- | --- | --- |
| `SPLITSTREAM_MASTER_KEY` | — | **Obligatoria.** 32 bytes en base64. Genérala con `splitstream -genkey`. |
| `SPLITSTREAM_HTTP_ADDR` | `:8080` | Dirección del panel y la API |
| `SPLITSTREAM_RTMP_ADDR` | `:1935` | Dirección del servidor RTMP de ingesta |
| `SPLITSTREAM_DB_PATH` | `splitstream.db` | Ruta del archivo SQLite |
| `SPLITSTREAM_LOG_LEVEL` | `info` | `debug`, `info`, `warn` o `error` |

`splitstream -version` imprime la versión y sale; `splitstream -genkey` imprime una
master key nueva. Ninguno de los dos toca la base de datos.

> **Respalda `SPLITSTREAM_MASTER_KEY` aparte de la base de datos.** Cifra las claves
> de tus destinos: si la pierdes, son irrecuperables y hay que volver a pegarlas todas.

## Alcance

Solo retransmisión. Sin transcodificación, sin grabación, sin chat unificado y
sin multi-tenant.

## Licencia

MIT.
