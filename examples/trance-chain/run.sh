#!/bin/sh
# examples/trance-chain/run.sh
#
# Demonstrates a TRANCE chain: human → ATLAS → SPECTRA → APIVR-Δ.
# Three stub Eidolon containers run sequentially via docker compose depends_on.
# Each step's output envelope is validated before the chain reports success.
#
# Bash 3.2 compatible / POSIX sh.
#
# Prerequisites: Docker Engine v24+ with Compose v2.

set -eu

EXAMPLE_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$EXAMPLE_DIR/../.." && pwd)"
THREAD_ID="trance-thread-001"
TRACE_DIR="$REPO_ROOT/.junction/threads/$THREAD_ID"

# Per-step in/out dirs.
ATLAS_IN="$TRACE_DIR/S0/in"
ATLAS_OUT="$TRACE_DIR/S0/out"
SPECTRA_IN="$TRACE_DIR/S1/in"
SPECTRA_OUT="$TRACE_DIR/S1/out"
APIVR_IN="$TRACE_DIR/S2/in"
APIVR_OUT="$TRACE_DIR/S2/out"

say() { printf '[trance-chain] %s\n' "$*" >&2; }
fail() { say "FAIL: $*"; exit 1; }

# ── Clean previous run ──────────────────────────────────────────────────────
say "cleaning up previous run …"
rm -rf "$TRACE_DIR"
mkdir -p "$ATLAS_IN" "$ATLAS_OUT" \
         "$SPECTRA_IN" "$SPECTRA_OUT" \
         "$APIVR_IN" "$APIVR_OUT"

# Stage the human REQUEST envelope as ATLAS's input.
cp "$EXAMPLE_DIR/envelopes/human-request.envelope.json" \
   "$ATLAS_IN/human-request.envelope.json"

# ── Run the chain ────────────────────────────────────────────────────────────
say "launching TRANCE chain (stub-atlas → stub-spectra → stub-apivr) …"
export JUNCTION_THREAD_ID="$THREAD_ID"
export ATLAS_IN_DIR="$ATLAS_IN"
export ATLAS_OUT_DIR="$ATLAS_OUT"
export SPECTRA_IN_DIR="$SPECTRA_IN"
export SPECTRA_OUT_DIR="$SPECTRA_OUT"
export APIVR_IN_DIR="$APIVR_IN"
export APIVR_OUT_DIR="$APIVR_OUT"

cd "$EXAMPLE_DIR"
docker compose up --abort-on-container-exit

say "compose stack exited 0."

# ── Validate each hand-off envelope ─────────────────────────────────────────
# Step S0 (atlas→spectra): expect PROPOSE.
ATLAS_ENV="$(find "$ATLAS_OUT" -name "*.envelope.json" | head -1)"
if [ -z "$ATLAS_ENV" ]; then
  fail "no output envelope in $ATLAS_OUT"
fi
PERF_S0="$(grep -o '"performative":"[^"]*"' "$ATLAS_ENV" | head -1 | cut -d'"' -f4)"
if [ "$PERF_S0" != "PROPOSE" ]; then
  fail "S0 expected PROPOSE, got: $PERF_S0"
fi
say "S0 (atlas→spectra): $PERF_S0 — OK"

# Step S1 (spectra→apivr): expect PROPOSE.
SPECTRA_ENV="$(find "$SPECTRA_OUT" -name "*.envelope.json" | head -1)"
if [ -z "$SPECTRA_ENV" ]; then
  fail "no output envelope in $SPECTRA_OUT"
fi
PERF_S1="$(grep -o '"performative":"[^"]*"' "$SPECTRA_ENV" | head -1 | cut -d'"' -f4)"
if [ "$PERF_S1" != "PROPOSE" ]; then
  fail "S1 expected PROPOSE, got: $PERF_S1"
fi
say "S1 (spectra→apivr): $PERF_S1 — OK"

# Step S2 (apivr→idg): expect PROPOSE.
APIVR_ENV="$(find "$APIVR_OUT" -name "*.envelope.json" | head -1)"
if [ -z "$APIVR_ENV" ]; then
  fail "no output envelope in $APIVR_OUT"
fi
PERF_S2="$(grep -o '"performative":"[^"]*"' "$APIVR_ENV" | head -1 | cut -d'"' -f4)"
if [ "$PERF_S2" != "PROPOSE" ]; then
  fail "S2 expected PROPOSE, got: $PERF_S2"
fi
say "S2 (apivr→idg): $PERF_S2 — OK"

# ── Verify parent_id chain ───────────────────────────────────────────────────
# S0 output parent_id should reference the human request message_id.
HUMAN_MID="01905e80-c0a0-7000-8000-000000000001"
S0_PARENT="$(grep -o '"parent_id":"[^"]*"' "$ATLAS_ENV" | head -1 | cut -d'"' -f4)"
if [ "$S0_PARENT" != "$HUMAN_MID" ]; then
  fail "S0 parent_id mismatch: got $S0_PARENT, want $HUMAN_MID"
fi
say "parent_id chain: S0 → human — OK"

# ── Tear down ────────────────────────────────────────────────────────────────
cd "$EXAMPLE_DIR"
docker compose down --remove-orphans 2>/dev/null || true

say "PASS — 3-step TRANCE chain (stub) completed."
