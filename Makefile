BINARY      := sl-dbg
PKG         := github.com/y0geshpatil/sl-dbg
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE        := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
               -X $(PKG)/internal/buildinfo.Version=$(VERSION) \
               -X $(PKG)/internal/buildinfo.Commit=$(COMMIT) \
               -X $(PKG)/internal/buildinfo.Date=$(DATE)

GO          ?= go
GOFLAGS     :=
BUILD_DIR   := bin

.PHONY: all build install clean test test-unit test-integration lint fmt vet tidy run help java-adapter setup

all: build

## build: Compile the sl-dbg binary into ./bin/
build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY) ./cmd/sl-dbg
	@echo "✓ Built $(BUILD_DIR)/$(BINARY) ($(VERSION))"

## install: Install sl-dbg to $GOPATH/bin
install:
	$(GO) install $(GOFLAGS) -ldflags '$(LDFLAGS)' ./cmd/sl-dbg

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR) dist coverage.* *.out

## test: Run unit + e2e tests
test: test-unit test-e2e

## test-unit: Run Go unit tests
test-unit:
	$(GO) test -race -count=1 ./...

## test-integration: Run integration tests (requires adapters installed)
test-integration:
	$(GO) test -tags=integration -race -count=1 ./test/integration/...

## test-e2e: Run end-to-end shell tests against installed adapters
##           (adapters that aren't installed are skipped, not failed)
test-e2e: build
	@bash test/e2e/run-all.sh

## lint: Run linters
lint:
	$(GO) vet ./...
	@command -v golangci-lint >/dev/null && golangci-lint run || echo "(install golangci-lint for full linting)"

## fmt: Format code
fmt:
	$(GO) fmt ./...

## vet: Run go vet
vet:
	$(GO) vet ./...

## tidy: Tidy go.mod
tidy:
	$(GO) mod tidy

## run: Build and run with args (e.g., `make run ARGS="version"`)
run: build
	./$(BUILD_DIR)/$(BINARY) $(ARGS)

## help: Show this help
help:
	@grep -E '^## ' Makefile | sed 's/## //'

## setup: One-command end-to-end install — build sl-dbg + every language adapter
setup: build
	./$(BUILD_DIR)/$(BINARY) install-adapter all
	@echo "✓ sl-dbg is ready. Try: ./$(BUILD_DIR)/$(BINARY) adapters"

## java-adapter: Build the embedded Java DAP launcher fat-jar (requires Maven + JDK 11+)
java-adapter:
	cd adapters/java-launcher && mvn -q -DskipTests package
	@echo "✓ Built adapters/java-launcher/target/sl-dbg-java-adapter.jar"

## docs-mcp: Regenerate ../sl-dbg-site/docs/mcp.md from a live binary
docs-mcp: build
	python3 scripts/gen-mcp-docs.py ./$(BUILD_DIR)/$(BINARY) > ../sl-dbg-site/docs/mcp.md
	@echo "✓ Wrote ../sl-dbg-site/docs/mcp.md"
