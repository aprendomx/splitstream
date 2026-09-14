#!/usr/bin/env bash
# Prueba que actualizar no le rompe la base a quien ya tiene Splitstream instalado.
#
# Los tests de Go migran bases que ellos mismos construyen; eso no es lo mismo que la base
# que lleva meses en el servidor de alguien, creada por un binario que ya no existe en este
# árbol. Aquí se hace de verdad: se descarga la release anterior, se arranca para que cree
# su base con el esquema de ENTONCES, y encima se pone el binario de hoy.
#
# Uso: deploy/migrate-test.sh <versión-anterior> <binario-nuevo>
#   deploy/migrate-test.sh v0.13.0 ./splitstream
#
# Códigos: 0 si el binario nuevo abrió la base de la versión anterior y respondió a
# /healthz; 1 si no; 2 si faltan argumentos o herramientas.
#
# Variables:
#   SPLITSTREAM_MIGRATE_TEST_OFFLINE=1  el «anterior» es una copia del nuevo y no se
#                                       descarga nada. Prueba el script, no la migración.
#
# No comparte helpers con nightly-smoke.sh a propósito: su prueba (nightly_smoke_test.sh)
# audita el TEXTO de ese script buscando secretos en la línea de órdenes, y sacar código a
# un lib.sh compartido sería justo el sitio donde esa auditoría dejaría de mirar.
set -euo pipefail

REPO=aprendomx/splitstream

uso() {
  cat >&2 <<'FIN'
Uso: deploy/migrate-test.sh <versión-anterior> <binario-nuevo>

  <versión-anterior>  etiqueta de una release publicada, p. ej. v0.13.0
  <binario-nuevo>     el binario a probar, p. ej. ./splitstream (make build-go)

Con SPLITSTREAM_MIGRATE_TEST_OFFLINE=1 no se descarga nada: el «anterior» es una
copia del nuevo. Sirve para probar este script, no para probar una migración.
FIN
}

error() { printf 'migrate-test: %s\n' "$*" >&2; }

if [ "$#" -ne 2 ]; then
  uso
  exit 2
fi

VER=$1
NUEVO=$2
OFFLINE=${SPLITSTREAM_MIGRATE_TEST_OFFLINE:-}

# La versión se pega en una URL de descarga y en un nombre de archivo. Se valida por lo
# mismo que en install.sh: una etiqueta con una barra de más apuntaría a otro sitio.
case "$VER" in
  v[0-9]*) ;;
  *)
    error "versión inválida: $VER (se espera algo como v0.13.0)"
    uso
    exit 2
    ;;
esac

if [ ! -x "$NUEVO" ]; then
  error "$NUEVO no es un binario ejecutable"
  exit 2
fi

# curl hace falta siempre (es quien pregunta por /healthz); od, para leer el
# PRAGMA user_version del encabezado de la base; tar, solo si hay algo que desempaquetar.
herramientas=(curl od)
[ -n "$OFFLINE" ] || herramientas+=(tar)
for h in "${herramientas[@]}"; do
  command -v "$h" >/dev/null 2>&1 || {
    error "hace falta $h"
    exit 2
  }
done

SO=$(uname -s)
ARQ=$(uname -m)
case "$ARQ" in
  x86_64 | amd64) ARQ=amd64 ;;
  aarch64 | arm64) ARQ=arm64 ;;
esac
case "$SO-$ARQ" in
  Darwin-arm64) ASSET=macos-apple-silicon ;;
  Darwin-amd64) ASSET=macos-intel ;;
  Linux-amd64) ASSET=linux-x86_64 ;;
  Linux-arm64) ASSET=linux-arm64 ;;
  *)
    error "sistema no soportado: $SO/$ARQ (Windows se publica en .zip y queda fuera)"
    exit 2
    ;;
esac

TMP=$(mktemp -d)
DATOS="$TMP/datos"
DB="$DATOS/splitstream.db"
LOG_ANT="$TMP/anterior.log"
LOG_NUEVO="$TMP/nuevo.log"
PID=""
mkdir -p "$DATOS"

limpiar() {
  if [ -n "$PID" ]; then
    kill -KILL "$PID" 2>/dev/null || true
  fi
  rm -rf "$TMP"
}
trap limpiar EXIT

fallo() { # mensaje
  error "FALLO — $1"
  for archivo in "$LOG_ANT" "$LOG_NUEVO"; do
    [ -s "$archivo" ] || continue
    printf -- '--- %s ---\n' "$(basename "$archivo")" >&2
    cat "$archivo" >&2
  done
  exit 1
}

# Un puerto libre de verdad: se comprueba que nadie escuche, en vez de confiar en que un
# número fijo esté libre en un runner compartido.
puerto_libre() {
  local p restantes=50
  while [ "$restantes" -gt 0 ]; do
    p=$(((RANDOM % 20000) + 20000))
    if ! timeout 1 bash -c "cat < /dev/null > /dev/tcp/127.0.0.1/$p" 2>/dev/null; then
      echo "$p"
      return 0
    fi
    restantes=$((restantes - 1))
  done
  error "no se encontró un puerto libre"
  return 1
}

# El PRAGMA user_version vive en el encabezado de SQLite, cuatro bytes big-endian en el
# desplazamiento 60. Se lee así y no con `sqlite3 'PRAGMA user_version'` porque la CLI de
# SQLite es una dependencia que este proyecto no tiene y no va a tener: el binario lleva su
# propio driver en Go.
#
# Solo vale con la base CERRADA: en modo WAL el user_version recién escrito vive en el
# -wal hasta que se hace checkpoint, y al cerrar la última conexión SQLite lo hace y borra
# el -wal. De ahí que abajo se exija que no quede -wal antes de leer.
version_esquema() { # ruta-de-la-base → user_version
  local b0 b1 b2 b3
  read -r b0 b1 b2 b3 < <(od -An -tu1 -j60 -N4 "$1")
  echo $((b0 * 16777216 + b1 * 65536 + b2 * 256 + b3))
}

# La clave maestra va SIEMPRE por el entorno y nunca por argumentos: los argumentos de
# cualquier proceso los lee todo el mundo con un `ps`. Tampoco se imprime en ningún sitio.
arrancar() { # binario log — deja el PID en la global PID
  SPLITSTREAM_DB_PATH="$DB" \
    SPLITSTREAM_MASTER_KEY="$CLAVE" \
    SPLITSTREAM_HTTP_ADDR="127.0.0.1:$PUERTO_HTTP" \
    SPLITSTREAM_RTMP_ADDR="127.0.0.1:$PUERTO_RTMP" \
    SPLITSTREAM_UPDATE_CHECK=false \
    "$1" >"$2" 2>&1 &
  # La global y no un `echo $!` recogido con $(...): en una substitución de órdenes el
  # proceso nace dentro de una subshell que muere enseguida, y entonces `wait` ya no lo
  # reconoce como hijo y la parada ordenada deja de poder esperarse.
  PID=$!
}

esperar_healthz() { # pid
  local restantes=30
  while [ "$restantes" -gt 0 ]; do
    if curl -sf -o /dev/null -m 2 "http://127.0.0.1:$PUERTO_HTTP/healthz"; then
      return 0
    fi
    # Si ya murió, no tiene sentido esperar los 30 s: el log dirá por qué.
    if ! kill -0 "$1" 2>/dev/null; then
      return 1
    fi
    sleep 1
    restantes=$((restantes - 1))
  done
  return 1
}

# SIGTERM y no SIGKILL: la parada ordenada es la que cierra la base y hace checkpoint del
# WAL. Si el binario no cerrara bien, esta prueba tiene que enterarse.
parar() { # pid
  local restantes=10
  kill -TERM "$1" 2>/dev/null || return 0
  while [ "$restantes" -gt 0 ]; do
    if ! kill -0 "$1" 2>/dev/null; then
      wait "$1" 2>/dev/null || true
      return 0
    fi
    sleep 1
    restantes=$((restantes - 1))
  done
  kill -KILL "$1" 2>/dev/null || true
  wait "$1" 2>/dev/null || true
  return 1
}

# ── El binario anterior ──────────────────────────────────────────────────────
if [ -n "$OFFLINE" ]; then
  ANTERIOR="$TMP/anterior/splitstream"
  mkdir -p "$TMP/anterior"
  cp "$NUEVO" "$ANTERIOR"
  echo "migración: OFFLINE — el «anterior» es una copia del binario nuevo"
else
  ARCHIVO="splitstream-$VER-$ASSET.tar.gz"
  echo "migración: descargando $ARCHIVO"
  # gh primero: en la nocturna ya trae el token del runner y no se come el límite de
  # peticiones anónimas. Si no está o no está autenticado, curl a la URL pública.
  if ! (command -v gh >/dev/null 2>&1 &&
    gh release download "$VER" --repo "$REPO" --pattern "$ARCHIVO" --dir "$TMP" >/dev/null 2>&1); then
    curl -fsSL --retry 3 -o "$TMP/$ARCHIVO" \
      "https://github.com/$REPO/releases/download/$VER/$ARCHIVO" ||
      fallo "no se pudo descargar $ARCHIVO de la release $VER"
  fi

  # El nombre del binario dentro del tar se MIRA, no se supone: hoy viene en una carpeta
  # splitstream-<ver>-<asset>/, y si eso cambiara en release.yml el script se enteraría
  # aquí en vez de fallar con un «no such file» tres pasos más abajo.
  DENTRO=$(tar -tzf "$TMP/$ARCHIVO" | grep -E '(^|/)splitstream$' | head -n 1 || true)
  [ -n "$DENTRO" ] || fallo "$ARCHIVO no contiene ningún archivo llamado splitstream"
  mkdir -p "$TMP/anterior"
  tar -xzf "$TMP/$ARCHIVO" -C "$TMP/anterior" || fallo "no se pudo extraer $ARCHIVO"
  ANTERIOR="$TMP/anterior/$DENTRO"
  chmod +x "$ANTERIOR"
fi

[ -x "$ANTERIOR" ] || fallo "el binario de $VER no quedó ejecutable"

PUERTO_HTTP=$(puerto_libre)
PUERTO_RTMP=$(puerto_libre)

# De usar y tirar: muere con el directorio temporal, porque la base también. Se genera con
# el binario NUEVO para no depender de que el antiguo tuviera ya el flag.
CLAVE=$("$NUEVO" -genkey) || fallo "no se pudo generar la clave maestra"

# ── 1. La versión anterior crea su base ──────────────────────────────────────
echo "migración: arrancando $VER para que cree la base"
arrancar "$ANTERIOR" "$LOG_ANT"
esperar_healthz "$PID" || fallo "$VER no respondió en /healthz en 30 s"
parar "$PID" || fallo "$VER no terminó con SIGTERM en 10 s"
PID=""

[ -f "$DB" ] || fallo "$VER no dejó ninguna base en $DB"
if [ -s "$DB-wal" ]; then
  fallo "$VER dejó un -wal sin checkpoint: no cerró la base limpiamente"
fi
V_ANTES=$(version_esquema "$DB")
[ "$V_ANTES" -gt 0 ] || fallo "la base de $VER quedó en user_version=0: no llegó a migrar"
echo "migración: la base de $VER está en el esquema $V_ANTES"

# ── 2. El binario nuevo, sobre esa misma base ────────────────────────────────
echo "migración: arrancando el binario nuevo sobre la base de $VER"
arrancar "$NUEVO" "$LOG_NUEVO"
esperar_healthz "$PID" || fallo "el binario nuevo no respondió en /healthz en 30 s"
parar "$PID" || fallo "el binario nuevo no terminó con SIGTERM en 10 s"
PID=""

if [ -s "$DB-wal" ]; then
  fallo "el binario nuevo dejó un -wal sin checkpoint: no cerró la base limpiamente"
fi
V_DESPUES=$(version_esquema "$DB")
[ "$V_DESPUES" -ge "$V_ANTES" ] ||
  fallo "el esquema BAJÓ de $V_ANTES a $V_DESPUES: eso no puede pasar"
APLICADAS=$((V_DESPUES - V_ANTES))

# ── 3. Veredicto ─────────────────────────────────────────────────────────────
# Dos evidencias independientes, y tienen que contar lo mismo: el `user_version` de la base
# —que dice en qué esquema quedó— y el log del binario nuevo, que desde la v1.0 escribe una
# línea «migración aplicada» por cada migración que aplica (internal/store/db.go).
#
# El `user_version` se sigue leyendo y no se sustituye por el log: es la única evidencia que
# sobrevive a que alguien cambie el texto de esa línea, y es la que mide el resultado de
# verdad. El log añade lo que el `user_version` no puede decir: que migró en ESTE arranque y
# cuántas veces, no que la base ya viniera así.
DIJO_MIGRAR=no
if grep -qi 'migraci' "$LOG_NUEVO"; then DIJO_MIGRAR=si; fi
APLICADAS_LOG=$(grep -c 'migración aplicada' "$LOG_NUEVO" || true)

if [ "$APLICADAS" -eq 0 ] && [ "$DIJO_MIGRAR" = si ]; then
  fallo "el log del binario nuevo habla de migraciones pero el esquema sigue en $V_ANTES"
fi
if [ "$APLICADAS" -gt 0 ] && [ "$APLICADAS_LOG" -ne "$APLICADAS" ]; then
  fallo "el esquema subió de $V_ANTES a $V_DESPUES pero el log trae $APLICADAS_LOG líneas «migración aplicada» y no $APLICADAS"
fi

# `err=` aparece en el apagado ordenado («la ingesta dejó de atender»), así que no vale
# buscar «error» a secas: se buscan las formas en que este binario reporta un fallo de
# verdad —el nivel ERROR del slog y el `error:` que main imprime antes de salir— y el
# pánico, que no tiene nivel porque lo escribe el runtime.
if grep -qE '(^|[[:space:]])level=ERROR|^error:|panic:|goroutine [0-9]+ \[running\]' "$LOG_NUEVO"; then
  fallo "el binario nuevo arrancó con errores en el log"
fi

VERSION_NUEVA=$("$NUEVO" -version | awk '{ print $2 }')
if [ "$APLICADAS" -eq 0 ]; then
  echo "migración OK: $VER → $VERSION_NUEVA (sin migraciones que aplicar; esquema $V_DESPUES)"
elif [ "$APLICADAS" -eq 1 ]; then
  echo "migración OK: $VER → $VERSION_NUEVA (1 migración: esquema $V_ANTES → $V_DESPUES)"
else
  echo "migración OK: $VER → $VERSION_NUEVA ($APLICADAS migraciones: esquema $V_ANTES → $V_DESPUES)"
fi
