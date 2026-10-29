package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
)

type hookLLM struct {
	mockLLM
	onEmbed func(text string)
}

func (p *hookLLM) Embed(ctx context.Context, text string) ([]float64, error) {
	if p.onEmbed != nil {
		p.onEmbed(text)
	}
	return p.mockLLM.Embed(ctx, text)
}

// When IndexFiles hands a batch over to a full run and that run is aborted by
// a checkout, the files the full run already indexed must stay in the saved
// state; IndexFiles must not overwrite it with its own older copy.
func TestIndexFilesFallbackKeepsStateSavedByFullRun(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a := write("a.go", "package main\nfunc A() {}\n")
	git("init", "-q", "-b", "main")
	git("add", "a.go")
	git("commit", "-qm", "initial")

	provider := &hookLLM{}
	store := &branchIndexStore{
		indexOrderStore: indexOrderStore{testStore: testStore{existing: map[string]bool{}}},
		names:           map[string][]string{},
	}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), &config.Config{})
	t.Cleanup(eng.Close)
	ctx := context.Background()
	if err := eng.IndexWorkspace(ctx, root, false); err != nil {
		t.Fatal(err)
	}

	write("a.go", "package main\nfunc A2() {}\n")
	n1 := write("sub/n1.go", "package sub\nfunc NOne() {}\n")
	write("sub/n2.go", "package sub\nfunc NTwo() {}\n")
	// Replacing HEAD mid-run is what a checkout does; the run must abort.
	head := filepath.Join(root, ".git", "HEAD")
	provider.onEmbed = func(text string) {
		if strings.Contains(text, "NTwo") {
			later := time.Now().Add(time.Minute)
			if err := os.Chtimes(head, later, later); err != nil {
				t.Error(err)
			}
		}
	}
	err := eng.IndexFiles(ctx, root, []string{a, filepath.Join(root, "sub")})
	if !errors.Is(err, indexer.ErrWorkspaceChanged) {
		t.Fatalf("expected the checkout to abort the full run, got %v", err)
	}

	wctx, err := eng.DetectContextAsRoot(ctx, root, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := indexer.LoadState(indexer.StatePath(wctx.Root, wctx.CollectionName("go")))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.GetFileState(n1); !ok {
		t.Fatalf("state lost %s, which the full run indexed before the abort: %v", n1, state.Files)
	}
}

// A truncated state file (kill or disk full during a save) must not make
// every watcher batch fail; the batch falls back to a full run that rebuilds it.
func TestIndexFilesRebuildsCorruptState(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.go")
	if err := os.WriteFile(a, []byte("package main\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &hookLLM{}
	store := &branchIndexStore{
		indexOrderStore: indexOrderStore{testStore: testStore{existing: map[string]bool{}}},
		names:           map[string][]string{},
	}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), &config.Config{})
	t.Cleanup(eng.Close)
	ctx := context.Background()
	if err := eng.IndexWorkspace(ctx, root, false); err != nil {
		t.Fatal(err)
	}
	wctx, err := eng.DetectContextAsRoot(ctx, root, false)
	if err != nil {
		t.Fatal(err)
	}
	statePath := indexer.StatePath(wctx.Root, wctx.CollectionName("go"))
	if err := os.WriteFile(statePath, []byte(`{"files": {`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(a, []byte("package main\nfunc AEdited() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := eng.IndexFiles(ctx, root, []string{a}); err != nil {
		t.Fatalf("watcher batch failed on a corrupt state file: %v", err)
	}
	state, err := indexer.LoadState(statePath)
	if err != nil {
		t.Fatalf("state file still corrupt: %v", err)
	}
	if _, ok := state.GetFileState(a); !ok {
		t.Fatal("rebuilt state does not track the edited file")
	}
}

// A directory indexed as plain files that becomes a linked worktree is
// excluded from then on; the watcher batch must drop its vectors from the
// parent instead of skipping it, or the parent keeps serving its code.
func TestIndexFilesDropsDirectoryThatBecameLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	git("add", "a.go")
	git("commit", "-qm", "initial")
	git("branch", "stale")
	wt := filepath.Join(root, "wt-stale")
	shared := filepath.Join(wt, "shared.go")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("package wt\nfunc Shared() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	provider := &hookLLM{}
	store := &branchIndexStore{
		indexOrderStore: indexOrderStore{testStore: testStore{existing: map[string]bool{}}},
		names:           map[string][]string{},
	}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), &config.Config{})
	t.Cleanup(eng.Close)
	ctx := context.Background()
	if err := eng.IndexWorkspace(ctx, root, false); err != nil {
		t.Fatal(err)
	}
	wctx, err := eng.DetectContextAsRoot(ctx, root, false)
	if err != nil {
		t.Fatal(err)
	}
	statePath := indexer.StatePath(wctx.Root, wctx.CollectionName("go"))
	if state, _ := indexer.LoadState(statePath); state == nil {
		t.Fatal("no state")
	} else if _, ok := state.GetFileState(shared); !ok {
		t.Fatal("setup: plain directory was not indexed")
	}

	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	git("worktree", "add", "-q", wt, "stale")
	if err := eng.IndexFiles(ctx, root, []string{wt, shared}); err != nil {
		t.Fatal(err)
	}
	state, err := indexer.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.GetFileState(shared); ok {
		t.Fatal("parent still holds the vectors of a directory that is now a linked worktree")
	}
}
