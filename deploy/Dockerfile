# Imagen de producción de Splitstream.
#
# Tres etapas: panel, binario e imagen final. La final es `scratch` —nada, ni siquiera una
# shell— y pesa lo que pese el binario. Es posible porque el binario es estático:
# CGO_ENABLED=0 y modernc.org/sqlite, que es SQLite reescrito en Go puro.

# ── 1. El panel ───────────────────────────────────────────────────────────────
FROM node:22-alpine AS panel
WORKDIR /web

# package.json y el lock primero: mientras no cambien, Docker reutiliza la capa de
# node_modules y npm ci no se vuelve a ejecutar.
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

# ── 2. El binario ─────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS binario
WORKDIR /src

# tzdata: la imagen de alpine no trae /usr/share/zoneinfo, y la final lo necesita para que
# los timestamps se lean en la zona donde se opera y no siempre en UTC.
RUN apk add --no-cache tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# El panel compilado entra aquí para que go:embed lo meta dentro del ejecutable.
COPY --from=panel /web/dist/spa ./web/dist/spa

ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /splitstream ./cmd/splitstream

# El directorio de datos se crea AQUÍ y con su dueño, porque en scratch no hay shell donde
# hacer un mkdir. Docker copia esos permisos al volumen la primera vez que lo crea; sin
# esto, /data pertenece a root, el contenedor corre como 65532 y la base no se puede abrir:
# "unable to open database file (14)".
RUN mkdir -p /data && chown 65532:65532 /data

# ── 3. La imagen final ────────────────────────────────────────────────────────
FROM scratch

# Los certificados raíz NO son opcionales: sin ellos, cualquier destino rtmps:// falla con
# "x509: certificate signed by unknown authority". Facebook solo acepta RTMPS, así que una
# imagen sin esto parece funcionar hasta que alguien vincula su primer canal.
COPY --from=binario /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Zona horaria, para que los timestamps del log se lean donde se opera.
COPY --from=binario /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=binario --chown=65532:65532 /data /data
COPY --from=binario /splitstream /splitstream

# Sin root. El número va a pelo porque en scratch no hay /etc/passwd donde resolver un
# nombre; 65532 es el "nonroot" de las imágenes distroless.
USER 65532:65532

# /data es donde vive la base. Debe ser un volumen, o la configuración y las claves se
# pierden al recrear el contenedor.
VOLUME ["/data"]
ENV SPLITSTREAM_DB_PATH=/data/splitstream.db \
    SPLITSTREAM_HTTP_ADDR=:8080 \
    SPLITSTREAM_RTMP_ADDR=:1935

EXPOSE 1935 8080

ENTRYPOINT ["/splitstream"]
