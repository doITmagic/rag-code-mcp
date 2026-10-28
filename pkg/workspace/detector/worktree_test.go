package detector

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDetectLinkedWorktreeStopsAtItsRoot(t *testing.T) {
	root := evalTempDir(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "go.mod")
	git("commit", "-qm", "initial")
	worktree := filepath.Join(root, ".worktrees", "feature")
	git("worktree", "add", "-qb", "feature", worktree)
	for _, file := range []string{"go.mod", "missing.go"} {
		got, err := New(DefaultOptions()).DetectFromFilePath(context.Background(), filepath.Join(worktree, file))
		if err != nil {
			t.Fatal(err)
		}
		if got.Root != worktree {
			t.Fatalf("linked worktree resolved to %s, want %s", got.Root, worktree)
		}
	}
}

func TestGitfileWithoutCommonDirKeepsMonorepoRoot(t *testing.T) {
	root := evalTempDir(t)
	module := filepath.Join(root, "module")
	gitDir := filepath.Join(root, ".git", "modules", "module")
	for _, dir := range []string{module, gitDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Submodules use a gitfile too, but do not have the linked worktree's
	// commondir metadata. Their ordinary monorepo resolution stays unchanged.
	if err := os.WriteFile(filepath.Join(module, ".git"), []byte("gitdir: ../.git/modules/module\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if IsLinkedWorktree(module) {
		t.Fatal("submodule gitfile was mistaken for a linked worktree")
	}
	got, err := New(DefaultOptions()).DetectFromFilePath(context.Background(), module)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != root {
		t.Fatalf("submodule resolved to %s, want monorepo %s", got.Root, root)
	}
}
