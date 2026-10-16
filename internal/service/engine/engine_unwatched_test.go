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

func (noopWatcher) Start() {}
func (noopWatcher) Stop()  {}

// A root whose watcher failed must be re-indexed on later detections, and
// the watcher retried, instead of staying stale until a restart.
func TestUnwatchedRootReindexesOnDetect(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc Work() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &testStore{existing: map[string]bool{}}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: true, WatchEnabled: true}}
	eng := NewEngine(indexer.NewService(&mockLLM{}, store), search.NewService(&mockLLM{}, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))
	t.Cleanup(eng.Close)

	var watcherOK atomic.Bool
	eng.watchers.SetFactory(func(string, watch.Options, watch.OnChangeFunc) (watch.Watcher, error) {
		if !watcherOK.Load() {
			return nil, errors.New("inotify instance limit")
		}
		return noopWatcher{}, nil
	})

	if err := eng.IndexWorkspace(context.Background(), root, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := eng.unwatched.Load(root); !ok {
		t.Fatal("root not marked unwatched after watcher failure")
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
	waitFor := func(msg string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatal(msg)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFor("detection did not re-run indexing / retry the watcher", func() bool {
		_, still := eng.unwatched.Load(root)
		return !still
	})
	waitFor("retry job did not finish", func() bool {
		_, running := eng.indexingJobs.Load(wctx.ID)
		return !running
	})

	// Watched now: a further detection must not start another job.
	if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, running := eng.indexingJobs.Load(wctx.ID); running {
		t.Fatal("watched root triggered another index run")
	}
}
