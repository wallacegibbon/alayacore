.PHONY: all build test lint fmt clean install run

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Binary names
MAIN_BINARY=alayacore
WEB_BINARY=alayacore-web

# Build flags
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.Version=$(VERSION)"

all: test build

## build: Build all binaries
build:
	$(GOBUILD) $(LDFLAGS) -o $(MAIN_BINARY) .
	$(GOBUILD) $(LDFLAGS) -o $(WEB_BINARY) ./cmd/alayacore-web/

## build-linux: Build for Linux
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(MAIN_BINARY)-linux .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(WEB_BINARY)-linux ./cmd/alayacore-web/

## build-darwin: Build for macOS
build-darwin:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(MAIN_BINARY)-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(MAIN_BINARY)-darwin-arm64 .

## test: Run all tests
test:
	$(GOTEST) -v ./...

## test-coverage: Run tests with coverage
test-coverage:
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

## lint: Run golangci-lint
lint:
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./...

## fmt: Format code
fmt:
	$(GOFMT) ./...

## vet: Run go vet
vet:
	$(GOCMD) vet ./...

## clean: Clean build artifacts
clean:
	$(GOCLEAN)
	rm -f $(MAIN_BINARY) $(WEB_BINARY)
	rm -f $(MAIN_BINARY)-linux $(WEB_BINARY)-linux
	rm -f $(MAIN_BINARY)-darwin-*
	rm -f coverage.out coverage.html

## install: Install binaries to GOPATH/bin
install:
	$(GOCMD) install $(LDFLAGS) .
	$(GOCMD) install $(LDFLAGS) ./cmd/alayacore-web/

## mod: Download and tidy modules
mod:
	$(GOMOD) download
	$(GOMOD) tidy

## run: Run the main binary
run:
	$(GOBUILD) -o $(MAIN_BINARY) .
	./$(MAIN_BINARY)

## check: Run all checks (fmt, vet, lint, test)
check: fmt vet lint test

## pre-commit: Run checks before committing
pre-commit: fmt vet test

## help: Show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':'
