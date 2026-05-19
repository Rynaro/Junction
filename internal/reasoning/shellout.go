package reasoning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// shelloutProvider pipes the PromptBundle JSON to an external CLI and
// unmarshals its stdout as a Reasoning. This is the OQ-22 headless-CI path.
//
// Contract:
//   - stdin:  PromptBundle JSON bytes
//   - stdout: complete Reasoning JSON document
//   - Non-JSON stdout → error with snippet
//   - Non-zero exit  → error with exit code + stderr snippet
type shelloutProvider struct {
	argv    []string
	timeout time.Duration
	// cmdRunner is the injection point for tests; nil means exec.CommandContext.
	cmdRunner shellCmdRunner
}

// shellCmdRunner abstracts exec.CommandContext for testability.
type shellCmdRunner func(ctx context.Context, name string, args ...string) shellCmd

// shellCmd is the minimal interface of exec.Cmd that shelloutProvider uses.
type shellCmd interface {
	setStdin([]byte)
	output() ([]byte, []byte, error)
}

// realShellCmd wraps exec.Cmd to satisfy shellCmd.
type realShellCmd struct {
	cmd *exec.Cmd
}

func (r *realShellCmd) setStdin(data []byte) {
	r.cmd.Stdin = bytes.NewReader(data)
}

func (r *realShellCmd) output() ([]byte, []byte, error) {
	var stderr bytes.Buffer
	r.cmd.Stderr = &stderr
	out, err := r.cmd.Output()
	return out, stderr.Bytes(), err
}

func newShelloutProvider(cmdStr string, timeout time.Duration) *shelloutProvider {
	argv := strings.Fields(cmdStr)
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &shelloutProvider{argv: argv, timeout: timeout}
}

// Reason marshals bundle to JSON, pipes it to the external CLI, and unmarshals
// stdout into a Reasoning. SourceProvider is forced to "shellout".
func (s *shelloutProvider) Reason(ctx context.Context, bundle *PromptBundle) (*Reasoning, error) {
	if len(s.argv) == 0 {
		return nil, fmt.Errorf("reasoning: shellout: empty command")
	}

	stdinData, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("reasoning: shellout: marshalling bundle: %w", err)
	}

	// Apply per-request timeout.
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var cmd shellCmd
	if s.cmdRunner != nil {
		cmd = s.cmdRunner(ctx, s.argv[0], s.argv[1:]...)
	} else {
		cmd = &realShellCmd{cmd: exec.CommandContext(ctx, s.argv[0], s.argv[1:]...)}
	}
	cmd.setStdin(stdinData)

	out, stderr, runErr := cmd.output()
	if runErr != nil {
		stderrSnippet := snippet(string(stderr), 200)
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return nil, fmt.Errorf("reasoning: shellout: command exited %d: %s", exitErr.ExitCode(), stderrSnippet)
		}
		return nil, fmt.Errorf("reasoning: shellout: executing command: %w: %s", runErr, stderrSnippet)
	}

	var r Reasoning
	if err := json.Unmarshal(out, &r); err != nil {
		outSnippet := snippet(string(out), 200)
		return nil, fmt.Errorf("reasoning: shellout: stdout is not valid Reasoning JSON: %w (got: %s)", err, outSnippet)
	}

	r.StepID = bundle.StepID
	r.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	r.SourceProvider = "shellout"
	return &r, nil
}

func (s *shelloutProvider) Name() string { return "shellout" }

// snippet returns at most maxLen bytes of s, appending "..." if truncated.
func snippet(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
