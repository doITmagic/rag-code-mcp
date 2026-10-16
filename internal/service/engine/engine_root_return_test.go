package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A workspace moved away and back keeps its ID, so the once-per-ID trigger
// is spent. It must still get re-indexed and a fresh watcher on detection.
func TestRootMovedAwayAndBackIsRewatched(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("root rename semantics differ on Windows")
	}
	old := unwatchedRescanInterval
	unwatchedRescanInterval = 0
	t.Cleanup(func() { unwatchedRescanInterval = old })

	eng, root := newUnwatchedTestEngine(t)
	if err := eng.IndexWorkspace(context.Background(), root, false); err != nil {
		t.Fatal(err)
	}
	wctx, err := eng.DetectContextAsRoot(context.Background(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	eng.connectTriggered.Store(wctx.ID, true)
	if !eng.watchers.Watching(root) {
		t.Fatal("expected a watcher after the first index")
	}

	away := filepath.Join(filepath.Dir(root), "away")
	if err := os.Rename(root, away); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "watcher not dropped after root rename", func() bool { return !eng.watchers.Watching(root) })
	if err := os.Rename(away, root); err != nil {
		t.Fatal(err)
	}

	if _, err := eng.DetectContextAsRoot(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "returned root was not re-watched", func() bool { return eng.watchers.Watching(root) })
	waitFor(t, "re-index job did not finish", func() bool {
		_, running := eng.indexingJobs.Load(wctx.ID)
		return !running
	})
}
