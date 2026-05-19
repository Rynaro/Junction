package reasoning_test

// provider_test.go — shared contract test (G1) + factory edge cases (G7, G8)
//
// G1: every provider produces a structurally valid Reasoning for a canonical
//     PromptBundle.
// G7: NewProvider with Provider=mcp-sampling AND Sampling.ClientCapabilities==nil
//     returns the expected clear error.
// G8 (atomicity): writeReasoning leaves no partial file; concurrent reader
//     sees no file or a complete Reasoning.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Rynaro/Junction/internal/reasoning"
)

// canonicalBundle returns a minimal valid PromptBundle for all contract tests.
func canonicalBundle(t *testing.T) *reasoning.PromptBundle {
	t.Helper()
	return &reasoning.PromptBundle{
		SchemaVersion: "1.0",
		StepID:        "S0",
		SystemPrompt:  "You are a helpful assistant.",
		UserMessages: []reasoning.UserMessage{
			{Role: "user", Content: reasoning.TextContent{Type: "text", Text: "Hello"}},
		},
		MaxTokens: 100,
	}
}

// assertValidReasoning verifies that r has the required fields populated.
func assertValidReasoning(t *testing.T, r *reasoning.Reasoning, wantProvider string) {
	t.Helper()
	if r == nil {
		t.Fatal("Reason() returned nil Reasoning (only noop should return nil)")
	}
	if r.SchemaVersion != "1.0" {
		t.Errorf("schema_version = %q, want \"1.0\"", r.SchemaVersion)
	}
	if r.Content.Type != "text" {
		t.Errorf("content.type = %q, want \"text\"", r.Content.Type)
	}
	if r.SourceProvider != wantProvider {
		t.Errorf("source_provider = %q, want %q", r.SourceProvider, wantProvider)
	}
	if r.GeneratedAt == "" {
		t.Error("generated_at is empty")
	}
	if _, parseErr := time.Parse(time.RFC3339, r.GeneratedAt); parseErr != nil {
		t.Errorf("generated_at is not RFC3339: %v", parseErr)
	}
}

// ─── G1: contract test table ─────────────────────────────────────────────────

func TestProviderContract_Canned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cannedPath := filepath.Join(dir, "reasoning-canned.json")

	// Write a valid canned fixture.
	fixture := &reasoning.Reasoning{
		SchemaVersion:  "1.0",
		StepID:         "S0",
		Model:          "test-model",
		StopReason:     "endTurn",
		Content:        reasoning.TextContent{Type: "text", Text: "canned response"},
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceProvider: "canned",
	}
	data, _ := json.Marshal(fixture)
	if err := os.WriteFile(cannedPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := reasoning.Config{Provider: "canned", CannedPath: cannedPath}
	p, err := reasoning.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	r, err := p.Reason(context.Background(), canonicalBundle(t))
	if err != nil {
		t.Fatalf("Reason: %v", err)
	}
	assertValidReasoning(t, r, "canned")
}

func TestProviderContract_Shellout(t *testing.T) {
	t.Parallel()
	// Build a canned Reasoning that the stub command will emit on stdout.
	fixture := &reasoning.Reasoning{
		SchemaVersion:  "1.0",
		StepID:         "stub",
		Model:          "stub-cli-model",
		StopReason:     "endTurn",
		Content:        reasoning.TextContent{Type: "text", Text: "shellout response"},
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceProvider: "shellout",
	}
	data, _ := json.Marshal(fixture)

	// Write a tiny shell script that echoes the fixture JSON.
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "stub-reasoning.sh")
	script := "#!/bin/sh\ncat <<'EOF'\n" + string(data) + "\nEOF\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := reasoning.Config{
		Provider:    "shellout",
		ShelloutCmd: scriptPath,
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
}

// ─── G7: mcp-sampling without MCP server → clear error ───────────────────────

func TestSamplingWithoutServer(t *testing.T) {
	t.Parallel()
	cfg := reasoning.Config{
		Provider: "mcp-sampling",
		// Sampling.ClientCapabilities is nil — no server.
	}
	_, err := reasoning.NewProvider(cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := "reasoning: mcp-sampling provider requires a running MCP server (use 'junction mcp serve' or pick canned/shellout/none)"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// ─── G8: writeReasoning atomicity ─────────────────────────────────────────────

// TestWriteReasoningAtomicity uses a tight goroutine loop to concurrently read
// reasoning.json while a writer is producing it. The reader must see either
// no file or a complete valid JSON document — never a partial one.
//
// The test writes to a single shared inDir. Each iteration removes the file
// before writing so the reader races against a fresh write.
func TestWriteReasoningAtomicity(t *testing.T) {
	t.Parallel()

	sharedInDir := t.TempDir()
	target := filepath.Join(sharedInDir, "reasoning.json")

	fixture := &reasoning.Reasoning{
		SchemaVersion:  "1.0",
		StepID:         "S0",
		Model:          "test-model",
		StopReason:     "endTurn",
		Content:        reasoning.TextContent{Type: "text", Text: "atomic test"},
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceProvider: "canned",
	}
	fixtureData, _ := json.Marshal(fixture)

	// Write fixture file once; all goroutines share it.
	fixtureFile := filepath.Join(t.TempDir(), "f.json")
	if err := os.WriteFile(fixtureFile, fixtureData, 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a prompt-bundle.json once; all goroutines share it.
	bundle := &reasoning.PromptBundle{
		SchemaVersion: "1.0",
		StepID:        "S0",
		UserMessages:  []reasoning.UserMessage{{Role: "user", Content: reasoning.TextContent{Type: "text", Text: "hi"}}},
		MaxTokens:     1,
	}
	bundleData, _ := json.Marshal(bundle)
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outDir, "prompt-bundle.json"), bundleData, 0o644); err != nil {
		t.Fatal(err)
	}

	const iterations = 20
	var wg sync.WaitGroup

	// Concurrent reader goroutine: continuously reads the target file and
	// asserts that any data it finds is valid JSON.
	readErrors := make(chan string, 10)
	done := make(chan struct{})
	go func() {
		defer close(readErrors)
		for {
			select {
			case <-done:
				return
			default:
				data, err := os.ReadFile(target)
				if err != nil || len(data) == 0 {
					continue
				}
				var parsed reasoning.Reasoning
				if jsonErr := json.Unmarshal(data, &parsed); jsonErr != nil {
					select {
					case readErrors <- jsonErr.Error():
					default:
					}
				}
			}
		}
	}()

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cannedCfg := reasoning.Config{Provider: "canned", CannedPath: fixtureFile}
			p, err := reasoning.NewProvider(cannedCfg)
			if err != nil {
				t.Error(err)
				return
			}
			fn := reasoning.NewReasoningStepFunc(p)
			if err := fn(context.Background(), "S0", sharedInDir, outDir); err != nil {
				t.Error(err)
			}
		}()
	}

	wg.Wait()
	close(done)

	// Check for any read errors collected.
	for errMsg := range readErrors {
		t.Errorf("concurrent read saw invalid JSON: %s", errMsg)
	}
}

// ─── factory edge cases ───────────────────────────────────────────────────────

func TestNewProvider_None(t *testing.T) {
	t.Parallel()
	p, err := reasoning.NewProvider(reasoning.Config{Provider: "none"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "none" {
		t.Errorf("Name() = %q, want \"none\"", p.Name())
	}
	r, err := p.Reason(context.Background(), canonicalBundle(t))
	if err != nil {
		t.Fatalf("Reason: %v", err)
	}
	if r != nil {
		t.Errorf("noop Reason should return nil, got %+v", r)
	}
}

func TestNewProvider_EmptyString_DefaultsToNone(t *testing.T) {
	t.Parallel()
	p, err := reasoning.NewProvider(reasoning.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "none" {
		t.Errorf("Name() = %q, want \"none\"", p.Name())
	}
}

func TestNewProvider_Unknown(t *testing.T) {
	t.Parallel()
	_, err := reasoning.NewProvider(reasoning.Config{Provider: "totally-unknown"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestNewProvider_CannedMissingPath(t *testing.T) {
	t.Parallel()
	_, err := reasoning.NewProvider(reasoning.Config{Provider: "canned"})
	if err == nil {
		t.Fatal("expected error when CannedPath is empty")
	}
}

func TestNewProvider_ShelloutMissingCmd(t *testing.T) {
	t.Parallel()
	_, err := reasoning.NewProvider(reasoning.Config{Provider: "shellout"})
	if err == nil {
		t.Fatal("expected error when ShelloutCmd is empty")
	}
}

// TestNoopReasoningStepFunc verifies that NewReasoningStepFunc with a noop
// provider writes no file and returns nil (G8 backward-compat).
func TestNoopReasoningStepFunc(t *testing.T) {
	t.Parallel()
	p, _ := reasoning.NewProvider(reasoning.Config{Provider: "none"})
	fn := reasoning.NewReasoningStepFunc(p)

	inDir := t.TempDir()
	outDir := t.TempDir()

	// Write a valid prompt-bundle so readPromptBundle doesn't fail.
	bundle := &reasoning.PromptBundle{
		SchemaVersion: "1.0",
		StepID:        "S0",
		UserMessages:  []reasoning.UserMessage{{Role: "user", Content: reasoning.TextContent{Type: "text", Text: "hi"}}},
		MaxTokens:     1,
	}
	data, _ := json.Marshal(bundle)
	if err := os.WriteFile(filepath.Join(outDir, "prompt-bundle.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fn(context.Background(), "S0", inDir, outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(inDir, "reasoning.json")); !os.IsNotExist(statErr) {
		t.Error("noop provider wrote reasoning.json — expected no file")
	}
}
