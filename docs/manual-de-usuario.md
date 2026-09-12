# Manual de usuario

Splitstream recibe **una** transmisión desde OBS y la reenvía a **varias** plataformas a la
vez. Tú emites una sola vez; tus espectadores te ven donde prefieran.

No cambia tu vídeo: lo reenvía tal cual. Eso significa que consume casi nada de CPU, pero
también que tu subida tiene que aguantar `tu bitrate × número de canales`. Si emites a
4 Mbps hacia tres plataformas, estás subiendo 12 Mbps.

---

## 1. Configura OBS

Abre el panel (`http://localhost:8080` si lo ejecutas en tu propio equipo). Arriba verás la
tarjeta **Configura esto en OBS** con dos datos.

En OBS: **Ajustes → Emisión**

| Campo | Qué poner |
| --- | --- |
| Servicio | `Personalizado…` |
| Servidor | El que muestra el panel, algo como `rtmp://localhost:1935/live` |
| Clave de retransmisión | La que muestra el panel (usa el botón de copiar) |

Después, en **Ajustes → Salida**, cambia el modo de salida a **Avanzado** y pon el
**intervalo de fotogramas clave en 2 segundos**.

> **Esto no es opcional.** En modo Simple, OBS deja que el codificador decida, y x264 usa
> 250 fotogramas por defecto — unos 8 segundos a 30 fps. YouTube exige 4 como máximo y te
> avisará de que hay problemas de almacenamiento en búfer. No es culpa de Splitstream:
> ese vídeo llega tal cual desde OBS.

---

## 2. Vincula tus canales

En el panel, pulsa **Vincular canal** y elige la plataforma. Splitstream ya conoce la
dirección de cada una, así que **solo tienes que pegar tu clave**.

### Dónde encontrar tu clave

| Plataforma | Dónde |
| --- | --- |
| **YouTube** | YouTube Studio → Crear → Emitir en directo |
| **Twitch** | Creator Dashboard → Configuración → Transmisión |
| **Facebook** | Live Producer → Usar clave de transmisión |
| **Kick** | Creator Dashboard → Configuración de stream |
| **X** | Media Studio → Producer |
| **TikTok** | TikTok Live Studio, o Live Center → Transmitir con software |

**TikTok es distinto de todas las demás:** te da un servidor *diferente en cada emisión*,
no uno fijo. Por eso el panel te pide dos cosas —servidor y clave— y hay que actualizarlas
cada vez que vuelvas a emitir. También necesitas 1.000 seguidores para que TikTok te
habilite las emisiones en directo.

### Ponle nombre y logo

Cada canal tiene un **nombre** que solo ves tú. Se rellena con el de la plataforma
—«YouTube», «Twitch»—, pero puedes cambiarlo por lo que te sirva para reconocerlo:
«Canal principal», «Solo domingos», el nombre de tu grupo. Si emites a dos cuentas del
mismo servicio, esto es lo que las distingue.

También puedes subirle un **logo** en PNG o JPEG. Aparece en su tarjeta con el icono de la
plataforma en una esquina, así que de un vistazo sabes qué canal es y a dónde va. Da igual
el tamaño que subas: se reduce solo. Es opcional; sin logo se ve el icono de la plataforma,
como siempre.

### Después de vincular

No te fíes de que diga «guardado». Mira la tarjeta del canal: cuando empieces a transmitir
desde OBS debe ponerse en **Emitiendo** con el bitrate subiendo. Eso es lo único que
demuestra que funciona.

### Conectar tu cuenta de Twitch

Con la cuenta conectada, Splitstream puede cambiar el título y la categoría de tu canal y
leer tu chat en el panel. No hace falta para retransmitir: la clave de stream sigue siendo
lo único imprescindible.

1. Edita el canal de Twitch (o vincúlalo) y, en el bloque **Cuenta**, pulsa **Conectar
   cuenta de Twitch**.
2. Verás un código de ocho letras. Abre `twitch.tv/activate` en cualquier dispositivo,
   entra con tu cuenta y escribe el código. El panel se entera solo.
3. Elige la cuenta en «Cuenta vinculada» y guarda.

Splitstream pide dos permisos: gestionar la configuración del canal (título y categoría) y
leer el chat. No puede escribir en el chat ni moderar. Los tokens se guardan cifrados con
tu clave maestra, como las claves de stream, y se renuevan solos; si Twitch los revoca, el
canal muestra «reconectar» y repites los tres pasos.

**Desconectar** borra los tokens de Splitstream; el permiso sigue vigente en Twitch hasta
que caduque (unas horas) o lo retires en Twitch → Configuración → Conexiones.

Los chips **Título**, **Categoría** y **Chat** de cada canal dicen qué puede hacer cada
plataforma. Un canal «Otro», TikTok o X no tiene ninguno: son solo una URL y una clave,
y eso está bien.

> Hasta que la app de Splitstream esté registrada en Twitch, conectar cuentas necesita que
> definas `SPLITSTREAM_TWITCH_CLIENT_ID` (ver el README). Sin ella, el bloque **Cuenta**
> te lo dice en vez de mostrar el botón de conectar.

### Conectar tu cuenta de YouTube

Con la cuenta conectada, Splitstream puede crear la emisión y traer la clave de ingesta
directamente de YouTube, cambiar el título en vivo y leer el chat (ver «Título en vivo y
chat» para lo de la cuota). No hace falta para retransmitir: puedes seguir pegando la
clave a mano.

1. Edita el canal (o vincúlalo) y, en el bloque **Cuenta**, pulsa **Conectar cuenta de
   YouTube**.
2. YouTube exige una app propia de Google Cloud (la cuota de su API es por proyecto, no
   por usuario), así que el asistente te pide `client_id` y `client_secret` con los pasos
   de cada consola — la guía completa, con capturas, está en
   [`docs/youtube-credenciales.md`](youtube-credenciales.md).
3. Verás un código. Abre `google.com/device` (la dirección que enseña el panel) en
   cualquier dispositivo, entra con tu cuenta de Google y escribe el código. El panel se
   entera solo.
4. Elige la cuenta en «Cuenta vinculada» y guarda.

Mientras el proyecto de Google Cloud esté en «Testing», la autorización caduca a los 7
días y hay que reconectar repitiendo los pasos 3 y 4; publicar la app lo evita.

### Conectar tu cuenta de Kick

Con la cuenta conectada, Splitstream puede traer la clave y la URL de ingesta
directamente de Kick, cambiar el título y la categoría, y —si el panel es público por
HTTPS— leer el chat por webhook.

1. Edita el canal (o vincúlalo) y, en el bloque **Cuenta**, pulsa **Conectar cuenta de
   Kick**.
2. Kick tampoco admite una app compartida: el asistente pide `client_id` y
   `client_secret` con los pasos exactos de su consola de desarrollador, incluida la
   Redirect URL que hay que copiar tal cual — guía completa en
   [`docs/kick-credenciales.md`](kick-credenciales.md).
3. El panel abre Kick en una **pestaña nueva** para que autorices; vuelve a la pestaña del
   panel cuando termines, se entera solo, sin que hagas nada más.
4. Elige la cuenta en «Cuenta vinculada» y guarda.

Desconectar borra los tokens de Splitstream, igual que con Twitch.

---

## 3. Empieza a emitir

Dale a **Iniciar transmisión** en OBS. En unos segundos, cada canal pasa por
`Conectando…` y llega a `Emitiendo`.

Puedes **añadir, apagar o editar canales mientras estás en directo**. Los cambios se
aplican al momento, sin cortar la transmisión ni afectar a los demás canales.

Arrastra las tarjetas para cambiar su orden.

### El interruptor «Todos»

Arriba de la lista, junto a «Vincular canal», hay un interruptor que enciende o apaga
**todos los canales a la vez**. Aparece cuando tienes más de uno.

Sirve para el momento de salir al aire: enciendes todo de un golpe en vez de ir tarjeta por
tarjeta. Cuando unos están encendidos y otros no, el interruptor se ve a medias, para no
decirte que están todos igual cuando no lo están.

Apagarlo mientras emites **corta las transmisiones en curso**, así que te pregunta antes y
te dice cuántas. Encenderlo no pregunta nada.

### Vista previa

Mientras estás emitiendo, el botón «Vista previa» de la tarjeta de señal abre un monitor
del vídeo que está saliendo hacia tus canales. Va sin sonido a propósito: es para
comprobar que se ve bien, no para verte el directo.

Dos cosas que conviene saber:

- **Mientras la vista está abierta, el servidor gasta en subida más o menos lo mismo que
  un canal más.** Al cerrarla, ese gasto desaparece. Por eso se abre con un botón y no
  sola.
- Necesita un navegador razonablemente moderno (Chrome o Edge, Safari 16.4 o más nuevo,
  Firefox 130 o más nuevo). Si el tuyo no puede, el panel te lo dirá y no pasa nada más.

Si la emisión se corta, la vista se cierra sola avisando. No se reabre por su cuenta.

---

## 4. Qué significa cada estado

| Estado | Qué pasa | Qué hacer |
| --- | --- | --- |
| **Emitiendo** | Todo bien | Nada |
| **En espera** | El canal está listo pero no hay señal de OBS | Arranca la transmisión en OBS |
| **Conectando…** | Está estableciendo la conexión | Esperar unos segundos |
| **Emitiendo con pérdidas** | Está descartando vídeo para no atrasarse | Tu subida no da abasto: baja el bitrate en OBS o apaga un canal |
| **Reconectando…** | Se cayó la conexión y lo está reintentando | Suele resolverse solo |
| **Conecta y se corta** | La plataforma acepta y cierra enseguida, una y otra vez | Ver abajo |
| **No llega a transmitir** | Nunca consigue enviar nada | Casi siempre es la clave: revísala |
| **Suspendido** | Lo intentó diez veces sin conseguirlo y dejó de insistir en esta emisión | Revisa la clave y pulsa **Reintentar** |
| **Apagado** | Lo apagaste tú | Enciéndelo con el interruptor |

---

## 5. Cuando un canal falla

### «No llega a transmitir»

Es la clave, en el 90 % de los casos. Cópiala otra vez desde la plataforma —entera, sin
espacios— y pégala en **Editar → Clave nueva**.

### «Conecta y se corta»

La plataforma te acepta y te cierra a los pocos segundos. Las causas habituales, por orden
de frecuencia:

1. **La emisión ya no está abierta en la plataforma.** Muchas caducan la clave al terminar
   un directo. Crea una emisión nueva y copia la clave nueva.
2. **Llegaste al límite de emisiones activas.** Facebook, por ejemplo, permite un número
   limitado de emisiones simultáneas y cuenta cada reconexión como una nueva. Si te
   aparece «Alcanzaste el límite de streams activos», **apaga el canal en Splitstream**,
   cierra las emisiones colgadas en la plataforma y espera unos minutos a que su contador
   se libere.
3. **La clave es de otra cuenta o de otro canal.**

Mientras eso pasa, Splitstream **espacia los reintentos** —1, 2, 4, 8, hasta 30 segundos—
en lugar de insistir cada segundo. Es a propósito: reintentar sin freno contra una
plataforma con límite de emisiones te agota la cuota y te deja sin poder emitir.

### «Emitiendo con pérdidas»

Tu subida no da para todos los canales. Splitstream descarta vídeo **por grupos completos**
para que la imagen no se rompa, pero eso significa saltos.

- Baja el bitrate en OBS (**Ajustes → Salida → Tasa de bits de vídeo**).
- O apaga temporalmente el canal menos importante.

Regla rápida: suma tu bitrate tantas veces como canales tengas y compáralo con tu subida
real, no con la que anuncia tu operador.

### Un canal falla y los demás van bien

Es lo normal y está diseñado así: cada canal es independiente. Que Facebook se caiga no
afecta a YouTube ni a Twitch.

### Probar un canal antes de emitir

Antes de salir al aire puedes comprobar que un canal está bien configurado sin emitir
nada. En el menú de su tarjeta (los tres puntos), elige **Probar**.

Splitstream se conecta a la plataforma como si fuera a transmitir, espera unos segundos y
se desconecta sin haber mandado vídeo. El resultado es uno de estos cuatro:

| Resultado | Qué significa |
| --- | --- |
| **Configuración plausible** | La plataforma aceptó la conexión y la mantuvo abierta. La configuración es plausible; solo emitir de verdad confirma la clave |
| **Conecta y se corta** | La plataforma aceptó la conexión y la cerró enseguida. Casi siempre es la clave, o una emisión que ya no está abierta en la plataforma |
| **Rechazado** | La plataforma rechazó el handshake, o la URL no vale — revísala |
| **No se llega al servidor** | No se pudo conectar: revisa la URL, el puerto y tu red |

Dos cosas a tener en cuenta:

- **No puedes probar un canal mientras está emitiendo.** Una segunda conexión con la
  misma clave haría que la plataforma cortara la que va en vivo, así que Splitstream lo
  bloquea.
- **En Facebook, cada prueba cuenta como una emisión activa** contra su cupo, igual que
  una emisión de verdad. El panel te avisa antes de dejarte probar un canal de Facebook,
  por si tienes el cupo justo.

---

## 6. Límites de cada plataforma

Lo que hay que saber para no perder una hora buscando un fallo que no es tuyo:

| Plataforma | Qué tener en cuenta |
| --- | --- |
| **YouTube** | Fotogramas clave cada 4 segundos como máximo. Recomendado: 2 |
| **Twitch** | Techo de 6.000 kbps; por encima, rechaza la emisión |
| **Facebook** | Solo acepta RTMPS, y limita las emisiones activas simultáneas |
| **TikTok** | Servidor distinto en cada emisión; requiere 1.000 seguidores |

### Qué puede hacer desde el panel

Retransmitir siempre funciona igual en todas las plataformas: es solo una URL y una clave.
Lo que cambia es si Splitstream puede además tocar el canal desde el panel —cambiarle el
título, la categoría, o leerte el chat—, y eso depende de tener una cuenta conectada, no
solo un canal vinculado:

| Plataforma | Qué puede hacer desde el panel |
| --- | --- |
| **Twitch** | Título, categoría, chat (de lectura) |
| **YouTube** | Título, crear la emisión y traer la clave por API, chat (de lectura, con presupuesto de cuota) |
| **Kick** | Título, categoría, traer la clave por API, chat (de lectura, solo con URL pública) |
| **Facebook** | Solo retransmitir — exige verificación de negocio para la API de canal |
| **X** | Solo retransmitir — no tiene una API viable para esto |
| **TikTok** | Solo retransmitir — no tiene una API viable para esto |
| **Otro** | Solo retransmitir |

---

## 7. Rotar la clave de ingesta

La clave que pusiste en OBS. Rótala si crees que se ha filtrado —por ejemplo, si compartiste
pantalla con OBS abierto.

Pulsa **Rotar clave**. La nueva aparece **una sola vez**: cópiala y pégala en OBS antes de
cerrar el aviso.

Si marcas **desconectar ahora**, se corta la transmisión en curso al instante. Úsalo cuando
sospeches que alguien más la está usando; si solo estás haciendo limpieza, déjalo sin
marcar y la clave nueva valdrá a partir de tu próxima transmisión.

---

## 8. Ver la clave de un canal

En el menú de cada tarjeta, **Ver la clave**. Se muestra en claro para que puedas copiarla.

**Queda registrado en el log de eventos**, siempre y sin excepción. Es a propósito: si
alguien entra en tu panel, quieres poder verlo.

---

## 9. Grabar

Además de reenviar tu emisión a tus canales, Splitstream puede guardar una copia en el
servidor. En **Ajustes → Grabación**, activa **Grabar las emisiones**: desde ese momento,
cada sesión que llegue por RTMP se graba en el disco del servidor, en FLV y sin
transcodificar — el mismo vídeo que reenvía a tus canales, tal cual.

### Segmentos, y por qué existen

Con «Minutos por segmento» en más de 0, la grabación se corta a archivos de ese tamaño en
vez de un único archivo por sesión. La razón es la resiliencia: si se va la luz o el
proceso muere a mitad de una emisión de una hora, los segmentos ya cerrados están
completos y se pueden reproducir — lo que se pierde es, como mucho, el segmento que
estaba en curso. Con 0 minutos queda un solo archivo, que solo tiene sentido para
emisiones cortas.

### El tope y la retención

Dos límites, y **manda el de gigas**:

- **Tope en GB** — al llegar, se borran las grabaciones más antiguas hasta volver a estar
  por debajo.
- **Días de retención** — borra por fecha. Con 0, solo manda el tope en GB.

Si los dos están puestos y compiten, gana el tope en GB: nunca vas a llenar el disco por
haber puesto una retención larga.

### Cuando el disco no da abasto

Si el disco no escribe tan rápido como llega la emisión, la grabación empieza a descartar
vídeo para no atrasarse — igual que le pasaría a un canal con la subida corta, pero solo
en el archivo. El chip **Grabando** del panel se pone en ámbar y su aviso dice que el
disco no da abasto. **Tus canales no se enteran**: la regla es que el directo nunca se
degrada por culpa de la grabación.

### Descargar, borrar y pasar a MP4

Desde la página **Grabaciones** del panel ves cada segmento con su duración y su peso, y
puedes **descargarlo** o **borrarlo** uno por uno. Los archivos son FLV; para editarlos o
subirlos a otro sitio, conviértelos sin volver a codificar:

```bash
ffmpeg -i x.flv -c copy x.mp4
```

---

## 10. Crear la emisión desde el panel

Con una cuenta de YouTube o Kick conectada, el diálogo del canal sustituye el campo de
clave por un botón que trae la clave real desde la plataforma en vez de que la copies a
mano.

**YouTube — «Crear emisión y traer la clave».** Pon el título, la privacidad
(`Público`, `No listado` o `Privado`; por defecto **no listado**) y, si quieres
programarla, la hora — vacío es «emitir ahora». Splitstream crea la emisión y el *stream*
en YouTube, y escribe la URL de ingesta y la clave reales en el canal.

- YouTube **sale al aire sola** en cuanto le llega la señal de OBS, y **termina sola**
  cuando la señal se va: no hace falta tocar nada más.
- Los botones **Salir al aire** y **Terminar** de la tarjeta del canal están para casos en
  los que quieras forzarlo (por ejemplo, cortar la emisión sin apagar OBS todavía). Cuando
  hay una emisión creada, la tarjeta también lleva un enlace **ver en YouTube**.
- La tarjeta muestra «clave por API» en el pie en vez de la clave enmascarada — **Probar**
  dice que no hace falta, porque la clave vino de la propia plataforma.

**Kick — «Traer la clave de Kick».** Un solo botón: lee la clave y la URL de ingesta del
canal en Kick y las escribe en el destino. Kick no tiene concepto de «emisión» que crear
ni programar, así que no hay más campos que rellenar.

En ambos casos, **«pegar la clave a mano»** sigue disponible detrás de un enlace, por si
prefieres seguir copiándola tú.

---

## 11. Título en vivo y chat

**Título en vivo.** Con al menos un canal con cuenta conectada aparece un campo sobre la
lista de canales. Escribe el título (y, para Twitch, busca la categoría) y pulsa **Aplicar
en todos los que puedan**. Cada canal responde por separado: si uno falla, los demás
cambian igual.

**Chat.** Durante una emisión, el botón **Chat** junto a «Vista previa» abre una columna
con los mensajes de las plataformas conectadas, con una pestaña por plataforma. Es solo
lectura. El chat se guarda con la sesión y la retención lo poda igual que los eventos
(`SPLITSTREAM_RETENTION_MAX_CHAT`, 200 000 mensajes por defecto).

**Cuota y pausa en YouTube.** Leer el chat de YouTube cuesta cuota de la API (ver
[`docs/youtube-credenciales.md`](youtube-credenciales.md)): la columna de chat enseña una
barra con lo gastado hoy frente al presupuesto diario. Al llegar a
`SPLITSTREAM_YOUTUBE_CHAT_BUDGET` (6 000 unidades por defecto), el chat de esa cuenta se
pausa solo — antes de agotar la cuota del proyecto entero y dejarte sin poder crear una
emisión o cambiar un título — y lo dice. Se reanuda al día siguiente, o si subes el
presupuesto.

**Kick y la URL pública.** El chat de Kick llega por un webhook que la propia plataforma
llama, así que **solo funciona si el panel es accesible por HTTPS desde internet** — con
el TLS integrado (ver «Ponerlo en internet» en el README) o con un proxy delante. En un
equipo sin URL pública, el bloque **Cuenta** del canal de Kick lo dice: la retransmisión
funciona igual, solo falta el chat.

---

## 12. Avisos

Splitstream puede avisarte cuando un canal falla o se corta la emisión, sin que tengas que
tener el panel abierto: en **Ajustes → Avisos**, pulsa **Nuevo aviso** y dale una URL de
Discord, de Slack, o de tu propio servidor.

### Crear un webhook de Discord

En el servidor de Discord donde quieras recibir los avisos: **Ajustes del servidor →
Integraciones → Webhooks → Nuevo webhook**, y **Copiar URL del webhook**. Pégala en el
campo **URL** del diálogo de Splitstream y elige el formato **Discord**.

Para Slack, el camino es el mismo dentro de tu espacio de trabajo: creas el webhook
entrante y copias la URL que te da.

### Qué avisa cada nivel

El campo **Avisar de** filtra qué llega a tu webhook:

| Nivel | Qué manda |
| --- | --- |
| **Solo errores** | Únicamente lo que necesita que actúes: un canal suspendido, un fallo que no se resuelve solo |
| **Avisos y errores** | Lo anterior, más avisos — como el resultado de una prueba que no fue «plausible» |
| **Todo** | Todo lo que se registra, incluida la actividad normal |

### El formato JSON, para quien monte su propio receptor

Si eliges el formato **JSON genérico**, cada evento llega así:

```json
{"id":42,"kind":"destination_suspended","level":"error","message":"…",
 "session_id":7,"destination":{"id":3,"name":"YouTube","platform":"youtube"},
 "at":"2026-09-09T20:15:03.123456789Z"}
```

Con la cabecera `X-Splitstream-Event` llevando el tipo de evento. Si le pusiste un
**secreto** al crear el aviso, también llega `X-Splitstream-Signature: sha256=<hex>`: el
HMAC-SHA256 del cuerpo exacto de la petición, calculado con ese secreto. Verifícalo antes
de confiar en el contenido — es lo que te dice que el aviso viene de tu Splitstream y no
de cualquiera que adivine la URL.

Discord y Slack no llevan firma: su propia URL ya funciona como el secreto.

### Cuando hay una versión nueva

Una vez al día Splitstream mira si hay una release más reciente y, si la hay, enseña una
franja arriba del panel con la versión y un enlace. Puedes cerrarla; vuelve a salir solo
con la siguiente versión. No se actualiza solo: cómo hacerlo depende de cómo lo
instalaste, y está en el README («Actualizar»). Es la única conexión que el programa hace
por su cuenta, y solo manda su propia versión.

---

## 13. Respaldo

En **Ajustes → Respaldo**, el botón **Descargar respaldo** te da una copia de la base de
datos completa: canales, claves cifradas y la contraseña del panel.

No se puede descargar con una emisión en curso: la copia retiene la base de datos y
frenaría a tus canales. Hazlo al terminar.

**Sin tu `splitstream.key` ese archivo no sirve de nada.** Las claves de tus canales están
cifradas con tu clave maestra; sin ella, el respaldo es un montón de bytes ilegibles.
Guarda los dos juntos, pero no en el mismo sitio que el original — el objetivo de un
respaldo es sobrevivir a que pierdas el original.

---

## 14. Preguntas frecuentes

**¿Puedo cambiar la calidad por canal?**
No. Splitstream reenvía el vídeo tal cual, sin tocarlo — por eso apenas consume CPU. Emitir
a distinta calidad en cada plataforma exige transcodificar, que es otro problema y otro
consumo.

**¿Y si se me cae internet?**
Cada canal reintenta por su cuenta y se reengancha solo. OBS también reconecta contra
Splitstream. Los espectadores verán una interrupción.

**¿Puedo cerrar el panel mientras emito?**
Sí. El panel solo mira: la retransmisión la hace el programa, no la página.

**¿Funciona desde el móvil?**
Sí, el panel está pensado para eso: comprobar cómo va todo desde el teléfono a mitad de un
directo.

**¿Dónde se guardan mis claves?**
Cifradas con AES-256-GCM en el archivo SQLite, con la clave maestra que generaste al
instalar. No aparecen nunca en los registros, ni siquiera enmascaradas.

**Perdí la clave maestra.**
Las claves de tus canales son irrecuperables: eso es el diseño, no un fallo. Genera una
maestra nueva, empieza con una base de datos limpia y vuelve a pegar las claves de cada
plataforma.
