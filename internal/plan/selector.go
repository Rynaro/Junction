package plan

import (
	"github.com/Rynaro/Junction/internal/dispatch"
)

// ExecutorMode is the typed enum for plan-level executor selection.
// Values mirror the "executor" field in plan.json §7.5.
type ExecutorMode string

const (
	// ExecutorModeContainer selects the ContainerExecutor (default).
	ExecutorModeContainer ExecutorMode = "container"

	// ExecutorModeShell selects the ShellExecutor (subprocess, --no-container
	// fallback).
	ExecutorModeShell ExecutorMode = "shell"
)

// ExecutorOptions carries the construction parameters that differ between
// executor types.
type ExecutorOptions struct {
	// ProjectDir is the root of the consumer project (cwd at run time).
	ProjectDir string

	// CacheDir is ~/.eidolons/cache or equivalent.
	CacheDir string

	// EidolonVersion is passed to the executor when building image/cache paths.
	// ContainerExecutor requires a non-empty value (the plan step's to.version);
	// it will error rather than fall back to ":latest". Cache lookup is skipped
	// by ShellExecutor when empty.
	EidolonVersion string
}

// SelectExecutor returns the appropriate dispatch.Executor given the plan-
// level executor mode and the --no-container override flag.
//
// Decision matrix:
//
//	noContainer=true  → ShellExecutor (regardless of mode)
//	mode="shell"      → ShellExecutor
//	mode="container"  → ContainerExecutor (default)
//	mode=""           → ContainerExecutor (treated as default)
//
// This is the seam that replaces the hardcoded &dispatch.ShellExecutor{...}
// construction in cmd/junction/main.go:runCmd (FINDING-003/004 from the ATLAS
// scout report, 2026-05-15).
func SelectExecutor(mode ExecutorMode, noContainer bool, opts ExecutorOptions) dispatch.Executor {
	if noContainer || mode == ExecutorModeShell {
		return &dispatch.ShellExecutor{
			ProjectDir: opts.ProjectDir,
			CacheDir:   opts.CacheDir,
			EidolonVersion: opts.EidolonVersion,
		}
	}
	return &dispatch.ContainerExecutor{
		EidolonVersion:  opts.EidolonVersion,
		SkipDaemonProbe: false,
	}
}

// ModeFromString converts a raw string (from plan.json) to an ExecutorMode.
// An empty string is treated as the default ("container").
func ModeFromString(s string) ExecutorMode {
	switch ExecutorMode(s) {
	case ExecutorModeShell:
		return ExecutorModeShell
	default:
		return ExecutorModeContainer
	}
}
