package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/index"
	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lineAnalyzer makes every "func NAME" line of a .go file a function symbol.
type lineAnalyzer struct{}

func (lineAnalyzer) Language() string     { return "go" }
func (lineAnalyzer) Extensions() []string { return []string{".go"} }
func (lineAnalyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	var syms []parser.Symbol
	for i, line := range strings.Split(string(f.Source), "\n") {
		if name, ok := strings.CutPrefix(line, "func "); ok {
			syms = append(syms, parser.Symbol{Name: name, Kind: parser.KindFunction, Module: "m", Content: line, StartLine: i + 1})
		}
	}
	return parser.Finalize(f, syms), nil
}

func TestMain(m *testing.M) {
	parser.Register(lineAnalyzer{})
	os.Exit(m.Run())
}

// blockingEmbedder holds every Embed call until release is closed.
type blockingEmbedder struct {
	embed.Fake
	release chan struct{}
}

func (b *blockingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return b.Fake.Embed(ctx, texts)
}

func testConfig(t *testing.T) config.Config {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.WatchEnabled = false
	cfg.WatchDebounce = 50 * time.Millisecond
	return cfg
}

// runRecorder is a fake index.Run that records each call's options; hold is
// closed by the test to let the first call return.
type runRecorder struct {
	mu    sync.Mutex
	calls []index.Options
	hold  chan struct{}
	errs  []error // returned in order; nil past the end
}

func (r *runRecorder) run(ctx context.Context, o index.Options) (index.Status, error) {
	r.mu.Lock()
	n := len(r.calls)
	r.calls = append(r.calls, o)
	var err error
	if n < len(r.errs) {
		err = r.errs[n]
	}
	r.mu.Unlock()
	if n == 0 && r.hold != nil {
		select {
		case <-r.hold:
		case <-ctx.Done():
			return index.Status{}, ctx.Err()
		}
	}
	return index.Status{Root: o.Root, Phase: "done", StartedAt: time.Now(), EndedAt: time.Now()}, err
}

func (r *runRecorder) snapshot() []index.Options {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]index.Options(nil), r.calls...)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestWorkspaceValidation(t *testing.T) {
	cfg := testConfig(t)
	good := t.TempDir()
	file := filepath.Join(good, "f.txt")
	writeFile(t, file, "x")
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(good, link))
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	allowed := cfg
	allowed.AllowedWorkspacePaths = []string{good}

	tests := []struct {
		name, root string
		cfg        config.Config
		wantRoot   string
		wantReason string
	}{
		{"ok", good, cfg, good, ""},
		{"symlink resolved", link, cfg, good, ""},
		{"trailing slash", good + "/", cfg, good, ""},
		{"empty", "", cfg, "", "is empty"},
		{"relative", "foo/bar", cfg, "", "is not absolute"},
		{"missing", filepath.Join(good, "nope"), cfg, "", "does not exist"},
		{"file", file, cfg, "", "is not a directory"},
		{"fs root", "/", cfg, "", "is a filesystem root"},
		{"home", home, cfg, "", "is the home directory"},
		{"temp", os.TempDir(), cfg, "", "is the temp directory"},
		{"inside allowed", filepath.Join(good), allowed, good, ""},
		{"outside allowed", t.TempDir(), allowed, "", "is outside allowed_workspace_paths"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := New(tc.cfg, store.NewMemory(), &embed.Fake{}, nil)
			defer e.Close()
			w, err := e.Workspace(tc.root)
			if tc.wantReason != "" {
				var re *RootError
				require.ErrorAs(t, err, &re)
				assert.Contains(t, re.Reason, tc.wantReason)
				assert.Contains(t, err.Error(), "workspace_root")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantRoot, w.Root)
			assert.Regexp(t, `^ragcode-[0-9a-f]{16}$`, w.Collection)
			again, err := e.Workspace(tc.root)
			require.NoError(t, err)
			assert.Same(t, w, again)
			assert.Equal(t, []string{tc.wantRoot}, readRoots())
		})
	}
}

func TestFirstContactAutoIndex(t *testing.T) {
	cfg := testConfig(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "func A\nfunc B\n")
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	w, err := e.Workspace(root)
	require.NoError(t, err)

	_, err = w.Search(context.Background(), "A", store.Filter{}, 5)
	require.ErrorIs(t, err, ErrNotIndexed)
	require.Eventually(t, func() bool {
		s := w.Status()
		return s.Phase == "done" && !s.Running && s.LastError == ""
	}, 2*time.Second, 10*time.Millisecond)
	all, err := w.Scroll(context.Background(), store.Filter{}, 10)
	require.NoError(t, err)
	assert.Len(t, all, 2)
	hits, err := w.Search(context.Background(), "A", store.Filter{Name: "A"}, 5)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, 1, w.Status().Done)

	ok, msg := w.Ready(context.Background())
	assert.True(t, ok, msg)
	_, err = w.Scroll(context.Background(), store.Filter{}, 10)
	require.NoError(t, err)

	noAuto := cfg
	noAuto.AutoIndex = false
	e2 := New(noAuto, store.NewMemory(), &embed.Fake{}, nil)
	defer e2.Close()
	w2, err := e2.Workspace(root)
	require.NoError(t, err)
	_, err = w2.Scroll(context.Background(), store.Filter{}, 10)
	require.ErrorIs(t, err, ErrNotIndexed)
	assert.False(t, w2.Status().Queued)
	ok, msg = w2.Ready(context.Background())
	assert.False(t, ok)
	assert.Contains(t, msg, "rag_index_workspace")
}

func TestCoalescing(t *testing.T) {
	cfg := testConfig(t)
	rec := &runRecorder{hold: make(chan struct{})}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)

	w.Index(false) // run 1, blocked in the fake
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 }, time.Second, 5*time.Millisecond)
	assert.True(t, w.Status().Running)
	w.IndexFiles([]string{"a.go"}, nil)
	w.IndexFiles([]string{"b.go"}, []string{"c.go"})
	assert.True(t, w.Status().Queued)
	w.Index(false) // absorbs the file runs
	w.IndexFiles([]string{"d.go"}, nil)
	close(rec.hold)
	require.Eventually(t, func() bool { s := w.Status(); return !s.Running && !s.Queued }, 2*time.Second, 5*time.Millisecond)
	calls := rec.snapshot()
	require.Len(t, calls, 2)
	assert.Nil(t, calls[0].Files)
	assert.Nil(t, calls[1].Files)

	w.IndexFiles([]string{"a.go"}, nil)
	w.IndexFiles([]string{"b.go"}, []string{"c.go"})
	require.Eventually(t, func() bool { return len(rec.snapshot()) >= 3 && !w.Status().Running }, 2*time.Second, 5*time.Millisecond)
	var files, deleted []string
	for _, c := range rec.snapshot()[2:] { // one run, or two when the second IndexFiles landed after run 3 started
		files = append(files, c.Files...)
		deleted = append(deleted, c.Deleted...)
	}
	assert.ElementsMatch(t, []string{"a.go", "b.go"}, files)
	assert.Equal(t, []string{"c.go"}, deleted)
}

func TestHeadChangedRequeue(t *testing.T) {
	cfg := testConfig(t)
	rec := &runRecorder{errs: []error{index.ErrHeadChanged}}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	w.IndexFiles([]string{"a.go"}, nil)
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 2 && !w.Status().Running }, 2*time.Second, 5*time.Millisecond)
	calls := rec.snapshot()
	assert.Equal(t, []string{"a.go"}, calls[0].Files)
	assert.Nil(t, calls[1].Files)
	assert.Empty(t, w.Status().LastError)
}

// A run that fails (embedder or store outage) keeps its files queued behind
// the retry timer instead of dropping them.
func TestFailedRunIsRetried(t *testing.T) {
	cfg := testConfig(t)
	rec := &runRecorder{errs: []error{errors.New("embed: connection refused")}}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	w.IndexFiles([]string{"a.go"}, nil)
	require.Eventually(t, func() bool { s := w.Status(); return s.LastError != "" && !s.Running }, 2*time.Second, 5*time.Millisecond)
	e.mu.Lock()
	armed, files := w.retry != nil, w.files
	e.mu.Unlock()
	assert.True(t, armed, "retry timer armed")
	assert.True(t, files["a.go"], "failed file still queued")
}

// auto_index: false starts no watcher, so no edit can trigger a first index.
func TestNoWatcherWithoutAutoIndex(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled, cfg.AutoIndex = true, false
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	assert.Nil(t, w.watch)
}

func TestRecreateDropsCollection(t *testing.T) {
	cfg := testConfig(t)
	st := store.NewMemory()
	rec := &runRecorder{}
	e := New(cfg, st, &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	w.Index(true)
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 && !w.Status().Running }, 2*time.Second, 5*time.Millisecond)
	assert.True(t, rec.snapshot()[0].Recreate, "index.Run drops the collection under its lock")
}

// An embedding model with another vector size makes searches answer
// ErrNotIndexed and queue the run that recreates the collection.
func TestEmbedModelChangeReindexes(t *testing.T) {
	cfg := testConfig(t)
	st := store.NewMemory()
	rec := &runRecorder{}
	e := New(cfg, st, &embed.Fake{Dim: 16}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	_, err = st.EnsureCollection(context.Background(), w.Collection, 8) // built by the old model
	require.NoError(t, err)
	_, err = w.Search(context.Background(), "x", store.Filter{}, 3)
	require.ErrorIs(t, err, ErrNotIndexed)
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 }, 2*time.Second, 5*time.Millisecond)
	assert.Nil(t, rec.snapshot()[0].Files)
}

func TestLockedByOtherEngine(t *testing.T) {
	cfg := testConfig(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "func A\n")
	be := &blockingEmbedder{release: make(chan struct{})}
	a := New(cfg, store.NewMemory(), be, nil)
	defer a.Close()
	wa, err := a.Workspace(root)
	require.NoError(t, err)
	wa.Index(false)
	require.Eventually(t, func() bool { p := wa.Status().Phase; return p == "parsing" || p == "embedding" }, 2*time.Second, 5*time.Millisecond)

	b := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	wb, err := b.Workspace(root)
	require.NoError(t, err)
	wb.Index(false)
	require.Eventually(t, func() bool { return errors.Is(wb.lastErrLocked(), index.ErrLocked) }, 2*time.Second, 5*time.Millisecond)
	s := wb.Status()
	assert.Contains(t, s.LastError, "another process is indexing")
	assert.True(t, s.Queued)
	assert.False(t, s.Running)
	b.Close() // stops the retry timer

	close(be.release)
	require.Eventually(t, func() bool { return wa.Status().Phase == "done" }, 2*time.Second, 5*time.Millisecond)
}

func (w *Workspace) lastErrLocked() error {
	w.e.mu.Lock()
	defer w.e.mu.Unlock()
	return w.lastErr
}

func TestResume(t *testing.T) {
	cfg := testConfig(t)
	root := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")
	data, _ := json.Marshal([]string{gone, root})
	require.NoError(t, os.MkdirAll(config.HomeDir(), 0o755))
	require.NoError(t, os.WriteFile(rootsPath(), data, 0o644))
	st, _ := json.Marshal(index.Status{Root: root, StartedAt: time.Now(), Phase: "embedding"})
	writeFile(t, filepath.Join(root, ".ragcode", "index_status.json"), string(st))

	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	e.Resume(context.Background())
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 }, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, root, rec.snapshot()[0].Root)
	assert.Nil(t, rec.snapshot()[0].Files)
	_, ok := e.ws[gone]
	assert.False(t, ok)
}

func TestCloseLeavesNoGoroutines(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "func A\n")
	before := runtime.NumGoroutine()
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	w, err := e.Workspace(root)
	require.NoError(t, err)
	w.Index(false)
	writeFile(t, filepath.Join(root, "b.go"), "func B\n")
	e.Close()
	w.Index(false) // no-op after Close
	_, err = e.Workspace(root)
	assert.ErrorIs(t, err, ErrClosed)
	// Polled by hand: assert.Eventually runs its condition on its own goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	assert.LessOrEqual(t, runtime.NumGoroutine(), before)
}

func TestWriteIDERules(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".cursor", "rules"), 0o755))
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "# Project")
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# Agents")
	require.NoError(t, WriteIDERules(root))
	require.NoError(t, WriteIDERules(root)) // idempotent
	md, err := os.ReadFile(filepath.Join(root, ".ragcode", "RAGCODE.md"))
	require.NoError(t, err)
	assert.Contains(t, string(md), "workspace_root")
	claude, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Project\n"+IDEPointer+"\n", string(claude))
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Agents\n"+IDEPointer+"\n", string(agents))
	assert.NoFileExists(t, filepath.Join(root, "GEMINI.md"), "absent instruction files are never created")
	mdc, err := os.ReadFile(filepath.Join(root, ".cursor", "rules", "ragcode.mdc"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(mdc), IDEPointer))
	assert.True(t, strings.HasPrefix(string(mdc), "---\n"))

	bare := t.TempDir()
	require.NoError(t, WriteIDERules(bare))
	_, err = os.Stat(filepath.Join(bare, "CLAUDE.md"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(bare, ".cursor"))
	assert.True(t, os.IsNotExist(err))
}

// A repo-committed symlink must never redirect the IDE rules writes.
func TestWriteIDERulesSymlinks(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	victim := filepath.Join(out, "victim")
	writeFile(t, victim, "keep")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".ragcode"), 0o755))
	require.NoError(t, os.Symlink(victim, filepath.Join(root, ".ragcode", "RAGCODE.md")))
	require.NoError(t, os.Symlink(victim, filepath.Join(root, "CLAUDE.md")))
	require.Error(t, WriteIDERules(root))
	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))
	info, err := os.Lstat(filepath.Join(root, ".ragcode", "RAGCODE.md"))
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular(), "the symlink was replaced by our file")
}

// One refused file must not stop the pointer reaching the others.
func TestWriteIDERulesKeepsGoingAfterSymlink(t *testing.T) {
	root, victim := t.TempDir(), filepath.Join(t.TempDir(), "victim")
	writeFile(t, victim, "keep")
	require.NoError(t, os.Symlink(victim, filepath.Join(root, "CLAUDE.md")))
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# Agents")
	require.Error(t, WriteIDERules(root))
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Agents\n"+IDEPointer+"\n", string(agents))
}

// Consecutive failures back off (doubling, capped) and a successful run
// resets the counter, so a workspace that fails every time does not take
// the slot every 30 s.
func TestFailedRunBacksOff(t *testing.T) {
	assert.Greater(t, retryDelay(1), retryDelay(0))
	assert.Equal(t, 10*time.Minute, retryDelay(20))

	cfg := testConfig(t)
	rec := &runRecorder{errs: []error{errors.New("boom"), errors.New("boom")}}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	fails := func() int { e.mu.Lock(); defer e.mu.Unlock(); return w.fails }
	fire := func() { // run the pending retry now instead of after its delay
		e.mu.Lock()
		tm := w.retry
		e.mu.Unlock()
		require.NotNil(t, tm)
		tm.Reset(0)
	}
	w.IndexFiles([]string{"a.go"}, nil)
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 && fails() == 1 }, 2*time.Second, 5*time.Millisecond)
	fire()
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 2 && fails() == 2 }, 2*time.Second, 5*time.Millisecond)
	fire()
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 3 && fails() == 0 && !w.Status().Running }, 2*time.Second, 5*time.Millisecond)
}

// A cloned repo may make .ragcode, .github or .cursor a symlink to a directory
// outside it; nothing may be written there.
func TestWriteIDERulesSymlinkedParentDirs(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(out, "rules"), 0o755))
	writeFile(t, filepath.Join(out, "copilot-instructions.md"), "keep")
	for _, d := range []string{".ragcode", ".github", ".cursor"} {
		require.NoError(t, os.Symlink(out, filepath.Join(root, d)))
	}
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# Agents")
	require.Error(t, WriteIDERules(root))

	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{"rules", "copilot-instructions.md"}, names, "nothing new outside the root")
	data, _ := os.ReadFile(filepath.Join(out, "copilot-instructions.md"))
	assert.Equal(t, "keep", string(data))
	rules, _ := os.ReadDir(filepath.Join(out, "rules"))
	assert.Empty(t, rules)
	agents, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	assert.Contains(t, string(agents), IDEPointer, "files inside the root still get the pointer")
}
