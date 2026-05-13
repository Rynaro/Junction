package trace_test

// Tests for the trace package (story S3 — append-only JSONL trace journal).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rynaro/Junction/internal/trace"
)

const testThreadID = "4042b976-63b7-4bc8-bbcf-15205a8e0ffd"

// ─── Open ────────────────────────────────────────────────────────────────────

func TestOpen_CreatesDirectory(t *testing.T) {
	root := t.TempDir()
	j, err := trace.Open(root, testThreadID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer j.Close()

	dir := filepath.Join(root, testThreadID)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("Open did not create thread directory %s", dir)
	}
}

func TestOpen_Idempotent(t *testing.T) {
	root := t.TempDir()
	j1, err := trace.Open(root, testThreadID)
	if err != nil {
		t.Fatalf("Open #1: %v", err)
	}
	j1.Close()

	j2, err := trace.Open(root, testThreadID)
	if err != nil {
		t.Fatalf("Open #2 (idempotent): %v", err)
	}
	j2.Close()
}

// ─── Append ──────────────────────────────────────────────────────────────────

func TestAppend_RequiresKind(t *testing.T) {
	root := t.TempDir()
	j, err := trace.Open(root, testThreadID)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	err = j.Append(trace.Event{}) // no Kind
	if err == nil {
		t.Fatal("Append with empty Kind = nil, want error")
	}
}

// ─── AppendEnvelope + ReadAll round-trip ─────────────────────────────────────

func TestAppendEnvelope_RoundTrip(t *testing.T) {
	root := t.TempDir()
	j, err := trace.Open(root, testThreadID)
	if err != nil {
		t.Fatal(err)
	}

	err = j.AppendEnvelope(
		"msg-001", "msg-000",
		"atlas@1.4.2", "spectra@4.2.11",
		"PROPOSE", "sha256", "claude-test", "standard",
		380,
	)
	if err != nil {
		t.Fatalf("AppendEnvelope: %v", err)
	}
	j.Close()

	events, err := trace.ReadAll(j.Path())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ReadAll: got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Kind != trace.KindEnvelope {
		t.Errorf("kind = %q, want %q", e.Kind, trace.KindEnvelope)
	}
	if e.MessageID != "msg-001" {
		t.Errorf("message_id = %q, want msg-001", e.MessageID)
	}
	if e.Performative != "PROPOSE" {
		t.Errorf("performative = %q, want PROPOSE", e.Performative)
	}
	if e.ThreadID != testThreadID {
		t.Errorf("thread_id = %q, want %q", e.ThreadID, testThreadID)
	}
}

// ─── AppendVerify ─────────────────────────────────────────────────────────────

func TestAppendVerify_AllPass(t *testing.T) {
	root := t.TempDir()
	j, err := trace.Open(root, testThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.AppendVerify("msg-001", true, true, true, true, ""); err != nil {
		t.Fatalf("AppendVerify failed: %v", err)
	}
	j.Close()

	events, _ := trace.ReadAll(j.Path())
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Kind != trace.KindVerify {
		t.Errorf("kind = %q, want verify", e.Kind)
	}
	if e.SchemaOK == nil || !*e.SchemaOK {
		t.Error("schema_ok should be true")
	}
	if e.IntegrityOK == nil || !*e.IntegrityOK {
		t.Error("integrity_ok should be true")
	}
	if e.EdgeOK == nil || !*e.EdgeOK {
		t.Error("edge_ok should be true")
	}
	if e.PerformOK == nil || !*e.PerformOK {
		t.Error("perform_ok should be true")
	}
}

// ─── Multiple events append-only ────────────────────────────────────────────

func TestJournal_MultipleAppends(t *testing.T) {
	root := t.TempDir()
	j, err := trace.Open(root, testThreadID)
	if err != nil {
		t.Fatal(err)
	}

	if err := j.AppendEnvelope("m1", "", "atlas@1.4.2", "spectra@4.2.11", "PROPOSE", "sha256", "m", "standard", 100); err != nil {
		t.Fatalf("AppendEnvelope failed: %v", err)
	}
	if err := j.AppendVerify("m1", true, true, true, true, ""); err != nil {
		t.Fatalf("AppendVerify failed: %v", err)
	}
	if err := j.AppendDispatch("S0", "m1", "atlas@1.4.2", "spectra@4.2.11", "shell", ""); err != nil {
		t.Fatalf("AppendDispatch failed: %v", err)
	}
	if err := j.AppendExit("S0", 0, ""); err != nil {
		t.Fatalf("AppendExit failed: %v", err)
	}
	j.Close()

	events, err := trace.ReadAll(j.Path())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4", len(events))
	}
	kinds := []trace.EventKind{
		trace.KindEnvelope, trace.KindVerify, trace.KindDispatch, trace.KindExit,
	}
	for i, want := range kinds {
		if events[i].Kind != want {
			t.Errorf("events[%d].kind = %q, want %q", i, events[i].Kind, want)
		}
	}
}

// ─── AppendError ─────────────────────────────────────────────────────────────

func TestAppendError(t *testing.T) {
	root := t.TempDir()
	j, err := trace.Open(root, testThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.AppendError("S1", "something went wrong"); err != nil {
		t.Fatalf("AppendError failed: %v", err)
	}
	j.Close()

	events, _ := trace.ReadAll(j.Path())
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Kind != trace.KindError {
		t.Errorf("kind = %q, want error", events[0].Kind)
	}
	if events[0].Error != "something went wrong" {
		t.Errorf("error = %q, unexpected", events[0].Error)
	}
}

// ─── ReadAll on existing ECL example trace ───────────────────────────────────

func TestReadAll_ExistingECLTrace(t *testing.T) {
	// Use the known-good trace from ECL examples via testdata.
	path := filepath.Join("..", "..", "testdata", "trace",
		"4042b976-63b7-4bc8-bbcf-15205a8e0ffd.jsonl")
	events, err := trace.ReadAll(path)
	if err != nil {
		t.Skipf("ECL example trace not in testdata (skipping): %v", err)
	}
	if len(events) == 0 {
		t.Error("ReadAll returned 0 events for the ECL example trace")
	}
}
