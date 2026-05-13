# APIVR-Δ Reflection — F1 Envelope I/O + Sequential Dispatch

**Branch:** feature/F1-envelope-io  
**Final commit:** baed005  
**Date:** 2026-05-13  

## What was built

Five packages implementing Feature F1 of the Junction harness:

| Package | Stories | Notes |
|---|---|---|
| `internal/schemas/` | S1 data layer | Vendored ECL v1.0 schemas via go:embed |
| `internal/contracts/` | S2 data layer | 18 Eidolon-to-Eidolon + 6 human-to-* contracts via go:embed |
| `internal/envelope/` | S1 | Read/validate/emit + SHA-256 integrity |
| `internal/contract/` | S2 | Registry + L3/L4 checks |
| `internal/trace/` | S3 | Append-only JSONL journal, fsync-safe |
| `internal/dispatch/` | S4 | ShellExecutor with env-var propagation |
| `cmd/junction/main.go` | CLI | `junction run` + `junction verify` |

## What worked

- The ECL schemas and contract YAMLs provided a crisp, complete source of
  truth. Anchoring to them first (Analyze phase) before writing any code
  kept the struct design stable — no retries on type layout.
- `go:embed` for both schemas and contracts makes the binary genuinely
  self-contained, which the spec requires (§8.3 "no network").
- The `Executor` interface in dispatch cleanly separates the F1 test path
  (EntrypointOverride) from the EIIS-aware F2 path without any dead code.
- Test-first anchoring to the GIVEN/WHEN/THEN stories meant every acceptance
  criterion had a named test before the implementation was written.

## Spec gaps / open questions surfaced

1. **ECL schema uses ECMA-262 lookahead regexes** (`^(?!/)(?!.*\\.\\.).+$`)
   which Go's `regexp` package rejects. The vendored schema was patched to
   `^[^/].+$` and the `..` guard was moved to `Validate()` in code.
   **Recommendation:** upstream should publish a Go-compatible variant of the
   schema or document this as a known incompatibility in the ECL spec.
   The `context-delta.v1.json` schema has the same issue (patched here too).

2. **`envelope_version: "2.0"` in the vigil escalation example** — the
   `apivr-vigil-escalation` example uses `envelope_version: "2.0"` and an
   `ise` extension object not present in the v1.0 schema. F1 does not
   validate those envelopes — they will fail L1 schema validation if passed
   to `junction verify`. This is expected (they're v2 spec artefacts), but
   a future `--ecl-version` flag should select the right schema.

3. **S19a upstream status** — the six `human-to-<eidolon>.yaml` contracts
   are authored here and vendored into the binary, but they don't yet exist
   in `Rynaro/eidolons-ecl`. Junction v0.1 gates on these landing upstream
   (spec G-S9). Parent should open a PR on eidolons-ecl with those six files.

4. **`cmd/junction` has 0% test coverage** — the CLI glue is thin, but an
   integration test that runs the binary against the three fixture envelopes
   would give a clean end-to-end signal. Deferred to F2 (integration test
   harness not in F1 scope).

5. **`trace.Event.TS` format** — the existing ECL trace JSONL uses RFC 3339
   (`"2026-05-12T00:12:14Z"`) which matches the spec. The `trace.now` var is
   a hook for test overrides; production uses `time.Now().UTC()`.

## Deviations from brief

- None material. The `Executor` interface is slightly more explicit than
  "make the integration point an interface" — that felt load-bearing for F2
  so I kept it.
- The `ValidateBytes` function is additive (not in S1's action plan) but
  covers a real need: verifying raw JSON without deserialising to the Go
  struct, which is important for the v2 envelope examples.

## F2 items logged

- Multi-Eidolon TRANCE chain (S5) — parallel fan-out with worktree isolation.
- EIIS-aware dispatch (S3 extension) — resolve `./.eidolons/<name>/commands/`.
- `junction resume <thread_id>` (S6) — reopen trace, skip completed steps.
- Integration tests for `cmd/junction` (CLI-level E2E).
- `junction doctor` subcommand.
- `junction trace` / `junction inject` / `junction stop` stubs → real impl.
