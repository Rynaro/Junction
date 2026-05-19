package contract_test

// Tests for the contract package (story S2 acceptance criteria).
//
// GIVEN/WHEN/THEN anchors match spec story S2 acceptance criteria.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Rynaro/Junction/internal/contract"
	"github.com/Rynaro/Junction/internal/contracts"
)

// embeddedRegistry returns a contract.Registry loaded from the embedded
// contracts FS (the same set the binary ships with).
func embeddedRegistry(t *testing.T) *contract.Registry {
	t.Helper()
	r, errs := contract.NewRegistryFromFS(contracts.Contracts, ".")
	if len(errs) > 0 {
		t.Fatalf("NewRegistryFromFS errors: %v", errs)
	}
	return r
}

// contractsDir returns the path to the internal/contracts/ directory, which
// also serves as the on-disk fixture set for NewRegistry tests.
func contractsDir() string {
	_, file, _, _ := runtime.Caller(0)
	// contract/contract_test.go → contract/ → internal/ → contracts/
	return filepath.Join(filepath.Dir(file), "..", "contracts")
}

// ─── S2: Embedded registry loads without errors ───────────────────────────────

func TestRegistry_EmbeddedLoads(t *testing.T) {
	r := embeddedRegistry(t)
	// Minimal sanity: at least the 18 Eidolon-to-Eidolon contracts + 6 human-edge.
	if r.Size() < 18 {
		t.Errorf("registry size = %d, want >= 18", r.Size())
	}
}

// ─── S2: Happy-path edge + performative checks ───────────────────────────────

// GIVEN the contract for spectra → apivr
// WHEN an envelope with PROPOSE is checked
// THEN Check returns nil.
func TestCheck_SpectraToApivr_PROPOSE(t *testing.T) {
	r := embeddedRegistry(t)
	if err := r.Check("spectra", "apivr", "PROPOSE"); err != nil {
		t.Errorf("Check(spectra, apivr, PROPOSE) = %v, want nil", err)
	}
}

// GIVEN the contract for atlas → spectra
// WHEN performatives PROPOSE, INFORM, REFUSE are each checked
// THEN all return nil.
func TestCheck_AtlasToSpectra_AllowedPerformatives(t *testing.T) {
	r := embeddedRegistry(t)
	for _, p := range []string{"PROPOSE", "INFORM", "REFUSE"} {
		if err := r.Check("atlas", "spectra", p); err != nil {
			t.Errorf("Check(atlas, spectra, %s) = %v, want nil", p, err)
		}
	}
}

// ─── S2: Human-edge contracts ────────────────────────────────────────────────

// GIVEN human-to-atlas.yaml exists
// WHEN an envelope with from:human, to:atlas, performative:REQUEST is checked
// THEN Check returns nil.
func TestCheck_HumanToAtlas_REQUEST(t *testing.T) {
	r := embeddedRegistry(t)
	if err := r.Check("human", "atlas", "REQUEST"); err != nil {
		t.Errorf("Check(human, atlas, REQUEST) = %v, want nil", err)
	}
}

// GIVEN human-to-* contracts for all six Eidolons
// WHEN each allowed performative is checked (REQUEST, INFORM, CRITIQUE, REFUSE, ACKNOWLEDGE, ESCALATE)
// THEN all return nil.
func TestCheck_HumanEdge_AllSixEidolons(t *testing.T) {
	r := embeddedRegistry(t)
	eidolons := []string{"atlas", "spectra", "apivr", "idg", "forge", "vigil"}
	allowed := []string{"REQUEST", "INFORM", "CRITIQUE", "REFUSE", "ACKNOWLEDGE", "ESCALATE"}

	for _, eid := range eidolons {
		for _, p := range allowed {
			if err := r.Check("human", eid, p); err != nil {
				t.Errorf("Check(human, %s, %s) = %v, want nil", eid, p, err)
			}
		}
	}
}

// ─── S2: Forbidden human-edge performatives ──────────────────────────────────

// GIVEN a human → atlas envelope with performative DECIDE (forbidden per §5.7)
// WHEN checked
// THEN Check returns ErrPerformativeNotAllowed.
func TestCheck_HumanToAtlas_DECIDE_Forbidden(t *testing.T) {
	r := embeddedRegistry(t)
	err := r.Check("human", "atlas", "DECIDE")
	if err == nil {
		t.Fatal("Check(human, atlas, DECIDE) = nil, want ErrPerformativeNotAllowed")
	}
	if !errors.Is(err, contract.ErrPerformativeNotAllowed) {
		t.Errorf("Check(human, atlas, DECIDE) = %v, want ErrPerformativeNotAllowed", err)
	}
}

// GIVEN human-origin forbidden performatives: PROPOSE, DECIDE, DELEGATE, RESUME
// WHEN each is checked on any human-edge contract
// THEN all return ErrPerformativeNotAllowed.
func TestCheck_HumanEdge_ForbiddenPerformatives(t *testing.T) {
	r := embeddedRegistry(t)
	forbidden := []string{"PROPOSE", "DECIDE", "DELEGATE", "RESUME"}
	for _, p := range forbidden {
		err := r.Check("human", "spectra", p)
		if err == nil {
			t.Errorf("Check(human, spectra, %s) = nil, want ErrPerformativeNotAllowed", p)
			continue
		}
		if !errors.Is(err, contract.ErrPerformativeNotAllowed) {
			t.Errorf("Check(human, spectra, %s) = %v, want ErrPerformativeNotAllowed", p, err)
		}
	}
}

// ─── S2: Edge not declared ───────────────────────────────────────────────────

// GIVEN no contract for forge → human
// WHEN such an envelope is checked under fail-fast
// THEN Check returns ErrEdgeNotDeclared with the searched path.
func TestCheck_EdgeNotDeclared(t *testing.T) {
	r := embeddedRegistry(t)
	err := r.Check("forge", "human", "PROPOSE")
	if err == nil {
		t.Fatal("Check(forge, human, PROPOSE) = nil, want ErrEdgeNotDeclared")
	}
	if !errors.Is(err, contract.ErrEdgeNotDeclared) {
		t.Errorf("Check(forge, human, PROPOSE) = %v, want ErrEdgeNotDeclared", err)
	}
}

// ─── S2: Performative not allowed ────────────────────────────────────────────

// GIVEN a contract that does not allow ESCALATE
// WHEN an ESCALATE envelope is checked on that edge
// THEN Check returns ErrPerformativeNotAllowed.
func TestCheck_PerformativeNotAllowed_OnEidolonEdge(t *testing.T) {
	r := embeddedRegistry(t)
	// apivr → idg only allows PROPOSE and INFORM; ESCALATE should be rejected.
	err := r.Check("apivr", "idg", "ESCALATE")
	if err == nil {
		t.Fatal("Check(apivr, idg, ESCALATE) = nil, want ErrPerformativeNotAllowed")
	}
	if !errors.Is(err, contract.ErrPerformativeNotAllowed) {
		t.Errorf("Check(apivr, idg, ESCALATE) = %v, want ErrPerformativeNotAllowed", err)
	}
}

// ─── NewRegistry: disk-based loading ─────────────────────────────────────────

func TestNewRegistry_FromDisk(t *testing.T) {
	r, errs := contract.NewRegistry(contractsDir())
	if len(errs) > 0 {
		t.Fatalf("NewRegistry errors: %v", errs)
	}
	if r.Size() == 0 {
		t.Error("registry loaded from disk is empty")
	}
	// Spot-check a known edge.
	if err := r.Check("atlas", "spectra", "PROPOSE"); err != nil {
		t.Errorf("disk registry: Check(atlas, spectra, PROPOSE) = %v", err)
	}
}

// ─── CheckPerformative standalone ────────────────────────────────────────────

func TestCheckPerformative_Standalone(t *testing.T) {
	c := &contract.Contract{
		From:                 "a",
		To:                   "b",
		PerformativesAllowed: []string{"PROPOSE", "INFORM"},
	}

	if err := contract.CheckPerformative(c, "PROPOSE"); err != nil {
		t.Errorf("CheckPerformative(PROPOSE) = %v, want nil", err)
	}
	if err := contract.CheckPerformative(c, "REFUSE"); err == nil {
		t.Error("CheckPerformative(REFUSE) = nil, want error")
	} else if !errors.Is(err, contract.ErrPerformativeNotAllowed) {
		t.Errorf("CheckPerformative(REFUSE) = %v, want ErrPerformativeNotAllowed", err)
	}
}

// ─── Issue #23: CheckWithOrigin — edge_origin awareness ──────────────────────

// Test 1: edge_origin "implicit" + unknown (from,to) + valid performative → passes.
// Regression fixture: real-world idg→human terminal envelope (no roster contract exists).
func TestCheckWithOrigin_Implicit_UnknownEdge_ValidPerformative(t *testing.T) {
	r := embeddedRegistry(t)
	// idg→human has no roster contract YAML; INFORM is a valid global performative.
	if err := r.CheckWithOrigin("idg", "human", "INFORM", "implicit"); err != nil {
		t.Errorf("CheckWithOrigin(idg, human, INFORM, implicit) = %v, want nil", err)
	}
}

// Test 1b: same test using the testdata fixture — loads, parses, and calls
// CheckWithOrigin mirroring the real-world verify path.
func TestCheckWithOrigin_Implicit_FixtureEnvelope(t *testing.T) {
	const fixturePath = "testdata/idg-to-human-implicit.envelope.json"
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var env struct {
		From struct {
			Eidolon string `json:"eidolon"`
		} `json:"from"`
		To struct {
			Eidolon string `json:"eidolon"`
		} `json:"to"`
		Performative string `json:"performative"`
		EdgeOrigin   string `json:"edge_origin"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}

	r := embeddedRegistry(t)
	if err := r.CheckWithOrigin(env.From.Eidolon, env.To.Eidolon, env.Performative, env.EdgeOrigin); err != nil {
		t.Errorf("CheckWithOrigin from fixture = %v, want nil", err)
	}
}

// Test 2: edge_origin "roster" (or unset) + unknown (from,to) → fails L3 with
// ErrEdgeNotDeclared. Regression guard: roster behaviour must be unchanged.
func TestCheckWithOrigin_Roster_UnknownEdge_FailsL3(t *testing.T) {
	r := embeddedRegistry(t)
	// idg→human has no roster contract — must fail with ErrEdgeNotDeclared.
	for _, origin := range []string{"roster", ""} {
		err := r.CheckWithOrigin("idg", "human", "INFORM", origin)
		if err == nil {
			t.Errorf("CheckWithOrigin(idg, human, INFORM, %q) = nil, want ErrEdgeNotDeclared", origin)
			continue
		}
		if !errors.Is(err, contract.ErrEdgeNotDeclared) {
			t.Errorf("CheckWithOrigin(idg, human, INFORM, %q) = %v, want ErrEdgeNotDeclared", origin, err)
		}
	}
}

// Test 3: edge_origin "implicit" + invalid performative → fails L4 against global enum.
func TestCheckWithOrigin_Implicit_InvalidPerformative_FailsL4(t *testing.T) {
	r := embeddedRegistry(t)
	err := r.CheckWithOrigin("idg", "human", "FOO", "implicit")
	if err == nil {
		t.Fatal("CheckWithOrigin(idg, human, FOO, implicit) = nil, want ErrPerformativeUnknown")
	}
	if !errors.Is(err, contract.ErrPerformativeUnknown) {
		t.Errorf("CheckWithOrigin(idg, human, FOO, implicit) = %v, want ErrPerformativeUnknown", err)
	}
}

// Test 4: edge_origin "implicit" + valid performative + known (from,to) pair →
// passes. For implicit edges the per-edge whitelist is bypassed even when a
// contract exists for the edge.
func TestCheckWithOrigin_Implicit_KnownEdge_BypassesWhitelist(t *testing.T) {
	r := embeddedRegistry(t)
	// apivr→idg exists in the roster; its whitelist is PROPOSE and INFORM.
	// ESCALATE is NOT in that whitelist but IS in the global set — implicit
	// edges must bypass the per-edge whitelist.
	if err := r.CheckWithOrigin("apivr", "idg", "ESCALATE", "implicit"); err != nil {
		t.Errorf("CheckWithOrigin(apivr, idg, ESCALATE, implicit) = %v, want nil (implicit bypasses per-edge whitelist)", err)
	}
}

// ─── Lookup ───────────────────────────────────────────────────────────────────

func TestLookup_Found(t *testing.T) {
	r := embeddedRegistry(t)
	c, err := r.Lookup("spectra", "apivr")
	if err != nil {
		t.Fatalf("Lookup(spectra, apivr) = %v", err)
	}
	if c.From != "spectra" || c.To != "apivr" {
		t.Errorf("Lookup got from=%q to=%q, want spectra/apivr", c.From, c.To)
	}
}

func TestLookup_NotFound(t *testing.T) {
	r := embeddedRegistry(t)
	_, err := r.Lookup("nobody", "nobody")
	if err == nil {
		t.Fatal("Lookup(nobody, nobody) = nil, want ErrEdgeNotDeclared")
	}
	if !errors.Is(err, contract.ErrEdgeNotDeclared) {
		t.Errorf("Lookup(nobody, nobody) = %v, want ErrEdgeNotDeclared", err)
	}
}
