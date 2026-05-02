.PHONY: build test lint clean release-dry fmt

# Format all Go files.
fmt:
	gofmt -w .

# Build the binary.
build:
	go build -ldflags "-s -w -X github.com/mendax0110/hf-sync/cmd.version=dev" -o bin/hf-sync .

# Run all tests.
test:
	go test -race ./...

# Run tests with coverage.
test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run linter.
lint:
	golangci-lint run ./...

# Clean build artifacts.
clean:
	rm -rf bin/ dist/ coverage.out coverage.html

# Dry-run goreleaser (test release without publishing).
release-dry:
	goreleaser release --snapshot --clean

# Install locally.
install:
	go install -ldflags "-s -w -X github.com/mendax0110/hf-sync/cmd.version=dev" .

# Tidy dependencies.
tidy:
	go mod tidy
