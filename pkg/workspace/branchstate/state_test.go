package branchstate

import (
	context "context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
)

type fakeGit struct {
	branch string
	sha    string
	err    error
	calls  int
}

func (f *fakeGit) run(ctx context.Context, root string, args ...string) (string, error) {
	f.calls++
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

func TestCompareAndUpdate_UsesCacheTTL(t *testing.T) {
	now := time.Unix(0, 0)
	fake := &fakeGit{branch: "main", sha: "abc"}
	manager := &Manager{
		clock:     func() time.Time { return now },
		gitRunner: fake.run,
		cache:     make(map[string]cachedGitState),
		cacheTTL:  2 * time.Second,
	}

	tmp := t.TempDir()
	_, _, _, err := manager.CompareAndUpdate(context.Background(), tmp)
	if err != nil {
		t.Fatalf("unexpected err on first compare: %v", err)
	}
	firstCalls := fake.calls
	if firstCalls == 0 {
		t.Fatalf("expected git calls on first read")
	}

	_, _, _, err = manager.CompareAndUpdate(context.Background(), tmp)
	if err != nil {
		t.Fatalf("unexpected err on second compare: %v", err)
	}
	if fake.calls != firstCalls {
		t.Fatalf("expected cached git state within TTL; calls before=%d after=%d", firstCalls, fake.calls)
	}

	now = now.Add(3 * time.Second)
	_, _, _, err = manager.CompareAndUpdate(context.Background(), tmp)
	if err != nil {
		t.Fatalf("unexpected err after ttl expiry: %v", err)
	}
	if fake.calls <= firstCalls {
		t.Fatalf("expected git calls to refresh after TTL expiry")
	}
}

// A repo straight after `git init` has no HEAD commit; git exits 128 with
// "unknown revision". That must read as "no git metadata", not as an invalid
// workspace, or every tool fails on a brand-new project.
func TestGitCommand_UnbornHeadIsMetadataUnavailable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if _, err := gitCommand(context.Background(), root, "rev-parse", "--abbrev-ref", "HEAD"); err != ErrGitMetadataUnavailable {
		t.Fatalf("got %v, want ErrGitMetadataUnavailable", err)
	}
}

// Without git metadata there is no branch to compare, so Annotate must not
// report a mismatch risk on every request.
func TestAnnotate_NoGitIsLowRisk(t *testing.T) {
	manager := &Manager{clock: time.Now, gitRunner: (&fakeGit{err: ErrGitMetadataUnavailable}).run}
	resp := &contract.ResolveWorkspaceResponse{}
	if err := manager.Annotate(context.Background(), t.TempDir(), resp); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.MismatchRisk != "low" {
		t.Fatalf("risk = %q, want low", resp.MismatchRisk)
	}
}

func TestConcurrentBranchStateResolution(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(WithCacheTTL(0))
	manager.gitRunner = func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) == 3 {
			return "main", nil
		}
		return "abc", nil
	}
	if err := manager.saveState(filepath.Join(root, ".ragcode", "branch_state.json"), &State{LastBranch: "previous", LastHeadSHA: "previous"}); err != nil {
		t.Fatal(err)
	}
	const callers = 64
	start := make(chan struct{})
	responses := make(chan *contract.ResolveWorkspaceResponse, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp := &contract.ResolveWorkspaceResponse{}
			if err := manager.Annotate(context.Background(), root, resp); err != nil {
				t.Errorf("concurrent resolution failed: %s", err.Message)
				return
			}
			responses <- resp
		}()
	}
	close(start)
	wg.Wait()
	close(responses)
	changed := 0
	for resp := range responses {
		if resp.ReindexRequired {
			changed++
			if resp.MismatchRisk != "high" {
				t.Errorf("changed branch risk = %q, want high", resp.MismatchRisk)
			}
		} else if resp.MismatchRisk != "low" {
			t.Errorf("unchanged branch risk = %q, want low", resp.MismatchRisk)
		}
	}
	if changed != 1 {
		t.Fatalf("branch transition reported %d times, want once", changed)
	}
}

func TestConcurrentManagersPublishCompleteBranchState(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	for _, branch := range []string{"main", "feature/longer-branch"} {
		wg.Add(1)
		go func(branch string) {
			defer wg.Done()
			manager := NewManager(WithCacheTTL(0))
			manager.gitRunner = func(_ context.Context, _ string, _ ...string) (string, error) {
				return branch, nil
			}
			for i := 0; i < 100; i++ {
				if err := manager.Annotate(context.Background(), root, &contract.ResolveWorkspaceResponse{}); err != nil {
					t.Errorf("concurrent manager observed incomplete state: %s", err.Message)
					return
				}
			}
		}(branch)
	}
	wg.Wait()
}
