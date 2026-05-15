package plan_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Rynaro/Junction/internal/dispatch"
	"github.com/Rynaro/Junction/internal/plan"
)

// ---------------------------------------------------------------------------
// Parse + validate tests
// ---------------------------------------------------------------------------

const validPlanJSON = `{
  "thread_id": "01hv1234567890abcdefghijkl",
  "tier": "trance",
  "enforce": "fail-fast",
  "executor": "shell",
  "steps": [
    {
      "step_id": "S0",
      "from": {"eidolon": "human", "version": "n/a"},
      "to":   {"eidolon": "atlas",  "version": "1.5.2"},
      "performative": "REQUEST",
      "edge_origin": "roster",
      "objective": "Scout the dispatch path.",
      "artifact": {"kind": "prompt", "schema_version": "1.0", "path": "input/prompt.md"}
    }
  ]
}`

const validPlanContainerJSON = `{
  "thread_id": "01hv1234567890abcdefghijkm",
  "tier": "standard",
  "steps": [
    {
      "step_id": "S0",
      "from": {"eidolon": "human",   "version": "n/a"},
      "to":   {"eidolon": "spectra", "version": "4.2.8"},
      "performative": "REQUEST"
    },
    {
      "step_id": "S1",
      "from": {"eidolon": "spectra", "version": "4.2.8"},
      "to":   {"eidolon": "apivr",   "version": "2.1.0"},
      "performative": "DELEGATE"
    }
  ]
}`

func TestParse_ValidPlan(t *testing.T) {
	p, err := plan.Parse(strings.NewReader(validPlanJSON))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if p.ThreadID != "01hv1234567890abcdefghijkl" {
		t.Errorf("thread_id: got %q", p.ThreadID)
	}
	if p.Tier != "trance" {
		t.Errorf("tier: got %q, want %q", p.Tier, "trance")
	}
	if p.Executor != "shell" {
		t.Errorf("executor: got %q, want %q", p.Executor, "shell")
	}
	if p.Enforce != "fail-fast" {
		t.Errorf("enforce: got %q, want %q", p.Enforce, "fail-fast")
	}
	if len(p.Steps) != 1 {
		t.Fatalf("steps: got %d, want 1", len(p.Steps))
	}

	s := p.Steps[0]
	if s.StepID != "S0" {
		t.Errorf("step_id: got %q", s.StepID)
	}
	if s.From.Eidolon != "human" {
		t.Errorf("from.eidolon: got %q", s.From.Eidolon)
	}
	if s.To.Eidolon != "atlas" {
		t.Errorf("to.eidolon: got %q", s.To.Eidolon)
	}
	if s.Performative != "REQUEST" {
		t.Errorf("performative: got %q", s.Performative)
	}
	if s.Artifact == nil || s.Artifact.Path != "input/prompt.md" {
		t.Errorf("artifact.path: got %v", s.Artifact)
	}
}

func TestParse_DefaultsApplied(t *testing.T) {
	// Plan without executor/enforce fields — defaults must be applied.
	p, err := plan.Parse(strings.NewReader(validPlanContainerJSON))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Enforce != "fail-fast" {
		t.Errorf("enforce default: got %q", p.Enforce)
	}
	if p.Executor != "container" {
		t.Errorf("executor default: got %q", p.Executor)
	}
}

var invalidCases = []struct {
	name    string
	json    string
	wantErr error
}{
	{
		name:    "missing thread_id",
		json:    `{"tier":"standard","steps":[{"step_id":"S0","from":{"eidolon":"human","version":"n/a"},"to":{"eidolon":"atlas","version":"1.0"},"performative":"REQUEST"}]}`,
		wantErr: plan.ErrInvalidPlan,
	},
	{
		name:    "missing tier",
		json:    `{"thread_id":"abc","steps":[{"step_id":"S0","from":{"eidolon":"human","version":"n/a"},"to":{"eidolon":"atlas","version":"1.0"},"performative":"REQUEST"}]}`,
		wantErr: plan.ErrInvalidPlan,
	},
	{
		name:    "empty steps array",
		json:    `{"thread_id":"abc","tier":"standard","steps":[]}`,
		wantErr: plan.ErrInvalidPlan,
	},
	{
		name:    "invalid performative",
		json:    `{"thread_id":"abc","tier":"standard","steps":[{"step_id":"S0","from":{"eidolon":"human","version":"n/a"},"to":{"eidolon":"atlas","version":"1.0"},"performative":"BOGUS"}]}`,
		wantErr: plan.ErrInvalidPlan,
	},
	{
		name:    "invalid tier value",
		json:    `{"thread_id":"abc","tier":"unknown","steps":[{"step_id":"S0","from":{"eidolon":"human","version":"n/a"},"to":{"eidolon":"atlas","version":"1.0"},"performative":"REQUEST"}]}`,
		wantErr: plan.ErrInvalidPlan,
	},
	{
		name:    "missing steps field",
		json:    `{"thread_id":"abc","tier":"standard"}`,
		wantErr: plan.ErrInvalidPlan,
	},
	{
		name:    "not valid JSON",
		json:    `{invalid`,
		wantErr: plan.ErrInvalidPlan,
	},
}

func TestParse_InvalidCases(t *testing.T) {
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := plan.Parse(strings.NewReader(tc.json))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("error chain: want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ToChainSteps conversion tests
// ---------------------------------------------------------------------------

func TestToChainSteps_SingleStep(t *testing.T) {
	p, err := plan.Parse(strings.NewReader(validPlanJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	steps := p.ToChainSteps()
	if len(steps) != 1 {
		t.Fatalf("got %d chain steps, want 1", len(steps))
	}

	cs := steps[0]
	if cs.StepID != "S0" {
		t.Errorf("StepID: got %q", cs.StepID)
	}
	if cs.Eidolon != "atlas" {
		t.Errorf("Eidolon: got %q", cs.Eidolon)
	}
	if cs.From != "human" {
		t.Errorf("From: got %q", cs.From)
	}
	if cs.To != "atlas" {
		t.Errorf("To: got %q", cs.To)
	}
	if cs.Performative != "REQUEST" {
		t.Errorf("Performative: got %q", cs.Performative)
	}
	// First step artifact.path → InitialEnvelopePath.
	if cs.InitialEnvelopePath != "input/prompt.md" {
		t.Errorf("InitialEnvelopePath: got %q", cs.InitialEnvelopePath)
	}
}

func TestToChainSteps_MultiStep(t *testing.T) {
	p, err := plan.Parse(strings.NewReader(validPlanContainerJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	steps := p.ToChainSteps()
	if len(steps) != 2 {
		t.Fatalf("got %d chain steps, want 2", len(steps))
	}

	// Second step must have empty InitialEnvelopePath (chained from first).
	if steps[1].InitialEnvelopePath != "" {
		t.Errorf("steps[1].InitialEnvelopePath: want empty, got %q",
			steps[1].InitialEnvelopePath)
	}
}

// GIVEN a plan with to.version set on each step
// WHEN ToChainSteps is called
// THEN each ChainStep.ToVersion matches the source plan's to.version.
func TestToChainSteps_ToVersionRoundtrip(t *testing.T) {
	p, err := plan.Parse(strings.NewReader(validPlanJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	steps := p.ToChainSteps()
	if len(steps) != 1 {
		t.Fatalf("got %d chain steps, want 1", len(steps))
	}

	// validPlanJSON has to.version = "1.5.2" for step S0.
	if steps[0].ToVersion != "1.5.2" {
		t.Errorf("steps[0].ToVersion: got %q, want %q", steps[0].ToVersion, "1.5.2")
	}
}

// GIVEN a multi-step plan with distinct to.version values
// WHEN ToChainSteps is called
// THEN each ChainStep.ToVersion matches its respective to.version.
func TestToChainSteps_MultiStep_ToVersionRoundtrip(t *testing.T) {
	p, err := plan.Parse(strings.NewReader(validPlanContainerJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	steps := p.ToChainSteps()
	if len(steps) != 2 {
		t.Fatalf("got %d chain steps, want 2", len(steps))
	}

	// validPlanContainerJSON has:
	//   S0: to.version = "4.2.8"
	//   S1: to.version = "2.1.0"
	if steps[0].ToVersion != "4.2.8" {
		t.Errorf("steps[0].ToVersion: got %q, want %q", steps[0].ToVersion, "4.2.8")
	}
	if steps[1].ToVersion != "2.1.0" {
		t.Errorf("steps[1].ToVersion: got %q, want %q", steps[1].ToVersion, "2.1.0")
	}
}

// ---------------------------------------------------------------------------
// SelectExecutor tests
// ---------------------------------------------------------------------------

func TestSelectExecutor_ShellWhenNoContainer(t *testing.T) {
	opts := plan.ExecutorOptions{ProjectDir: "/tmp", CacheDir: "/tmp/cache"}
	exec := plan.SelectExecutor(plan.ExecutorModeContainer, true, opts)
	if _, ok := exec.(*dispatch.ShellExecutor); !ok {
		t.Errorf("noContainer=true: expected *ShellExecutor, got %T", exec)
	}
}

func TestSelectExecutor_ShellWhenModeShell(t *testing.T) {
	opts := plan.ExecutorOptions{ProjectDir: "/tmp", CacheDir: "/tmp/cache"}
	exec := plan.SelectExecutor(plan.ExecutorModeShell, false, opts)
	if _, ok := exec.(*dispatch.ShellExecutor); !ok {
		t.Errorf("mode=shell: expected *ShellExecutor, got %T", exec)
	}
}

func TestSelectExecutor_ContainerWhenModeContainer(t *testing.T) {
	opts := plan.ExecutorOptions{ProjectDir: "/tmp", CacheDir: "/tmp/cache"}
	exec := plan.SelectExecutor(plan.ExecutorModeContainer, false, opts)
	if _, ok := exec.(*dispatch.ContainerExecutor); !ok {
		t.Errorf("mode=container: expected *ContainerExecutor, got %T", exec)
	}
}

func TestSelectExecutor_ContainerWhenModeEmpty(t *testing.T) {
	opts := plan.ExecutorOptions{ProjectDir: "/tmp", CacheDir: "/tmp/cache"}
	exec := plan.SelectExecutor("", false, opts)
	if _, ok := exec.(*dispatch.ContainerExecutor); !ok {
		t.Errorf("mode=\"\": expected *ContainerExecutor, got %T", exec)
	}
}

func TestSelectExecutor_NoContainerOverridesContainerMode(t *testing.T) {
	opts := plan.ExecutorOptions{ProjectDir: "/tmp", CacheDir: "/tmp/cache"}
	// Even when mode is container, noContainer=true must force shell.
	exec := plan.SelectExecutor(plan.ExecutorModeContainer, true, opts)
	if _, ok := exec.(*dispatch.ShellExecutor); !ok {
		t.Errorf("noContainer override: expected *ShellExecutor, got %T", exec)
	}
}

// ---------------------------------------------------------------------------
// ModeFromString tests
// ---------------------------------------------------------------------------

func TestModeFromString(t *testing.T) {
	cases := []struct {
		in   string
		want plan.ExecutorMode
	}{
		{"shell", plan.ExecutorModeShell},
		{"container", plan.ExecutorModeContainer},
		{"", plan.ExecutorModeContainer},
		{"unknown", plan.ExecutorModeContainer},
	}
	for _, tc := range cases {
		got := plan.ModeFromString(tc.in)
		if got != tc.want {
			t.Errorf("ModeFromString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
