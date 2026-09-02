.PHONY: build test test-integration sinks-up sinks-down vet tidy run clean

# CGO_ENABLED=0 solo aquí: el binario de producción debe ser estático.
build:
	CGO_ENABLED=0 go build -o splitstream ./cmd/splitstream

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
