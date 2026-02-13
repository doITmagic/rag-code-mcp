package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
)

func TestManagerStartWatcherRegistersWatcher(t *testing.T) {
	root := t.TempDir()

	mgr := &Manager{
		watchers: make(map[string]*FileWatcher),
	}

	mgr.StartWatcher(root)

	mgr.watchersMu.Lock()
	watcher, ok := mgr.watchers[root]
	mgr.watchersMu.Unlock()
	if !ok {
		t.Fatalf("expected watcher for %s", root)
	}
	if watcher == nil {
		t.Fatalf("watcher for %s is nil", root)
	}
	t.Cleanup(watcher.Stop)

	// Starting the watcher again for the same root should reuse the same instance
	mgr.StartWatcher(root)

	mgr.watchersMu.Lock()
	defer mgr.watchersMu.Unlock()
	if len(mgr.watchers) != 1 {
		t.Fatalf("expected exactly one watcher, got %d", len(mgr.watchers))
	}
	if mgr.watchers[root] != watcher {
		t.Fatalf("expected watcher instance to be reused for %s", root)
	}
}

func TestWatcherTriggersDebouncedEnsureWorkspaceIndexed(t *testing.T) {
	root := t.TempDir()

	goMod := filepath.Join(root, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/watcher\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("failed to create go.mod: %v", err)
	}

	goFile := filepath.Join(root, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	mgr := NewManager(
		&mockQdrant{
			exists: true,
			collectionInfo: &storage.CollectionInfo{
				VectorSize: 768,
				PointsCount: 1,
			},
			pointCount: 1,
		},
		&fakeLLM{dimension: 768},
		&config.Config{
			Workspace: config.WorkspaceConfig{
				Enabled:   true,
				AutoIndex: true,
				DetectionMarkers: []string{
					"go.mod",
				},
				ExcludePatterns: []string{".git", "node_modules", "vendor", "dist", "build"},
			},
		},
	)

	var indexCalls int32
	indexTriggered := make(chan struct{}, 4)
	mgr.indexLanguageFn = func(ctx context.Context, info *Info, language string, collectionName string, force bool) error {
		atomic.AddInt32(&indexCalls, 1)
		select {
		case indexTriggered <- struct{}{}:
		default:
		}
		return nil
	}

	mgr.StartWatcher(root)

	mgr.watchersMu.Lock()
	watcher := mgr.watchers[root]
	mgr.watchersMu.Unlock()
	if watcher == nil {
		t.Fatalf("expected watcher to be created for %s", root)
	}
	t.Cleanup(watcher.Stop)

	// Let watcher startup settle and ignore any startup noise.
	time.Sleep(250 * time.Millisecond)
	atomic.StoreInt32(&indexCalls, 0)
	for {
		select {
		case <-indexTriggered:
		default:
			goto drained
		}
	}

drained:
	newContent := fmt.Sprintf("package main\nfunc main() { println(%q) }\n", time.Now().Format(time.RFC3339Nano))
	if err := os.WriteFile(goFile, []byte(newContent), 0644); err != nil {
		t.Fatalf("failed to modify main.go: %v", err)
	}

	select {
	case <-indexTriggered:
		if got := atomic.LoadInt32(&indexCalls); got < 1 {
			t.Fatalf("expected at least one index trigger, got %d", got)
		}
	case <-time.After(8 * time.Second):
		t.Fatalf("timeout waiting for watcher debounce/index trigger")
	}
}
