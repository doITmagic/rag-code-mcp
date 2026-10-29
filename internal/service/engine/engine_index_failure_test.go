package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

type flakyLLM struct {
	mockLLM
	fail  atomic.Bool
	calls atomic.Int32
}

func (p *flakyLLM) Embed(context.Context, string) ([]float64, error) {
	p.calls.Add(1)
	if p.fail.Load() {
		return nil, errors.New("embedding service unavailable")
	}
	return []float64{0.1}, nil
}

// Files that fail to embed must surface as a failed run and be retried on the
// next request, even though the workspace was already auto-indexed once.
func TestFailedEmbeddingIsReportedAndRetried(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc Work() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &flakyLLM{}
	provider.fail.Store(true)
	store := &testStore{existing: map[string]bool{}}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: true}}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))
	t.Cleanup(eng.Close)

	wctx, err := eng.DetectContextAsRoot(context.Background(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	store.existing[wctx.CollectionName("go")] = true
	jobDone := func() bool {
		_, running := eng.indexingJobs.Load(wctx.ID)
		return !running
	}

	// Behave like a daemon that already auto-indexed this workspace once.
	eng.connectTriggered.Store(wctx.ID, true)
	eng.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
	waitFor(t, "indexing did not finish", jobDone)
	if s := indexer.LoadIndexStatus(root); s == nil || s.Error == "" {
		t.Fatalf("index status hides the failed file: %+v", s)
	}

	// The watcher path must not hide the failure either.
	if err := eng.IndexFiles(context.Background(), root, []string{path}); err == nil {
		t.Fatal("watcher indexing reported success although the file failed to embed")
	}

	provider.fail.Store(false)
	before := provider.calls.Load()
	if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "next request did not retry the failed file", func() bool { return provider.calls.Load() > before && jobDone() })
	if s := indexer.LoadIndexStatus(root); s == nil || s.Error != "" {
		t.Fatalf("retry did not succeed: %+v", s)
	}
}
