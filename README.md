# Junction

**ECL v1.0 production harness — dispatch Eidolons with contract-checked hand-offs.**

Junction is a single-binary Go runtime that dispatches Eidolons (ATLAS, SPECTRA, APIVR-Δ, IDG, FORGE, VIGIL) over their installed entry points, treats every hand-off as an ECL v1.0 envelope on disk, and verifies envelopes against the directed-edge contracts in `Rynaro/eidolons-ecl`. It is not a planner — the host LLM (Claude Code) is the planner in the two-phase model. Junction's role: invoke deterministic work, then hand control back to the host for reasoning.

## Quickstart

Follow these 5 steps to dispatch your first TRANCE plan through Junction. The full loop takes ~5 minutes from clean checkout.

### 1. Install Junction

The canonical path is via the Eidolons nexus:

```bash
eidolons harness install
```

This installs Junction into `~/.eidolons/cache/junction@<version>/junction` and resolves the version from GitHub Releases (or from `$JUNCTION_VERSION` if set). To pin a specific version:

```bash
eidolons harness install 0.1.0
```

Verify the install with:

```bash
eidolons harness up
```

This prints the binary path and confirms Docker is reachable. A successful output looks like:

```
junction 0.1.0 (ECL 1.0.0)
commit: ed4f53c
built:  2026-05-15T00:00:00Z
```

### 2. Wire the Eidolons nexus

From your project root, run:

```bash
eidolons harness up
```

This soft-confirms that Junction is installed and Docker is reachable. The binary is already in `$PATH` from step 1 — no further setup is needed for envelope I/O.

### 3. Install the MCP server in your project

```bash
junction mcp install --with-skill
```

This writes two files to your project:
- `.mcp.json`: registers `junction mcp serve` as a stdio MCP server under `mcpServers.junction`
- `.claude/skills/junction/SKILL.md`: the skill card describing the four MCP tools

The entry in `.mcp.json` looks like:

```json
{
  "mcpServers": {
    "junction": {
      "args": ["mcp", "serve"],
      "command": "junction",
      "type": "stdio"
    }
  }
}
```

Run it twice — the second run is a no-op and produces byte-identical output (idempotent).

### 4. Dispatch a TRANCE plan from Claude Code

Start a Claude Code session in your project. The MCP server launches automatically. You now have access to four tools:

| Tool | Status | Purpose |
|---|---|---|
| `harness.plan_from_prompt` | Stubbed in v0.1 | Reserved for future plan generation. Construct a `plan.json` manually (see §7.5 of the Junction spec). |
| `harness.run` | Implemented | Execute a Junction plan. Input: `plan_path` (string). Returns: `thread_id` and `trace_root`. |
| `harness.verify` | Implemented | Verify an ECL envelope against L1–L4 (schema, integrity, edge, performative). Input: `envelope_path`. Returns: `ok` (bool) + `errors` (list). |
| `harness.inject` | Stubbed in v0.1 | Human-in-the-loop envelope injection. Not implemented — returns `ok: false` with a descriptive error. |

**From Claude Code:** paste this canonical prompt to dispatch the quickstart example:

> Use `harness.run` with `plan_path` set to `examples/plan-trance/plan.json`.

This runs a three-step TRANCE chain (ATLAS → SPECTRA → APIVR-Δ) with performatives `REQUEST / INFORM / DELEGATE`.

**Alternative (terminal-only, no Claude Code required):**

```bash
junction run --plan examples/plan-trance/plan.json
```

On success, you see:

```
dispatched to atlas (thread <id>) — trace at <root>
```

### 5. Verify the trace

```bash
junction verify --thread-id <id>
```

Exit code `0` means all four verification levels passed (L1 schema, L2 integrity, L3 edge, L4 performative). Exit codes 65–68 are classified failures:

| Exit code | Meaning |
|---|---|
| `0` | PASS (L1–L4 all pass) |
| `65` | L1 schema validation failed |
| `66` | L2 integrity mismatch |
| `67` | L3 edge not declared in contracts |
| `68` | L4 performative not allowed on declared edge |

On success, the output is:

```
junction verify: PASS — <path>
```

## Subcommand reference

**Built-in commands:**

- `junction run --envelope <path>` — dispatch a single envelope; optionally `--plan <path>` for plan.json-driven chains
  - Flags: `--contracts-dir`, `--trace-dir`, `--enforce {fail-fast|warn|off}`, `--no-container`
- `junction verify --envelope <path>` — verify an ECL envelope against L1–L4 checks
  - Flags: `--contracts-dir`, `--enforce {fail-fast|warn|off}`
- `junction mcp serve` — start the MCP server (stdio subprocess, launched automatically by `.mcp.json`)
- `junction mcp install [--with-skill]` — wire `.mcp.json` and optionally `.claude/skills/junction/SKILL.md`
- `junction mcp uninstall` — remove MCP wiring from the project
- `junction --version` — print version and ECL version

**Phase 2+ stubs (not yet implemented):**

- `junction resume` — resume a paused trace
- `junction trace` — inspect a trace journal
- `junction inject` — inject an envelope into a running trace
- `junction stop` — stop a running harness
- `junction doctor` — diagnose the environment

## Examples in this repo

- `examples/single-eidolon-happy-path/` — simplest envelope round-trip (ATLAS REQUEST, empty artifact)
  - File: `examples/single-eidolon-happy-path/envelopes/input.envelope.json`
  - Run: `junction run --envelope examples/single-eidolon-happy-path/envelopes/input.envelope.json`

- `examples/trance-chain/` — multi-step SPECTRA → APIVR-Δ chain
  - File: `examples/trance-chain/envelopes/human-request.envelope.json`
  - Run: `junction run --envelope examples/trance-chain/envelopes/human-request.envelope.json`

- `examples/plan-trance/` — plan.json-driven TRANCE (three-step chain)
  - File: `examples/plan-trance/plan.json`
  - Run: `junction run --plan examples/plan-trance/plan.json`

## Architecture

Junction is a host runtime; Eidolons are methodologies. The runtime itself executes only deterministic work (envelope I/O, contract checking, Eidolon invocation). The host LLM (Claude Code) is the planner — it decides what step comes next based on the prior step's output.

The two-phase execution model: invoke(assemble) → host LLM reasons → invoke(package). In the first phase, Junction calls the Eidolon's `commands/<verb>.sh` entry point with the input envelope. The Eidolon runs deterministically (typically inside a container) and emits an output envelope. In the second phase, the host LLM reads the output, reasons about the next step, and either dispatches another envelope or terminates the chain. Every emitted artefact carries a SHA-256 integrity tag in its sidecar envelope.

## Specifications

- **EIIS** — Eidolons Install Interface Specification at https://github.com/Rynaro/eidolons-eiis
- **ECL** — Eidolons Communication Layer (envelopes, performatives, contracts) at https://github.com/Rynaro/eidolons-ecl
- **Eidolons nexus** — roster, harness CLI, and methodology cortex at https://github.com/Rynaro/eidolons

## License

Apache-2.0. See `LICENSE`.
