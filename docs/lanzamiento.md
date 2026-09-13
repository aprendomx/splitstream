# Entrada de lanzamiento

Borrador listo para publicar y el trabajo de posicionamiento que lo acompaña.
Escrito para la v0.13.0, la primera bilingüe: el panel se conmuta entre español e inglés,
el `README.md` principal pasa a inglés (el español queda en `README.es.md`) y hay una
vista de historial para mirar una emisión ya terminada. Sigue en pie lo de la v0.12.0: a
la conexión de cuenta de Twitch de la v0.11.0 —cambiar el título y leer el chat desde el
panel— se sumaron YouTube y Kick, con las que además desapareció el copiar y pegar de la
clave de stream. Si cambian las plataformas soportadas, los idiomas del panel o lo que la
herramienta no hace, hay que revisarlo: son las cosas que el texto promete.

El borrador está en español en «El borrador» y en inglés en «English draft», al final, con
la misma estructura de encabezados.

---

## El borrador

### Emite a YouTube, Twitch y Facebook a la vez desde tu propio ordenador

Splitstream recibe una transmisión de OBS y la reenvía a todas tus plataformas
simultáneamente. Es un único archivo de 4 MB, funciona sin cuenta ni suscripción, y el
código está abierto bajo licencia MIT.

Si retransmites en directo, conoces el problema: tu público está repartido entre YouTube,
Twitch, Facebook y TikTok, pero tu ordenador solo puede subir a un sitio a la vez sin
ahogarse. La solución habitual son servicios de pago que cobran una cuota mensual por hacer
de intermediarios — y que ven pasar todo tu vídeo por sus servidores.

**Splitstream hace ese trabajo en tu propia máquina.** Apuntas OBS a `localhost`, pegas la
clave de cada plataforma una sola vez, y a partir de ahí emites a todas de golpe. Sin
cuenta, sin cuota, sin que tu vídeo pase por nadie.

#### Cómo funciona

No transcodifica: reenvía los paquetes tal cual llegan. Eso tiene dos consecuencias
prácticas. La primera, que el consumo de procesador es despreciable — puedes tenerlo
corriendo en el mismo equipo con el que juegas o editas. La segunda, que lo que limita es tu
subida: emitir a 4 Mbps hacia tres plataformas significa subir 12.

Cada destino es independiente. Si Facebook se cae, YouTube y Twitch ni se enteran. Cuando la
subida no da abasto, descarta vídeo por grupos completos en el destino que va lento, de
forma que la imagen no se rompa, y el resto sigue intacto.

#### Instalación

Descargas el archivo de tu sistema, lo descomprimes y haces doble clic. Se crea su propia
clave de cifrado, abre el panel en el navegador y te pide una contraseña. No hay instalador,
ni dependencias, ni variables de entorno que configurar.

```
  ┌───────────────────────────────────────────────┐
  │  Splitstream todavía no está configurado      │
  └───────────────────────────────────────────────┘

  Abre el panel y elige tu contraseña:

      http://localhost:8080
```

Las claves de tus canales se guardan cifradas con AES-256-GCM y no aparecen en ningún
registro, ni siquiera enmascaradas. Ver una deja constancia en el historial, siempre: si
alguien entra en tu panel, quieres poder saberlo.

#### Lo que aprendimos probándolo de verdad

Splitstream tiene más de doscientas pruebas automáticas y todas pasaban. Después lo
conectamos a cuentas reales de YouTube, Twitch y Facebook, y aparecieron cuatro fallos que
ninguna de esas pruebas había visto.

El más instructivo: Twitch aceptaba la conexión y la cortaba al segundo. El registro decía
`broken pipe`, que no dice nada. Publicando con `ffmpeg` directamente a Twitch con la misma
clave, la emisión aguantaba — así que el problema era nuestro. Resultó ser el orden de dos
comandos del protocolo RTMP: los mandábamos después de crear el canal, y el cliente que las
plataformas esperan los manda antes. YouTube lo perdonaba; Twitch no.

El más caro fue con Facebook. Al ser rechazados, reintentábamos cada segundo — y Facebook
cuenta cada intento como una emisión activa. Le agotamos la cuota a la cuenta y se quedó sin
poder emitir. Ahora los reintentos se espacian, y transmitir unos pocos kilobytes ya no
cuenta como «la configuración es correcta».

La v0.8 añade lo que nos hubiera ahorrado varias de estas sorpresas: `/metrics` en formato
Prometheus para vigilarlo desde fuera, avisos por webhook cuando algo falla, y un botón
«probar destino» que sondea la configuración sin emitir. Y el propio fallo de Facebook ya
no puede repetirse: un destino que nunca consigue transmitir se suspende tras diez intentos
seguidos y lo dice, en vez de reintentar para siempre.

La v0.9 añade grabación, y con la misma cautela: si el disco no aguanta el ritmo de
escritura, la regla es que se degrada la grabación, nunca el directo, exactamente como un
destino con la subida corta pierde vídeo sin arrastrar a los demás.

La v0.11 empieza a abrir la puerta a algo más ambicioso: cambia el título en todas tus
plataformas desde un solo campo. Por ahora está acotado a Twitch —es la única que conecta
una cuenta propia—, pero conectada esa cuenta puedes cambiar el título y la categoría del
canal, y leer el chat, sin salir del panel. Las demás plataformas se irán sumando a
medida que tengan una integración así de directa.

La v0.12 suma YouTube y Kick a esa misma cuenta propia, y con ellas se acaba el copiar y
pegar de la clave de stream — el peor momento del arranque, con pestañas abiertas
buscando dónde esconde cada plataforma su clave. Si Splitstream crea la emisión por API
también recibe la clave de ingesta por API: en YouTube, un botón crea la emisión, la
vincula y la escribe en el canal, y la emisión sale al aire y termina sola siguiendo la
señal de OBS; en Kick, otro botón trae la clave y la URL directamente de tu cuenta. Las
dos piden que registres tu propia app en la consola de Google o de Kick —lo explicamos
paso a paso en la documentación—, porque ninguna de las dos plataformas admite compartir
una app entre todos los usuarios de Splitstream.

La v0.13 quita la última barrera que quedaba para quien no habla español: el panel entero
se conmuta a inglés desde un selector en la barra, y los mensajes de error de la API
llegan en el idioma del navegador. El registro del servidor y el historial de eventos
siguen en español a propósito — son evidencia escrita una vez, no interfaz. Y hay una
página de historial: cada emisión terminada tiene su ficha con la línea de tiempo de lo
que pasó, cuántas veces reconectó cada canal, el chat de esa sesión y sus grabaciones.

> Los tres fallos producían exactamente el mismo mensaje en el registro. Por eso el panel no
> te enseña el error técnico: te dice «conecta y se corta» y te sugiere revisar si alcanzaste
> el límite de emisiones de la plataforma.

#### Qué no hace

No transcodifica, así que no puedes emitir a distinta calidad en cada plataforma. Graba en
FLV, sin transcodificar, con segmentos y tope de disco. El chat es de lectura, por
plataforma, y hoy con Twitch, YouTube y Kick (este último solo con el panel accesible por
URL pública); escribir y moderar quedan fuera. Y no es multiusuario.
Si necesitas cualquiera de esas cosas, esto no es la herramienta — y preferimos decirlo
antes de que la descargues.

#### Pruébalo

Hay binarios para macOS (Intel y Apple Silicon), Linux (x86 y ARM, sirve en una Raspberry
Pi) y Windows, además de una imagen de Docker de 18 MB. El código está en GitHub bajo
licencia MIT: úsalo, cámbialo y despliégalo donde quieras.

---

## Posicionamiento

La intención dominante no es «qué es el multistreaming», sino **resolver un problema
concreto y barato**: emitir a dos sitios a la vez sin pagar una cuota. El texto de arriba
está escrito alrededor de esa intención — por eso nombra las plataformas, dice el precio
(nada) y admite pronto lo que no hace.

### Palabras clave

| Término | Intención | Prioridad | Dónde usarlo |
| --- | --- | --- | --- |
| emitir en youtube y twitch a la vez | Resolver un problema | Principal | Titular, primer párrafo, `<title>` |
| multistreaming gratis | Comercial, comparativa | Principal | Entradilla y meta description |
| alternativa a Restream | Comparativa | Principal | Un apartado propio, no de pasada |
| retransmitir a varias plataformas | Informativa | Secundaria | Subtítulos (H2/H3) |
| OBS multistream self hosted | Técnica | Secundaria | Apartado de instalación |
| servidor RTMP propio | Técnica | Secundaria | Explicación de funcionamiento |
| cómo emitir en tiktok y youtube al mismo tiempo | Cola larga | Cola larga | Preguntas frecuentes |
| error alcanzaste el límite de streams activos facebook | Cola larga, alta conversión | Cola larga | Entrada aparte que enlace a esta |

**En inglés**, las mismas intenciones se buscan como: `stream to youtube and twitch at the
same time`, `free multistreaming`, `restream alternative`, `self hosted obs multistream`,
`own rtmp server`, `how to stream to tiktok and youtube at once`,
`facebook error you have reached the maximum number of active streams`.

Esas dos últimas valen más de lo que su volumen sugiere. Quien busca un mensaje de error
exacto tiene el problema *ahora mismo* y hay poquísimo contenido compitiendo. Una entrada
corta que explique ese error de Facebook y termine mencionando Splitstream convierte mejor
que pelear por «multistreaming».

### Metadatos

**Título de la página** (58 caracteres):

    Emite a YouTube, Twitch y Facebook a la vez — Splitstream

**Meta description** (154 caracteres):

    Reenvía tu emisión de OBS a todas tus plataformas desde tu propio equipo. Un archivo,
    sin cuenta ni cuota mensual. Código abierto para macOS, Linux y Windows.

**URL:** `/emitir-youtube-twitch-facebook-a-la-vez`
**Título social (OG):** Una emisión, todas las plataformas

El título lleva la palabra clave delante y la marca detrás, porque nadie busca
«Splitstream» todavía. Cuando la marca tenga búsquedas propias, se invierte. Los 58
caracteres caben sin recortarse en resultados de escritorio.

### Estructura de encabezados

Un solo `H1`, y cada `H2` respondiendo a una pregunta que alguien teclea. Los encabezados
que solo describen —«Características», «Ventajas»— no captan ninguna búsqueda.

- **H1** — Emite a YouTube, Twitch y Facebook a la vez desde tu propio ordenador
- **H2** — Cómo funciona el multistreaming sin transcodificar
- **H2** — Cómo instalarlo en macOS, Linux o Windows
- **H2** — Splitstream frente a Restream y Castr *(capta comparativas)*
- **H2** — Qué ancho de banda necesitas *(capta dudas técnicas)*
- **H2** — Preguntas frecuentes *(candidato a fragmento destacado)*

Marca las preguntas frecuentes con datos estructurados `FAQPage` en JSON-LD, y el proyecto
con `SoftwareApplication`. Son de los pocos tipos que Google sigue usando para enriquecer
resultados, y aquí encajan sin forzar nada.

### Difusión

| Canal | Qué funciona ahí | Qué evitar |
| --- | --- | --- |
| r/obs, r/Twitch, r/selfhosted | Contar el fallo de Twitch y cómo se aisló con ffmpeg. La historia técnica interesa más que el anuncio | Publicar solo el enlace. Ahí se penaliza |
| Hacker News | Título sobrio: «Splitstream: relay RTMP self-hosted en un solo binario». El ángulo de los 4 MB y cero cgo | Superlativos y emojis en el titular |
| Foros y Discord de streamers | Responder a quien ya pregunta cómo emitir a dos sitios, mencionándolo cuando venga a cuento | Entrar a promocionar en frío |
| El propio README | Es la página que más tráfico orgánico recibirá. Que empiece diciendo qué resuelve, no cómo está hecho | Abrir con la arquitectura |

### Lo que hay que dejar hecho

- **Etiqueta canónica** si publicas la misma entrada en dev.to o Medium, apuntando a tu
  dominio. Sin ella, compites contigo mismo.
- **Una imagen que se entienda pequeña**: una captura del panel con los tres canales en
  verde dice más que un logotipo. Con `alt` descriptivo.
- **Enlaces internos** desde la entrada al manual de usuario y a la de instalación, con
  texto descriptivo — nunca «aquí».
- **Velocidad**: si el blog es tuyo, las Core Web Vitals cuentan. Una entrada de texto no
  debería pasar de un segundo.
- **Fecha visible y actualizada.** En software, una entrada sin fecha se lee como
  abandonada.

Un aviso honesto sobre expectativas: «multistreaming» es un término con competencia pagada
detrás — Restream y Castr compran esos anuncios. Posicionar por ahí lleva meses. Las
búsquedas de cola larga, los mensajes de error y las comparativas son donde un proyecto
nuevo puede ganar en semanas.

---

## English draft

### Stream to YouTube, Twitch and Facebook at once from your own computer

Splitstream takes one stream from OBS and forwards it to all of your platforms at the same
time. It is a single 4 MB file, it works without an account or a subscription, and the code
is open under the MIT licence.

If you stream live, you know the problem: your audience is spread across YouTube, Twitch,
Facebook and TikTok, but your computer can only upload to one place at a time without
choking. The usual answer is paid services that charge a monthly fee to act as
middlemen — and that watch all of your video go through their servers.

**Splitstream does that job on your own machine.** You point OBS at `localhost`, paste each
platform's key once, and from then on you stream to all of them at once. No account, no
fee, no video passing through anyone else.

#### How it works

It doesn't transcode: it forwards packets exactly as they arrive. That has two practical
consequences. First, processor usage is negligible — you can keep it running on the same
machine you play or edit on. Second, what limits you is your upload: streaming at 4 Mbps to
three platforms means uploading 12.

Every destination is independent. If Facebook goes down, YouTube and Twitch never notice.
When the upload can't keep up, it drops video in whole groups on the destination that is
falling behind, so that the picture doesn't break up, and the rest stay intact.

#### Installation

You download the file for your system, unzip it and double-click. It creates its own
encryption key, opens the panel in your browser and asks you for a password. There is no
installer, no dependencies and no environment variables to configure.

```
  ┌───────────────────────────────────────────────┐
  │  Splitstream todavía no está configurado      │
  └───────────────────────────────────────────────┘

  Abre el panel y elige tu contraseña:

      http://localhost:8080
```

(the command line still speaks Spanish; the web panel is bilingual.)

Your channel keys are stored encrypted with AES-256-GCM and never appear in any log, not
even masked. Viewing one leaves a trace in the history, always: if someone gets into your
panel, you want to be able to know.

#### What we learned by actually testing it

Splitstream has over two hundred automated tests and all of them were passing. Then we
connected it to real YouTube, Twitch and Facebook accounts, and four failures turned up
that none of those tests had seen.

The most instructive one: Twitch accepted the connection and cut it a second later. The log
said `broken pipe`, which says nothing. Publishing with `ffmpeg` straight to Twitch with the
same key, the stream held — so the problem was ours. It turned out to be the order of two
RTMP protocol commands: we were sending them after creating the channel, and the client the
platforms expect sends them before. YouTube forgave it; Twitch didn't.

The most expensive one was with Facebook. On being rejected, we retried every second — and
Facebook counts every attempt as an active stream. We used up the account's quota and it was
left unable to stream. Retries are now spaced out, and sending a few kilobytes no longer
counts as "the configuration is correct".

v0.8 adds what would have saved us several of those surprises: `/metrics` in Prometheus
format to watch it from outside, webhook alerts when something fails, and a "test
destination" button that probes the configuration without streaming. And Facebook's own
failure can no longer repeat itself: a destination that never manages to transmit is
suspended after ten attempts in a row and says so, instead of retrying forever.

v0.9 adds recording, with the same caution: if the disk can't keep up with the write rate,
the rule is that the recording degrades, never the live stream, exactly as a destination
with a short upload loses video without dragging the others down with it.

v0.11 starts to open the door to something more ambitious: it changes the title on all of
your platforms from a single field. For now it is limited to Twitch —the only one that
connects an account of its own— but with that account connected you can change the
channel's title and category, and read chat, without leaving the panel. The rest of the
platforms will join as they get an integration that direct.

v0.12 adds YouTube and Kick to that same account connection, and with them the copy-pasting
of the stream key is over — the worst moment of getting started, with tabs open looking for
where each platform hides its key. If Splitstream creates the broadcast through the API it
also receives the ingest key through the API: on YouTube, one button creates the broadcast,
binds it and writes it into the channel, and the broadcast goes live and ends by itself
following the OBS signal; on Kick, another button brings the key and the URL straight from
your account. Both ask you to register your own app in Google's or Kick's console —we
explain it step by step in the documentation— because neither platform allows sharing one
app across all Splitstream users.

v0.13 removes the last barrier left for anyone who doesn't speak Spanish: the whole panel
switches to English from a selector in the top bar, and the API's error messages arrive in
the browser's language. The server log and the event history stay in Spanish on purpose —
they are evidence written once, not interface. And there is a history page: every finished
stream has its own record with a timeline of what happened, how many times each channel
reconnected, that session's chat and its recordings.

> The three failures produced exactly the same message in the log. That is why the panel
> doesn't show you the technical error: it tells you "it connects and drops" and suggests
> checking whether you hit the platform's limit on active streams.

#### What it doesn't do

It doesn't transcode, so you can't stream at a different quality to each platform. It
records in FLV, without transcoding, with segments and a disk cap. Chat is read-only, per
platform, and today with Twitch, YouTube and Kick (the last one only with the panel
reachable at a public HTTPS URL); writing and moderating are out of scope. And it is not
multi-user. If you need any of those things, this is not the tool — and we prefer to say so
before you download it.

#### Try it

There are binaries for macOS (Intel and Apple Silicon), Linux (x86 and ARM, it works on a
Raspberry Pi) and Windows, plus an 18 MB Docker image. The code is on GitHub under the MIT
licence: use it, change it and deploy it wherever you want.
