package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	// Backstop: a test that forgets RAGCODE_HOME must not write index files
	// into the real ~/.ragcode.
	home, err := os.MkdirTemp("", "ragcode-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("RAGCODE_HOME", home)
	parser.Register(lineAnalyzer{})
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
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
	return index.Status{Root: o.Root, Phase: "done", StartedAt: time.Now(), EndedAt: time.Now(), Recreated: o.Recreate}, err
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

// An index made with another model of the same vector size must not answer
// semantic searches: the vectors are not comparable.
func TestSearchRefusesIndexOfAnotherModel(t *testing.T) {
	cfg := testConfig(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "func A\n")
	st := store.NewMemory()
	e := New(cfg, st, &embed.Fake{Name: "model-a"}, nil)
	w, err := e.Workspace(root)
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running && !s.Queued }, 2*time.Second, 10*time.Millisecond)
	e.Close()

	cfg.AutoIndex = false
	e2 := New(cfg, st, &embed.Fake{Name: "model-b"}, nil)
	defer e2.Close()
	w2, err := e2.Workspace(root)
	require.NoError(t, err)
	_, err = w2.Search(context.Background(), "A", store.Filter{}, 5)
	require.ErrorIs(t, err, ErrNotIndexed)
	_, err = w2.Scroll(context.Background(), store.Filter{}, 5)
	require.NoError(t, err, "name lookups do not depend on the model")
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

// A recreate that fails after its drop (Ollama down mid-run) retries as a
// full run: dropping again would throw away every file already re-indexed.
func TestFailedRecreateRetriesWithoutDropping(t *testing.T) {
	cfg := testConfig(t)
	rec := &runRecorder{errs: []error{errors.New("embed: connection refused")}}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	w.Index(true)
	require.Eventually(t, func() bool { s := w.Status(); return s.LastError != "" && !s.Running }, 2*time.Second, 5*time.Millisecond)
	e.mu.Lock()
	queued := w.queued
	e.mu.Unlock()
	assert.Equal(t, kindFull, queued)
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

	var logBuf safeBuf
	b := New(cfg, store.NewMemory(), &embed.Fake{}, slog.New(slog.NewTextHandler(&logBuf, nil)))
	wb, err := b.Workspace(root)
	require.NoError(t, err)
	wb.Index(false)
	require.Eventually(t, func() bool { return errors.Is(wb.lastErrLocked(), index.ErrLocked) }, 2*time.Second, 5*time.Millisecond)
	// Another server indexing the root is normal with several clients: no
	// WARN, and the retry stays at lockRetry instead of backing off.
	assert.NotContains(t, logBuf.String(), "level=WARN")
	assert.Contains(t, logBuf.String(), "another process is indexing")
	b.mu.Lock()
	assert.Zero(t, wb.fails)
	b.mu.Unlock()
	s := wb.Status()
	assert.Contains(t, s.LastError, "another process is indexing")
	assert.True(t, s.Queued)
	assert.True(t, s.Running) // the other engine's run holds the lock
	b.Close()                 // stops the retry timer

	close(be.release)
	require.Eventually(t, func() bool { return wa.Status().Phase == "done" }, 2*time.Second, 5*time.Millisecond)
}

type safeBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *safeBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

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
	writeFile(t, filepath.Join(index.Dir(root), "index_status.json"), string(st))

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
	gi, err := os.ReadFile(filepath.Join(root, ".ragcode", ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "*\n", string(gi))
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

// A pointer in a git-tracked instruction file would dirty the working tree
// and can block a checkout or pull: only untracked files get it.
func TestWriteIDERulesSkipsTrackedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# Agents")
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "# Mine")
	for _, args := range [][]string{{"init", "-q"}, {"add", "AGENTS.md"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	require.NoError(t, WriteIDERules(root))
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Agents", string(agents), "tracked file left alone")
	claude, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	require.NoError(t, err)
	assert.Contains(t, string(claude), IDEPointer, "untracked file gets the pointer")
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

// A workspace whose runner waits for another workspace's run to free the
// global slot is queued, not indexing: its status must not report the last
// run's numbers as a run in progress.
func TestWaitingForSlotIsQueued(t *testing.T) {
	cfg := testConfig(t)
	rec := &runRecorder{hold: make(chan struct{})}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	a, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	b, err := e.Workspace(t.TempDir())
	require.NoError(t, err)

	a.Index(false) // holds the slot, blocked in the fake
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 }, time.Second, 5*time.Millisecond)
	b.Index(false)
	time.Sleep(20 * time.Millisecond) // let b's runner reach the slot
	s := b.Status()
	assert.False(t, s.Running, "b waits for the slot")
	assert.True(t, s.Queued, "b waits for the slot")
	assert.True(t, a.Status().Running)

	close(rec.hold)
	require.Eventually(t, func() bool { s := b.Status(); return !s.Running && !s.Queued && len(rec.snapshot()) == 2 }, 2*time.Second, 5*time.Millisecond)
}

// Several server processes register their roots at once: none is lost. Each
// Engine has its own mutex, as separate processes do.
func TestRegisterRootAcrossEngines(t *testing.T) {
	cfg := testConfig(t)
	var roots []string
	var wg sync.WaitGroup
	for i := range 16 {
		root := filepath.Join(t.TempDir(), fmt.Sprint("r", i))
		roots = append(roots, root)
		e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
		t.Cleanup(e.Close)
		wg.Add(1)
		go func() { defer wg.Done(); assert.NoError(t, e.registerRoot(root)) }()
	}
	wg.Wait()
	assert.ElementsMatch(t, roots, readRoots())
}

// The end of a run is logged with its counts and duration, so the log alone
// tells how long indexing took.
func TestRunEndIsLogged(t *testing.T) {
	cfg := testConfig(t)
	var logBuf safeBuf
	e := New(cfg, store.NewMemory(), &embed.Fake{}, slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer e.Close()
	e.run = (&runRecorder{}).run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool { return strings.Contains(logBuf.String(), "engine: index run finished") }, 2*time.Second, 5*time.Millisecond)
	assert.Contains(t, logBuf.String(), "duration=")
	assert.Contains(t, logBuf.String(), "phase=done")
}

// A checkout fires many watcher events: the full run is logged once while it
// is already queued, and a run stopped by the HEAD change says so.
func TestWatcherFullRunLoggedOnce(t *testing.T) {
	cfg := testConfig(t)
	var logBuf safeBuf
	rec := &runRecorder{hold: make(chan struct{}), errs: []error{index.ErrHeadChanged}}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	w.Index(false) // run 1, blocked in the fake
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 }, time.Second, 5*time.Millisecond)
	wt := &watcher{ws: w}
	for range 5 {
		wt.full("directory removed")
	}
	assert.Equal(t, 1, strings.Count(logBuf.String(), "watcher queues a full run"))
	close(rec.hold)
	require.Eventually(t, func() bool { return strings.Contains(logBuf.String(), "engine: index run finished") }, 2*time.Second, 5*time.Millisecond)
	assert.Contains(t, logBuf.String(), "git HEAD changed during the run")
}

// A request queued between the runner's last empty read and its exit is not
// stranded: kick sees running and returns, so the runner must pick it up.
func TestRequestQueuedWhileRunnerExits(t *testing.T) {
	cfg := testConfig(t)
	rec := &runRecorder{}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	var once sync.Once
	idleHook = func() { once.Do(func() { w.IndexFiles([]string{"late.go"}, nil) }) }
	t.Cleanup(func() { idleHook = nil })
	w.Index(false)
	require.Eventually(t, func() bool {
		for _, c := range rec.snapshot() {
			if slices.Contains(c.Files, "late.go") {
				return true
			}
		}
		return false
	}, 2*time.Second, 5*time.Millisecond)
}

// A run that fails because the root was deleted is not retried: the root is
// marked gone, and the next call after it reappears catches up.
func TestRunFailingOnDeletedRootNotRetried(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	var logBuf safeBuf
	root := filepath.Join(t.TempDir(), "r")
	require.NoError(t, os.Mkdir(root, 0o755))
	rec := &runRecorder{errs: []error{errors.New("lstat " + root + ": no such file or directory")}}
	e := New(cfg, store.NewMemory(), &embed.Fake{}, slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(root)
	require.NoError(t, err)
	require.NoError(t, os.Remove(root))
	w.Index(false)
	require.Eventually(t, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return len(rec.snapshot()) == 1 && !w.running
	}, 2*time.Second, 5*time.Millisecond)
	e.mu.Lock()
	assert.Nil(t, w.retry, "no retry for a root that is gone")
	assert.True(t, w.gone)
	e.mu.Unlock()
	assert.NotContains(t, logBuf.String(), "index run failed")
	assert.Contains(t, logBuf.String(), "workspace root is gone")

	require.NoError(t, os.Mkdir(root, 0o755))
	_, err = e.Workspace(root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 2 }, 2*time.Second, 5*time.Millisecond)
}

// A run with nothing to do (queued behind a full one) leaves the counters of
// the last run that indexed something.
func TestIdleRunKeepsLastCounters(t *testing.T) {
	cfg := testConfig(t)
	cfg.AutoIndex = false
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "func A\n")
	writeFile(t, filepath.Join(root, "b.go"), "func B\n")
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	w, err := e.Workspace(root)
	require.NoError(t, err)
	idle := func() bool { s := w.Status(); return s.Phase == "done" && !s.Running && !s.Queued }
	w.Index(false)
	require.Eventually(t, idle, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, 2, w.Status().Done)
	w.Index(false)
	require.Eventually(t, idle, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, 2, w.Status().Done)
}

// A run of another process (it holds index.lock) shows as running here; a
// recorded in-progress phase with no lock holder is a run that died.
func TestStatusOfAnotherProcessRun(t *testing.T) {
	cfg := testConfig(t)
	cfg.AutoIndex = false
	root := t.TempDir()
	st, _ := json.Marshal(index.Status{Root: root, StartedAt: time.Now(), Phase: "embedding", Done: 39, Total: 260})
	writeFile(t, filepath.Join(index.Dir(root), "index_status.json"), string(st))
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	w, err := e.Workspace(root)
	require.NoError(t, err)

	s := w.Status()
	assert.False(t, s.Running)
	assert.Equal(t, "interrupted", s.Phase)

	unlock, err := index.LockFile(filepath.Join(index.Dir(root), "index.lock"))
	require.NoError(t, err)
	defer unlock()
	s = w.Status()
	assert.True(t, s.Running)
	assert.Equal(t, 39, s.Done)
	assert.Equal(t, 260, s.Total)
}

// A parent of the home directory and the broad system directories are
// refused; projects below a system directory are not.
func TestValidateRootRefusesBroadDirs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("unix paths")
	}
	cfg := testConfig(t)
	home := filepath.Join(t.TempDir(), "users", "me")
	require.NoError(t, os.MkdirAll(home, 0o755))
	t.Setenv("HOME", home)
	for root, reason := range map[string]string{filepath.Dir(home): "contains the home directory", "/etc": "system", "/usr": "system", "/usr/lib": "system"} {
		_, err := validateRoot(cfg, root)
		var re *RootError
		require.ErrorAs(t, err, &re, root)
		assert.Contains(t, re.Reason, reason, root)
	}
	proj := filepath.Join(home, "proj")
	require.NoError(t, os.MkdirAll(proj, 0o755))
	_, err := validateRoot(cfg, proj)
	assert.NoError(t, err)
	for _, l := range []string{"/bin", "/lib"} { // symlinks into /usr on merged-/usr systems
		if _, err := os.Stat(l); err == nil {
			_, err := validateRoot(cfg, l)
			var re *RootError
			require.ErrorAs(t, err, &re, l)
		}
	}
	if _, err := os.Stat("/usr/share"); err == nil {
		_, err := validateRoot(cfg, "/usr/share")
		assert.NoError(t, err, "below a system directory is allowed")
	}
}

// reopenStore reports reopens like store.SQLite does when index.db was
// deleted or replaced on disk.
type reopenStore struct {
	store.Store
	n atomic.Uint64
}

func (s *reopenStore) Reopens(string) uint64 { return s.n.Load() }

// A collection file replaced on disk (an older copy restored by hand) is
// reopened by the store; the next query queues an incremental run that diffs
// state.json against the files, instead of answering from the copy forever.
func TestQueryAfterReopenQueuesResync(t *testing.T) {
	cfg := testConfig(t)
	cfg.AutoIndex = true
	root := t.TempDir()
	st := &reopenStore{Store: store.NewMemory()}
	rec := &runRecorder{}
	e := New(cfg, st, &embed.Fake{}, nil)
	defer e.Close()
	e.run = rec.run
	w, err := e.Workspace(root)
	require.NoError(t, err)
	calls := func() int { rec.mu.Lock(); defer rec.mu.Unlock(); return len(rec.calls) }
	require.Eventually(t, func() bool { s := w.Status(); return !s.Running && !s.Queued }, 2*time.Second, 10*time.Millisecond)
	_, err = st.EnsureCollection(context.Background(), w.Collection, 8) // the run is faked: an existing index
	require.NoError(t, err)
	n := calls()
	_, _ = w.Scroll(context.Background(), store.Filter{}, 1)
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, n, calls(), "no reopen, no run")
	st.n.Add(1)
	_, _ = w.Scroll(context.Background(), store.Filter{}, 1)
	require.Eventually(t, func() bool { return calls() > n }, 2*time.Second, 10*time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Nil(t, rec.calls[n].Files, "a walk run, not a files run")
	assert.False(t, rec.calls[n].Recreate)
}
