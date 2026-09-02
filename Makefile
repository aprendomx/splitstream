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

# Requiere sinks-up, ffmpeg y ffprobe.
test-integration:
	go test -tags integration ./test/integration/ -v -count=1 -timeout 5m
