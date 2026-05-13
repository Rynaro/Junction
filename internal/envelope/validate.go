package envelope

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rynaro/Junction/internal/schemas"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// validateArtifactPath checks security constraints on the artifact path that
// cannot be expressed in Go-compatible regex:
// - MUST NOT begin with '/'  (caught by schema regex ^[^/].+$)
// - MUST NOT contain '..'    (path traversal guard)
func validateArtifactPath(path string) error {
	if path == "" {
		return nil // schema "required" check catches this
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." {
			return fmt.Errorf("%w: artifact.path contains '..': %q", ErrSchemaValidation, path)
		}
	}
	return nil
}

// schemaID is the canonical $id of the envelope schema as declared in the
// vendored envelope.v1.json. The jsonschema/v5 loader uses this as the
// registration key when we add sub-schemas by their declared $id.
const (
	envelopeSchemaID   = "https://github.com/Rynaro/eidolons-ecl/blob/v2.0.0/schemas/envelope.v1.json"
	performativeID     = "https://github.com/Rynaro/eidolons-ecl/blob/v2.0.0/schemas/performative.v1.json"
	contextDeltaID     = "https://github.com/Rynaro/eidolons-ecl/blob/v2.0.0/schemas/context-delta.v1.json"
)

// compiledSchema is the singleton compiled schema, built once on first use.
// Tests and multiple Validate calls share the same compiled schema.
var compiledSchema *jsonschema.Schema

// getSchema returns the compiled ECL envelope v1.0 JSON schema, compiling it
// on first call. Thread-safety is not required for the F1 scope (single-
// goroutine verification path).
func getSchema() (*jsonschema.Schema, error) {
	if compiledSchema != nil {
		return compiledSchema, nil
	}

	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020

	// Add sub-schemas by their declared $id so the $ref resolution in the
	// envelope schema resolves without network access.
	if err := c.AddResource(performativeID, bytes.NewReader(schemas.PerformativeV1)); err != nil {
		return nil, fmt.Errorf("envelope: loading performative schema: %w", err)
	}
	if err := c.AddResource(contextDeltaID, bytes.NewReader(schemas.ContextDeltaV1)); err != nil {
		return nil, fmt.Errorf("envelope: loading context-delta schema: %w", err)
	}
	if err := c.AddResource(envelopeSchemaID, bytes.NewReader(schemas.EnvelopeV1)); err != nil {
		return nil, fmt.Errorf("envelope: loading envelope schema: %w", err)
	}

	schema, err := c.Compile(envelopeSchemaID)
	if err != nil {
		return nil, fmt.Errorf("envelope: compiling schema: %w", err)
	}
	compiledSchema = schema
	return compiledSchema, nil
}

// Validate validates the envelope against the ECL v1.0 JSON schema (L1 check)
// and then checks the performative against the closed set of 10 (spec §2).
//
// The envelope is re-serialised to a map[string]any before schema validation
// so we validate exactly the JSON representation, not the Go struct.
//
// Returns a wrapped ErrSchemaValidation on schema failure, or
// ErrUnknownPerformative for a bad performative.
func (e *Envelope) Validate() error {
	schema, err := getSchema()
	if err != nil {
		return err
	}

	// Re-serialise to map for schema validation (handles x_* extension fields
	// and ensures we test the actual wire format).
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("envelope.Validate: marshaling: %w", err)
	}

	var doc interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("envelope.Validate: unmarshaling for schema: %w", err)
	}

	if err := schema.Validate(doc); err != nil {
		// Extract a concise summary from the validation error.
		var ve *jsonschema.ValidationError
		if ok := isValidationError(err, &ve); ok {
			return fmt.Errorf("%w: %s", ErrSchemaValidation, summariseValidationError(ve))
		}
		return fmt.Errorf("%w: %s", ErrSchemaValidation, err.Error())
	}

	// Path traversal guard (complement to the schema regex; see vendored schema note).
	if err := validateArtifactPath(e.Artifact.Path); err != nil {
		return err
	}

	// Performative closed-set check (belt-and-suspenders beyond the schema).
	return e.ValidatePerformative()
}

// ValidateBytes validates raw JSON bytes (as would be read from a sidecar
// file) against the ECL envelope schema without first deserialising to the
// Go struct. Useful for verifying envelopes whose structure may not match
// the current Envelope type (e.g. v2 example envelopes that include ISE
// extensions not in the F1 struct).
func ValidateBytes(data []byte) error {
	schema, err := getSchema()
	if err != nil {
		return err
	}

	var doc interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%w: invalid JSON: %s", ErrSchemaValidation, err.Error())
	}

	if err := schema.Validate(doc); err != nil {
		var ve *jsonschema.ValidationError
		if ok := isValidationError(err, &ve); ok {
			return fmt.Errorf("%w: %s", ErrSchemaValidation, summariseValidationError(ve))
		}
		return fmt.Errorf("%w: %s", ErrSchemaValidation, err.Error())
	}
	return nil
}

// isValidationError tests whether err is a *jsonschema.ValidationError and
// sets *target if so. This avoids a direct type assertion that would fail if
// the error is wrapped.
func isValidationError(err error, target **jsonschema.ValidationError) bool {
	ve, ok := err.(*jsonschema.ValidationError)
	if ok {
		*target = ve
	}
	return ok
}

// summariseValidationError extracts the most useful field path and message
// from a jsonschema.ValidationError tree.
func summariseValidationError(ve *jsonschema.ValidationError) string {
	if ve == nil {
		return "unknown validation error"
	}
	// Walk the causes tree to find the deepest leaf messages.
	var leaves []string
	collectLeaves(ve, &leaves)
	if len(leaves) > 0 {
		return strings.Join(leaves, "; ")
	}
	return ve.Error()
}

func collectLeaves(ve *jsonschema.ValidationError, out *[]string) {
	if len(ve.Causes) == 0 {
		*out = append(*out, fmt.Sprintf("%s: %s", ve.InstanceLocation, ve.Message))
		return
	}
	for _, c := range ve.Causes {
		collectLeaves(c, out)
	}
}
