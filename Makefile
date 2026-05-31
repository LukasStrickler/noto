# Developer ergonomics for working on noto from a checked-out repo.
# All targets run against the local source tree — no `go install`
# required.

# --- Go toolchain discovery ----------------------------------------
# scripts/go is a wrapper that finds Go in PATH or common install
# locations, and downloads a copy into ./.tools/go on first use if
# none is present. This means `make dev` works on a fresh checkout
# even without Go installed system-wide.
#
# Override with `make GO=/path/to/go ...` if you want a specific
# toolchain.
GO ?= $(CURDIR)/scripts/go

# Go toolchain version downloaded by `make bootstrap-go` (and the
# scripts/go fallback). Keep in sync with scripts/go's GO_VERSION.
GO_VERSION ?= 1.26.3
HOST_OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
HOST_ARCH := $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')

BIN_DIR := $(CURDIR)/bin
BIN := $(BIN_DIR)/noto

LDFLAGS := -s -w
GOFLAGS := -trimpath

# Default target: build into ./bin/noto.
.PHONY: all
all: build

.PHONY: which-go
which-go:
	@echo "GO=$(GO)"
	@$(GO) version

.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/noto

# `make dev` — launch the TUI directly from source. Fastest iteration
# loop. Prints debug paths to stderr.
.PHONY: dev
dev:
	$(GO) run ./cmd/noto dev

# `make serve` — launch the backend daemon from source.
.PHONY: serve
serve:
	$(GO) run ./cmd/noto dev serve

# `make tui` — launch the TUI (assumes a backend is reachable or
# auto-spawns one).
.PHONY: tui
tui:
	$(GO) run ./cmd/noto tui

# `make run-cli ARGS="list"` — run any subcommand from source.
.PHONY: run-cli
run-cli:
	$(GO) run ./cmd/noto $(ARGS)

# `make seed` — drop three fixture meetings into the local store so the
# TUI/search have something to render in a fresh checkout. Idempotent:
# fixtures use fixed UUIDs and overwrite themselves on re-run.
.PHONY: seed
seed:
	$(GO) run ./cmd/noto seed

.PHONY: test
test:
	$(GO) test -count=1 ./...

.PHONY: test-e2e
test-e2e:
	$(GO) test -count=1 -run E2E -v ./internal/notohost/

.PHONY: test-race
test-race:
	$(GO) test -race -count=1 ./...

.PHONY: vet
vet:
	$(GO) vet ./...

# gofmt the project source in place.
.PHONY: fmt
fmt:
	@"$$($(GO) env GOROOT)/bin/gofmt" -w cmd internal

# Fail (without modifying anything) if any file isn't gofmt-clean — the
# gate CI enforces.
.PHONY: fmt-check
fmt-check:
	@files=$$("$$($(GO) env GOROOT)/bin/gofmt" -l cmd internal); \
	if [ -n "$$files" ]; then \
		echo "These files need gofmt (run 'make fmt'):"; echo "$$files"; exit 1; \
	fi; \
	echo "gofmt: clean"

# golangci-lint config lives in .golangci.yml. Falls back to `go vet`
# with a hint if the linter isn't installed locally.
GOLANGCI ?= golangci-lint
GOLANGCI_VERSION ?= v2.1.6
.PHONY: lint
lint:
	@if command -v $(GOLANGCI) >/dev/null 2>&1; then \
		$(GOLANGCI) run ./...; \
	else \
		echo "golangci-lint not found — run 'make lint-install'; falling back to vet"; \
		$(GO) vet ./...; \
	fi

# Install the pinned golangci-lint into the toolchain bin so `make lint`
# works on a fresh checkout.
.PHONY: lint-install
lint-install:
	GOBIN="$$($(GO) env GOROOT)/bin" $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

.PHONY: clean
clean:
	rm -rf $(BIN_DIR)

.PHONY: tidy
tidy:
	$(GO) mod tidy

# Run the full check sweep that a PR should pass — same gates as CI.
.PHONY: check
check: fmt-check vet lint test

# --- bootstrap-go --------------------------------------------------
# Download a Go toolchain into ./.tools/go and use it for subsequent
# `make` invocations. Useful on dev boxes without Go in PATH.
.PHONY: bootstrap-go
bootstrap-go:
	@mkdir -p $(CURDIR)/.tools
	@echo "Fetching Go $(GO_VERSION) for $(HOST_OS)/$(HOST_ARCH)…"
	@curl -fsSL "https://go.dev/dl/go$(GO_VERSION).$(HOST_OS)-$(HOST_ARCH).tar.gz" \
		| tar -xz -C $(CURDIR)/.tools
	@echo "Done. Re-run your `make` target (or set GO=$(CURDIR)/.tools/go/bin/go)."

.PHONY: help
help:
	@echo 'Common targets:'
	@echo '  make dev           # run TUI from source (./cmd/noto dev)'
	@echo '  make serve         # run backend daemon from source'
	@echo '  make build         # produce ./bin/noto'
	@echo '  make test          # run all tests'
	@echo '  make test-e2e      # only the end-to-end pipeline test'
	@echo '  make run-cli ARGS="list --json"'
	@echo '  make seed          # populate the local store with 3 fixture meetings'
	@echo '  make which-go      # print the Go toolchain noto will use'
	@echo '  make bootstrap-go  # download Go locally into ./.tools/go (no sudo)'
	@echo ''
	@echo 'Toolchain: $(GO)'
