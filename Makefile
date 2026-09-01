.PHONY: build test vet tidy run clean

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
