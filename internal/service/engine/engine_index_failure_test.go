package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/watch"
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

// Files that fail to embed must surface as a failed run and be retried on a
// later request, even though the workspace was already auto-indexed once, but
// not on every request while the embedder is down.
func TestFailedEmbeddingIsReportedAndRetried(t *testing.T) {
	old := unwatchedRescanInterval
	unwatchedRescanInterval = 500 * time.Millisecond
	t.Cleanup(func() { unwatchedRescanInterval = old })
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
	if s := indexer.LoadIndexStatus(root); s == nil || !strings.Contains(s.Error, "Check if Ollama is running") {
		t.Fatalf("index status hides the failed file or its cause: %+v", s)
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
	time.Sleep(50 * time.Millisecond)
	if provider.calls.Load() != before {
		t.Fatal("a request right after the outage retried at once")
	}
	time.Sleep(unwatchedRescanInterval)
	if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "next request did not retry the failed file", func() bool { return provider.calls.Load() > before && jobDone() })
	if s := indexer.LoadIndexStatus(root); s == nil || s.Error != "" {
		t.Fatalf("retry did not succeed: %+v", s)
	}
}

// brokenFileLLM fails to embed one symbol every time, like a file that
// overflows the model's context, while the service itself stays up.
type brokenFileLLM struct {
	mockLLM
	brokenCalls atomic.Int32
}

func (p *brokenFileLLM) Embed(_ context.Context, text string) ([]float64, error) {
	if strings.Contains(text, "Broken") {
		p.brokenCalls.Add(1)
		return nil, errors.New("input length exceeds the context length")
	}
	return []float64{0.1}, nil
}

// A file that fails on its own must not start a new full run on every
// request of a watched workspace; only the run that found it reports it.
func TestFailingFileDoesNotRerunOnEveryRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc Broken() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &brokenFileLLM{}
	store := &testStore{existing: map[string]bool{}}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: true, WatchEnabled: true}}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))
	eng.watchers.SetFactory(func(string, watch.Options, watch.OnChangeFunc) (watch.Watcher, error) {
		return noopWatcher{}, nil
	})
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
	eng.connectTriggered.Store(wctx.ID, true)
	eng.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
	waitFor(t, "indexing did not finish", jobDone)
	if s := indexer.LoadIndexStatus(root); s == nil || s.Error == "" {
		t.Fatalf("index status hides the failed file: %+v", s)
	} else if strings.Contains(s.Error, "Ollama") {
		t.Fatalf("a file that fails on its own blames the embedding service: %s", s.Error)
	}

	for i := 0; i < 3; i++ {
		if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		waitFor(t, "indexing did not finish", jobDone)
	}
	if n := provider.brokenCalls.Load(); n != 1 {
		t.Fatalf("failing file was re-indexed on every request: %d attempts", n)
	}
}

// onceFailingLLM fails its first embed call only, like an embedder that
// blipped mid-run and was back before the run ended.
type onceFailingLLM struct {
	mockLLM
	calls atomic.Int32
}

func (p *onceFailingLLM) Embed(context.Context, string) ([]float64, error) {
	if p.calls.Add(1) == 1 {
		return nil, errors.New("connection refused")
	}
	return []float64{0.1}, nil
}

// Files that failed during an outage that was over by the end of the run
// must still be retried later in a watched workspace, paced like any retry.
func TestFileFailedInTransientOutageIsRetriedWithWatcher(t *testing.T) {
	old := unwatchedRescanInterval
	unwatchedRescanInterval = 300 * time.Millisecond
	t.Cleanup(func() { unwatchedRescanInterval = old })
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc Work() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &onceFailingLLM{}
	store := &testStore{existing: map[string]bool{}}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: true, WatchEnabled: true}}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))
	eng.watchers.SetFactory(func(string, watch.Options, watch.OnChangeFunc) (watch.Watcher, error) {
		return noopWatcher{}, nil
	})
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
	eng.connectTriggered.Store(wctx.ID, true)
	eng.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
	waitFor(t, "indexing did not finish", jobDone)
	if s := indexer.LoadIndexStatus(root); s == nil || s.Error == "" {
		t.Fatalf("index status hides the failed file: %+v", s)
	}

	before := provider.calls.Load()
	if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	waitFor(t, "indexing did not finish", jobDone)
	if provider.calls.Load() != before {
		t.Fatal("a request right after the failed run retried at once")
	}
	time.Sleep(unwatchedRescanInterval)
	if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "file that failed during a recovered outage was never retried", func() bool { return provider.calls.Load() > before && jobDone() })
	if s := indexer.LoadIndexStatus(root); s == nil || s.Error != "" {
		t.Fatalf("retry did not succeed: %+v", s)
	}
}
