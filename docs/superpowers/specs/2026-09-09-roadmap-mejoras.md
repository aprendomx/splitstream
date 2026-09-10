# Splitstream — Roadmap de mejoras

**Fecha:** 2026-09-09
**Base:** `main` @ 136 commits, v0.5.0, seis fases completas
**Objetivo asumido:** adopción. Que otros lo instalen y lo dejen corriendo.
**Estado del documento:** propuesta, pendiente de priorización

---

## 1. Lo que se está decidiendo aquí

Integrar chats, títulos, programación de eventos y grabación convierte a Splitstream de
un *relay* en una *cabina de control*. Es una decisión legítima y es exactamente lo que
lo pone a competir con Restream y Castr, que es contra quien la gente lo va a comparar.
Pero conviene decirlo en voz alta, porque cambia el perfil de mantenimiento del proyecto:

- **El motor de relay es código de escribir una vez.** RTMP no cambia. Los tests que ya
  existen seguirán pasando en 2030.
- **Las APIs de plataforma son mantenimiento permanente.** Cambian scopes, endpoints,
  procesos de revisión y políticas, y lo hacen sin avisar. Cada plataforma añadida es una
  suscripción de trabajo, no una entrega.

Esto no es un argumento para no hacerlo. Es un argumento para hacerlo por capas, con la
capa de APIs claramente separada del motor, y para no prometer paridad entre plataformas:
no la va a haber.

**Lo que no cambia:** sin transcodificación, sin multi-tenant, una ingesta. La grabación
*no* rompe esa regla —grabar sin transcodificar es solo muxear— pero sí contradice el
spec §1, que dice "sin grabación" de forma permanente. Eso hay que revertirlo
explícitamente en el spec, con fecha y razón, no dejarlo derivar en silencio.

---

## 2. La realidad de cada plataforma

Esta es la parte que decide el alcance, y es asimétrica. No hay forma de que las seis
plataformas tengan las mismas funciones.

| Plataforma | Título en vivo | Programar | Chat | Costo de entrada |
| --- | --- | --- | --- | --- |
| **Twitch** | Sí, sencillo | No aplica (no hay "evento") | EventSub por WebSocket, push | Bajo. Registrar app y listo |
| **YouTube** | Sí | Sí, es la mejor de todas | Polling, caro en cuota | **Alto.** Verificación OAuth + problema de cuota (§3) |
| **Kick** | Sí, API pública oficial | Parcial | Solo por webhook entrante | Medio. Requiere URL pública (§4) |
| **Facebook** | Sí | Sí | Sí | **Muy alto.** Requiere App Review y solo está disponible con verificación de negocio, y puede exigir firmar contratos adicionales |
| **X** | Improbable | — | — | Sin vía viable a costo razonable |
| **TikTok** | Acceso a live restringido a socios | — | — | Sin vía viable |

**Consecuencia de diseño:** el panel tiene que mostrar capacidades por destino, no una
lista uniforme de funciones. Un destino "custom" o TikTok seguirá siendo solo una URL y
una clave, para siempre, y eso está bien. Lo que no puede pasar es que la interfaz
insinúe que todos son iguales.

**Orden recomendado:** Twitch primero (barata y completa, valida la arquitectura), Kick
segundo, YouTube tercero (la más valiosa y la más cara), Facebook solo si hay demanda
real que justifique la verificación de negocio. X y TikTok: documentar que no se puede y
por qué.

---

## 3. El problema que decide todo: de quién son las credenciales

Hay dos modelos y hay que elegir uno antes de escribir código.

**(a) Cada usuario registra su propia app** y pega client ID y secret en el panel.
Cero trabajo de verificación para ti, funciona hoy, cuotas separadas por usuario. Pero es
un paso de onboarding brutal, en contradicción directa con "descarga un archivo y ya".

**(b) Splitstream trae credenciales propias verificadas**, con flujo PKCE sobre loopback
(el estándar para apps instaladas, no requiere guardar un secret en el binario). El
usuario da un clic. Pero exige pasar verificación de Google —`youtube.force-ssl` es un
scope sensible, con revisión y video demostrativo— y, sobre todo, choca con esto:

> **La cuota de YouTube es por proyecto de Google Cloud, no por usuario.**
> Cada proyecto recibe 10.000 unidades diarias gratis, y las llamadas de
> lectura, subida y streaming en vivo salen todas del mismo presupuesto diario.
> No se puede comprar cuota adicional; la única vía es un formulario de
> auditoría y extensión.

La aritmética mata el modelo (b) para YouTube:

- Cambiar el título es una escritura: 50 unidades. Con una app compartida, eso son **200
  cambios de título al día entre todos los usuarios del mundo.** A 200 usuarios activos,
  se acabó.
- `liveChatMessages.list` cuesta 5 unidades. Sondeando cada 5
  segundos, una transmisión de tres horas son ~2.160 llamadas, unas **10.800 unidades: un
  solo directo de un solo usuario agota la cuota diaria del proyecto entero.**

**Conclusión, y es firme:** para YouTube, credenciales propias del usuario. No es una
preferencia de arquitectura, es la única configuración que escala en un producto
distribuido. Para Twitch y Kick, credenciales incluidas funcionan bien y ahí sí conviene
el clic único.

Eso da un modelo híbrido, y hay que asumir que la parte fea del onboarding existe:

- Twitch y Kick: "Conectar" → PKCE → hecho
- YouTube: asistente guiado de cinco pasos para crear el proyecto en Google Cloud, con
  capturas, y la explicación honesta de por qué se le pide eso
- Escape para todos: "usar mi propia app" siempre disponible

Ese asistente de YouTube es trabajo de documentación e interfaz, no de backend, y va a
ser lo que determine cuánta gente completa la integración.

---

## 4. Chat

Distíngase en dos productos, porque tienen costos muy distintos:

**Leer** (agregar los chats en una columna del panel). Riesgo bajo, valor alto,
transporte distinto por plataforma:

- Twitch: EventSub por WebSocket. Push, sin sondeo, sin cuota consumida. Fácil.
- YouTube: sondeo con cuota, según §3 solo viable con credenciales del usuario.
- Kick: **el tiempo real es por webhook entrante, no por WebSocket.** Eso significa que
  Splitstream necesita una URL pública alcanzable por HTTPS. En un portátil detrás de NAT
  no funciona. Encaja con el punto de TLS integrado del roadmap base: el chat de Kick se
  ofrece solo cuando el panel ya está publicado.

**Escribir** (mandar mensajes, moderar). Más scopes, más superficie de revisión, y
responsabilidad de moderación que hoy el proyecto no tiene. Dejarlo para después, si
acaso.

Persistencia: guardar el chat en SQLite junto a la sesión encaja con las tablas que ya
existen y hace que la vista de historial (§6) valga mucho más. Con retención, o la base
crece sin control.

---

## 5. Títulos y programación

Es la funcionalidad más barata en cuota —una escritura por transmisión, no miles— y
puede que sea la más vendible. "Cambia el título en las cuatro plataformas desde un solo
campo" es una frase que se entiende sin explicación.

Pero el premio de verdad está escondido en programar eventos:

> Si Splitstream crea la transmisión por API, también recibe la clave de ingesta por API.
> **Se acaba el copiar y pegar claves de stream.**

Ese es hoy el peor momento del onboarding: seis pestañas abiertas buscando dónde esconde
cada plataforma su clave. Eliminarlo justifica por sí solo el trabajo de OAuth, y encaja
perfecto con "probar destino" del roadmap base: si la clave la trajo la API, no hay clave
inválida que probar.

Alcance sensato: crear la transmisión programada (título, descripción, privacidad,
miniatura), vincularla, arrancar y terminar. Nada de gestión de catálogo de video.

---

## 6. Grabación y almacenamiento

La buena noticia: arquitectónicamente esto es **un sink más**. `relay.Hub` ya reparte a N
destinos; uno de ellos escribe a disco en lugar de a un socket. No toca el motor, no
requiere transcodificar, y reutiliza toda la maquinaria de cola y contrapresión.

**Regla innegociable, y es la que hay que escribir en el spec antes que el código:**
si el disco se atrasa, se degrada la grabación, nunca el directo. El sink de grabación
usa la misma cola acotada y la misma política de descarte que los demás. Un disco lento
que provoque drops en YouTube sería el peor bug posible del proyecto: perder la
transmisión en vivo por guardar una copia.

Diseño propuesto, en dos entregas:

**v1 — FLV.** Ya se manejan tags FLV; grabar es volcarlos a un archivo. Es casi trivial,
valida el sink completo y no arriesga nada. FLV es un formato muerto, sí, pero para
sacarlo se remuxea con ffmpeg en un segundo y se sabe en el minuto uno si el diseño
funciona.

**v2 — fMP4 fragmentado.** Sin transcodificar, solo muxeando. Reproducible sin
conversión, y resistente a que maten el proceso: lo escrito hasta el corte sigue siendo
válido. MediaMTX hace exactamente esto en Go y sirve de referencia.

En ambos casos:

- **Segmentación** cada N minutos. Un `kill -9` no puede costar tres horas de grabación.
- **Cuota de disco obligatoria.** Llenar el disco de un VPS tumba el relay, que es peor
  que no grabar. Umbral configurable, aviso al 80%, parada limpia con evento al llegar
  al límite. Comprobación previa al arrancar sesión.
- **Retención** por días y por gigas, con la de gigas mandando.
- Grabación por sesión, no por destino. Una entrada, un archivo.

**Almacenamiento remoto**, después y opcional: subida a S3-compatible (Backblaze B2,
Cloudflare R2, MinIO) **al terminar la sesión, no en vivo**. La subida en vivo añade
modos de fallo que compiten con el directo por ancho de banda de subida, que es
justamente el recurso escaso en este producto —recuérdese que ya se consume
`bitrate × destinos`.

---

## 7. Secuencia revisada

El roadmap base sigue vigente; esto lo reordena metiendo lo nuevo.

**v0.6 — Confianza en producción** *(sin cambios, sigue siendo lo primero)*
Probar destino · alertas y webhooks · `/healthz` y `/metrics` · retención y respaldo ·
deshabilitar tras N fallos.
*Nada de esto depende de las APIs y todo hace falta igual. No saltárselo.*

**v0.7 — Grabación**
Sink de grabación a FLV · segmentación · cuota de disco y retención · descarga desde el
panel. Después fMP4.
*Va antes que las APIs porque el riesgo es conocido, no depende de terceros y no puede
ser rechazado por un proceso de revisión.*

**v0.8 — Que instalarlo no duela**
Homebrew, winget, script de instalación · TLS integrado (habilita además el chat de Kick)
· rate limit correcto detrás de proxy · aviso de versión.

**v0.9 — Capa de plataformas, primera mitad**
Arquitectura de credenciales y almacén de tokens (cifrado con la clave maestra que ya
existe) · OAuth PKCE · Twitch completo: título, categoría, chat de lectura · capacidades
por destino en la interfaz.
*Twitch es el banco de pruebas barato. Si la arquitectura de esta fase está bien, las
demás plataformas son trabajo repetido, no diseño nuevo.*

**v0.10 — Capa de plataformas, segunda mitad**
YouTube: asistente de credenciales propias, título, programar transmisión, traer la clave
de ingesta automáticamente, chat con presupuesto de cuota visible · Kick sobre webhook.
*Aquí es donde desaparece el copiar y pegar de claves, que es el argumento de venta.*

**v0.11 — Alcance internacional**
Inglés en README y panel · vista de historial y post-mortem, ahora mucho más rica con
chat y grabación · comparativa honesta contra las alternativas.

**v1.0 — Endurecimiento**
Contingencia de `go-rtmp` · CI estricto (golangci-lint, govulncheck, integración
nocturna) · congelar el contrato de API y la política de migraciones.

**Fuera, salvo demanda que lo justifique**
Facebook (verificación de negocio) · X y TikTok (sin vía) · escritura en chat ·
subida en vivo al almacenamiento · multi-tenant.

---

## 8. Dos cosas para decidir antes de escribir código

**El almacén de tokens.** Los tokens de OAuth son secretos con la misma sensibilidad que
las claves de stream, y ya hay una primitiva buena para eso: `crypto/secret.go` con
AES-256-GCM bajo la clave maestra. Úsese la misma, con su tabla y su refresco automático,
y con el mismo enmascaramiento en logs que ya tienen las claves. Que no aparezca un
segundo mecanismo de secretos en el proyecto.

**Dónde vive la capa de plataformas.** Debe ser un paquete aparte —`internal/platforms`
con una interfaz por capacidad, no por plataforma— que el motor no importe nunca. El día
que Facebook cambie su API, o que Google rechace la verificación, o que X siga sin ser
viable, eso tiene que poder romperse, deshabilitarse o borrarse sin que el relay se
entere. Es lo que mantiene intacta la propiedad más valiosa del proyecto: que la parte
que transmite funciona siempre.
