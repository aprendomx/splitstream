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

---

## 6. Límites de cada plataforma

Lo que hay que saber para no perder una hora buscando un fallo que no es tuyo:

| Plataforma | Qué tener en cuenta |
| --- | --- |
| **YouTube** | Fotogramas clave cada 4 segundos como máximo. Recomendado: 2 |
| **Twitch** | Techo de 6.000 kbps; por encima, rechaza la emisión |
| **Facebook** | Solo acepta RTMPS, y limita las emisiones activas simultáneas |
| **TikTok** | Servidor distinto en cada emisión; requiere 1.000 seguidores |

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

## 9. Preguntas frecuentes

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
