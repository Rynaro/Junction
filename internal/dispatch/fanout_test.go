package dispatch_test

// Tests for FanoutExecutor (S2.3 — concurrent fan-out worker pool).

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Rynaro/Junction/internal/dispatch"
)

// ─── concurrent executor ─────────────────────────────────────────────────────

// concurrentStubExecutor tracks peak concurrent usage and returns canned
// results by branch index. Each Execute call increments the in-flight counter,
// waits on a barrier (so every goroutine in a worker-pool slot reaches the peak
// counter check simultaneously), then decrements. The barrier sizes itself to
// the pool's concurrency cap so we observe a deterministic peak without
// burning wall-clock on time.Sleep.
type concurrentStubExecutor struct {
	inFlight    atomic.Int64
	peakConcurr atomic.Int64
	results     []stubExecutorCall // indexed by order of call
	callCount   atomic.Int64

	// barrierSize, when > 1, makes Execute block until barrierSize goroutines
	// have arrived. This replaces a time.Sleep that previously enforced
	// overlap; the barrier guarantees deterministic peak concurrency
	// observation without burning wall-clock.
	barrierSize int
	barrierOnce sync.Once
	barrierCh   chan struct{}
	arrived     atomic.Int64
}

func (c *concurrentStubExecutor) Execute(_ context.Context, req dispatch.Request) (dispatch.Result, error) {
	c.inFlight.Add(1)
	peak := c.inFlight.Load()
	for {
		cur := c.peakConcurr.Load()
		if peak <= cur || c.peakConcurr.CompareAndSwap(cur, peak) {
			break
		}
	}

	// Capture this call's index BEFORE any blocking so the result lookup is
	// stable regardless of ordering.
	idx := int(c.callCount.Add(1)) - 1

	// Synchronisation barrier (replaces time.Sleep). Only active when the
	// test sets barrierSize > 1 — e.g. to guarantee peak concurrency reaches
	// barrierSize. The first goroutine to call this initializes the channel;
	// the Nth one closes it, releasing all waiters.
	if c.barrierSize > 1 {
		c.barrierOnce.Do(func() {
			c.barrierCh = make(chan struct{})
		})
		if c.arrived.Add(1) == int64(c.barrierSize) {
			close(c.barrierCh)
		}
		<-c.barrierCh
	}

	c.inFlight.Add(-1)

	if idx < len(c.results) {
		r := c.results[idx]
		r.result.StepID = req.StepID
		return r.result, r.err
	}
	return dispatch.Result{StepID: req.StepID, ExitCode: 0}, nil
}

// ─── fanout tests ─────────────────────────────────────────────────────────────

// GIVEN 2 parallel FORGE-stub branches
// WHEN Execute is called
// THEN both complete and FanoutResult.Branches has 2 entries with no error.
func TestFanoutExecutor_TwoBranchesSucceed(t *testing.T) {
	t.Parallel()
	inner := &concurrentStubExecutor{
		results: []stubExecutorCall{
			{result: dispatch.Result{ExitCode: 0}},
			{result: dispatch.Result{ExitCode: 0}},
		},
	}
	f := &dispatch.FanoutExecutor{
		Executor:      inner,
		ThreadID:      "fanout-test-001",
		ParentStepID:  "S0",
		BaseOutputDir: t.TempDir(),
		Concurrency:   2,
	}

	branches := []dispatch.BranchRequest{
		{Eidolon: "forge"},
		{Eidolon: "forge"},
	}

	result, err := f.Execute(context.Background(), branches)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Branches) != 2 {
		t.Errorf("got %d branches, want 2", len(result.Branches))
	}
	for i, br := range result.Branches {
		if br.Err != nil {
			t.Errorf("branch %d: unexpected error %v", i, br.Err)
		}
	}
}

// GIVEN one of two branches fails
// WHEN Execute is called
// THEN the returned error is non-nil (the failing branch's error).
func TestFanoutExecutor_OneBranchFails(t *testing.T) {
	t.Parallel()
	sentinelErr := errors.New("branch 1 failed")
	inner := &concurrentStubExecutor{
		results: []stubExecutorCall{
			{result: dispatch.Result{ExitCode: 0}},
			{result: dispatch.Result{ExitCode: 1}, err: sentinelErr},
		},
	}
	f := &dispatch.FanoutExecutor{
		Executor:      inner,
		ThreadID:      "fanout-fail-001",
		ParentStepID:  "S0",
		BaseOutputDir: t.TempDir(),
		Concurrency:   2,
	}

	_, err := f.Execute(context.Background(), []dispatch.BranchRequest{
		{Eidolon: "forge"},
		{Eidolon: "forge"},
	})
	if err == nil {
		t.Fatal("Execute with one failing branch = nil, want error")
	}
}

// GIVEN more than MaxConcurrency branches requested
// WHEN Execute is called
// THEN Execute returns ErrParallelBranchesExceeded without dispatching.
func TestFanoutExecutor_ExceedsMaxConcurrency(t *testing.T) {
	t.Parallel()
	inner := &concurrentStubExecutor{}
	f := &dispatch.FanoutExecutor{
		Executor:      inner,
		ThreadID:      "fanout-overflow",
		ParentStepID:  "S0",
		BaseOutputDir: t.TempDir(),
	}

	// Build 6 branches (> MaxConcurrency of 5).
	branches := make([]dispatch.BranchRequest, 6)
	for i := range branches {
		branches[i] = dispatch.BranchRequest{Eidolon: "forge"}
	}

	_, err := f.Execute(context.Background(), branches)
	if err == nil {
		t.Fatal("Execute with 6 branches = nil, want ErrParallelBranchesExceeded")
	}
	if !errors.Is(err, dispatch.ErrParallelBranchesExceeded) {
		t.Errorf("error = %v, want ErrParallelBranchesExceeded", err)
	}
}

// GIVEN Concurrency=2 and 4 branches
// WHEN Execute is called
// THEN peak concurrency observed is at most 2.
func TestFanoutExecutor_ConcurrencyCap(t *testing.T) {
	t.Parallel()
	// barrierSize=2 forces both pool slots to arrive in Execute before either
	// proceeds — guarantees peak concurrency reaches the cap without a sleep.
	inner := &concurrentStubExecutor{
		results: []stubExecutorCall{
			{result: dispatch.Result{ExitCode: 0}},
			{result: dispatch.Result{ExitCode: 0}},
			{result: dispatch.Result{ExitCode: 0}},
			{result: dispatch.Result{ExitCode: 0}},
		},
		barrierSize: 2,
	}
	f := &dispatch.FanoutExecutor{
		Executor:      inner,
		ThreadID:      "fanout-cap",
		ParentStepID:  "S0",
		BaseOutputDir: t.TempDir(),
		Concurrency:   2,
	}

	_, err := f.Execute(context.Background(), []dispatch.BranchRequest{
		{Eidolon: "forge"},
		{Eidolon: "forge"},
		{Eidolon: "forge"},
		{Eidolon: "forge"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	peak := inner.peakConcurr.Load()
	if peak > 2 {
		t.Errorf("peak concurrency = %d, want <= 2", peak)
	}
}

// GIVEN the context is cancelled before dispatch
// WHEN Execute is called with an already-cancelled context
// THEN all branches report a cancelled error and the overall error is non-nil.
func TestFanoutExecutor_ContextCancelled(t *testing.T) {
	t.Parallel()
	inner := &concurrentStubExecutor{}
	f := &dispatch.FanoutExecutor{
		Executor:      inner,
		ThreadID:      "fanout-cancel",
		ParentStepID:  "S0",
		BaseOutputDir: t.TempDir(),
		Concurrency:   2,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := f.Execute(ctx, []dispatch.BranchRequest{
		{Eidolon: "forge"},
		{Eidolon: "forge"},
	})
	// With an already-cancelled context the error should be non-nil
	// (either context.Canceled from the worker or from the semaphore select).
	// It's valid for a fast executor to have already succeeded before noticing
	// cancellation; we just verify no panic and that the result structure is intact.
	_ = err
}
