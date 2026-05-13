package envelope

import "testing"

// TestPlaceholder exists only to make `go test ./...` exit 0 during
// Phase 0 bootstrap. APIVR-Δ replaces this with the real envelope
// round-trip test in F1.
func TestPlaceholder(t *testing.T) {
	t.Log("Phase 0 bootstrap — toolchain check only")
}
