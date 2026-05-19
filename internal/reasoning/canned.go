package reasoning

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// cannedProvider returns a fixed Reasoning sourced from a file on disk.
// The fixture is read and parsed at construction time; each Reason call
// returns a copy of the parsed value with a fresh GeneratedAt timestamp.
// This is the test-mode provider.
type cannedProvider struct {
	base Reasoning
}

// newCannedProvider reads and parses the Reasoning fixture at path.
// Returns an error if the file is missing, unreadable, or not valid JSON.
func newCannedProvider(path string) (*cannedProvider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reasoning: canned: reading %s: %w", path, err)
	}
	var r Reasoning
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("reasoning: canned: parsing %s: %w", path, err)
	}
	return &cannedProvider{base: r}, nil
}

// Reason returns a copy of the parsed fixture with a fresh GeneratedAt and
// SourceProvider == "canned".
func (c *cannedProvider) Reason(_ context.Context, bundle *PromptBundle) (*Reasoning, error) {
	r := c.base
	r.StepID = bundle.StepID
	r.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	r.SourceProvider = "canned"
	return &r, nil
}

func (c *cannedProvider) Name() string { return "canned" }
