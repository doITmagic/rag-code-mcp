package watch

import (
	"context"
	"testing"
)

// When directory watches cannot be installed, Start must say so instead of
// running with partial coverage.
func TestFileWatcherStartFailsWhenWatchesCannotBeAdded(t *testing.T) {
	fw, err := NewFileWatcher(t.TempDir(), Options{}, func(context.Context, string, []string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	// A closed backend rejects every Add, like an exhausted inotify quota.
	if err := fw.watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fw.Start(); err == nil {
		t.Fatal("expected Start to fail when no watch could be added")
	}
}
