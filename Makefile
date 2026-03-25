.PHONY: build clean test lint tools-test

BINARY := opax
BUILD_DIR := bin

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/opax/

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...
	$(MAKE) tools-test

tools-test:
	cd tools && go test ./...

lint:
	go vet ./...
