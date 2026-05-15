package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rynaro/Junction/internal/mcp"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// readJSON reads and parses a JSON file from path.
func readJSON(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readJSON %s: %v", path, err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("readJSON %s: unmarshal: %v", path, err)
	}
	return m
}

// junctionEntry extracts the mcpServers.junction sub-map from a parsed
// .mcp.json map. Returns nil if absent.
func junctionEntry(m map[string]interface{}) map[string]interface{} {
	servers, ok := m["mcpServers"].(map[string]interface{})
	if !ok {
		return nil
	}
	entry, _ := servers["junction"].(map[string]interface{})
	return entry
}

// ─── Test 1: idempotent install ───────────────────────────────────────────────

func TestWriteMCPEntry_Idempotent(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, ".mcp.json")
	cfg := mcp.DefaultServerConfig()

	// First install.
	if err := mcp.WriteMCPEntry(mcpPath, cfg); err != nil {
		t.Fatalf("first WriteMCPEntry: %v", err)
	}
	first, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("reading after first write: %v", err)
	}

	// Second install.
	if err := mcp.WriteMCPEntry(mcpPath, cfg); err != nil {
		t.Fatalf("second WriteMCPEntry: %v", err)
	}
	second, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("reading after second write: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("idempotency violated:\nfirst:\n%s\n\nsecond:\n%s", first, second)
	}
}

// ─── Test 2: existing keys preserved on install ───────────────────────────────

func TestWriteMCPEntry_PreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, ".mcp.json")

	// Seed the file with existing data.
	initial := `{
  "mcpServers": {
    "another-tool": {
      "command": "another",
      "args": ["serve"],
      "type": "stdio"
    }
  },
  "topLevelExtra": "preserved"
}
`
	if err := os.WriteFile(mcpPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("seeding .mcp.json: %v", err)
	}

	cfg := mcp.DefaultServerConfig()
	if err := mcp.WriteMCPEntry(mcpPath, cfg); err != nil {
		t.Fatalf("WriteMCPEntry: %v", err)
	}

	m := readJSON(t, mcpPath)

	// junction entry must be present.
	entry := junctionEntry(m)
	if entry == nil {
		t.Fatal("mcpServers.junction not found after install")
	}
	if entry["command"] != "junction" {
		t.Errorf("junction.command = %q, want junction", entry["command"])
	}

	// Existing another-tool entry must still be present.
	servers := m["mcpServers"].(map[string]interface{})
	other, ok := servers["another-tool"].(map[string]interface{})
	if !ok {
		t.Fatal("another-tool entry missing after install")
	}
	if other["command"] != "another" {
		t.Errorf("another-tool.command = %q, want another", other["command"])
	}

	// Top-level extra key must still be present.
	if m["topLevelExtra"] != "preserved" {
		t.Errorf("topLevelExtra = %q, want preserved", m["topLevelExtra"])
	}
}

// ─── Test 3: uninstall removes junction key, preserves others ────────────────

func TestRemoveMCPEntry_SurgicalRemoval(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, ".mcp.json")
	cfg := mcp.DefaultServerConfig()

	// Install into a file that already has another-tool.
	initial := `{
  "mcpServers": {
    "another-tool": {
      "command": "another",
      "args": ["serve"],
      "type": "stdio"
    }
  }
}
`
	if err := os.WriteFile(mcpPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("seeding .mcp.json: %v", err)
	}
	if err := mcp.WriteMCPEntry(mcpPath, cfg); err != nil {
		t.Fatalf("WriteMCPEntry: %v", err)
	}

	// Verify junction was added.
	m := readJSON(t, mcpPath)
	if junctionEntry(m) == nil {
		t.Fatal("junction entry missing after install — prerequisite failure")
	}

	// Uninstall.
	if err := mcp.RemoveMCPEntry(mcpPath); err != nil {
		t.Fatalf("RemoveMCPEntry: %v", err)
	}

	// File must still exist and be valid JSON.
	m2 := readJSON(t, mcpPath)

	// junction must be gone.
	if junctionEntry(m2) != nil {
		t.Error("mcpServers.junction still present after uninstall")
	}

	// another-tool must still be present.
	servers := m2["mcpServers"].(map[string]interface{})
	other, ok := servers["another-tool"].(map[string]interface{})
	if !ok {
		t.Fatal("another-tool entry missing after uninstall")
	}
	if other["command"] != "another" {
		t.Errorf("another-tool.command = %q, want another", other["command"])
	}
}

// TestRemoveMCPEntry_Noop verifies uninstall is a no-op when the file does not exist.
func TestRemoveMCPEntry_Noop(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, ".mcp.json")

	if err := mcp.RemoveMCPEntry(mcpPath); err != nil {
		t.Errorf("RemoveMCPEntry on non-existent file: %v", err)
	}
}

// ─── Test 4: --with-skill write is idempotent ─────────────────────────────────

func TestWriteSkill_Idempotent(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, ".claude", "skills", "junction", "SKILL.md")
	content := mcp.SkillContent()

	// First write.
	if err := mcp.WriteSkill(skillPath, content); err != nil {
		t.Fatalf("first WriteSkill: %v", err)
	}
	first, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("reading after first write: %v", err)
	}

	// Second write.
	if err := mcp.WriteSkill(skillPath, content); err != nil {
		t.Fatalf("second WriteSkill: %v", err)
	}
	second, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("reading after second write: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("WriteSkill idempotency violated:\nfirst:\n%s\n\nsecond:\n%s", first, second)
	}

	// Must contain both markers.
	body := string(first)
	if !strings.Contains(body, "<!-- junction:mcp start -->") {
		t.Error("SKILL.md missing start marker")
	}
	if !strings.Contains(body, "<!-- junction:mcp end -->") {
		t.Error("SKILL.md missing end marker")
	}
}

// TestWriteSkill_AppendToExisting verifies that writing to a file that already
// has content (but no junction markers) appends the block.
func TestWriteSkill_AppendToExisting(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")

	preexisting := "# Existing Content\n\nSome text here.\n"
	if err := os.WriteFile(skillPath, []byte(preexisting), 0o644); err != nil {
		t.Fatalf("seeding SKILL.md: %v", err)
	}

	content := mcp.SkillContent()
	if err := mcp.WriteSkill(skillPath, content); err != nil {
		t.Fatalf("WriteSkill: %v", err)
	}

	body, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("reading SKILL.md: %v", err)
	}
	bodyStr := string(body)

	if !strings.HasPrefix(bodyStr, preexisting) {
		t.Error("pre-existing content not preserved at the start of SKILL.md")
	}
	if !strings.Contains(bodyStr, "<!-- junction:mcp start -->") {
		t.Error("junction marker block not appended")
	}
}

// ─── Test 5: uninstall (with prior --with-skill) excises the marker block ─────

func TestRemoveSkill_ExcisesMarkerBlock(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, ".claude", "skills", "junction", "SKILL.md")
	content := mcp.SkillContent()

	if err := mcp.WriteSkill(skillPath, content); err != nil {
		t.Fatalf("WriteSkill: %v", err)
	}

	if err := mcp.RemoveSkill(skillPath); err != nil {
		t.Fatalf("RemoveSkill: %v", err)
	}

	// File must be deleted (it contained only the junction block).
	if _, err := os.Stat(skillPath); !os.IsNotExist(err) {
		t.Error("SKILL.md should be deleted when it only contained the junction block")
	}
}

// TestRemoveSkill_PreservesOtherContent verifies that only the junction block
// is removed when SKILL.md contains other content.
func TestRemoveSkill_PreservesOtherContent(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")
	content := mcp.SkillContent()

	// Install with append (file has pre-existing content).
	preexisting := "# Other Skills\n\nOther content here.\n"
	if err := os.WriteFile(skillPath, []byte(preexisting), 0o644); err != nil {
		t.Fatalf("seeding SKILL.md: %v", err)
	}
	if err := mcp.WriteSkill(skillPath, content); err != nil {
		t.Fatalf("WriteSkill: %v", err)
	}

	if err := mcp.RemoveSkill(skillPath); err != nil {
		t.Fatalf("RemoveSkill: %v", err)
	}

	// File must still exist.
	body, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("SKILL.md should not be deleted when it has other content: %v", err)
	}
	bodyStr := string(body)

	// Pre-existing content must be intact.
	if !strings.Contains(bodyStr, "# Other Skills") {
		t.Error("pre-existing content removed by RemoveSkill — should only excise the junction block")
	}

	// Junction block must be gone.
	if strings.Contains(bodyStr, "<!-- junction:mcp start -->") {
		t.Error("junction start marker still present after RemoveSkill")
	}
	if strings.Contains(bodyStr, "<!-- junction:mcp end -->") {
		t.Error("junction end marker still present after RemoveSkill")
	}
}

// TestRemoveSkill_Noop verifies that RemoveSkill is a no-op when the file does
// not exist.
func TestRemoveSkill_Noop(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")

	if err := mcp.RemoveSkill(skillPath); err != nil {
		t.Errorf("RemoveSkill on non-existent file: %v", err)
	}
}
