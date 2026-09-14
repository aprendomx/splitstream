#!/usr/bin/env bash
# Humo contra una plataforma REAL: arranca un splitstream limpio, le configura un destino
# con la clave de una cuenta de pruebas, publica cinco minutos de vídeo sintético y exige
# que el destino siga en «live» todo ese rato.
#
# Es lo único que prueba lo que ningún mediamtx puede probar: que YouTube, Twitch o Kick
# siguen aceptando lo que este relay les manda. Las plataformas cambian su ingesta sin
# avisar —handshakes, chunks, códecs—, y eso solo se nota transmitiéndoles de verdad.
#
# La clave va por la variable de entorno STREAM_KEY y NUNCA por argumento: los argumentos
# de cualquier proceso se ven en `ps` para todo el sistema. Por eso también `set +x` desde
# el principio: con las trazas puestas, la clave acabaría en el log del runner.
#
# Uso: STREAM_KEY=<clave> deploy/nightly-smoke.sh <twitch|youtube|kick> <binario>
set -euo pipefail
set +x

MINUTOS=5
INTERVALO=15
# Margen inicial: conectar con una plataforma real, resolver DNS y hacer el handshake no
# es instantáneo. Antes de que venza no se exige «live», solo que no haya muerto nada.
GRACIA=60

uso() {
  cat >&2 <<'FIN'
Uso: STREAM_KEY=<clave> deploy/nightly-smoke.sh <twitch|youtube|kick> <binario>

La clave de la plataforma se pasa SIEMPRE por la variable de entorno STREAM_KEY:
por argumento quedaría visible en `ps` para cualquier usuario de la máquina.
FIN
}

if [ "$#" -ne 2 ]; then
  uso
  exit 2
fi

PLATAFORMA=$1
BIN=$2

if [ -z "${STREAM_KEY:-}" ]; then
  echo "error: falta STREAM_KEY (la clave de emisión de la plataforma)" >&2
  uso
  exit 2
fi

# Las URL son las del catálogo del panel (web/src/plataformas.js): las mismas que el
# usuario recibe precargadas al elegir la plataforma, no las de la documentación.
case "$PLATAFORMA" in
  youtube) RTMP_URL='rtmp://a.rtmp.youtube.com/live2' ;;
  twitch)  RTMP_URL='rtmp://live.twitch.tv/app' ;;
  kick)    RTMP_URL='rtmps://fa723fc1b171.global-contribute.live-video.net/app' ;;
  *)
    echo "error: plataforma desconocida: $PLATAFORMA" >&2
    uso
    exit 2
    ;;
esac

if [ ! -x "$BIN" ]; then
  echo "error: $BIN no es un binario ejecutable" >&2
  exit 2
fi

for herramienta in curl jq ffmpeg; do
  command -v "$herramienta" >/dev/null 2>&1 || {
    echo "error: hace falta $herramienta" >&2
    exit 2
  }
done

TMP=$(mktemp -d)
COOKIES="$TMP/cookies"
LOG="$TMP/splitstream.log"
SS_PID=""
FFMPEG_PID=""

limpiar() {
  [ -n "$FFMPEG_PID" ] && kill -TERM "$FFMPEG_PID" 2>/dev/null || true
  # SIGTERM, no SIGKILL: es la parada ordenada que el binario promete, y si no cerrara
  # bien su base lo descubrimos aquí y no en el servidor de alguien.
  [ -n "$SS_PID" ] && kill -TERM "$SS_PID" 2>/dev/null || true
  wait "$SS_PID" 2>/dev/null || true
  rm -rf "$TMP"
}
trap limpiar EXIT

# El log del binario puede acabar llevando la URL de un destino en el texto de un error, y
# esa URL sale de la plataforma. Nada que venga de ahí se imprime sin pasar por aquí: se
# tiran ENTERAS las líneas que contengan una clave, que es más simple y más seguro que
# intentar sustituirla con sed y acertar con el escapado.
#
# Son DOS claves, no una. La de la plataforma (STREAM_KEY) es la obvia. La de la ingesta
# (INGEST_KEY) también: es la única que viaja en un argumento —la URL de salida de ffmpeg,
# que ffmpeg solo acepta así—, con lo que sale en el log de ffmpeg en cuanto algo va mal, y
# de ahí al mensaje de `fallo`. Es local y de usar y tirar, pero el log de un runner es
# público y no hay motivo para publicarla.
#
# awk y no `grep -vF "$STREAM_KEY"`: el patrón de grep sería un argumento, y los argumentos
# de cualquier proceso se leen desde fuera (`ps`, /proc/PID/cmdline). awk lee las claves del
# entorno, que solo ve el propio proceso. Por eso INGEST_KEY se exporta al asignarla.
#
# La comprobación de vacío no sobra: `index($0, "")` vale 1, así que una variable sin poner
# —INGEST_KEY antes de rotarla— tiraría TODAS las líneas y el log del fallo saldría en
# blanco justo cuando hace falta.
sin_clave() {
  awk '
    ENVIRON["STREAM_KEY"] != "" && index($0, ENVIRON["STREAM_KEY"]) > 0 { next }
    ENVIRON["INGEST_KEY"] != "" && index($0, ENVIRON["INGEST_KEY"]) > 0 { next }
    { print }
  ' || true
}

# Un puerto libre de verdad: se comprueba que nadie esté escuchando, en vez de confiar en
# que un número fijo esté libre en un runner compartido.
puerto_libre() {
  local p i
  for i in $(seq 1 50); do
    p=$(( (RANDOM % 20000) + 20000 ))
    if ! timeout 1 bash -c "cat < /dev/null > /dev/tcp/127.0.0.1/$p" 2>/dev/null; then
      echo "$p"
      return 0
    fi
  done
  echo "error: no se encontró un puerto libre" >&2
  return 1
}

fallo() { # mensaje
  echo "humo $PLATAFORMA: FALLO — $1"
  echo "--- últimas líneas del log del binario ---" >&2
  tail -n 40 "$LOG" 2>/dev/null | sin_clave >&2
  exit 1
}

PUERTO_HTTP=$(puerto_libre)
PUERTO_RTMP=$(puerto_libre)
BASE="http://127.0.0.1:$PUERTO_HTTP"

# La clave maestra se genera para esta corrida y muere con el directorio temporal: la base
# se tira al terminar, así que nada que se cifre aquí hace falta después.
MASTER_KEY=$("$BIN" -genkey)

SPLITSTREAM_DB_PATH="$TMP/splitstream.db" \
SPLITSTREAM_HTTP_ADDR="127.0.0.1:$PUERTO_HTTP" \
SPLITSTREAM_RTMP_ADDR="127.0.0.1:$PUERTO_RTMP" \
SPLITSTREAM_MASTER_KEY="$MASTER_KEY" \
SPLITSTREAM_UPDATE_CHECK=false \
  "$BIN" >"$LOG" 2>&1 &
SS_PID=$!

for i in $(seq 1 30); do
  if curl -sf -o /dev/null -m 2 "$BASE/healthz"; then
    break
  fi
  if [ "$i" -eq 30 ]; then
    fallo "el binario no respondió en /healthz en 30 s"
  fi
  sleep 1
done

# El cuerpo se pasa SIEMPRE en un archivo, nunca como argumento: la clave de la plataforma
# va dentro del JSON del destino, y `curl --data '{"key":"…"}'` la enseñaría en `ps`.
api() { # método ruta [archivo-json]
  local metodo=$1 ruta=$2
  if [ "$#" -ge 3 ]; then
    curl -sS --fail-with-body -m 30 -b "$COOKIES" -c "$COOKIES" \
      -H 'Content-Type: application/json' -X "$metodo" --data-binary "@$3" "$BASE$ruta"
  else
    curl -sS --fail-with-body -m 30 -b "$COOKIES" -c "$COOKIES" -X "$metodo" "$BASE$ruta"
  fi
}

# El código del primer arranque se lee del log. Desde 127.0.0.1 el servicio no lo exige,
# pero se manda igual: así esta prueba también verifica que el binario lo imprime y con el
# formato que el panel espera.
CODIGO=$(grep -oE '[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}' "$LOG" | head -n 1 || true)
# Una contraseña de usar y tirar. /dev/urandom y no `openssl`: una dependencia menos.
PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -d '/+=' | head -c 24)

# Los JSON con algo secreto dentro —la contraseña, el código del primer arranque, la clave
# de la plataforma— se construyen leyendo el ENTORNO (`env.X`), nunca con `--arg`: un
# `--arg k "$STREAM_KEY"` metería la clave en los argumentos de jq, que cualquiera lee con
# `ps`. Las variables se pasan como prefijo de la orden, que es entorno y no argumentos.
# `umask 077` porque el archivo lleva el secreto hasta que curl lo manda.
(umask 077 && PASSWORD="$PASSWORD" CODIGO="$CODIGO" \
  jq -n '{password: env.PASSWORD, codigo: env.CODIGO}' >"$TMP/setup.json")
api POST /api/setup "$TMP/setup.json" >/dev/null \
  || fallo "no se pudo completar la configuración inicial"

(umask 077 && PASSWORD="$PASSWORD" jq -n '{password: env.PASSWORD}' >"$TMP/login.json")
api POST /api/auth/login "$TMP/login.json" >/dev/null \
  || fallo "no se pudo iniciar sesión"

# STREAM_KEY ya está en el entorno (llega de fuera), así que aquí no hay ni que pasarla.
(umask 077 && jq -n --arg n "humo-$PLATAFORMA" --arg pl "$PLATAFORMA" --arg u "$RTMP_URL" \
  '{name: $n, platform: $pl, rtmp_url: $u, key: env.STREAM_KEY, enabled: true}' >"$TMP/destino.json")
DEST_ID=$(api POST /api/destinations "$TMP/destino.json" | jq -r '.id') \
  || fallo "no se pudo crear el destino"
[ -n "$DEST_ID" ] && [ "$DEST_ID" != "null" ] || fallo "el destino creado no trajo id"

# La clave de ingesta solo sale en claro al rotarla (spec §8): GET /api/ingest la devuelve
# enmascarada, así que para publicar hay que pedir una nueva.
INGEST_URL=$(api GET /api/ingest | jq -r '.url') || fallo "no se pudo leer la ingesta"
echo '{}' >"$TMP/rotar.json"
# `export`: sin él, `sin_clave` —que lee las claves de ENVIRON, no de sus argumentos— no
# vería esta y la dejaría pasar al log.
export INGEST_KEY
INGEST_KEY=$(api POST /api/ingest/rotate-key "$TMP/rotar.json" | jq -r '.key') \
  || fallo "no se pudo rotar la clave de ingesta"
[ -n "$INGEST_KEY" ] && [ "$INGEST_KEY" != "null" ] || fallo "la rotación no devolvió clave"

# La única excepción a "ningún secreto en la línea de órdenes", y es inevitable: ffmpeg solo
# acepta la URL de salida como argumento. Se puede vivir con ella porque es la clave de la
# ingesta LOCAL —no la de la plataforma—, escucha en 127.0.0.1, se acaba de rotar y muere
# con este directorio temporal.
ffmpeg -hide_banner -loglevel error -nostdin \
  -re -f lavfi -i "testsrc=size=1280x720:rate=30" -f lavfi -i sine \
  -c:v libx264 -preset veryfast -b:v 2500k -g 60 -c:a aac \
  -f flv "$INGEST_URL/$INGEST_KEY" >"$TMP/ffmpeg.log" 2>&1 &
FFMPEG_PID=$!

echo "humo $PLATAFORMA: publicando ${MINUTOS} min; se comprueba cada ${INTERVALO}s"

VUELTAS=$(( MINUTOS * 60 / INTERVALO ))
for vuelta in $(seq 1 "$VUELTAS"); do
  sleep "$INTERVALO"
  transcurrido=$(( vuelta * INTERVALO ))

  kill -0 "$FFMPEG_PID" 2>/dev/null || fallo "ffmpeg murió a los ${transcurrido}s: $(tail -n 5 "$TMP/ffmpeg.log" | sin_clave | tr '\n' ' ')"
  kill -0 "$SS_PID" 2>/dev/null || fallo "el binario murió a los ${transcurrido}s"

  # Las métricas son null mientras no hay sesión viva o el destino está apagado, y de ahí
  # el `//`: sin él, jq devolvería «null» y el mensaje de fallo no diría nada.
  estado=$(api GET /api/status | jq -r --argjson id "$DEST_ID" \
    '.destinations[] | select(.id == $id) | .metrics.state // "sin métricas"') \
    || fallo "no se pudo leer el estado"

  # Un corte en una plataforma real no siempre saca al destino de «live» en el instante en
  # que se mira: el sink reconecta. Por eso el veredicto no es solo el estado de ahora,
  # también el registro de eventos, que no olvida.
  caidas=$(api GET '/api/events?limit=500' | jq '[.[] | select(.kind == "destination_disconnected")] | length') \
    || fallo "no se pudieron leer los eventos"
  [ "$caidas" = "0" ] || fallo "$caidas desconexión(es) del destino a los ${transcurrido}s"

  if [ "$estado" != "live" ]; then
    # Antes de que venza la gracia, «connecting» es normal; después, no.
    if [ "$transcurrido" -ge "$GRACIA" ]; then
      fallo "el destino está en «$estado» a los ${transcurrido}s, no en «live»"
    fi
    echo "  ${transcurrido}s: $estado (aún dentro del margen de ${GRACIA}s)"
    continue
  fi
  echo "  ${transcurrido}s: live"
done

echo "humo $PLATAFORMA: OK — ${MINUTOS} min en «live» sin desconexiones"
