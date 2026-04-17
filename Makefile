.PHONY: all build test lint fmt clean install run release release-all \
       build-linux build-darwin build-windows

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

# Build flags
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X github.com/alayacore/alayacore/internal/config.Version=$(VERSION)"
RELEASE_LDFLAGS=-ldflags "-s -w -X github.com/alayacore/alayacore/internal/config.Version=$(VERSION)"
BUILDTAGS=-tags netgo

all: test build

## build: Build main binary for the current OS (static)
build:
	CGO_ENABLED=0 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY) .

## build-linux: Build for Linux (amd64, arm64, arm, riscv64)
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-arm .
	CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-linux-riscv64 .

## build-darwin: Build for macOS (amd64 + arm64)
build-darwin:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-darwin-arm64 .

## build-windows: Build for Windows (amd64 + arm64)
build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-windows-amd64.exe .
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(LDFLAGS) -o $(MAIN_BINARY)-windows-arm64.exe .

## release: Build optimized release binary for the current OS (stripped)
release:
	CGO_ENABLED=0 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY) .

## release-all: Build optimized release binaries for all platforms
release-all:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-arm .
	CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-linux-riscv64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-darwin-arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-windows-amd64.exe .
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GOBUILD) $(BUILDTAGS) $(RELEASE_LDFLAGS) -o $(MAIN_BINARY)-windows-arm64.exe .

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
	rm -f $(MAIN_BINARY) $(MAIN_BINARY).exe
	rm -f $(MAIN_BINARY)-linux-amd64 $(MAIN_BINARY)-linux-arm64 $(MAIN_BINARY)-linux-arm $(MAIN_BINARY)-linux-riscv64
	rm -f $(MAIN_BINARY)-darwin-amd64 $(MAIN_BINARY)-darwin-arm64
	rm -f $(MAIN_BINARY)-windows-amd64.exe $(MAIN_BINARY)-windows-arm64.exe
	rm -f coverage.out coverage.html

## install: Install main binary to GOPATH/bin
install:
	CGO_ENABLED=0 $(GOCMD) install $(BUILDTAGS) $(LDFLAGS) .

## mod: Download and tidy modules
mod:
	$(GOMOD) download
	$(GOMOD) tidy

## run: Run the main binary
run:
	CGO_ENABLED=0 $(GOBUILD) $(BUILDTAGS) -o $(MAIN_BINARY) .
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
