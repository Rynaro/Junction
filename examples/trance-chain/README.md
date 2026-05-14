# Example: trance-chain

Demonstrates a TRANCE chain: `human → ATLAS → SPECTRA → APIVR-Δ` running as
a Docker Compose v2 stack with sequential ordering via `depends_on:
condition: service_completed_successfully`.

Each Eidolon is a stub container (alpine:3.20 + shell script) that reads its
input envelope from a bind-mounted `in/` directory and writes a canned output
envelope to its `out/` directory. The `run.sh` validates each hand-off
envelope's performative and the `parent_id` chain.

## Prerequisites

- Docker Engine v24+ with the Compose v2 plugin (`docker compose`).
- No Junction binary required.

## Running

```sh
bash examples/trance-chain/run.sh
```

Or from the repo root:

```sh
make examples
```

## What it proves

1. `stub-atlas` starts, reads the human REQUEST envelope, and emits a
   `PROPOSE` scout-report envelope (atlas→spectra contract).
2. `stub-spectra` starts only after `stub-atlas` exits 0 (`depends_on`),
   reads the scout-report envelope, and emits a `PROPOSE` spec envelope
   (spectra→apivr contract).
3. `stub-apivr` starts only after `stub-spectra` exits 0, reads the spec
   envelope, and emits a `PROPOSE` completion envelope (apivr→idg contract).
4. Each output envelope's `parent_id` chains correctly through the thread.

## Directory layout

```
envelopes/
  human-request.envelope.json   # human→atlas REQUEST fixture
docker-compose.yml              # three stub services with depends_on ordering
run.sh                          # POSIX sh; validates each hand-off; exits 0 on pass
README.md                       # this file
```

## Trace layout (written to .junction/threads/trance-thread-001/)

```
S0/
  in/    human-request.envelope.json
  out/   scout-report.md.envelope.json    ← emitted by stub-atlas
S1/
  in/    (empty — stub-spectra reads from SPECTRA_IN_DIR)
  out/   spec.md.envelope.json            ← emitted by stub-spectra
S2/
  in/    (empty — stub-apivr reads from APIVR_IN_DIR)
  out/   completion.md.envelope.json      ← emitted by stub-apivr
```

## Expected output

```
[trance-chain] cleaning up previous run …
[trance-chain] launching TRANCE chain (stub-atlas → stub-spectra → stub-apivr) …
[stub-atlas] emitting scout-report envelope …
[stub-spectra] emitting spec envelope …
[stub-apivr] emitting completion envelope …
[trance-chain] compose stack exited 0.
[trance-chain] S0 (atlas→spectra): PROPOSE — OK
[trance-chain] S1 (spectra→apivr): PROPOSE — OK
[trance-chain] S2 (apivr→idg): PROPOSE — OK
[trance-chain] parent_id chain: S0 → human — OK
[trance-chain] PASS — 3-step TRANCE chain (stub) completed.
```
