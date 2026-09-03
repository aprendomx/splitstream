# SDD ledger — fases 5 y 6

Spec: `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (§10 frontend, §12 operación)
Rama: `feat/fase-4-api-http` (se siguió usando; ver la nota de proceso al final)
Ejecución: 2026-09-03, inline, dirigida por el usuario paso a paso

Estas dos fases no tuvieron plan escrito previo, a diferencia de la 1 a la 4. El usuario
fue pidiendo las piezas —panel, rejilla, asistente, créditos, manuales, binarios, Docker—
y cada una se construyó y verificó en el momento. Este ledger es el registro.

## Fase 5 — Panel web

### Decisiones

**Vite con `@quasar/vite-plugin` en lugar del CLI de Quasar.** El CLI es interactivo al
crear el proyecto y eso no sirve para una CI. Es el mismo Quasar 2 sobre Vue 3 que pide el
spec §5, con un build reproducible.

**El paquete de embed vive en `web/` y no en `internal/web/`** como decía el spec §4:
`go:embed` no admite `..` en sus patrones, así que el archivo tiene que estar en un
directorio que contenga al `dist`.

**La validación no se duplica en el cliente.** Decisión del usuario: «la mayoría de los
errores serán cachados por el backend». La API ya devuelve mensajes escritos para personas
y es la única fuente de verdad; el frontend solo comprueba lo mínimo para no mandar
peticiones vacías.

**El estado se traduce a un diagnóstico accionable.** Es la decisión de diseño más
importante del panel, y sale directamente de la semana: los tres fallos que sufrimos
—Twitch rechazando el handshake, Facebook con el cupo lleno, y nuestro propio bucle de
reintentos— producían **el mismo `broken pipe`** en el log. El estado y los contadores sí
los distinguen, así que la tarjeta dice «Conecta y se corta» con el consejo de revisar el
límite de emisiones, en lugar de enseñar la traza.

**Los iconos entran uno a uno como SVG.** Importar el CSS de `mdi-v7` metía 394 KB de
woff2 más 574 KB de woff en el binario para usar doce glifos. El panel pasó de 1,9 MB a
712 KB. Se sirve a un móvil, a veces con mala cobertura, a mitad de una transmisión.

**La lista de canales es una rejilla.** A petición del usuario tras verla: en un monitor
ancho, una tarjeta por fila dejaba medio panel vacío.

### Fallos encontrados y corregidos

- **El `.gitkeep` que hace compilable un clon limpio no se versionaba.** El `.gitignore` de
  la raíz ya excluía `/web/dist/`, y git no permite reincluir un archivo cuyo directorio
  padre está excluido. Además Vite lo borraba en cada build. Verificado clonando de verdad.
- **El fallback del panel devolvía `index.html` para cualquier ruta**, incluido
  `/favicon.ico`: HTML donde el navegador espera otra cosa. Ahora una ruta con extensión
  que no existe da 404.
- **`http.FileServer` redirige `/index.html` a `./` con un 301**, así que recargar en una
  ruta del cliente devolvía un redirect en vez del panel. Se escribe el index a mano.
- **Una ruta `/api/` desconocida devolvía HTML.** El cliente fallaría al parsear con un
  error que no dice nada. Ahora hay un cortafuegos que responde JSON.
- **El WebSocket no se conectaba al recargar la página**: solo lo hacía `entrar()`, y con
  la cookie válida se entra por `cargar()`. El panel se quedaba con la foto del GET inicial.
- **La lista de arrastre era un `computed` cuyo getter devolvía un array nuevo** en cada
  evaluación; con `v-model` sobre `vuedraggable` eso se realimenta.
- **Un icono usado sin importar** que el build no detecta, porque en una plantilla Vue una
  variable indefinida no rompe la compilación. Se añadió una comprobación.

### Limitación de la verificación

**No se pudo capturar el panel autenticado.** La extensión de navegador espera a
`document_idle` y el panel nunca está ocioso: tiene un WebSocket que empuja estado cada
segundo. La pantalla de login sí se captura. Se descartó que fuera un bucle de JS mirando
el consumo de CPU del renderizador, que estaba en el 4 %. El usuario revisó el resultado.

## Fase 6 — Empaquetado

### Binarios descargables

Cinco plataformas —macOS Intel y Apple Silicon, Linux x86_64 y arm64, Windows— construidas
y adjuntadas a la release al empujar una etiqueta `v*`, con checksums.

La compilación cruzada solo es posible porque no hay cgo: `modernc.org/sqlite` es SQLite
reescrito en Go puro. Con el driver de C haría falta un toolchain por plataforma y esta
matriz no existiría.

El workflow comprueba que **el panel viaja dentro de cada binario**: uno sin panel arranca
igual, y el fallo no se vería hasta que alguien abriera la página.

**Hallazgo tras publicar la v0.5.0:** el usuario se topó con Gatekeeper —«Apple no pudo
verificar que splitstream no contenga software malicioso»— y mi verificación no lo había
cubierto, porque descargué el artefacto con `gh` y la marca de cuarentena la pone el
navegador. El binario lleva solo firma ad-hoc (`Identifier=a.out`), suficiente para
ejecutarse en Apple Silicon pero no para Gatekeeper. Firmarlo de verdad exige una
suscripción de desarrollador de Apple. Documentado en el README con las dos vías que sí
funcionan; el «clic derecho → Abrir» clásico ya no siempre las ofrece.

### Docker

Imagen de 18,5 MB sobre `scratch`, sin shell, corriendo como usuario sin privilegios y con
el sistema de archivos en solo lectura salvo su base.

Dos cosas que **solo aparecieron al ejecutarla**, no al construirla:

- **`unable to open database file (14)`**: el volumen `/data` pertenecía a root y el
  contenedor corre como 65532. En `scratch` no hay shell para un `mkdir`, así que el
  directorio se crea en la etapa anterior con su dueño y se copia con `--chown`.
- **El asistente pide el código desde Docker**, porque la petición llega por la red puente
  y no por loopback. Es lo conservador y correcto —no hay forma de distinguir Docker en un
  portátil de Docker en un servidor expuesto— pero había que documentar dónde mirarlo.

Los **certificados raíz** se copian explícitamente: sin ellos, cualquier destino `rtmps://`
falla con «certificate signed by unknown authority», y Facebook solo acepta RTMPS. Una
imagen sin ellos parece funcionar hasta que alguien vincula su primer canal. La CI lo
comprueba exportando el sistema de archivos de la imagen, porque en `scratch` no hay shell
para mirarlo desde dentro.

### CI

Cuatro jobs: el rápido (vet, build, `-race`, aislamiento de capas), el panel, la imagen de
Docker —construida **y ejecutada**, porque el fallo de permisos solo aparecía al arrancar—
y la integración contra `mediamtx`. La unidad de systemd se valida con `systemd-analyze`.

### Documentación

- **Manual de instalación** en el README, para quien descarga un ejecutable y no ha
  compilado nada nunca.
- **Manual de usuario** aparte, con lo que solo se aprende usando esto contra plataformas
  reales: el intervalo de fotogramas clave que YouTube exige y que OBS no pone en modo
  Simple, el techo de 6 Mbps de Twitch, el límite de emisiones activas de Facebook y qué
  hacer cuando se agota, y que TikTok da un servidor distinto en cada emisión.
- **Créditos y licencias** en el panel, leídas de los propios paquetes: `go-rtmp` resultó
  ser Boost Software License y no MIT, y `coder/websocket` es ISC.

## Asistente de primer arranque

El usuario pidió cubrir dos casos: PC de escritorio y servidor. En la fase 4 había
descartado el setup por interfaz precisamente por el riesgo de que «quien llegue primero al
panel se lo queda».

**Se resolvió sin preguntar, porque el servidor ya sabe la respuesta:** si la petición
viene de loopback, quien está en el teclado ya controla el equipo y el asistente no pide
nada; si viene de fuera, exige un código de un solo uso que el binario imprime al arrancar.
Preguntar «¿estás en un PC?» habría sido peor: es una pregunta que la gente responde mal y
que un atacante responde que sí.

El endpoint deja de existir en cuanto hay contraseña, lleva el mismo limitador que el
login, el código se compara en tiempo constante y su error no distingue «casi» de «nada».

## Nota de proceso: dos errores de verificación

Dos veces encadené `go test ... | tail && git commit`. El estado de salida de una tubería
es el del último comando, así que el commit se ejecutó **con la suite en rojo** la primera
vez, y la segunda un `echo "COMPILA"` se imprimió tras un build fallido. Desde entonces los
códigos de salida se capturan en variables antes de decidir nada.

También hubo un test cuya aserción estaba mal planteada: prohibía la palabra «contraseña»
en la salida de arranque, y el aviso del primer arranque dice legítimamente «elige tu
contraseña». Confundía nombrar un concepto con filtrar un secreto.

## La carrera de go-rtmp, predicha en la fase 2 y resuelta aquí

El ledger de la fase 2 dejó anotado esto como riesgo abierto:

> go-rtmp v0.0.7 tiene una carrera propia en su CLIENTE, entre `(*streams).Delete` y su
> goroutine de lectura, que aflora al llamar a `Publisher.Close()`. Si aparece, hay que
> decidir qué hacer, no parchearla a ciegas.

Apareció bajo `-race` al ejecutar la suite completa. El informe la señala con precisión:
`streams.Delete()` toma el mutex del mapa de streams, pero `streams.At()` —que usa la
goroutine de lectura de la conexión— **lo lee sin tomarlo** (`streams.go:86`). Uno protege
y el otro no.

**No es un problema de tests.** En producción `Close()` se llama en cada reconexión —con
Facebook aleteando fueron 55 seguidas— y una escritura concurrente sobre un mapa de Go
puede abortar el proceso con `concurrent map writes`, llevándose por delante la emisión a
todos los destinos a la vez.

La decisión: **no mandar `deleteStream` al cerrar**. Es lo único nuestro que llega a
`streams.Delete`. Cerrar el socket termina el stream igual, `FCUnpublish` ya le dijo a la
plataforma que suelte el slot, y `ClientConn.Close` solo cierra la conexión sin tocar ese
mapa. Verificado con 20 corridas del test que la destapó y tres pasadas completas de la
suite: cero carreras.

Es también la explicación más probable del «fallo intermitente sin identificar» que quedó
abierto en el ledger de la fase 4: se vio una vez, no se reprodujo en 19 intentos, y esta
carrera tiene exactamente ese perfil.

## La clave maestra se crea sola

Petición del usuario: «cuando ejecuto desde Finder, debería crear y autoasignar la llave».
Tenía razón — al abrir el binario con doble clic no hay variables de entorno, así que el
programa moría con «falta SPLITSTREAM_MASTER_KEY» y no había forma de arrancarlo sin
terminal.

**La decisión con coste:** guardar la clave junto a la base cambia el modelo de seguridad.
Antes la base cifrada sola era inútil; ahora quien tenga la carpeta lo tiene todo. Para un
escritorio es lo razonable —quien accede a tus archivos ya accede a tus archivos— y para un
servidor no, así que `SPLITSTREAM_MASTER_KEY` **manda siempre** cuando está puesta y el
camino del servidor no cambia.

El archivo se crea con permisos `0600` y con `O_EXCL`, para que dos procesos arrancando a la
vez no se pisen: pisarlo dejaría las claves de los destinos ilegibles para siempre. Un
archivo de clave vacío es un error y no una invitación a generar otra, por la misma razón.

Un test que afirmaba que faltar la variable es un error se reescribió: ese comportamiento
cambió a propósito. Lo que sigue siendo error es una variable puesta con un valor
inservible, porque ahí hubo intención.

## Lo que queda abierto

- **`ON DELETE SET NULL` no se aplica**, porque nadie activa `PRAGMA foreign_keys`.
  Comprobado por ejecución: tras borrar un destino, sus eventos quedan apuntando a una fila
  que ya no existe. Una línea de arreglo.
- **TikTok está soportado en el modelo pero sin probar** contra la plataforma.
- **Los binarios no están firmados ni notarizados**, así que macOS y Windows avisan.
- **Sin imagen publicada en un registro.** El `docker-compose.yml` construye desde el
  código; publicar en GHCR es una decisión del dueño del repositorio, no mía.
