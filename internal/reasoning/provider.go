// Package reasoning implements the host-LLM reasoning step for Junction's
// two-phase container orchestration (F10-S1 / v0.2).
//
// The package exposes a single Provider interface with four concrete
// implementations:
//
//   - mcp-sampling  — issues a sampling/createMessage request to the MCP host.
//   - canned        — returns a fixed Reasoning from a file on disk (test-mode).
//   - shellout      — shells out to an operator-supplied CLI (OQ-22 headless-CI).
//   - noop (none)   — returns nil; preserves v0.1 single-phase behaviour.
//
// Selection is driven by JUNCTION_REASONING_PROVIDER. Config is read at start
// time via LoadConfigFromEnv; a typed promotion path is provided for F9-S1.
//
// NG15: no new external dependencies. NG17: no direct LLM API call inside
// this package — mcp-sampling routes through the MCP host, shellout through an
// operator-owned CLI.
package reasoning

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Rynaro/Junction/internal/dispatch"
	"github.com/Rynaro/Junction/internal/mcp"
)

// ─── Core types ───────────────────────────────────────────────────────────────

// PromptBundle is the assemble-phase output written to outDir/prompt-bundle.json.
// schema_version must be "1.0" in v0.2.
type PromptBundle struct {
	SchemaVersion        string         `json:"schema_version"`
	StepID               string         `json:"step_id"`
	Objective            string         `json:"objective,omitempty"`
	SystemPrompt         string         `json:"system_prompt,omitempty"`
	UserMessages         []UserMessage  `json:"user_messages"`
	ModelHints           []string       `json:"model_hints,omitempty"`
	IntelligencePriority *float64       `json:"intelligence_priority,omitempty"`
	SpeedPriority        *float64       `json:"speed_priority,omitempty"`
	CostPriority         *float64       `json:"cost_priority,omitempty"`
	MaxTokens            int            `json:"max_tokens"`
	Temperature          *float64       `json:"temperature,omitempty"`
	StopSequences        []string       `json:"stop_sequences,omitempty"`
	Metadata             map[string]any `json:"metadata,omitempty"`
	ParentEnvelopeID     string         `json:"parent_envelope_id,omitempty"`
}

// UserMessage is one entry in PromptBundle.UserMessages.
type UserMessage struct {
	Role    string      `json:"role"`
	Content TextContent `json:"content"`
}

// TextContent is a typed text payload used in both PromptBundle and Reasoning.
type TextContent struct {
	Type string `json:"type"` // always "text" in v0.2
	Text string `json:"text"`
}

// Reasoning is the package-phase input written to inDir/reasoning.json.
// schema_version is "1.0" in v0.2.
type Reasoning struct {
	SchemaVersion  string         `json:"schema_version"`
	StepID         string         `json:"step_id"`
	Model          string         `json:"model"`
	StopReason     string         `json:"stop_reason"`
	Content        TextContent    `json:"content"`
	GeneratedAt    string         `json:"generated_at"`
	SourceProvider string         `json:"source_provider"`
	UsageHints     map[string]any `json:"usage_hints,omitempty"`
}

// ─── sampling/createMessage types ────────────────────────────────────────────

// SamplingCreateMessageParams is the verbatim mirror of the MCP 2025-06-18
// sampling/createMessage params shape.
// Reference: https://modelcontextprotocol.io/specification/2025-06-18/client/sampling
type SamplingCreateMessageParams struct {
	Messages         []SamplingMessage  `json:"messages"`
	ModelPreferences *ModelPreferences  `json:"modelPreferences,omitempty"`
	SystemPrompt     string             `json:"systemPrompt,omitempty"`
	MaxTokens        int                `json:"maxTokens"`
	Temperature      *float64           `json:"temperature,omitempty"`
	StopSequences    []string           `json:"stopSequences,omitempty"`
	Metadata         map[string]any     `json:"metadata,omitempty"`
}

// SamplingMessage is one entry in SamplingCreateMessageParams.Messages.
type SamplingMessage struct {
	Role    string          `json:"role"`
	Content SamplingContent `json:"content"`
}

// SamplingContent is the content body of a SamplingMessage.
// Type is always "text" in v0.2.
type SamplingContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ModelPreferences carries model-selection hints and priorities.
type ModelPreferences struct {
	Hints                []ModelHint `json:"hints,omitempty"`
	IntelligencePriority *float64    `json:"intelligencePriority,omitempty"`
	SpeedPriority        *float64    `json:"speedPriority,omitempty"`
	CostPriority         *float64    `json:"costPriority,omitempty"`
}

// ModelHint is a single model name hint.
type ModelHint struct {
	Name string `json:"name"`
}

// SamplingCreateMessageResult is the verbatim mirror of the MCP 2025-06-18
// sampling/createMessage response.
type SamplingCreateMessageResult struct {
	Role       string          `json:"role"`
	Content    SamplingContent `json:"content"`
	Model      string          `json:"model"`
	StopReason string          `json:"stopReason"`
}

// ─── Provider interface ───────────────────────────────────────────────────────

// Provider produces a Reasoning for a given PromptBundle. Implementations MAY
// invoke a host LLM via MCP sampling, shell out to a CLI, copy a fixture, or
// no-op — but they MUST NOT touch the host filesystem directly. I/O is the
// responsibility of NewReasoningStepFunc.
type Provider interface {
	// Reason produces a Reasoning for the given bundle. Returns nil if the
	// provider is a no-op (JUNCTION_REASONING_PROVIDER=none).
	Reason(ctx context.Context, bundle *PromptBundle) (*Reasoning, error)

	// Name returns the canonical provider name for error messages and logging.
	Name() string
}

// ─── Config ───────────────────────────────────────────────────────────────────

// Config is the typed projection of the env vars in §5 of the v0.2 spec.
// After F9-S1 lands this struct is the single shape downstream code reads from.
type Config struct {
	// Provider selects the implementation: "mcp-sampling", "canned",
	// "shellout", or "none" (default).
	Provider string

	// CannedPath is the path to a Reasoning JSON fixture file.
	// Required when Provider == "canned".
	CannedPath string

	// ShelloutCmd is the argv string for the external CLI (parsed by
	// strings.Fields; no shell interpolation).
	// Required when Provider == "shellout".
	ShelloutCmd string

	// Sampling is filled in at MCP server start. Not user-tunable via env.
	Sampling SamplingConfig
}

// SamplingConfig holds the MCP-server-provided closures for the mcp-sampling
// provider.
type SamplingConfig struct {
	// ClientCapabilities returns the cached capabilities from the last
	// initialize handshake. The provider re-reads it on every call.
	ClientCapabilities func() *mcp.ClientCapabilities

	// Request issues a server-initiated sampling/createMessage round-trip and
	// blocks until the client responds or ctx cancels.
	Request func(ctx context.Context, params *SamplingCreateMessageParams) (*SamplingCreateMessageResult, error)

	// Timeout is the per-request maximum. Default: 5 minutes.
	Timeout time.Duration
}

// LoadConfigFromEnv reads the JUNCTION_REASONING_* env vars and returns a
// Config. SamplingConfig is NOT populated here; the MCP server wires that
// after construction.
//
// Migration path: when F9-S1 lands, this function is replaced by
// FromAppConfig(appCfg *config.Config) Config. Env vars remain working.
func LoadConfigFromEnv() Config {
	cfg := Config{
		Provider:    os.Getenv("JUNCTION_REASONING_PROVIDER"),
		CannedPath:  os.Getenv("JUNCTION_REASONING_CANNED_PATH"),
		ShelloutCmd: os.Getenv("JUNCTION_REASONING_SHELLOUT_CMD"),
	}
	if cfg.Provider == "" {
		cfg.Provider = "none"
	}
	if raw := os.Getenv("JUNCTION_REASONING_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			cfg.Sampling.Timeout = d
		}
	}
	return cfg
}

// ─── Factory ─────────────────────────────────────────────────────────────────

// NewProvider validates cfg and returns the matching Provider.
// An unknown Provider string or missing required config is an error.
// An empty Provider string defaults to "none".
func NewProvider(cfg Config) (Provider, error) {
	if cfg.Provider == "" {
		cfg.Provider = "none"
	}
	switch cfg.Provider {
	case "none":
		return &noopProvider{}, nil

	case "canned":
		if cfg.CannedPath == "" {
			return nil, fmt.Errorf("reasoning: JUNCTION_REASONING_CANNED_PATH is required when JUNCTION_REASONING_PROVIDER=canned")
		}
		return newCannedProvider(cfg.CannedPath)

	case "shellout":
		if cfg.ShelloutCmd == "" {
			return nil, fmt.Errorf("reasoning: JUNCTION_REASONING_SHELLOUT_CMD is required when JUNCTION_REASONING_PROVIDER=shellout")
		}
		return newShelloutProvider(cfg.ShelloutCmd, cfg.Sampling.Timeout), nil

	case "mcp-sampling":
		if cfg.Sampling.ClientCapabilities == nil {
			return nil, fmt.Errorf("reasoning: mcp-sampling provider requires a running MCP server (use 'junction mcp serve' or pick canned/shellout/none)")
		}
		if cfg.Sampling.Request == nil {
			return nil, fmt.Errorf("reasoning: mcp-sampling provider requires SamplingConfig.Request to be set")
		}
		timeout := cfg.Sampling.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}
		return &mcpSamplingProvider{cfg: cfg.Sampling, timeout: timeout}, nil

	default:
		return nil, fmt.Errorf("reasoning: unknown provider %q — valid values: mcp-sampling, canned, shellout, none", cfg.Provider)
	}
}

// ─── Adapter ─────────────────────────────────────────────────────────────────

// NewReasoningStepFunc returns a dispatch.ReasoningStepFunc that reads
// prompt-bundle.json from outDir, calls p.Reason, and writes reasoning.json
// atomically to inDir.
//
// If p.Reason returns nil (noop provider), the function returns nil without
// writing any file, preserving v0.1 single-phase behaviour.
func NewReasoningStepFunc(p Provider) dispatch.ReasoningStepFunc {
	return func(ctx context.Context, stepID, inDir, outDir string) error {
		bundle, err := readPromptBundle(outDir)
		if err != nil {
			return fmt.Errorf("reasoning: read prompt-bundle.json: %w", err)
		}
		bundle.StepID = stepID // overlay the dispatch-supplied step ID

		r, err := p.Reason(ctx, bundle)
		if err != nil {
			return fmt.Errorf("reasoning: provider %q: %w", p.Name(), err)
		}
		if r == nil {
			// noop provider — no file written; single-phase behaviour preserved.
			return nil
		}
		return writeReasoning(inDir, r)
	}
}
