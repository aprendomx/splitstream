#!/bin/sh
# Prueba deploy/migrate-test.sh sin tocar la red: su sintaxis, que se niega con código 2
# cuando le falta algo, y —con SPLITSTREAM_MIGRATE_TEST_OFFLINE=1— el flujo entero contra
# un «binario anterior» que es una copia del nuevo. Lo que no se puede probar aquí es la
# migración de verdad, que necesita una release publicada: eso lo corre la nocturna.
#
# Uso: sh deploy/migrate_test_test.sh [binario]
#
# Sin argumento usa ./splitstream si existe, y si no lo compila con `go build`.
set -eu
cd "$(dirname "$0")/.."

MT=deploy/migrate-test.sh
ART=$(mktemp -d)
ERR="$ART/err"
SAL="$ART/salida"
trap 'rm -rf "$ART"' EXIT

echo "== sintaxis"
bash -n "$MT"

echo "== shellcheck"
if command -v shellcheck >/dev/null 2>&1; then
  shellcheck "$MT" "$0"
else
  echo "   (shellcheck no está instalado; en la CI sí corre)"
fi

# La clave maestra que el script genera es un secreto de usar y tirar, pero de usar y tirar
# no quiere decir publicable: los argumentos de cualquier proceso se leen desde fuera con un
# `ps`, y el log del runner es público. Se comprueba leyendo el script porque el momento en
# que esto se rompe es cuando alguien añade una llamada nueva, y entonces no hay ninguna
# corrida delante que lo enseñe.
echo "== la clave maestra no viaja por argumentos ni se imprime"
CODIGO_SOLO="$ART/codigo.sh"
# Los comentarios se vacían conservando la numeración: el script EXPLICA en los suyos justo
# los patrones que esta prueba persigue, y sin esto se delataría a sí mismo.
sed 's/^[[:space:]]*#.*//' "$MT" >"$CODIGO_SOLO"
if grep -nE '(echo|printf|cat).*\$\{?CLAVE|-(key|clave)[= ]+"?\$\{?CLAVE' "$CODIGO_SOLO"; then
  echo "FALLO: la clave maestra se imprime o se pasa por argumentos (líneas de arriba)" >&2
  exit 1
fi

# El envoltorio corre el script en un shell aparte para quedarse con su código de salida
# sin que el `set -e` de este mate la prueba.
salida_de() { # argumentos…
  "$MT" "$@" >"$SAL" 2>"$ERR"
}

espera_2() { # descripción argumentos…
  descripcion=$1
  shift
  echo "== $descripcion"
  # El `rc=$?` va DENTRO del else: tras un `if cmd; then …; fi` sin else, $? es el del
  # propio if —cero— y no el del comando que falló.
  if salida_de "$@"; then
    echo "FALLO: migrate-test.sh aceptó lo que no debía ($descripcion)" >&2
    cat "$SAL" >&2
    exit 1
  else
    rc=$?
    [ "$rc" -eq 2 ] || {
      echo "FALLO: salió $rc y se esperaba 2 ($descripcion)" >&2
      cat "$ERR" >&2
      exit 1
    }
  fi
}

espera_2 "sin argumentos se niega con código 2"
espera_2 "con un solo argumento se niega con código 2" v0.13.0
espera_2 "con un binario inexistente se niega con código 2" v0.13.0 ./no-existe-este-binario
espera_2 "con una versión inválida se niega con código 2" no-es-una-version ./splitstream

# ── El flujo entero, sin red ─────────────────────────────────────────────────
BIN=${1:-}
if [ -z "$BIN" ]; then
  if [ -x ./splitstream ]; then
    BIN=./splitstream
  elif command -v go >/dev/null 2>&1; then
    echo "== compilando el binario (no había ./splitstream)"
    BIN="$ART/splitstream"
    CGO_ENABLED=0 go build -o "$BIN" ./cmd/splitstream
  fi
fi

if [ -z "$BIN" ] || [ ! -x "$BIN" ]; then
  echo "   (sin binario y sin go: el flujo offline se salta; corre 'make build-go' antes)"
  echo "migrate_test_test: ok (parcial)"
  exit 0
fi

echo "== el flujo entero en modo offline"
if SPLITSTREAM_MIGRATE_TEST_OFFLINE=1 "$MT" v0.0.0-prueba "$BIN" >"$SAL" 2>"$ERR"; then
  grep -q 'migración OK' "$SAL" || {
    echo "FALLO: terminó con 0 pero no dijo «migración OK»" >&2
    cat "$SAL" "$ERR" >&2
    exit 1
  }
  sed 's/^/   /' "$SAL"
else
  echo "FALLO: el flujo offline no terminó bien" >&2
  cat "$SAL" "$ERR" >&2
  exit 1
fi

echo "migrate_test_test: ok"
