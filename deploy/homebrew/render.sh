#!/bin/sh
# Rellena la fórmula con la etiqueta y los checksums de SHA256SUMS.txt.
#   deploy/homebrew/render.sh v0.10.0 SHA256SUMS.txt > splitstream.rb
set -eu
TAG=$1
SUMAS=$2
DIR=$(dirname "$0")

suma() { awk -v f="splitstream-$TAG-$1.tar.gz" '$2 == f { print $1 }' "$SUMAS"; }

EXPR="s/{{VERSION_SIN_V}}/${TAG#v}/g; s/{{VERSION}}/$TAG/g"
for n in macos-apple-silicon macos-intel linux-x86_64 linux-arm64; do
  s=$(suma "$n")
  [ -n "$s" ] || { echo "render.sh: falta $n en $SUMAS" >&2; exit 1; }
  EXPR="$EXPR; s/{{SHA256_$n}}/$s/g"
done
sed -e "$EXPR" "$DIR/splitstream.rb.tmpl"
