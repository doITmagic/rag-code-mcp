package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSkillID(t *testing.T) {
	validIDs := []string{"my-skill", "go-best-practices", "oxygen-builder", "a", "abc123"}
	for _, id := range validIDs {
		if err := validateSkillID(id); err != nil {
			t.Errorf("expected valid ID '%s' to pass, got: %v", id, err)
		}
	}

	invalidIDs := []string{"../foo", "foo/bar", "/etc/passwd", `foo\bar`, "Foo", "foo bar", ""}
	for _, id := range invalidIDs {
		if err := validateSkillID(id); err == nil {
			t.Errorf("expected invalid ID '%s' to fail validation", id)
		}
	}
}

func TestSkillCandidateDirs(t *testing.T) {
	root := "/tmp/test-workspace"
	dirs := skillCandidateDirs(root)
	if len(dirs) != 5 {
		t.Errorf("expected 5 candidate dirs, got %d", len(dirs))
	}
	expected := []string{
		filepath.Join(root, ".agent", "skills"),    // rag-code-mcp / Antigravity / OpenCode
		filepath.Join(root, ".agents", "skills"),   // GitHub Copilot / VS Code
		filepath.Join(root, ".claude", "skills"),   // Claude (Anthropic)
		filepath.Join(root, ".cursor", "skills"),   // Cursor
		filepath.Join(root, ".windsurf", "skills"), // Windsurf (Codeium)
	}
	for i, dir := range dirs {
		if dir != expected[i] {
			t.Errorf("dir[%d]: expected %s, got %s", i, expected[i], dir)
		}
	}
}

func TestFindSkillPath(t *testing.T) {
	tempDir := t.TempDir()
	skillID := "test-skill"

	// Not installed yet
	if path := FindSkillPath(skillID, tempDir); path != "" {
		t.Errorf("expected empty path for non-installed skill, got %s", path)
	}

	// Create skill in .claude/skills/ (simulating Cursor/Claude usage)
	claudeSkillDir := filepath.Join(tempDir, ".claude", "skills", skillID)
	if err := os.MkdirAll(claudeSkillDir, 0755); err != nil {
		t.Fatal(err)
	}

	found := FindSkillPath(skillID, tempDir)
	if found != claudeSkillDir {
		t.Errorf("expected to find skill at %s, got %s", claudeSkillDir, found)
	}
}

func TestIsSkillInstalled(t *testing.T) {
	tempDir := t.TempDir()
	skillID := "my-skill"

	if IsSkillInstalled(skillID, tempDir) {
		t.Error("skill should not be installed in empty dir")
	}

	// Install manually in .agent/skills/
	agentSkillDir := filepath.Join(tempDir, ".agent", "skills", skillID)
	if err := os.MkdirAll(agentSkillDir, 0755); err != nil {
		t.Fatal(err)
	}

	if !IsSkillInstalled(skillID, tempDir) {
		t.Error("skill should be detected as installed")
	}
}

func TestUninstallSkillFromMultipleLocations(t *testing.T) {
	tempDir := t.TempDir()
	skillID := "test-skill"

	// Simulate skill installed in both .agent/ and .cursor/
	for _, base := range []string{".agent", ".cursor"} {
		dir := filepath.Join(tempDir, base, "skills", skillID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	if err := UninstallSkill(skillID, tempDir); err != nil {
		t.Fatalf("UninstallSkill failed: %v", err)
	}

	// Verify both are gone
	if IsSkillInstalled(skillID, tempDir) {
		t.Error("skill should be completely removed from all locations")
	}
}

func TestInstallSkillPathTraversal(t *testing.T) {
	tempDir := t.TempDir()

	evilIDs := []string{
		"../foo",
		"foo/bar",
		"/etc/passwd",
		`foo\bar`,
	}

	for _, id := range evilIDs {
		if err := InstallSkill(id, tempDir, "agent"); err == nil {
			t.Errorf("InstallSkill should have rejected ID '%s'", id)
		}
	}
}
