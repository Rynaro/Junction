// Package dispatch executes an Eidolon as a subprocess with the ECL
// envelope-on-disk hand-off convention, mirroring the resolution order in
// cli/src/dispatch_eidolon.sh in the nexus.
//
// For the F1 happy path the "Eidolon" can be any configured entrypoint
// binary. The integration point is the Executor interface so that EIIS-aware
// dispatch (resolving ./.eidolons/<name>/commands/) can be substituted in F2
// without changing callers.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Sentinel errors.
var (
	// ErrEntrypointNotFound is returned when the Eidolon entrypoint cannot be
	// located in either the project-local or cache search paths.
	ErrEntrypointNotFound = errors.New("dispatch: entrypoint not found")

	// ErrDispatchFailed wraps subprocess non-zero exit.
	ErrDispatchFailed = errors.New("dispatch: Eidolon subprocess exited non-zero")
)

// Request is the input to a single dispatch call.
type Request struct {
	// StepID is a human-readable step identifier for logging ("S0", "S1", …).
	StepID string

	// Eidolon is the slug of the Eidolon to invoke (e.g. "atlas", "spectra").
	Eidolon string

	// Subcommand is the commands/ script name to invoke (e.g. "scout", "plan").
	// If empty, the Executor may choose a default (implementation-defined).
	Subcommand string

	// EnvelopePath is the absolute path to the input ECL envelope file.
	EnvelopePath string

	// ThreadID propagates ECL_THREAD_ID to the subprocess environment.
	ThreadID string

	// OutputDir is the directory under which the Eidolon should write its
	// output envelope and artifacts. Junction creates this directory before
	// calling Execute.
	OutputDir string

	// Env is a list of extra KEY=VALUE pairs appended to the subprocess env.
	// Host-LLM credentials should be passed here; they are never logged or
	// persisted by Junction.
	Env []string
}

// Result is the output of a successful dispatch call.
type Result struct {
	// StepID echoes Request.StepID.
	StepID string

	// OutputEnvelopePath is the path to the output ECL envelope written by
	// the Eidolon subprocess. May be empty if the Eidolon produced no output
	// (e.g. a terminal REFUSE step).
	OutputEnvelopePath string

	// ExitCode is the subprocess exit code (0 on success).
	ExitCode int

	// ImageRef is the container image reference used for dispatch.
	// Non-empty only when a ContainerExecutor dispatched this step.
	ImageRef string

	// ImageDigest is the resolved image digest (sha256:...).
	// Non-empty only when a ContainerExecutor dispatched this step and the
	// digest was available after the run. Recorded in the trace (spec §5.4).
	ImageDigest string
}

// Executor is the interface for invoking an Eidolon. F1 ships a single
// implementation (ShellExecutor); F2 will add an EIIS-aware variant.
type Executor interface {
	Execute(ctx context.Context, req Request) (Result, error)
}

// ShellExecutor resolves the Eidolon entrypoint by searching:
//  1. <ProjectDir>/.eidolons/<eidolon>/commands/<subcommand>.sh
//  2. <CacheDir>/<eidolon>@<version>/commands/<subcommand>.sh
//
// For the F1 happy path, if EntrypointOverride is set it is used directly,
// bypassing the above resolution. This lets tests wire in any binary without
// needing a full EIIS install layout.
type ShellExecutor struct {
	// ProjectDir is the root of the consumer project (usually cwd).
	ProjectDir string

	// CacheDir is ~/.eidolons/cache or equivalent.
	CacheDir string

	// EntrypointOverride bypasses resolution for the F1 test path.
	// The override receives the same environment and arguments as a real
	// Eidolon would.
	EntrypointOverride string

	// EidolonVersion is used when building the cache fallback path.
	// If empty, the cache fallback is skipped.
	EidolonVersion string
}

// Execute runs the Eidolon subprocess described by req and returns a Result.
// Standard output is captured for envelope-path detection; stderr is
// forwarded to the process's stderr for live log visibility.
func (e *ShellExecutor) Execute(ctx context.Context, req Request) (Result, error) {
	entrypoint, err := e.resolveEntrypoint(req.Eidolon, req.Subcommand)
	if err != nil {
		return Result{}, err
	}

	// Ensure the output directory exists.
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("dispatch: creating output dir: %w", err)
	}

	cmd := exec.CommandContext(ctx, entrypoint)
	cmd.Env = buildEnv(req)
	cmd.Stderr = os.Stderr
	// Stdout is discarded at the process level; the Eidolon communicates via
	// the output envelope on disk (ECL_OUTPUT_DIR). Capture it for
	// diagnostic purposes only.
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			return Result{
				StepID:   req.StepID,
				ExitCode: code,
			}, fmt.Errorf("%w: exit %d", ErrDispatchFailed, code)
		}
		return Result{}, fmt.Errorf("dispatch: running entrypoint: %w", err)
	}

	// Discover the output envelope: look for *.envelope.json in OutputDir.
	outEnv, _ := findOutputEnvelope(req.OutputDir)

	return Result{
		StepID:             req.StepID,
		OutputEnvelopePath: outEnv,
		ExitCode:           0,
	}, nil
}

// resolveEntrypoint returns the path to the Eidolon script to run.
func (e *ShellExecutor) resolveEntrypoint(eidolon, subcommand string) (string, error) {
	if e.EntrypointOverride != "" {
		if _, err := os.Stat(e.EntrypointOverride); err != nil {
			return "", fmt.Errorf("%w: override %q not found", ErrEntrypointNotFound, e.EntrypointOverride)
		}
		return e.EntrypointOverride, nil
	}

	sub := subcommand
	if sub == "" {
		sub = "run"
	}
	scriptName := sub + ".sh"

	// 1. Project-local install: ./.eidolons/<eidolon>/commands/<sub>.sh
	if e.ProjectDir != "" {
		local := filepath.Join(e.ProjectDir, ".eidolons", eidolon, "commands", scriptName)
		if _, err := os.Stat(local); err == nil {
			return local, nil
		}
	}

	// 2. Cache fallback: <CacheDir>/<eidolon>@<version>/commands/<sub>.sh
	if e.CacheDir != "" && e.EidolonVersion != "" {
		cache := filepath.Join(e.CacheDir, eidolon+"@"+e.EidolonVersion, "commands", scriptName)
		if _, err := os.Stat(cache); err == nil {
			return cache, nil
		}
	}

	return "", fmt.Errorf("%w: eidolon=%q subcommand=%q", ErrEntrypointNotFound, eidolon, subcommand)
}

// buildEnv constructs the subprocess environment from the host env plus the
// ECL-specific variables Junction is required to set (spec §5.2).
func buildEnv(req Request) []string {
	env := os.Environ()
	env = append(env,
		"ECL_THREAD_ID="+req.ThreadID,
		"ECL_INPUT_ENVELOPE="+req.EnvelopePath,
		"ECL_OUTPUT_DIR="+req.OutputDir,
		"JUNCTION_VERSION="+junctionVersion(),
	)
	env = append(env, req.Env...)
	return env
}

// junctionVersion returns the build-time version injected via ldflags, or
// "dev" when running in tests or unbuilt binaries.
var junctionVersion = func() string {
	return "dev"
}

// findOutputEnvelope looks for the first *.envelope.json file in dir.
// Returns ("", nil) if none is found — callers may treat an absent output
// envelope as a terminal step (REFUSE, dry-run, etc.).
func findOutputEnvelope(dir string) (string, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.envelope.json"))
	if err != nil || len(entries) == 0 {
		return "", nil
	}
	return entries[0], nil
}
