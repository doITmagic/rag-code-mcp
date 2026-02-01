package workspace

import "testing"

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
