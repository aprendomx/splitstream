#!/bin/sh
# Rellena los manifiestos de winget en <dir>.
#   deploy/winget/render.sh v0.10.0 SHA256SUMS.txt manifests/a/aprendomx/Splitstream/0.10.0
set -eu
TAG=$1
SUMAS=$2
DEST=$3
DIR=$(dirname "$0")

SUMA=$(awk -v f="splitstream-$TAG-windows-x86_64.zip" '$2 == f { print $1 }' "$SUMAS")
[ -n "$SUMA" ] || { echo "render.sh: falta windows-x86_64.zip en $SUMAS" >&2; exit 1; }

mkdir -p "$DEST"
for t in "$DIR"/*.yaml.tmpl; do
  salida="$DEST/$(basename "$t" .tmpl)"
  sed -e "s/{{VERSION_SIN_V}}/${TAG#v}/g; s/{{VERSION}}/$TAG/g; s/{{SHA256_windows}}/$SUMA/g" "$t" > "$salida"
done
ls "$DEST"
