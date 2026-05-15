# Changelog

All notable changes to Junction are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Compatibility is tracked separately for the ECL spec version Junction
targets — see `README.md` and the spec at
`Rynaro/eidolons/.spectra/plans/2026-05-13-ecl-harness.md` §8.6.

## [Unreleased]

### Added

### Changed
- L1 schema accepts ECL v1.0 and v2.0 envelopes per ECL §7.3 compat window;
  L3/L4 remain the semantic gate. Resolves OQ-23 Friction-5 from the v0.1.0
  README walk. `.eclref` stays pinned to v1.0; this is a v1.0-and-v2.0-tolerant
  reader posture, not a v2.0-native upgrade.

### Fixed

### Security

### Notes

## [0.1.0] - 2026-05-15

### Added
- `junction run` and `junction verify` CLI commands with four-level envelope validation (L1 schema, L2 integrity, L3 edge, L4 performative).
- ECL v1.0 envelope read/decode/validate/emit surface in `internal/envelope/`:
  - Typed Go structs for `ecl-envelope.v1.json` (Draft 2020-12).
  - JSON Schema validation via jsonschema/v5 against the vendored schema.
  - SHA-256 integrity verification (in-document consistency + artifact digest).
- Hand-off contract registry in `internal/contract/`:
  - Directed-edge contract lookup (sender/receiver + performative).
  - Required-payload field validation per spec.
  - Closed-set performative gate.
- Sequential TRANCE-chain dispatch (`ChainExecutor` in `internal/dispatch/chain.go`):
  - Multi-step Eidolon pipelines with per-step contract validation.
- Concurrent fan-out dispatch (`FanoutExecutor` in `internal/dispatch/fanout.go`):
  - Worker pool with configurable concurrency cap (default `runtime.NumCPU()`, hard limit 5).
  - Isolated output directories per branch.
  - `JUNCTION_MAX_CONCURRENCY` env override.
- Container-per-Eidolon executor (`ContainerExecutor` in `internal/dispatch/container.go`):
  - Docker-based dispatch with image resolution: `JUNCTION_EIDOLON_IMAGE_<EIDOLON>` override, then `ghcr.io/rynaro/<eidolon>:<version>` fallback.
- Append-only JSONL trace journal in `internal/trace/`:
  - Thread-per-invocation at `.junction/threads/<thread_id>.jsonl`.
  - Per-step trace events (envelope, verify, dispatch, exit).
- Pinned upstream contract sync mechanism in `internal/contracts/`:
  - `.eclref` pins the `eidolons-ecl` upstream commit.
  - `make sync-contracts` — idempotent blobless clone and wholesale contract copy.
  - All 24 contracts (6 human + 18 Eidolon) vendored with provenance tracking.
- Example stacks: `examples/single-eidolon-happy-path/` and `examples/trance-chain/` with stub Eidolon containers, fixture envelopes, and end-to-end test scripts.
- Static lint gate for examples: `make lint-examples` validates YAML, Docker Compose interpolation, and shell syntax across all scenarios (aggregates all failures before exit).
- `.githooks/pre-push` hook runs `make check` (go vet + go test -race + golangci-lint + lint-examples) before allowing push.
- Exit codes per spec §5.5:
  - 65 (L1 schema validation failed), 66 (L2 integrity mismatch), 67 (L3 edge not declared), 68 (L4 performative not allowed).
  - 71 (image pull failed), 72 (Docker daemon unreachable).

### Changed
- `--version` flag now reports ECL spec version alongside binary version (e.g., `junction 0.1.0-dev (ECL 1.0.0)`).
- `make check` now depends on `lint-examples` — static gate runs ahead of Go toolchain.
- Dockerfile dev stage adds `shellcheck` and `yq` (pinned versions).

### Fixed
- `JUNCTION_CONTRACTS_DIR` environment variable — previously advertised in help text but unimplemented — now wired as `--contracts-dir` fallback and honored by contract registry loader.

### Security
- SHA-256 integrity verification (artifact digest validation against envelope payload).
- Contract drift-check in `.github/workflows/ci.yml` (`contracts-drift` job) — fails if vendored contracts diverge from the pinned upstream ref.

### Notes
- F1 + F2 together constitute the bulk of the 0.1.0 release; Phase 2/3 stub commands (resume, trace, inject, stop, doctor, mcp) exit 1 with "not yet implemented" to surface the roadmap early.
- 44+ unit tests covering envelope I/O, integrity, contract validation, chain/fanout dispatch, and container integration.

## [0.0.0] - 2026-05-13 — Bootstrap

### Added
- Initial repository scaffold (Phase 0 per spec §9.1):
  - Go module `github.com/Rynaro/Junction` on Go 1.23.
  - `cmd/junction/main.go` stub printing version.
  - Empty internal packages (`envelope`, `dispatch`, `trace`, `contract`)
    each with a one-line `doc.go`.
  - Placeholder test in `internal/envelope/` so `go test ./...` exits 0.
  - Multi-stage `Dockerfile` (dev + builder + distroless release).
  - `docker-compose.yml` for the dev loop with host-UID bind mounts and
    `HOME=/tmp` to avoid the baked-`$HOME` EACCES trap.
  - `Makefile` wrapping all Go invocations inside the dev container.
  - `.github/workflows/ci.yml` running `make test` and `make lint`
    containerized, plus a release-image build job.
  - Apache-2.0 `LICENSE`, `.gitignore`, `.dockerignore`, this changelog.

### Notes
- This release is **bootstrap only**: the binary prints a banner and
  exits. F1 (envelope I/O + sequential dispatch) lands in 0.1.0 per
  spec §9.2.
- Junction targets ECL spec v1.0.x for the 0.x series per spec §8.6.
