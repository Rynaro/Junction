// Package trace owns the append-only JSONL trace journal that records
// every envelope, verify result, dispatch, exit, refusal, escalation,
// human inject, and resume marker for a Junction thread.
// Phase 0 placeholder; F1 (APIVR-Δ) introduces the writer per spec
// story S3 (crash-safe fsync, append-only).
package trace
