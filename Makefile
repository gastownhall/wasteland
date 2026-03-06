GOLANGCI_LINT_VERSION := 2.9.0

# Detect OS and arch for binary download.
GOOS   := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

BIN_DIR := $(shell go env GOPATH)/bin
GOLANGCI_LINT := $(BIN_DIR)/golangci-lint

BINARY     := wl
BUILD_DIR  := bin
INSTALL_DIR := $(HOME)/.local/bin

# Version metadata injected via ldflags.
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Set to "true" to enable inference UI in CLI and web.
INFER_ENABLED ?= false

LDFLAGS := -X main.version=$(VERSION) \
           -X main.commit=$(COMMIT) \
           -X main.date=$(BUILD_TIME) \
           -X main.inferEnabled=$(INFER_ENABLED)

.PHONY: build build-go web check check-all lint fmt-check fmt vet test test-integration test-integration-offline test-cover cover install install-tools setup clean web-check web-test audit audit-web

## web: build web UI (requires bun)
web:
	cd web && VITE_INFER_ENABLED=$(INFER_ENABLED) bun install --frozen-lockfile && bun run build
	touch web/dist/.gitkeep

## build: compile wl binary with embedded web UI
build: web
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/wl

## build-go: compile wl binary without rebuilding web UI
build-go:
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/wl

## install: build and install wl to ~/.local/bin
install: build
	@mkdir -p $(INSTALL_DIR)
	@rm -f $(INSTALL_DIR)/$(BINARY)
	@cp $(BUILD_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "Installed $(BINARY) to $(INSTALL_DIR)/$(BINARY)"

## clean: remove build artifacts
clean:
	rm -f $(BUILD_DIR)/$(BINARY)

## check: run fast quality gates (pre-commit: unit tests only)
check: fmt-check lint vet test

## check-all: run all quality gates including integration tests (CI)
check-all: fmt-check lint vet test-integration

## lint: run golangci-lint
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run ./...

## fmt-check: fail if formatting would change files
fmt-check: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) fmt --diff ./...

## fmt: auto-fix formatting
fmt: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) fmt ./...

## vet: run go vet
vet:
	go vet ./...

## test: run unit tests (skip integration tests tagged with //go:build integration)
test:
	go test ./...

## test-integration: run all tests including integration
test-integration:
	go test -tags integration -timeout 20m ./...

## test-integration-offline: run offline integration tests only (no network, requires dolt)
test-integration-offline:
	go test -tags integration -v -timeout 20m ./internal/remote/ ./test/integration/offline/

## test-cover: run tests with coverage output
test-cover:
	go test -coverprofile=coverage.txt ./...

## cover: run tests and show coverage report
cover: test-cover
	go tool cover -func=coverage.txt

## install-tools: install pinned golangci-lint
install-tools: $(GOLANGCI_LINT)

$(GOLANGCI_LINT):
	@echo "Installing golangci-lint v$(GOLANGCI_LINT_VERSION)..."
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | \
		sh -s -- -b $(BIN_DIR) v$(GOLANGCI_LINT_VERSION)

## setup: install tools, web deps, and git hooks
setup: install-tools
	@command -v bun >/dev/null 2>&1 || { echo "Installing bun..."; curl -fsSL https://bun.sh/install | bash; }
	cd web && bun install
	ln -sf ../../scripts/pre-commit .git/hooks/pre-commit
	@echo "Done. Tools installed, pre-commit hook active."

## web-check: typecheck + lint + test web frontend
web-check:
	cd web && bun run check

## web-test: run web tests with coverage
web-test:
	cd web && bun run test:coverage

## audit: run Go vulnerability check
audit:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

## audit-web: run web dependency audit
audit-web:
	cd web && bun audit

## help: show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'
