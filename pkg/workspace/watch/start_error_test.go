package watch

import (
	"context"
	"os"
	"path/filepath"
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

// A root that vanished before Start has no watch to report its return, so
// Start must fail and let the engine fall back to rescans.
func TestFileWatcherStartFailsWhenRootIsMissing(t *testing.T) {
	fw, err := NewFileWatcher(filepath.Join(t.TempDir(), "gone"), Options{}, func(context.Context, string, []string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Start(); err == nil {
		fw.Stop()
		t.Fatal("expected Start to fail for a missing root")
	}
}

// WalkDir does not follow a symlinked root, so nothing would be watched;
// Start must fail instead of reporting the root as watched.
func TestFileWatcherStartFailsWhenRootIsSymlink(t *testing.T) {
	for name, mkTarget := range map[string]bool{"existing-target": true, "broken": false} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			target, root := filepath.Join(base, "target"), filepath.Join(base, "link")
			if mkTarget {
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, root); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			fw, err := NewFileWatcher(root, Options{}, func(context.Context, string, []string) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := fw.Start(); err == nil {
				fw.Stop()
				t.Fatal("expected Start to fail for a symlinked root")
			}
		})
	}
}
