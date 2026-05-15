// Command junction is the production ECL harness binary.
//
// F1 implements:
//
//	junction run   --envelope <path> [--contracts-dir <path>]
//	junction verify --envelope <path> [--contracts-dir <path>]
//	junction --version
//
// Phase 2+ commands (resume, trace, inject, stop, doctor, mcp) are declared
// as stubs that exit 1 with a "not yet implemented" message so that callers
// learn early about what is coming rather than seeing "unknown command".
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rynaro/Junction/internal/contract"
	"github.com/Rynaro/Junction/internal/contracts"
	"github.com/Rynaro/Junction/internal/dispatch"
	"github.com/Rynaro/Junction/internal/envelope"
	"github.com/Rynaro/Junction/internal/plan"
	"github.com/Rynaro/Junction/internal/trace"
)

// Version is overridden at build time via -ldflags "-X main.Version=...".
var Version = "0.1.0-dev"

// ECLVersion records the vendored ECL spec version Junction was built against.
const ECLVersion = "1.0.0"

// stdout is the writer used for normal (non-error) output. Tests may replace
// this to capture output without spawning a subprocess.
var stdout io.Writer = os.Stdout

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "junction: %s\n", err)
		os.Exit(exitCodeForError(err))
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "--version", "-version", "version":
		fmt.Fprintf(stdout, "junction %s (ECL %s)\n", Version, ECLVersion)
		if Commit != "" {
			short := Commit
			if len(short) > 7 {
				short = short[:7]
			}
			fmt.Fprintf(stdout, "commit: %s\n", short)
		}
		if Date != "" {
			fmt.Fprintf(stdout, "built:  %s\n", Date)
		}
		return nil
	case "--help", "-help", "-h", "help":
		printUsage()
		return nil
	case "run":
		return runCmd(args[1:])
	case "verify":
		return verifyCmd(args[1:])
	case "resume", "trace", "inject", "stop", "doctor", "mcp":
		return fmt.Errorf("%s: not yet implemented in F1 — coming in Phase 2/3", args[0])
	default:
		return fmt.Errorf("unknown command %q — run `junction --help` for usage", args[0])
	}
}

// ---- run subcommand ---------------------------------------------------------

// runConfig holds parsed flags for `junction run`.
type runConfig struct {
	envelopePath string
	contractsDir string
	traceRoot    string
	enforce      string
	// F9-S0: plan.json path.  When set, single-envelope mode is bypassed.
	planPath string
	// F9-S0: --no-container forces ShellExecutor regardless of plan.executor.
	noContainer bool
}

func runCmd(args []string) error {
	cfg, err := parseRunFlags(args)
	if err != nil {
		return err
	}

	// F9-S0: if --plan is provided, delegate to the plan-based dispatch path.
	if cfg.planPath != "" {
		return runPlanCmd(cfg)
	}

	// Legacy single-envelope path (backward compatible).
	if cfg.envelopePath == "" {
		return fmt.Errorf("run: --envelope or --plan is required")
	}

	ctx := context.Background()

	// 1. Read and validate the envelope.
	env, err := loadAndVerifyEnvelope(cfg.envelopePath, cfg.contractsDir, cfg.enforce)
	if err != nil {
		return err
	}

	// 2. Open a trace journal.
	traceRoot := cfg.traceRoot
	if traceRoot == "" {
		traceRoot = trace.DefaultTraceRoot
	}
	journal, err := trace.Open(traceRoot, env.ThreadID)
	if err != nil {
		return fmt.Errorf("run: opening trace journal: %w", err)
	}
	defer journal.Close()

	// 3. Record the inbound envelope.
	ctxTokens := 0
	if env.ContextDelta != nil {
		ctxTokens = env.ContextDelta.TokensUsed
	}
	intMethod := env.Integrity.Method
	parentID := ""
	if env.ParentID != nil {
		parentID = *env.ParentID
	}
	_ = journal.AppendEnvelope(
		env.MessageID, parentID,
		env.From.Eidolon+"@"+env.From.Version,
		env.To.Eidolon+"@"+env.To.Version,
		env.Performative,
		intMethod,
		env.Trace.Model,
		env.Trace.Tier,
		ctxTokens,
	)

	// 4. Record verify pass.
	_ = journal.AppendVerify(env.MessageID, true, true, true, true, "")

	// 5. Dispatch to the receiver Eidolon.
	// F9-S0: the hardcoded ShellExecutor construction (original main.go:131) is
	// replaced by plan.SelectExecutor. The single-envelope path always defaults
	// to shell (no plan.executor to read from); --no-container is accepted but
	// is a no-op here since shell is already the default.
	stepID := "S0"
	outputDir := filepath.Join(traceRoot, env.ThreadID, stepID, "out")

	opts := plan.ExecutorOptions{
		ProjectDir: cwd(),
		CacheDir:   eidolonsCacheDir(),
	}
	// Single-envelope path: default executor is shell for backward compatibility.
	exec := plan.SelectExecutor(plan.ExecutorModeShell, true, opts)
	executorLabel := "shell"

	_ = journal.AppendDispatch(stepID, env.MessageID,
		env.From.Eidolon+"@"+env.From.Version,
		env.To.Eidolon+"@"+env.To.Version,
		executorLabel, "",
	)

	result, dispErr := exec.Execute(ctx, dispatch.Request{
		StepID:       stepID,
		Eidolon:      env.To.Eidolon,
		Subcommand:   subcommandFromEnv(),
		EnvelopePath: cfg.envelopePath,
		ThreadID:     env.ThreadID,
		OutputDir:    outputDir,
	})

	exitCode := 0
	errMsg := ""
	if dispErr != nil {
		exitCode = result.ExitCode
		if exitCode == 0 {
			exitCode = 1
		}
		errMsg = dispErr.Error()
	}
	_ = journal.AppendExit(stepID, exitCode, errMsg)

	if dispErr != nil {
		return fmt.Errorf("run: dispatch: %w", dispErr)
	}

	fmt.Fprintf(stdout, "junction run: dispatched to %s (thread %s) — trace at %s\n",
		env.To.Eidolon, env.ThreadID, journal.Path())
	return nil
}

// runPlanCmd handles `junction run --plan <path>` (F9-S0).
// It parses the plan.json, selects the executor from plan.executor (subject to
// --no-container override), and runs all steps via ChainExecutor.
func runPlanCmd(cfg runConfig) error {
	f, err := os.Open(cfg.planPath)
	if err != nil {
		return &exitError{code: 64, cause: fmt.Errorf("run: opening plan: %w", err)}
	}
	defer f.Close()

	p, err := plan.Parse(f)
	if err != nil {
		return &exitError{code: 64, cause: fmt.Errorf("run: parsing plan: %w", err)}
	}

	// Apply --enforce from CLI; plan.enforce is used only when CLI flag is
	// the default "fail-fast" (i.e. not explicitly overridden).
	enforce := cfg.enforce
	if enforce == "" {
		enforce = p.Enforce
	}

	traceRoot := cfg.traceRoot
	if traceRoot == "" {
		traceRoot = trace.DefaultTraceRoot
	}

	journal, err := trace.Open(traceRoot, p.ThreadID)
	if err != nil {
		return fmt.Errorf("run: opening trace journal: %w", err)
	}
	defer journal.Close()

	opts := plan.ExecutorOptions{
		ProjectDir: cwd(),
		CacheDir:   eidolonsCacheDir(),
	}
	mode := plan.ModeFromString(p.Executor)
	innerExec := plan.SelectExecutor(mode, cfg.noContainer, opts)

	reg, _ := buildRegistry(cfg.contractsDir)
	chain := &dispatch.ChainExecutor{
		Executor:      innerExec,
		Registry:      reg,
		ThreadID:      p.ThreadID,
		BaseOutputDir: filepath.Join(traceRoot, p.ThreadID),
	}

	steps := p.ToChainSteps()
	_, chainErr := chain.Execute(context.Background(), steps)
	if chainErr != nil {
		return fmt.Errorf("run: plan chain: %w", chainErr)
	}

	fmt.Fprintf(os.Stdout, "junction run: plan %s completed — %d step(s), thread %s, trace at %s\n",
		cfg.planPath, len(steps), p.ThreadID, journal.Path())
	return nil
}

// ---- verify subcommand -------------------------------------------------------

// verifyConfig holds parsed flags for `junction verify`.
type verifyConfig struct {
	envelopePath string
	contractsDir string
	enforce      string
}

func verifyCmd(args []string) error {
	cfg, err := parseVerifyFlags(args)
	if err != nil {
		return err
	}
	if cfg.envelopePath == "" {
		return fmt.Errorf("verify: --envelope is required")
	}

	_, err = loadAndVerifyEnvelope(cfg.envelopePath, cfg.contractsDir, cfg.enforce)
	if err != nil {
		fmt.Fprintf(os.Stderr, "junction verify: FAIL — %s\n", err)
		return err
	}

	fmt.Fprintf(stdout, "junction verify: PASS — %s\n", cfg.envelopePath)
	return nil
}

// ---- shared helpers ---------------------------------------------------------

// loadAndVerifyEnvelope reads an envelope file, validates it (L1–L4), and
// returns the parsed Envelope. On any failure it returns a descriptive error
// that encodes the relevant exit code category.
func loadAndVerifyEnvelope(envelopePath, contractsDir, enforce string) (*envelope.Envelope, error) {
	// Read raw bytes for schema validation.
	raw, err := os.ReadFile(envelopePath)
	if err != nil {
		return nil, fmt.Errorf("reading envelope: %w", err)
	}

	// L1 — Schema validation.
	if err := envelope.ValidateBytes(raw); err != nil {
		return nil, &exitError{code: 65, cause: fmt.Errorf("L1 schema: %w", err)}
	}

	// Parse into typed struct.
	env := &envelope.Envelope{}
	if err := json.Unmarshal(raw, env); err != nil {
		return nil, &exitError{code: 65, cause: fmt.Errorf("parsing envelope: %w", err)}
	}

	// L2 — Integrity check (in-document consistency + artifact digest).
	// The artifact path is relative to the envelope's containing directory.
	artDir := filepath.Dir(envelopePath)
	artPath := filepath.Join(artDir, env.Artifact.Path)
	if err := env.VerifyIntegrity(artPath); err != nil {
		return nil, &exitError{code: 66, cause: fmt.Errorf("L2 integrity: %w", err)}
	}

	// L3 + L4 — Contract validation.
	if enforce != "off" {
		reg, loadErrs := buildRegistry(contractsDir)
		if len(loadErrs) > 0 && enforce != "warn" {
			// Log warnings but continue.
			for _, e := range loadErrs {
				fmt.Fprintf(os.Stderr, "junction: contract load warning: %s\n", e)
			}
		}

		checkErr := reg.Check(env.From.Eidolon, env.To.Eidolon, env.Performative)
		if checkErr != nil {
			code := 67
			if isPerformativeError(checkErr) {
				code = 68
			}
			if enforce == "warn" {
				fmt.Fprintf(os.Stderr, "junction: contract warning (--enforce warn): %s\n", checkErr)
			} else {
				return nil, &exitError{code: code, cause: checkErr}
			}
		}
	}

	return env, nil
}

// buildRegistry builds a contract registry from the override dir (if
// provided), the JUNCTION_CONTRACTS_DIR environment variable (if set), or
// falls back to the embedded contracts.
func buildRegistry(contractsDir string) (*contract.Registry, []error) {
	if contractsDir == "" {
		contractsDir = os.Getenv("JUNCTION_CONTRACTS_DIR")
	}
	if contractsDir != "" {
		return contract.NewRegistry(contractsDir)
	}
	return contract.NewRegistryFromFS(contracts.Contracts, ".")
}

// isPerformativeError returns true when the error wraps
// contract.ErrPerformativeNotAllowed.
func isPerformativeError(err error) bool {
	return strings.Contains(err.Error(), "performative not allowed")
}

// exitError is an error that carries a specific exit code category per
// spec §5.5.
type exitError struct {
	code  int
	cause error
}

func (e *exitError) Error() string {
	return e.cause.Error()
}

func (e *exitError) Unwrap() error {
	return e.cause
}

// exitCodeForError maps an error to a process exit code.
func exitCodeForError(err error) int {
	var ee *exitError
	if ok := isExitError(err, &ee); ok {
		return ee.code
	}
	return 1
}

func isExitError(err error, target **exitError) bool {
	ee, ok := err.(*exitError)
	if ok {
		*target = ee
	}
	return ok
}

// ---- flag parsing -----------------------------------------------------------

func parseRunFlags(args []string) (runConfig, error) {
	var cfg runConfig
	cfg.enforce = "fail-fast"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--envelope", "-envelope":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("--envelope requires a value")
			}
			cfg.envelopePath = args[i]
		case "--plan", "-plan":
			// F9-S0: plan.json path.
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("--plan requires a value")
			}
			cfg.planPath = args[i]
		case "--no-container", "-no-container":
			// F9-S0: force ShellExecutor regardless of plan.executor.
			cfg.noContainer = true
		case "--contracts-dir", "-contracts-dir":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("--contracts-dir requires a value")
			}
			cfg.contractsDir = args[i]
		case "--trace-dir", "-trace-dir":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("--trace-dir requires a value")
			}
			cfg.traceRoot = args[i]
		case "--enforce", "-enforce":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("--enforce requires a value")
			}
			cfg.enforce = args[i]
		default:
			return cfg, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return cfg, nil
}

func parseVerifyFlags(args []string) (verifyConfig, error) {
	var cfg verifyConfig
	cfg.enforce = "fail-fast"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--envelope", "-envelope":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("--envelope requires a value")
			}
			cfg.envelopePath = args[i]
		case "--contracts-dir", "-contracts-dir":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("--contracts-dir requires a value")
			}
			cfg.contractsDir = args[i]
		case "--enforce", "-enforce":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("--enforce requires a value")
			}
			cfg.enforce = args[i]
		default:
			return cfg, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return cfg, nil
}

// ---- environment helpers ----------------------------------------------------

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

func eidolonsCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".eidolons", "cache")
}

func subcommandFromEnv() string {
	if s := os.Getenv("JUNCTION_EIDOLON_SUBCOMMAND"); s != "" {
		return s
	}
	return ""
}

// printUsage writes the usage text to stdout.
func printUsage() {
	fmt.Fprintf(stdout, `junction %s — ECL v%s production harness

Usage:
  junction run    --envelope <path> [--contracts-dir <path>] [--trace-dir <path>] [--enforce {fail-fast|warn|off}] [--no-container]
  junction run    --plan <path>     [--contracts-dir <path>] [--trace-dir <path>] [--enforce {fail-fast|warn|off}] [--no-container]
  junction verify --envelope <path> [--contracts-dir <path>] [--enforce {fail-fast|warn|off}]
  junction --version

Exit codes:
  0   success
  64  configuration error (e.g. --enforce off in CI)
  65  schema validation failed (L1)
  66  integrity mismatch (L2)
  67  edge not declared (L3)
  68  performative not allowed (L4)
  1   general error

Env:
  JUNCTION_TRACE_ROOT   override default trace root (.junction/threads/)
  JUNCTION_CONTRACTS_DIR override default embedded contracts

Phase 2+ commands (resume, trace, inject, stop, doctor, mcp) are not yet
implemented in F1.
`, Version, ECLVersion)
}

