.PHONY: build run test clean lint

# Binary name
BINARY_NAME=server

# Build the application
build:
	go build -o $(BINARY_NAME) ./cmd/rest-server/

# Run the application
run:
	go run ./cmd/rest-server/

# Run tests
test:
	go test ./... -v

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME)
	go clean

# Run linter (requires golangci-lint)
lint:
	golangci-lint run ./... 2>/dev/null || go vet ./...
