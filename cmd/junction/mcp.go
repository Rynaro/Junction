package main

// mcp.go wires the `junction mcp` subcommand family.
//
// Subcommands:
//
//	junction mcp serve               — start the hand-rolled JSON-RPC 2.0 / stdio MCP server
//	                                   (MCP 2025-03-26 stdio transport, zero new deps).
//	junction mcp install [--with-skill] — write idempotent .mcp.json entry + optional SKILL.md
//	junction mcp uninstall           — excise exactly what install wrote

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Rynaro/Junction/internal/mcp"
	"github.com/Rynaro/Junction/internal/reasoning"
)

// mcpCmd dispatches `junction mcp <sub>`.
func mcpCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("mcp: subcommand required — try `junction mcp serve`, `junction mcp install`, or `junction mcp uninstall`")
	}
	switch args[0] {
	case "serve":
		return mcpServeCmd(args[1:])
	case "install":
		return mcpInstallCmd(args[1:])
	case "uninstall":
		return mcpUninstallCmd(args[1:])
	default:
		return fmt.Errorf("mcp: unknown subcommand %q — known: serve, install, uninstall", args[0])
	}
}

// mcpServeCmd starts the MCP stdio server. It reads from os.Stdin and writes
// to os.Stdout until EOF or SIGTERM/SIGINT.
//
// Usage: junction mcp serve
//
// The server is launched by Claude Code as a stdio subprocess via a .mcp.json
// mcpServers entry (A2, verify before S7 implementation). All MCP
// communication uses stdout; diagnostic/error messages use stderr.
func mcpServeCmd(args []string) error {
	// No flags in v0.1 — flag parsing reserved for future --contracts-dir etc.
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprintf(os.Stdout, "Usage: junction mcp serve\n\n"+
				"Start the Junction MCP stdio server (MCP 2025-03-26 protocol).\n"+
				"Claude Code launches this as a stdio subprocess via .mcp.json.\n"+
				"All MCP traffic on stdin/stdout; diagnostics on stderr.\n")
			return nil
		default:
			return fmt.Errorf("mcp serve: unknown flag %q", a)
		}
	}

	// Create the server first so we can pass it to the registry (needed for
	// harness.run to inject the reasoning step into ContainerExecutor).
	srv := mcp.NewServer(Version, nil)

	reg, err := mcp.NewRegistryDefaultWithServer(srv)
	if err != nil {
		return fmt.Errorf("mcp serve: initialising tool registry: %w", err)
	}
	srv.SetTools(reg)

	// v0.2: wire the ReasoningStep provider from env config.
	reasoningCfg := reasoning.LoadConfigFromEnv()

	// Build the SamplingConfig closures that route through the MCP server.
	timeout := reasoningCfg.Sampling.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	reasoningCfg.Sampling = reasoning.SamplingConfig{
		ClientCapabilities: srv.ClientCapabilities,
		Request:            makeSamplingRequestFunc(srv),
		Timeout:            timeout,
	}

	provider, provErr := reasoning.NewProvider(reasoningCfg)
	if provErr != nil {
		return fmt.Errorf("mcp serve: initialising reasoning provider: %w", provErr)
	}
	srv.SetReasoningStep(reasoning.NewReasoningStepFunc(provider))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	fmt.Fprintf(os.Stderr, "junction mcp serve: ready (junction %s, MCP 2025-03-26, reasoning=%s)\n",
		Version, reasoningCfg.Provider)

	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		// context.Canceled = graceful shutdown via SIGTERM/SIGINT; not an error.
		if err == context.Canceled {
			return nil
		}
		return fmt.Errorf("mcp serve: %w", err)
	}
	return nil
}

// makeSamplingRequestFunc builds the SamplingConfig.Request closure for the
// MCP server: issues a sampling/createMessage request via server.SendRequest
// and unmarshals the response.
func makeSamplingRequestFunc(srv *mcp.Server) func(ctx context.Context, params *reasoning.SamplingCreateMessageParams) (*reasoning.SamplingCreateMessageResult, error) {
	return func(ctx context.Context, params *reasoning.SamplingCreateMessageParams) (*reasoning.SamplingCreateMessageResult, error) {
		raw, err := srv.SendRequest(ctx, "sampling/createMessage", params)
		if err != nil {
			return nil, err
		}
		var out reasoning.SamplingCreateMessageResult
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("reasoning: unmarshal sampling result: %w", err)
		}
		return &out, nil
	}
}
