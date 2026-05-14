# Example: single-eidolon-happy-path

Demonstrates a one-shot `human → ATLAS REQUEST` dispatch via the Junction
container executor. A stub ATLAS Eidolon (alpine:3.20 + shell script) reads
the input envelope from the bind-mounted `in/` directory and writes a canned
`INFORM` response envelope to `out/`.

## Prerequisites

- Docker Engine v24+ with the Compose v2 plugin (`docker compose`).
- No Junction binary required — the example exercises the stub Eidolon
  container and validates the output envelope directly.

## Running

```sh
bash examples/single-eidolon-happy-path/run.sh
```

Or from the repo root:

```sh
make demo
```

## What it proves

1. The stub Eidolon container starts, reads its input envelope from
   `/junction/io/in/`, and writes a conformant `INFORM` envelope to
   `/junction/io/out/`.
2. The `run.sh` asserts that the output envelope has performative `INFORM`.
3. The compose stack tears down cleanly after the container exits.

## Directory layout

```
envelopes/
  input.envelope.json     # human→atlas REQUEST fixture (§A1 round-4 shape)
docker-compose.yml        # stub-atlas service (alpine:3.20 + shell entry point)
run.sh                    # POSIX sh; exits 0 on success
README.md                 # this file
```

## Trace layout (written to .junction/threads/example-thread-001/)

```
S0/
  in/   input.envelope.json   (staged from envelopes/)
  out/  scout-report.md.envelope.json  (written by stub-atlas)
```

## Expected output

```
[single-eidolon-happy-path] cleaning up previous run …
[single-eidolon-happy-path] starting stub-atlas container …
[stub-atlas] reading input from /junction/io/in/input.envelope.json
[stub-atlas] wrote /junction/io/out/scout-report.md.envelope.json
[single-eidolon-happy-path] stub-atlas exited 0.
[single-eidolon-happy-path] output envelope: …/scout-report.md.envelope.json
[single-eidolon-happy-path] performative: INFORM — OK
[single-eidolon-happy-path] PASS.
```
