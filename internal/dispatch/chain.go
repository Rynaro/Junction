// Package dispatch — ChainExecutor for sequential TRANCE chain dispatch (S2.2).
//
// ChainExecutor runs an ordered list of {eidolon, envelope} pairs via an
// underlying Executor (typically ContainerExecutor). Each step's output
// envelope is threaded as the next step's input. Every envelope is validated
// against the directed-edge contract via the supplied contract.Registry before
// the next step starts.
//
// Chain stops on the first failed validation or dispatch; the error message
// names the failing step index and step ID.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Rynaro/Junction/internal/contract"
)

// ChainStep describes a single step in a sequential dispatch chain.
type ChainStep struct {
	// StepID is the human-readable identifier (e.g. "S0", "S1").
	StepID string

	// Eidolon is the slug of the Eidolon to dispatch.
	Eidolon string

	// Subcommand is the entry-point subcommand (may be empty for default).
	Subcommand string

	// From is the sender identity for contract edge validation ("human", "atlas", …).
	From string

	// To is the receiver identity for contract edge validation.
	To string

	// Performative is the ECL performative for this edge (e.g. "REQUEST", "PROPOSE").
	Performative string

	// InitialEnvelopePath is the input envelope path for this step.
	// If empty (for steps after the first), the previous step's output envelope
	// is used automatically.
	InitialEnvelopePath string

	// Env holds extra KEY=VALUE env vars for this step.
	Env []string
}

// ChainResult aggregates the per-step results of a chain run.
type ChainResult struct {
	// Steps holds the Result for each step, in order. Steps that did not
	// run (chain failed before them) are absent.
	Steps []Result
}

// ErrChainStepFailed wraps a step-level failure with the failing step index.
var ErrChainStepFailed = errors.New("dispatch: chain step failed")

// ErrChainContractViolation wraps a contract check failure on a chain edge.
var ErrChainContractViolation = errors.New("dispatch: chain contract violation")

// ChainExecutor runs a sequential chain of Eidolon dispatches. Each step's
// output envelope is fed as the next step's input. Contract conformance is
// checked on each edge before dispatch.
type ChainExecutor struct {
	// Executor is the underlying single-step executor (ContainerExecutor or
	// ShellExecutor). Required.
	Executor Executor

	// Registry is used for L3/L4 contract checks on each edge. If nil,
	// contract checking is skipped (useful for unit tests that want to test
	// the chain mechanics only).
	Registry *contract.Registry

	// ThreadID is propagated to every step.
	ThreadID string

	// BaseOutputDir is the root directory under which per-step output
	// directories are created: <BaseOutputDir>/<stepID>/out/.
	BaseOutputDir string
}

// Execute runs the chain steps in order. It returns ChainResult and the first
// error encountered (wrapped with ErrChainStepFailed or ErrChainContractViolation).
func (c *ChainExecutor) Execute(ctx context.Context, steps []ChainStep) (ChainResult, error) {
	var chainResult ChainResult
	prevOutputEnvelope := ""

	for i, step := range steps {
		// Contract check (L3/L4) before dispatch.
		if c.Registry != nil && step.From != "" && step.To != "" {
			if err := c.Registry.Check(step.From, step.To, step.Performative); err != nil {
				return chainResult, fmt.Errorf("%w: step %d (%s) — %v",
					ErrChainContractViolation, i, step.StepID, err)
			}
		}

		// Determine input envelope: explicit for first step; previous output otherwise.
		inputEnvelope := step.InitialEnvelopePath
		if inputEnvelope == "" {
			inputEnvelope = prevOutputEnvelope
		}

		outputDir := filepath.Join(c.BaseOutputDir, step.StepID, "out")

		req := Request{
			StepID:       step.StepID,
			Eidolon:      step.Eidolon,
			Subcommand:   step.Subcommand,
			EnvelopePath: inputEnvelope,
			ThreadID:     c.ThreadID,
			OutputDir:    outputDir,
			Env:          step.Env,
		}

		result, err := c.Executor.Execute(ctx, req)
		if err != nil {
			return chainResult, fmt.Errorf("%w: step %d (%s) — %v",
				ErrChainStepFailed, i, step.StepID, err)
		}

		chainResult.Steps = append(chainResult.Steps, result)
		prevOutputEnvelope = result.OutputEnvelopePath
	}

	return chainResult, nil
}
