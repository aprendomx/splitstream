# Splitstream — v0.9 «Grabación»

**Fecha:** 2026-09-10
**Estado:** aprobado por el plan maestro (decisión D6 confirmada); pendiente de plan de implementación
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`
**Roadmap:** `docs/superpowers/specs/2026-09-09-roadmap-mejoras.md` §6 y §7
**Plan maestro:** `docs/superpowers/plans/2026-09-09-roadmap-ejecucion.md` §4
**Versión de partida:** `v0.8.0` (`main` @ `9f4e704`)

## 1. Qué se construye

Grabar la sesión de ingesta a disco **como un sink más**, sin transcodificar: el mismo
`relay.Sink`, con la misma cola acotada y la misma política de descarte que cualquier
destino, cuyo `Publisher` escribe tags FLV en archivos segmentados en vez de mandarlos por
un socket.

Entra en esta entrega (B.1–B.3 del plan maestro):

1. **Escritor FLV** (`internal/record`) que implementa `relay.Publisher`, con
   **segmentación** cada N minutos en keyframe.
2. **Cuota de disco** obligatoria (aviso al 80 %, parada limpia al llegar al tope) y
   comprobación previa antes de arrancar la grabación de una sesión.
3. **Retención** por días y por gigas —la de gigas manda— integrada en el planificador
   diario de la v0.8.
4. Tablas `recordings` y `recording_settings`; API para ajustes, listado, descarga y
   borrado; panel: bloque «Grabación» en Ajustes, chip «Grabando» en el panel y página
   «Grabaciones».

Queda para la **v0.9.1** con spec propio: **fMP4 fragmentado** (B.4). Es la pieza con más
código propenso a error del roadmap y no cambia nada de lo anterior: será un segundo
`Publisher` detrás de la misma `Options` y una columna `format` más.

**La regla innegociable** (roadmap §6), y va en el spec antes que el código: **si el disco
se atrasa, se degrada la grabación, nunca el directo.** El sink de grabación cuelga del hub
con un `Enqueue` no bloqueante como todos; un disco lento llena SU cola y descarta SU vídeo
por GOP. Ningún camino de la grabación llama a `Sync` ni bloquea en el hilo de `Publish`.

## 2. Enmiendas al spec base

- **§1** «Fuera de alcance de forma explícita y permanente: … grabar a disco» → revertido
  el 2026-09-10: «Grabar sin transcodificar es muxear: entra como un `Publisher` más
  detrás de la misma cola y la misma política de descarte que cualquier destino. Sigue
  fuera: transcodificar, ABR, chat de escritura, multi-tenant.»
- **§6.2** El hub admite un sink con `ID = relay.RecorderSinkID (-1)` que no corresponde a
  ninguna fila de `destinations`. Sus eventos van sin `destination_id` y con `kind`
  `recording_*`.
- **§6.5 / relay.SinkProvider** pasa a recibir el id de sesión:
  `func(sessionID int64) ([]*Sink, error)`. La grabación necesita saber a qué sesión
  pertenece cada archivo.
- **§7** tablas `recordings` y `recording_settings` (migración 0006).
- **§9** endpoints nuevos (§6 de este spec).
- **§12** `SPLITSTREAM_RECORDINGS_DIR` (por defecto `recordings/` junto a la base, como el
  archivo de clave).

## 3. El escritor FLV

`internal/record` importa `relay` (por `Publisher` y `Message`) y stdlib. Nada del motor
importa `record`: entra por `sinks.Factory`, que es la capa de composición.

```go
type Options struct {
    Dir            string        // directorio de la sesión; se crea con 0o700
    SessionID      int64
    SegmentMinutes int           // 0 = sin segmentar
    OnSegment      func(Segment) // al cerrar cada archivo (rotación o Close)
    Quota          Quota         // ver §4
    Now            func() time.Time
    Logger         *slog.Logger
}
type Segment struct {
    Path       string
    Index      int       // 1, 2, 3…
    StartedAt  time.Time
    EndedAt    time.Time
    Bytes      int64
    DurationMS uint32
}
func NewFLVWriter(o Options) *FLVWriter
var _ relay.Publisher = (*FLVWriter)(nil)
```

- **Archivos:** `<Dir>/<AAAAMMDD-HHMMSS>-<nn>.flv`, abiertos con `O_EXCL`. `Connect` abre
  el primero tras comprobar la cuota (§4). Cabecera FLV con flags audio+vídeo y
  `PreviousTagSize0`.
- **Tags:** tipo 18 (script) para el `onMetaData` —el payload que circula por el hub ya es
  el cuerpo AMF0 `"onMetaData" + ECMA array`, se escribe tal cual—, 8 para audio, 9 para
  vídeo; datasize de 3 bytes, timestamp de 3 + 1 extendido, streamID 0,
  `PreviousTagSize = 11 + len`.
- **Escritura:** `bufio.Writer` de 1 MiB. Un `Flush` cada 2 s de tiempo de media, nunca
  `Sync` en el camino caliente. `Sync` solo al cerrar un archivo.
- **Segmentación:** en el primer keyframe cuyo timestamp supere `SegmentMinutes` desde el
  arranque del segmento se cierra el archivo (flush, sync, `OnSegment`), se abre el
  siguiente, se reescribe el preámbulo cacheado —`onMetaData`, AVC seq header, AAC seq
  header, los tres con `ts = 0`— y se **rebasa** el timestamp al del keyframe de arranque.
  El audio anterior a esa base se descarta (misma regla que el sink, spec base §3.2). El
  preámbulo se cachea al recibirlo: el sink lo manda una vez por conexión, y cada segmento
  es «una conexión» para el reproductor.
- **Errores de escritura** (disco lleno, permisos) se devuelven al sink, que los trata
  como conexión perdida: backoff, `NewPub` nuevo, `Connect` vuelve a comprobar la cuota y
  falla con `ErrDiskFull` mientras no haya sitio. Tras `SuspendAfterAttempts` el sink queda
  `suspended` como cualquier destino: la sesión sigue, la grabación no.
- **Close** es idempotente: flush, sync, cierra y llama a `OnSegment` del último segmento.

## 4. Cuota de disco y retención

```go
type Quota struct {
    MaxBytes int64   // tope de la suma de grabaciones (recording_settings.max_gb)
    UsedBytes int64  // lo que ya ocupan (lo aporta el store)
    MinFree  int64   // espacio libre mínimo del sistema de archivos: 512 MiB
    WarnAt   float64 // 0.8
}
func FreeSpace(dir string) (free, total int64, err error) // Statfs en unix, GetDiskFreeSpaceExW en Windows; sin dependencias nuevas
```

- **Antes de arrancar una sesión**: si `UsedBytes >= MaxBytes` se intenta primero la poda
  (§4.2) y, si sigue sin sitio, la grabación **no arranca** y queda evento
  `recording_skipped_quota` (warn). Si `free < MinFree`, igual con
  `recording_skipped_disk` (warn). La sesión y los destinos no se enteran.
- **En cada rotación**: la misma comprobación. Si no hay sitio se cierra el segmento en
  curso y `Connect` del siguiente falla con `ErrDiskFull`; evento
  `recording_stopped_disk_full` (error), una sola vez por sesión.
- **Aviso al 80 %**: al cruzar `WarnAt · MaxBytes`, evento `recording_disk_warning`
  (warn), una vez por sesión.
- Un `kill -9` no puede costar más de un segmento (§3): cada archivo cerrado ya está
  sincronizado, y el FLV en curso es reproducible hasta el último flush.

### 4.2 Retención

Job `grabaciones` del `maintenance.Scheduler` (v0.8): (1) borra las grabaciones con
`ended_at` anterior a `keep_days`; (2) mientras la suma de bytes supere `max_gb`, borra
la más antigua. Archivo primero, fila después; un archivo que ya no existe se da por
borrado. Nunca con sesión viva (regla del planificador). Evento `recording_pruned`
(info) con el resumen.

## 5. Modelo de datos

Migración `0006_recordings.sql`. Sin `ALTER TABLE`: los tests rebobinan `user_version` y
reaplican migraciones, y SQLite no tiene `ADD COLUMN IF NOT EXISTS`. Los ajustes van en una
tabla propia de fila única, como `settings`.

```sql
CREATE TABLE IF NOT EXISTS recording_settings (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    enabled      INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    segment_min  INTEGER NOT NULL DEFAULT 10 CHECK (segment_min BETWEEN 0 AND 240),
    max_gb       REAL    NOT NULL DEFAULT 20 CHECK (max_gb > 0),
    keep_days    INTEGER NOT NULL DEFAULT 30 CHECK (keep_days >= 0),
    updated_at   TEXT    NOT NULL
);
INSERT OR IGNORE INTO recording_settings (id, updated_at) VALUES (1, '1970-01-01T00:00:00.000000000Z');

CREATE TABLE IF NOT EXISTS recordings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  INTEGER REFERENCES sessions (id) ON DELETE SET NULL,
    path        TEXT    NOT NULL UNIQUE,   -- relativo a SPLITSTREAM_RECORDINGS_DIR
    segment     INTEGER NOT NULL,
    started_at  TEXT    NOT NULL,
    ended_at    TEXT,
    bytes       INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_recordings_session ON recordings (session_id, segment);
```

`path` es **relativo** al directorio de grabaciones: mover la carpeta entera (o cambiar la
variable) no rompe el listado. `PruneSessions` (v0.8) sigue sin borrar sesiones con
grabaciones: la condición gana `AND id NOT IN (SELECT session_id FROM recordings WHERE
session_id IS NOT NULL)`.

## 6. Cableado y API

- `sinks.(*Factory).BuildRecorder(ctx, sessionID int64) (*relay.Sink, error)`: lee
  `recording_settings`; si `enabled = 0` devuelve `nil, nil`; calcula `UsedBytes` (suma de
  `recordings.bytes`), comprueba la cuota (podando primero si hace falta) y construye
  `relay.NewSink(SinkConfig{ID: relay.RecorderSinkID, Name: "grabación", NewPub: …})`.
  `OnSegment` inserta/actualiza la fila en `recordings`. Los eventos del sink se
  traducen: `DestinationID` nil, `kind` `destination_*` → `recording_*`, mensaje con el
  prefijo «grabación: ». Un `destination_id = -1` violaría la clave ajena.
- `main.go`: el `SinkProvider` recibe `sessionID`, construye los destinos y, si procede,
  añade el recorder.
- **Estado** (`statusDTO.Recording`, REST y WS por igual):
  `{enabled, active, state, degraded, bytes, segments, free_bytes, used_bytes, max_bytes, dir}`.
  `active` y `state` salen de `Snapshot()[RecorderSinkID]`; `segments`, `used_bytes` del
  store; `free_bytes` de `FreeSpace` (una llamada por push, es un `statfs`).
- **Endpoints** (todos tras `protegida`):

```
GET    /api/recording/settings           → {enabled, segment_min, max_gb, keep_days, dir, used_bytes, free_bytes}
PATCH  /api/recording/settings           → mismos campos (punteros); con sesión viva, encender arranca el recorder y apagar lo para (RemoveSink(-1))
GET    /api/recordings?session_id=&limit=&before=  → [{id, session_id, segment, path, started_at, ended_at, bytes, duration_ms}]
GET    /api/recordings/{id}/download     → attachment (http.ServeContent); 409 si el segmento está en curso
DELETE /api/recordings/{id}              → 204; 409 si está en curso; borra archivo y fila
```

- **Panel:** en Ajustes, bloque «Grabación» (interruptor, minutos por segmento, tope en GB,
  días, ocupación y espacio libre, enlace «Ver grabaciones»); en el panel, chip «Grabando ·
  1,2 GB · disco 63 %» junto a la tarjeta de señal (ámbar si `degraded`); página
  `/grabaciones` con lista, descarga y borrado con confirmación.
- **Métricas:** `splitstream_recording_active`, `splitstream_recording_bytes_total`,
  `splitstream_recording_dropped_frames_total`, `splitstream_recording_free_bytes`.

## 7. Pruebas

- **record (unitarias, con `-race`):** cabecera y tags FLV correctos (parser mínimo en el
  test); un patrón de 3 GOPs produce un FLV que `ffprobe` reconoce con h264 y aac (skip
  sin ffprobe); segmentación: con `SegmentMinutes=1` y timestamps de 150 s salen 3
  archivos, cada uno empieza por meta + 2 seq headers + keyframe con `ts=0`;
  `OnSegment` recibe índice, bytes y duración; el audio anterior a la base del segmento
  se descarta; un `Close` doble no rompe nada; `Connect` falla con `ErrDiskFull` cuando la
  cuota no da; `FreeSpace` devuelve valores positivos para `t.TempDir()`.
- **relay:** un `Publisher` que bloquea 5 s en `Write` no frena a un sink hermano del
  mismo hub (ya existe `TestHubSlowSinkDoesNotBlockOthers`; se añade la variante con un
  writer real sobre un `io.Writer` bloqueante).
- **store:** CRUD de `recordings` y `recording_settings`; `PruneRecordings` por días y por
  gigas con «la de gigas manda»; `PruneSessions` no borra sesiones con grabaciones;
  migración 0006 idempotente al rebobinar `user_version`.
- **sinks:** `BuildRecorder` devuelve `nil` con la grabación apagada; con cuota agotada
  no construye y deja evento; los eventos del recorder no llevan `destination_id`.
- **httpapi:** ajustes GET/PATCH con validación (400) y aplicación en caliente (AddSink/
  RemoveSink con id -1 en el fake); listado paginado; descarga con `Content-Disposition`;
  409 en descarga/borrado de un segmento en curso; `statusDTO.Recording` idéntico en REST
  y WS.
- **Integración** (`test/integration/recording_test.go`, tag `integration`, requiere
  ffmpeg y ffprobe pero **no** Docker): ffmpeg publica 12 s contra el motor con la
  grabación activada y `SegmentMinutes=0`; al terminar hay un `.flv` que `ffprobe`
  reconoce con vídeo y audio y ~12 s de duración; la fila de `recordings` tiene `bytes` y
  `duration_ms` coherentes.
- **Puerta de salida** (plan maestro §1): una emisión de 1 h en segmentos de 10 min;
  `kill -9` a mitad; todos los segmentos previos reproducibles; **cero descartes en los
  destinos** mientras se graba en un disco artificialmente lento.

## 8. Fuera de esta entrega

fMP4 (v0.9.1); subida a almacenamiento remoto (roadmap: al terminar la sesión, después);
grabar por destino (la grabación es por sesión: una entrada, un archivo); remuxear desde
el panel (el manual explica `ffmpeg -i x.flv -c copy x.mp4`).
