.PHONY: build build-web build-go test test-integration sinks-up sinks-down vet tidy run clean

# La versión sale del tag más cercano. Sin tags (o sin git) queda en "dev", que es
# exactamente lo que vale un binario que no viene de una release.
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)

# El panel se compila ANTES que el binario: go:embed mete dist/spa dentro del ejecutable,
# así que un binario construido sin esto llevaría el panel de la vez anterior.
build-web:
	cd web && npm ci --silent && npm run build

# CGO_ENABLED=0 solo aquí: el binario de producción debe ser estático.
build: build-web
	CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(VERSION)" -o splitstream ./cmd/splitstream

# Solo el binario, con el panel que ya hubiera compilado. Para iterar en Go sin esperar a
# npm en cada vuelta.
build-go:
	CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(VERSION)" -o splitstream ./cmd/splitstream

# El detector de carreras necesita cgo, así que este target no lo desactiva.
test:
	go test ./... -race -count=1

vet:
	go vet ./...

tidy:
	go mod tidy

run: build
	./splitstream

clean:
	rm -f splitstream
	rm -rf web/dist/spa/assets web/dist/spa/index.html

# Levanta los mediamtx que usan los tests de integración.
sinks-up:
	docker compose -f deploy/test-compose.yml up -d

sinks-down:
	docker compose -f deploy/test-compose.yml down

# Requiere sinks-up, ffmpeg y ffprobe. El test de reconexión para el sink B a propósito;
# si una corrida anterior se abortó con Ctrl-C o por timeout, el contenedor puede haber
# quedado parado y el `defer` de Go no lo levanta —Go no desenrolla los defer al morir por
# señal o por timeout del runner—. Por eso se asegura aquí.
test-integration:
	@docker start splitstream-test-sink-a splitstream-test-sink-b 2>/dev/null || true
	go test -tags integration ./test/integration/ -v -count=1 -timeout 15m
