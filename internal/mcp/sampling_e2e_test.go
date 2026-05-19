package mcp_test

// sampling_e2e_test.go — G3: stub-MCP-client end-to-end test (LOAD-BEARING).
//
// This test wires a full Junction MCP server with JUNCTION_REASONING_PROVIDER=
// mcp-sampling against a stub MCP client that satisfies sampling/createMessage
// requests. It validates the complete two-phase server-initiated request path:
//   1. MCP server calls srv.SendRequest("sampling/createMessage", params).
//   2. Stub client receives the server-initiated request, validates params, and
//      responds with a canned Reasoning result.
//   3. The reasoning step writes reasoning.json to inDir.
//   4. reasoning.json has the expected SourceProvider, Model, and Content.Text.
//
// Methodology note (per spec §12 R05): the os.Executable+exec re-exec path
// in runEnvelopeShellOut strips Go test stdin/stdout. This test uses the
// direct dispatch path (calling ReasoningStep directly on the server) rather
// than routing through harness.run tools/call, which would require a real
// Docker daemon. The harness.run ↔ ContainerExecutor wiring is verified by
// internal/dispatch/container_test.go::TestContainerExecutor_TwoPhaseRoundTrip.
//
// The protocol assertions (server-initiated request shape, response demux,
// capability cache) are fully exercised here.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rynaro/Junction/internal/mcp"
	"github.com/Rynaro/Junction/internal/reasoning"
)

// cannedSamplingResultText is the text the stub client returns for every
// sampling/createMessage request.
const cannedSamplingResultText = "Stub reasoning output for the package phase."

// ─── G3: stub-MCP-client end-to-end ──────────────────────────────────────────

// TestSamplingE2E_TwoPhaseRoundTrip is the G3 load-bearing end-to-end test.
//
// Spec §6 G3 assertions:
//   A. sampling/createMessage received exactly once for the single-step plan.
//   B. inDir/reasoning.json exists; SourceProvider=="mcp-sampling"; Model=="stub-model";
//      Content.Text==cannedSamplingResultText.
//   C. The server-initiated request had a valid params shape (messages, maxTokens).
//   D. The tools/call result for harness.run parses to an object with non-empty
//      thread_id and trace_root (verified in TestToolsCall_Run_MultiStepPlan).
func TestSamplingE2E_TwoPhaseRoundTrip(t *testing.T) {
	// ── 1. Build the MCP server with mcp-sampling provider ───────────────────
	srv := mcp.NewServer("0.2.0-test", nil)

	// Track how many sampling requests we received.
	samplingCount := 0

	samplingCfg := reasoning.SamplingConfig{
		ClientCapabilities: srv.ClientCapabilities,
		Request: func(ctx context.Context, params *reasoning.SamplingCreateMessageParams) (*reasoning.SamplingCreateMessageResult, error) {
			samplingCount++
			// Route through the server's actual SendRequest so the stub client
			// loop handles it (validates the real wire protocol).
			raw, err := srv.SendRequest(ctx, "sampling/createMessage", params)
			if err != nil {
				return nil, err
			}
			var out reasoning.SamplingCreateMessageResult
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, fmt.Errorf("unmarshal sampling result: %w", err)
			}
			return &out, nil
		},
		Timeout: 30 * time.Second,
	}
	reasoningCfg := reasoning.Config{
		Provider: "mcp-sampling",
		Sampling: samplingCfg,
	}
	provider, err := reasoning.NewProvider(reasoningCfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	srv.SetReasoningStep(reasoning.NewReasoningStepFunc(provider))

	reg, err := mcp.NewRegistryDefaultWithServer(srv)
	if err != nil {
		t.Fatalf("NewRegistryDefaultWithServer: %v", err)
	}
	srv.SetTools(reg)

	// ── 2. Wire io.Pipe for bidirectional MCP stdio ───────────────────────────
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Serve(ctx, serverRead, serverWrite)
	}()

	enc := json.NewEncoder(clientWrite)
	dec := json.NewDecoder(clientRead)

	// ── 3. Initialize handshake with sampling capability ─────────────────────
	if err := enc.Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"clientInfo":      map[string]string{"name": "stub-client", "version": "1.0"},
			"capabilities":    map[string]interface{}{"sampling": map[string]interface{}{}},
		},
	}); err != nil {
		t.Fatalf("encode initialize: %v", err)
	}

	// Read initialize response.
	var initResp map[string]interface{}
	if err := dec.Decode(&initResp); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}

	// ── 4. Write prompt-bundle.json in a temp outDir ──────────────────────────
	inDir := t.TempDir()
	outDir := t.TempDir()

	promptBundle := map[string]interface{}{
		"schema_version": "1.0",
		"step_id":        "S0",
		"objective":      "Analyse entry points.",
		"system_prompt":  "Be concise.",
		"user_messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": map[string]interface{}{"type": "text", "text": "Please analyse the code."},
			},
		},
		"max_tokens": 256,
	}
	promptBundleBytes, _ := json.Marshal(promptBundle)
	if err := os.WriteFile(filepath.Join(outDir, "prompt-bundle.json"), promptBundleBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// ── 5. Stub client goroutine: handle server-initiated sampling/createMessage ─
	stubDone := make(chan error, 1)
	var capturedParams map[string]interface{}
	go func() {
		for {
			var msg map[string]interface{}
			if decErr := dec.Decode(&msg); decErr != nil {
				if decErr == io.EOF {
					stubDone <- nil
					return
				}
				stubDone <- fmt.Errorf("stub client decode: %v", decErr)
				return
			}

			method, _ := msg["method"].(string)
			id := msg["id"]

			if method == "sampling/createMessage" && id != nil {
				// Capture the params for assertion C.
				if p, ok := msg["params"]; ok {
					pb, _ := json.Marshal(p)
					json.Unmarshal(pb, &capturedParams)
				}

				// Respond with the canned sampling result.
				resp := map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]interface{}{
						"role": "assistant",
						"content": map[string]interface{}{
							"type": "text",
							"text": cannedSamplingResultText,
						},
						"model":      "stub-model",
						"stopReason": "endTurn",
					},
				}
				if encErr := enc.Encode(resp); encErr != nil {
					stubDone <- fmt.Errorf("stub client encode: %v", encErr)
					return
				}
				stubDone <- nil
				return
			}
			// Ignore other messages (e.g. tools/call responses not expected here).
		}
	}()

	// ── 6. Call the reasoning step ────────────────────────────────────────────
	reasoningStep := srv.ReasoningStep()
	if reasoningStep == nil {
		t.Fatal("ReasoningStep is nil")
	}

	if err := reasoningStep(ctx, "S0", inDir, outDir); err != nil {
		t.Fatalf("reasoningStep: %v", err)
	}

	// Wait for stub client.
	select {
	case err := <-stubDone:
		if err != nil {
			t.Fatalf("stub client: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stub client timed out")
	}

	// ── 7. Assertion A: sampling/createMessage received exactly once ──────────
	if samplingCount != 1 {
		t.Errorf("samplingCount = %d, want 1", samplingCount)
	}

	// ── 8. Assertion B: reasoning.json exists with correct fields ─────────────
	reasoningPath := filepath.Join(inDir, "reasoning.json")
	if _, statErr := os.Stat(reasoningPath); os.IsNotExist(statErr) {
		t.Fatal("reasoning.json not found in inDir")
	}
	data, err := os.ReadFile(reasoningPath)
	if err != nil {
		t.Fatalf("reading reasoning.json: %v", err)
	}
	var r reasoning.Reasoning
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("unmarshal reasoning.json: %v", err)
	}
	if r.SourceProvider != "mcp-sampling" {
		t.Errorf("SourceProvider = %q, want \"mcp-sampling\"", r.SourceProvider)
	}
	if r.Model != "stub-model" {
		t.Errorf("Model = %q, want \"stub-model\"", r.Model)
	}
	if r.Content.Text != cannedSamplingResultText {
		t.Errorf("Content.Text = %q, want %q", r.Content.Text, cannedSamplingResultText)
	}

	// ── 9. Assertion C: params shape matches MCP spec §2.2 ────────────────────
	if capturedParams == nil {
		t.Fatal("capturedParams is nil — stub client did not receive params")
	}
	if _, ok := capturedParams["messages"]; !ok {
		t.Error("params missing 'messages' field")
	}
	if _, ok := capturedParams["maxTokens"]; !ok {
		t.Error("params missing 'maxTokens' field")
	}
	// systemPrompt should include the objective prepended.
	if sp, ok := capturedParams["systemPrompt"].(string); ok {
		if len(sp) == 0 {
			t.Error("systemPrompt is empty, expected objective to be prepended")
		}
	}

	// ── 10. Shutdown ──────────────────────────────────────────────────────────
	cancel()
	clientWrite.Close()
	<-serverErr
}

// ─── G2: ClientCapabilities cache ────────────────────────────────────────────

// TestClientCapabilitiesCache verifies G2: handleInitialize populates
// Server.ClientCapabilities and the field is readable via the getter.
func TestClientCapabilitiesCache(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	// Before initialize: should be nil.
	if caps := srv.ClientCapabilities(); caps != nil {
		t.Errorf("ClientCapabilities() before initialize = %+v, want nil", caps)
	}

	in := bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test","version":"1.0"},"capabilities":{"sampling":{}}}}` + "\n",
	)
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	caps := srv.ClientCapabilities()
	if caps == nil {
		t.Fatal("ClientCapabilities() after initialize = nil, want non-nil")
	}
	if caps.Sampling == nil {
		t.Error("ClientCapabilities().Sampling = nil after sending sampling:{}")
	}
}

// TestClientCapabilities_NilSampling verifies that "sampling": null unmarshals
// to nil *SamplingCapability (spec R02).
func TestClientCapabilities_NilSampling(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	in := bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test","version":"1.0"},"capabilities":{"sampling":null}}}` + "\n",
	)
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	caps := srv.ClientCapabilities()
	if caps == nil {
		t.Fatal("ClientCapabilities() = nil, want non-nil")
	}
	if caps.Sampling != nil {
		t.Errorf("ClientCapabilities().Sampling = %+v, want nil (sampling:null should be absent)", caps.Sampling)
	}
}

// TestClientCapabilities_Empty verifies that empty capabilities{} leaves Sampling nil.
func TestClientCapabilities_Empty(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	in := bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test","version":"1.0"},"capabilities":{}}}` + "\n",
	)
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	caps := srv.ClientCapabilities()
	if caps == nil {
		t.Fatal("ClientCapabilities() = nil, want non-nil struct")
	}
	if caps.Sampling != nil {
		t.Errorf("ClientCapabilities().Sampling = %+v, want nil when capabilities.sampling is absent", caps.Sampling)
	}
}
