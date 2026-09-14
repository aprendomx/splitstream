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
trap 'rm -f "$ERR"' EXIT

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

echo "nightly_smoke_test: ok"
