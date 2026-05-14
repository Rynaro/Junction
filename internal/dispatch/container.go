// Package dispatch — ContainerExecutor implementation (S2.1, round 4).
//
// ContainerExecutor is the primary Executor from F2 onward. It runs each
// Eidolon in its own container via raw `docker run` (single-shot dispatch).
// Multi-Eidolon TRANCE chains use the chain.go ChainExecutor which composes
// multiple ContainerExecutor calls.
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

// ContainerExecutor runs each Eidolon in its own container via `docker run`.
// It implements the Executor interface from F1 and is the primary executor
// from F2 onward. Use ShellExecutor (--no-container) as fallback.
type ContainerExecutor struct {
	// Runner is the docker CLI adapter. If nil, uses the real docker binary.
	Runner CommandRunner

	// EidolonVersion is the version tag used when building the image reference
	// (e.g. "1.5.2"). Defaults to "latest" when empty.
	EidolonVersion string

	// ProbeDocker, when true, skips the daemon reachability probe. Useful in
	// tests that don't have a real Docker daemon.
	SkipDaemonProbe bool
}

// runner returns the configured runner or the production default.
func (c *ContainerExecutor) runner() CommandRunner {
	if c.Runner != nil {
		return c.Runner
	}
	return realRunner{}
}

// Execute dispatches req by running the resolved Eidolon image via docker run.
func (c *ContainerExecutor) Execute(ctx context.Context, req Request) (Result, error) {
	if !c.SkipDaemonProbe {
		if err := c.probeDaemon(ctx); err != nil {
			return Result{}, err
		}
	}

	image, err := c.resolveImage(ctx, req.Eidolon)
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

	dockerArgs := []string{
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
	}

	for _, e := range req.Env {
		dockerArgs = append(dockerArgs, "-e", e)
	}

	dockerArgs = append(dockerArgs, image)

	_, _, runErr := c.runner().Run(ctx, nil, "docker", dockerArgs...)

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
		outEnv, _ := findOutputEnvelope(req.OutputDir)
		return Result{
			StepID:             req.StepID,
			ExitCode:           exitCode,
			OutputEnvelopePath: outEnv,
			ImageRef:           image,
		}, fmt.Errorf("%w: exit %d", ErrDispatchFailed, exitCode)
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
//  2. ghcr.io/rynaro/<lowercased-eidolon>:<version>.
//
// If the resolved image is not pullable, returns ErrImageNotAvailable (exit 71).
// Build-from-source fallback is deferred to v0.2 (OQ-17).
func (c *ContainerExecutor) resolveImage(ctx context.Context, eidolon string) (string, error) {
	// 1. Env override.
	envKey := "JUNCTION_EIDOLON_IMAGE_" + toEnvSlug(eidolon)
	if img := os.Getenv(envKey); img != "" {
		return img, nil
	}

	// 2. Default GHCR image.
	version := c.EidolonVersion
	if version == "" {
		version = "latest"
	}
	image := "ghcr.io/rynaro/" + strings.ToLower(eidolon) + ":" + version

	// Try pulling the image to verify availability.
	_, _, err := c.runner().Run(ctx, nil, "docker", "pull", image)
	if err != nil {
		return "", fmt.Errorf("%w: image %q — %v. "+
			"Set JUNCTION_EIDOLON_IMAGE_%s to override. "+
			"Build-from-source fallback (OQ-17) is deferred to v0.2.",
			ErrImageNotAvailable, image, err, toEnvSlug(eidolon))
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
