package indexer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
)

func branchFixture(t *testing.T) (string, string, func(...string)) {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	write := func(name, symbol string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte("package example\nfunc "+symbol+"() {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "baseline")
	write("a.go", "StableFirst")
	write("z.go", "BaselineSecond")
	git("add", ".")
	git("commit", "-qm", "baseline")
	git("switch", "-qc", "variant")
	write("z.go", "VariantSecond")
	git("commit", "-qam", "variant")
	git("switch", "-q", "baseline")
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return root, strings.TrimSpace(string(head)), git
}

func TestIndexWorkspaceDoesNotMixBranches(t *testing.T) {
	root, head, git := branchFixture(t)
	store := &mockStore{}
	service := NewService(&mockEmbedder{}, store)
	ctx := WithGitState(context.Background(), root, "baseline", head)
	err := service.IndexWorkspace(ctx, root, "baseline", Options{
		Language: "go",
		Progress: func(done, _ int) {
			if done == 1 {
				git("switch", "-q", "variant")
			}
		},
	})
	for _, point := range store.upsertPoints {
		if point.Payload["name"] == "VariantSecond" {
			t.Errorf("baseline collection received variant symbol: %v", point.Payload["name"])
		}
	}
	if !errors.Is(err, ErrWorkspaceChanged) {
		t.Errorf("expected interrupted index to report the branch change, got %v", err)
	}
}

type branchSwitchEmbedder struct {
	mockEmbedder
	root string
	once sync.Once
	err  error
}

func (e *branchSwitchEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	e.once.Do(func() {
		e.err = exec.Command("git", "-C", e.root, "switch", "-q", "variant").Run()
	})
	if e.err != nil {
		return nil, e.err
	}
	return e.mockEmbedder.Embed(ctx, text)
}

func TestInterruptedReplacementInvalidatesSavedFileState(t *testing.T) {
	root, head, git := branchFixture(t)
	file := filepath.Join(root, "z.go")
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	state := NewState()
	state.UpdateFile(file, info)
	statePath := StatePath(root, "baseline")
	if err := state.Save(statePath); err != nil {
		t.Fatal(err)
	}
	// Force a replacement, then later restore metadata matching the old saved state.
	changed := info.ModTime().Add(time.Second)
	if err := os.Chtimes(file, changed, changed); err != nil {
		t.Fatal(err)
	}
	store := &mockStore{}
	service := NewService(&branchSwitchEmbedder{root: root}, store)
	ctx := WithGitState(context.Background(), root, "baseline", head)
	opts := Options{Language: "go", ExcludePatterns: []string{"a.go"}}
	if err := service.IndexWorkspace(ctx, root, "baseline", opts); !errors.Is(err, ErrWorkspaceChanged) {
		t.Fatalf("expected branch change during embedding, got %v", err)
	}
	if len(store.upsertPoints) != 0 || len(store.deletedFilters) != 1 {
		t.Fatalf("want aborted replacement after deletion, got upserts=%d deletions=%v", len(store.upsertPoints), store.deletedFilters)
	}
	state, err = LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := state.GetFileState(file); exists {
		t.Fatal("interrupted replacement left saved state marking missing vectors as indexed")
	}
	git("switch", "-q", "baseline")
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	service = NewService(&mockEmbedder{}, store)
	ctx = WithGitState(context.Background(), root, "baseline", head)
	if err := service.IndexWorkspace(ctx, root, "baseline", opts); err != nil {
		t.Fatal(err)
	}
	if len(store.upsertPoints) != 1 || store.upsertPoints[0].Payload["name"] != "BaselineSecond" {
		t.Fatalf("returning to baseline did not repair missing vectors: %v", store.upsertPoints)
	}
}

type branchRoundTripAnalyzer struct {
	parser.Analyzer
	git func(...string)
}

func (a branchRoundTripAnalyzer) Analyze(ctx context.Context, path string) (*parser.Result, error) {
	a.git("switch", "-q", "variant")
	result, err := a.Analyzer.Analyze(ctx, path)
	a.git("switch", "-q", "baseline")
	return result, err
}

func TestGitBranchRoundTripDuringParseStopsBeforeDeletion(t *testing.T) {
	root, head, git := branchFixture(t)
	original := parser.GetByName("go")
	parser.Register(branchRoundTripAnalyzer{Analyzer: original, git: git})
	defer parser.Register(original)
	store := &mockStore{}
	service := NewService(&mockEmbedder{}, store)
	state := NewState()
	file := filepath.Join(root, "z.go")
	ctx := WithGitState(context.Background(), root, "baseline", head)
	_, err := service.IndexFile(ctx, "baseline", file, state)
	if !errors.Is(err, ErrWorkspaceChanged) || len(store.deletedFilters) != 0 || len(store.upsertPoints) != 0 {
		t.Fatalf("round-trip checkout reached mutations: error=%v deleted=%v upserts=%d", err, store.deletedFilters, len(store.upsertPoints))
	}
	if _, exists := state.GetFileState(file); exists {
		t.Fatal("variant contents marked as indexed for baseline")
	}
}

func TestGitGuardUsesLinkedWorktreeHEAD(t *testing.T) {
	_, head, git := branchFixture(t)
	linked := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "--detach", linked, head)
	ctx := WithGitState(context.Background(), linked, "detached", head)
	if err := checkWorkspace(ctx); err != nil {
		t.Fatalf("unchanged linked worktree rejected: %v", err)
	}
	for _, revision := range []string{"variant", head} {
		cmd := exec.Command("git", "-C", linked, "checkout", "-q", "--detach", revision)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("linked checkout: %v: %s", err, output)
		}
	}
	if err := checkWorkspace(ctx); !errors.Is(err, ErrWorkspaceChanged) {
		t.Fatalf("missed linked worktree round trip: %v", err)
	}
}

func TestGitGuardRejectsMissingHEADAtBinding(t *testing.T) {
	root, head, _ := branchFixture(t)
	if err := os.Remove(filepath.Join(root, ".git", "HEAD")); err != nil {
		t.Fatal(err)
	}
	ctx := WithGitState(context.Background(), root, "baseline", head)
	if err := checkWorkspace(ctx); !errors.Is(err, ErrWorkspaceChanged) {
		t.Fatalf("missing HEAD accepted: %v", err)
	}
	if err := checkWorkspace(WithGitState(context.Background(), root, "", "")); err != nil {
		t.Fatalf("non-Git context must remain unguarded: %v", err)
	}
}

func TestGitIndexLockStopsIndexingBeforeDeletion(t *testing.T) {
	root, head, _ := branchFixture(t)
	if err := os.WriteFile(filepath.Join(root, ".git", "index.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &mockStore{}
	service := NewService(&mockEmbedder{}, store)
	ctx := WithGitState(context.Background(), root, "baseline", head)
	_, err := service.IndexFile(ctx, "baseline", filepath.Join(root, "z.go"), NewState())
	if !errors.Is(err, ErrWorkspaceChanged) || len(store.deletedFilters) != 0 || len(store.upsertPoints) != 0 {
		t.Fatalf("locked checkout reached mutations: error=%v deleted=%v upserts=%d", err, store.deletedFilters, len(store.upsertPoints))
	}
}
