package watch

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// Renaming the workspace root must stop the watcher and drop it from the
// Manager, without queuing a reindex for the old path.
func TestRootRenameStopsWatcher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("root rename semantics differ on Windows")
	}
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	var changes atomic.Int32
	mgr := NewManager(Options{Debounce: 20 * time.Millisecond})
	if err := mgr.Start(root, func(context.Context, string, []string) error {
		changes.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.StopAll)
	time.Sleep(50 * time.Millisecond)

	if err := os.Rename(root, filepath.Join(base, "proj2")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mgr.mu.Lock()
		_, registered := mgr.watchers[root]
		mgr.mu.Unlock()
		if !registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manager still holds the watcher for the renamed root")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // past the debounce
	if n := changes.Load(); n != 0 {
		t.Fatalf("expected no reindex request for the old root, got %d", n)
	}
	// A watcher for the new path can be started normally.
	if err := mgr.Start(filepath.Join(base, "proj2"), func(context.Context, string, []string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
