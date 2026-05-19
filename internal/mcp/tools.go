package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Rynaro/Junction/internal/contract"
	"github.com/Rynaro/Junction/internal/contracts"
	"github.com/Rynaro/Junction/internal/dispatch"
	"github.com/Rynaro/Junction/internal/envelope"
	"github.com/Rynaro/Junction/internal/plan"
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

// makeRunHandler returns a handler that executes a Junction plan or single
// envelope. It uses shape detection: if the file at plan_path can be parsed
// as a plan.json (§7.5), it dispatches in-process via plan.Parse +
// dispatch.ChainExecutor; otherwise it falls back to invoking the junction
// binary with --envelope for single-envelope back-compat.
//
// thread_id and trace_root are derived from the actual dispatch result — not
// scraped from subprocess stdout.
func makeRunHandler() HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var in runInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("harness.run: invalid arguments: %w", err)
		}
		if in.PlanPath == "" {
			return nil, fmt.Errorf("harness.run: plan_path is required")
		}

		// Fast-fail: verify the file exists before any further processing.
		if _, statErr := os.Stat(in.PlanPath); statErr != nil {
			return nil, fmt.Errorf("harness.run: plan_path %q: %w", in.PlanPath, statErr)
		}

		// Shape detection: try to parse as plan.json; on success dispatch in-process.
		raw, err := os.ReadFile(in.PlanPath)
		if err != nil {
			return nil, fmt.Errorf("harness.run: reading file: %w", err)
		}
		p, planErr := plan.Parse(bytes.NewReader(raw))
		if planErr == nil {
			return runPlanInProcess(ctx, p, in.PlanPath)
		}

		// Fall back to single-envelope shell-out (backward-compat).
		return runEnvelopeShellOut(ctx, in.PlanPath)
	}
}

// runPlanInProcess executes a parsed plan.json in-process using
// plan.SelectExecutor + dispatch.ChainExecutor. Returns thread_id and
// trace_root from the actual dispatch result.
func runPlanInProcess(ctx context.Context, p plan.Plan, planPath string) (json.RawMessage, error) {
	traceRoot := trace.DefaultTraceRoot

	reg, _ := contract.NewRegistryFromFS(contracts.Contracts, ".")
	opts := plan.ExecutorOptions{
		ProjectDir: cwd(),
		CacheDir:   eidolonsCacheDirMCP(),
	}
	mode := plan.ModeFromString(p.Executor)
	// MCP server always runs with --no-container=false; ShellExecutor is
	// chosen if plan.executor == "shell", otherwise ContainerExecutor.
	innerExec := plan.SelectExecutor(mode, false, opts)

	chain := &dispatch.ChainExecutor{
		Executor:      innerExec,
		Registry:      reg,
		ThreadID:      p.ThreadID,
		BaseOutputDir: filepath.Join(traceRoot, p.ThreadID),
	}

	steps := p.ToChainSteps()
	_, chainErr := chain.Execute(ctx, steps)

	actualTraceRoot := filepath.Join(traceRoot, p.ThreadID)
	result := map[string]interface{}{
		"plan_path":  planPath,
		"thread_id":  p.ThreadID,
		"trace_root": actualTraceRoot,
		"step_count": len(steps),
	}
	if chainErr != nil {
		return nil, fmt.Errorf("harness.run: plan chain: %w", chainErr)
	}
	return mustMarshal(result), nil
}

// runEnvelopeShellOut invokes the junction binary with --envelope for the
// single-envelope back-compat path.
func runEnvelopeShellOut(ctx context.Context, envelopePath string) (json.RawMessage, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("harness.run: cannot resolve binary path: %w", err)
	}

	cmd := exec.CommandContext(ctx, self, "run", "--envelope", envelopePath)
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
	if exitCode != 0 {
		return nil, fmt.Errorf("harness.run: junction run exited %d: %s", exitCode, strings.TrimSpace(string(out)))
	}

	// For single-envelope mode, thread_id and trace_root are not available
	// in-process (we don't parse the envelope here to avoid duplication with
	// the subprocess). Return the defaults.
	result := map[string]interface{}{
		"exit_code":  exitCode,
		"plan_path":  envelopePath,
		"thread_id":  "",
		"trace_root": trace.DefaultTraceRoot,
	}
	return mustMarshal(result), nil
}

// cwd returns the current working directory for executor construction.
func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

// eidolonsCacheDirMCP returns the default ~/.eidolons/cache directory.
func eidolonsCacheDirMCP() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".eidolons", "cache")
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

		// L3+L4 — contract registry (edge_origin aware).
		reg, loadErrs := contract.NewRegistryFromFS(contracts.Contracts, ".")
		for _, le := range loadErrs {
			errs = append(errs, "contract-load: "+le.Error())
		}
		if checkErr := reg.CheckWithOrigin(env.From.Eidolon, env.To.Eidolon, env.Performative, env.EdgeOrigin); checkErr != nil {
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
