# EROFS Snapshotter Makefile

BINARY := erofs-snapshotter
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)

GO := go
GOFLAGS := -trimpath
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

BIN_DIR := bin
CMD_DIR := cmd/erofs-snapshotter

.PHONY: all
all: build

.PHONY: build
build: $(BIN_DIR)/$(BINARY)

$(BIN_DIR)/$(BINARY): $(shell find . -name '*.go' -type f)
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./$(CMD_DIR)

.PHONY: build-linux
build-linux:
	GOOS=linux GOARCH=amd64 $(MAKE) build

.PHONY: install
install: build
	install -D -m 755 $(BIN_DIR)/$(BINARY) /usr/local/bin/$(BINARY)

.PHONY: test
test:
	$(GO) test -v -race ./...

.PHONY: test-root
test-root:
	sudo $(GO) test -v -race ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: fmt
fmt:
	$(GO) fmt ./...
	gofumpt -w .

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: clean
clean:
	rm -rf $(BIN_DIR)

.PHONY: deps
deps:
	$(GO) mod download

.PHONY: vendor
vendor:
	$(GO) mod vendor

# Docker build
.PHONY: docker-build
docker-build:
	docker build -t erofs-snapshotter:$(VERSION) .

# Install systemd service
.PHONY: install-service
install-service: install
	install -D -m 644 config/erofs-snapshotter.service /etc/systemd/system/erofs-snapshotter.service
	install -D -m 644 config/config.toml.example /etc/erofs-snapshotter/config.toml
	systemctl daemon-reload

.PHONY: help
help:
	@echo "EROFS Snapshotter Build Targets:"
	@echo ""
	@echo "  build        Build the binary"
	@echo "  build-linux  Build for Linux (cross-compile)"
	@echo "  install      Install binary to /usr/local/bin"
	@echo "  test         Run tests"
	@echo "  test-root    Run tests as root (required for integration tests)"
	@echo "  lint         Run linter"
	@echo "  fmt          Format code"
	@echo "  vet          Run go vet"
	@echo "  tidy         Run go mod tidy"
	@echo "  clean        Clean build artifacts"
	@echo "  deps         Download dependencies"
	@echo "  vendor       Vendor dependencies"
	@echo "  docker-build Build Docker image"
	@echo "  help         Show this help"
