VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

BINARY  = ats
GOFLAGS = -trimpath
LDFLAGS = -s -w \
	-X github.com/atlasshare/atlax-tools/internal/version.ToolVersion=$(VERSION) \
	-X github.com/atlasshare/atlax-tools/internal/version.BuildCommit=$(COMMIT) \
	-X github.com/atlasshare/atlax-tools/internal/version.BuildDate=$(DATE)

.PHONY: build clean test test-coverage lint install cross

## Build for current platform
build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/ats/

## Install to GOPATH/bin
install:
	go install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/ats/

## Run tests
test:
	go test -race -count=1 ./...

## Run tests with coverage gate (fails if coverage drops below 80%)
test-coverage:
	go test -race -coverprofile=coverage.out \
		./internal/config/... ./internal/certs/... ./internal/tui/... ./internal/caddy/...
	go tool cover -func=coverage.out | awk '/total/{if ($$3+0 < 80) {print "FAIL: coverage below 80%"; exit 1}}'

## Run linter
lint:
	golangci-lint run ./...

## Cross-compile for all target platforms
cross:
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64   ./cmd/ats/
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64   ./cmd/ats/
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-amd64  ./cmd/ats/
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64  ./cmd/ats/
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-amd64.exe ./cmd/ats/
	GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-freebsd-amd64 ./cmd/ats/

## Clean build artifacts
clean:
	rm -rf bin/

## Show help
help:
	@echo "Targets:"
	@echo "  build          Build for current platform"
	@echo "  install        Install to GOPATH/bin"
	@echo "  test           Run tests"
	@echo "  test-coverage  Run tests with 80% coverage gate on core packages"
	@echo "  lint           Run linter"
	@echo "  cross          Cross-compile for all platforms"
	@echo "  clean          Remove build artifacts"
