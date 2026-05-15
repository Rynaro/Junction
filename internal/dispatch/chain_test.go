package dispatch_test

// Tests for ChainExecutor (S2.2 — TRANCE chain dispatch).
//
// Uses a stubExecutor that returns canned Results so tests don't need a
// real Docker daemon or real Eidolons.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rynaro/Junction/internal/contracts"
	"github.com/Rynaro/Junction/internal/contract"
	"github.com/Rynaro/Junction/internal/dispatch"
)

// ─── stub executor ────────────────────────────────────────────────────────────

// stubExecutorCall defines a canned response for one Execute call.
type stubExecutorCall struct {
	result dispatch.Result
	err    error
}

// stubExecutor is a sequential stub Executor. Each Execute call pops the next
// canned response from the queue and records the received Request.
type stubExecutor struct {
	calls    []stubExecutorCall
	received []dispatch.Request
}

func (s *stubExecutor) Execute(_ context.Context, req dispatch.Request) (dispatch.Result, error) {
	s.received = append(s.received, req)
	if len(s.calls) == 0 {
		return dispatch.Result{StepID: req.StepID, ExitCode: 0}, nil
	}
	c := s.calls[0]
	s.calls = s.calls[1:]
	c.result.StepID = req.StepID
	return c.result, c.err
}

// ─── test helpers ────────────────────────────────────────────────────────────

func cannedEnvelope(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name+".envelope.json")
	if err := os.WriteFile(p, []byte(`{"message_id":"canned"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ─── S2.2 tests ──────────────────────────────────────────────────────────────

// GIVEN a 3-step chain (atlas→spectra→apivr) with stub executor
// WHEN Execute is called
// THEN all three steps complete; ChainResult has 3 entries.
func TestChainExecutor_AllPass(t *testing.T) {
	base := t.TempDir()
	firstEnv := cannedEnvelope(t, base, "human-input")

	reg, errs := contract.NewRegistryFromFS(contracts.Contracts, ".")
	if len(errs) > 0 {
		t.Logf("contract load warnings: %v", errs)
	}

	exec := &stubExecutor{
		calls: []stubExecutorCall{
			{result: dispatch.Result{ExitCode: 0, OutputEnvelopePath: filepath.Join(base, "S0", "out", "out.envelope.json")}},
			{result: dispatch.Result{ExitCode: 0, OutputEnvelopePath: filepath.Join(base, "S1", "out", "out.envelope.json")}},
			{result: dispatch.Result{ExitCode: 0, OutputEnvelopePath: filepath.Join(base, "S2", "out", "out.envelope.json")}},
		},
	}

	chain := &dispatch.ChainExecutor{
		Executor:      exec,
		Registry:      reg,
		ThreadID:      "chain-test-001",
		BaseOutputDir: base,
	}

	steps := []dispatch.ChainStep{
		{StepID: "S0", Eidolon: "atlas", From: "human", To: "atlas", Performative: "REQUEST", InitialEnvelopePath: firstEnv},
		{StepID: "S1", Eidolon: "spectra", From: "atlas", To: "spectra", Performative: "PROPOSE"},
		{StepID: "S2", Eidolon: "apivr", From: "spectra", To: "apivr", Performative: "PROPOSE"},
	}

	result, err := chain.Execute(context.Background(), steps)
	if err != nil {
		t.Fatalf("chain.Execute: %v", err)
	}
	if len(result.Steps) != 3 {
		t.Errorf("got %d steps, want 3", len(result.Steps))
	}
}

// GIVEN the second step (S1) fails
// WHEN Execute is called
// THEN chain stops at S1; error wraps ErrChainStepFailed; only 1 step completed.
func TestChainExecutor_MidChainFailure(t *testing.T) {
	base := t.TempDir()
	firstEnv := cannedEnvelope(t, base, "input")

	exec := &stubExecutor{
		calls: []stubExecutorCall{
			{result: dispatch.Result{ExitCode: 0, OutputEnvelopePath: filepath.Join(base, "S0", "out", "out.envelope.json")}},
			{result: dispatch.Result{ExitCode: 1}, err: dispatch.ErrDispatchFailed},
		},
	}

	chain := &dispatch.ChainExecutor{
		Executor:      exec,
		ThreadID:      "chain-fail-001",
		BaseOutputDir: base,
	}

	steps := []dispatch.ChainStep{
		{StepID: "S0", Eidolon: "atlas", InitialEnvelopePath: firstEnv},
		{StepID: "S1", Eidolon: "spectra"},
		{StepID: "S2", Eidolon: "apivr"},
	}

	chainResult, err := chain.Execute(context.Background(), steps)
	if err == nil {
		t.Fatal("Execute with mid-chain failure = nil, want error")
	}
	if !errors.Is(err, dispatch.ErrChainStepFailed) {
		t.Errorf("error = %v, want ErrChainStepFailed", err)
	}
	if len(chainResult.Steps) != 1 {
		t.Errorf("got %d completed steps, want 1", len(chainResult.Steps))
	}
}

// GIVEN a contract mismatch on the second edge (wrong performative)
// WHEN Execute is called
// THEN chain stops; error wraps ErrChainContractViolation.
func TestChainExecutor_ContractMismatch(t *testing.T) {
	base := t.TempDir()
	firstEnv := cannedEnvelope(t, base, "input")

	reg, errs := contract.NewRegistryFromFS(contracts.Contracts, ".")
	if len(errs) > 0 {
		t.Logf("contract load warnings: %v", errs)
	}

	exec := &stubExecutor{
		calls: []stubExecutorCall{
			{result: dispatch.Result{ExitCode: 0}},
		},
	}

	chain := &dispatch.ChainExecutor{
		Executor:      exec,
		Registry:      reg,
		ThreadID:      "chain-contract-001",
		BaseOutputDir: base,
	}

	steps := []dispatch.ChainStep{
		{StepID: "S0", Eidolon: "atlas", From: "human", To: "atlas", Performative: "REQUEST", InitialEnvelopePath: firstEnv},
		// DECIDE is not an allowed performative on atlas→spectra.
		{StepID: "S1", Eidolon: "spectra", From: "atlas", To: "spectra", Performative: "DECIDE"},
	}

	_, err := chain.Execute(context.Background(), steps)
	if err == nil {
		t.Fatal("Execute with contract mismatch = nil, want error")
	}
	if !errors.Is(err, dispatch.ErrChainContractViolation) {
		t.Errorf("error = %v, want ErrChainContractViolation", err)
	}
}

// GIVEN a chain with no Registry set
// WHEN Execute is called
// THEN contract checking is skipped and the chain succeeds.
func TestChainExecutor_NoRegistry_SkipsContractCheck(t *testing.T) {
	base := t.TempDir()
	firstEnv := cannedEnvelope(t, base, "input")

	exec := &stubExecutor{
		calls: []stubExecutorCall{
			{result: dispatch.Result{ExitCode: 0}},
			{result: dispatch.Result{ExitCode: 0}},
		},
	}

	chain := &dispatch.ChainExecutor{
		Executor:      exec,
		Registry:      nil, // no contract check
		ThreadID:      "chain-no-reg",
		BaseOutputDir: base,
	}

	steps := []dispatch.ChainStep{
		{StepID: "S0", Eidolon: "atlas", From: "human", To: "atlas", Performative: "DECIDE", InitialEnvelopePath: firstEnv},
		{StepID: "S1", Eidolon: "spectra", From: "atlas", To: "spectra", Performative: "DECIDE"},
	}

	result, err := chain.Execute(context.Background(), steps)
	if err != nil {
		t.Fatalf("Execute without registry: %v", err)
	}
	if len(result.Steps) != 2 {
		t.Errorf("got %d steps, want 2", len(result.Steps))
	}
}

// GIVEN a chain where each step has a distinct ToVersion
// WHEN Execute is called
// THEN each Request received by the executor has EidolonVersion equal to the
// step's ToVersion (WS-V2-B: version threading).
func TestChainExecutor_ThreadsToVersionToRequest(t *testing.T) {
	base := t.TempDir()
	firstEnv := cannedEnvelope(t, base, "input")

	exec := &stubExecutor{
		calls: []stubExecutorCall{
			{result: dispatch.Result{ExitCode: 0, OutputEnvelopePath: filepath.Join(base, "S0", "out", "out.envelope.json")}},
			{result: dispatch.Result{ExitCode: 0, OutputEnvelopePath: filepath.Join(base, "S1", "out", "out.envelope.json")}},
		},
	}

	chain := &dispatch.ChainExecutor{
		Executor:      exec,
		Registry:      nil,
		ThreadID:      "chain-version-001",
		BaseOutputDir: base,
	}

	steps := []dispatch.ChainStep{
		{StepID: "S0", Eidolon: "atlas", ToVersion: "1.5.3", InitialEnvelopePath: firstEnv},
		{StepID: "S1", Eidolon: "spectra", ToVersion: "4.2.8"},
	}

	_, err := chain.Execute(context.Background(), steps)
	if err != nil {
		t.Fatalf("chain.Execute: %v", err)
	}

	if len(exec.received) != 2 {
		t.Fatalf("got %d received requests, want 2", len(exec.received))
	}

	if exec.received[0].EidolonVersion != "1.5.3" {
		t.Errorf("S0 Request.EidolonVersion = %q, want %q", exec.received[0].EidolonVersion, "1.5.3")
	}
	if exec.received[1].EidolonVersion != "4.2.8" {
		t.Errorf("S1 Request.EidolonVersion = %q, want %q", exec.received[1].EidolonVersion, "4.2.8")
	}
}
