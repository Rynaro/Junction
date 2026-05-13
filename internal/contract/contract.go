// Package contract loads directed-edge hand-off contracts from the ECL
// contracts directory (Eidolon→Eidolon and the F-HUMAN-EDGE human→Eidolon
// YAMLs) and validates that each envelope's (from, to, performative) matches
// an allowed entry per spec story S2.
//
// Contracts are vendored via go:embed in the embedded sub-package. A caller
// may also supply an override directory via NewRegistry(dir) to point at a
// local or newer contract set.
package contract

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Sentinel errors for contract violations (spec §5.5).
var (
	// ErrEdgeNotDeclared is returned when no contract file exists for the
	// directed edge (from → to). Exit code 67.
	ErrEdgeNotDeclared = errors.New("ecl: edge not declared — no contract found")

	// ErrPerformativeNotAllowed is returned when the performative is not in
	// the contract's allowed list for this edge. Exit code 68.
	ErrPerformativeNotAllowed = errors.New("ecl: performative not allowed for this edge")
)

// Contract is the parsed representation of a single directed-edge YAML file.
// Field names mirror the handoff-contract.v1.json schema.
type Contract struct {
	ContractVersion      string        `yaml:"contract_version"`
	From                 string        `yaml:"from"`
	To                   string        `yaml:"to"`
	EdgeOrigin           string        `yaml:"edge_origin"`
	PerformativesAllowed []string      `yaml:"performatives_allowed"`
	Artifacts            []ArtifactDef `yaml:"artifacts"`
	ContextDelta         *ContextDelta `yaml:"context_delta,omitempty"`
	TrustLevel           string        `yaml:"trust_level,omitempty"`
	Notes                string        `yaml:"notes,omitempty"`
}

// ArtifactDef describes one artifact shape allowed on the edge.
type ArtifactDef struct {
	Kind                   string   `yaml:"kind"`
	SchemaRef              string   `yaml:"schema_ref"`
	RequiredSections       []string `yaml:"required_sections,omitempty"`
	EvidenceAnchorRequired bool     `yaml:"evidence_anchor_required,omitempty"`
}

// ContextDelta mirrors the context_delta block in the contract YAML.
type ContextDelta struct {
	TokenBudgetMax  int      `yaml:"token_budget_max,omitempty"`
	RequiredHandles []string `yaml:"required_handles,omitempty"`
}

// edgeKey is the canonical lookup key: "<from>-to-<to>".
func edgeKey(from, to string) string {
	return from + "-to-" + to
}

// Registry holds a parsed set of directed-edge contracts. It is the
// canonical object for performing L3 (edge present) and L4 (performative
// allowed) checks.
//
// The zero value is an empty registry; use NewRegistry or NewEmbeddedRegistry
// to populate it.
type Registry struct {
	mu        sync.RWMutex
	contracts map[string]*Contract // key: edgeKey(from, to)
	sourceDir string               // the directory contracts were loaded from
}

// NewRegistry loads all *.yaml files from dir into a new Registry.
// Files that fail YAML parsing are skipped with a warning appended to
// loadErrs (the returned []error), so partial loads succeed.
func NewRegistry(dir string) (*Registry, []error) {
	r := &Registry{
		contracts: make(map[string]*Contract),
		sourceDir: dir,
	}
	var errs []error

	entries, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return r, []error{fmt.Errorf("contract.NewRegistry: glob: %w", err)}
	}

	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("contract: reading %s: %w", filepath.Base(path), err))
			continue
		}
		c, err := parseContractBytes(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("contract: skipping %s: %w", filepath.Base(path), err))
			continue
		}
		key := edgeKey(c.From, c.To)
		r.contracts[key] = c
	}
	return r, errs
}

// NewRegistryFromFS loads all *.yaml files from an fs.FS (used for the
// embedded contracts). It follows the same partial-load semantics as
// NewRegistry.
func NewRegistryFromFS(fsys fs.FS, dir string) (*Registry, []error) {
	r := &Registry{
		contracts: make(map[string]*Contract),
		sourceDir: dir,
	}
	var errs []error

	pattern := dir + "/*.yaml"
	if dir == "." || dir == "" {
		pattern = "*.yaml"
	}
	entries, err := fs.Glob(fsys, pattern)
	if err != nil {
		return r, []error{fmt.Errorf("contract.NewRegistryFromFS: glob: %w", err)}
	}

	for _, path := range entries {
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("contract: reading %s: %w", path, err))
			continue
		}
		c, err := parseContractBytes(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("contract: parsing %s: %w", path, err))
			continue
		}
		key := edgeKey(c.From, c.To)
		r.contracts[key] = c
	}
	return r, errs
}

// parseContractBytes decodes contract YAML into a Contract struct.
func parseContractBytes(data []byte) (*Contract, error) {
	var c Contract
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("yaml unmarshal: %w", err)
	}
	if c.From == "" || c.To == "" {
		return nil, fmt.Errorf("contract missing from or to field")
	}
	if len(c.PerformativesAllowed) == 0 {
		return nil, fmt.Errorf("contract %s->%s: no performatives_allowed", c.From, c.To)
	}
	return &c, nil
}

// Lookup returns the contract for the edge (from → to), or
// ErrEdgeNotDeclared if no contract is loaded for that edge.
func (r *Registry) Lookup(from, to string) (*Contract, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := edgeKey(from, to)
	c, ok := r.contracts[key]
	if !ok {
		searched := key + ".yaml"
		if r.sourceDir != "" {
			searched = filepath.Join(r.sourceDir, searched)
		}
		return nil, fmt.Errorf("%w: searched %q", ErrEdgeNotDeclared, searched)
	}
	return c, nil
}

// CheckEdge runs the L3 (edge exists) check only.
func (r *Registry) CheckEdge(from, to string) error {
	_, err := r.Lookup(from, to)
	return err
}

// CheckPerformative runs the L4 (performative allowed) check on an already-
// looked-up contract. Separated so callers can reuse a Lookup result.
func CheckPerformative(c *Contract, performative string) error {
	for _, p := range c.PerformativesAllowed {
		if p == performative {
			return nil
		}
	}
	return fmt.Errorf("%w: %q not in allowed set %v for edge %s->%s",
		ErrPerformativeNotAllowed, performative, c.PerformativesAllowed, c.From, c.To)
}

// Check performs both L3 and L4 checks for the given (from, to, performative)
// triple. It is the primary entry point for contract validation.
func (r *Registry) Check(from, to, performative string) error {
	c, err := r.Lookup(from, to)
	if err != nil {
		return err
	}
	return CheckPerformative(c, performative)
}

// Size returns the number of contracts currently loaded in the registry.
func (r *Registry) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.contracts)
}
