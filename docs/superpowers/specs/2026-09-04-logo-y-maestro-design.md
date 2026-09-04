# Splitstream — Logo por canal e interruptor maestro

**Fecha:** 2026-09-04
**Estado:** aprobado, pendiente de plan de implementación
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
**Versión de partida:** v0.6.0

## 1. Qué se construye

Dos cosas pequeñas e independientes, pedidas por el usuario:

1. **Un logo por canal.** Cada destino puede tener una imagen propia que lo identifique
   en el panel de un vistazo.
2. **Un interruptor maestro.** Un solo control que enciende o apaga todos los canales a la
   vez, en vez de tocar el interruptor de cada tarjeta.

Fuera de alcance de este diseño: la **vista previa del vídeo** en el panel, que el usuario
también pidió. Es un subsistema nuevo —el navegador no reproduce RTMP y el `Hub` reparte a
`*Sink`, un tipo concreto de destino RTMP, no una interfaz— y tiene una consecuencia de
producto propia: previsualizar desde un VPS gasta tanto ancho de banda de salida como un
destino más. Se diseña aparte para que no bloquee esto.

**El nombre por canal no se toca.** El usuario lo pidió como si faltara, pero
`destinations.name` existe desde la migración inicial, el diálogo tiene su campo y la
tarjeta lo muestra. Se autorrellena con el nombre de la plataforma y el usuario decidió
dejarlo así: la identidad visual la va a llevar el logo.

## 2. Decisiones tomadas

| Decisión | Elegido | Por qué |
| --- | --- | --- |
| Dónde viven los bytes | BLOB en SQLite, tabla aparte | La base sigue siendo el estado completo: copiarla copia los logos, y el volumen único de Docker basta |
| Tabla aparte y no columna | `destination_logos` | El listado de destinos se consulta cada vez que se pinta el panel; no debe arrastrar imágenes |
| Formatos aceptados | PNG y JPEG | Cubren lo que la gente tiene; ver §4.2 para por qué SVG no |
| Qué se guarda | Siempre PNG, ≤256×256 | Lo que entra no se sirve nunca tal cual |
| Reescalado | A mano, promediado de área | Evita traer `golang.org/x/image` por una función |
| Semántica del maestro | Todos on / todos off | Elegido por el usuario frente a «solo encender» y a «reenganche en caliente» |

## 3. Modelo de datos

Migración `0004_destination_logos.sql`:

```sql
CREATE TABLE destination_logos (
    destination_id INTEGER PRIMARY KEY REFERENCES destinations (id) ON DELETE CASCADE,
    image          BLOB    NOT NULL,
    etag           TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
);
```

`ON DELETE CASCADE` **sí se aplica**, y borrar el destino se lleva su logo sin código extra.

Esto corrige una nota equivocada del ledger de las fases 5 y 6, que daba por abierto que
`PRAGMA foreign_keys` no estaba activo. Sí lo está: viene en el DSN de `Open`
(`db.go:79`). Comprobado por ejecución sobre el código actual — `PRAGMA foreign_keys`
devuelve 1, un `ON DELETE CASCADE` sobre `destinations` borra la fila dependiente, y el
`ON DELETE SET NULL` de `events` pone el `destination_id` a NULL en lugar de dejar un
huérfano. La observación original venía casi seguro de mirar el archivo con el shell
`sqlite3`, que trae `foreign_keys` apagado por defecto y no refleja cómo abre el programa.

El test de que borrar un canal se lleva su logo se escribe igual: lo que garantiza es la
propiedad, no la implementación, y protege de que alguien toque el DSN.

`etag` son los primeros 64 bits del SHA-256 de los bytes ya normalizados, en hexadecimal:
16 caracteres. Sirve para dos cosas: responder 304 en el `GET`, y romper la caché del
navegador cuando el logo cambia. Va corto a propósito porque viaja en cada push del
WebSocket; el porqué está en §4.1.

`updated_at` usa `formatTime` como el resto de las tablas: ancho fijo, para que el orden de
texto coincida con el cronológico.

## 4. API

### 4.1 Endpoints

Los tres van tras `requireSession`, como todo lo demás salvo el login y el setup.

| Método | Ruta | Cuerpo | Respuesta |
| --- | --- | --- | --- |
| `PUT` | `/api/destinations/{id}/logo` | `multipart/form-data`, campo `file` | 200 con el DTO del destino |
| `GET` | `/api/destinations/{id}/logo` | — | 200 `image/png`, o 304, o 404 si no tiene |
| `DELETE` | `/api/destinations/{id}/logo` | — | 200 con el DTO del destino |
| `POST` | `/api/destinations/toggle-all` | `{"enabled": true}` | 200 con la lista completa |

El `GET` responde con `ETag: "<etag>"` y `Cache-Control: private, max-age=0,
must-revalidate`, y devuelve 304 cuando el `If-None-Match` coincide. Sin esto el panel se
descargaría todos los logos en cada recarga.

El DTO de destino gana un campo `logo_etag`. Cadena vacía significa «sin logo». Es lo único
que el frontend necesita para decidir si pinta un avatar y con qué URL.

**Eso lo mete también en el WebSocket, y es lo correcto.** `statusDTO.Destinations` es
`[]destinationDTO`, y el spec base §10 exige que el snapshot REST y el push del WS tengan
exactamente la misma forma —hay un test, `TestWebSocketPayloadMatchesTheRESTSnapshot`, que
falla si divergen—. Separarlos para ahorrar unos bytes rompería la propiedad que permite
que la interfaz arranque con el GET y siga con el WS.

Lo que sí se hace es abaratar el campo: el `etag` que viaja son **16 caracteres
hexadecimales**, los primeros 64 bits del SHA-256. Para romper una caché eso sobra, y a un
push por segundo la diferencia frente a 64 caracteres se nota en una conexión móvil durante
una emisión larga.

Lo que el WebSocket no lleva nunca son **bytes de imagen**, y eso sí tiene su test.

### 4.2 Validación de la subida

El orden importa, porque cada paso protege al siguiente:

1. `http.MaxBytesReader` a 2 MB antes de leer nada. Un cuerpo mayor se rechaza sin
   cargarlo en memoria.
2. El tipo se decide **por los bytes**, con `image.DecodeConfig`, no por la extensión del
   archivo ni por el `Content-Type` que declare el cliente. Ambos los escribe quien sube.
3. Solo PNG y JPEG. **SVG se rechaza a propósito**: un SVG puede contener `<script>`, y se
   serviría desde el mismo origen que el panel, con la cookie de sesión presente. Es un XSS
   almacenado servido por nosotros mismos. `image.DecodeConfig` ya lo descarta por no ser
   un formato registrado, pero el rechazo es explícito y tiene su test.
4. Se decodifica, se reduce para caber en 256×256 conservando la proporción, y se
   re-codifica a PNG. Si la imagen ya es más pequeña, no se amplía.

Lo que queda guardado es siempre PNG y siempre de pocos KB, sin importar qué entró.

Los mensajes de error los escribe el backend, que es la única fuente de verdad para el
panel (decisión de la fase 5): «Ese archivo no es una imagen PNG o JPEG», «La imagen pesa
más de 2 MB».

### 4.3 Reescalado sin dependencias

El reescalado es un promediado de área: cada píxel de salida es la media de los píxeles de
entrada que le corresponden. Son unas treinta líneas sobre `image` de la biblioteca
estándar y da buena calidad justo en el caso que nos ocupa, que es reducir mucho.

La alternativa era `golang.org/x/image/draw` con `CatmullRom`. Se descarta porque el panel
ya renunció a una fuente de iconos entera para ahorrar 700 KB del binario; traer un módulo
nuevo por una función de escalado no encaja con ese criterio.

### 4.4 Interruptor maestro

`POST /api/destinations/toggle-all` recibe `{"enabled": true|false}` y lo aplica a todos los
destinos en una transacción. Por cada destino que **cambió** llama a `applyHot`, que es el
mismo camino que ya usa el toggle individual para enganchar o soltar un sink en caliente.

Es idempotente: si todos los canales ya están como se pide, no se escribe nada, no se llama
a `applyHot` y no se generan eventos. Encender diez veces seguidas no debe llenar el
historial.

## 5. Interfaz

**Tarjeta.** Con logo, el avatar de 28 px lo ocupa la imagen y el icono de la plataforma
baja a sello pequeño sobre su esquina: se gana identidad sin perder de vista a qué servicio
va ese canal. Sin logo, la cabecera queda exactamente como hoy. El avatar lleva `alt` con
el nombre del canal.

**Diálogo de destino.** Una zona de subida con vista previa y un botón de quitar. En el
alta, el destino todavía no tiene `id`, así que el logo se manda en una segunda petición
justo después de crearlo: una sola acción del usuario, dos peticiones. Si la creación va
bien y la subida del logo falla, **el destino queda creado** y se avisa de que el logo no se
guardó. No se deshace nada a espaldas del usuario.

**Cabecera del panel.** Un interruptor «Todos los canales» junto al botón de añadir, con
tres estados visuales: todos encendidos, todos apagados, y mezcla. Apagarlo mientras hay
emisión en curso pide confirmación diciendo cuántos canales se van a cortar — es la misma
regla que ya se aplica al borrar un destino.

## 6. Pruebas

**Store**

- Guardar un logo, leerlo y volver a leerlo tras reemplazarlo.
- Borrar un destino borra su logo: no queda fila huérfana. Cubre el `ON DELETE CASCADE`, y
  con él que nadie apague `foreign_keys` en el DSN sin enterarse.
- `ListDestinations` no trae bytes de imagen.

**API**

- Un PNG válido se acepta y el destino queda con `logo_etag` no vacío.
- Un SVG que se declara `image/png` se rechaza.
- Un cuerpo de más de 2 MB se rechaza sin leerlo entero.
- Una imagen de 2000×2000 se guarda reducida: el PNG devuelto mide 256 o menos por lado.
- `GET` con el `If-None-Match` correcto responde 304.
- `GET`, `PUT` y `DELETE` del logo sin sesión responden 401.
- El payload del WebSocket no contiene bytes de imagen.
- El WebSocket y `GET /api/status` siguen teniendo la misma forma con el campo nuevo: el
  test que ya existe debe seguir en verde.

**Interruptor maestro**

- Enciende todos los canales de una vez.
- Es idempotente: repetirlo no genera eventos nuevos.
- Con una sesión viva, encender arranca los sinks de los canales que estaban apagados.

## 7. Lo que este diseño no resuelve

- **La vista previa** queda para su propio spec, por lo dicho en §1.
- **Nada sobre `PRAGMA foreign_keys`**: resultó estar activo ya, así que no hay nada que
  arreglar. Lo que sí queda es corregir la nota del ledger que decía lo contrario.
- **No hay recorte ni encuadre.** Se sube una imagen y se reduce; si viene muy apaisada,
  se verá apaisada dentro del avatar. Recortar es una interfaz entera y nadie la ha pedido.
