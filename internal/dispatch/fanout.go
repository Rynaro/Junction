// Package dispatch — FanoutExecutor for concurrent Eidolon dispatch (S2.3).
//
// FanoutExecutor runs N independent dispatches in parallel using a worker
// pool capped at a configurable concurrency limit (default runtime.NumCPU(),
// capped at MaxConcurrency=5 per the TRANCE cortex cap). It honors
// context.Context cancellation so one branch failure can cancel others.
//
// Each branch gets its own isolated output directory under:
//   <BaseOutputDir>/<parentStepID>/branch-<i>/{in,out}/
//
// This matches the spec §5.9 "Concurrent fan-out" layout.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
)

const (
	// MaxConcurrency is the hard cap on parallel branches per the TRANCE
	// cortex cap §6.4 C1. Override via JUNCTION_MAX_CONCURRENCY env var.
	MaxConcurrency = 5
)

// ErrParallelBranchesExceeded is returned when the requested branch count
// exceeds MaxConcurrency.
var ErrParallelBranchesExceeded = errors.New("dispatch: parallel branch count exceeds maximum (5)")

// BranchRequest describes one branch in a fan-out dispatch.
type BranchRequest struct {
	// Eidolon is the slug of the Eidolon to dispatch.
	Eidolon string

	// Subcommand is the entry-point subcommand (may be empty for default).
	Subcommand string

	// EnvelopePath is the input envelope for this branch.
	EnvelopePath string

	// Env holds extra KEY=VALUE env vars.
	Env []string
}

// BranchResult holds the result of one branch dispatch.
type BranchResult struct {
	// BranchIndex is the zero-based index in the original branches slice.
	BranchIndex int

	// Result is the dispatch result for this branch.
	Result

	// Err is non-nil if this branch failed.
	Err error
}

// FanoutResult aggregates all branch results.
type FanoutResult struct {
	// Branches holds one BranchResult per requested branch, in index order.
	// Branches that were cancelled before starting have a nil Result and
	// Err == context.Canceled.
	Branches []BranchResult
}

// FanoutExecutor dispatches N branches concurrently via a worker pool.
type FanoutExecutor struct {
	// Executor is the underlying single-step executor. Required.
	Executor Executor

	// ThreadID is propagated to all branches.
	ThreadID string

	// ParentStepID is used to build per-branch output dirs:
	// <BaseOutputDir>/<ParentStepID>/branch-<i>/{in,out}/.
	ParentStepID string

	// BaseOutputDir is the root directory for per-branch output dirs.
	BaseOutputDir string

	// Concurrency is the maximum number of simultaneous goroutines.
	// 0 → runtime.NumCPU(), capped at MaxConcurrency.
	// Negative values are treated as 1.
	// Values > MaxConcurrency are clamped to MaxConcurrency.
	Concurrency int
}

// concurrencyLimit returns the effective concurrency limit.
func (f *FanoutExecutor) concurrencyLimit() int {
	// Check env override.
	if v := os.Getenv("JUNCTION_MAX_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > MaxConcurrency {
				return MaxConcurrency
			}
			return n
		}
	}
	n := f.Concurrency
	if n <= 0 {
		n = runtime.NumCPU()
	}
	if n > MaxConcurrency {
		n = MaxConcurrency
	}
	return n
}

// Execute runs all branches concurrently. If any branch fails, the context is
// cancelled so in-flight branches terminate early.
//
// Returns ErrParallelBranchesExceeded if len(branches) > MaxConcurrency.
func (f *FanoutExecutor) Execute(ctx context.Context, branches []BranchRequest) (FanoutResult, error) {
	if len(branches) > MaxConcurrency {
		return FanoutResult{}, fmt.Errorf("%w: requested %d, max %d",
			ErrParallelBranchesExceeded, len(branches), MaxConcurrency)
	}

	n := len(branches)
	results := make([]BranchResult, n)
	for i := range results {
		results[i].BranchIndex = i
	}

	limit := f.concurrencyLimit()
	if limit > n {
		limit = n
	}

	// Worker pool via semaphore channel.
	sem := make(chan struct{}, limit)

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var firstErr error
	var firstErrMu sync.Mutex

	for i, branch := range branches {
		wg.Add(1)
		go func(idx int, br BranchRequest) {
			defer wg.Done()

			// Acquire semaphore slot.
			select {
			case sem <- struct{}{}:
			case <-cancelCtx.Done():
				firstErrMu.Lock()
				results[idx].Err = cancelCtx.Err()
				firstErrMu.Unlock()
				return
			}
			defer func() { <-sem }()

			// Check for cancellation before doing work.
			select {
			case <-cancelCtx.Done():
				firstErrMu.Lock()
				results[idx].Err = cancelCtx.Err()
				firstErrMu.Unlock()
				return
			default:
			}

			branchDir := filepath.Join(f.BaseOutputDir, f.ParentStepID,
				"branch-"+strconv.Itoa(idx))
			outDir := filepath.Join(branchDir, "out")

			req := Request{
				StepID:       f.ParentStepID + "-branch-" + strconv.Itoa(idx),
				Eidolon:      br.Eidolon,
				Subcommand:   br.Subcommand,
				EnvelopePath: br.EnvelopePath,
				ThreadID:     f.ThreadID,
				OutputDir:    outDir,
				Env:          br.Env,
			}

			res, err := f.Executor.Execute(cancelCtx, req)
			firstErrMu.Lock()
			results[idx].Result = res
			results[idx].Err = err
			if err != nil && firstErr == nil {
				firstErr = err
				cancel() // cancel other in-flight branches
			}
			firstErrMu.Unlock()
		}(i, branch)
	}

	wg.Wait()

	return FanoutResult{Branches: results}, firstErr
}
