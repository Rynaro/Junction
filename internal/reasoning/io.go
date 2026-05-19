package reasoning

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// readPromptBundle reads and validates outDir/prompt-bundle.json.
//
// Validation rules (§2.1):
//   - schema_version must be "1.0"
//   - len(user_messages) >= 1
//   - every user_messages[i].content.type must be "text"
//   - max_tokens must be > 0
func readPromptBundle(outDir string) (*PromptBundle, error) {
	p := filepath.Join(outDir, "prompt-bundle.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	var b PromptBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parsing prompt-bundle.json: %w", err)
	}

	// Validation.
	if b.SchemaVersion != "1.0" {
		return nil, fmt.Errorf("prompt-bundle.json: schema_version must be \"1.0\", got %q", b.SchemaVersion)
	}
	if len(b.UserMessages) < 1 {
		return nil, fmt.Errorf("prompt-bundle.json: user_messages must have at least one entry")
	}
	for i, m := range b.UserMessages {
		if m.Content.Type != "text" {
			return nil, fmt.Errorf("prompt-bundle.json: user_messages[%d].content.type must be \"text\", got %q", i, m.Content.Type)
		}
	}
	if b.MaxTokens <= 0 {
		return nil, fmt.Errorf("prompt-bundle.json: max_tokens must be > 0, got %d", b.MaxTokens)
	}

	return &b, nil
}

// writeReasoning atomically writes r as JSON to inDir/reasoning.json.
//
// The write sequence is:
//  1. Write to inDir/.reasoning.json.tmp-<pid> with 0o644 permissions.
//  2. fsync the temp file (best-effort).
//  3. os.Rename the temp file to inDir/reasoning.json.
//
// A concurrent reader either sees no file or the complete Reasoning, never a
// partial write.
func writeReasoning(inDir string, r *Reasoning) error {
	// Set generated_at if not already populated by the provider.
	if r.GeneratedAt == "" {
		r.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}

	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("reasoning: marshal reasoning: %w", err)
	}

	// Use PID + random suffix to avoid collisions when multiple goroutines
	// concurrently write reasoning.json to the same directory.
	tmpName := filepath.Join(inDir, fmt.Sprintf(".reasoning.json.tmp-%d-%d", os.Getpid(), rand.Int63()))
	if err := os.WriteFile(tmpName, data, 0o644); err != nil {
		return fmt.Errorf("reasoning: write temp file %s: %w", tmpName, err)
	}

	// fsync the temp file (best-effort — on some filesystems Rename already
	// implies flush, but explicit fsync is safer).
	if f, openErr := os.Open(tmpName); openErr == nil {
		_ = f.Sync()
		_ = f.Close()
	}

	dst := filepath.Join(inDir, "reasoning.json")
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName) // best-effort cleanup
		return fmt.Errorf("reasoning: rename to reasoning.json: %w", err)
	}
	return nil
}
