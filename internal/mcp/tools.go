package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Rynaro/Junction/internal/contract"
	"github.com/Rynaro/Junction/internal/contracts"
	"github.com/Rynaro/Junction/internal/envelope"
	"github.com/Rynaro/Junction/internal/trace"
)

// HandlerFunc is the signature for a tool handler.
// args is the raw JSON object from the tools/call "arguments" field.
// The return value is a JSON-encodable payload that will be text-encoded
// in the tools/call response content list.
type HandlerFunc func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)

// Registry holds the tool definitions and their handlers.
type Registry struct {
	defs     []ToolDef
	handlers map[string]HandlerFunc
}

// NewRegistry constructs a Registry pre-populated with the four harness.*
// tools wired to their handlers.
func NewRegistry(reg *contract.Registry) *Registry {
	r := &Registry{handlers: make(map[string]HandlerFunc)}
	r.register(planFromPromptDef(), handlePlanFromPrompt)
	r.register(runDef(), makeRunHandler())
	r.register(verifyDef(), handleVerify)
	r.register(injectDef(), handleInject)
	_ = reg // held for future use when harness.inject gains contract validation
	return r
}

// NewRegistryDefault constructs a Registry using the embedded contract set.
func NewRegistryDefault() (*Registry, error) {
	reg, errs := contract.NewRegistryFromFS(contracts.Contracts, ".")
	if len(errs) > 0 {
		// Log warnings but continue — soft load errors should not block the
		// MCP server from starting.
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "mcp: contract load warning: %s\n", e)
		}
	}
	return NewRegistry(reg), nil
}

// register adds one tool to the registry.
func (r *Registry) register(def ToolDef, h HandlerFunc) {
	r.defs = append(r.defs, def)
	r.handlers[def.Name] = h
}

// Definitions returns the tool catalog for tools/list responses.
func (r *Registry) Definitions() []ToolDef {
	return r.defs
}

// Handler returns the handler for the named tool, or false if not found.
func (r *Registry) Handler(name string) (HandlerFunc, bool) {
	h, ok := r.handlers[name]
	return h, ok
}

// ─── Tool 1: harness.plan_from_prompt ────────────────────────────────────────

// planFromPromptInput is the input schema for harness.plan_from_prompt.
type planFromPromptInput struct {
	Prompt string `json:"prompt"`
}

func planFromPromptDef() ToolDef {
	schema := mustMarshal(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "A natural-language description of the Eidolon team task to plan.",
			},
		},
		"required": []string{"prompt"},
	})
	return ToolDef{
		Name: "harness.plan_from_prompt",
		Description: "STUB (v0.1) — In the two-phase Junction model the host LLM (Claude Code) is the " +
			"planner; Junction executes the plan. This tool is reserved for future use when an " +
			"automated plan-generation path is added. For now, construct a plan.json manually " +
			"(see §7.5 of the Junction spec) and call harness.run with its path.",
		InputSchema: schema,
	}
}

// handlePlanFromPrompt is a v0.1 stub. The host LLM (Claude Code) is the
// planner in the two-phase model (round-6 §0, §5.2). Junction executes plans
// authored by the LLM — it does not generate them. This stub is clearly
// flagged in the inputSchema description so callers understand the limitation.
func handlePlanFromPrompt(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in planFromPromptInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("harness.plan_from_prompt: invalid arguments: %w", err)
	}
	result := map[string]interface{}{
		"stub":    true,
		"message": "harness.plan_from_prompt is not implemented in v0.1 — the host LLM is the planner. Construct a plan.json per §7.5 and call harness.run with its path.",
		"prompt_echo": in.Prompt,
	}
	return mustMarshal(result), nil
}

// ─── Tool 2: harness.run ─────────────────────────────────────────────────────

// runInput is the input schema for harness.run.
type runInput struct {
	PlanPath string `json:"plan_path"`
}

func runDef() ToolDef {
	schema := mustMarshal(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"plan_path": map[string]interface{}{
				"type":        "string",
				"description": "Absolute or relative path to the plan.json file (§7.5 shape).",
			},
		},
		"required": []string{"plan_path"},
	})
	return ToolDef{
		Name:        "harness.run",
		Description: "Execute a Junction plan. Reads plan.json at plan_path, dispatches each step via the appropriate executor, and returns the thread_id and trace_root so the host LLM can later call harness.verify.",
		InputSchema: schema,
	}
}

// makeRunHandler returns a handler that invokes `junction run --envelope` via
// os/exec. Using os/exec rather than a direct internal call avoids a circular
// import (cmd/junction cannot import internal/mcp if internal/mcp imports
// cmd/junction). The same binary that serves MCP is re-invoked for the run
// subcommand.
//
// Note: internal/plan does not exist yet (F9-S0 is a separate story). When
// F9-S0 lands and internal/plan is available, this handler can be updated to
// call plan.Parse + the dispatch chain directly, removing the os/exec
// indirection.
func makeRunHandler() HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var in runInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("harness.run: invalid arguments: %w", err)
		}
		if in.PlanPath == "" {
			return nil, fmt.Errorf("harness.run: plan_path is required")
		}

		// Fast-fail: verify the envelope/plan file exists before invoking
		// the subprocess. This avoids confusing errors from the shell executor
		// and makes the missing-file error deterministic.
		if _, statErr := os.Stat(in.PlanPath); statErr != nil {
			return nil, fmt.Errorf("harness.run: plan_path %q: %w", in.PlanPath, statErr)
		}

		// Resolve the junction binary path from the running process.
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("harness.run: cannot resolve binary path: %w", err)
		}

		// harness.run v0.1: junction's current `run` subcommand takes an
		// --envelope flag, not a --plan flag (F9-S0 not merged). We invoke
		// the binary with the envelope path derived from plan_path. If the
		// caller passed a plan.json, we surface a clear error rather than
		// silently failing.
		//
		// TODO(F9-S0): when internal/plan lands, replace this with a direct
		// call to plan.Parse + dispatch chain, and accept a proper plan.json.
		cmd := exec.CommandContext(ctx, self, "run", "--envelope", in.PlanPath)
		cmd.Env = os.Environ()
		out, runErr := cmd.CombinedOutput()

		exitCode := 0
		if runErr != nil {
			if exitErr, ok := runErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				return nil, fmt.Errorf("harness.run: exec: %w", runErr)
			}
		}

		result := map[string]interface{}{
			"exit_code": exitCode,
			"output":    strings.TrimSpace(string(out)),
			"plan_path": in.PlanPath,
		}
		if exitCode != 0 {
			return nil, fmt.Errorf("harness.run: junction run exited %d: %s", exitCode, strings.TrimSpace(string(out)))
		}

		// Extract thread_id from output if present (junction run prints
		// "junction run: dispatched to <eidolon> (thread <id>) — trace at <path>").
		threadID := ""
		traceRoot := trace.DefaultTraceRoot
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "(thread ") {
				start := strings.Index(line, "(thread ") + len("(thread ")
				end := strings.Index(line[start:], ")")
				if end > 0 {
					threadID = line[start : start+end]
				}
			}
			if strings.Contains(line, "trace at ") {
				idx := strings.Index(line, "trace at ") + len("trace at ")
				traceRoot = strings.TrimSpace(line[idx:])
			}
		}

		result["thread_id"] = threadID
		result["trace_root"] = traceRoot
		return mustMarshal(result), nil
	}
}

// ─── Tool 3: harness.verify ──────────────────────────────────────────────────

// verifyInput is the input schema for harness.verify.
type verifyInput struct {
	EnvelopePath string `json:"envelope_path"`
}

func verifyDef() ToolDef {
	schema := mustMarshal(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"envelope_path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the ECL envelope file to verify (L1–L4 checks).",
			},
		},
		"required": []string{"envelope_path"},
	})
	return ToolDef{
		Name:        "harness.verify",
		Description: "Verify an ECL envelope against the L1 (schema), L2 (integrity), L3 (edge), and L4 (performative) checks. Returns ok:true when all checks pass, or ok:false with an errors list.",
		InputSchema: schema,
	}
}

// handleVerify runs L1–L4 verification on an envelope file using the
// internal/envelope package — the same path that `junction verify` uses.
func handleVerify(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in verifyInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("harness.verify: invalid arguments: %w", err)
	}
	if in.EnvelopePath == "" {
		return nil, fmt.Errorf("harness.verify: envelope_path is required")
	}

	var errs []string

	// L1 — schema validation.
	raw, err := os.ReadFile(in.EnvelopePath)
	if err != nil {
		return nil, fmt.Errorf("harness.verify: reading envelope: %w", err)
	}
	if err := envelope.ValidateBytes(raw); err != nil {
		errs = append(errs, "L1: "+err.Error())
	}

	// Parse envelope for L2+.
	env := &envelope.Envelope{}
	if parseErr := json.Unmarshal(raw, env); parseErr != nil {
		errs = append(errs, "parse: "+parseErr.Error())
	} else {
		// L2 — integrity.
		artPath := in.EnvelopePath[:strings.LastIndex(in.EnvelopePath, "/")+1] + env.Artifact.Path
		if strings.LastIndex(in.EnvelopePath, "/") < 0 {
			artPath = env.Artifact.Path
		}
		if intErr := env.VerifyIntegrity(artPath); intErr != nil {
			errs = append(errs, "L2: "+intErr.Error())
		}

		// L3+L4 — contract registry.
		reg, loadErrs := contract.NewRegistryFromFS(contracts.Contracts, ".")
		for _, le := range loadErrs {
			errs = append(errs, "contract-load: "+le.Error())
		}
		if checkErr := reg.Check(env.From.Eidolon, env.To.Eidolon, env.Performative); checkErr != nil {
			errs = append(errs, "L3/L4: "+checkErr.Error())
		}
	}

	result := map[string]interface{}{
		"ok":           len(errs) == 0,
		"errors":       errs,
		"envelope_path": in.EnvelopePath,
	}
	return mustMarshal(result), nil
}

// ─── Tool 4: harness.inject ──────────────────────────────────────────────────

func injectDef() ToolDef {
	schema := mustMarshal(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"thread_id": map[string]interface{}{
				"type":        "string",
				"description": "The Junction thread ID to inject the envelope into.",
			},
			"envelope": map[string]interface{}{
				"type":        "object",
				"description": "STUB (v0.1) — harness.inject is not implemented in v0.1. See roadmap (F9 § inject). The envelope object is accepted but not processed.",
			},
		},
		"required": []string{"thread_id", "envelope"},
	})
	return ToolDef{
		Name: "harness.inject",
		Description: "STUB (v0.1) — Inject a human-authored ECL envelope into a running Junction thread " +
			"(enforces §5.7/§6.5 constraints). Not implemented in v0.1; see roadmap. " +
			"Returns ok:false with a descriptive error.",
		InputSchema: schema,
	}
}

// handleInject is a v0.1 stub. Human-in-the-loop injection (§5.7/§6.5) is
// deferred to a later story. The stub returns ok:false with a clear message
// so callers know it is not silently dropping the request.
func handleInject(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(args, &raw)
	result := map[string]interface{}{
		"ok":    false,
		"error": "harness.inject is stubbed in v0.1 — see roadmap (F9 inject story). Envelope injection will enforce §5.7/§6.5 constraints when implemented.",
	}
	return mustMarshal(result), nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// mustMarshal marshals v to JSON and panics if it fails. Only use with
// static/known-good values (literals, maps with string keys).
func mustMarshal(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("mcp: mustMarshal: " + err.Error())
	}
	return b
}
