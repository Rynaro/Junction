# Junction

> Junction Eidolons to a runtime with contract-checked hand-offs.

**Junction** is the production [ECL](https://github.com/Rynaro/eidolons-ecl)
harness — a single-binary Go orchestrator that dispatches Eidolons
(ATLAS, SPECTRA, APIVR-Δ, IDG, FORGE, VIGIL) over their installed
`commands/*.sh` entry points, treats every hand-off as an ECL v1.0
envelope on disk, verifies envelopes against the directed-edge contracts
in `Rynaro/eidolons-ecl/contracts/`, and persists a deterministic trace
journal that any session can resume.

The name is a nod to the Final Fantasy VIII Junction system: in Junction
you bind each character (Eidolon) to a Guardian Force (methodology) and
then to elemental + stat slots (ECL contracts) before the party (the
chain) is allowed to act. Every link is declared, every link is checked.

## Status

**Alpha — Phase 0 bootstrap (2026-05-13).** The binary in this commit
prints a banner and exits. The full spec is at round 3, 88% confidence,
auto-proceed: see
[`Rynaro/eidolons/.spectra/plans/2026-05-13-ecl-harness.md`](https://github.com/Rynaro/eidolons/blob/main/.spectra/plans/2026-05-13-ecl-harness.md).

Roadmap:

| Phase | Scope | Target |
|---|---|---|
| **0 — Bootstrap** | Repo skeleton, Go toolchain, CI, license. _(this commit)_ | 2026-05-13 |
| 1 — Envelope I/O + sequential dispatch | Round-trip envelopes against the ECL fixtures; happy-path SPECTRA → APIVR-Δ chain. | v0.1.0 |
| 2 — TRANCE chains, concurrency, nexus `eidolons harness` family | Parallel fan-out with worktree isolation, evaluator-optimizer, resume. | v0.2.0 |
| 3 — Telemetry, MCP, Claude Code integration | OTLP, `junction mcp serve`, `--with-skill`. | v0.3.0 |
| 4 — Production hardening + GA | Per-step Docker sandbox, cosign-signed releases, reproducible builds. | v1.0.0 |

## What it is

Junction is **not** an Eidolon. It is the runtime that runs them.

| Project | Role |
|---|---|
| [`Rynaro/eidolons-eiis`](https://github.com/Rynaro/eidolons-eiis) | The install contract every Eidolon satisfies. |
| [`Rynaro/eidolons-ecl`](https://github.com/Rynaro/eidolons-ecl) | The wire format and hand-off contracts. Sibling spec to EIIS. |
| [`Rynaro/{ATLAS,SPECTRA,APIVR-Delta,IDG,FORGE,VIGIL}`](https://github.com/Rynaro) | The Eidolon methodology repos. |
| [`Rynaro/eidolons`](https://github.com/Rynaro/eidolons) | The nexus — roster, CLI, methodology cortex. |
| **Junction (this repo)** | The runtime that dispatches Eidolons and verifies every envelope. |

What makes Junction different from LangGraph / AutoGen / CrewAI / Mastra
/ Swarm / LlamaIndex Workflows: **contract-first hand-offs on every
edge — including the human's**. Every Eidolon-to-Eidolon edge has a
machine-readable YAML contract; every human-to-Eidolon edge gets one
too in Phase 1 (`F-HUMAN-EDGE`, additive, no ECL spec bump). Every
emitted artefact carries a sidecar envelope with a SHA-256 integrity
tag, and refusals (`ATLAS doesn't write`, `SPECTRA doesn't implement`,
`IDG doesn't retrieve`, `FORGE doesn't tool`, `VIGIL doesn't auto-apply
patches`) are enforced at dispatch.

## Install

> **Coming in v0.1.** Phase 0 does not yet ship a binary or installer.

The intended install paths once v0.1 lands:

```sh
# curl-pipe (matches the nexus pattern)
curl -fsSL https://raw.githubusercontent.com/Rynaro/Junction/main/install.sh | bash

# Or via the nexus, as a seamless adoption:
eidolons harness install

# Or with go install
go install github.com/Rynaro/Junction/cmd/junction@latest

# Or via the GHCR container
docker pull ghcr.io/rynaro/junction:latest
```

## Quickstart

> **Coming in v0.1.** Once envelope I/O and sequential dispatch land,
> the canonical flow will be:
>
> ```sh
> # Inside a project that has Eidolons installed via `eidolons init`:
> junction run --plan plan.json
> junction trace --thread <thread_id>
> junction verify .junction/threads/<thread_id>/
> ```

## Development

All Go tooling runs **inside the dev container**. The only host
prerequisites are Docker (with Compose v2) and `make`.

```sh
# Build the dev image and open a shell inside it
make dev

# Run the test suite
make test

# Run the linter
make lint

# Build the release binary (lands at ./bin/junction)
make build

# Build the release container image (gcr.io/distroless/static-debian12)
make image

# Print the toolchain versions Junction sees inside the container
make doctor
```

See `Makefile` for the full target list. The container forces
`HOME=/tmp` and bind-mounts the working tree as the host UID so the
Go build / module / golangci-lint caches don't end up root-owned.

## License

Apache-2.0. See [`LICENSE`](LICENSE).
