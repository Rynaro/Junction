package reasoning_test

// shellout_test.go — G6: shellout provider stdin/stdout contract

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rynaro/Junction/internal/reasoning"
)

// stubReasoningJSON returns a valid Reasoning document as a JSON string.
func stubReasoningJSON(stepID string) string {
	r := reasoning.Reasoning{
		SchemaVersion:  "1.0",
		StepID:         stepID,
		Model:          "stub-cli-model",
		StopReason:     "endTurn",
		Content:        reasoning.TextContent{Type: "text", Text: "shellout reasoning output"},
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceProvider: "shellout",
	}
	data, _ := json.Marshal(r)
	return string(data)
}

// writeScript writes an executable shell script to a temp dir and returns its path.
func writeScript(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+content+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// G6: PromptBundle JSON is piped to stdin; stdout JSON unmarshals into Reasoning.
func TestShelloutProvider_HappyPath(t *testing.T) {
	t.Parallel()
	script := writeScript(t, "cat - > /dev/null\necho '"+stubReasoningJSON("S0")+"'")
	cfg := reasoning.Config{
		Provider:    "shellout",
		ShelloutCmd: script,
		Sampling:    reasoning.SamplingConfig{Timeout: 10 * time.Second},
	}
	p, err := reasoning.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	r, err := p.Reason(context.Background(), canonicalBundle(t))
	if err != nil {
		t.Fatalf("Reason: %v", err)
	}
	assertValidReasoning(t, r, "shellout")
	if r.Content.Text != "shellout reasoning output" {
		t.Errorf("Content.Text = %q, want \"shellout reasoning output\"", r.Content.Text)
	}
}

// G6: non-JSON stdout → clear error including snippet.
func TestShelloutProvider_NonJSONStdout(t *testing.T) {
	t.Parallel()
	script := writeScript(t, `echo "this is not JSON at all"`)
	cfg := reasoning.Config{
		Provider:    "shellout",
		ShelloutCmd: script,
		Sampling:    reasoning.SamplingConfig{Timeout: 10 * time.Second},
	}
	p, err := reasoning.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, err = p.Reason(context.Background(), canonicalBundle(t))
	if err == nil {
		t.Fatal("expected error for non-JSON stdout")
	}
	if !strings.Contains(err.Error(), "not valid Reasoning JSON") {
		t.Errorf("error = %q, want to contain \"not valid Reasoning JSON\"", err.Error())
	}
}

// G6: non-zero exit → error includes exit code + stderr snippet.
func TestShelloutProvider_NonZeroExit(t *testing.T) {
	t.Parallel()
	script := writeScript(t, `echo "error details" >&2; exit 1`)
	cfg := reasoning.Config{
		Provider:    "shellout",
		ShelloutCmd: script,
		Sampling:    reasoning.SamplingConfig{Timeout: 10 * time.Second},
	}
	p, err := reasoning.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, err = p.Reason(context.Background(), canonicalBundle(t))
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("error = %q, want to contain \"exited 1\"", err.Error())
	}
}

// G6: context cancellation kills the subprocess.
// We use a provider-level timeout that is shorter than the script's runtime
// (rather than a very long sleep) so the test completes quickly.
func TestShelloutProvider_ContextCancellation(t *testing.T) {
	t.Parallel()
	// Script that blocks by reading stdin forever (but no sleep child process).
	// exec.CommandContext will kill the process group on context cancellation.
	script := writeScript(t, "read LINE")
	cfg := reasoning.Config{
		Provider:    "shellout",
		ShelloutCmd: script,
		Sampling:    reasoning.SamplingConfig{Timeout: 50 * time.Millisecond},
	}
	p, err := reasoning.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, err = p.Reason(context.Background(), canonicalBundle(t))
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	t.Logf("got expected error: %s", err)
}
