package dispatch_test

// Tests for ContainerExecutor (S2.1).
//
// All tests use a stubCommandRunner that returns canned responses so no
// real Docker daemon is required.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rynaro/Junction/internal/dispatch"
)

// ─── stub runner ─────────────────────────────────────────────────────────────

// stubCall records one expected invocation.
type stubCall struct {
	// match is a substring of the joined args that must appear for this
	// stub to activate (empty = matches everything).
	match  string
	stdout string
	stderr string
	err    error
}

// stubRunner is a simple sequential stub: each Run call pops the first
// matching stubCall from the queue, or returns success if the queue is empty.
type stubRunner struct {
	calls []stubCall
}

func (s *stubRunner) Run(_ context.Context, _ []string, _ string, args ...string) (string, string, error) {
	joined := joinArgs(args)
	for i, c := range s.calls {
		if c.match == "" || containsStr(joined, c.match) {
			// Remove matched call from the queue.
			s.calls = append(s.calls[:i], s.calls[i+1:]...)
			return c.stdout, c.stderr, c.err
		}
	}
	// Default: success.
	return "", "", nil
}

func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}

func containsStr(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	n := len(substr)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == substr {
			return true
		}
	}
	return false
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// writeFixtureEnvelope writes a minimal envelope file and returns its path.
func writeFixtureEnvelope(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "input.envelope.json")
	if err := os.WriteFile(p, []byte(`{"message_id":"test-001"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// stubContainerExecutor creates a ContainerExecutor backed by a stubRunner.
// The stub runner is pre-loaded with the provided calls.
func stubContainerExecutor(calls []stubCall) *dispatch.ContainerExecutor {
	return &dispatch.ContainerExecutor{
		Runner:          &stubRunner{calls: calls},
		EidolonVersion:  "1.0.0",
		SkipDaemonProbe: true,
	}
}

// ─── S2.1 tests ──────────────────────────────────────────────────────────────

// GIVEN an Eidolon image that pulls and the container exits 0
// WHEN Execute is called
// THEN Result.ExitCode == 0 and ImageRef is set to the resolved image.
func TestContainerExecutor_HappyPath(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	stub := stubContainerExecutor([]stubCall{
		{match: "pull", stdout: "", stderr: "", err: nil},           // pull succeeds
		{match: "run", stdout: "", stderr: "", err: nil},            // run succeeds
		{match: "inspect", stdout: "sha256:abcdef", stderr: "", err: nil}, // digest
	})

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-container-001",
		OutputDir:    outDir,
	}

	result, err := stub.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.ImageRef == "" {
		t.Error("ImageRef is empty, expected a resolved image reference")
	}
}

// GIVEN JUNCTION_EIDOLON_IMAGE_ATLAS env var is set
// WHEN Execute is called
// THEN the env-var image is used (no pull probe for default image).
func TestContainerExecutor_EnvVarImageOverride(t *testing.T) {
	t.Setenv("JUNCTION_EIDOLON_IMAGE_ATLAS", "localhost/stub-atlas:test")

	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	// No pull call should be made for the override image.
	stub := stubContainerExecutor([]stubCall{
		{match: "run", stdout: "", stderr: "", err: nil},
		{match: "inspect", stdout: "sha256:localstub", stderr: "", err: nil},
	})

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-env-override",
		OutputDir:    outDir,
	}

	result, err := stub.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ImageRef != "localhost/stub-atlas:test" {
		t.Errorf("ImageRef = %q, want localhost/stub-atlas:test", result.ImageRef)
	}
}

// GIVEN docker pull fails (image not available)
// WHEN Execute is called (no --no-container override)
// THEN Execute returns ErrImageNotAvailable (maps to exit 71).
func TestContainerExecutor_ImagePullFailed_Exit71(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	stub := stubContainerExecutor([]stubCall{
		{match: "pull", stdout: "", stderr: "not found", err: errors.New("exit status 1")},
	})

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-pull-fail",
		OutputDir:    outDir,
	}

	_, err := stub.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Execute with pull failure = nil, want ErrImageNotAvailable")
	}
	if !errors.Is(err, dispatch.ErrImageNotAvailable) {
		t.Errorf("error = %v, want ErrImageNotAvailable (exit 71)", err)
	}
}

// GIVEN Docker daemon is unreachable (SkipDaemonProbe=false)
// WHEN Execute is called
// THEN Execute returns ErrDockerUnreachable (maps to exit 72).
func TestContainerExecutor_DockerUnreachable_Exit72(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	exec := &dispatch.ContainerExecutor{
		Runner: &stubRunner{calls: []stubCall{
			{match: "version", stdout: "", stderr: "cannot connect", err: errors.New("exit status 1")},
		}},
		EidolonVersion:  "1.0.0",
		SkipDaemonProbe: false, // probe enabled
	}

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-no-daemon",
		OutputDir:    outDir,
	}

	_, err := exec.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Execute with unreachable daemon = nil, want ErrDockerUnreachable")
	}
	if !errors.Is(err, dispatch.ErrDockerUnreachable) {
		t.Errorf("error = %v, want ErrDockerUnreachable (exit 72)", err)
	}
}

// GIVEN the container exits non-zero
// WHEN Execute is called
// THEN Execute returns ErrDispatchFailed.
func TestContainerExecutor_ContainerExitNonZero(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	stub := stubContainerExecutor([]stubCall{
		{match: "pull", stdout: "", err: nil},
		{match: "run", stdout: "", err: errors.New("exit status 2")},
	})

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-exit-nonzero",
		OutputDir:    outDir,
	}

	_, err := stub.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Execute with non-zero container = nil, want ErrDispatchFailed")
	}
	if !errors.Is(err, dispatch.ErrDispatchFailed) {
		t.Errorf("error = %v, want ErrDispatchFailed", err)
	}
}

// GIVEN the container writes an output envelope to the out/ dir
// WHEN Execute is called
// THEN Result.OutputEnvelopePath is non-empty.
func TestContainerExecutor_OutputEnvelopeDiscovered(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	// Pre-create the output envelope (simulating what the container would write).
	outEnvPath := filepath.Join(outDir, "output.md.envelope.json")
	if err := os.WriteFile(outEnvPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stub := stubContainerExecutor([]stubCall{
		{match: "pull", stdout: "", err: nil},
		{match: "run", stdout: "", err: nil},
		{match: "inspect", stdout: "sha256:xyz", err: nil},
	})

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-out-env",
		OutputDir:    outDir,
	}

	result, err := stub.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.OutputEnvelopePath == "" {
		t.Error("OutputEnvelopePath is empty, expected it to be discovered")
	}
}

// GIVEN a successful run
// WHEN Execute is called
// THEN JUNCTION_THREAD_ID and JUNCTION_INPUT_ENVELOPE appear in the docker run args.
func TestContainerExecutor_EnvVarPropagation(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	// Use a recording stub that captures the args of the run call.
	var capturedArgs []string
	recordRunner := &recordingRunner{
		onRun: func(args []string) {
			if len(args) > 0 && args[0] == "run" {
				capturedArgs = append(capturedArgs, args...)
			}
		},
	}

	exec := &dispatch.ContainerExecutor{
		Runner:          recordRunner,
		EidolonVersion:  "1.0.0",
		SkipDaemonProbe: true,
	}

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "expected-thread-xyz",
		OutputDir:    outDir,
	}

	_, _ = exec.Execute(context.Background(), req)

	wantEnvVars := []string{"JUNCTION_THREAD_ID=expected-thread-xyz", "ECL_THREAD_ID=expected-thread-xyz"}
	for _, want := range wantEnvVars {
		found := false
		for _, arg := range capturedArgs {
			if arg == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("env var %q not found in docker run args: %v", want, capturedArgs)
		}
	}
}

// recordingRunner captures args and always succeeds.
type recordingRunner struct {
	onRun func(args []string)
}

func (r *recordingRunner) Run(_ context.Context, _ []string, _ string, args ...string) (string, string, error) {
	if r.onRun != nil {
		r.onRun(args)
	}
	return "", "", nil
}
