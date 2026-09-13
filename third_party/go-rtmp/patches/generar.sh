#!/usr/bin/env bash
# Genera patches/NNNN-nombre.diff a partir de uno o más archivos que difieren entre la
# copia (third_party/go-rtmp) y el upstream de la caché de módulos
# (github.com/yutopp/go-rtmp@v0.0.7). El diff resultante usa la misma normalización que
# el test TestGoRTMPCopyMatchesUpstreamPlusPatches en internal/rtmpio/upstream_test.go:
# cabeceras `diff --git a/<rel> b/<rel>`, sin línea `index …`, `--- a/<rel>` y
# `+++ b/<rel>`, para que no dependan de dónde vive la caché de módulos en cada máquina.
#
# Uso: patches/generar.sh NNNN-nombre archivo1 [archivo2 …]
# Los archivos son rutas relativas a third_party/go-rtmp (p.ej. conn.go).

set -euo pipefail

if [ "$#" -lt 2 ]; then
  echo "uso: $0 NNNN-nombre archivo1 [archivo2 …]" >&2
  exit 1
fi

nombre="$1"
shift

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
copia="$(cd "$script_dir/.." && pwd)"
salida="$script_dir/${nombre}.diff"

up="$(go env GOMODCACHE)/github.com/yutopp/go-rtmp@v0.0.7"
if [ ! -d "$up" ]; then
  echo "no está go-rtmp v0.0.7 en la caché de módulos ($up)" >&2
  exit 1
fi

: >"$salida"

for rel in "$@"; do
  a="$up/$rel"
  b="$copia/$rel"
  if [ ! -f "$a" ]; then
    echo "no existe en upstream: $rel" >&2
    exit 1
  fi
  if [ ! -f "$b" ]; then
    echo "no existe en la copia: $rel" >&2
    exit 1
  fi

  diff_crudo="$(git diff --no-index --src-prefix=a/ --dst-prefix=b/ "$a" "$b" || true)"

  # Normaliza las cabeceras a la ruta relativa rel y quita la línea `index …`, que varía
  # según el sistema de archivos donde se generó el diff.
  printf '%s\n' "$diff_crudo" \
    | grep -v '^index [0-9a-f]*\.\.[0-9a-f]*' \
    | sed \
      -e "s#^diff --git a/.*#diff --git a/${rel} b/${rel}#" \
      -e "s#^--- .*#--- a/${rel}#" \
      -e "s#^+++ .*#+++ b/${rel}#" \
    >>"$salida"
done

echo "generado: $salida"
