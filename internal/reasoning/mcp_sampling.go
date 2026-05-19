package reasoning

import (
	"context"
	"fmt"
	"time"
)

// mcpSamplingProvider issues a sampling/createMessage request to the MCP host
// and parses the response into a Reasoning.
//
// The capability probe runs on every Reason call (per spec §4.3 — cheap, and
// survives any future race where the server is configured before a client
// connects).
type mcpSamplingProvider struct {
	cfg     SamplingConfig
	timeout time.Duration
}

// Reason probes client capabilities, builds sampling/createMessage params from
// the bundle, sends the request, and returns a Reasoning from the response.
func (p *mcpSamplingProvider) Reason(ctx context.Context, bundle *PromptBundle) (*Reasoning, error) {
	// Capability probe (per-call, per spec §4.3).
	caps := p.cfg.ClientCapabilities()
	if caps == nil || caps.Sampling == nil {
		return nil, fmt.Errorf(
			"reasoning: MCP host did not declare sampling capability " +
				"(client capabilities.sampling is absent); " +
				"configure JUNCTION_REASONING_PROVIDER=canned or shellout for non-sampling hosts, " +
				"or JUNCTION_REASONING_PROVIDER=none to keep the legacy single-phase behaviour")
	}

	// Build params from bundle.
	params := bundleToSamplingParams(bundle)

	// Apply per-request timeout.
	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	result, err := p.cfg.Request(reqCtx, params)
	if err != nil {
		return nil, fmt.Errorf("reasoning: mcp-sampling: %w", err)
	}

	if result.Content.Type != "text" {
		return nil, fmt.Errorf("reasoning: mcp-sampling: unexpected content type %q (only \"text\" supported in v0.2)", result.Content.Type)
	}

	return &Reasoning{
		SchemaVersion:  "1.0",
		StepID:         bundle.StepID,
		Model:          result.Model,
		StopReason:     result.StopReason,
		Content:        TextContent{Type: "text", Text: result.Content.Text},
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceProvider: "mcp-sampling",
	}, nil
}

func (p *mcpSamplingProvider) Name() string { return "mcp-sampling" }

// bundleToSamplingParams maps a PromptBundle to SamplingCreateMessageParams
// according to the field map in spec §2.3.
func bundleToSamplingParams(b *PromptBundle) *SamplingCreateMessageParams {
	params := &SamplingCreateMessageParams{
		MaxTokens: b.MaxTokens,
	}

	// systemPrompt: prepend objective if non-empty.
	systemPrompt := b.SystemPrompt
	if b.Objective != "" {
		if systemPrompt != "" {
			systemPrompt = b.Objective + "\n\n" + systemPrompt
		} else {
			systemPrompt = b.Objective
		}
	}
	params.SystemPrompt = systemPrompt

	// messages: each UserMessage → SamplingMessage.
	params.Messages = make([]SamplingMessage, len(b.UserMessages))
	for i, m := range b.UserMessages {
		params.Messages[i] = SamplingMessage{
			Role:    m.Role,
			Content: SamplingContent{Type: m.Content.Type, Text: m.Content.Text},
		}
	}

	// modelPreferences: only emit if there are hints or priorities.
	hasHints := len(b.ModelHints) > 0
	hasPriorities := b.IntelligencePriority != nil || b.SpeedPriority != nil || b.CostPriority != nil
	if hasHints || hasPriorities {
		mp := &ModelPreferences{
			IntelligencePriority: b.IntelligencePriority,
			SpeedPriority:        b.SpeedPriority,
			CostPriority:         b.CostPriority,
		}
		if hasHints {
			mp.Hints = make([]ModelHint, len(b.ModelHints))
			for i, h := range b.ModelHints {
				mp.Hints[i] = ModelHint{Name: h}
			}
		}
		params.ModelPreferences = mp
	}

	// Pass-through fields.
	params.Temperature = b.Temperature
	if len(b.StopSequences) > 0 {
		params.StopSequences = b.StopSequences
	}
	if len(b.Metadata) > 0 {
		params.Metadata = b.Metadata
	}

	return params
}

