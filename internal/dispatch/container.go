// Package dispatch — ContainerExecutor implementation (S2.1, round 4; F10-S1, round 6).
//
// ContainerExecutor is the primary Executor from F2 onward. It runs each
// Eidolon in its own container via raw `docker run`.
//
// Round 6 (F10-S1): the single-invocation model is generalised to a two-phase
// orchestration loop:
//
//  1. invoke(assemble)  — JUNCTION_PHASE=assemble; container writes prompt-bundle.json
//  2. host-LLM reasoning step (injectable seam: ReasoningStep field)
//  3. invoke(package)   — JUNCTION_PHASE=package; container writes *.envelope.json
//
// The docker run invocation line (flags + mounts) is UNCHANGED — JUNCTION_PHASE
// rides the existing req.Env channel. What changed is the orchestration around
// the invocation.
//
// Image resolution order (spec §5.9 / OQ-17):
//  1. Env var JUNCTION_EIDOLON_IMAGE_<EIDOLON> (upper-cased slug, hyphens→underscores)
//  2. ghcr.io/rynaro/<lowercased-eidolon>:<version>
//  3. If image not available (docker pull fails): emit IMAGE_NOT_AVAILABLE error (exit 71).
//     Build-from-source (OQ-17 full fallback) is deferred to v0.2.
//
// Docker is invoked via a CommandRunner abstraction so unit tests can stub
// the `docker` binary without requiring a real daemon.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Rynaro/Junction/internal/trace"
)

// Sentinel errors for container-specific failures (spec §5.5 round 4).
var (
	// ErrImageNotAvailable is returned when the Eidolon container image cannot
	// be pulled from the registry and no local-build fallback is configured.
	// Maps to exit code 71.
	ErrImageNotAvailable = errors.New("container: Eidolon image not available (exit 71)")

	// ErrDockerUnreachable is returned when the Docker daemon cannot be
	// contacted at the start of a run. Maps to exit code 72.
	ErrDockerUnreachable = errors.New("container: Docker daemon unreachable (exit 72)")
)

// CommandRunner is the abstraction over the docker CLI. Tests inject a stub;
// production code uses the default realRunner.
type CommandRunner interface {
	// Run executes the command described by name + args with the given
	// environment appended to the process environment.
	// stdout and stderr are captured and returned.
	Run(ctx context.Context, env []string, name string, args ...string) (stdout, stderr string, err error)
}

// realRunner is the production CommandRunner that execs docker directly.
type realRunner struct{}

func (realRunner) Run(ctx context.Context, env []string, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	outB, err := cmd.Output()
	var exitErr *exec.ExitError
	stderr := ""
	if errors.As(err, &exitErr) {
		stderr = string(exitErr.Stderr)
	}
	return strings.TrimSpace(string(outB)), stderr, err
}

// ReasoningStepFunc is the injectable seam for the host-LLM reasoning step in
// the two-phase ContainerExecutor orchestration (F10-S1, round 6).
//
// The function is called after the assemble container invocation completes.
// It receives:
//   - ctx: the request context (honours cancellation).
//   - stepID: the step identifier, for logging.
//   - inDir: the host path to the step's in/ bind-mount directory.
//   - outDir: the host path to the step's out/ bind-mount directory.
//
// The assemble phase writes prompt-bundle.json to outDir. The function MUST
// write reasoning.json into inDir before returning. Junction then runs the
// package container invocation with the in/ directory containing reasoning.json.
//
// NG17: no real LLM call is made inside this codebase. In tests a canned
// reasoning.json is written directly. In production the MCP stdio server
// (junction mcp serve, §7.4) is the natural host — OQ-22 covers headless-CI.
type ReasoningStepFunc func(ctx context.Context, stepID, inDir, outDir string) error

// ContainerExecutor runs each Eidolon in its own container via `docker run`.
// It implements the Executor interface from F1 and is the primary executor
// from F2 onward. Use ShellExecutor (--no-container) as fallback.
//
// Round 6 (F10-S1): Execute now runs a two-phase orchestration loop
// (assemble → host-LLM reasoning → package). The ReasoningStep field is the
// injectable seam for the host-LLM step; tests supply a canned implementation.
type ContainerExecutor struct {
	// Runner is the docker CLI adapter. If nil, uses the real docker binary.
	Runner CommandRunner

	// EidolonVersion is the version tag used when building the image reference
	// (e.g. "1.5.2"). Must be set; an empty value causes resolveImage to return
	// an error immediately rather than falling back to ":latest".
	EidolonVersion string

	// SkipDaemonProbe, when true, skips the daemon reachability probe. Useful
	// in tests that don't have a real Docker daemon.
	SkipDaemonProbe bool

	// ReasoningStep is the injectable host-LLM reasoning step between the
	// assemble and package container invocations. If nil, a no-op pass-through
	// is used (useful for legacy single-phase tests that pre-date F10-S1, and
	// for environments where no reasoning provider is configured yet).
	//
	// Production callers (MCP server, §7.4) supply a real implementation that
	// reads prompt-bundle.json and writes reasoning.json. Tests supply a canned
	// function that copies a fixture reasoning.json. NG17 forbids any direct
	// LLM API call here.
	ReasoningStep ReasoningStepFunc

	// Journal is an optional trace journal. When non-nil, the executor records
	// per-phase dispatch events (kind="dispatch" with phase="assemble"|"package")
	// and a host_reasoning event between the two phases. The caller is
	// responsible for opening and closing the journal.
	Journal *trace.Journal
}

// runner returns the configured runner or the production default.
func (c *ContainerExecutor) runner() CommandRunner {
	if c.Runner != nil {
		return c.Runner
	}
	return realRunner{}
}

// Execute dispatches req using the two-phase orchestration loop (F10-S1):
//
//  1. invoke(assemble) — container writes prompt-bundle.json to out/
//  2. ReasoningStep    — host-LLM reads prompt-bundle.json, writes reasoning.json to in/
//  3. invoke(package)  — container reads reasoning.json, writes *.envelope.json to out/
//
// The docker run invocation line (flags + mounts) is IDENTICAL in both phases.
// JUNCTION_PHASE ("assemble" or "package") is injected via req.Env — the
// channel that was already permitted before F10-S1.
func (c *ContainerExecutor) Execute(ctx context.Context, req Request) (Result, error) {
	if !c.SkipDaemonProbe {
		if err := c.probeDaemon(ctx); err != nil {
			return Result{}, err
		}
	}

	// Per-step version overrides the executor-level default.
	version := req.EidolonVersion
	if version == "" {
		version = c.EidolonVersion
	}
	image, err := c.resolveImage(ctx, req.Eidolon, version)
	if err != nil {
		return Result{}, err
	}

	// Ensure in/out directories exist on the host.
	inDir := filepath.Join(req.OutputDir, "..", "in")
	inDir = filepath.Clean(inDir)
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("container: creating in dir: %w", err)
	}
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("container: creating out dir: %w", err)
	}

	// Copy the input envelope into the in/ dir if it's not already there.
	containerEnvPath := ""
	if req.EnvelopePath != "" {
		containerEnvPath = "/junction/io/in/" + filepath.Base(req.EnvelopePath)
		dst := filepath.Join(inDir, filepath.Base(req.EnvelopePath))
		if _, statErr := os.Stat(dst); errors.Is(statErr, os.ErrNotExist) {
			if copyErr := copyFile(req.EnvelopePath, dst); copyErr != nil {
				return Result{}, fmt.Errorf("container: staging envelope: %w", copyErr)
			}
		}
	}

	absIn, err := filepath.Abs(inDir)
	if err != nil {
		return Result{}, fmt.Errorf("container: abs in dir: %w", err)
	}
	absOut, err := filepath.Abs(req.OutputDir)
	if err != nil {
		return Result{}, fmt.Errorf("container: abs out dir: %w", err)
	}

	// buildDockerArgs constructs the docker run invocation for a given phase.
	// The invocation line (flags + mounts) is UNCHANGED from S2.1; only
	// JUNCTION_PHASE is added to the env entries — it rides req.Env just like
	// any other caller-supplied env var.
	buildDockerArgs := func(phase string, extraEnv []string) []string {
		args := []string{
			"run", "--rm",
			"--network", "none",
			"--read-only",
			"--tmpfs", "/tmp",
			"--cap-drop", "ALL",
			"-v", absIn + ":/junction/io/in:ro",
			"-v", absOut + ":/junction/io/out:rw",
			"-e", "JUNCTION_THREAD_ID=" + req.ThreadID,
			"-e", "JUNCTION_INPUT_ENVELOPE=" + containerEnvPath,
			"-e", "ECL_THREAD_ID=" + req.ThreadID,
			"-e", "ECL_INPUT_ENVELOPE=" + containerEnvPath,
			"-e", "ECL_OUTPUT_DIR=/junction/io/out",
			"-e", "JUNCTION_VERSION=" + junctionVersion(),
			"-e", "JUNCTION_PHASE=" + phase,
		}
		for _, e := range extraEnv {
			args = append(args, "-e", e)
		}
		args = append(args, image)
		return args
	}

	// ── Phase 1: assemble ────────────────────────────────────────────────────
	assembleArgs := buildDockerArgs("assemble", req.Env)

	if c.Journal != nil {
		_ = c.Journal.AppendDispatchPhase(req.StepID, "", "", "", "container", "", "assemble")
	}

	_, _, assembleErr := c.runner().Run(ctx, nil, "docker", assembleArgs...)
	if assembleErr != nil {
		exitCode := exitCodeFrom(assembleErr)
		outEnv, _ := findOutputEnvelope(req.OutputDir)
		return Result{
			StepID:             req.StepID,
			ExitCode:           exitCode,
			OutputEnvelopePath: outEnv,
			ImageRef:           image,
		}, fmt.Errorf("%w: assemble phase exit %d", ErrDispatchFailed, exitCode)
	}

	// ── Host-LLM reasoning step ──────────────────────────────────────────────
	// This is the injectable seam (NG17: no LLM call here). Tests supply a
	// canned ReasoningStepFunc; production uses the MCP server (OQ-22).
	reasoningStep := c.ReasoningStep
	if reasoningStep == nil {
		reasoningStep = noopReasoningStep
	}

	reasoningStart := time.Now()
	if err := reasoningStep(ctx, req.StepID, absIn, absOut); err != nil {
		return Result{
			StepID:   req.StepID,
			ExitCode: 1,
			ImageRef: image,
		}, fmt.Errorf("container: host-LLM reasoning step failed: %w", err)
	}
	reasoningDurationMS := time.Since(reasoningStart).Milliseconds()

	if c.Journal != nil {
		_ = c.Journal.AppendHostReasoning(req.StepID, "prompt-bundle.json", "reasoning.json", reasoningDurationMS)
	}

	// ── Phase 2: package ─────────────────────────────────────────────────────
	packageArgs := buildDockerArgs("package", req.Env)

	if c.Journal != nil {
		_ = c.Journal.AppendDispatchPhase(req.StepID, "", "", "", "container", "", "package")
	}

	_, _, packageErr := c.runner().Run(ctx, nil, "docker", packageArgs...)
	if packageErr != nil {
		exitCode := exitCodeFrom(packageErr)
		outEnv, _ := findOutputEnvelope(req.OutputDir)
		return Result{
			StepID:             req.StepID,
			ExitCode:           exitCode,
			OutputEnvelopePath: outEnv,
			ImageRef:           image,
		}, fmt.Errorf("%w: package phase exit %d", ErrDispatchFailed, exitCode)
	}

	// Fetch the image digest after a successful run for the trace record.
	imageDigest, _ := c.imageDigest(ctx, image)

	outEnv, _ := findOutputEnvelope(req.OutputDir)

	return Result{
		StepID:             req.StepID,
		ExitCode:           0,
		OutputEnvelopePath: outEnv,
		ImageRef:           image,
		ImageDigest:        imageDigest,
	}, nil
}

// exitCodeFrom extracts the process exit code from a command error, or returns
// 1 if the error is not an *exec.ExitError.
func exitCodeFrom(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

// noopReasoningStep is the default ReasoningStepFunc used when ReasoningStep
// is nil. It does nothing and returns nil — appropriate for legacy single-phase
// tests and environments without a reasoning provider configured yet.
// In production this is replaced by the MCP stdio server's reasoning hook.
func noopReasoningStep(_ context.Context, _, _, _ string) error {
	return nil
}

// probeDaemon checks that the Docker daemon is reachable.
func (c *ContainerExecutor) probeDaemon(ctx context.Context) error {
	_, _, err := c.runner().Run(ctx, nil, "docker", "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDockerUnreachable, err)
	}
	return nil
}

// resolveImage returns the container image reference for eidolon.
//
// Priority:
//  1. JUNCTION_EIDOLON_IMAGE_<UPPER_EIDOLON> env var.
//  2. ghcr.io/rynaro/<lowercased-eidolon>:<version> where version is the
//     caller-supplied value (derived from req.EidolonVersion, which comes from
//     the plan step's to.version, falling back to c.EidolonVersion).
//
// If version is empty, resolveImage returns ErrImageNotAvailable immediately —
// there is no ":latest" fallback (images are SemVer-only by registry policy).
//
// If the resolved image is not pullable, returns ErrImageNotAvailable (exit 71)
// with an actionable message naming the pinned version, the full image ref,
// and the override env-var. Build-from-source fallback is deferred to v0.2
// (OQ-17).
func (c *ContainerExecutor) resolveImage(ctx context.Context, eidolon, version string) (string, error) {
	// 1. Env override.
	envKey := "JUNCTION_EIDOLON_IMAGE_" + toEnvSlug(eidolon)
	if img := os.Getenv(envKey); img != "" {
		return img, nil
	}

	// 2. Pinned GHCR image — version must come from the plan (to.version).
	// No ":latest" fallback: the registry publishes SemVer tags only, and a
	// floating tag would defeat the reproducibility guarantee.
	if version == "" {
		return "", fmt.Errorf("%w: eidolon %q has no version set in the plan (to.version is empty). "+
			"Set JUNCTION_EIDOLON_IMAGE_%s to override. "+
			"Build-from-source fallback (OQ-17) is deferred to v0.2.",
			ErrImageNotAvailable, eidolon, toEnvSlug(eidolon))
	}
	image := "ghcr.io/rynaro/" + strings.ToLower(eidolon) + ":" + version

	// Try pulling the image to verify availability.
	_, _, err := c.runner().Run(ctx, nil, "docker", "pull", image)
	if err != nil {
		return "", fmt.Errorf("%w: eidolon %q version %q — image %q not available — %v. "+
			"Set JUNCTION_EIDOLON_IMAGE_%s to override. "+
			"Build-from-source fallback (OQ-17) is deferred to v0.2.",
			ErrImageNotAvailable, eidolon, version, image, err, toEnvSlug(eidolon))
	}

	return image, nil
}

// imageDigest returns the local digest for an image reference, or empty on error.
func (c *ContainerExecutor) imageDigest(ctx context.Context, image string) (string, error) {
	out, _, err := c.runner().Run(ctx, nil, "docker", "inspect",
		"--format", "{{index .RepoDigests 0}}", image)
	if err != nil || out == "" {
		// Fall back to image ID if no digest is available.
		out, _, err = c.runner().Run(ctx, nil, "docker", "inspect",
			"--format", "{{.Id}}", image)
		if err != nil {
			return "", err
		}
	}
	return out, nil
}

// toEnvSlug converts an Eidolon slug to an env var suffix:
// upper-cased, hyphens and dots replaced with underscores.
func toEnvSlug(eidolon string) string {
	s := strings.ToUpper(eidolon)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	return s
}

// copyFile copies src to dst, creating dst if it doesn't exist.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
