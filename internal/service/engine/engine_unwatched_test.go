package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

// A completed rag_index_workspace run already synced the workspace: the next
// request must not start another full run, which, queued behind another
// workspace's indexing, made the complete index answer indexing_in_progress.
func TestExplicitIndexCountsAsFirstAutoIndex(t *testing.T) {
	eng, root := newUnwatchedTestEngine(t)
	eng.watchers.SetFactory(func(string, watch.Options, watch.OnChangeFunc) (watch.Watcher, error) {
		return noopWatcher{}, nil
	})
	ctx := context.Background()
	wctx, err := eng.DetectContextAsRoot(ctx, root, false)
	if err != nil {
		t.Fatal(err)
	}
	eng.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
	waitFor(t, "index did not finish", func() bool {
		s := indexer.LoadIndexStatus(root)
		return len(eng.ActiveIndexingJobs()) == 0 && s != nil && s.EndedAt != ""
	})
	// Another workspace is indexing.
	eng.indexingSlot <- struct{}{}
	defer func() { <-eng.indexingSlot }()
	if _, err := eng.DetectContext(ctx, filepath.Join(root, "main.go")); err != nil {
		t.Fatal(err)
	}
	if s := indexer.LoadIndexStatus(root); len(eng.ActiveIndexingJobs()) != 0 || s == nil || s.EndedAt == "" {
		t.Fatalf("request after a completed index started another run: jobs %v, status %+v", eng.ActiveIndexingJobs(), s)
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

// With watch_enabled: false there is no watcher at all, so detections must
// still re-run the incremental index (paced) instead of never again.
func TestWatchDisabledReindexesOnDetect(t *testing.T) {
	old := unwatchedRescanInterval
	unwatchedRescanInterval = 0
	t.Cleanup(func() { unwatchedRescanInterval = old })

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc Work() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &testStore{existing: map[string]bool{}}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: true, WatchEnabled: false}}
	eng := NewEngine(indexer.NewService(&mockLLM{}, store), search.NewService(&mockLLM{}, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))
	t.Cleanup(eng.Close)
	if eng.watchers != nil {
		t.Fatal("expected no watcher manager with watch_enabled: false")
	}

	wctx, err := eng.DetectContextAsRoot(context.Background(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	// Behave like a daemon that already auto-indexed this workspace once.
	eng.connectTriggered.Store(wctx.ID, true)
	before := time.Now()
	eng.lastIndexed.Store(wctx.ID, before.Add(-time.Hour))

	if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "detection did not re-run indexing with watching disabled", func() bool {
		if _, running := eng.indexingJobs.Load(wctx.ID); running {
			return false
		}
		last, ok := eng.lastIndexed.Load(wctx.ID)
		return ok && !last.(time.Time).Before(before)
	})
}

// A root moved away and back stops its watcher. The last watched run must not
// count against the unwatched rescan pace, or requests in the next minute
// served stale results and re-armed nothing.
func TestRootMovedBackIsRewatchedOnNextDetect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cannot rename a watched directory on Windows")
	}
	eng, root := newUnwatchedTestEngine(t)
	wctx, err := eng.DetectContextAsRoot(context.Background(), root, true)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first run did not start the watcher", func() bool {
		_, running := eng.indexingJobs.Load(wctx.ID)
		return !running && eng.watchers.Watching(root)
	})

	if err := os.Rename(root, root+"-away"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "watcher did not stop for the moved root", func() bool { return !eng.watchers.Watching(root) })
	if err := os.Rename(root+"-away", root); err != nil {
		t.Fatal(err)
	}

	if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "detection after the move-back did not rescan and re-watch the root", func() bool {
		_, running := eng.indexingJobs.Load(wctx.ID)
		return !running && eng.watchers.Watching(root)
	})
}
