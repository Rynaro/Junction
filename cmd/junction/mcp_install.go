package main

// mcp_install.go implements the `junction mcp install [--with-skill]` and
// `junction mcp uninstall` subcommands (F9-S7).
//
// Install writes an idempotent keyed mcpServers.junction entry into .mcp.json
// in the current working directory. With --with-skill it additionally writes
// a marker-bounded .claude/skills/junction/SKILL.md.
//
// Uninstall excises exactly what install wrote — nothing else.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Rynaro/Junction/internal/mcp"
)

// mcpInstallCmd handles `junction mcp install [--with-skill]`.
func mcpInstallCmd(args []string) error {
	withSkill := false
	for _, a := range args {
		switch a {
		case "--with-skill", "-with-skill":
			withSkill = true
		case "--help", "-h":
			fmt.Fprintf(os.Stdout, "Usage: junction mcp install [--with-skill]\n\n"+
				"Write an idempotent mcpServers.junction entry into .mcp.json\n"+
				"in the current directory so that Claude Code can launch\n"+
				"junction mcp serve as a stdio subprocess.\n\n"+
				"Flags:\n"+
				"  --with-skill    also write .claude/skills/junction/SKILL.md\n"+
				"                  (marker-bounded; safe to run twice)\n\n"+
				"To undo: junction mcp uninstall\n")
			return nil
		default:
			return fmt.Errorf("mcp install: unknown flag %q", a)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("mcp install: cannot determine working directory: %w", err)
	}

	mcpJSONPath := filepath.Join(cwd, ".mcp.json")
	cfg := mcp.DefaultServerConfig()

	if err := mcp.WriteMCPEntry(mcpJSONPath, cfg); err != nil {
		return fmt.Errorf("mcp install: writing .mcp.json: %w", err)
	}
	fmt.Fprintf(os.Stderr, "junction mcp install: wrote mcpServers.junction to %s\n", mcpJSONPath)

	if withSkill {
		skillPath := filepath.Join(cwd, ".claude", "skills", "junction", "SKILL.md")
		if err := mcp.WriteSkill(skillPath, mcp.SkillContent()); err != nil {
			return fmt.Errorf("mcp install: writing SKILL.md: %w", err)
		}
		fmt.Fprintf(os.Stderr, "junction mcp install: wrote SKILL.md to %s\n", skillPath)
	}

	fmt.Fprintf(os.Stderr, "junction mcp install: done — run `junction mcp serve` to verify\n")
	return nil
}

// mcpUninstallCmd handles `junction mcp uninstall`.
func mcpUninstallCmd(args []string) error {
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprintf(os.Stdout, "Usage: junction mcp uninstall\n\n"+
				"Remove the mcpServers.junction entry from .mcp.json and\n"+
				"excise the marker-bounded block from\n"+
				".claude/skills/junction/SKILL.md (if present).\n\n"+
				"Other .mcp.json content and other SKILL.md content are\n"+
				"never touched.\n")
			return nil
		default:
			return fmt.Errorf("mcp uninstall: unknown flag %q", a)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("mcp uninstall: cannot determine working directory: %w", err)
	}

	mcpJSONPath := filepath.Join(cwd, ".mcp.json")
	if err := mcp.RemoveMCPEntry(mcpJSONPath); err != nil {
		return fmt.Errorf("mcp uninstall: removing .mcp.json entry: %w", err)
	}
	fmt.Fprintf(os.Stderr, "junction mcp uninstall: removed mcpServers.junction from %s\n", mcpJSONPath)

	skillPath := filepath.Join(cwd, ".claude", "skills", "junction", "SKILL.md")
	if err := mcp.RemoveSkill(skillPath); err != nil {
		return fmt.Errorf("mcp uninstall: removing SKILL.md: %w", err)
	}
	fmt.Fprintf(os.Stderr, "junction mcp uninstall: removed junction SKILL.md block (if any) from %s\n", skillPath)

	fmt.Fprintf(os.Stderr, "junction mcp uninstall: done\n")
	return nil
}
