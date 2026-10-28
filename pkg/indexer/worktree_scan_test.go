package indexer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLinkedWorktreeIsAnIndexingBoundary(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-qb", "main")
	write(filepath.Join(root, "main.go"), "package main\nfunc MainOnly() {}\n")
	git("add", "main.go")
	git("commit", "-qm", "initial")
	linked := filepath.Join(root, "worktrees", "feature")
	git("worktree", "add", "-qb", "feature", linked)
	featureFile := filepath.Join(linked, "feature.go")
	write(featureFile, "package main\nfunc FeatureOnly() {}\n")
	// A submodule gitfile has no commondir; it keeps belonging to the parent.
	module := filepath.Join(root, "module")
	write(filepath.Join(module, ".git"), "gitdir: ../.git/modules/module\n")
	write(filepath.Join(module, "module.go"), "package module\nfunc ModuleOnly() {}\n")
	svc := &Service{}
	for _, workspace := range []string{root, linked} {
		scan, err := svc.ScanWorkspace(context.Background(), workspace, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := scan.Counts["go"]; got != 2 {
			t.Errorf("workspace %s has %d Go files, want 2", workspace, got)
		}
	}
	for _, path := range []string{linked, featureFile, filepath.Join(linked, "deleted.go")} {
		if !ExcludedPath(root, path, path == linked, nil) {
			t.Errorf("parent watcher must exclude linked-worktree path %s", path)
		}
	}
	if ExcludedPath(linked, featureFile, false, nil) || ExcludedPath(root, filepath.Join(module, "module.go"), false, nil) {
		t.Fatal("own worktree files or ordinary submodules were excluded")
	}
}
