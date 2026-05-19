package reasoning_test

// io_test.go — tests for readPromptBundle validation logic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rynaro/Junction/internal/reasoning"
)

// TestReadPromptBundle_Validation exercises the validation rules in §2.1.
func TestReadPromptBundle_Validation(t *testing.T) {
	t.Parallel()

	writeBundle := func(t *testing.T, b any) string {
		t.Helper()
		dir := t.TempDir()
		data, _ := json.Marshal(b)
		if err := os.WriteFile(filepath.Join(dir, "prompt-bundle.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	cases := []struct {
		name    string
		bundle  any
		wantErr bool
	}{
		{
			name: "valid bundle",
			bundle: reasoning.PromptBundle{
				SchemaVersion: "1.0",
				StepID:        "S0",
				UserMessages:  []reasoning.UserMessage{{Role: "user", Content: reasoning.TextContent{Type: "text", Text: "hi"}}},
				MaxTokens:     1,
			},
			wantErr: false,
		},
		{
			name: "wrong schema_version",
			bundle: map[string]any{
				"schema_version": "2.0",
				"step_id":        "S0",
				"user_messages":  []any{map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": "hi"}}},
				"max_tokens":     1,
			},
			wantErr: true,
		},
		{
			name: "no user_messages",
			bundle: map[string]any{
				"schema_version": "1.0",
				"step_id":        "S0",
				"user_messages":  []any{},
				"max_tokens":     1,
			},
			wantErr: true,
		},
		{
			name: "non-text content type",
			bundle: map[string]any{
				"schema_version": "1.0",
				"step_id":        "S0",
				"user_messages":  []any{map[string]any{"role": "user", "content": map[string]any{"type": "image", "text": ""}}},
				"max_tokens":     1,
			},
			wantErr: true,
		},
		{
			name: "zero max_tokens",
			bundle: map[string]any{
				"schema_version": "1.0",
				"step_id":        "S0",
				"user_messages":  []any{map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": "hi"}}},
				"max_tokens":     0,
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			outDir := writeBundle(t, tc.bundle)

			// Use NewReasoningStepFunc with a noop provider to exercise readPromptBundle.
			p, _ := reasoning.NewProvider(reasoning.Config{Provider: "none"})
			fn := reasoning.NewReasoningStepFunc(p)

			err := fn(nil, "S0", t.TempDir(), outDir)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
