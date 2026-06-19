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

ifneq (,$(wildcard .env))
include .env
export
endif

ifneq (,$(wildcard .env.test))
include .env.test
export
endif

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

# `make reset` — wipe the local store (all meetings + people) back to empty.
.PHONY: reset
reset:
	$(GO) run ./cmd/noto reset --yes

.PHONY: test
test:
	$(GO) test -count=1 ./...

.PHONY: test-e2e
test-e2e:
	$(GO) test -count=1 -run E2E -v ./internal/notohost/

# `make bench-metrics` — the fast TDD loop for the pure scorers (WER/DER/cpWER/…).
# No assets, no network; also covered by plain `make test` via ./...
.PHONY: bench-metrics
bench-metrics:
	$(GO) test -count=1 ./benchmark/metrics/

# Sample-size controls for the Go benchmark suites. `make bench HOURS=1` limits a
# run to ~1 hour of audio (whole meetings only); SEED makes that draw reproducible
# (default 1). These become trailing `-hours`/`-seed` flags on the go test command
# — they MUST come after the package path, which is exactly the gotcha this
# wrapper hides (go test only parses custom test flags once it has seen the
# packages; flags before the path make it look for a package literally named ".").
HOURS ?=
SEED ?=
BENCH_FLAGS := $(if $(strip $(HOURS)),-hours=$(HOURS),) $(if $(strip $(SEED)),-seed=$(SEED),)

# `make bench` — the full accuracy+speed harness (atomic + chained). Gated on the
# datasets/models fetched by the benchmark/*/fetch.py scripts; tests t.Skip when
# assets are absent, so this is safe to run without them (it just skips the heavy
# cases). BENCH_DEEP/PERSONA_DEEP unlock the deep sweeps.
.PHONY: bench
bench:
	BENCH_DEEP=1 PERSONA_DEEP=1 $(GO) test -count=1 -v -timeout 1800s ./benchmark/... $(BENCH_FLAGS)

# `make bench-deps` — fetch the local STT runtime + model + ffmpeg the real
# benchmark needs (sherpa-onnx CLI, Parakeet-TDT-0.6b-v3, static ffmpeg). Lands
# under ~/.cache/noto-bench (override with NOTO_BENCH_DEPS), idempotent.
.PHONY: bench-deps
bench-deps:
	bash scripts/fetch-bench-deps.sh

# `make bench-stt` — real WER over the staged corpus, using the local sherpa
# engine fetched by `make bench-deps`. Requires reference words on disk (e.g.
# `FFMPEG=~/.cache/noto-bench/ffmpeg python3 benchmark/dataset/fetch_librispeech_wer.py`).
NOTO_BENCH_DEPS ?= $(HOME)/.cache/noto-bench
SHERPA_DIR := $(NOTO_BENCH_DEPS)/sherpa-onnx-v1.13.2-linux-x64-shared-no-tts
.PHONY: bench-stt
bench-stt:
	BENCH_STT_ENGINE=sherpa-parakeet \
	BENCH_STT_MODELS="$(NOTO_BENCH_DEPS)/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8" \
	NOTO_SHERPA_BIN="$(SHERPA_DIR)/bin/sherpa-onnx-offline" \
	NOTO_SHERPA_LIB="$(SHERPA_DIR)/lib" \
	NOTO_FFMPEG="$(NOTO_BENCH_DEPS)/ffmpeg" \
	NOTO_SHERPA_THREADS=$$(nproc) \
	$(GO) test -count=1 -v -timeout 1800s -run TestSTTAMI ./benchmark/stt/ $(BENCH_FLAGS)

# `make bench-stt-libri` — clean per-clip LibriSpeech WER baseline. Build the
# corpus first: FFMPEG=$(NOTO_BENCH_DEPS)/ffmpeg python3 benchmark/dataset/fetch_librispeech_wer.py
.PHONY: bench-stt-libri
bench-stt-libri:
	BENCH_STT_ENGINE=sherpa-parakeet \
	BENCH_STT_MODELS="$(NOTO_BENCH_DEPS)/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8" \
	NOTO_SHERPA_BIN="$(SHERPA_DIR)/bin/sherpa-onnx-offline" \
	NOTO_SHERPA_LIB="$(SHERPA_DIR)/lib" \
	NOTO_FFMPEG="$(NOTO_BENCH_DEPS)/ffmpeg" \
	NOTO_SHERPA_THREADS=$$(nproc) \
	BENCH_LIBRI_DIR="$(CURDIR)/benchmark/dataset/librispeech_wer" \
	$(GO) test -count=1 -v -timeout 1800s -run TestSTTLibriSpeech ./benchmark/stt/

# `make bench-synth` — full attributed pipeline (STT + diarization + merge) on
# the labeled synthetic meetings, reporting WER + DER + cpWER + the attribution
# tax + overlap. Build the corpus first:
#   FFMPEG=$(NOTO_BENCH_DEPS)/ffmpeg python3 benchmark/dataset/build_synthetic_meetings.py
SEG_MODEL := $(NOTO_BENCH_DEPS)/sherpa-onnx-pyannote-segmentation-3-0/model.onnx
EMB_MODEL := $(NOTO_BENCH_DEPS)/diar-embedding.onnx
.PHONY: bench-synth
bench-synth:
	BENCH_STT_ENGINE=sherpa-parakeet \
	BENCH_STT_MODELS="$(NOTO_BENCH_DEPS)/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8" \
	BENCH_DIAR_ENGINE=sherpa-pyannote \
	NOTO_SHERPA_BIN="$(SHERPA_DIR)/bin/sherpa-onnx-offline" \
	NOTO_SHERPA_LIB="$(SHERPA_DIR)/lib" \
	NOTO_FFMPEG="$(NOTO_BENCH_DEPS)/ffmpeg" \
	NOTO_SHERPA_THREADS=$$(nproc) \
	NOTO_SHERPA_SEG_MODEL="$(SEG_MODEL)" \
	NOTO_SHERPA_EMB_MODEL="$(EMB_MODEL)" \
	BENCH_SYNTH_DIR="$(CURDIR)/benchmark/dataset/synthetic_meetings" \
	$(GO) test -count=1 -v -timeout 3600s -run TestSynthAttributedPipeline ./benchmark/e2e/ $(BENCH_FLAGS)

.PHONY: model-status
model-status:
	NOTO_FORCE_IN_PROCESS=1 $(GO) run ./cmd/noto modal status

.PHONY: model-setup
model-setup:
	NOTO_FORCE_IN_PROCESS=1 $(GO) run ./cmd/noto modal setup

# Modal benchmark defaults: one GPU, e2e quick, no duplicate atomic passes.
# Set BENCH_QUICK=0 to run the full atomic diagnostic suite.
# Set BENCH_E2E_BATCH=1 to enable experimental cross-meeting e2e batching.
BENCH_QUICK ?= 1
BENCH_QUICK_FLAG := $(if $(filter 0 false no,$(BENCH_QUICK)),,--quick)
BENCH_BATCH_SIZE_FLAG := $(if $(strip $(BENCH_BATCH_SIZE)),--batch-size "$(BENCH_BATCH_SIZE)",)
BENCH_BATCH_WAIT_FLAG := $(if $(strip $(BENCH_BATCH_WAIT_MS)),--batch-wait-ms "$(BENCH_BATCH_WAIT_MS)",)
BENCH_E2E_BATCH_FLAG := $(if $(filter 1 true yes,$(BENCH_E2E_BATCH)),--e2e-batch,)

# `make model-benchmark` — run the suite on one Modal GPU.
#   BENCH_SUITE   synthetic|ami|all
#   BENCH_PROFILE fast (parallel benchmark, the default) | prod (one stream — the
#                 honest per-user latency on the product's own GPU)
#   BENCH_GPU     L4 (default, cheap/TDD) | L40S | A100-40GB | H100 …
#   BENCH_JOBS    override concurrent meetings (default prod=1, fast=8)
#   BENCH_QUICK=1 run only the chained e2e test (default); 0 = atomic diagnostics too
#   BENCH_E2E_BATCH=1 enable experimental cross-meeting e2e batching
#   BENCH_BATCH_SIZE cap meetings per e2e batch when BENCH_E2E_BATCH=1
#   BENCH_HOURS   cap to ~N hours of audio (whole meetings; 0 = full corpus)
#   BENCH_SEED    fix which meetings are drawn (default 1, matching local benches)
# Start small (L4 + BENCH_HOURS) for quick TDD; scale the GPU up once a profile
# is proven to feed it. `make model-sweep` measures perf+price across GPUs.
.PHONY: model-benchmark
model-benchmark:
	BENCH_QUICK="$(BENCH_QUICK)" .venv-modal/bin/python scripts/modal_benchmark.py run \
		--suite "$${BENCH_SUITE:-synthetic}" --profile "$${BENCH_PROFILE:-prod}" \
		--gpu "$${BENCH_GPU:-L40S}" --hours "$${BENCH_HOURS:-0}" --seed "$${BENCH_SEED:-1}" \
		$${BENCH_JOBS:+--jobs $${BENCH_JOBS}} $(BENCH_QUICK_FLAG) $(BENCH_E2E_BATCH_FLAG) $(BENCH_BATCH_SIZE_FLAG) $(BENCH_BATCH_WAIT_FLAG) \
		$${BENCH_REBUILD_SYNTH:+--rebuild-synth}

# `make model-benchmark-cpu` — same suite on Modal CPU (the worst-case baseline).
.PHONY: model-benchmark-cpu
model-benchmark-cpu:
	BENCH_ACCELERATOR=cpu BENCH_STT_ENGINE=sherpa-parakeet BENCH_DIAR_ENGINE=sherpa-pyannote \
	BENCH_QUICK="$(BENCH_QUICK)" \
	.venv-modal/bin/python scripts/modal_benchmark.py run \
		--suite "$${BENCH_SUITE:-synthetic}" --profile "$${BENCH_PROFILE:-prod}" \
		--hours "$${BENCH_HOURS:-0}" --seed "$${BENCH_SEED:-1}" \
		$${BENCH_JOBS:+--jobs $${BENCH_JOBS}} $(BENCH_QUICK_FLAG) $(BENCH_E2E_BATCH_FLAG) $(BENCH_BATCH_SIZE_FLAG) $(BENCH_BATCH_WAIT_FLAG)

# `make model-sweep` — run the suite across GPUs × profiles and print a perf+price
# table (single-stream "prod" vs max-parallel "fast"). This is the "test once what
# each GPU really costs and how fast it is" comparison. Override the matrix with
# BENCH_SWEEP_GPUS / BENCH_SWEEP_PROFILES. Subsample with BENCH_HOURS to keep cost down.
.PHONY: model-sweep
model-sweep:
	BENCH_QUICK="$(BENCH_QUICK)" .venv-modal/bin/python scripts/modal_benchmark.py sweep \
		--suite "$${BENCH_SUITE:-synthetic}" --hours "$${BENCH_HOURS:-0}" --seed "$${BENCH_SEED:-1}" \
		--gpus "$${BENCH_SWEEP_GPUS:-L4,L40S,A100-40GB}" --profiles "$${BENCH_SWEEP_PROFILES:-prod,fast}" \
		$(BENCH_QUICK_FLAG) $(BENCH_E2E_BATCH_FLAG) $(BENCH_BATCH_SIZE_FLAG) $(BENCH_BATCH_WAIT_FLAG)

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
	@echo '  make bench-metrics # fast loop for the pure scorers (no assets)'
	@echo '  make bench         # full accuracy+speed harness (skips when assets absent)'
	@echo '  make bench HOURS=1 # limit a run to ~1h of audio (whole meetings; SEED=N to fix the draw)'
	@echo '  make run-cli ARGS="list --json"'
	@echo '  make seed          # populate the local store with fixture meetings + people'
	@echo '  make reset         # wipe all local meetings + people'
	@echo '  make which-go      # print the Go toolchain noto will use'
	@echo '  make bootstrap-go  # download Go locally into ./.tools/go (no sudo)'
	@echo ''
	@echo 'Toolchain: $(GO)'
