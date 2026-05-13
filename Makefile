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

.PHONY: help dev shell test test-race lint fmt vet tidy build image clean doctor cache-dirs

# Pre-create bind-mount source dirs so Docker (running as root on CI)
# doesn't create them root-owned before the container can write to them.
cache-dirs:
	@mkdir -p .gocache .gomodcache .gocache/golangci-lint

help:
	@echo "Junction — developer entry points"
	@echo
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
	@echo "  make clean     Remove build output (does not touch caches)"

dev: shell

shell: cache-dirs
	$(COMPOSE) run --rm dev bash

test: cache-dirs
	$(COMPOSE) run --rm dev go test ./...

test-race: cache-dirs
	$(COMPOSE) run --rm dev go test -race ./...

lint: cache-dirs
	$(COMPOSE) run --rm dev golangci-lint run ./...

fmt: cache-dirs
	$(COMPOSE) run --rm dev gofmt -w .

vet: cache-dirs
	$(COMPOSE) run --rm dev go vet ./...

tidy: cache-dirs
	$(COMPOSE) run --rm dev go mod tidy

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

clean:
	rm -rf bin dist
