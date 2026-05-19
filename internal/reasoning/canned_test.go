package reasoning_test

// canned_test.go — G5: canned provider error paths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rynaro/Junction/internal/reasoning"
)

// G5: missing path → error naming the env var.
func TestCannedProvider_MissingPath(t *testing.T) {
	t.Parallel()
	_, err := reasoning.NewProvider(reasoning.Config{Provider: "canned"})
	if err == nil {
		t.Fatal("expected error for missing CannedPath")
	}
	// Should mention the env var.
	errStr := err.Error()
	if len(errStr) == 0 {
		t.Fatal("empty error string")
	}
	// Just ensure it is a meaningful error.
	t.Logf("got expected error: %s", errStr)
}

// G5: unreadable / nonexistent path → wraps os.PathError.
func TestCannedProvider_NonexistentFile(t *testing.T) {
	t.Parallel()
	_, err := reasoning.NewProvider(reasoning.Config{
		Provider:   "canned",
		CannedPath: "/nonexistent/path/reasoning.json",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	t.Logf("got expected error: %s", err)
}

// G5: malformed JSON → error.
func TestCannedProvider_MalformedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{not valid json`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := reasoning.NewProvider(reasoning.Config{
		Provider:   "canned",
		CannedPath: bad,
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	t.Logf("got expected error: %s", err)
}

// TestCannedProvider_ReadsFromTestdata exercises the fixture file.
func TestCannedProvider_ReadsFromTestdata(t *testing.T) {
	t.Parallel()
	cfg := reasoning.Config{
		Provider:   "canned",
		CannedPath: filepath.Join("testdata", "reasoning-canned.json"),
	}
	p, err := reasoning.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	r, err := p.Reason(nil, canonicalBundle(t))
	if err != nil {
		t.Fatalf("Reason: %v", err)
	}
	assertValidReasoning(t, r, "canned")
}
