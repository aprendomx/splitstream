#!/bin/sh
# Los render.sh rellenan todo y no dejan ningún {{marcador}}.
set -eu
cd "$(dirname "$0")/.."
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
TAG=v1.2.3
i=0
for n in macos-apple-silicon.tar.gz macos-intel.tar.gz linux-x86_64.tar.gz linux-arm64.tar.gz windows-x86_64.zip; do
  i=$((i + 1))
  printf '%064d  splitstream-%s-%s\n' "$i" "$TAG" "$n" >> "$T/SHA256SUMS.txt"
done

deploy/homebrew/render.sh "$TAG" "$T/SHA256SUMS.txt" > "$T/splitstream.rb"
grep -q 'version "1.2.3"' "$T/splitstream.rb"
grep -q "releases/download/$TAG/splitstream-$TAG-linux-arm64.tar.gz" "$T/splitstream.rb"
grep -q "$(printf '%064d' 4)" "$T/splitstream.rb"
grep -q '{{' "$T/splitstream.rb" && exit 1

deploy/winget/render.sh "$TAG" "$T/SHA256SUMS.txt" "$T/winget" > /dev/null
# `ls | wc -l` dispara SC2012 (el resultado de ls no es para parsear); find cuenta sin ese aviso.
[ "$(find "$T/winget" -type f | wc -l)" -eq 4 ]
grep -q 'PackageVersion: 1.2.3' "$T/winget/aprendomx.Splitstream.installer.yaml"
grep -q "InstallerSha256: $(printf '%064d' 5)" "$T/winget/aprendomx.Splitstream.installer.yaml"
grep -rq '{{' "$T/winget" && exit 1

# Sin el checksum de una plataforma, se niega.
head -n 2 "$T/SHA256SUMS.txt" > "$T/parcial.txt"
deploy/homebrew/render.sh "$TAG" "$T/parcial.txt" > /dev/null 2>&1 && exit 1
echo "render_test: ok"
