BINARY := keystone

.PHONY: build test vet fmt cover run clean

build:
	go build -o $(BINARY) ./cmd/keystone

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

run: build
	./$(BINARY) --config config.example.yaml

clean:
	rm -f $(BINARY) coverage.out
