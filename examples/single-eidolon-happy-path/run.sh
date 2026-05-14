#!/bin/sh
# examples/single-eidolon-happy-path/run.sh
#
# Demonstrates a one-shot human→ATLAS REQUEST dispatch via the stub container
# executor. Runs the stub-atlas service from docker-compose.yml, verifies the
# trace, and exits 0 on success.
#
# Bash 3.2 compatible / POSIX sh.
#
# Prerequisites: Docker Engine v24+ with Compose v2.

set -eu

EXAMPLE_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$EXAMPLE_DIR/../.." && pwd)"
THREAD_ID="example-thread-001"
TRACE_DIR="$REPO_ROOT/.junction/threads/$THREAD_ID"
STEP_IN="$TRACE_DIR/S0/in"
STEP_OUT="$TRACE_DIR/S0/out"

say() { printf '[single-eidolon-happy-path] %s\n' "$*" >&2; }
fail() { say "FAIL: $*"; exit 1; }

# ── Clean previous run ──────────────────────────────────────────────────────
say "cleaning up previous run …"
rm -rf "$TRACE_DIR"
mkdir -p "$STEP_IN" "$STEP_OUT"

# ── Stage the input envelope ─────────────────────────────────────────────────
cp "$EXAMPLE_DIR/envelopes/input.envelope.json" "$STEP_IN/input.envelope.json"

# ── Run the stub Eidolon container ───────────────────────────────────────────
say "starting stub-atlas container …"
export JUNCTION_IN_DIR="$STEP_IN"
export JUNCTION_OUT_DIR="$STEP_OUT"
export JUNCTION_THREAD_ID="$THREAD_ID"
export JUNCTION_INPUT_ENVELOPE="/junction/io/in/input.envelope.json"

cd "$EXAMPLE_DIR"
docker compose up --abort-on-container-exit --exit-code-from stub-atlas stub-atlas

say "stub-atlas exited 0."

# ── Assert the output envelope was written ───────────────────────────────────
OUT_ENV="$(find "$STEP_OUT" -name "*.envelope.json" | head -1)"
if [ -z "$OUT_ENV" ]; then
  fail "no output envelope found in $STEP_OUT"
fi
say "output envelope: $OUT_ENV"

# ── Assert the trace contains the expected events ────────────────────────────
# The run.sh does not call `junction run` because the junction binary may not
# be installed on the host. Instead it validates the envelope JSON directly,
# confirming the example plumbing works end-to-end.
performative="$(python3 -c "import json,sys; d=json.load(open('$OUT_ENV')); print(d['performative'])" 2>/dev/null || \
  grep -o '"performative":"[^"]*"' "$OUT_ENV" | head -1 | cut -d'"' -f4)"

if [ "$performative" != "INFORM" ]; then
  fail "expected performative INFORM, got: $performative"
fi
say "performative: $performative — OK"

# ── Tear down ────────────────────────────────────────────────────────────────
cd "$EXAMPLE_DIR"
docker compose down --remove-orphans 2>/dev/null || true

say "PASS."
