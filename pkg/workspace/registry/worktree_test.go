package registry

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParentRegistrationPreservesNestedLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git("init", "-q")
	git("commit", "-qm", "initial", "--allow-empty")
	linked := filepath.Join(root, "worktrees", "feature")
	git("worktree", "add", "-b", "feature", linked)
	state := filepath.Join(linked, ".ragcode", "branch_state.json")
	if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := New(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{linked, root, root} {
		if _, err := r.Upsert(path, "", "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := r.LookupByRoot(linked); !ok || len(r.List()) != 2 {
		t.Fatal("parent registration absorbed the independent linked worktree")
	}
	if data, err := os.ReadFile(state); err != nil || string(data) != "{}" {
		t.Fatalf("parent registration changed linked worktree state: %s, %v", data, err)
	}
}

func TestParentDoesNotClaimPathsInsideNestedLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git("init", "-q")
	git("commit", "-qm", "initial", "--allow-empty")
	linked := filepath.Join(root, "wt", "feat")
	git("worktree", "add", "-b", "feature", linked)
	sub := filepath.Join(linked, "pkg")
	state := filepath.Join(sub, ".ragcode", "branch_state.json")
	if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := New(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{sub, root} {
		if _, err := r.Upsert(path, "", "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := r.LookupByRoot(sub); !ok {
		t.Fatal("parent registration absorbed an entry inside a linked worktree")
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("parent registration deleted state inside a linked worktree: %v", err)
	}
	if got, found := r.FindParentWorkspace(filepath.Join(linked, "cmd")); found {
		t.Fatalf("path inside linked worktree resolved to parent %q", got)
	}
	if got, found := r.FindParentWorkspace(filepath.Join(sub, "x")); !found || got != sub {
		t.Fatalf("registered root inside the worktree must still win: %q, %v", got, found)
	}
	if got, found := r.FindParentWorkspace(filepath.Join(root, "internal")); !found || got != root {
		t.Fatalf("ordinary subdirectory must still resolve to parent: %q, %v", got, found)
	}
}
