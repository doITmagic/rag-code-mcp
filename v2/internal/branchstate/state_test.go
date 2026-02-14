package branchstate

import (
	context "context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/internal/contract"
)

type fakeGit struct {
	branch string
	sha    string
	err    error
}

func (f *fakeGit) run(ctx context.Context, root string, args ...string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	switch {
	case len(args) >= 3 && args[0] == "rev-parse" && args[2] == "HEAD" && args[1] == "--abbrev-ref":
		return f.branch, nil
	case len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD":
		return f.sha, nil
	default:
		return "", nil
	}
}

func TestCompareAndUpdate_ReindexOnBranchChange(t *testing.T) {
	manager := &Manager{clock: func() time.Time { return time.Unix(0, 0) }, gitRunner: (&fakeGit{branch: "main", sha: "abc"}).run}
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, ".ragcode", "branch_state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initial := &State{SchemaVersion: currentSchemaVersion, LastBranch: "dev", LastHeadSHA: "def"}
	if err := manager.saveState(statePath, initial); err != nil {
		t.Fatalf("save: %v", err)
	}

	state, reindex, _, err := manager.CompareAndUpdate(context.Background(), tmp)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !reindex {
		t.Fatalf("expected reindex on branch change")
	}
	if state.LastBranch != "main" {
		t.Fatalf("expected branch main, got %s", state.LastBranch)
	}
}

func TestCompareAndUpdate_NoReindexWhenSameState(t *testing.T) {
	fake := &fakeGit{branch: "main", sha: "abc"}
	manager := &Manager{clock: func() time.Time { return time.Unix(0, 0) }, gitRunner: fake.run}
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, ".ragcode", "branch_state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initial := &State{SchemaVersion: currentSchemaVersion, LastBranch: "main", LastHeadSHA: "abc"}
	if err := manager.saveState(statePath, initial); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, reindex, _, err := manager.CompareAndUpdate(context.Background(), tmp)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if reindex {
		t.Fatalf("did not expect reindex when state unchanged")
	}
}

func TestCompareAndUpdate_DetachedHead(t *testing.T) {
	fake := &fakeGit{branch: "HEAD", sha: "abc"}
	manager := &Manager{clock: func() time.Time { return time.Unix(0, 0) }, gitRunner: fake.run}
	tmp := t.TempDir()

	// initialize git repo to avoid git errors
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	fake.sha = "deadbeef"
	fake.branch = "HEAD"
	state, _, reason, err := manager.CompareAndUpdate(context.Background(), tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.LastBranch != "detached" {
		t.Fatalf("expected detached, got %s", state.LastBranch)
	}
	if reason != contract.ReasonHeadChanged && reason != contract.ReasonBranchChanged && reason != contract.ReasonFirstSeen {
		t.Fatalf("expected reason for reindex, got %s", reason)
	}
}
