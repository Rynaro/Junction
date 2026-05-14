// Package trace owns the append-only JSONL trace journal for a Junction
// thread. Every significant event — envelope received, validated, dispatched,
// response received, error — is appended as one JSON line.
//
// The journal lives at $JUNCTION_TRACE_ROOT/<thread_id>/trace.jsonl
// (default JUNCTION_TRACE_ROOT=.junction/threads/).
//
// Design invariant: writes are crash-safe via fsync. Reads scan from the
// beginning so replay is deterministic even after a crash mid-write.
package trace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultTraceRoot is the default base directory for trace journals, relative
// to the consumer project's working directory.
const DefaultTraceRoot = ".junction/threads"

// EventKind is the discriminator for a trace journal entry.
// Values are a subset of the kinds defined in spec §5.4.
type EventKind string

const (
	KindEnvelope      EventKind = "envelope"
	KindVerify        EventKind = "verify"
	KindDispatch      EventKind = "dispatch"
	KindHostReasoning EventKind = "host_reasoning"
	KindExit          EventKind = "exit"
	KindRefuse        EventKind = "refuse"
	KindEscalate      EventKind = "escalate"
	KindHumanInject   EventKind = "human_inject"
	KindResume        EventKind = "resume_marker"
	KindError         EventKind = "error"
)

// Event is a single journal entry. All fields except Kind and TS are
// optional; consumers populate only what is meaningful for the event.
type Event struct {
	// Core fields — always present.
	Kind     EventKind `json:"kind"`
	TS       string    `json:"ts"`
	ThreadID string    `json:"thread_id,omitempty"`

	// Envelope identity — present on envelope / verify / dispatch / exit.
	MessageID    string `json:"message_id,omitempty"`
	ParentID     string `json:"parent_id,omitempty"`
	From         string `json:"from,omitempty"`  // "eidolon@version"
	To           string `json:"to,omitempty"`    // "eidolon@version"
	Performative string `json:"performative,omitempty"`

	// Integrity / schema check results — present on verify events.
	SchemaOK    *bool  `json:"schema_ok,omitempty"`
	IntegrityOK *bool  `json:"integrity_ok,omitempty"`
	EdgeOK      *bool  `json:"edge_ok,omitempty"`
	PerformOK   *bool  `json:"perform_ok,omitempty"`

	// Dispatch context — present on dispatch / exit events.
	StepID      string `json:"step_id,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	Executor    string `json:"executor,omitempty"`     // "shell" | "container" (spec §5.4 round 4)
	ImageDigest string `json:"image_digest,omitempty"` // populated when executor == "container"
	Phase       string `json:"phase,omitempty"`        // "assemble" | "package" (F10-S1 two-phase; only for container dispatch)

	// Error detail — present on error / refuse / escalate events.
	Error string `json:"error,omitempty"`

	// Host-reasoning context — present on host_reasoning events (F10-S1).
	Input      string `json:"input,omitempty"`       // relative name of the prompt-bundle file
	Output     string `json:"output,omitempty"`      // relative name of the reasoning.json file
	DurationMS int64  `json:"duration_ms,omitempty"` // wall-clock duration in milliseconds

	// Tracing metadata — present on envelope events.
	IntegrityMethod string `json:"integrity_method,omitempty"`
	ContextTokens   int    `json:"context_tokens,omitempty"`
	Model           string `json:"model,omitempty"`
	Tier            string `json:"tier,omitempty"`
}

// now is a hook for tests to override the current time.
var now = func() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// Journal is an append-only JSONL trace writer for one thread.
// All public methods are safe for concurrent use.
type Journal struct {
	mu       sync.Mutex
	threadID string
	path     string
	f        *os.File
}

// Open opens (or creates) the journal for threadID under traceRoot.
// The directory <traceRoot>/<threadID>/ is created if necessary.
// Subsequent Append calls are appended to the file.
func Open(traceRoot, threadID string) (*Journal, error) {
	dir := filepath.Join(traceRoot, threadID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("trace.Open: mkdir: %w", err)
	}
	path := filepath.Join(dir, "trace.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("trace.Open: open: %w", err)
	}
	return &Journal{
		threadID: threadID,
		path:     path,
		f:        f,
	}, nil
}

// Close flushes and closes the underlying file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	if err := j.f.Sync(); err != nil {
		_ = j.f.Close()
		j.f = nil
		return fmt.Errorf("trace.Close: sync: %w", err)
	}
	err := j.f.Close()
	j.f = nil
	return err
}

// Path returns the absolute path of the trace file.
func (j *Journal) Path() string {
	return j.path
}

// Append writes one event as a single JSON line, followed by a newline.
// It sets Kind, TS, and ThreadID if they are empty.
// The write is fsynced before returning so a subsequent crash does not lose
// the event.
func (j *Journal) Append(e Event) error {
	if e.Kind == "" {
		return errors.New("trace.Append: event Kind is required")
	}
	if e.TS == "" {
		e.TS = now()
	}
	if e.ThreadID == "" {
		e.ThreadID = j.threadID
	}

	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("trace.Append: marshal: %w", err)
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if _, err := j.f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("trace.Append: write: %w", err)
	}
	if err := j.f.Sync(); err != nil {
		return fmt.Errorf("trace.Append: sync: %w", err)
	}
	return nil
}

// boolPtr is a helper to get a *bool for the optional schema check fields.
func boolPtr(b bool) *bool {
	return &b
}

// AppendEnvelope records an "envelope received" event from a parsed envelope.
func (j *Journal) AppendEnvelope(messageID, parentID, from, to, performative, integrityMethod, model, tier string, contextTokens int) error {
	return j.Append(Event{
		Kind:            KindEnvelope,
		MessageID:       messageID,
		ParentID:        parentID,
		From:            from,
		To:              to,
		Performative:    performative,
		IntegrityMethod: integrityMethod,
		ContextTokens:   contextTokens,
		Model:           model,
		Tier:            tier,
	})
}

// AppendVerify records a verification result for a single envelope.
func (j *Journal) AppendVerify(messageID string, schemaOK, integrityOK, edgeOK, performOK bool, errMsg string) error {
	return j.Append(Event{
		Kind:        KindVerify,
		MessageID:   messageID,
		SchemaOK:    boolPtr(schemaOK),
		IntegrityOK: boolPtr(integrityOK),
		EdgeOK:      boolPtr(edgeOK),
		PerformOK:   boolPtr(performOK),
		Error:       errMsg,
	})
}

// AppendDispatch records that an Eidolon was dispatched for a step.
// executor is "shell" or "container"; imageDigest is the container image
// digest when executor == "container" (empty string for shell dispatches).
// For two-phase container dispatch, use AppendDispatchPhase instead.
func (j *Journal) AppendDispatch(stepID, messageID, from, to, executor, imageDigest string) error {
	return j.Append(Event{
		Kind:        KindDispatch,
		StepID:      stepID,
		MessageID:   messageID,
		From:        from,
		To:          to,
		Executor:    executor,
		ImageDigest: imageDigest,
	})
}

// AppendDispatchPhase records a single phase of a two-phase container dispatch
// (F10-S1). phase must be "assemble" or "package". The Phase field is included
// in the emitted JSON record so post-hoc trace inspection can correlate the two
// records with their respective container invocations.
func (j *Journal) AppendDispatchPhase(stepID, messageID, from, to, executor, imageDigest, phase string) error {
	return j.Append(Event{
		Kind:        KindDispatch,
		StepID:      stepID,
		MessageID:   messageID,
		From:        from,
		To:          to,
		Executor:    executor,
		ImageDigest: imageDigest,
		Phase:       phase,
	})
}

// AppendHostReasoning records the host-LLM reasoning step between the assemble
// and package container invocations (F10-S1). inputFile and outputFile are the
// relative names of the intermediate artefacts (prompt-bundle.json and
// reasoning.json). durationMS is the wall-clock duration of the step in milliseconds.
func (j *Journal) AppendHostReasoning(stepID, inputFile, outputFile string, durationMS int64) error {
	return j.Append(Event{
		Kind:       KindHostReasoning,
		StepID:     stepID,
		Input:      inputFile,
		Output:     outputFile,
		DurationMS: durationMS,
	})
}

// AppendExit records the exit of a dispatched Eidolon.
func (j *Journal) AppendExit(stepID string, exitCode int, errMsg string) error {
	code := exitCode
	return j.Append(Event{
		Kind:     KindExit,
		StepID:   stepID,
		ExitCode: &code,
		Error:    errMsg,
	})
}

// AppendError records a generic error event.
func (j *Journal) AppendError(stepID, errMsg string) error {
	return j.Append(Event{
		Kind:   KindError,
		StepID: stepID,
		Error:  errMsg,
	})
}

// ReadAll reads all events from a trace file at path.
// It tolerates a trailing newline after the last valid JSON line.
func ReadAll(path string) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("trace.ReadAll: %w", err)
	}
	return parseJSONL(data)
}

// parseJSONL parses a JSONL byte slice into a slice of Events.
func parseJSONL(data []byte) ([]Event, error) {
	var events []Event
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var e Event
		if err := dec.Decode(&e); err != nil {
			return events, fmt.Errorf("trace.parseJSONL: %w", err)
		}
		events = append(events, e)
	}
	return events, nil
}
