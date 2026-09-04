# SDD ledger — logo por canal e interruptor maestro

Spec: `docs/superpowers/specs/2026-09-04-logo-y-maestro-design.md`
Rama: `feat/logo-y-maestro`
Ejecución: 2026-09-04, inline, sin plan escrito previo (el usuario dijo «implementa»)

## Lo que se pidió y lo que se hizo

El usuario pidió cuatro cosas: nombre por canal, logo, un botón de «pasar todas las
transmisiones» y una vista previa silenciada.

**El nombre ya existía** desde la migración inicial —campo en el diálogo, visible en la
tarjeta— y solo pasaba que se autorrellena con el nombre de la plataforma. El usuario
decidió dejarlo así y que la identidad la lleve el logo. Lo único que se hizo fue
documentarlo en el manual, porque que nadie lo contara es precisamente por lo que se pidió
como si faltara.

**La vista previa se sacó del alcance** y tendrá su propio diseño: el navegador no
reproduce RTMP y el `Hub` reparte a `*Sink`, un tipo concreto de destino RTMP y no una
interfaz, así que un consumidor «navegador» reestructura el fan-out. Además previsualizar
desde un VPS gasta tanta subida como un destino más, que es una decisión de producto y no
un detalle de implementación.

## Dos fallos ajenos encontrados por el camino

### `PRAGMA foreign_keys` sí estaba activo, y la migración 0003 perdía datos

El ledger de las fases 5 y 6 daba por abierto que las claves ajenas no se aplicaban.
**Era falso**: `foreign_keys(1)` está en el DSN de `Open` desde la fase 1. Comprobado por
ejecución antes de escribir nada, porque de ello dependía si el logo se borraba solo al
borrar el canal.

La observación original venía casi seguro de inspeccionar el archivo con el shell
`sqlite3`, que trae la pragma apagada por defecto. **Comprobar el comportamiento de la base
con una herramienta externa no dice cómo la abre el programa.**

Y tirando de ahí apareció algo peor. La migración 0003 reconstruye `destinations` con
`DROP TABLE` + `RENAME`, y su comentario justificaba no desactivar las claves ajenas con la
misma premisa falsa. Con ellas activas, **un `DROP TABLE` ejecuta un borrado implícito que
dispara las acciones referenciales**: el `ON DELETE SET NULL` de `events` convertía en NULL
el `destination_id` de todo el historial de quien actualizara desde una base anterior a la
v0.5.0. Reproducido con un test que parte de una base en la versión 2 con datos.

Ese daño ya está hecho y no se puede deshacer: las filas afectadas no guardan a qué destino
pertenecían. Lo que se arregló es que no vuelva a pasar — `migrate()` apaga las claves
ajenas mientras aplica cada migración, que es el procedimiento que prescribe SQLite— y en
particular que una reconstrucción futura no se lleve por delante los logos, que cuelgan de
`destinations` con `CASCADE`.

En una instalación nueva no se notaba nada, porque cuando la migración corre no hay filas
que perder. Por eso ningún test lo veía: todos parten de una base vacía.

### La lista de formatos de imagen no era una lista

`image.Decode` usa un **registro global al proceso** que se llena con importaciones en
blanco. Restringir los formatos importando solo `png` y `jpeg` no restringe nada: basta con
que cualquier otro paquete del binario importe `image/gif` para que empiecen a aceptarse
GIFs sin que nadie lo decida.

Lo descubrió el propio test. `TestRechazaGIF` importa `image/gif` para fabricar el GIF que
debía rechazar, y esa importación lo habilitaba: el test pasaba en verde un archivo que la
implementación creía estar rechazando. Ahora el formato se despacha a mano por los bytes de
la firma, así que la lista de formatos aceptados es una función y nada más. La importación
de `image/gif` se queda en el test a propósito, con un comentario que dice que no se borre.

### El test intermitente de `relay` no estaba arreglado

CI tumbó el PR con `TestHubSlowSinkDoesNotBlockOthers`, el mismo test que el ledger de las
fases 5 y 6 daba por resuelto subiendo su plazo de 3 a 20 segundos. Falló a los 20,02 s.

Aquel diagnóstico era falso, y la medición que lo respaldaba se hizo en local, sin `-race`
y con ocho núcleos. Reproducido con `GOMAXPROCS=2` y `-race`, que es lo que se parece a un
runner de CI, se ve lo que pasa de verdad: `escritos=0`, `descartes=300`, la cola vacía y el
sink en `live`. **El destino rápido no iba lento: estaba tirando la ráfaga a propósito.**
Cuando el consumidor se queda sin CPU, la cola descarta el retraso por GOP y el sink se
reengancha en el siguiente keyframe, que es justo lo que el diseño manda.

Esperar más nunca lo iba a arreglar, porque el plazo no deshace un descarte. El test ahora
prueba la propiedad que le da nombre —que el rápido sigue vivo mientras el lento está
atascado—: comprueba que el lento no escribió nada, espera a que el rápido drene y le manda
un keyframe nuevo, que debe llegar.

La lección se parece a las otras dos de este trabajo: una premisa que nadie volvió a
comprobar. Aquí la premisa era mía, y la medición que la sostenía no reproducía las
condiciones en las que el fallo ocurría.

## Decisiones

**Los bytes van en la base, en tabla aparte.** En la base para que copiar `splitstream.db`
siga copiando el estado completo y el volumen único de Docker siga bastando. Aparte de
`destinations` porque el listado se consulta cada vez que se pinta el panel y no debe
arrastrar imágenes.

**El `logo_etag` viaja en el WebSocket, y es lo correcto.** Al escribir el spec dije que no
iría; comprobarlo contra el código lo desmintió: `statusDTO.Destinations` es
`[]destinationDTO` y hay un test que exige que el snapshot REST y el push tengan la misma
forma. Separarlos rompería la propiedad que permite que la interfaz arranque con el GET y
siga con el WS. En vez de eso el etag se acorta a 16 caracteres —64 bits del SHA-256—,
porque a un push por segundo la diferencia se nota en una conexión móvil.

**Se mira el tamaño antes de decodificar píxeles.** El límite de 2 MB del cuerpo HTTP no
protege de una bomba de descompresión: un PNG de pocos KB puede declarar 30000×30000 y
pedir gigabytes. El test fabrica esa cabecera a mano —firma e IHDR, que es todo lo que hace
falta— en vez de generar una imagen enorme de verdad.

**Lo que entra no se guarda tal cual.** Se decodifica y se vuelve a codificar a PNG, lo que
además tira los metadatos del original: en una foto de móvil, la geolocalización.

**El reescalado se escribió a mano** —promediado de área, unas treinta líneas— en vez de
traer `golang.org/x/image`. El panel ya renunció a una fuente de iconos entera para ahorrar
700 KB del binario; un módulo nuevo por una función de escalado no encaja con ese criterio.

**El maestro manda el estado deseado, no una orden de invertir.** Con unos canales
encendidos y otros no, invertir dejaría la mitad al revés de lo que el usuario acaba de
pulsar. El campo `enabled` es un puntero para distinguir `false` de «no vino»: sin eso, un
cuerpo vacío apagaría todos los canales en mitad de una emisión.

## Verificación

Suite completa de Go en verde. Además, ejecutado contra el binario real sobre una instancia
desechable, porque los fallos de las fases 5 y 6 solo aparecieron al ejecutar:

- Un PNG de 900×450 se guarda como 256×128 y 767 bytes.
- El `GET` devuelve `image/png`, `ETag`, `Cache-Control: private…` y `X-Content-Type-Options:
  nosniff`; con `If-None-Match` responde 304 con cuerpo de 0 bytes.
- Un SVG con `<script>` renombrado a `.png` → 400, «ese archivo no es una imagen PNG o JPEG».
- Un archivo de 3 MB → 413, «la imagen pesa más de 2 MB».
- Sin sesión → 401.
- El estado completo con logo mide 746 bytes y no contiene bytes de imagen.
- El maestro enciende los dos canales y los apaga; sin `enabled` responde 400.
- El logo sobrevive a parar y arrancar el proceso: mismo SHA antes y después.

## Lo que queda abierto

- **La vista previa**, con su propio spec.
- **Sin recorte ni encuadre.** Se sube una imagen y se reduce; si viene muy apaisada, se
  verá apaisada dentro del avatar. Recortar es una interfaz entera y nadie la ha pedido.
- **El panel no se pudo capturar autenticado**, la misma limitación de la fase 5: la
  extensión espera a `document_idle` y el panel empuja estado cada segundo. La interfaz del
  logo y del interruptor la tiene que mirar el usuario.
- **Los eventos que perdieron su destino** con la migración 0003 no se pueden recuperar.
- **La suite local no corre con `-race` ni con los núcleos limitados.** Los dos fallos
  intermitentes de este proyecto se han visto primero en CI. Correr al menos el paquete
  `relay` con `GOMAXPROCS=2 -race` antes de empujar cuesta un minuto y habría ahorrado las
  dos rondas.
