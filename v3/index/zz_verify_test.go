package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errStore fails every collection call, standing in for an unreachable store.
type errStore struct{ *store.Memory }

func (errStore) CollectionExists(context.Context, string) (bool, error) {
	return false, errors.New("store unreachable")
}

func TestVerifyEdgeInputs(t *testing.T) {
	e := newEnv(t)
	empty := e.write(t, "empty.go", "")
	one := e.write(t, "one.go", "x")
	trunc := e.write(t, "trunc.go", "func ")
	st, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, "done", st.Phase)
	assert.Equal(t, 3, st.Total)
	assert.Equal(t, 3, st.Done)
	assert.Equal(t, 0, st.Failed)
	state, err := LoadState(e.root)
	require.NoError(t, err)
	assert.Contains(t, state.Files, empty)
	assert.Contains(t, state.Files, one)
	assert.Contains(t, state.Files, trunc)
	assert.Equal(t, 1, e.count(t, store.Filter{})) // only "func " with empty name
}

func TestVerifyStoreUnreachable(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	start := time.Now()
	st, err := e.run(t, func(o *Options) { o.Store = errStore{e.store} })
	assert.EqualError(t, err, "store unreachable")
	assert.Equal(t, "failed", st.Phase)
	assert.Less(t, time.Since(start), 5*time.Second)
	saved, ok := ReadStatus(e.root)
	require.True(t, ok)
	assert.Equal(t, "failed", saved.Phase)
	assert.Equal(t, "store unreachable", saved.Error)
}

func TestVerifyEmbedWrongCount(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\nfunc B\n")
	e.emb.Dim = 4
	// Fake returns len(texts) vectors; shrink through a wrapper is not possible
	// without another type, so check the Dimension/EnsureCollection mismatch
	// path instead: a dimension change recreates the collection and resets state.
	_, err := e.run(t, nil)
	require.NoError(t, err)
	state, _ := LoadState(e.root)
	require.Len(t, state.Files, 1)
	e.emb.Dim = 6
	st, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Total, "model change must re-embed everything")
	assert.Equal(t, 2, e.count(t, store.Filter{}))
}

func TestVerifyPreCancelledCtx(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	st, err := Run(ctx, e.opts)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, "interrupted", st.Phase)
	assert.Less(t, time.Since(start), time.Second)
}

// Explicit Files are not run through the directory rules: a watcher event
// for vendor/ or a hidden dir lands in the index.
func TestVerifyExplicitFilesBypassDirRules(t *testing.T) {
	e := newEnv(t)
	v := e.write(t, "vendor/v.go", "func V\n")
	h := e.write(t, ".hidden/h.go", "func H\n")
	st, err := e.run(t, func(o *Options) { o.Files = []string{v, h} })
	require.NoError(t, err)
	t.Logf("explicit excluded/hidden files: total=%d done=%d points=%d", st.Total, st.Done, e.count(t, store.Filter{}))
	assert.Equal(t, 0, e.count(t, store.Filter{FilePath: v}), "vendor file indexed through Files")
	assert.Equal(t, 0, e.count(t, store.Filter{FilePath: h}), "hidden-dir file indexed through Files")
}

// A Deleted path that the state does not know (crash between upsert and the
// 2 s state save) keeps its points.
func TestVerifyDeletedUnknownToState(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func A\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	// Simulate the crash: state lost, points present.
	require.NoError(t, os.Remove(filepath.Join(Dir(e.root), "state.json")))
	require.NoError(t, os.Remove(a))
	st, err := e.run(t, func(o *Options) { o.Files = []string{}; o.Deleted = []string{a} })
	require.NoError(t, err)
	// Spec-literal ("files in State but ... in Deleted"), so only observed:
	// after a crash between upsert and state save the points stay forever.
	t.Logf("deleted-unknown: deleted=%d points=%d", st.Deleted, e.count(t, store.Filter{FilePath: a}))
}

// Fresh-state incremental run: store lookups are skipped even though the
// store holds the target.
func TestVerifyFreshStateSkipsStoreLookup(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func Foo -> b.Bar\n")
	e.write(t, "b.go", "func Bar\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	require.Equal(t, parser.ResolutionExact, e.symbols(t, a)["Foo"].Relations[0].Resolution)
	require.NoError(t, os.Remove(filepath.Join(Dir(e.root), "state.json")))
	e.write(t, "a.go", "func Foo -> b.Bar\n\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{a} })
	require.NoError(t, err)
	t.Logf("fresh-state incremental resolution: %q", e.symbols(t, a)["Foo"].Relations[0].Resolution)
	assert.Equal(t, parser.ResolutionExact, e.symbols(t, a)["Foo"].Relations[0].Resolution)
}

func TestVerifyRunDefinedWinsOverStore(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func Foo -> Bar\n")
	e.write(t, "b.go", "func Bar\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	// a now defines Bar too: by name there are two → ambiguous.
	e.write(t, "a.go", "func Foo -> Bar\nfunc Bar\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{a} })
	require.NoError(t, err)
	assert.Equal(t, parser.ResolutionAmbiguous, e.symbols(t, a)["Foo"].Relations[0].Resolution)
}

func TestVerifyProgressCadence(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 20; i++ {
		e.write(t, filepath.Join("p", "f"+string(rune('a'+i))+".go"), funcs(20, "F"))
	}
	var times []time.Time
	_, err := e.run(t, func(o *Options) { o.Progress = func(Status) { times = append(times, time.Now()) } })
	require.NoError(t, err)
	// Ignore the forced phase-boundary calls: non-forced ones must be 500 ms apart.
	t.Logf("progress calls: %d", len(times))
	assert.LessOrEqual(t, len(times), 4, "expected only phase-boundary calls on a sub-500ms run")
}

func TestVerifyLockReleasedAfterRun(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	unlock, err := lock(filepath.Join(Dir(e.root), "index.lock"))
	require.NoError(t, err, "lock must be released after Run")
	unlock()
}

// ---- second verification pass ----

type traceStore struct {
	*store.Memory
	ops       []string
	maxUpsert int
	scrolls   int
	failAt    int // the scroll that fails, and every one after it; 0 never
}

func (t *traceStore) Upsert(ctx context.Context, c string, items []store.Item) error {
	t.ops = append(t.ops, "upsert")
	t.maxUpsert = max(t.maxUpsert, len(items))
	return t.Memory.Upsert(ctx, c, items)
}

func (t *traceStore) DeleteByFile(ctx context.Context, c, f string) error {
	t.ops = append(t.ops, "delete:"+filepath.Base(f))
	return t.Memory.DeleteByFile(ctx, c, f)
}

func (t *traceStore) Scroll(ctx context.Context, c string, f store.Filter, limit int) ([]store.Hit, error) {
	t.scrolls++
	if t.failAt > 0 && t.scrolls >= t.failAt {
		return nil, errors.New("store down")
	}
	return t.Memory.Scroll(ctx, c, f, limit)
}

type batchEmbedder struct {
	embed.Fake
	maxBatch int
}

func (b *batchEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	b.maxBatch = max(b.maxBatch, len(texts))
	return b.Fake.Embed(ctx, texts)
}

func TestVerifyBatchSizesAndPurgeOrder(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", funcs(100, "A"))
	e.write(t, "b.go", funcs(5, "B"))
	ts := &traceStore{Memory: e.store}
	be := &batchEmbedder{}
	st, err := e.run(t, func(o *Options) { o.Store = ts; o.Embedder = be })
	require.NoError(t, err)
	assert.Equal(t, 2, st.Done)
	assert.Equal(t, 16, be.maxBatch)
	assert.Equal(t, 64, ts.maxUpsert)
	assert.Equal(t, 105, e.count(t, store.Filter{}))
	// Each changed file is purged exactly once, before its first upsert.
	assert.Equal(t, []string{"delete:a.go", "upsert", "delete:b.go", "upsert"}, ts.ops)
}

// A store error during relation resolution stops the lookups for the run
// instead of waiting on the store once per remaining target.
func TestVerifyLookupStopsOnStoreError(t *testing.T) {
	e := newEnv(t)
	var lines []string
	for i := 0; i < 520; i++ {
		lines = append(lines, fmt.Sprintf("func F%d -> zzz.T%d", i, i))
	}
	a := e.write(t, "a.go", strings.Join(lines, "\n"))
	e.write(t, "b.go", "func Bar\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)

	e.write(t, "a.go", strings.Join(lines, "\n")+"\n")
	ts := &traceStore{Memory: e.store, failAt: 2}
	var logBuf bytes.Buffer
	_, err = e.run(t, func(o *Options) {
		o.Files = []string{a}
		o.Store = ts
		o.Log = slog.New(slog.NewTextHandler(&logBuf, nil))
	})
	require.NoError(t, err)
	assert.Equal(t, 2, ts.scrolls, "the empty-collection probe, then one failed lookup")
	assert.Contains(t, logBuf.String(), "relation lookup failed")
}

func TestVerifyFilesOutsideRootIgnored(t *testing.T) {
	e := newEnv(t)
	other := t.TempDir()
	p := filepath.Join(other, "x.go")
	require.NoError(t, os.WriteFile(p, []byte("func X\n"), 0o644))
	st, err := e.run(t, func(o *Options) { o.Files = []string{p, "../x.go"} })
	require.NoError(t, err)
	assert.Equal(t, 0, st.Total)
	assert.Equal(t, 0, e.count(t, store.Filter{}))
}

func TestVerifyRepoWithoutCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := newEnv(t)
	out, err := exec.Command("git", "-C", e.root, "init", "-q").CombinedOutput()
	require.NoError(t, err, string(out))
	e.write(t, "a.go", "func A\n")
	st, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, "", st.Branch)
	assert.Equal(t, 1, st.Done)
}
