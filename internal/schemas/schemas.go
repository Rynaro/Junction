// Package schemas embeds the ECL v1.0 JSON schemas used for envelope
// validation. Schemas are vendored from eidolons-ecl at the version
// recorded in VERSION.
//
// The embedded set intentionally includes only the schemas that Junction
// needs to validate envelopes at runtime (envelope.v1.json and its
// sub-schema references). Per-Eidolon profile schemas are not embedded
// here — they belong to the individual Eidolon repos.
package schemas

import _ "embed"

// EnvelopeV1 is the raw JSON of ecl-envelope.v1.json (Draft 2020-12).
//
//go:embed envelope.v1.json
var EnvelopeV1 []byte

// PerformativeV1 is the raw JSON of performative.v1.json, referenced by
// the envelope schema via a $ref.
//
//go:embed performative.v1.json
var PerformativeV1 []byte

// ContextDeltaV1 is the raw JSON of context-delta.v1.json, referenced by
// the envelope schema via $ref in the context_delta property.
//
//go:embed context-delta.v1.json
var ContextDeltaV1 []byte
