package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListAvailableSkills(t *testing.T) {
	available, err := ListAvailableSkills()
	if err != nil {
		t.Fatalf("ListAvailableSkills failed: %v", err)
	}

	if len(available) == 0 {
		t.Error("Expected at least one available skill, got none")
	}

	// Verify we found our priority skill
	foundPriority := false
	for _, skill := range available {
		if skill.ID == "ragcode-priority" {
			foundPriority = true
			if skill.Name == "" || skill.Description == "" {
				t.Errorf("Skill %s has empty name or description", skill.ID)
			}
		}
	}

	if !foundPriority {
		t.Error("Skill 'ragcode-priority' not found in available skills")
	}
}

func TestInstallAndUninstallSkill(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "skill-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	skillID := "ragcode-priority"

	// Test Installation
	err = InstallSkill(skillID, tempDir)
	if err != nil {
		t.Fatalf("InstallSkill failed: %v", err)
	}

	// Verify file existence
	skillFile := filepath.Join(tempDir, ".agent", "skills", skillID, "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		t.Errorf("Skill file %s was not created", skillFile)
	}

	// Test Uninstallation
	err = UninstallSkill(skillID, tempDir)
	if err != nil {
		t.Fatalf("UninstallSkill failed: %v", err)
	}

	// Verify file removal
	if _, err := os.Stat(filepath.Dir(skillFile)); !os.IsNotExist(err) {
		t.Errorf("Skill directory %s still exists after uninstallation", filepath.Dir(skillFile))
	}
}
