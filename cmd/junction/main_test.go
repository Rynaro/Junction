package main

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestVersionOutput verifies that --version prints the expected first line and
// optional commit/date lines when Commit and Date are set.
func TestVersionOutput(t *testing.T) {
	// expectedFirstLine returns "junction <version> (ECL <eclversion>)"
	wantFirstLine := fmt.Sprintf("junction %s (ECL %s)", Version, ECLVersion)

	tests := []struct {
		name           string
		commit         string
		date           string
		wantFirstLine  string
		wantCommitLine string
		wantDateLine   string
	}{
		{
			name:          "no metadata",
			commit:        "",
			date:          "",
			wantFirstLine: wantFirstLine,
		},
		{
			name:           "short commit (≤7 chars)",
			commit:         "abc1234",
			date:           "",
			wantFirstLine:  wantFirstLine,
			wantCommitLine: "commit: abc1234",
		},
		{
			name:           "long commit (>7 chars)",
			commit:         "abc1234567890abcdef",
			date:           "",
			wantFirstLine:  wantFirstLine,
			wantCommitLine: "commit: abc1234",
		},
		{
			name:          "with date",
			commit:        "",
			date:          "2026-05-15T00:00:00Z",
			wantFirstLine: wantFirstLine,
			wantDateLine:  "built:  2026-05-15T00:00:00Z",
		},
		{
			name:           "full metadata",
			commit:         "deadbeef12345678",
			date:           "2026-05-15T12:00:00Z",
			wantFirstLine:  wantFirstLine,
			wantCommitLine: "commit: deadbee",
			wantDateLine:   "built:  2026-05-15T12:00:00Z",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Save and restore package-level vars.
			origCommit := Commit
			origDate := Date
			origStdout := stdout
			defer func() {
				Commit = origCommit
				Date = origDate
				stdout = origStdout
			}()

			Commit = tc.commit
			Date = tc.date

			var buf bytes.Buffer
			stdout = &buf

			if err := run([]string{"--version"}); err != nil {
				t.Fatalf("run(--version) returned error: %v", err)
			}

			lines := splitLines(buf.String())
			if len(lines) == 0 {
				t.Fatal("--version produced no output")
			}

			// First line MUST match exactly (G-J0.1-2 grep target).
			if lines[0] != tc.wantFirstLine {
				t.Errorf("first line = %q, want %q", lines[0], tc.wantFirstLine)
			}

			// Check commit line if expected.
			if tc.wantCommitLine != "" {
				if !containsLine(lines, tc.wantCommitLine) {
					t.Errorf("output missing commit line %q; got:\n%s", tc.wantCommitLine, buf.String())
				}
			}

			// Check date line if expected.
			if tc.wantDateLine != "" {
				if !containsLine(lines, tc.wantDateLine) {
					t.Errorf("output missing date line %q; got:\n%s", tc.wantDateLine, buf.String())
				}
			}
		})
	}
}

// TestVersionFirstLineFormat is a focused check: the first line of --version
// output must be "junction <Version> (ECL <ECLVersion>)" so that the
// smoke-step assertion `head -n1 smoke.out | grep -Fxq "junction X.Y.Z (ECL 1.0.0)"`
// passes at any injected Version value.
func TestVersionFirstLineFormat(t *testing.T) {
	origStdout := stdout
	defer func() { stdout = origStdout }()

	var buf bytes.Buffer
	stdout = &buf

	if err := run([]string{"--version"}); err != nil {
		t.Fatalf("run(--version) error: %v", err)
	}

	first := firstLine(buf.String())
	want := fmt.Sprintf("junction %s (ECL %s)", Version, ECLVersion)
	if first != want {
		t.Errorf("first line of --version = %q, want %q", first, want)
	}
}

// splitLines returns non-empty trimmed lines.
func splitLines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := sc.Text()
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func containsLine(lines []string, target string) bool {
	for _, l := range lines {
		if l == target {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	sc := bufio.NewScanner(strings.NewReader(s))
	if sc.Scan() {
		return sc.Text()
	}
	return ""
}
