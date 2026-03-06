//go:build integration

package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// Integration tests make real HTTP calls to GitHub.
// Run with: go test -tags integration -v ./internal/skills/
// They are excluded from regular `go test ./...` runs.

func TestIntegration_FetchRemoteRegistry(t *testing.T) {
	registry, err := FetchRemoteRegistry()
	if err != nil {
		t.Fatalf("FetchRemoteRegistry failed: %v", err)
	}

	if registry.Version == "" {
		t.Error("registry.Version is empty")
	}

	if len(registry.Skills) == 0 {
		t.Fatal("registry has no skills")
	}

	t.Logf("Registry version: %s", registry.Version)
	t.Logf("Skills available: %d", len(registry.Skills))

	for _, s := range registry.Skills {
		t.Logf("  - %s: %s (path: %s)", s.ID, s.Description, s.Path)

		if s.ID == "" {
			t.Error("skill missing ID")
		}
		if s.Path == "" {
			t.Errorf("skill '%s' missing path", s.ID)
		}
		if s.Description == "" {
			t.Errorf("skill '%s' missing description", s.ID)
		}
	}
}

func TestIntegration_ListAvailableSkills(t *testing.T) {
	skills, err := ListAvailableSkills()
	if err != nil {
		t.Fatalf("ListAvailableSkills failed: %v", err)
	}

	if len(skills) == 0 {
		t.Fatal("no skills returned")
	}

	// Verify expected skills are present
	expected := []string{"oxygen-builder", "go-best-practices", "debugging-guide"}
	found := make(map[string]bool)
	for _, s := range skills {
		found[s.ID] = true
	}

	for _, id := range expected {
		if !found[id] {
			t.Errorf("expected skill '%s' not found in registry", id)
		}
	}
}

func TestIntegration_InstallSkill_OxygenBuilder(t *testing.T) {
	tempDir := t.TempDir()

	// Install the oxygen-builder skill into a temporary workspace
	err := InstallSkill("oxygen-builder", tempDir, "agent")
	if err != nil {
		t.Fatalf("InstallSkill failed: %v", err)
	}

	// Verify SKILL.md exists
	skillMD := filepath.Join(tempDir, ".agent", "skills", "oxygen-builder", "SKILL.md")
	info, err := os.Stat(skillMD)
	if err != nil {
		t.Fatalf("SKILL.md not found after install: %v", err)
	}
	if info.Size() == 0 {
		t.Error("SKILL.md is empty")
	}
	t.Logf("SKILL.md size: %d bytes", info.Size())

	// Verify examples/ directory
	examplesDir := filepath.Join(tempDir, ".agent", "skills", "oxygen-builder", "examples")
	if _, err := os.Stat(examplesDir); os.IsNotExist(err) {
		t.Error("examples/ directory not found after install")
	}

	// Verify workflows/ directory
	workflowsDir := filepath.Join(tempDir, ".agent", "skills", "oxygen-builder", "workflows")
	if _, err := os.Stat(workflowsDir); os.IsNotExist(err) {
		t.Error("workflows/ directory not found after install")
	}

	// Verify IsSkillInstalled returns true
	if !IsSkillInstalled("oxygen-builder", tempDir) {
		t.Error("IsSkillInstalled returned false after successful install")
	}
}

func TestIntegration_InstallSkill_AllTargets(t *testing.T) {
	targets := []struct {
		name string
		dir  string
	}{
		{"agent", ".agent"},
		{"claude", ".claude"},
		{"cursor", ".cursor"},
		{"windsurf", ".windsurf"},
		{"agents", ".agents"},
	}

	skillID := "debugging-guide"

	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			tempDir := t.TempDir()

			err := InstallSkill(skillID, tempDir, target.name)
			if err != nil {
				t.Fatalf("InstallSkill to target '%s' failed: %v", target.name, err)
			}

			expectedPath := filepath.Join(tempDir, target.dir, "skills", skillID, "SKILL.md")
			if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
				t.Errorf("SKILL.md not found at %s", expectedPath)
			}

			// Verify via IsSkillInstalled (checks all locations)
			if !IsSkillInstalled(skillID, tempDir) {
				t.Error("IsSkillInstalled returned false")
			}

			t.Logf("✅ Installed '%s' into %s/", skillID, target.dir)
		})
	}
}

func TestIntegration_InstallAndUninstall(t *testing.T) {
	tempDir := t.TempDir()
	skillID := "go-best-practices"

	// Install
	if err := InstallSkill(skillID, tempDir, "agent"); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if !IsSkillInstalled(skillID, tempDir) {
		t.Fatal("skill not detected as installed")
	}

	// Double install should fail
	if err := InstallSkill(skillID, tempDir, "agent"); err == nil {
		t.Error("expected error on double install, got nil")
	}

	// Uninstall
	if err := UninstallSkill(skillID, tempDir); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}
	if IsSkillInstalled(skillID, tempDir) {
		t.Error("skill still detected after uninstall")
	}

	// Uninstall again should fail (not found)
	if err := UninstallSkill(skillID, tempDir); err == nil {
		t.Error("expected error uninstalling non-existent skill, got nil")
	}
}

func TestIntegration_InstallSkill_UnknownID(t *testing.T) {
	tempDir := t.TempDir()

	err := InstallSkill("does-not-exist-xyz", tempDir, "agent")
	if err == nil {
		t.Fatal("expected error for unknown skill ID, got nil")
	}
	t.Logf("Correctly rejected unknown skill: %v", err)
}
