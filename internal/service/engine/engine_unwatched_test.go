package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/watch"
)

type noopWatcher struct{}

func (noopWatcher) Start() error { return nil }
func (noopWatcher) Stop()        {}

func newUnwatchedTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc Work() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &testStore{existing: map[string]bool{}}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: true, WatchEnabled: true}}
	eng := NewEngine(indexer.NewService(&mockLLM{}, store), search.NewService(&mockLLM{}, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))
	t.Cleanup(eng.Close)
	return eng, root
}

func waitFor(t *testing.T, msg string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A root whose watcher failed must be re-indexed on later detections, and
// the watcher retried, instead of staying stale until a restart.
func TestUnwatchedRootReindexesOnDetect(t *testing.T) {
	old := unwatchedRescanInterval
	unwatchedRescanInterval = 0
	t.Cleanup(func() { unwatchedRescanInterval = old })

	eng, root := newUnwatchedTestEngine(t)
	var attempts atomic.Int32
	var watcherOK atomic.Bool
	eng.watchers.SetFactory(func(string, watch.Options, watch.OnChangeFunc) (watch.Watcher, error) {
		attempts.Add(1)
		if !watcherOK.Load() {
			return nil, errors.New("inotify instance limit")
		}
		return noopWatcher{}, nil
	})

	if err := eng.IndexWorkspace(context.Background(), root, false); err != nil {
		t.Fatal(err)
	}
	if eng.watchers.Watching(root) || attempts.Load() != 1 {
		t.Fatalf("expected one failed watcher attempt, watching=%v attempts=%d", eng.watchers.Watching(root), attempts.Load())
	}
	wctx, err := eng.DetectContextAsRoot(context.Background(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	// Behave like a daemon that already auto-indexed this workspace once.
	eng.connectTriggered.Store(wctx.ID, true)

	watcherOK.Store(true)
	if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "detection did not re-run indexing / retry the watcher", func() bool { return attempts.Load() == 2 })
	waitFor(t, "retry job did not finish", func() bool {
		_, running := eng.indexingJobs.Load(wctx.ID)
		return !running
	})
	if !eng.watchers.Watching(root) {
		t.Fatal("retried watcher not registered")
	}

	// Watched now: a further detection must not start another job.
	if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := attempts.Load(); n != 2 {
		t.Fatalf("watched root triggered another index run, attempts=%d", n)
	}
}

// With the watcher failing for good, detections must not rescan more often
// than unwatchedRescanInterval.
func TestUnwatchedRescanIsPaced(t *testing.T) {
	old := unwatchedRescanInterval
	unwatchedRescanInterval = time.Hour
	t.Cleanup(func() { unwatchedRescanInterval = old })

	eng, root := newUnwatchedTestEngine(t)
	var attempts atomic.Int32
	eng.watchers.SetFactory(func(string, watch.Options, watch.OnChangeFunc) (watch.Watcher, error) {
		attempts.Add(1)
		return nil, errors.New("inotify instance limit")
	})
	if err := eng.IndexWorkspace(context.Background(), root, false); err != nil {
		t.Fatal(err)
	}
	wctx, err := eng.DetectContextAsRoot(context.Background(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	eng.connectTriggered.Store(wctx.ID, true)
	for i := 0; i < 5; i++ {
		if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "job did not finish", func() bool {
			_, running := eng.indexingJobs.Load(wctx.ID)
			return !running
		})
	}
	if n := attempts.Load(); n != 1 {
		t.Fatalf("expected no rescan within the interval, got %d watcher attempts", n)
	}
}
