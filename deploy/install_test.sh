#!/bin/sh
# Prueba deploy/install.sh contra una release falsa: empaqueta el binario del commit
# como lo hace release.yml, lo sirve por HTTP dentro de una red de Docker y corre el
# instalador en ubuntu (con curl) y en alpine (con el wget de busybox). Después
# manipula SHA256SUMS.txt y comprueba que el instalador se niega.
#
# Necesita Docker y Go. Uso: sh deploy/install_test.sh
set -eu
cd "$(dirname "$0")/.."

VERSION=v0.0.0-test
NOMBRE=linux-x86_64
ART=$(mktemp -d)
RED=splitstream-install-test
limpiar() {
  docker rm -f "$RED-server" >/dev/null 2>&1 || true
  docker network rm "$RED" >/dev/null 2>&1 || true
  rm -rf "$ART"
}
trap limpiar EXIT

# 1. Una release como la de verdad.
CARPETA="splitstream-$VERSION-$NOMBRE"
mkdir -p "$ART/$CARPETA"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
  -o "$ART/$CARPETA/splitstream" ./cmd/splitstream
cp README.md LICENSE deploy/splitstream.service "$ART/$CARPETA/"
(cd "$ART" && tar czf "$CARPETA.tar.gz" "$CARPETA" && rm -r "$CARPETA" && sha256sum splitstream-* > SHA256SUMS.txt)
chmod -R a+rX "$ART"

# 2. Servida por HTTP en una red propia.
docker network create "$RED" >/dev/null
docker run -d --rm --name "$RED-server" --network "$RED" -v "$ART:/srv:ro" busybox:1.36 \
  httpd -f -p 80 -h /srv >/dev/null

instalar() { # imagen preparación
  docker run --rm --network "$RED" -v "$PWD/deploy/install.sh:/install.sh:ro" \
    -e SPLITSTREAM_VERSION="$VERSION" -e SPLITSTREAM_RELEASE_URL="http://$RED-server" \
    -e SPLITSTREAM_INSTALL_DIR=/opt/bin -e SPLITSTREAM_SERVICE=no \
    "$1" sh -c "$2; cat /install.sh | sh && /opt/bin/splitstream -version | grep -F '$VERSION' && ! command -v sudo"
}

echo "== ubuntu (curl)"
instalar ubuntu:24.04 "apt-get update -qq >/dev/null && apt-get install -qq -y curl ca-certificates >/dev/null"
echo "== alpine (wget de busybox)"
instalar alpine:3.20 "true"

# 3. Checksum manipulado: no instala y no deja el binario.
echo "== checksum manipulado"
printf '%064d  %s.tar.gz\n' 0 "$CARPETA" > "$ART/SHA256SUMS.txt"
if docker run --rm --network "$RED" -v "$PWD/deploy/install.sh:/install.sh:ro" \
    -e SPLITSTREAM_VERSION="$VERSION" -e SPLITSTREAM_RELEASE_URL="http://$RED-server" \
    -e SPLITSTREAM_INSTALL_DIR=/opt/bin -e SPLITSTREAM_SERVICE=no \
    alpine:3.20 sh -c 'sh /install.sh; rc=$?; [ ! -e /opt/bin/splitstream ] && [ $rc -ne 0 ]'; then
  echo "el instalador rechazó el checksum manipulado"
else
  echo "FALLO: el instalador aceptó un checksum manipulado o dejó el binario" >&2
  exit 1
fi
echo "install_test: ok"
