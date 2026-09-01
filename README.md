# Splitstream

Retransmisión RTMP self-hosted. Recibe un stream desde OBS y lo reenvía
simultáneamente a YouTube, Twitch, Facebook, Kick, X o cualquier endpoint
RTMP/RTMPS genérico.

Un solo binario: servidor RTMP de ingesta, API HTTP y panel web embebido.
Sin transcodificación — los paquetes se reenvían tal cual, así que el consumo de
CPU es despreciable y el de subida es `bitrate × número de destinos`.

> **Estado:** en diseño. Todavía no hay implementación.
> Ver [el documento de diseño](docs/superpowers/specs/2026-09-01-rtmp-relay-design.md).

## Alcance

Solo retransmisión. Sin transcodificación, sin grabación, sin chat unificado y
sin multi-tenant.

## Licencia

MIT.
