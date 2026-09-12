# Conectar YouTube: crea tu propia app en Google Cloud

Para que Splitstream conecte tu cuenta de YouTube —crear la emisión, traer la clave de
ingesta, cambiar el título y leer el chat— necesita un `client_id` y un `client_secret`
de una app de Google Cloud **tuya**, no una compartida con el resto de usuarios de
Splitstream.

**Por qué se pide.** La cuota de la YouTube Data API es por proyecto de Google Cloud, no
por usuario: 10 000 unidades al día. Si Splitstream usara una app compartida entre todo
el mundo, doscientos cambios de título repartidos entre todos los usuarios del planeta la
agotarían, y entonces nadie podría crear una emisión ni cambiar un título hasta el día
siguiente. Con tu propio proyecto, esa cuota es solo tuya.

**Cuánto tarda.** Unos 10 minutos si es la primera vez que entras a Google Cloud Console.

**El aviso honesto sobre «Testing».** Mientras tu proyecto esté en modo «Testing» (el que
sale por defecto), Google caduca la autorización a los 7 días: pasado ese plazo, la cuenta
pide reconectarse. Publicar la app («In production») lo evita, a cambio de que la pantalla
de consentimiento de Google muestre un aviso de «app no verificada» — algo que solo verás
tú, porque nadie más va a usar tu proyecto.

---

## Paso 1: crea un proyecto en Google Cloud

Entra a [Google Cloud Console](https://console.cloud.google.com/projectcreate) y crea un
proyecto nuevo. Puede ser uno que uses solo para esto.

![captura](img/youtube-paso-1.png)

## Paso 2: habilita la YouTube Data API v3

Con el proyecto abierto, ve a **APIs y servicios → Biblioteca**, busca **YouTube Data API
v3** y pulsa **Habilitar**.

![captura](img/youtube-paso-2.png)

## Paso 3: configura la pantalla de consentimiento

En **APIs y servicios → Pantalla de consentimiento de OAuth**, elige tipo **Externo** y,
en la pestaña de usuarios de prueba, añade tu propia cuenta de Google.

![captura](img/youtube-paso-3.png)

## Paso 4: crea la credencial

En **APIs y servicios → Credenciales → Crear credenciales → ID de cliente de OAuth**,
elige el tipo **«TVs and Limited Input devices»**. Es el que permite el flujo de
dispositivo (el código que escribes en `google.com/device`), y funciona igual en tu
propio equipo que en un servidor sin navegador.

![captura](img/youtube-paso-4.png)

## Paso 5: copia el client_id y el client_secret

Google te los muestra al terminar el paso anterior. Cópialos y pégalos en el asistente del
panel de Splitstream.

![captura](img/youtube-paso-5.png)

---

## Qué gasta cuota

Cada llamada que Splitstream hace a la YouTube Data API en tu nombre suma a tu cuota
diaria de 10 000 unidades (`SPLITSTREAM_YOUTUBE_QUOTA`: el límite real lo fija Google, aquí
solo se declara para poder enseñarlo). El panel enseña lo gastado en **Ajustes → Cuentas
conectadas** y, junto al presupuesto del chat y a esa cuota diaria, en la barra de cuota
del chat.

| Acción | Unidades |
| --- | --- |
| Crear la emisión («Crear emisión y traer la clave») | ≈ 150 |
| Cambiar el título en vivo | 50 |
| Cada sondeo del chat | 5 |

El chat sondea cada pocos segundos mientras esté abierto: a un sondeo cada 4 segundos, son
unas 4 500 unidades por hora. Por eso el chat de YouTube se pausa solo al llegar a
**6 000 unidades en el día** (`SPLITSTREAM_YOUTUBE_CHAT_BUDGET`) — antes de que se coma
toda la cuota del proyecto y te deje sin poder crear una emisión o cambiar un título. Se
reanuda al día siguiente, o si subes el presupuesto tú mismo.

Los costes de las llamadas de creación y transición de la emisión (`insert`, `bind`,
`transition`) son una estimación conservadora: la tabla pública de Google no los desglosa
método por método, solo dice que una escritura cuesta 50 unidades en general.
