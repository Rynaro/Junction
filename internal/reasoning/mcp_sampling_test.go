package reasoning_test

// mcp_sampling_test.go — unit tests for mcpSamplingProvider
//
// G2: TestParamsSnapshot — PromptBundle → SamplingCreateMessageParams snapshot
// G4: TestCapabilityAbsent — verbatim error string when capability missing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rynaro/Junction/internal/mcp"
	"github.com/Rynaro/Junction/internal/reasoning"
)

// ─── G4: capability-absent error ─────────────────────────────────────────────

const wantCapabilityAbsentErr = "reasoning: MCP host did not declare sampling capability " +
	"(client capabilities.sampling is absent); " +
	"configure JUNCTION_REASONING_PROVIDER=canned or shellout for non-sampling hosts, " +
	"or JUNCTION_REASONING_PROVIDER=none to keep the legacy single-phase behaviour"

func TestCapabilityAbsent_NilCaps(t *testing.T) {
	t.Parallel()
	var nilCaps *mcp.ClientCapabilities // nil
	cfg := reasoning.SamplingConfig{
		ClientCapabilities: func() *mcp.ClientCapabilities { return nilCaps },
		Request: func(_ context.Context, _ *reasoning.SamplingCreateMessageParams) (*reasoning.SamplingCreateMessageResult, error) {
			return nil, nil
		},
		Timeout: 5 * time.Second,
	}
	p, err := reasoning.NewProvider(reasoning.Config{Provider: "mcp-sampling", Sampling: cfg})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, err = p.Reason(context.Background(), canonicalBundle(t))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != wantCapabilityAbsentErr {
		t.Errorf("error = %q\nwant  = %q", err.Error(), wantCapabilityAbsentErr)
	}
}

func TestCapabilityAbsent_NilSampling(t *testing.T) {
	t.Parallel()
	caps := &mcp.ClientCapabilities{Sampling: nil} // capabilities present but sampling absent
	cfg := reasoning.SamplingConfig{
		ClientCapabilities: func() *mcp.ClientCapabilities { return caps },
		Request: func(_ context.Context, _ *reasoning.SamplingCreateMessageParams) (*reasoning.SamplingCreateMessageResult, error) {
			return nil, nil
		},
		Timeout: 5 * time.Second,
	}
	p, err := reasoning.NewProvider(reasoning.Config{Provider: "mcp-sampling", Sampling: cfg})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, err = p.Reason(context.Background(), canonicalBundle(t))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != wantCapabilityAbsentErr {
		t.Errorf("error = %q\nwant  = %q", err.Error(), wantCapabilityAbsentErr)
	}
}

// ─── G2: params snapshot ─────────────────────────────────────────────────────

// TestParamsSnapshot marshals a PromptBundle fixture to SamplingCreateMessageParams
// and compares byte-exact against testdata/sampling-params-golden.json.
//
// To update the golden file: delete sampling-params-golden.json, run the test
// with UPDATE_GOLDEN=1 to regenerate, then inspect and commit the result.
func TestParamsSnapshot(t *testing.T) {
	t.Parallel()

	// Read the prompt-bundle fixture.
	bundleData, err := os.ReadFile(filepath.Join("testdata", "prompt-bundle.json"))
	if err != nil {
		t.Fatalf("reading testdata/prompt-bundle.json: %v", err)
	}
	var bundle reasoning.PromptBundle
	if err := json.Unmarshal(bundleData, &bundle); err != nil {
		t.Fatalf("unmarshal prompt-bundle.json: %v", err)
	}

	// Exercise the exported BundleToSamplingParams via the provider path.
	// We use a fake Request that captures the params.
	var captured *reasoning.SamplingCreateMessageParams
	caps := &mcp.ClientCapabilities{Sampling: &mcp.SamplingCapability{}}
	cfg := reasoning.SamplingConfig{
		ClientCapabilities: func() *mcp.ClientCapabilities { return caps },
		Request: func(_ context.Context, p *reasoning.SamplingCreateMessageParams) (*reasoning.SamplingCreateMessageResult, error) {
			captured = p
			return &reasoning.SamplingCreateMessageResult{
				Role:       "assistant",
				Content:    reasoning.SamplingContent{Type: "text", Text: "snapshot test"},
				Model:      "test-model",
				StopReason: "endTurn",
			}, nil
		},
		Timeout: 5 * time.Second,
	}
	p, err := reasoning.NewProvider(reasoning.Config{Provider: "mcp-sampling", Sampling: cfg})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := p.Reason(context.Background(), &bundle); err != nil {
		t.Fatalf("Reason: %v", err)
	}

	if captured == nil {
		t.Fatal("params were not captured — Request was not called")
	}

	// Marshal captured params to indented JSON.
	got, err := json.MarshalIndent(captured, "", "  ")
	if err != nil {
		t.Fatalf("marshal captured params: %v", err)
	}
	// Add trailing newline to match the golden file on disk.
	got = append(got, '\n')

	goldenPath := filepath.Join("testdata", "sampling-params-golden.json")

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("writing golden file: %v", err)
		}
		t.Logf("Updated %s", goldenPath)
		return
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden file %s: %v", goldenPath, err)
	}
	if string(got) != string(golden) {
		t.Errorf("params snapshot mismatch.\nGot:\n%s\nWant:\n%s", got, golden)
	}
}

// ─── G6: response unmarshal round-trip ───────────────────────────────────────

// TestResponseUnmarshal round-trips the upstream-spec example response through
// SamplingCreateMessageResult.
func TestResponseUnmarshal(t *testing.T) {
	t.Parallel()
	raw := `{
  "role": "assistant",
  "content": { "type": "text", "text": "Hello from the LLM." },
  "model": "claude-3-sonnet-20240307",
  "stopReason": "endTurn"
}`
	var result reasoning.SamplingCreateMessageResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Role != "assistant" {
		t.Errorf("Role = %q, want \"assistant\"", result.Role)
	}
	if result.Content.Type != "text" {
		t.Errorf("Content.Type = %q, want \"text\"", result.Content.Type)
	}
	if result.Content.Text != "Hello from the LLM." {
		t.Errorf("Content.Text = %q, want \"Hello from the LLM.\"", result.Content.Text)
	}
	if result.Model != "claude-3-sonnet-20240307" {
		t.Errorf("Model = %q, want \"claude-3-sonnet-20240307\"", result.Model)
	}
	if result.StopReason != "endTurn" {
		t.Errorf("StopReason = %q, want \"endTurn\"", result.StopReason)
	}
}

// TestMCPSamplingSuccess verifies a happy-path round-trip via the provider.
func TestMCPSamplingSuccess(t *testing.T) {
	t.Parallel()
	caps := &mcp.ClientCapabilities{Sampling: &mcp.SamplingCapability{}}
	cfg := reasoning.SamplingConfig{
		ClientCapabilities: func() *mcp.ClientCapabilities { return caps },
		Request: func(_ context.Context, _ *reasoning.SamplingCreateMessageParams) (*reasoning.SamplingCreateMessageResult, error) {
			return &reasoning.SamplingCreateMessageResult{
				Role:       "assistant",
				Content:    reasoning.SamplingContent{Type: "text", Text: "The analysis shows three HTTP handlers."},
				Model:      "claude-3-sonnet-20240307",
				StopReason: "endTurn",
			}, nil
		},
		Timeout: 5 * time.Second,
	}
	p, err := reasoning.NewProvider(reasoning.Config{Provider: "mcp-sampling", Sampling: cfg})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	bundle := canonicalBundle(t)
	r, err := p.Reason(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Reason: %v", err)
	}
	assertValidReasoning(t, r, "mcp-sampling")
	if r.Model != "claude-3-sonnet-20240307" {
		t.Errorf("Model = %q, want \"claude-3-sonnet-20240307\"", r.Model)
	}
	if r.Content.Text != "The analysis shows three HTTP handlers." {
		t.Errorf("Content.Text = %q", r.Content.Text)
	}
}

// TestMCPSamplingTimeout verifies that context cancellation surfaces as an error.
func TestMCPSamplingTimeout(t *testing.T) {
	t.Parallel()
	caps := &mcp.ClientCapabilities{Sampling: &mcp.SamplingCapability{}}
	cfg := reasoning.SamplingConfig{
		ClientCapabilities: func() *mcp.ClientCapabilities { return caps },
		Request: func(ctx context.Context, _ *reasoning.SamplingCreateMessageParams) (*reasoning.SamplingCreateMessageResult, error) {
			// Block until context cancelled.
			<-ctx.Done()
			return nil, ctx.Err()
		},
		Timeout: 10 * time.Millisecond, // very short timeout
	}
	p, err := reasoning.NewProvider(reasoning.Config{Provider: "mcp-sampling", Sampling: cfg})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err = p.Reason(ctx, canonicalBundle(t))
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
