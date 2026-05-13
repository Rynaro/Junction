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
### Deprecated
### Removed
### Fixed
### Security

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
