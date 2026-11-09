package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/index"
	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatcher(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	cfg.ExcludePatterns = []string{"vendor"}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(root, "old.go"), "func Old\n")
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(root)
	require.NoError(t, err)
	require.NotNil(t, w.watch)

	wait := func(name string, pred func(index.Options) bool) index.Options {
		t.Helper()
		var found index.Options
		require.Eventually(t, func() bool {
			for _, c := range rec.snapshot() {
				if pred(c) {
					found = c
					return true
				}
			}
			return false
		}, 2*time.Second, 10*time.Millisecond, name)
		return found
	}
	has := func(f string) func(index.Options) bool {
		return func(o index.Options) bool { return slices.Contains(o.Files, f) }
	}

	// Two writes inside one debounce window land in one files run.
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	writeFile(t, a, "func A\n")
	writeFile(t, b, "func B\n")
	writeFile(t, filepath.Join(root, "vendor", "v.go"), "func V\n")
	writeFile(t, filepath.Join(root, ".ragcode", "state.json"), "{}")
	run := wait("debounced batch", has(a))
	assert.Contains(t, run.Files, b)
	assert.Len(t, rec.snapshot(), 1, "one run for the whole batch")

	// A deleted file is purged, a new directory is watched.
	require.NoError(t, os.Remove(filepath.Join(root, "old.go")))
	wait("deleted", func(o index.Options) bool { return slices.Contains(o.Deleted, filepath.Join(root, "old.go")) })
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	time.Sleep(100 * time.Millisecond) // let the new directory watch land before writing into it
	writeFile(t, filepath.Join(root, "sub", "s.go"), "func S\n")
	wait("new dir", has(filepath.Join(root, "sub", "s.go")))
	for _, c := range rec.snapshot() {
		assert.NotContains(t, c.Files, filepath.Join(root, "vendor", "v.go"))
		assert.NotContains(t, c.Files, filepath.Join(root, ".ragcode", "state.json"))
		assert.NotNil(t, c.Files, "no full run yet")
	}

	// A checkout queues a full run.
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/dev\n")
	wait("git HEAD", func(o index.Options) bool { return o.Files == nil })

	w.watch.Stop()
	w.watch.Stop() // idempotent
}

func TestWatcherRootRemoved(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := filepath.Join(t.TempDir(), "ws")
	require.NoError(t, os.Mkdir(root, 0o755))
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	w, err := e.Workspace(root)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(root))
	require.Eventually(t, func() bool { return w.Status().LastError != "" }, 2*time.Second, 10*time.Millisecond)
	assert.Contains(t, w.Status().LastError, "removed")
}

// A root that comes back (git stash of the whole dir, a restore, a rename
// undone) is watched again on the next call, with a catch-up run.
func TestWatcherRootComesBack(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := filepath.Join(t.TempDir(), "ws")
	require.NoError(t, os.Mkdir(root, 0o755))
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(root)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(root))
	require.Eventually(t, func() bool { return w.Status().LastError != "" }, 2*time.Second, 10*time.Millisecond)

	require.NoError(t, os.Mkdir(root, 0o755))
	w2, err := e.Workspace(root)
	require.NoError(t, err)
	assert.Same(t, w, w2)
	require.Eventually(t, func() bool {
		return slices.ContainsFunc(rec.snapshot(), func(o index.Options) bool { return o.Files == nil })
	}, 2*time.Second, 10*time.Millisecond, "catch-up full run")
	a := filepath.Join(root, "a.go")
	writeFile(t, a, "func A\n")
	require.Eventually(t, func() bool {
		return slices.ContainsFunc(rec.snapshot(), func(o index.Options) bool { return slices.Contains(o.Files, a) })
	}, 2*time.Second, 10*time.Millisecond, "watched again")
}

// One directory the server cannot watch must not leave the rest unwatched.
func TestWatcherUnreadableSubdir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	require.NoError(t, os.Mkdir(locked, 0o000))
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(root)
	require.NoError(t, err)
	require.NotNil(t, w.watch)
	a := filepath.Join(root, "a.go")
	writeFile(t, a, "func A\n")
	require.Eventually(t, func() bool {
		return slices.ContainsFunc(rec.snapshot(), func(o index.Options) bool { return slices.Contains(o.Files, a) })
	}, 2*time.Second, 10*time.Millisecond)
}

// A failed restart (the root came back unreadable) is retried on a later call.
func TestWatcherRestartRetried(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := filepath.Join(t.TempDir(), "ws")
	require.NoError(t, os.Mkdir(root, 0o755))
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = (&runRecorder{}).run
	w, err := e.Workspace(root)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(root))
	require.Eventually(t, func() bool { return w.Status().LastError != "" }, 2*time.Second, 10*time.Millisecond)

	require.NoError(t, os.Mkdir(root, 0o100)) // can enter, cannot list: the watch fails
	t.Cleanup(func() { os.Chmod(root, 0o755) })
	_, err = e.Workspace(root)
	require.NoError(t, err)
	watching := func() bool { e.mu.Lock(); defer e.mu.Unlock(); return w.watch != nil }
	require.False(t, watching())

	require.NoError(t, os.Chmod(root, 0o755))
	_, err = e.Workspace(root)
	require.NoError(t, err)
	assert.True(t, watching(), "restart retried")
}

// Only a full walk applies ignore rules to files already indexed (or newly
// eligible): a .gitignore edit, at any depth, queues one.
func TestWatcherGitignoreQueuesFullRun(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(root)
	require.NoError(t, err)
	require.NotNil(t, w.watch)
	for _, p := range []string{".gitignore", filepath.Join("sub", ".gitignore")} {
		n := len(rec.snapshot())
		writeFile(t, filepath.Join(root, p), "local.json\n")
		require.Eventually(t, func() bool {
			s := rec.snapshot()
			return len(s) > n && s[len(s)-1].Files == nil
		}, 2*time.Second, 10*time.Millisecond, p)
	}
}

// Close beginning while Resume sets up a root's watcher (a session that ends
// right after initialize): no catch-up run is queued or logged, and the
// watches that fail because the instance closed are not warnings.
func TestCloseDuringResumeWatchSetup(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sub", "a.go"), "func A\n")
	writeFile(t, filepath.Join(root, "other", "b.go"), "func B\n")
	data, _ := json.Marshal([]string{root})
	require.NoError(t, os.MkdirAll(config.HomeDir(), 0o755))
	require.NoError(t, os.WriteFile(rootsPath(), data, 0o644))
	st, _ := json.Marshal(index.Status{Root: root, StartedAt: time.Now(), EndedAt: time.Now(), Phase: "done"})
	writeFile(t, filepath.Join(index.Dir(root), "index_status.json"), string(st))

	var logBuf safeBuf
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer e.Close()
	e.run = rec.run
	// Close starts right after the watcher instance is created, before the
	// root's directories are added: the first half of Close, under e.mu.
	newFSWatcher = func() (*fsnotify.Watcher, error) {
		fsw, err := fsnotify.NewWatcher()
		if err == nil {
			e.closed = true
			e.cancel()
			fsw.Close()
		}
		return fsw, err
	}
	t.Cleanup(func() { newFSWatcher = fsnotify.NewWatcher })
	e.Resume(context.Background())
	e.Close()
	assert.Empty(t, rec.snapshot())
	assert.NotContains(t, logBuf.String(), "level=WARN")
	assert.NotContains(t, logBuf.String(), "catch-up index")
}

func fullRuns(rec *runRecorder) int {
	n := 0
	for _, c := range rec.snapshot() {
		if c.Files == nil {
			n++
		}
	}
	return n
}

func watching(e *Engine, dir string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Contains(e.fsw.WatchList(), dir)
}

// git worktree add creates the directory before its .git: the watcher has
// already watched it, so the first event that shows a linked worktree drops
// its watches and queues a full run, whose walk prunes it.
func TestWatcherLinkedWorktreeAdded(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := t.TempDir()
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	_, err := e.Workspace(root)
	require.NoError(t, err)

	wt := filepath.Join(root, "wt2")
	require.NoError(t, os.Mkdir(wt, 0o755))
	require.Eventually(t, func() bool { return watching(e, wt) }, 2*time.Second, 10*time.Millisecond)
	admin := filepath.Join(t.TempDir(), "worktrees", "wt2")
	writeFile(t, filepath.Join(admin, "commondir"), "../..\n")
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+admin+"\n")
	require.Eventually(t, func() bool { return fullRuns(rec) > 0 && !watching(e, wt) }, 2*time.Second, 10*time.Millisecond)
}

// git init in a root watched without a repository queues a full run (files
// it now ignores are purged) and starts watching HEAD and refs.
func TestWatcherGitInitAfterStart(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := t.TempDir()
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	_, err := e.Workspace(root)
	require.NoError(t, err)

	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	require.Eventually(t, func() bool { return fullRuns(rec) > 0 }, 2*time.Second, 10*time.Millisecond, "repository created")
	require.Eventually(t, func() bool { return watching(e, filepath.Join(root, ".git")) }, 2*time.Second, 10*time.Millisecond)
	n := fullRuns(rec)
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	require.Eventually(t, func() bool { return fullRuns(rec) > n }, 2*time.Second, 10*time.Millisecond, "HEAD written")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git", "refs", "heads"), 0o755))
	require.Eventually(t, func() bool { return watching(e, filepath.Join(root, ".git", "refs", "heads")) }, 2*time.Second, 10*time.Millisecond)
	n = fullRuns(rec)
	writeFile(t, filepath.Join(root, ".git", "refs", "heads", "main"), "0123\n")
	require.Eventually(t, func() bool { return fullRuns(rec) > n }, 2*time.Second, 10*time.Millisecond, "first commit")
}

// RagCode's own home inside a root (a non-hidden RAGCODE_HOME) is never
// watched: its state and log writes would queue a run after every run. The
// home itself is refused as a root.
func TestWatcherIgnoresOwnHome(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := t.TempDir()
	rch := filepath.Join(root, "rch")
	t.Setenv("RAGCODE_HOME", rch)
	require.NoError(t, os.Mkdir(rch, 0o755))
	_, err := validateRoot(cfg, rch)
	var re *RootError
	require.ErrorAs(t, err, &re)
	assert.Contains(t, re.Reason, "own data directory")

	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	_, err = e.Workspace(root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return watching(e, root) }, 2*time.Second, 10*time.Millisecond)
	time.Sleep(300 * time.Millisecond) // let the start-up run settle
	rec.mu.Lock()
	n := len(rec.calls)
	rec.mu.Unlock()
	writeFile(t, filepath.Join(rch, "index", "c", "state.json"), "{}")
	writeFile(t, filepath.Join(root, "seen.go"), "func Seen\n") // control: a real file is seen
	require.Eventually(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return len(rec.calls) > n
	}, 3*time.Second, 10*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	assert.False(t, watching(e, rch), "own home not watched")
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, c := range rec.calls[n:] {
		for _, f := range c.Files {
			assert.False(t, strings.HasPrefix(f, rch), "own home file queued: %s", f)
		}
	}
}
