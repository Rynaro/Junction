package reasoning

import "context"

// noopProvider returns nil on every Reason call. It is selected by
// JUNCTION_REASONING_PROVIDER=none (or the empty string) and preserves the
// v0.1 single-phase behaviour: no reasoning.json is written.
type noopProvider struct{}

func (n *noopProvider) Reason(_ context.Context, _ *PromptBundle) (*Reasoning, error) {
	return nil, nil
}

func (n *noopProvider) Name() string { return "none" }
