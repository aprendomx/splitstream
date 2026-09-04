# Entrada de lanzamiento

Borrador listo para publicar y el trabajo de posicionamiento que lo acompaña.
Escrito para la v0.6.0. Si cambian las plataformas soportadas o lo que la
herramienta no hace, hay que revisarlo: son las dos cosas que el texto promete.

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

> Los tres fallos producían exactamente el mismo mensaje en el registro. Por eso el panel no
> te enseña el error técnico: te dice «conecta y se corta» y te sugiere revisar si alcanzaste
> el límite de emisiones de la plataforma.

#### Qué no hace

No transcodifica, así que no puedes emitir a distinta calidad en cada plataforma. No graba.
No unifica el chat. Y no es multiusuario. Si necesitas cualquiera de esas cosas, esto no es
la herramienta — y preferimos decirlo antes de que la descargues.

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
