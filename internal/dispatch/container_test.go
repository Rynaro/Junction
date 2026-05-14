package dispatch_test

// Tests for ContainerExecutor (S2.1 + F10-S1 two-phase round-trip).
//
// All tests use a stubCommandRunner that returns canned responses so no
// real Docker daemon is required.
//
// F10-S1 two-phase round-trip test (TestContainerExecutor_TwoPhaseRoundTrip)
// verifies the full assemble→reasoning→package orchestration using:
//   - a writing stub runner that simulates the container writing artefacts
//   - a canned ReasoningStepFunc that supplies reasoning.json
//   - schema+integrity validation on the output *.envelope.json

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rynaro/Junction/internal/dispatch"
	"github.com/Rynaro/Junction/internal/envelope"
	"github.com/Rynaro/Junction/internal/trace"
)

// ─── stub runner ─────────────────────────────────────────────────────────────

// stubCall records one expected invocation.
type stubCall struct {
	// match is a substring of the joined args that must appear for this
	// stub to activate (empty = matches everything).
	match  string
	stdout string
	stderr string
	err    error
}

// stubRunner is a simple sequential stub: each Run call pops the first
// matching stubCall from the queue, or returns success if the queue is empty.
type stubRunner struct {
	calls []stubCall
}

func (s *stubRunner) Run(_ context.Context, _ []string, _ string, args ...string) (string, string, error) {
	joined := joinArgs(args)
	for i, c := range s.calls {
		if c.match == "" || containsStr(joined, c.match) {
			// Remove matched call from the queue.
			s.calls = append(s.calls[:i], s.calls[i+1:]...)
			return c.stdout, c.stderr, c.err
		}
	}
	// Default: success.
	return "", "", nil
}

func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}

func containsStr(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	n := len(substr)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == substr {
			return true
		}
	}
	return false
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// writeFixtureEnvelope writes a minimal envelope file and returns its path.
func writeFixtureEnvelope(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "input.envelope.json")
	if err := os.WriteFile(p, []byte(`{"message_id":"test-001"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// stubContainerExecutor creates a ContainerExecutor backed by a stubRunner.
// The stub runner is pre-loaded with the provided calls.
func stubContainerExecutor(calls []stubCall) *dispatch.ContainerExecutor {
	return &dispatch.ContainerExecutor{
		Runner:          &stubRunner{calls: calls},
		EidolonVersion:  "1.0.0",
		SkipDaemonProbe: true,
	}
}

// ─── S2.1 tests ──────────────────────────────────────────────────────────────

// GIVEN an Eidolon image that pulls and the container exits 0
// WHEN Execute is called
// THEN Result.ExitCode == 0 and ImageRef is set to the resolved image.
func TestContainerExecutor_HappyPath(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	stub := stubContainerExecutor([]stubCall{
		{match: "pull", stdout: "", stderr: "", err: nil},           // pull succeeds
		{match: "run", stdout: "", stderr: "", err: nil},            // run succeeds
		{match: "inspect", stdout: "sha256:abcdef", stderr: "", err: nil}, // digest
	})

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-container-001",
		OutputDir:    outDir,
	}

	result, err := stub.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.ImageRef == "" {
		t.Error("ImageRef is empty, expected a resolved image reference")
	}
}

// GIVEN JUNCTION_EIDOLON_IMAGE_ATLAS env var is set
// WHEN Execute is called
// THEN the env-var image is used (no pull probe for default image).
func TestContainerExecutor_EnvVarImageOverride(t *testing.T) {
	t.Setenv("JUNCTION_EIDOLON_IMAGE_ATLAS", "localhost/stub-atlas:test")

	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	// No pull call should be made for the override image.
	stub := stubContainerExecutor([]stubCall{
		{match: "run", stdout: "", stderr: "", err: nil},
		{match: "inspect", stdout: "sha256:localstub", stderr: "", err: nil},
	})

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-env-override",
		OutputDir:    outDir,
	}

	result, err := stub.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ImageRef != "localhost/stub-atlas:test" {
		t.Errorf("ImageRef = %q, want localhost/stub-atlas:test", result.ImageRef)
	}
}

// GIVEN docker pull fails (image not available)
// WHEN Execute is called (no --no-container override)
// THEN Execute returns ErrImageNotAvailable (maps to exit 71).
func TestContainerExecutor_ImagePullFailed_Exit71(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	stub := stubContainerExecutor([]stubCall{
		{match: "pull", stdout: "", stderr: "not found", err: errors.New("exit status 1")},
	})

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-pull-fail",
		OutputDir:    outDir,
	}

	_, err := stub.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Execute with pull failure = nil, want ErrImageNotAvailable")
	}
	if !errors.Is(err, dispatch.ErrImageNotAvailable) {
		t.Errorf("error = %v, want ErrImageNotAvailable (exit 71)", err)
	}
}

// GIVEN Docker daemon is unreachable (SkipDaemonProbe=false)
// WHEN Execute is called
// THEN Execute returns ErrDockerUnreachable (maps to exit 72).
func TestContainerExecutor_DockerUnreachable_Exit72(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	exec := &dispatch.ContainerExecutor{
		Runner: &stubRunner{calls: []stubCall{
			{match: "version", stdout: "", stderr: "cannot connect", err: errors.New("exit status 1")},
		}},
		EidolonVersion:  "1.0.0",
		SkipDaemonProbe: false, // probe enabled
	}

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-no-daemon",
		OutputDir:    outDir,
	}

	_, err := exec.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Execute with unreachable daemon = nil, want ErrDockerUnreachable")
	}
	if !errors.Is(err, dispatch.ErrDockerUnreachable) {
		t.Errorf("error = %v, want ErrDockerUnreachable (exit 72)", err)
	}
}

// GIVEN the container exits non-zero
// WHEN Execute is called
// THEN Execute returns ErrDispatchFailed.
func TestContainerExecutor_ContainerExitNonZero(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	stub := stubContainerExecutor([]stubCall{
		{match: "pull", stdout: "", err: nil},
		{match: "run", stdout: "", err: errors.New("exit status 2")},
	})

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-exit-nonzero",
		OutputDir:    outDir,
	}

	_, err := stub.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Execute with non-zero container = nil, want ErrDispatchFailed")
	}
	if !errors.Is(err, dispatch.ErrDispatchFailed) {
		t.Errorf("error = %v, want ErrDispatchFailed", err)
	}
}

// GIVEN the container writes an output envelope to the out/ dir
// WHEN Execute is called
// THEN Result.OutputEnvelopePath is non-empty.
func TestContainerExecutor_OutputEnvelopeDiscovered(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	// Pre-create the output envelope (simulating what the container would write).
	outEnvPath := filepath.Join(outDir, "output.md.envelope.json")
	if err := os.WriteFile(outEnvPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stub := stubContainerExecutor([]stubCall{
		{match: "pull", stdout: "", err: nil},
		{match: "run", stdout: "", err: nil},
		{match: "inspect", stdout: "sha256:xyz", err: nil},
	})

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-out-env",
		OutputDir:    outDir,
	}

	result, err := stub.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.OutputEnvelopePath == "" {
		t.Error("OutputEnvelopePath is empty, expected it to be discovered")
	}
}

// GIVEN a successful run
// WHEN Execute is called
// THEN JUNCTION_THREAD_ID and JUNCTION_INPUT_ENVELOPE appear in the docker run args.
func TestContainerExecutor_EnvVarPropagation(t *testing.T) {
	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	// Use a recording stub that captures the args of the run call.
	var capturedArgs []string
	recordRunner := &recordingRunner{
		onRun: func(args []string) {
			if len(args) > 0 && args[0] == "run" {
				capturedArgs = append(capturedArgs, args...)
			}
		},
	}

	exec := &dispatch.ContainerExecutor{
		Runner:          recordRunner,
		EidolonVersion:  "1.0.0",
		SkipDaemonProbe: true,
	}

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "expected-thread-xyz",
		OutputDir:    outDir,
	}

	_, _ = exec.Execute(context.Background(), req)

	wantEnvVars := []string{"JUNCTION_THREAD_ID=expected-thread-xyz", "ECL_THREAD_ID=expected-thread-xyz"}
	for _, want := range wantEnvVars {
		found := false
		for _, arg := range capturedArgs {
			if arg == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("env var %q not found in docker run args: %v", want, capturedArgs)
		}
	}
}

// recordingRunner captures args and always succeeds.
type recordingRunner struct {
	onRun func(args []string)
}

func (r *recordingRunner) Run(_ context.Context, _ []string, _ string, args ...string) (string, string, error) {
	if r.onRun != nil {
		r.onRun(args)
	}
	return "", "", nil
}

// ─── F10-S1 two-phase round-trip fixture ─────────────────────────────────────

// writingStubRunner is a CommandRunner that simulates container behaviour by
// writing artefact files to the host filesystem as side effects.
// This lets the two-phase round-trip test exercise real file I/O without a
// Docker daemon.
type writingStubRunner struct {
	// assembleOut is a map of filename→content written to outDir on the assemble call.
	assembleOut map[string][]byte
	// packageOut is a map of filename→content written to outDir on the package call.
	packageOut map[string][]byte
	// calls tracks the sequence of docker sub-commands received.
	calls []string
}

func (w *writingStubRunner) Run(_ context.Context, _ []string, _ string, args ...string) (string, string, error) {
	if len(args) == 0 {
		return "", "", nil
	}
	sub := args[0]
	w.calls = append(w.calls, sub)

	// For "run" calls, detect the phase from args and write the matching files.
	if sub == "run" {
		phase := phaseFromArgs(args)
		// Locate the out-dir mount: "-v <hostPath>:/junction/io/out:rw"
		outDir := outDirFromArgs(args)
		switch phase {
		case "assemble":
			for name, data := range w.assembleOut {
				if writeErr := os.WriteFile(filepath.Join(outDir, name), data, 0o644); writeErr != nil {
					return "", "", writeErr
				}
			}
		case "package":
			for name, data := range w.packageOut {
				if writeErr := os.WriteFile(filepath.Join(outDir, name), data, 0o644); writeErr != nil {
					return "", "", writeErr
				}
			}
		}
	}
	return "", "", nil
}

// phaseFromArgs extracts JUNCTION_PHASE=<value> from docker run args.
func phaseFromArgs(args []string) string {
	prefix := "JUNCTION_PHASE="
	for _, a := range args {
		if len(a) > len(prefix) && a[:len(prefix)] == prefix {
			return a[len(prefix):]
		}
	}
	return ""
}

// outDirFromArgs extracts the host path from a -v <host>:/junction/io/out:rw mount.
func outDirFromArgs(args []string) string {
	suffix := ":/junction/io/out:rw"
	for _, a := range args {
		if len(a) > len(suffix) && a[len(a)-len(suffix):] == suffix {
			return a[:len(a)-len(suffix)]
		}
	}
	return ""
}

// buildValidOutputEnvelope creates a minimal, schema-valid ECL v2.0 envelope
// JSON bytes for an artifact file at artifactPath, with correct SHA-256.
// Used by the two-phase round-trip fixture to simulate the package-phase output.
func buildValidOutputEnvelope(t *testing.T, artifactBytes []byte) []byte {
	t.Helper()
	digest := sha256Hex(artifactBytes)
	parentID := "00000000-0000-0000-0000-000000000001"
	env := map[string]interface{}{
		"envelope_version": "1.0",
		"message_id":       "f10s1-pkg-out-001",
		"thread_id":        "thread-two-phase-001",
		"parent_id":        parentID,
		"from": map[string]string{
			"eidolon": "atlas",
			"version": "1.5.2",
		},
		"to": map[string]string{
			"eidolon": "spectra",
			"version": "4.3.2",
		},
		"performative": "PROPOSE",
		"objective":    "Two-phase round-trip fixture output.",
		"artifact": map[string]interface{}{
			"kind":           "scout-report",
			"schema_version": "1.0",
			"path":           "output.md",
			"sha256":         digest,
			"size_bytes":     len(artifactBytes),
		},
		"integrity": map[string]string{
			"method": "sha256",
			"value":  digest,
		},
		"trace": map[string]string{
			"ts":    time.Now().UTC().Format(time.RFC3339),
			"host":  "junction-test",
			"model": "fixture",
			"tier":  "standard",
		},
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// cannedReasoningJSON is a minimal valid reasoning.json for the fixture.
const cannedReasoningJSON = `{
  "schema_version": "1.0",
  "eidolon": "atlas",
  "thread_id": "thread-two-phase-001",
  "parent_id": "00000000-0000-0000-0000-000000000001",
  "performative": "PROPOSE",
  "body": "Fixture reasoning output for F10-S1 two-phase round-trip test.",
  "evidence_anchors": []
}
`

// TestContainerExecutor_TwoPhaseRoundTrip is the F10-S1 acceptance test.
//
// GIVEN a ContainerExecutor with a writing stub runner and a canned reasoning step
// WHEN Execute is called
// THEN:
//  1. The assemble phase runs first (stub writes prompt-bundle.json to out/).
//  2. The canned ReasoningStep writes reasoning.json to in/.
//  3. The package phase runs second (stub writes a valid *.envelope.json to out/).
//  4. Result.OutputEnvelopePath is non-empty.
//  5. The output envelope passes ECL schema validation (L1).
//  6. The output envelope's SHA-256 integrity tag matches the artifact.
//  7. The trace journal records two dispatch events with phase="assemble" and
//     phase="package", with a host_reasoning event in between.
func TestContainerExecutor_TwoPhaseRoundTrip(t *testing.T) {
	t.Setenv("JUNCTION_EIDOLON_IMAGE_ATLAS", "localhost/stub-atlas:test")

	base := t.TempDir()
	inDir := filepath.Join(base, "in")
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := writeFixtureEnvelope(t, inDir)

	// Artifact bytes that the package phase "produces" (used to compute SHA-256).
	artifactBytes := []byte("fixture scout-report content for F10-S1 two-phase round-trip\n")

	// Build the package-phase envelope using the real artifact digest so the
	// integrity check passes.
	packageEnvBytes := buildValidOutputEnvelope(t, artifactBytes)

	// Prompt-bundle.json written by the assemble phase.
	promptBundle := []byte(`{"schema_version":"1.0","eidolon":"atlas","ecl_version":"2.0","agent_md":"","selected_skills":[],"input_payload":"","response_template":""}` + "\n")

	stub := &writingStubRunner{
		assembleOut: map[string][]byte{
			"prompt-bundle.json": promptBundle,
		},
		packageOut: map[string][]byte{
			"output.md":                  artifactBytes,
			"output.md.envelope.json":    packageEnvBytes,
		},
	}

	// Open a trace journal so we can verify the phase dispatch records.
	traceRoot := t.TempDir()
	journal, err := trace.Open(traceRoot, "thread-two-phase-001")
	if err != nil {
		t.Fatalf("trace.Open: %v", err)
	}
	defer journal.Close()

	// cannedReasoning is the injectable ReasoningStepFunc: writes a fixture
	// reasoning.json into inDir (NG17: no LLM call).
	var reasoningStepCalled bool
	cannedReasoning := func(_ context.Context, _ string, inD, _ string) error {
		reasoningStepCalled = true
		return os.WriteFile(filepath.Join(inD, "reasoning.json"), []byte(cannedReasoningJSON), 0o644)
	}

	exec := &dispatch.ContainerExecutor{
		Runner:          stub,
		EidolonVersion:  "1.5.2",
		SkipDaemonProbe: true,
		ReasoningStep:   cannedReasoning,
		Journal:         journal,
	}

	req := dispatch.Request{
		StepID:       "S0",
		Eidolon:      "atlas",
		EnvelopePath: envPath,
		ThreadID:     "thread-two-phase-001",
		OutputDir:    outDir,
	}

	result, execErr := exec.Execute(context.Background(), req)
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}

	// ── AC1: ReasoningStep was called ────────────────────────────────────────
	if !reasoningStepCalled {
		t.Error("ReasoningStep was not called — expected one call between assemble and package")
	}

	// ── AC2: reasoning.json present in inDir ─────────────────────────────────
	reasoningPath := filepath.Join(inDir, "reasoning.json")
	if _, statErr := os.Stat(reasoningPath); os.IsNotExist(statErr) {
		t.Error("reasoning.json not found in inDir after reasoning step")
	}

	// ── AC3: package produced exactly one *.envelope.json ────────────────────
	if result.OutputEnvelopePath == "" {
		t.Fatal("Result.OutputEnvelopePath is empty — expected one *.envelope.json from package phase")
	}

	// ── AC4: output envelope passes L1 schema validation ─────────────────────
	outEnvData, err := os.ReadFile(result.OutputEnvelopePath)
	if err != nil {
		t.Fatalf("reading output envelope: %v", err)
	}
	if err := envelope.ValidateBytes(outEnvData); err != nil {
		t.Errorf("output envelope schema validation (L1) failed: %v", err)
	}

	// ── AC5: output envelope passes L2 integrity check ───────────────────────
	outEnv, err := envelope.Read(result.OutputEnvelopePath)
	if err != nil {
		t.Fatalf("envelope.Read: %v", err)
	}
	artifactPath := filepath.Join(outDir, outEnv.Artifact.Path)
	if err := outEnv.VerifyIntegrity(artifactPath); err != nil {
		t.Errorf("output envelope integrity (L2) failed: %v", err)
	}

	// ── AC6: trace shows assemble dispatch, host_reasoning, package dispatch ──
	journal.Close()
	events, err := trace.ReadAll(journal.Path())
	if err != nil {
		t.Fatalf("trace.ReadAll: %v", err)
	}

	// Expect: dispatch(assemble), host_reasoning, dispatch(package)
	if len(events) < 3 {
		t.Fatalf("trace has %d events, want at least 3 (assemble dispatch, host_reasoning, package dispatch)", len(events))
	}

	var assembleDispatch, hostReasoning, packageDispatch bool
	for _, e := range events {
		switch {
		case e.Kind == trace.KindDispatch && e.Phase == "assemble":
			assembleDispatch = true
		case e.Kind == trace.KindHostReasoning:
			hostReasoning = true
			if e.Input != "prompt-bundle.json" {
				t.Errorf("host_reasoning.input = %q, want prompt-bundle.json", e.Input)
			}
			if e.Output != "reasoning.json" {
				t.Errorf("host_reasoning.output = %q, want reasoning.json", e.Output)
			}
		case e.Kind == trace.KindDispatch && e.Phase == "package":
			packageDispatch = true
		}
	}
	if !assembleDispatch {
		t.Error("trace missing dispatch record with phase=assemble")
	}
	if !hostReasoning {
		t.Error("trace missing host_reasoning record")
	}
	if !packageDispatch {
		t.Error("trace missing dispatch record with phase=package")
	}

	// ── AC7: runner received two "run" calls (assemble then package) ──────────
	runCount := 0
	for _, c := range stub.calls {
		if c == "run" {
			runCount++
		}
	}
	if runCount != 2 {
		t.Errorf("runner received %d run calls, want 2 (assemble + package)", runCount)
	}

	// ── AC8: result.ExitCode == 0 ─────────────────────────────────────────────
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
}
