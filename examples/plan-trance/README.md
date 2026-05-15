# Example: plan-trance

Demonstrates `junction run --plan plan.json` — the F9-S0 CLI ingestion
path. The plan encodes a 3-step TRANCE chain:

```
human → REQUEST → ATLAS → INFORM → SPECTRA → DELEGATE → APIVR-Δ
```

## File layout

```
examples/plan-trance/
  plan.json          — the plan file consumed by junction run --plan
  README.md          — this file
```

## Running

```sh
# With container executor (requires published GHCR images — v0.1):
junction run --plan examples/plan-trance/plan.json

# With shell executor (--no-container, requires EIIS installs in ./.eidolons/):
junction run --plan examples/plan-trance/plan.json --no-container
```

## Validating the plan schema (requires ajv-cli)

```sh
ajv validate -s schemas/plan.v1.json -d examples/plan-trance/plan.json
```

## What it proves

- `plan.Parse` accepts a 3-step TRANCE plan conforming to spec §7.5.
- `plan.SelectExecutor` picks `ContainerExecutor` when `executor: "container"`.
- `--no-container` forces `ShellExecutor` regardless of `plan.executor`.
- `plan.ToChainSteps` threads `artifact.path` as `InitialEnvelopePath` for
  step S0; steps S1/S2 chain from the previous output automatically.
