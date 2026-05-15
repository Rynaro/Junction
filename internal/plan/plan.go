// Package plan parses and validates Junction plan.json files (spec §7.5,
// round 5/6) and converts them to typed [dispatch.ChainStep] slices.
//
// OQ-20 resolution (F9-S0 implementation decision): plan validation uses a
// vendored JSON Schema (plan.v1.json, embedded via go:embed) compiled with the
// same github.com/santhosh-tekuri/jsonschema/v5 library used by the envelope
// package. A copy of the schema is published to schemas/plan.v1.json at the
// repo root for external tooling (e.g. ajv validate). Inline-only Go
// validation was considered but rejected to keep the single-source-of-truth
// property: the schema file remains normative for both Go validation and the
// consumer-facing fixture.
package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	_ "embed"

	"github.com/Rynaro/Junction/internal/dispatch"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed plan.v1.json
var planSchemaJSON []byte

// Sentinel errors.
var (
	// ErrInvalidPlan is returned when the plan.json does not conform to the
	// §7.5 format.
	ErrInvalidPlan = errors.New("plan: invalid plan.json")

	// ErrMissingField is returned when a required field is absent and the
	// JSON Schema error message is not specific enough.
	ErrMissingField = errors.New("plan: required field missing")
)

const planSchemaID = "https://github.com/Rynaro/Junction/schemas/plan.v1.json"

// compiledPlanSchema is a singleton compiled from planSchemaJSON.
var compiledPlanSchema *jsonschema.Schema

// getPlanSchema returns the compiled plan JSON schema, compiling it once.
func getPlanSchema() (*jsonschema.Schema, error) {
	if compiledPlanSchema != nil {
		return compiledPlanSchema, nil
	}
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(planSchemaID, bytes.NewReader(planSchemaJSON)); err != nil {
		return nil, fmt.Errorf("plan: loading schema: %w", err)
	}
	s, err := c.Compile(planSchemaID)
	if err != nil {
		return nil, fmt.Errorf("plan: compiling schema: %w", err)
	}
	compiledPlanSchema = s
	return compiledPlanSchema, nil
}

// ---------------------------------------------------------------------------
// Wire types — mirror of §7.5 JSON shape.
// ---------------------------------------------------------------------------

// Identity is the {eidolon, version} object used in from/to step fields.
type Identity struct {
	Eidolon string `json:"eidolon"`
	Version string `json:"version"`
}

// StepArtifact is the artifact descriptor in a plan step.
type StepArtifact struct {
	Kind          string `json:"kind"`
	SchemaVersion string `json:"schema_version"`
	// Path is the relative path to the input artifact; it maps to
	// dispatch.Request.EnvelopePath for the first step in a chain.
	Path string `json:"path"`
}

// PlanStep is the wire representation of a single step in plan.json.
type PlanStep struct {
	StepID       string            `json:"step_id"`
	From         Identity          `json:"from"`
	To           Identity          `json:"to"`
	Performative string            `json:"performative"`
	EdgeOrigin   string            `json:"edge_origin,omitempty"`
	Objective    string            `json:"objective,omitempty"`
	Artifact     *StepArtifact     `json:"artifact,omitempty"`
	ModelTierHint string           `json:"model_tier_hint,omitempty"`
	Constraints  map[string]any    `json:"constraints,omitempty"`
}

// Plan is the top-level wire representation of a plan.json file.
type Plan struct {
	ThreadID string     `json:"thread_id"`
	Tier     string     `json:"tier"`
	Enforce  string     `json:"enforce,omitempty"`
	// Executor selects the default executor for all steps.
	// "container" (default) or "shell".
	Executor string     `json:"executor,omitempty"`
	Steps    []PlanStep `json:"steps"`
}

// ---------------------------------------------------------------------------
// Parse + validate.
// ---------------------------------------------------------------------------

// Parse reads a plan.json from r, validates it against the vendored §7.5 JSON
// Schema, and returns the typed Plan. On any validation failure it returns a
// non-nil error wrapping ErrInvalidPlan; exit code 64 is appropriate (config
// error per spec §5.5).
func Parse(r io.Reader) (Plan, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return Plan{}, fmt.Errorf("plan: reading: %w", err)
	}

	// Schema validation first — gives the most precise field-level errors.
	if err := validateBytes(raw); err != nil {
		return Plan{}, err
	}

	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		return Plan{}, fmt.Errorf("%w: JSON decode: %s", ErrInvalidPlan, err)
	}

	// Belt-and-suspenders field checks that complement the schema.
	if err := p.validate(); err != nil {
		return Plan{}, err
	}

	// Apply defaults.
	if p.Enforce == "" {
		p.Enforce = "fail-fast"
	}
	if p.Executor == "" {
		p.Executor = "container"
	}

	return p, nil
}

// validateBytes runs the vendored JSON Schema against raw bytes.
func validateBytes(raw []byte) error {
	s, err := getPlanSchema()
	if err != nil {
		return err
	}

	var doc interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%w: invalid JSON: %s", ErrInvalidPlan, err)
	}

	if err := s.Validate(doc); err != nil {
		var ve *jsonschema.ValidationError
		if ok := isSchemaValidationError(err, &ve); ok {
			return fmt.Errorf("%w: %s", ErrInvalidPlan, summarisePlanError(ve))
		}
		return fmt.Errorf("%w: %s", ErrInvalidPlan, err)
	}
	return nil
}

func isSchemaValidationError(err error, target **jsonschema.ValidationError) bool {
	ve, ok := err.(*jsonschema.ValidationError)
	if ok {
		*target = ve
	}
	return ok
}

func summarisePlanError(ve *jsonschema.ValidationError) string {
	var leaves []string
	collectPlanLeaves(ve, &leaves)
	if len(leaves) > 0 {
		return strings.Join(leaves, "; ")
	}
	return ve.Error()
}

func collectPlanLeaves(ve *jsonschema.ValidationError, out *[]string) {
	if len(ve.Causes) == 0 {
		*out = append(*out, fmt.Sprintf("%s: %s", ve.InstanceLocation, ve.Message))
		return
	}
	for _, c := range ve.Causes {
		collectPlanLeaves(c, out)
	}
}

// validate performs post-unmarshal checks that complement the JSON Schema.
func (p *Plan) validate() error {
	if p.ThreadID == "" {
		return fmt.Errorf("%w: thread_id", ErrMissingField)
	}
	if len(p.Steps) == 0 {
		return fmt.Errorf("%w: steps (must have at least one)", ErrMissingField)
	}
	for i, s := range p.Steps {
		if s.StepID == "" {
			return fmt.Errorf("%w: steps[%d].step_id", ErrMissingField, i)
		}
		if s.From.Eidolon == "" {
			return fmt.Errorf("%w: steps[%d].from.eidolon", ErrMissingField, i)
		}
		if s.To.Eidolon == "" {
			return fmt.Errorf("%w: steps[%d].to.eidolon", ErrMissingField, i)
		}
		if s.Performative == "" {
			return fmt.Errorf("%w: steps[%d].performative", ErrMissingField, i)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Conversion to dispatch types.
// ---------------------------------------------------------------------------

// ToChainSteps converts a parsed Plan to a slice of dispatch.ChainStep values
// ready for [dispatch.ChainExecutor]. The first step's artifact.path (if
// present) becomes InitialEnvelopePath; subsequent steps leave it empty so
// ChainExecutor threads the previous output automatically.
func (p *Plan) ToChainSteps() []dispatch.ChainStep {
	steps := make([]dispatch.ChainStep, len(p.Steps))
	for i, s := range p.Steps {
		cs := dispatch.ChainStep{
			StepID:      s.StepID,
			Eidolon:     s.To.Eidolon,
			From:        s.From.Eidolon,
			To:          s.To.Eidolon,
			Performative: s.Performative,
		}
		if i == 0 && s.Artifact != nil {
			cs.InitialEnvelopePath = s.Artifact.Path
		}
		steps[i] = cs
	}
	return steps
}
