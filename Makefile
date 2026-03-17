.PHONY: build clean test lint

BINARY := opax
BUILD_DIR := bin

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/opax/

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...

lint:
	go vet ./...
