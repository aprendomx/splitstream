> Also in English → [comparison.md](comparison.md)

# Splitstream frente a Restream, Castr, nginx-rtmp y MediaMTX

## 1. Qué compara este documento y qué no

**Escrito el 2026-09-13.** Los precios y los planes de los servicios de pago cambian con
frecuencia: los de aquí son los que estaban publicados a esa fecha y hay que comprobarlos
en la página de precios de cada producto antes de tomar una decisión con ellos.

Este documento lo escriben los autores de Splitstream. Por eso no emite juicios de valor
propios: no dice cuál es mejor, más fácil ni más potente. Dice qué hace cada uno, qué
cuesta y por dónde pasa tu vídeo, y deja la comparación a quien lee. Cuando reproduce un
juicio ajeno —la columna «Costo de entrada» del §5 es del roadmap— se dice de quién es.

**Cuando un dato no se puede verificar en la documentación pública del producto, la celda
dice «no documentado».** No se rellena por aproximación ni de memoria. Una celda «no
documentado» significa «compruébalo tú en la fuente», no «no existe».

Las fuentes están al final, con una letra por producto ([S], [R], [C], [N], [M]), y cada
tabla la cita en su última columna.

Los cinco productos no son la misma clase de cosa, y eso condiciona todo lo demás:

- **Splitstream**, **nginx-rtmp** y **MediaMTX** son software que instalas y ejecutas tú.
- **Restream** y **Castr** son servicios en la nube con cuenta y suscripción.

---

## 2. Qué hace cada uno

| Producto | Relay a varios destinos | Grabación local | Título y chat de plataformas | Panel web | Instalación | Dónde corre | Fuente |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **Splitstream** | Sí: N destinos RTMP/RTMPS a la vez, sin transcodificar | Sí: FLV en tu disco, con segmentos, tope en GB y retención | Título en Twitch, YouTube y Kick con la cuenta conectada; chat de lectura en esas tres | Sí, dentro del mismo binario | Binario único: Homebrew, winget, script o Docker | Tu equipo o tu servidor | [S] |
| **Restream** | Sí, a las plataformas de su catálogo | No documentado | Sí: chat unificado y cambio de título en los destinos que lo permiten | Sí, aplicación web | Cuenta en su web; no se instala nada | Sus servidores | [R] |
| **Castr** | Sí, a las plataformas de su catálogo | No documentado | No documentado | Sí, aplicación web | Cuenta en su web; no se instala nada | Sus servidores | [C] |
| **nginx-rtmp** (módulo `rtmp` de nginx) | Sí: una directiva `push` por destino | Sí: directiva `record`, en FLV | No | No; expone una página de estadísticas (`stat`) en XML con hoja XSL | Compilar nginx con `--add-module` | Tu equipo o tu servidor | [N] |
| **MediaMTX** | No como función propia: se hace lanzando `ffmpeg` desde `runOnReady` | Sí: `record: yes`, en fMP4 o MPEG-TS | No | No; tiene API HTTP de control y endpoint de métricas | Binario único | Tu equipo o tu servidor | [M] |

Dos notas sobre los dos autoalojados que no son Splitstream:

- El `push` de nginx-rtmp habla RTMP, no RTMPS. Los destinos que exigen TLS (Facebook, por
  ejemplo) necesitan un túnel delante, del tipo `stunnel` [N].
- MediaMTX es un servidor multiprotocolo (RTSP, RTMP, HLS, WebRTC, SRT). Reenviar a varias
  plataformas a la vez se resuelve con procesos externos, no con una lista de destinos
  [M].

---

## 3. Qué cuesta

| Producto | Plan gratuito | Primer plan de pago | Fuente |
| --- | --- | --- | --- |
| **Splitstream** | Todo el producto. Licencia MIT | No hay. El costo es tu equipo y tu subida: `bitrate × número de destinos` | [S] |
| **Restream** | Publica un plan gratuito. Qué incluye exactamente (destinos, marcas de agua, horas) cambia y no se reproduce aquí | No documentado a esta fecha; el precio está en su página de precios | [R] |
| **Castr** | No documentado | No documentado a esta fecha; el precio está en su página de precios | [C] |
| **nginx-rtmp** | Todo el módulo. Licencia BSD de dos cláusulas | No hay. El costo es tu equipo y tu subida | [N] |
| **MediaMTX** | Todo el producto. Licencia MIT | No hay. El costo es tu equipo y tu subida | [M] |

Los tres autoalojados no cobran, pero tampoco son gratis: la subida sale de tu conexión.
Emitir a 4 Mbps hacia tres plataformas son 12 Mbps de subida sostenida. Los dos servicios
en la nube suben una vez y multiplican ellos.

---

## 4. Dónde va tu vídeo

| Producto | Camino | Fuente |
| --- | --- | --- |
| **Splitstream** | Tu ordenador → las plataformas | [S] |
| **Restream** | Tu ordenador → sus servidores → las plataformas | [R] |
| **Castr** | Tu ordenador → sus servidores → las plataformas | [C] |
| **nginx-rtmp** | Tu ordenador o tu servidor → las plataformas | [N] |
| **MediaMTX** | Tu ordenador o tu servidor → las plataformas | [M] |

Si instalas cualquiera de los tres autoalojados en un VPS alquilado, tu vídeo pasa por ese
VPS. La diferencia con los servicios de la nube no es que no haya intermediario: es que el
intermediario lo eliges y lo administras tú.

---

## 5. Capacidades por plataforma

Esta matriz viene del roadmap
[`superpowers/specs/2026-09-09-roadmap-mejoras.md`](superpowers/specs/2026-09-09-roadmap-mejoras.md)
§2 y describe lo que **cada plataforma** permite hacer por API, no lo que hace un producto
concreto. Se reproduce tal cual salvo una celda: donde el roadmap valoraba la programación
de YouTube («es la mejor de todas») aquí se dice qué ofrece su API. Su columna «Costo de
entrada» es el juicio del roadmap sobre el trabajo de integración, no una valoración de
las plataformas.

| Plataforma | Título en vivo | Programar | Chat | Costo de entrada |
| --- | --- | --- | --- | --- |
| **Twitch** | Sí, sencillo | No aplica (no hay "evento") | EventSub por WebSocket, push | Bajo. Registrar app y listo |
| **YouTube** | Sí | Sí, con API de eventos programados | Polling, caro en cuota | **Alto.** Verificación OAuth + problema de cuota (§3) |
| **Kick** | Sí, API pública oficial | Parcial | Solo por webhook entrante | Medio. Requiere URL pública (§4) |
| **Facebook** | Sí | Sí | Sí | **Muy alto.** Requiere App Review y solo está disponible con verificación de negocio, y puede exigir firmar contratos adicionales |
| **X** | Improbable | — | — | Sin vía viable a costo razonable |
| **TikTok** | Acceso a live restringido a socios | — | — | Sin vía viable |

Lo que Splitstream hace hoy con esa matriz: título en Twitch, YouTube y Kick; chat de
lectura en esas mismas tres (Kick solo con el panel accesible en una URL pública HTTPS);
Facebook, X y TikTok, solo retransmitir [S].

---

## Fuentes

- **[S] Splitstream** — este repositorio: [`README.md`](../README.md) y
  [`docs/manual-de-usuario.md`](manual-de-usuario.md).
- **[R] Restream** — <https://restream.io> (producto) y <https://restream.io/pricing>
  (precios).
- **[C] Castr** — <https://castr.io> (producto y precios).
- **[N] nginx-rtmp** — <https://github.com/arut/nginx-rtmp-module> y su wiki de
  directivas <https://github.com/arut/nginx-rtmp-module/wiki/Directives>.
- **[M] MediaMTX** — <https://github.com/bluenviron/mediamtx> (el README del proyecto es
  su documentación).
