package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Rynaro/Junction/internal/mcp"
)

// newTestServer builds a Server with a fresh Registry (embedded contracts)
// and a fixed version string for deterministic serverInfo assertions.
func newTestServer(t *testing.T) *mcp.Server {
	t.Helper()
	reg, err := mcp.NewRegistryDefault()
	if err != nil {
		t.Fatalf("NewRegistryDefault: %v", err)
	}
	return mcp.NewServer("0.1.0-test", reg)
}

// roundtrip sends a single JSON-RPC line to the server and returns the
// decoded response map.
func roundtrip(t *testing.T, srv *mcp.Server, req string) map[string]interface{} {
	t.Helper()
	in := bytes.NewBufferString(req + "\n")
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &got); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, out.String())
	}
	return got
}

// roundtripMulti sends multiple JSON-RPC lines and returns all decoded
// response objects. Notifications (no "id") produce no response lines.
func roundtripMulti(t *testing.T, srv *mcp.Server, reqs []string) []map[string]interface{} {
	t.Helper()
	var sb strings.Builder
	for _, r := range reqs {
		sb.WriteString(r)
		sb.WriteByte('\n')
	}
	in := bytes.NewBufferString(sb.String())
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve error: %v", err)
	}
	var results []map[string]interface{}
	dec := json.NewDecoder(&out)
	for dec.More() {
		var m map[string]interface{}
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		results = append(results, m)
	}
	return results
}

// ─── Initialize handshake ─────────────────────────────────────────────────────

func TestInitialize_Handshake(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test","version":"1.0"},"capabilities":{}}}`
	got := roundtrip(t, srv, req)

	if got["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", got["jsonrpc"])
	}
	// id comes back as float64 from JSON decode with map[string]interface{}.
	if fmt.Sprint(got["id"]) != "1" {
		t.Errorf("id = %v, want 1", got["id"])
	}
	result, ok := got["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", got["result"])
	}
	if result["protocolVersion"] != "2025-03-26" {
		t.Errorf("protocolVersion = %q, want 2025-03-26", result["protocolVersion"])
	}
	info, ok := result["serverInfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("serverInfo not a map")
	}
	if info["name"] != "junction" {
		t.Errorf("serverInfo.name = %q, want junction", info["name"])
	}
	if info["version"] != "0.1.0-test" {
		t.Errorf("serverInfo.version = %q, want 0.1.0-test", info["version"])
	}
	caps, ok := result["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("capabilities not a map")
	}
	if _, hasTools := caps["tools"]; !hasTools {
		t.Errorf("capabilities missing 'tools' key")
	}
}

func TestInitialize_NotificationNoResponse(t *testing.T) {
	srv := newTestServer(t)
	// Send initialize (id=1) + notifications/initialized (no id).
	reqs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test","version":"1.0"},"capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	}
	results := roundtripMulti(t, srv, reqs)
	// Only one response expected — the notification produces no output.
	if len(results) != 1 {
		t.Errorf("got %d responses, want 1 (notification should be silent)", len(results))
	}
}

// ─── tools/list ──────────────────────────────────────────────────────────────

func TestToolsList_ReturnsFourTools(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	got := roundtrip(t, srv, req)

	result, ok := got["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", got["result"])
	}
	toolsRaw, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatalf("tools not an array: %T", result["tools"])
	}
	if len(toolsRaw) != 4 {
		t.Errorf("tools count = %d, want 4", len(toolsRaw))
	}
	wantNames := map[string]bool{
		"harness.plan_from_prompt": true,
		"harness.run":              true,
		"harness.verify":           true,
		"harness.inject":           true,
	}
	for _, raw := range toolsRaw {
		tool, ok := raw.(map[string]interface{})
		if !ok {
			t.Errorf("tool entry not a map: %T", raw)
			continue
		}
		name, _ := tool["name"].(string)
		if !wantNames[name] {
			t.Errorf("unexpected tool name %q", name)
		}
		delete(wantNames, name)
		if desc, _ := tool["description"].(string); desc == "" {
			t.Errorf("tool %q: empty description", name)
		}
		if tool["inputSchema"] == nil {
			t.Errorf("tool %q: missing inputSchema", name)
		}
	}
	for name := range wantNames {
		t.Errorf("expected tool %q not found in tools/list", name)
	}
}

func TestToolsList_EachHasObjectInputSchema(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`
	got := roundtrip(t, srv, req)
	result := got["result"].(map[string]interface{})
	tools := result["tools"].([]interface{})
	for _, raw := range tools {
		tool := raw.(map[string]interface{})
		schema, ok := tool["inputSchema"].(map[string]interface{})
		if !ok {
			t.Errorf("tool %q: inputSchema not a map", tool["name"])
			continue
		}
		if schema["type"] != "object" {
			t.Errorf("tool %q: inputSchema.type = %q, want object", tool["name"], schema["type"])
		}
		if schema["properties"] == nil {
			t.Errorf("tool %q: inputSchema.properties is nil", tool["name"])
		}
	}
}

// ─── tools/call — harness.plan_from_prompt (stub) ────────────────────────────

func TestToolsCall_PlanFromPrompt_Stub(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"harness.plan_from_prompt","arguments":{"prompt":"map the atlas dispatch path"}}}`
	got := roundtrip(t, srv, req)

	result, ok := got["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", got["result"])
	}
	if result["isError"] != false {
		t.Errorf("isError = %v, want false (stub returns valid response, not handler error)", result["isError"])
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("content missing or empty")
	}
	item := content[0].(map[string]interface{})
	if item["type"] != "text" {
		t.Errorf("content[0].type = %q, want text", item["type"])
	}
	text, _ := item["text"].(string)
	if !strings.Contains(strings.ToLower(text), "stub") && !strings.Contains(strings.ToLower(text), "not implemented") {
		t.Errorf("expected stub/not-implemented message in text, got: %s", text)
	}
}

// ─── tools/call — harness.run ────────────────────────────────────────────────

func TestToolsCall_Run_MissingPlanPath(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"harness.run","arguments":{}}}`
	got := roundtrip(t, srv, req)

	result, ok := got["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", got["result"])
	}
	if result["isError"] != true {
		t.Errorf("isError = %v, want true for missing plan_path", result["isError"])
	}
}

func TestToolsCall_Run_NonexistentFile(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"harness.run","arguments":{"plan_path":"/nonexistent-plan-mcp-12345.json"}}}`
	got := roundtrip(t, srv, req)

	result, ok := got["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", got["result"])
	}
	// junction run --envelope /nonexistent exits non-zero → isError:true
	if result["isError"] != true {
		t.Errorf("isError = %v, want true for non-existent envelope file", result["isError"])
	}
}

// ─── tools/call — harness.verify ─────────────────────────────────────────────

func TestToolsCall_Verify_MissingEnvelopePath(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"harness.verify","arguments":{}}}`
	got := roundtrip(t, srv, req)

	result, ok := got["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", got["result"])
	}
	if result["isError"] != true {
		t.Errorf("isError = %v, want true for missing envelope_path", result["isError"])
	}
}

func TestToolsCall_Verify_NonexistentFile(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"harness.verify","arguments":{"envelope_path":"/nonexistent-envelope-mcp-99999.json"}}}`
	got := roundtrip(t, srv, req)

	result, ok := got["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", got["result"])
	}
	if result["isError"] != true {
		t.Errorf("isError = %v, want true for non-existent file", result["isError"])
	}
}

// TestToolsCall_Verify_ValidEnvelope exercises the happy path with a real
// fixture. Acceptance criterion: same pass/fail result as `junction verify`.
func TestToolsCall_Verify_ValidEnvelope(t *testing.T) {
	envelopePath := "../../testdata/apivr-to-atlas/ecl-envelope.json"
	if _, err := os.Stat(envelopePath); err != nil {
		t.Skipf("fixture %s not found — skipping happy-path verify test: %v", envelopePath, err)
	}

	srv := newTestServer(t)
	req := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"harness.verify","arguments":{"envelope_path":%q}}}`,
		envelopePath,
	)
	got := roundtrip(t, srv, req)

	result, ok := got["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", got["result"])
	}
	if result["isError"] != false {
		content := result["content"].([]interface{})
		t.Errorf("isError=true for valid fixture; content: %v", content)
		return
	}

	content := result["content"].([]interface{})
	item := content[0].(map[string]interface{})
	text, _ := item["text"].(string)

	var verifyResult map[string]interface{}
	if err := json.Unmarshal([]byte(text), &verifyResult); err != nil {
		t.Fatalf("content text is not JSON: %v\ntext: %s", err, text)
	}
	if verifyResult["ok"] != true {
		t.Errorf("verify ok = %v, want true for valid fixture; errors: %v",
			verifyResult["ok"], verifyResult["errors"])
	}
}

// ─── tools/call — harness.inject (stub) ──────────────────────────────────────

func TestToolsCall_Inject_Stub(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":40,"method":"tools/call","params":{"name":"harness.inject","arguments":{"thread_id":"abc-123","envelope":{}}}}`
	got := roundtrip(t, srv, req)

	result, ok := got["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", got["result"])
	}
	// Stub is a valid tools/call response (isError:false), but payload ok:false.
	if result["isError"] != false {
		t.Errorf("isError = %v, want false (stub is a valid response)", result["isError"])
	}
	content := result["content"].([]interface{})
	item := content[0].(map[string]interface{})
	text, _ := item["text"].(string)
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("inject response text not JSON: %v\ntext: %s", err, text)
	}
	if payload["ok"] != false {
		t.Errorf("inject payload ok = %v, want false (stub)", payload["ok"])
	}
	if payload["error"] == "" || payload["error"] == nil {
		t.Errorf("inject payload missing error field")
	}
}

// ─── Protocol error cases ─────────────────────────────────────────────────────

func TestUnknownMethod_MethodNotFound(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":99,"method":"unknown/method","params":{}}`
	got := roundtrip(t, srv, req)

	errObj, ok := got["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error object, got result: %v", got)
	}
	code := int(errObj["code"].(float64))
	if code != -32601 {
		t.Errorf("error.code = %d, want -32601 (MethodNotFound)", code)
	}
}

func TestUnknownTool_MethodNotFound(t *testing.T) {
	srv := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":100,"method":"tools/call","params":{"name":"nonexistent.tool","arguments":{}}}`
	got := roundtrip(t, srv, req)

	errObj, ok := got["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error object for unknown tool, got: %v", got)
	}
	code := int(errObj["code"].(float64))
	if code != -32601 {
		t.Errorf("error.code = %d, want -32601 (MethodNotFound for unknown tool)", code)
	}
}

func TestMalformedJSON_ParseError(t *testing.T) {
	srv := newTestServer(t)
	in := bytes.NewBufferString("{not valid json}\n")
	var out bytes.Buffer
	_ = srv.Serve(context.Background(), in, &out)

	outBytes := bytes.TrimSpace(out.Bytes())
	if len(outBytes) == 0 {
		t.Fatal("expected an error response for malformed JSON, got empty output")
	}
	var got map[string]interface{}
	if err := json.Unmarshal(outBytes, &got); err != nil {
		t.Fatalf("decode error response: %v\nraw: %s", err, out.String())
	}
	errObj, ok := got["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("error not a map: %T", got["error"])
	}
	code := int(errObj["code"].(float64))
	if code != -32700 {
		t.Errorf("error.code = %d, want -32700 (ParseError)", code)
	}
}

// ─── A3 invariant — pure request/response, exactly one line per call ─────────

// TestA3_PureRequestResponse confirms that each tools/call produces exactly one
// JSON output line (no streaming, no progress notifications), verifying
// assumption A3 (MCP 2025-03-26 tools are pure request/response).
func TestA3_PureRequestResponse(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"harness.plan_from_prompt", `{"prompt":"test"}`},
		{"harness.verify", `{"envelope_path":"/nonexistent.json"}`},
		{"harness.inject", `{"thread_id":"t1","envelope":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			req := fmt.Sprintf(
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
				tc.name, tc.args,
			)
			in := bytes.NewBufferString(req + "\n")
			var out bytes.Buffer
			_ = srv.Serve(context.Background(), in, &out)

			lines := bytes.Split(bytes.TrimRight(out.Bytes(), "\n"), []byte("\n"))
			var nonEmpty [][]byte
			for _, l := range lines {
				if len(bytes.TrimSpace(l)) > 0 {
					nonEmpty = append(nonEmpty, l)
				}
			}
			if len(nonEmpty) != 1 {
				t.Errorf("%s: got %d response lines, want exactly 1 (A3: pure request/response)", tc.name, len(nonEmpty))
			}
		})
	}
}
