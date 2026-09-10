#!/bin/sh
# Instala Splitstream en Linux o macOS: descarga la release, verifica el checksum, copia
# el binario y, si hay systemd y una terminal, ofrece dejarlo como servicio.
#
#   curl -fsSL https://raw.githubusercontent.com/aprendomx/splitstream/main/deploy/install.sh | sh
#
# Variables opcionales:
#   SPLITSTREAM_VERSION      etiqueta a instalar (por defecto, la última release)
#   SPLITSTREAM_INSTALL_DIR  dónde dejar el binario (por defecto /usr/local/bin)
#   SPLITSTREAM_RELEASE_URL  base de descarga (para probar el script contra un servidor local)
#   SPLITSTREAM_SERVICE      "no" para no ofrecer la unidad de systemd
#
# Nunca usa sudo sin decirlo antes y sin que se conteste que sí. Sin terminal —el caso
# de `curl | sh` en un script— imprime el comando y termina.
set -u

REPO="aprendomx/splitstream"
INSTALL_DIR="${SPLITSTREAM_INSTALL_DIR:-/usr/local/bin}"
TMP=""

decir() { printf '%s\n' "$*"; }
fallar() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }
limpiar() { [ -n "$TMP" ] && rm -rf "$TMP"; }
trap limpiar EXIT
tiene() { command -v "$1" >/dev/null 2>&1; }

descargar() { # url destino
  if tiene curl; then curl -fsSL --retry 3 -o "$2" "$1"
  elif tiene wget; then wget -q -O "$2" "$1"
  else fallar "hace falta curl o wget"
  fi
}

preguntar() { # texto → 0 si la persona dice que sí
  [ -r /dev/tty ] || return 1
  printf '%s [s/N] ' "$1"
  read -r resp < /dev/tty
  case "$resp" in s|S|si|sí|y|Y) return 0 ;; *) return 1 ;; esac
}

# ── 1. Plataforma ────────────────────────────────────────────────────────────
SO=$(uname -s | tr '[:upper:]' '[:lower:]')
ARQ=$(uname -m)
case "$ARQ" in
  x86_64|amd64) ARQ=amd64 ;;
  aarch64|arm64) ARQ=arm64 ;;
  *) fallar "arquitectura no soportada: $ARQ" ;;
esac
case "$SO-$ARQ" in
  darwin-arm64) NOMBRE=macos-apple-silicon ;;
  darwin-amd64) NOMBRE=macos-intel ;;
  linux-amd64)  NOMBRE=linux-x86_64 ;;
  linux-arm64)  NOMBRE=linux-arm64 ;;
  *) fallar "sistema no soportado: $SO. En Windows: winget install aprendomx.Splitstream" ;;
esac

# ── 2. Versión ───────────────────────────────────────────────────────────────
TMP=$(mktemp -d) || fallar "no se pudo crear un directorio temporal"
VERSION="${SPLITSTREAM_VERSION:-}"
if [ -z "$VERSION" ]; then
  descargar "https://api.github.com/repos/$REPO/releases/latest" "$TMP/latest.json" \
    || fallar "no se pudo consultar la última release"
  VERSION=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$TMP/latest.json" | head -n 1)
  [ -n "$VERSION" ] || fallar "no se pudo leer la versión de la respuesta de GitHub"
fi
BASE="${SPLITSTREAM_RELEASE_URL:-https://github.com/$REPO/releases/download/$VERSION}"
ARCHIVO="splitstream-$VERSION-$NOMBRE.tar.gz"
decir "Splitstream $VERSION para $NOMBRE"

# ── 3. Descarga y verificación ───────────────────────────────────────────────
descargar "$BASE/$ARCHIVO" "$TMP/$ARCHIVO" || fallar "no se pudo descargar $BASE/$ARCHIVO"
descargar "$BASE/SHA256SUMS.txt" "$TMP/SHA256SUMS.txt" || fallar "no se pudo descargar SHA256SUMS.txt"
ESPERADA=$(awk -v f="$ARCHIVO" '$2 == f { print $1 }' "$TMP/SHA256SUMS.txt")
[ -n "$ESPERADA" ] || fallar "SHA256SUMS.txt no lista $ARCHIVO"
if tiene sha256sum; then REAL=$(sha256sum "$TMP/$ARCHIVO" | awk '{ print $1 }')
elif tiene shasum; then REAL=$(shasum -a 256 "$TMP/$ARCHIVO" | awk '{ print $1 }')
else fallar "hace falta sha256sum o shasum para verificar la descarga"
fi
[ "$REAL" = "$ESPERADA" ] || fallar "el checksum de $ARCHIVO no coincide: descarga corrupta o manipulada. No se instaló nada."
decir "checksum verificado"

# ── 4. Extraer ───────────────────────────────────────────────────────────────
tar -xzf "$TMP/$ARCHIVO" -C "$TMP" || fallar "no se pudo extraer $ARCHIVO"
CARPETA="$TMP/splitstream-$VERSION-$NOMBRE"
BIN="$CARPETA/splitstream"
[ -f "$BIN" ] || fallar "el archivo no contiene el binario esperado"
chmod +x "$BIN"

# ── 5. Instalar ──────────────────────────────────────────────────────────────
[ -d "$INSTALL_DIR" ] || mkdir -p "$INSTALL_DIR" 2>/dev/null || true
if [ -d "$INSTALL_DIR" ] && [ -w "$INSTALL_DIR" ]; then
  install -m 755 "$BIN" "$INSTALL_DIR/splitstream" || fallar "no se pudo copiar a $INSTALL_DIR"
elif tiene sudo; then
  decir "Copiar el binario a $INSTALL_DIR necesita permiso de administrador. El comando:"
  decir "    sudo install -d -m 755 $INSTALL_DIR && sudo install -m 755 $BIN $INSTALL_DIR/splitstream"
  if preguntar "¿Lo ejecuto con sudo?"; then
    # fallar() siempre termina el script, así que no hay caso en que el patrón
    # A && B || C se ejecute mal tras un A exitoso: es seguro.
    # shellcheck disable=SC2015
    sudo install -d -m 755 "$INSTALL_DIR" && sudo install -m 755 "$BIN" "$INSTALL_DIR/splitstream" \
      || fallar "sudo install falló"
  else
    fallar "cancelado. Ejecuta ese comando tú, o vuelve a correr el script con SPLITSTREAM_INSTALL_DIR=\$HOME/.local/bin"
  fi
else
  fallar "$INSTALL_DIR no es escribible y no hay sudo. Usa SPLITSTREAM_INSTALL_DIR=\$HOME/.local/bin"
fi

"$INSTALL_DIR/splitstream" -version >/dev/null 2>&1 || fallar "el binario instalado no arranca"
decir "Instalado: $INSTALL_DIR/splitstream ($("$INSTALL_DIR/splitstream" -version))"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) decir "Aviso: $INSTALL_DIR no está en tu PATH; añádelo o llama al binario con la ruta completa." ;;
esac

# ── 6. systemd ───────────────────────────────────────────────────────────────
instrucciones_servicio() {
  decir ""
  decir "Para dejarlo como servicio de systemd (arranque automático, usuario propio):"
  decir "    sudo useradd --system --home /var/lib/splitstream --shell /usr/sbin/nologin splitstream"
  decir "    sudo install -d -o splitstream -g splitstream /var/lib/splitstream"
  decir "    sudo install -d -m 700 /etc/splitstream"
  decir "    printf 'SPLITSTREAM_MASTER_KEY=%s\\n' \"\$(splitstream -genkey)\" | sudo tee /etc/splitstream/env >/dev/null"
  decir "    sudo chmod 600 /etc/splitstream/env"
  decir "    sudo install -m 644 $CARPETA/splitstream.service /etc/systemd/system/   # o deploy/splitstream.service del repo"
  decir "    sudo systemctl enable --now splitstream"
}

instalar_servicio() {
  UNIT="$CARPETA/splitstream.service"
  if [ ! -f "$UNIT" ]; then
    descargar "https://raw.githubusercontent.com/$REPO/$VERSION/deploy/splitstream.service" "$UNIT" \
      || fallar "no se pudo descargar la unidad de systemd"
  fi
  decir "Voy a ejecutar con sudo: useradd splitstream, crear /var/lib/splitstream y /etc/splitstream/env, instalar la unidad y systemctl enable --now."
  id splitstream >/dev/null 2>&1 \
    || sudo useradd --system --home /var/lib/splitstream --shell /usr/sbin/nologin splitstream \
    || fallar "useradd falló"
  sudo install -d -o splitstream -g splitstream /var/lib/splitstream || fallar "no se pudo crear /var/lib/splitstream"
  sudo install -d -m 700 /etc/splitstream || fallar "no se pudo crear /etc/splitstream"
  if ! sudo test -f /etc/splitstream/env; then
    CLAVE=$("$INSTALL_DIR/splitstream" -genkey) || fallar "no se pudo generar la clave maestra"
    printf 'SPLITSTREAM_MASTER_KEY=%s\n' "$CLAVE" | sudo tee /etc/splitstream/env >/dev/null \
      || fallar "no se pudo escribir /etc/splitstream/env"
    unset CLAVE
    sudo chmod 600 /etc/splitstream/env
    decir "Clave maestra nueva en /etc/splitstream/env. RESPÁLDALA: sin ella, las claves de tus canales son irrecuperables."
  fi
  sudo install -m 644 "$UNIT" /etc/systemd/system/splitstream.service || fallar "no se pudo instalar la unidad"
  # Mismo caso: fallar() no retorna, así que no hay ambigüedad en el A && B || C.
  # shellcheck disable=SC2015
  sudo systemctl daemon-reload && sudo systemctl enable --now splitstream || fallar "systemctl falló"
  decir "Servicio arrancado. El código del primer arranque: journalctl -u splitstream | grep -A2 'te pedirá este código'"
}

if [ "$SO" = linux ] && tiene systemctl && [ "${SPLITSTREAM_SERVICE:-}" != no ]; then
  if [ -f /etc/systemd/system/splitstream.service ]; then
    decir "La unidad de systemd ya existe; reinicia el servicio para usar el binario nuevo: sudo systemctl restart splitstream"
  elif [ "$INSTALL_DIR" != /usr/local/bin ]; then
    decir "La unidad de systemd espera el binario en /usr/local/bin; ajusta ExecStart si lo instalas como servicio."
    instrucciones_servicio
  elif preguntar "¿Instalar el servicio de systemd (usuario splitstream, /var/lib/splitstream, arranque automático)? Usa sudo."; then
    instalar_servicio
  else
    instrucciones_servicio
  fi
fi

decir ""
decir "Listo. Arranca con: splitstream   y abre http://localhost:8080"
