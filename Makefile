# Junction — developer entry points.
#
# All Go tooling runs INSIDE the dev container. The host needs only
# Docker (compose v2) and make. No host Go, no host golangci-lint.
#
# Recipes use POSIX /bin/sh syntax; no bashisms — keeps macOS bash 3.2
# users happy when they shell out from make.

SHELL := /bin/sh

# Compose invocation. `docker compose` (v2 plugin) is the convention;
# fall back to `docker-compose` if the user has the legacy v1 binary.
COMPOSE ?= docker compose

# Host UID/GID propagated into the container so bind-mount writes land
# under the user's account, not root.
HOST_UID ?= $(shell id -u)
HOST_GID ?= $(shell id -g)
export HOST_UID HOST_GID

# Release-build args. CI overrides VERSION on the `make build` line.
VERSION ?= 0.0.0-dev
IMAGE   ?= junction:local

.PHONY: help dev shell check lint-examples test test-race lint fmt vet tidy sync-contracts build image clean doctor cache-dirs demo examples

# Pre-create bind-mount source dirs so Docker (running as root on CI)
# doesn't create them root-owned before the container can write to them.
cache-dirs:
	@mkdir -p .gocache .gomodcache .gocache/golangci-lint

help:
	@echo "Junction — developer entry points"
	@echo
	@echo "  make check          go vet + go test -race + golangci-lint + lint-examples (single source of truth for CI)"
	@echo "  make lint-examples  static gate: yq parse + compose config + shellcheck over examples/"
	@echo "  make dev       Open an interactive shell in the dev container"
	@echo "  make test      Run go test ./... inside the dev container"
	@echo "  make test-race Run go test -race ./... inside the dev container"
	@echo "  make lint      Run golangci-lint inside the dev container"
	@echo "  make fmt       gofmt -w ./..."
	@echo "  make vet       go vet ./..."
	@echo "  make tidy      go mod tidy"
	@echo "  make build     Build the release static binary into ./bin/junction"
	@echo "  make image     Build the release container image ($(IMAGE))"
	@echo "  make doctor    Print container / Docker / Go versions"
	@echo "  make demo      Run examples/single-eidolon-happy-path/run.sh"
	@echo "  make examples  Run all examples/<scenario>/run.sh"
	@echo "  make clean     Remove build output (does not touch caches)"

dev: shell

shell: cache-dirs
	$(COMPOSE) run --rm dev bash

# check: single source of truth for lint + test + vet + examples static gate — matches CI verbatim.
# Pre-push hook (.githooks/pre-push) invokes this target.
# CI (.github/workflows/ci.yml) invokes this target in the check job.
check: cache-dirs lint-examples
	$(COMPOSE) run --rm -e CGO_ENABLED=1 dev sh -c "go vet ./... && go test -race ./... && golangci-lint run ./..."

# lint-examples: fast static gate over every examples/<scenario>/.
# Three checks run over all scenarios, aggregating failures (never stops at
# the first broken scenario — every failure is reported before exit).
#
#   1. yq eval parse    — catches malformed YAML in compose files.
#                         Runs inside the dev container (yq installed there).
#   2. docker compose config -q  — variable-interpolation pass; catches the
#                                   unescaped-$VAR class of bug (bare $VAR in
#                                   an entrypoint script gets interpolated at
#                                   parse time; $$VAR is the correct form).
#                                   Required env vars are stubbed to /tmp so
#                                   the :? guard doesn't abort the parse pass.
#                                   docker compose config is a client-side op
#                                   (no daemon contact for parse/interpolate).
#                                   If the daemon is unreachable AND the command
#                                   fails, treat as skip-with-warning; never
#                                   hard-fail on daemon-down.
#   3. shellcheck       — catches shell syntax / quoting bugs in *.sh files.
#                         Runs inside the dev container (shellcheck installed).
#
# yq and shellcheck run inside the dev container; compose config runs on the
# host (client-side operation, no image pull, no container create).
lint-examples:
	@fail=0; \
	daemon_ok=1; \
	if ! docker info >/dev/null 2>&1; then \
	    daemon_ok=0; \
	    echo "examples: docker daemon unreachable, skipping compose config validation"; \
	fi; \
	for d in examples/*/; do \
	    scenario="$$(basename "$$d")"; \
	    \
	    echo "==> lint-examples: $$scenario — yq parse"; \
	    $(COMPOSE) run --rm dev sh -c \
	        'rc=0; for f in /workspace/'"$$d"'docker-compose*.yml /workspace/'"$$d"'compose*.yml; do [ -f "$f" ] || continue; echo "  yq: $f"; yq eval "." "$f" > /dev/null || rc=1; done; exit $rc' \
	    || { echo "FAIL: $$scenario yq parse failed"; fail=1; }; \
	    \
	    echo "==> lint-examples: $$scenario — compose config"; \
	    if [ "$$daemon_ok" = "1" ]; then \
	        ( cd "$$d" \
	          && JUNCTION_IN_DIR=/tmp \
	             JUNCTION_OUT_DIR=/tmp \
	             ATLAS_IN_DIR=/tmp \
	             ATLAS_OUT_DIR=/tmp \
	             SPECTRA_IN_DIR=/tmp \
	             SPECTRA_OUT_DIR=/tmp \
	             APIVR_IN_DIR=/tmp \
	             APIVR_OUT_DIR=/tmp \
	             $(COMPOSE) config -q \
	        ) 2>&1 \
	        || { \
	            if ! docker info >/dev/null 2>&1; then \
	                echo "examples: docker daemon unreachable, skipping compose config validation for $$scenario"; \
	            else \
	                echo "FAIL: $$scenario compose config failed"; fail=1; \
	            fi; \
	        }; \
	    fi; \
	    \
	    echo "==> lint-examples: $$scenario — shellcheck"; \
	    $(COMPOSE) run --rm dev sh -c \
	        'rc=0; for f in /workspace/'"$$d"'*.sh; do [ -f "$f" ] || continue; shellcheck -x -S error "$f" || rc=1; done; exit $rc' \
	    || { echo "FAIL: $$scenario shellcheck failed"; fail=1; }; \
	\
	done; \
	if [ "$$fail" = "1" ]; then \
	    echo "lint-examples: one or more scenarios failed — see above"; \
	    exit 1; \
	fi; \
	echo "lint-examples: all scenarios passed."

test: cache-dirs
	$(COMPOSE) run --rm dev go test ./...

test-race: cache-dirs
	$(COMPOSE) run --rm -e CGO_ENABLED=1 dev go test -race ./...

lint: cache-dirs
	$(COMPOSE) run --rm dev golangci-lint run ./...

fmt: cache-dirs
	$(COMPOSE) run --rm dev gofmt -w .

vet: cache-dirs
	$(COMPOSE) run --rm dev go vet ./...

tidy: cache-dirs
	$(COMPOSE) run --rm dev go mod tidy

# sync-contracts: fetch the pinned eidolons-ecl commit and overwrite
# internal/contracts/*.yaml wholesale. Preserves embed.go, .eclref,
# VERSION, and any _test.go files. Runs inside the dev container.
sync-contracts: cache-dirs
	$(COMPOSE) run --rm dev sh -c '\
	  set -e; \
	  REF=$$(cat internal/contracts/.eclref | tr -d "[:space:]"); \
	  TMPDIR=$$(mktemp -d); \
	  git clone --filter=blob:none https://github.com/Rynaro/eidolons-ecl.git "$$TMPDIR/ecl"; \
	  git -C "$$TMPDIR/ecl" checkout "$$REF"; \
	  cp "$$TMPDIR/ecl/contracts/"*.yaml internal/contracts/; \
	  DATE=$$(date +%Y-%m-%d); \
	  printf "eidolons-ecl %s (%s) synced %s\n" "$$REF" "$$REF" "$$DATE" > internal/contracts/VERSION; \
	  rm -rf "$$TMPDIR"; \
	  echo "sync-contracts: done (eidolons-ecl@$$REF)"; \
	'

# Release build runs in the same dev image (CGO disabled, trimpath).
# Output lands at ./bin/junction on the host via the bind mount.
build:
	mkdir -p bin
	$(COMPOSE) run --rm \
	    -e CGO_ENABLED=0 \
	    -e GOFLAGS=-trimpath \
	    dev \
	    go build \
	      -ldflags "-s -w -buildid= -X main.Version=$(VERSION)" \
	      -o bin/junction \
	      ./cmd/junction

image:
	docker build \
	    --target release \
	    --build-arg VERSION=$(VERSION) \
	    -t $(IMAGE) \
	    .

doctor: cache-dirs
	@echo "docker:"
	@docker version --format '  {{.Server.Version}}' 2>/dev/null || echo "  not available"
	@echo "docker compose:"
	@$(COMPOSE) version --short 2>/dev/null | sed 's/^/  /' || echo "  not available"
	@echo "container go:"
	@$(COMPOSE) run --rm dev go version 2>/dev/null | sed 's/^/  /' || echo "  dev image not built yet"

demo:
	bash examples/single-eidolon-happy-path/run.sh

examples:
	@for d in examples/*/; do \
	    echo "==> $$d"; \
	    bash "$$d/run.sh" || exit 1; \
	done

clean:
	rm -rf bin dist
