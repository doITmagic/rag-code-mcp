package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
)

// Branch metadata only annotates a workspace: when git cannot run (not on
// the daemon's PATH, or a repo owned by another user that trips the
// safe.directory check) the workspace must still resolve, without branches.
func TestWorkspaceResolvesWhenGitCannotRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v: %s", err, output)
	}
	for name, env := range map[string][2]string{
		"git not on PATH":   {"PATH", t.TempDir()},
		"dubious ownership": {"GIT_TEST_ASSUME_DIFFERENT_OWNER", "1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(env[0], env[1])
			eng := NewEngine(nil, nil, filepath.Join(t.TempDir(), "registry.json"), &config.Config{})
			defer eng.Close()
			if _, err := eng.DetectContextAsRoot(context.Background(), root, false); err != nil {
				t.Fatalf("workspace root rejected: %v", err)
			}
			if _, err := eng.DetectContext(context.Background(), filepath.Join(root, "main.go")); err != nil {
				t.Fatalf("file path rejected: %v", err)
			}
		})
	}
}
