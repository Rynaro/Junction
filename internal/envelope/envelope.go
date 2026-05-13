// Package envelope is the ECL v1.0 envelope read/validate/emit surface.
// It provides typed Go structs mirroring ecl-envelope.v1.json, JSON schema
// validation via jsonschema/v5, and SHA-256 integrity verification per
// spec story S1.
package envelope

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Closed set of ECL v1.0 performatives (spec §2, performative.v1.json).
var validPerformatives = map[string]bool{
	"REQUEST":     true,
	"INFORM":      true,
	"PROPOSE":     true,
	"CRITIQUE":    true,
	"DECIDE":      true,
	"DELEGATE":    true,
	"ACKNOWLEDGE": true,
	"ESCALATE":    true,
	"RESUME":      true,
	"REFUSE":      true,
}

// Sentinel errors matching spec §5.5 failure classes.
var (
	// ErrIntegrityMismatch is returned when the recomputed SHA-256 of the
	// artifact file does not match integrity.value in the envelope.
	ErrIntegrityMismatch = errors.New("ecl: integrity mismatch — artifact bytes do not match integrity.value")

	// ErrIntegrityDigestMismatch is returned when integrity.value and
	// artifact.sha256 disagree in the envelope document itself (L2 check).
	ErrIntegrityDigestMismatch = errors.New("ecl: integrity digest mismatch — integrity.value != artifact.sha256")

	// ErrUnknownPerformative is returned when the performative is not in
	// the closed set of 10 (spec §2).
	ErrUnknownPerformative = errors.New("ecl: unknown performative — not in the closed set of 10")

	// ErrSchemaValidation is the base type for JSON schema validation errors.
	// Wrap it with additional context via fmt.Errorf.
	ErrSchemaValidation = errors.New("ecl: schema validation failed")
)

// AgentRef mirrors the agentRef $def in envelope.v1.json.
type AgentRef struct {
	Eidolon string `json:"eidolon"`
	Version string `json:"version"`
}

// Artifact mirrors the artifact object in envelope.v1.json.
type Artifact struct {
	Kind          string `json:"kind"`
	SchemaVersion string `json:"schema_version"`
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"size_bytes"`
}

// ContextDelta mirrors context-delta.v1.json embedded in an envelope.
type ContextDelta struct {
	TokenBudget  int      `json:"token_budget"`
	TokensUsed   int      `json:"tokens_used"`
	InputHandles []string `json:"input_handles"`
	Summary      string   `json:"summary"`
}

// Constraints mirrors the constraints sub-object.
type Constraints struct {
	DeadlineTS *string `json:"deadline_ts,omitempty"`
	TrustLevel string  `json:"trust_level,omitempty"`
}

// ExpectedResponse mirrors the expected_response sub-object.
type ExpectedResponse struct {
	Performative string `json:"performative,omitempty"`
	ShapeHint    string `json:"shape_hint,omitempty"`
}

// Integrity mirrors the integrity sub-object.
type Integrity struct {
	Method string `json:"method"`
	Value  string `json:"value"`
}

// Trace mirrors the trace sub-object.
type Trace struct {
	TS    string `json:"ts"`
	Host  string `json:"host"`
	Model string `json:"model"`
	Tier  string `json:"tier"`
}

// Envelope is the canonical Go representation of an ECL v1.0 envelope
// sidecar file. It mirrors the full structure of ecl-envelope.v1.json.
//
// Extension fields (x_*) are captured in Extensions to preserve round-trip
// fidelity without polluting the typed surface.
type Envelope struct {
	EnvelopeVersion  string           `json:"envelope_version"`
	MessageID        string           `json:"message_id"`
	ThreadID         string           `json:"thread_id"`
	ParentID         *string          `json:"parent_id"`
	From             AgentRef         `json:"from"`
	To               AgentRef         `json:"to"`
	Performative     string           `json:"performative"`
	EdgeOrigin       string           `json:"edge_origin,omitempty"`
	Objective        string           `json:"objective"`
	Artifact         Artifact         `json:"artifact"`
	ContextDelta     *ContextDelta    `json:"context_delta,omitempty"`
	Constraints      *Constraints     `json:"constraints,omitempty"`
	ExpectedResponse *ExpectedResponse `json:"expected_response,omitempty"`
	Confidence       *float64         `json:"confidence,omitempty"`
	Assumptions      []string         `json:"assumptions,omitempty"`
	Integrity        Integrity        `json:"integrity"`
	Trace            Trace            `json:"trace"`

	// Extensions captures any x_* vendor extension fields.
	Extensions map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler to capture x_* extension fields.
func (e *Envelope) UnmarshalJSON(data []byte) error {
	// Use an alias to avoid infinite recursion.
	type alias Envelope
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*e = Envelope(a)

	// Capture extension fields (x_*).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	exts := make(map[string]json.RawMessage)
	for k, v := range raw {
		if strings.HasPrefix(k, "x_") {
			exts[k] = v
		}
	}
	if len(exts) > 0 {
		e.Extensions = exts
	}
	return nil
}

// Read reads and decodes an ECL v1.0 envelope from the given file path.
// It does NOT validate the envelope — call Validate or VerifyIntegrity
// explicitly after reading.
func Read(path string) (*Envelope, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("envelope.Read: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("envelope.Read: %w", err)
	}

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("envelope.Read: %w", err)
	}
	return &env, nil
}

// ValidatePerformative checks whether the envelope's performative is a member
// of the closed 10-set (spec §2). This is a lightweight check that does not
// require the JSON schema; it can be called before or after schema validation.
func (e *Envelope) ValidatePerformative() error {
	if !validPerformatives[e.Performative] {
		return fmt.Errorf("%w: %q", ErrUnknownPerformative, e.Performative)
	}
	return nil
}

// VerifyIntegrity recomputes the SHA-256 digest of the artifact file at
// artifactPath and checks it against the envelope's integrity.value field.
//
// It also checks that integrity.value == artifact.sha256 within the envelope
// document itself (the in-document consistency check).
//
// Returns ErrIntegrityDigestMismatch if the in-document hashes disagree,
// or ErrIntegrityMismatch if the file's computed hash differs from
// integrity.value.
func (e *Envelope) VerifyIntegrity(artifactPath string) error {
	// In-document consistency: integrity.value must equal artifact.sha256.
	if !strings.EqualFold(e.Integrity.Value, e.Artifact.SHA256) {
		return fmt.Errorf("%w: integrity.value=%q artifact.sha256=%q",
			ErrIntegrityDigestMismatch, e.Integrity.Value, e.Artifact.SHA256)
	}

	// Recompute SHA-256 of the artifact bytes.
	digest, err := sha256File(artifactPath)
	if err != nil {
		return fmt.Errorf("envelope.VerifyIntegrity: reading artifact: %w", err)
	}

	if !strings.EqualFold(digest, e.Integrity.Value) {
		return fmt.Errorf("%w: computed=%q envelope=%q",
			ErrIntegrityMismatch, digest, e.Integrity.Value)
	}
	return nil
}

// sha256File computes the lowercase hex SHA-256 digest of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256Bytes computes the lowercase hex SHA-256 digest of the given byte
// slice. Exported for use by the emit path and tests.
func SHA256Bytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Emit writes the envelope as a JSON sidecar beside the artifact file.
// The sidecar is named "<artifactPath>.envelope.json".
//
// Emit recomputes artifact.sha256, artifact.size_bytes, and integrity.value
// from the artifact file at the time of emission so callers do not need to
// pre-populate those fields.
//
// Returns the path of the written sidecar file.
func (e *Envelope) Emit(artifactPath string) (string, error) {
	// Read the artifact to compute hash and size.
	artBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		return "", fmt.Errorf("envelope.Emit: reading artifact: %w", err)
	}
	digest := SHA256Bytes(artBytes)
	e.Artifact.SHA256 = digest
	e.Artifact.SizeBytes = int64(len(artBytes))
	e.Integrity.Value = digest
	if e.Integrity.Method == "" {
		e.Integrity.Method = "sha256"
	}
	if e.Trace.TS == "" {
		e.Trace.TS = time.Now().UTC().Format(time.RFC3339)
	}

	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return "", fmt.Errorf("envelope.Emit: marshaling: %w", err)
	}

	sidecar := artifactPath + ".envelope.json"
	if err := os.WriteFile(sidecar, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("envelope.Emit: writing sidecar: %w", err)
	}
	return sidecar, nil
}

// SidecarPath returns the conventional sidecar path for an artifact file.
func SidecarPath(artifactPath string) string {
	return artifactPath + ".envelope.json"
}

// ArtifactDir returns the directory containing a sidecar envelope file, which
// is also the directory that holds the artifact file.
func ArtifactDir(sidecarPath string) string {
	return filepath.Dir(sidecarPath)
}
