# syntax=docker/dockerfile:1
#
# Junction — production ECL harness.
#
# Multi-stage:
#   - `dev`      → interactive Go toolchain image; used by `make dev`,
#                  `make test`, `make lint`. Bind-mounts /workspace.
#   - `builder`  → reproducible release build (CGO disabled, static).
#   - `release`  → distroless static, single binary, non-root.
#
# Build a dev image:
#     docker build --target dev -t junction-dev .
#
# Build a release image (consumed by CI / GHCR push):
#     docker build --target release -t ghcr.io/rynaro/junction:dev .
#
# ──────────────────────────────────────────────────────────────────────

FROM golang:1.23-bookworm AS dev

# golangci-lint is pinned at the same major as the project tooling.
# `make lint` calls this binary; do not skip if you bump the Go image.
ARG GOLANGCI_LINT_VERSION=v1.62.2
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
    | sh -s -- -b /usr/local/bin "${GOLANGCI_LINT_VERSION}"

# shellcheck: shell script static analysis — used by `make lint-examples`.
# yq (mikefarah): YAML parse/eval — used by `make lint-examples`.
ARG YQ_VERSION=v4.44.1
RUN apt-get update -qq && apt-get install -y --no-install-recommends shellcheck \
    && rm -rf /var/lib/apt/lists/* \
    && curl -sSfL "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_amd64" \
       -o /usr/local/bin/yq \
    && chmod +x /usr/local/bin/yq

# `$HOME` must be writable for go-build cache + golangci-lint cache.
# Memory note (feedback_home_eacces_signature): when the container UID
# differs from the bind-mount UID, baked $HOME becomes unreadable. We
# point HOME at /tmp (always world-writable) and let `docker compose`
# override with `--user` so the host UID owns the cache volumes.
ENV HOME=/tmp \
    GOCACHE=/tmp/.gocache \
    GOMODCACHE=/tmp/.gomodcache \
    GOLANGCI_LINT_CACHE=/tmp/.golangci-lint \
    CGO_ENABLED=0

# Pre-create the cache dirs world-writable so the named volumes that
# `docker compose` mounts on top of them inherit world-writable
# permissions. Without this, a volume mounted as a non-root host UID
# can't write to the dir (Docker creates the volume root-owned the
# first time).
RUN mkdir -p /tmp/.gocache /tmp/.gomodcache /tmp/.golangci-lint \
    && chmod 1777 /tmp/.gocache /tmp/.gomodcache /tmp/.golangci-lint

WORKDIR /workspace
CMD ["bash"]

# ──────────────────────────────────────────────────────────────────────

FROM golang:1.23-bookworm AS builder

WORKDIR /src

# Module download is cached as a separate layer so source edits don't
# re-fetch the world.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# Reproducible build: pinned timestamp (-buildid= clears the per-build
# nonce, -trimpath strips host paths). Version is injected by CI.
ARG VERSION=0.0.0-dev
ARG BUILD_TIMESTAMP=0
ENV CGO_ENABLED=0 \
    GOFLAGS="-trimpath"
RUN go build \
    -ldflags "-s -w -buildid= -X main.Version=${VERSION}" \
    -o /out/junction \
    ./cmd/junction

# ──────────────────────────────────────────────────────────────────────

FROM gcr.io/distroless/static-debian12:nonroot AS release

LABEL org.opencontainers.image.title="junction" \
      org.opencontainers.image.description="Production ECL harness — junction Eidolons to a runtime with contract-checked hand-offs" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.source="https://github.com/Rynaro/Junction"

COPY --from=builder /out/junction /usr/local/bin/junction

USER nonroot:nonroot
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/junction"]
