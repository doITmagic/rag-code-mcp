package resolver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/workspace/branchstate"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/detector"
)

func TestRegisteredParentDoesNotOverrideLinkedWorktree(t *testing.T) {
	root := t.TempDir()
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
	r := New(Dependencies{
		Detector: detector.New(detector.DefaultOptions()), Registry: &fakeRegistry{parentRoot: root},
		BranchAnnotator: branchstate.NewManager(),
	})
	for _, request := range []contract.ResolveWorkspaceRequest{
		{FilePath: filepath.Join(worktree, "go.mod")},
		{WorkspaceRoot: worktree},
		{Roots: []string{worktree}},
	} {
		got, err := r.Resolve(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if got.ResolvedRoot != worktree || got.Branch != "feature" {
			t.Fatalf("linked worktree collapsed into parent: %+v", got)
		}
	}
}
