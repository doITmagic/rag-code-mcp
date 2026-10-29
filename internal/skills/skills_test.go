package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
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
		if err := os.WriteFile(filepath.Join(dir, installedMarker), nil, 0o644); err != nil {
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

// Uninstall must only remove what RagCode installed: a skill the user wrote
// by hand, committed or not, under the same id is left alone.
func TestUninstallSkillKeepsUserSkills(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	write := func(rel string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# my skill\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude/skills/code-review/SKILL.md")
	run("add", ".")
	run("commit", "-q", "-m", "skill")
	write(".cursor/skills/code-review/SKILL.md")

	// A skill RagCode installed sits beside them.
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	body := []byte("# installed\n")
	for _, h := range []*tar.Header{
		{Name: "repo-main/skills/code-review/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "repo-main/skills/code-review/SKILL.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write(body)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	installed := filepath.Join(root, ".agent", "skills", "code-review")
	if err := extractSkillFromTarGz(&archive, "skills/code-review", installed); err != nil {
		t.Fatal(err)
	}

	if err := UninstallSkill("code-review", root); err != nil {
		t.Fatalf("UninstallSkill failed: %v", err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Errorf("installed skill not removed: %v", err)
	}
	for _, rel := range []string{".claude/skills/code-review/SKILL.md", ".cursor/skills/code-review/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("user skill %s removed: %v", rel, err)
		}
	}
	if err := UninstallSkill("code-review", root); err == nil {
		t.Error("uninstalling only user skills should report an error")
	}
}

func TestInstallSkillPathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	emptyCfg := config.SkillsConfig{}

	evilIDs := []string{
		"../foo",
		"foo/bar",
		"/etc/passwd",
		`foo\bar`,
	}

	for _, id := range evilIDs {
		if err := InstallSkill(id, tempDir, "agent", emptyCfg); err == nil {
			t.Errorf("InstallSkill should have rejected ID '%s'", id)
		}
	}
}
