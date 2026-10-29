package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/watch"
)

// A root reached through a symlink must resolve to the same workspace as
// file paths inside it; otherwise every alternation forces a full reindex.
func TestSymlinkedRootHasOneWorkspaceIdentity(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "main.go"), []byte("package main\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@e.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = real
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	store := &branchIndexStore{
		indexOrderStore: indexOrderStore{testStore: testStore{existing: map[string]bool{}}},
		names:           map[string][]string{},
	}
	provider := &mockLLM{}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: true, WatchEnabled: true}}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
	eng.watchers.SetFactory(func(string, watch.Options, watch.OnChangeFunc) (watch.Watcher, error) {
		return noopWatcher{}, nil
	})
	t.Cleanup(eng.Close)
	ctx := context.Background()

	if err := eng.IndexWorkspace(ctx, link, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "idle", func() bool { return len(eng.ActiveIndexingJobs()) == 0 })
	for i := 0; i < 2; i++ {
		byFile, err := eng.DetectContext(ctx, filepath.Join(link, "main.go"))
		if err != nil {
			t.Fatal(err)
		}
		byRoot, err := eng.DetectContextAsRoot(ctx, link, false)
		if err != nil {
			t.Fatal(err)
		}
		if byFile.ID != byRoot.ID || byFile.ReindexRequired || byRoot.ReindexRequired {
			t.Fatalf("iteration %d: file id=%s reindex=%v, root id=%s reindex=%v", i, byFile.ID, byFile.ReindexRequired, byRoot.ID, byRoot.ReindexRequired)
		}
		waitFor(t, "idle", func() bool { return len(eng.ActiveIndexingJobs()) == 0 })
	}
	if eng.watchers.Watching(link) {
		t.Fatal("symlinked spelling got its own watcher")
	}
}
