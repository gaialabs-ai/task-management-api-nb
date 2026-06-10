APP_NAME := rest-server
BUILD_DIR := bin

.PHONY: build run test lint clean fmt vet

build:
	go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/rest-server

run: build
	./$(BUILD_DIR)/$(APP_NAME)

test:
	go test ./... -v -race -cover

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BUILD_DIR)

fmt:
	go fmt ./...

vet:
	go vet ./...
