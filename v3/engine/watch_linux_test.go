package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func inotifyFDs(t *testing.T) int {
	t.Helper()
	fds, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	n := 0
	for _, fd := range fds {
		if l, _ := os.Readlink(filepath.Join("/proc/self/fd", fd.Name())); l == "anon_inode:inotify" {
			n++
		}
	}
	return n
}

// The per-user inotify instance limit is small (128 by default) and every
// client starts its own server: all roots of a process share one instance,
// and nested roots still both see their events.
func TestWatchersShareOneInotifyInstance(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	before := inotifyFDs(t)
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	e.run = rec.run
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	require.NoError(t, os.Mkdir(inner, 0o755))
	var roots []*Workspace
	for _, r := range []string{outer, inner, t.TempDir(), t.TempDir()} {
		w, err := e.Workspace(r)
		require.NoError(t, err)
		require.NotNil(t, w.watch)
		roots = append(roots, w)
	}
	assert.Equal(t, before+1, inotifyFDs(t))

	f := filepath.Join(inner, "a.go")
	writeFile(t, f, "func A\n")
	seen := func(root string) bool {
		for _, c := range rec.snapshot() {
			if c.Root == root && len(c.Files) == 1 && c.Files[0] == f {
				return true
			}
		}
		return false
	}
	require.Eventually(t, func() bool { return seen(outer) && seen(inner) }, 2*time.Second, 10*time.Millisecond)
	e.Close()
	assert.Equal(t, before, inotifyFDs(t), "Close releases the instance")
	_ = roots
}

// A watcher that cannot start (instance limit reached) is retried on the
// next call for that root, with a catch-up run, instead of never.
func TestWatcherStartRetried(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	fail := true
	newFSWatcher = func() (*fsnotify.Watcher, error) {
		if fail {
			return nil, errors.New("too many open files")
		}
		return fsnotify.NewWatcher()
	}
	t.Cleanup(func() { newFSWatcher = fsnotify.NewWatcher })
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	root := t.TempDir()
	w, err := e.Workspace(root)
	require.NoError(t, err)
	require.Nil(t, w.watch)
	fail = false
	_, err = e.Workspace(root)
	require.NoError(t, err)
	assert.NotNil(t, w.watch)
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 }, 2*time.Second, 10*time.Millisecond)
}

// An event overflow drops the Create of new directories: the recovery
// re-walks the tree so edits inside them are seen again.
func TestOverflowRewatchesTree(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	root := t.TempDir()
	w, err := e.Workspace(root)
	require.NoError(t, err)
	require.NotNil(t, w.watch)
	sub := filepath.Join(root, "sub")
	require.NoError(t, e.fsw.Remove(root)) // the Create of sub is lost
	require.NoError(t, os.Mkdir(sub, 0o755))
	require.NoError(t, e.fsw.Add(root))
	e.fsw.Errors <- fsnotify.ErrEventOverflow
	require.Eventually(t, func() bool { return len(rec.snapshot()) >= 1 }, 2*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	f := filepath.Join(sub, "s.go")
	writeFile(t, f, "func S\n")
	require.Eventually(t, func() bool {
		for _, c := range rec.snapshot() {
			if len(c.Files) == 1 && c.Files[0] == f {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)
}
