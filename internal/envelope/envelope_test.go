package envelope_test

// Tests for the envelope package (story S1 acceptance criteria + negative path).
//
// GIVEN/WHEN/THEN anchors are cited per the spec so reviewers can map each
// test back to a story acceptance criterion.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rynaro/Junction/internal/envelope"
)

// fixturesDir returns the absolute path to testdata/envelopes/.
// Tests that need fixture files use this helper.
func fixturesDir(t *testing.T) string {
	t.Helper()
	// The test binary's working directory is the package directory, so we
	// need to walk up two levels to reach the module root, then into
	// testdata/envelopes/.
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// internal/envelope → ../../testdata/envelopes
	return filepath.Join(here, "..", "..", "testdata", "envelopes")
}

// ─── S1: Round-trip happy path ────────────────────────────────────────────────

// GIVEN an envelope conforming to ECL v1.0
// WHEN Envelope.Validate runs
// THEN it returns nil.
func TestValidate_HappyPath(t *testing.T) {
	dir := fixturesDir(t)
	cases := []struct {
		sidecar  string
		artifact string
	}{
		{"scout-report.md.envelope.json", "scout-report.md"},
		{"spec.md.envelope.json", "spec.md"},
		{"apivr-completion-report.md.envelope.json", "apivr-completion-report.md"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.sidecar, func(t *testing.T) {
			env, err := envelope.Read(filepath.Join(dir, tc.sidecar))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if err := env.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

// GIVEN an envelope conforming to ECL v1.0
// WHEN VerifyIntegrity runs against the real artifact file
// THEN it returns nil.
func TestVerifyIntegrity_HappyPath(t *testing.T) {
	dir := fixturesDir(t)
	env, err := envelope.Read(filepath.Join(dir, "scout-report.md.envelope.json"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	artPath := filepath.Join(dir, env.Artifact.Path)
	if err := env.VerifyIntegrity(artPath); err != nil {
		t.Errorf("VerifyIntegrity() = %v, want nil", err)
	}
}

// GIVEN an artifact edited after emission
// WHEN VerifyIntegrity runs
// THEN it returns ErrIntegrityMismatch.
func TestVerifyIntegrity_TamperedArtifact(t *testing.T) {
	// Set up a temp dir with a copy of the fixture.
	dir := t.TempDir()
	srcDir := fixturesDir(t)

	// Copy artifact and sidecar.
	copyFile(t, filepath.Join(srcDir, "scout-report.md"), filepath.Join(dir, "scout-report.md"))
	copyFile(t, filepath.Join(srcDir, "scout-report.md.envelope.json"), filepath.Join(dir, "scout-report.md.envelope.json"))

	// Tamper with the artifact.
	if err := os.WriteFile(filepath.Join(dir, "scout-report.md"), []byte("TAMPERED CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}

	env, err := envelope.Read(filepath.Join(dir, "scout-report.md.envelope.json"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	err = env.VerifyIntegrity(filepath.Join(dir, "scout-report.md"))
	if err == nil {
		t.Fatal("VerifyIntegrity() = nil, want ErrIntegrityMismatch")
	}
	if !errors.Is(err, envelope.ErrIntegrityMismatch) {
		t.Errorf("VerifyIntegrity() = %v, want ErrIntegrityMismatch", err)
	}
}

// GIVEN integrity.value != artifact.sha256 in the envelope document
// WHEN VerifyIntegrity runs
// THEN it returns ErrIntegrityDigestMismatch.
func TestVerifyIntegrity_DigestMismatch(t *testing.T) {
	dir := fixturesDir(t)
	env, err := envelope.Read(filepath.Join(dir, "scout-report.md.envelope.json"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Break the in-document consistency.
	env.Integrity.Value = strings.Repeat("a", 64)

	artPath := filepath.Join(dir, env.Artifact.Path)
	err = env.VerifyIntegrity(artPath)
	if err == nil {
		t.Fatal("VerifyIntegrity() = nil, want ErrIntegrityDigestMismatch")
	}
	if !errors.Is(err, envelope.ErrIntegrityDigestMismatch) {
		t.Errorf("VerifyIntegrity() = %v, want ErrIntegrityDigestMismatch", err)
	}
}

// ─── S1: Schema validation negative paths ────────────────────────────────────

// GIVEN an envelope JSON with a required field absent (message_id deleted)
// WHEN ValidateBytes runs
// THEN it returns a wrapped ErrSchemaValidation.
func TestValidate_MissingRequiredField(t *testing.T) {
	dir := fixturesDir(t)
	data, err := os.ReadFile(filepath.Join(dir, "scout-report.md.envelope.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Delete the required message_id key from the JSON.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "message_id")
	mutated, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}

	if err := envelope.ValidateBytes(mutated); err == nil {
		t.Fatal("ValidateBytes(missing message_id) = nil, want ErrSchemaValidation")
	} else if !errors.Is(err, envelope.ErrSchemaValidation) {
		t.Errorf("ValidateBytes(missing message_id) = %v, want wrapped ErrSchemaValidation", err)
	}
}

// GIVEN an envelope with an invalid envelope_version pattern
// WHEN Validate runs
// THEN it returns a schema error pointing at the field.
func TestValidate_BadEnvelopeVersion(t *testing.T) {
	dir := fixturesDir(t)
	env, err := envelope.Read(filepath.Join(dir, "scout-report.md.envelope.json"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	env.EnvelopeVersion = "2.0" // Not in the "^1\.0" pattern

	err = env.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want schema error for envelope_version")
	}
	if !errors.Is(err, envelope.ErrSchemaValidation) {
		t.Errorf("Validate() = %v, want wrapped ErrSchemaValidation", err)
	}
}

// ─── S1: Performative closed-set check ───────────────────────────────────────

// GIVEN a valid envelope with a known performative
// WHEN ValidatePerformative runs
// THEN it returns nil.
func TestValidatePerformative_KnownValues(t *testing.T) {
	known := []string{
		"REQUEST", "INFORM", "PROPOSE", "CRITIQUE", "DECIDE",
		"DELEGATE", "ACKNOWLEDGE", "ESCALATE", "RESUME", "REFUSE",
	}
	env := &envelope.Envelope{}
	for _, p := range known {
		env.Performative = p
		if err := env.ValidatePerformative(); err != nil {
			t.Errorf("ValidatePerformative(%q) = %v, want nil", p, err)
		}
	}
}

// GIVEN an envelope with an unknown performative
// WHEN ValidatePerformative runs
// THEN it returns ErrUnknownPerformative.
func TestValidatePerformative_Unknown(t *testing.T) {
	env := &envelope.Envelope{Performative: "INVENT"}
	err := env.ValidatePerformative()
	if err == nil {
		t.Fatal("ValidatePerformative(INVENT) = nil, want ErrUnknownPerformative")
	}
	if !errors.Is(err, envelope.ErrUnknownPerformative) {
		t.Errorf("ValidatePerformative(INVENT) = %v, want ErrUnknownPerformative", err)
	}
}

// ─── S1: Emit ─────────────────────────────────────────────────────────────────

// GIVEN an envelope and an artifact file
// WHEN Emit is called
// THEN a sidecar file is written, the integrity fields are set correctly,
//
//	and a subsequent VerifyIntegrity returns nil.
func TestEmit_HappyPath(t *testing.T) {
	dir := t.TempDir()

	// Write a test artifact.
	artPath := filepath.Join(dir, "payload.txt")
	content := []byte("hello junction F1\n")
	if err := os.WriteFile(artPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	parentID := "cf492a47-a1ee-4622-ba33-d16a0514cfe9"
	threadID := "4042b976-63b7-4bc8-bbcf-15205a8e0ffd"
	env := &envelope.Envelope{
		EnvelopeVersion: "1.0",
		MessageID:       "00000000-0000-0000-0000-000000000001",
		ThreadID:        threadID,
		ParentID:        &parentID,
		From:            envelope.AgentRef{Eidolon: "atlas", Version: "1.4.2"},
		To:              envelope.AgentRef{Eidolon: "spectra", Version: "4.2.11"},
		Performative:    "PROPOSE",
		EdgeOrigin:      "roster",
		Objective:       "Test Emit round-trip.",
		Artifact: envelope.Artifact{
			Kind:          "scout-report",
			SchemaVersion: "1.0",
			Path:          "payload.txt",
		},
		Integrity: envelope.Integrity{Method: "sha256"},
		Trace: envelope.Trace{
			Host:  "test",
			Model: "test-model",
			Tier:  "standard",
		},
	}

	sidecar, err := env.Emit(artPath)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// Re-read the sidecar and verify round-trip.
	env2, err := envelope.Read(sidecar)
	if err != nil {
		t.Fatalf("Read after Emit: %v", err)
	}
	if err := env2.VerifyIntegrity(artPath); err != nil {
		t.Errorf("VerifyIntegrity after Emit: %v", err)
	}

	// Validate against schema.
	if err := env2.Validate(); err != nil {
		t.Errorf("Validate after Emit: %v", err)
	}
}

// ─── ValidateBytes ────────────────────────────────────────────────────────────

// GIVEN malformed JSON
// WHEN ValidateBytes runs
// THEN it returns an ErrSchemaValidation.
func TestValidateBytes_MalformedJSON(t *testing.T) {
	err := envelope.ValidateBytes([]byte(`{"not": "closed"`))
	if err == nil {
		t.Fatal("ValidateBytes(malformed JSON) = nil, want error")
	}
	if !errors.Is(err, envelope.ErrSchemaValidation) {
		t.Errorf("ValidateBytes(malformed JSON) = %v, want ErrSchemaValidation", err)
	}
}

// GIVEN valid ECL envelope bytes
// WHEN ValidateBytes runs
// THEN it returns nil.
func TestValidateBytes_ValidFixture(t *testing.T) {
	dir := fixturesDir(t)
	data, err := os.ReadFile(filepath.Join(dir, "scout-report.md.envelope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := envelope.ValidateBytes(data); err != nil {
		t.Errorf("ValidateBytes(valid fixture) = %v, want nil", err)
	}
}

// ─── SHA256Bytes ──────────────────────────────────────────────────────────────

func TestSHA256Bytes_KnownVector(t *testing.T) {
	// echo -n "" | sha256sum → e3b0...
	empty := envelope.SHA256Bytes([]byte{})
	if empty != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("SHA256Bytes([]) = %q, unexpected", empty)
	}
}

// ─── Read error path ─────────────────────────────────────────────────────────

func TestRead_NonExistentFile(t *testing.T) {
	_, err := envelope.Read("/tmp/no-such-envelope-xyzzy.json")
	if err == nil {
		t.Fatal("Read(nonexistent) = nil, want error")
	}
}

func TestRead_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json{{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := envelope.Read(path)
	if err == nil {
		t.Fatal("Read(bad JSON) = nil, want error")
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("copyFile: reading %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("copyFile: writing %s: %v", dst, err)
	}
}

