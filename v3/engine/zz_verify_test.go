package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/index"
	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent verification probes; kept beside the package tests.

func TestVerifyConcurrentWorkspaceCalls(t *testing.T) {
	cfg := testConfig(t)
	root := t.TempDir()
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	var wg sync.WaitGroup
	got := make([]*Workspace, 16)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, err := e.Workspace(root + "/")
			assert.NoError(t, err)
			got[i] = w
		}()
	}
	wg.Wait()
	for _, w := range got[1:] {
		assert.Same(t, got[0], w)
	}
	assert.Equal(t, []string{root}, readRoots())
	assert.Equal(t, root, got[0].Root)
	// "sub/.." resolves to the same root.
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	w, err := e.Workspace(filepath.Join(root, "sub", ".."))
	require.NoError(t, err)
	assert.Same(t, got[0], w)
}

func TestVerifyCancelledContext(t *testing.T) {
	cfg := testConfig(t)
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := w.Search(ctx, "x", store.Filter{}, 3)
		assert.Error(t, err)
		_, err = w.Scroll(ctx, store.Filter{}, 3)
		assert.Error(t, err)
		_, _ = w.Ready(ctx)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hang on cancelled ctx")
	}
	// Empty / junk queries never hang or panic either.
	_, err = w.Search(context.Background(), "", store.Filter{}, 5)
	assert.Error(t, err) // not indexed
	_, err = w.Search(context.Background(), "\x00\xff", store.Filter{}, 5)
	assert.Error(t, err)
}

func TestVerifyEnqueueDuringLockRetryWaits(t *testing.T) {
	cfg := testConfig(t)
	cfg.AutoIndex = false
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "func A\n")
	be := &blockingEmbedder{release: make(chan struct{})}
	a := New(cfg, store.NewMemory(), be, nil)
	defer a.Close()
	wa, err := a.Workspace(root)
	require.NoError(t, err)
	wa.Index(false)
	require.Eventually(t, func() bool { p := wa.Status().Phase; return p == "parsing" || p == "embedding" }, 2*time.Second, 5*time.Millisecond)

	rec := &runRecorder{}
	b := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer b.Close()
	wb, err := b.Workspace(root)
	require.NoError(t, err)
	// Direct the first call at the real runner (to hit the lock), the rest at the recorder.
	real := b.run
	var once sync.Once
	b.run = func(ctx context.Context, o index.Options) (index.Status, error) {
		var st index.Status
		var err error
		hit := false
		once.Do(func() { hit = true; st, err = real(ctx, o) })
		if hit {
			return st, err
		}
		return rec.run(ctx, o)
	}
	wb.IndexFiles([]string{filepath.Join(root, "a.go")}, nil)
	require.Eventually(t, func() bool { s := wb.Status(); return s.LastError != "" && !s.Running }, 2*time.Second, 5*time.Millisecond)
	wb.e.mu.Lock()
	hasTimer := wb.retry != nil
	wb.e.mu.Unlock()
	assert.True(t, hasTimer, "retry timer armed")
	// A request during the retry window is merged, not started.
	wb.Index(false)
	time.Sleep(150 * time.Millisecond)
	assert.Empty(t, rec.snapshot(), "no run before the retry timer fires")
	s := wb.Status()
	assert.True(t, s.Queued)
	assert.False(t, s.Running)
	close(be.release)
	require.Eventually(t, func() bool { return wa.Status().Phase == "done" }, 2*time.Second, 5*time.Millisecond)
}

// Two processes share one Qdrant: a recreate requested while the other one
// holds the lock drops the collection under the running job.
func TestVerifyRecreateWhileLockedDropsLiveCollection(t *testing.T) {
	cfg := testConfig(t)
	cfg.AutoIndex = false
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "func A\n")
	shared := store.NewMemory()
	be := &blockingEmbedder{release: make(chan struct{})}
	a := New(cfg, shared, be, nil)
	defer a.Close()
	wa, err := a.Workspace(root)
	require.NoError(t, err)
	wa.Index(false)
	require.Eventually(t, func() bool { p := wa.Status().Phase; return p == "parsing" || p == "embedding" }, 2*time.Second, 5*time.Millisecond)
	exists, _ := shared.CollectionExists(context.Background(), wa.Collection)
	require.True(t, exists, "run A created the collection")

	b := New(cfg, shared, &embed.Fake{}, nil)
	defer b.Close()
	wb, err := b.Workspace(root)
	require.NoError(t, err)
	wb.Index(true)
	require.Eventually(t, func() bool { return wb.Status().LastError != "" }, 2*time.Second, 5*time.Millisecond)
	exists, _ = shared.CollectionExists(context.Background(), wa.Collection)
	t.Logf("collection still exists while A indexes: %v", exists)
	close(be.release)
	require.Eventually(t, func() bool { p := wa.Status().Phase; return p == "done" || p == "failed" }, 2*time.Second, 5*time.Millisecond)
	t.Logf("run A ended: phase=%s err=%q", wa.Status().Phase, wa.Status().LastError)
	assert.True(t, exists, "recreate must not delete the collection while another process indexes into it")
}

func TestVerifyWatcherDirMovedOutAndForcedFlush(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	cfg.WatchDebounce = 50 * time.Millisecond
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(root, "pkg", "p.go"), "func P\n")
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	_, err := e.Workspace(root)
	require.NoError(t, err)

	// A directory moved out of the tree queues a full run.
	require.NoError(t, os.Rename(filepath.Join(root, "pkg"), filepath.Join(outside, "pkg")))
	assert.Eventually(t, func() bool {
		for _, c := range rec.snapshot() {
			if c.Files == nil {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "full run after directory moved out")
	for _, c := range rec.snapshot() {
		t.Logf("run: files=%q deleted=%q", c.Files, c.Deleted)
	}

	// A populated directory moved in: its files are queued.
	writeFile(t, filepath.Join(outside, "in", "i.go"), "func I\n")
	require.NoError(t, os.Rename(filepath.Join(outside, "in"), filepath.Join(root, "in")))
	require.Eventually(t, func() bool {
		for _, c := range rec.snapshot() {
			for _, f := range c.Files {
				if f == filepath.Join(root, "in", "i.go") {
					return true
				}
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "files of a moved-in directory")

	// Continuous writes: a flush is forced after 10 debounce periods (500 ms).
	n := len(rec.snapshot())
	start := time.Now()
	deadline := start.Add(1500 * time.Millisecond)
	var flushedAt time.Duration
	for time.Now().Before(deadline) {
		writeFile(t, filepath.Join(root, "hot.go"), "func H\n"+time.Now().String())
		if len(rec.snapshot()) > n && flushedAt == 0 {
			flushedAt = time.Since(start)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("forced flush after %s", flushedAt)
	assert.NotZero(t, flushedAt, "a flush must happen under a continuous event stream")
	assert.Less(t, flushedAt, 1200*time.Millisecond)
}

func TestVerifyStatusFromDiskBeforeRun(t *testing.T) {
	cfg := testConfig(t)
	cfg.AutoIndex = false
	root := t.TempDir()
	writeFile(t, filepath.Join(index.Dir(root), "index_status.json"), `{"root":"x","phase":"done","started_at":"2026-01-01T00:00:00Z","ended_at":"2026-01-01T00:00:01Z","done":7}`)
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	w, err := e.Workspace(root)
	require.NoError(t, err)
	s := w.Status()
	assert.Equal(t, 7, s.Done)
	assert.Equal(t, "done", s.Phase)
	assert.False(t, s.Running)
	// Truncated / junk status file is ignored, not a panic.
	writeFile(t, filepath.Join(index.Dir(root), "index_status.json"), `{"root":`)
	assert.Equal(t, "", w.Status().Phase)
}

// Second verification pass.

func TestVerifyHomeSymlinkGuard(t *testing.T) {
	cfg := testConfig(t)
	realHome := t.TempDir()
	link := filepath.Join(t.TempDir(), "homelink")
	require.NoError(t, os.Symlink(realHome, link))
	t.Setenv("HOME", link)
	_, err := validateRoot(cfg, realHome)
	var re *RootError
	require.ErrorAs(t, err, &re, "the resolved home directory must be rejected")
	assert.Contains(t, re.Reason, "home")
	_, err = validateRoot(cfg, link)
	require.ErrorAs(t, err, &re)
}

func TestVerifyCloseCancelsBlockedRun(t *testing.T) {
	cfg := testConfig(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "func A\n")
	be := &blockingEmbedder{release: make(chan struct{})}
	e := New(cfg, store.NewMemory(), be, nil)
	w, err := e.Workspace(root)
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool { return w.Status().Running }, 2*time.Second, 5*time.Millisecond)
	done := make(chan struct{})
	go func() { e.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hangs on a blocked run")
	}
	w.IndexFiles([]string{filepath.Join(root, "a.go")}, nil) // no-op after Close
	assert.False(t, w.Status().Queued)
}

func TestVerifyResumeJunkRootsFile(t *testing.T) {
	cfg := testConfig(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(rootsPath()), 0o755))
	for _, junk := range []string{"", "\x00", "[", `["` + "\xff" + `", 3]`, `{"a":1}`} {
		require.NoError(t, os.WriteFile(rootsPath(), []byte(junk), 0o644))
		e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e.Resume(ctx)
		cancel()
		e.Close()
	}
	// A root registered after junk is still written and read back.
	require.NoError(t, os.WriteFile(rootsPath(), []byte("junk"), 0o644))
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	root := t.TempDir()
	_, err := e.Workspace(root)
	require.NoError(t, err)
	assert.Equal(t, []string{root}, readRoots())
}

func TestVerifyFilesRunWithOnlyDeletions(t *testing.T) {
	cfg := testConfig(t)
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	w.IndexFiles(nil, []string{"gone.go"})
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 && !w.Status().Running }, 2*time.Second, 5*time.Millisecond)
	c := rec.snapshot()[0]
	assert.NotNil(t, c.Files, "a deletion-only request must stay a files run, not a full walk")
	assert.Equal(t, []string{"gone.go"}, c.Deleted)
	w.IndexFiles(nil, nil) // nothing: no run
	time.Sleep(50 * time.Millisecond)
	assert.Len(t, rec.snapshot(), 1)
}

func TestVerifyWorkspaceJunkPaths(t *testing.T) {
	cfg := testConfig(t)
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	for _, p := range []string{"\x00", "/\x00", string([]byte{0xff, 0xfe}), "/nonexistent/\xff", "   ", "/dev/null", "/proc/self/fd/0"} {
		_, err := e.Workspace(p)
		var re *RootError
		assert.ErrorAs(t, err, &re, "%q", p)
	}
}

// Third verification pass.

type downEmbedder struct{ *embed.Fake }

func (*downEmbedder) Dimension(context.Context) (int, error) { return 0, embed.ErrUnavailable }

type downStore struct{ store.Store }

func (downStore) CollectionExists(context.Context, string) (bool, error) {
	return false, errors.New("dial tcp: connection refused")
}

func TestVerifyReadyRemediation(t *testing.T) {
	cfg := testConfig(t)
	cfg.AutoIndex = false
	e := New(cfg, store.NewMemory(), &downEmbedder{&embed.Fake{}}, nil)
	defer e.Close()
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	ok, msg := w.Ready(context.Background())
	assert.False(t, ok)
	assert.Contains(t, msg, "Ollama")
	assert.Contains(t, msg, cfg.OllamaBaseURL)

	e2 := New(cfg, downStore{store.NewMemory()}, &embed.Fake{}, nil)
	defer e2.Close()
	w2, err := e2.Workspace(t.TempDir())
	require.NoError(t, err)
	ok, msg = w2.Ready(context.Background())
	assert.False(t, ok)
	assert.Contains(t, msg, "Qdrant")
	assert.Contains(t, msg, cfg.QdrantURL)
}

func TestVerifySearchEmbedFailureDoesNotEnqueue(t *testing.T) {
	cfg := testConfig(t)
	fk := &embed.Fake{}
	fk.Fail.Store(1)
	e := New(cfg, store.NewMemory(), fk, nil)
	defer e.Close()
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	_, err = w.Search(context.Background(), "x", store.Filter{}, 3)
	require.ErrorIs(t, err, embed.ErrUnavailable)
	assert.NotErrorIs(t, err, ErrNotIndexed)
	assert.False(t, w.Status().Queued, "an embedder failure is not first contact")
	assert.False(t, w.Status().Running)
}

func TestVerifyRecreateAbsorbsQueuedFiles(t *testing.T) {
	cfg := testConfig(t)
	st := store.NewMemory()
	rec := &runRecorder{hold: make(chan struct{})}
	e := New(cfg, st, &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	_, err = st.EnsureCollection(context.Background(), w.Collection, 8)
	require.NoError(t, err)
	w.Index(false) // held
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 }, time.Second, 5*time.Millisecond)
	exists, _ := st.CollectionExists(context.Background(), w.Collection)
	assert.True(t, exists, "a queued recreate must not drop the collection before its turn")
	w.IndexFiles([]string{"a.go"}, nil)
	w.Index(true)
	w.IndexFiles([]string{"b.go"}, []string{"c.go"})
	close(rec.hold)
	require.Eventually(t, func() bool { s := w.Status(); return !s.Running && !s.Queued }, 2*time.Second, 5*time.Millisecond)
	calls := rec.snapshot()
	require.Len(t, calls, 2, "held full + one recreate; the file runs are absorbed")
	assert.Nil(t, calls[1].Files)
	assert.False(t, calls[0].Recreate)
	assert.True(t, calls[1].Recreate, "the recreate run drops the collection")
}

func TestVerifyResumeCatchUpAndNoAutoIndex(t *testing.T) {
	for _, tc := range []struct {
		name      string
		auto      bool
		status    index.Status
		wantRuns  int
		wantKnown bool
	}{
		{"interrupted no auto", false, index.Status{Phase: "embedding", StartedAt: time.Now()}, 0, true},
		{"finished auto", true, index.Status{Phase: "done", StartedAt: time.Now(), EndedAt: time.Now()}, 1, true}, // catch-up run
		{"interrupted auto", true, index.Status{Phase: "parsing", StartedAt: time.Now()}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.AutoIndex = tc.auto
			root := t.TempDir()
			data, _ := json.Marshal([]string{root})
			require.NoError(t, os.MkdirAll(config.HomeDir(), 0o755))
			require.NoError(t, os.WriteFile(rootsPath(), data, 0o644))
			tc.status.Root = root
			sd, _ := json.Marshal(tc.status)
			writeFile(t, filepath.Join(index.Dir(root), "index_status.json"), string(sd))
			rec := &runRecorder{}
			e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
			defer e.Close()
			e.run = rec.run
			e.Resume(context.Background())
			if tc.wantRuns > 0 {
				require.Eventually(t, func() bool { return len(rec.snapshot()) == tc.wantRuns }, 2*time.Second, 5*time.Millisecond)
			} else {
				time.Sleep(100 * time.Millisecond)
			}
			assert.Len(t, rec.snapshot(), tc.wantRuns)
			e.mu.Lock()
			_, known := e.ws[root]
			e.mu.Unlock()
			assert.Equal(t, tc.wantKnown, known)
		})
	}
	// A cancelled ctx opens nothing.
	cfg := testConfig(t)
	root := t.TempDir()
	data, _ := json.Marshal([]string{root})
	require.NoError(t, os.MkdirAll(config.HomeDir(), 0o755))
	require.NoError(t, os.WriteFile(rootsPath(), data, 0o644))
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Resume(ctx)
	e.mu.Lock()
	n := len(e.ws)
	e.mu.Unlock()
	assert.Equal(t, 0, n)
}

func TestVerifyCloseTwiceAndEnqueueAfterClose(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { e.Close(); e.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("double Close hangs")
	}
	w.Index(true)
	w.IndexFiles([]string{"x.go"}, nil)
	s := w.Status()
	assert.False(t, s.Queued)
	assert.False(t, s.Running)
	w.watch.Stop() // idempotent after Close
}

func TestVerifyWatcherRefsHiddenAndExcluded(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(root, ".git", "refs", "heads", "main"), "aaa\n")
	writeFile(t, filepath.Join(root, "node_modules", "m.go"), "func M\n")
	writeFile(t, filepath.Join(root, ".hidden", "h.go"), "func H\n")
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	_, err := e.Workspace(root)
	require.NoError(t, err)

	// Writes under excluded and hidden directories never reach a run.
	writeFile(t, filepath.Join(root, "node_modules", "m.go"), "func M2\n")
	writeFile(t, filepath.Join(root, ".hidden", "h.go"), "func H2\n")
	writeFile(t, filepath.Join(root, "ok.go"), "func OK\n")
	require.Eventually(t, func() bool {
		for _, c := range rec.snapshot() {
			if slices.Contains(c.Files, filepath.Join(root, "ok.go")) {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)
	for _, c := range rec.snapshot() {
		assert.NotNil(t, c.Files)
		for _, f := range c.Files {
			assert.NotContains(t, f, "node_modules")
			assert.NotContains(t, f, ".hidden")
		}
	}
	// A ref update the way git does it (write .lock, rename) queues a full run.
	lock := filepath.Join(root, ".git", "refs", "heads", "main.lock")
	writeFile(t, lock, "bbb\n")
	require.NoError(t, os.Rename(lock, filepath.Join(root, ".git", "refs", "heads", "main")))
	require.Eventually(t, func() bool {
		for _, c := range rec.snapshot() {
			if c.Files == nil {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "full run after a ref moved")
}

// /proc/self would expose the server's environment through /proc/self/environ.
func TestValidateRootRefusesKernelFS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux pseudo filesystems")
	}
	cfg := testConfig(t)
	link := filepath.Join(t.TempDir(), "self")
	require.NoError(t, os.Symlink("/proc/self", link))
	for _, root := range []string{"/proc/self", "/proc", "/sys/kernel", "/dev", link} {
		_, err := validateRoot(cfg, root)
		var re *RootError
		require.ErrorAs(t, err, &re, root)
		assert.Contains(t, re.Reason, "system", root)
	}
}
