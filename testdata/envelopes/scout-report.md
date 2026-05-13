---
eidolon: atlas
version: 1.4.2
kind: scout-report
status: ready-for-spectra
created_at: "2026-05-07T14:00:00Z"
decision_target: "Which lines or functions in cli/install.sh are provably unreachable from any documented entry path?"
scope:
  entrypoints: ["cli/install.sh"]
  modules: ["cli/install.sh"]
  excluded: []
findings_count: 4
gaps_count: 1
confidence_distribution:
  H: 3
  M: 1
  L: 0
evidence_anchors_count: 6
---

# Scout Report — install.sh dead-code audit

## Decision target
Which lines or functions in `cli/install.sh` are provably unreachable from
any documented entry path?

## Findings

- FINDING-001 — `cli/install.sh:142-156` defines `_legacy_wire_mcp()`; no caller exists in the repo. Last referenced in commit `a2c5e91` (removed 2025-09-12). (H)
- FINDING-002 — `cli/install.sh:201-218` `_warn_v0_target()` only triggers when `EIIS_VERSION < 1.0`; floor was raised in v1.1 release. Dead under EIIS ≥ 1.1. (H)
- FINDING-003 — `cli/install.sh:312` constant `OPUS_LEGACY_TAR_URL` referenced once in `_legacy_wire_mcp()` (FINDING-001); becomes orphaned with that removal. (H)
- FINDING-004 — `cli/install.sh:78` flag `--mcp-legacy` accepted by getopt but never branched on. (M)

## Gaps

- GAP-001 — `cli/install.sh:445-452` looks dead but is referenced from a Cursor host wiring path that may be exercised in non-test installs. Manual trace required.

## Scope

Bounded to `cli/install.sh`. No other repo files reference these symbols
based on `git grep` over the workspace.
