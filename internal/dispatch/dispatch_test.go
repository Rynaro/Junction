package dispatch_test

// Tests for the dispatch package (story S4 — subprocess dispatch).
//
// The F1 happy path uses EntrypointOverride so tests don't need a full
// EIIS install layout. Each test wires a small shell script as the "Eidolon".

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rynaro/Junction/internal/dispatch"
)

// writeTmpScript writes a small bash/sh script to a temp file and returns
// its path with execute permission.
func writeTmpScript(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "eidolon.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// ─── Happy path: entrypoint override ─────────────────────────────────────────

// GIVEN a configured EntrypointOverride that exits 0 and writes an envelope
// WHEN Execute is called
// THEN Result.ExitCode == 0 and OutputEnvelopePath is non-empty.
func TestExecute_HappyPath(t *testing.T) {
	dir := t.TempDir()

	// The "Eidolon" just creates an output envelope file.
	script := writeTmpScript(t, `#!/bin/sh
echo "fake eidolon running" >&2
touch "$ECL_OUTPUT_DIR/output.md.envelope.json"
`)

	exec := &dispatch.ShellExecutor{
		EntrypointOverride: script,
	}

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "spectra",
		EnvelopePath: "/fake/input.envelope.json",
		ThreadID:     "test-thread-001",
		OutputDir:    filepath.Join(dir, "out"),
	}

	result, err := exec.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.OutputEnvelopePath == "" {
		t.Error("OutputEnvelopePath is empty, expected a path to the created envelope")
	}
}

// ─── Non-zero exit → ErrDispatchFailed ───────────────────────────────────────

func TestExecute_NonZeroExit(t *testing.T) {
	dir := t.TempDir()
	script := writeTmpScript(t, `#!/bin/sh
exit 42
`)

	exec := &dispatch.ShellExecutor{
		EntrypointOverride: script,
	}
	req := dispatch.Request{
		StepID:    "S0",
		Eidolon:   "spectra",
		ThreadID:  "thread-x",
		OutputDir: filepath.Join(dir, "out"),
	}

	result, err := exec.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Execute with exit 42 = nil, want ErrDispatchFailed")
	}
	if !errors.Is(err, dispatch.ErrDispatchFailed) {
		t.Errorf("Execute error = %v, want ErrDispatchFailed", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", result.ExitCode)
	}
}

// ─── EntrypointOverride not found ───────────────────────────────────────────

func TestExecute_OverrideNotFound(t *testing.T) {
	exec := &dispatch.ShellExecutor{
		EntrypointOverride: "/tmp/no-such-eidolon-xyzzy.sh",
	}
	req := dispatch.Request{
		StepID:    "S0",
		Eidolon:   "atlas",
		OutputDir: t.TempDir(),
	}

	_, err := exec.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Execute with missing override = nil, want ErrEntrypointNotFound")
	}
	if !errors.Is(err, dispatch.ErrEntrypointNotFound) {
		t.Errorf("Execute error = %v, want ErrEntrypointNotFound", err)
	}
}

// ─── Entrypoint resolution: no override, no local, no cache ──────────────────

func TestExecute_NoEntrypoint(t *testing.T) {
	exec := &dispatch.ShellExecutor{
		ProjectDir: t.TempDir(), // empty — no .eidolons/
		CacheDir:   t.TempDir(), // empty — no cache
	}
	req := dispatch.Request{
		StepID:    "S0",
		Eidolon:   "atlas",
		OutputDir: t.TempDir(),
	}

	_, err := exec.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Execute with no entrypoint = nil, want ErrEntrypointNotFound")
	}
	if !errors.Is(err, dispatch.ErrEntrypointNotFound) {
		t.Errorf("error = %v, want ErrEntrypointNotFound", err)
	}
}

// ─── ECL_THREAD_ID is set in the subprocess ──────────────────────────────────

func TestExecute_ThreadIDPropagated(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "thread_id.txt")

	script := writeTmpScript(t, `#!/bin/sh
echo "$ECL_THREAD_ID" > `+sentinel+`
`)

	exec := &dispatch.ShellExecutor{
		EntrypointOverride: script,
	}
	req := dispatch.Request{
		StepID:    "S0",
		Eidolon:   "atlas",
		ThreadID:  "expected-thread-id-abc",
		OutputDir: filepath.Join(dir, "out"),
	}

	_, _ = exec.Execute(context.Background(), req)

	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel file not written: %v", err)
	}
	got := string(data)
	if got == "" {
		t.Error("ECL_THREAD_ID was empty in subprocess")
	}
	// Check it contains our expected value (trimming newline).
	for i := len(got) - 1; i >= 0 && (got[i] == '\n' || got[i] == '\r'); i-- {
		got = got[:i]
	}
	if got != "expected-thread-id-abc" {
		t.Errorf("ECL_THREAD_ID = %q, want expected-thread-id-abc", got)
	}
}

// ─── ECL_INPUT_ENVELOPE is set in the subprocess ────────────────────────────

func TestExecute_InputEnvelopePropagated(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "input_env.txt")

	script := writeTmpScript(t, `#!/bin/sh
echo "$ECL_INPUT_ENVELOPE" > `+sentinel+`
`)

	exec := &dispatch.ShellExecutor{EntrypointOverride: script}
	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "spectra",
		EnvelopePath: "/some/path/spec.envelope.json",
		ThreadID:     "tid",
		OutputDir:    filepath.Join(dir, "out"),
	}

	_, _ = exec.Execute(context.Background(), req)

	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel not written: %v", err)
	}
	got := string(data)
	for i := len(got) - 1; i >= 0 && (got[i] == '\n' || got[i] == '\r'); i-- {
		got = got[:i]
	}
	if got != "/some/path/spec.envelope.json" {
		t.Errorf("ECL_INPUT_ENVELOPE = %q, want /some/path/spec.envelope.json", got)
	}
}
