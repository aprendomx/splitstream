#!/bin/sh
# Prueba deploy/nightly-smoke.sh sin tocar la red ni ninguna plataforma: lo que se puede
# comprobar del humo nocturno en cada push es su sintaxis y que se niega a arrancar cuando
# le falta la clave. Lo demás —transmitir de verdad cinco minutos— solo lo puede ejecutar
# la nocturna, con secretos que un PR de fuera no tiene.
#
# Uso: sh deploy/nightly_smoke_test.sh
set -eu
cd "$(dirname "$0")/.."

HUMO=deploy/nightly-smoke.sh

echo "== sintaxis"
bash -n "$HUMO"

echo "== shellcheck"
if command -v shellcheck >/dev/null 2>&1; then
  shellcheck "$HUMO" "$0"
else
  echo "   (shellcheck no está instalado; en la CI sí corre)"
fi

# Que las trazas estén apagadas no es cosmético: con `set -x`, la clave de la plataforma
# acabaría en el log público del runner en cuanto se construyera el JSON del destino.
echo "== las trazas están apagadas"
grep -q '^set +x$' "$HUMO" || {
  echo "FALLO: el humo no desactiva las trazas (set +x)" >&2
  exit 1
}

# Los argumentos de cualquier proceso los lee todo el mundo (`ps`, /proc/PID/cmdline), así
# que ningún secreto puede viajar por ahí: ni en `--arg` de jq, ni en `-d`/`-H "Cookie:"`/
# `-u` de curl. Esto se comprueba leyendo el script y no ejecutándolo: el momento en que se
# rompe es cuando alguien añade una llamada nueva, y entonces no hay ninguna corrida con
# claves de verdad delante que lo enseñe.
echo "== ningún secreto en la línea de órdenes"
SECRETOS='STREAM_KEY|PASSWORD|CODIGO|INGEST_KEY|MASTER_KEY'
CODIGO_SOLO=$(mktemp)
# Los comentarios se vacían conservando la numeración: el script EXPLICA en sus comentarios
# justo los patrones que esta prueba persigue, y sin esto se delataría a sí mismo.
sed 's/^[[:space:]]*#.*//' "$HUMO" >"$CODIGO_SOLO"
malo=0
# Un secreto como argumento de jq o de grep.
if grep -nE -- "(--arg|-[A-Za-z]*F[A-Za-z]*) +[A-Za-z_]* *\"\\\$\\{?($SECRETOS)" "$CODIGO_SOLO"; then
  malo=1
fi
# Un cuerpo, una cookie o unas credenciales inline en curl. El cuerpo va SIEMPRE en un
# archivo (`--data-binary @…`), que es lo único que no acaba en la línea de órdenes.
if grep -nE -- 'curl.*(-H +.Cookie|(^| )-u +|(^| )(-d|--data|--data-raw|--data-urlencode) |--data-binary +"[^@])' "$CODIGO_SOLO"; then
  malo=1
fi
rm -f "$CODIGO_SOLO"
[ "$malo" -eq 0 ] || {
  echo "FALLO: el humo pasa un secreto por argumentos (líneas de arriba)" >&2
  exit 1
}

# El envoltorio corre el script en un shell aparte para poder quedarse con su código de
# salida sin que `set -e` de este mate la prueba.
salida_de() { # STREAM_KEY-o-vacía argumentos…
  clave=$1
  shift
  if [ -z "$clave" ]; then
    env -u STREAM_KEY "$HUMO" "$@" >/dev/null 2>"$ERR"
  else
    STREAM_KEY="$clave" "$HUMO" "$@" >/dev/null 2>"$ERR"
  fi
}

ERR=$(mktemp)
FILTRO=$(mktemp)
MUTANTE=$(mktemp)
trap 'rm -f "$ERR" "$FILTRO" "$MUTANTE"' EXIT

echo "== sin STREAM_KEY se niega con código 2"
if salida_de "" twitch ./splitstream; then
  echo "FALLO: el humo arrancó sin clave" >&2
  exit 1
else
  rc=$?
  [ "$rc" -eq 2 ] || { echo "FALLO: salió $rc y se esperaba 2" >&2; cat "$ERR" >&2; exit 1; }
  grep -q STREAM_KEY "$ERR" || {
    echo "FALLO: el mensaje no dice que falta STREAM_KEY" >&2
    cat "$ERR" >&2
    exit 1
  }
fi

echo "== sin argumentos se niega con código 2"
if salida_de ""; then
  echo "FALLO: el humo arrancó sin argumentos" >&2
  exit 1
else
  rc=$?
  [ "$rc" -eq 2 ] || { echo "FALLO: salió $rc y se esperaba 2" >&2; exit 1; }
fi

echo "== una plataforma desconocida se niega con código 2"
if salida_de no-existe-esta-clave inventada ./splitstream; then
  echo "FALLO: el humo aceptó una plataforma inventada" >&2
  exit 1
else
  rc=$?
  [ "$rc" -eq 2 ] || { echo "FALLO: salió $rc y se esperaba 2" >&2; exit 1; }
fi

# `sin_clave` es el único filtro entre el log de un proceso y la salida pública del runner,
# y es fácil creer que solo hay una clave que tapar. Hay dos: la de la plataforma
# (STREAM_KEY) y la de la ingesta (INGEST_KEY), que viaja en la URL de salida de ffmpeg
# —ffmpeg solo la acepta como argumento— y por eso aparece en su log en cuanto algo falla,
# de donde el mensaje de `fallo` la sacaría con un `tail`.
#
# Esto se comprueba EJECUTANDO la función, no leyéndola: lo que importa es lo que deja
# pasar, no cómo está escrita.
echo "== sin_clave tapa las dos claves"

# Extrae sin_clave() del humo indicado y la corre sobre tres líneas —una limpia y una con
# cada clave—; imprime lo que sobrevive al filtro.
correr_filtro() { # archivo-del-humo
  sed -n '/^sin_clave() {/,/^}/p' "$1" >"$FILTRO"
  STREAM_KEY=CLAVE-DE-PLATAFORMA INGEST_KEY=CLAVE-DE-INGESTA \
    bash -c '. "$1"; printf "%s\n" \
      "linea limpia" \
      "destino rtmp://x/app/CLAVE-DE-PLATAFORMA" \
      "ffmpeg: rtmp://127.0.0.1/live/CLAVE-DE-INGESTA" | sin_clave' bash "$FILTRO"
}

SOBREVIVE=$(correr_filtro "$HUMO")
[ "$SOBREVIVE" = "linea limpia" ] || {
  echo "FALLO: sin_clave dejó pasar una clave (o se comió una línea limpia). Sobrevivió:" >&2
  printf '%s\n' "$SOBREVIVE" >&2
  exit 1
}

# El mutante. Un humo igual pero con sin_clave filtrando solo STREAM_KEY —que es como
# estaba— tiene que suspender la comprobación de arriba. Si la pasa, esa comprobación no
# está comprobando nada y el día que alguien quite la línea nadie se entera.
grep -v 'ENVIRON\["INGEST_KEY"\]' "$HUMO" >"$MUTANTE"
MUTA=$(correr_filtro "$MUTANTE")
if [ "$MUTA" = "linea limpia" ]; then
  echo "FALLO: la comprobación no caza un sin_clave que se olvida de INGEST_KEY" >&2
  exit 1
fi

# Y con las claves sin poner el filtro no puede comerse el log entero: `index($0, "")` vale
# 1 en awk, así que un sin_clave sin la comprobación de vacío se convierte en `>/dev/null`
# justo cuando `fallo` intenta enseñar por qué murió el binario.
sed -n '/^sin_clave() {/,/^}/p' "$HUMO" >"$FILTRO"
# shellcheck disable=SC2016  # el $1 es el posicional del `bash -c`, no una expansión de aquí
VACIO=$(env -u STREAM_KEY -u INGEST_KEY \
  bash -c '. "$1"; printf "%s\n" "linea limpia" | sin_clave' bash "$FILTRO")
[ "$VACIO" = "linea limpia" ] || {
  echo "FALLO: sin claves en el entorno, sin_clave se traga el log entero" >&2
  exit 1
}

# El filtro no sirve de nada si el log de ffmpeg se imprime por un lado.
echo "== el log de ffmpeg no se imprime sin filtrar"
if grep -n 'ffmpeg\.log' "$HUMO" | grep -E 'tail|cat ' | grep -qv 'sin_clave'; then
  echo "FALLO: hay un volcado del log de ffmpeg que no pasa por sin_clave" >&2
  grep -n 'ffmpeg\.log' "$HUMO" | grep -E 'tail|cat ' | grep -v 'sin_clave' >&2
  exit 1
fi

echo "nightly_smoke_test: ok"
