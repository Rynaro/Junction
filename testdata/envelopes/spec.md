---
eidolon: spectra
version: 4.2.11
kind: spec
status: ready-for-apivr
created_at: "2026-05-07T14:30:00Z"
target_repos: ["eidolons"]
stories_count: 2
validation_gates_count: 3
confidence: 0.92
decisions_resolved_at: "2026-05-07T14:28:00Z"
---

# Spec — install.sh dead-code cleanup

## Stories

### Story 1 — Remove `_legacy_wire_mcp()` and its dependents

**GIVEN** ATLAS scout-report (FINDING-001, FINDING-003) confirms
`_legacy_wire_mcp()` is unreachable and `OPUS_LEGACY_TAR_URL` is referenced
only by it,

**WHEN** `cli/install.sh:142-156` and `cli/install.sh:312` are removed,

**THEN** `bash cli/install.sh --help` SHALL exit 0 with unchanged output,
and `bats cli/tests/install.bats` SHALL pass.

### Story 2 — Remove `_warn_v0_target()` and the `--mcp-legacy` getopt branch

**GIVEN** EIIS_VERSION floor is 1.1 and FINDING-002, FINDING-004 confirm
both targets dead,

**WHEN** `cli/install.sh:201-218` and the `--mcp-legacy` getopt entry on
`cli/install.sh:78` are removed,

**THEN** `bash cli/install.sh --help` SHALL not list `--mcp-legacy`, and
`shellcheck -x -S error cli/install.sh` SHALL exit 0.

## Validation gates

- VG-1 — `bats cli/tests/install.bats` exits 0
- VG-2 — `shellcheck -x -S error cli/install.sh` exits 0
- VG-3 — `bash cli/install.sh --dry-run` against the skeleton fixture
  produces byte-identical output to the pre-cleanup baseline.

## Agent hints

- Use the APIVR-Δ Internal-First P0: confirm no test currently exercises
  the removed paths before deleting.
- GAP-001 from the scout report covers a non-overlapping concern; it is
  out of scope for this spec.
