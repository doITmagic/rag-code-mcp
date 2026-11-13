package vecdb

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ctx = context.Background()

func pt(id string, vec []float32, fields map[string][]string) Point {
	return Point{ID: id, Vector: vec, Payload: []byte("payload-" + id), Fields: fields}
}

func ids(hs []Hit) []string {
	var out []string
	for _, h := range hs {
		out = append(out, h.ID)
	}
	return out
}

func seeded(t *testing.T) (*DB, string) {
	t.Helper()
	d := New(t.TempDir())
	t.Cleanup(func() { d.Close() })
	_, err := d.Ensure(ctx, "c", 2)
	require.NoError(t, err)
	require.NoError(t, d.Upsert(ctx, "c", []Point{
		pt("a", []float32{1, 0}, map[string][]string{"kind": {"method"}, "dir": {"/w", "/w/m"}, "rel": {"x", "y"}}),
		pt("b", []float32{0, 1}, map[string][]string{"kind": {"method"}, "dir": {"/w", "/w/n"}}),
		pt("c", []float32{1, 1}, map[string][]string{"kind": {"function"}, "dir": {"/w"}, "rel": {"y"}}),
	}))
	return d, "c"
}

func TestSearchOrderScoreAndFilter(t *testing.T) {
	d, c := seeded(t)
	hits, err := d.Search(ctx, c, []float32{1, 0}, nil, 3)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c", "b"}, ids(hits))
	assert.InDelta(t, 1, hits[0].Score, 1e-5)
	assert.InDelta(t, 0.7071, hits[1].Score, 1e-3)
	assert.InDelta(t, 0, hits[2].Score, 1e-5)
	assert.Equal(t, "payload-a", string(hits[0].Payload))

	hits, err = d.Search(ctx, c, []float32{1, 0}, Cond{{Field: "kind", Values: []string{"method"}}}, 5)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, ids(hits))

	hits, err = d.Search(ctx, c, []float32{1, 0}, nil, 0)
	require.NoError(t, err)
	assert.Empty(t, hits)
}

func TestScanConditions(t *testing.T) {
	d, c := seeded(t)
	for _, tt := range []struct {
		name string
		c    Cond
		want []string
	}{
		{"all", nil, []string{"a", "b", "c"}},
		{"multi-valued field", Cond{{Field: "dir", Values: []string{"/w/m"}}}, []string{"a"}},
		{"any of values", Cond{{Field: "kind", Values: []string{"function", "method"}}}, []string{"a", "b", "c"}},
		{"and", Cond{{Field: "kind", Values: []string{"method"}}, {Field: "rel", Values: []string{"y"}}}, []string{"a"}},
		{"no values matches nothing", Cond{{Field: "kind"}}, nil},
		{"unknown field", Cond{{Field: "nope", Values: []string{"x"}}}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hits, err := d.Scan(ctx, c, tt.c, 10)
			require.NoError(t, err)
			assert.Equal(t, tt.want, ids(hits))
		})
	}
	hits, err := d.Scan(ctx, c, nil, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, ids(hits), "limit applies after ordering by id")
}

func TestUpsertReplacesPayloadVectorAndFields(t *testing.T) {
	d, c := seeded(t)
	require.NoError(t, d.Upsert(ctx, c, []Point{pt("a", []float32{0, 1}, map[string][]string{"kind": {"function"}})}))
	hits, err := d.Scan(ctx, c, Cond{{Field: "kind", Values: []string{"method"}}}, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, ids(hits), "old fields of a are gone")
	hits, err = d.Search(ctx, c, []float32{0, 1}, Cond{{Field: "kind", Values: []string{"function"}}}, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, ids(hits))
	assert.InDelta(t, 1, hits[0].Score, 1e-5)
}

func TestDelete(t *testing.T) {
	d, c := seeded(t)
	assert.ErrorIs(t, d.Delete(ctx, c, nil), errEmptyCond)
	require.NoError(t, d.Delete(ctx, c, Cond{{Field: "dir", Values: []string{"/w/m"}}}))
	hits, _ := d.Scan(ctx, c, nil, 10)
	assert.Equal(t, []string{"b", "c"}, ids(hits))
	hits, _ = d.Scan(ctx, c, Cond{{Field: "rel", Values: []string{"x"}}}, 10)
	assert.Empty(t, hits, "fields of deleted points are gone")
}

func TestZeroVectorsScoreZero(t *testing.T) {
	d := New(t.TempDir())
	defer d.Close()
	_, err := d.Ensure(ctx, "z", 2)
	require.NoError(t, err)
	require.NoError(t, d.Upsert(ctx, "z", []Point{pt("zero", []float32{0, 0}, nil), pt("one", []float32{1, 0}, nil)}))
	hits, err := d.Search(ctx, "z", []float32{1, 0}, nil, 5)
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "zero"}, ids(hits))
	assert.Equal(t, float32(0), hits[1].Score)
	hits, err = d.Search(ctx, "z", []float32{0, 0}, nil, 5)
	require.NoError(t, err)
	assert.Equal(t, []float32{0, 0}, []float32{hits[0].Score, hits[1].Score})
}

func TestCollections(t *testing.T) {
	dir := t.TempDir()
	d := New(dir)
	defer d.Close()

	ok, err := d.Exists(ctx, "c")
	require.NoError(t, err)
	assert.False(t, ok)
	_, err = d.Dim(ctx, "c")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, d.Drop(ctx, "c"), ErrNotFound)
	_, err = d.Scan(ctx, "c", nil, 1)
	assert.ErrorIs(t, err, ErrNotFound)
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries, "reads create nothing")

	rec, err := d.Ensure(ctx, "c", 4)
	require.NoError(t, err)
	assert.False(t, rec)
	rec, err = d.Ensure(ctx, "c", 4)
	require.NoError(t, err)
	assert.False(t, rec)
	require.NoError(t, d.Upsert(ctx, "c", []Point{pt("a", []float32{1, 0, 0, 0}, nil)}))

	rec, err = d.Ensure(ctx, "c", 8)
	require.NoError(t, err)
	assert.True(t, rec)
	n, err := d.Dim(ctx, "c")
	require.NoError(t, err)
	assert.Equal(t, 8, n)
	hits, _ := d.Scan(ctx, "c", nil, 10)
	assert.Empty(t, hits)

	require.NoError(t, d.Drop(ctx, "c"))
	ok, _ = d.Exists(ctx, "c")
	assert.False(t, ok)
	assert.FileExists(t, d.Path("c"), "drop keeps the file (Windows cannot delete an open one)")
	assert.ErrorIs(t, d.Drop(ctx, "c"), ErrNotFound)
	assert.ErrorIs(t, d.Upsert(ctx, "c", []Point{pt("a", make([]float32, 8), nil)}), ErrNotFound)
}

func TestVectorSizeMismatch(t *testing.T) {
	d, c := seeded(t)
	err := d.Upsert(ctx, c, []Point{pt("x", []float32{1, 2, 3}, nil)})
	assert.ErrorContains(t, err, "vector size")
	_, err = d.Search(ctx, c, []float32{1}, nil, 1)
	assert.ErrorContains(t, err, "vector size")
}

func TestInvalidNamesAndDims(t *testing.T) {
	d := New(t.TempDir())
	defer d.Close()
	for _, name := range []string{"", ".", "..", "a/b", `a\b`} {
		_, err := d.Ensure(ctx, name, 2)
		assert.ErrorContains(t, err, "invalid collection name", name)
		_, err = d.Exists(ctx, name)
		assert.Error(t, err, name)
	}
	_, err := d.Ensure(ctx, "c", 0)
	assert.ErrorContains(t, err, "invalid dimension")
}

func TestCanceledContext(t *testing.T) {
	d, c := seeded(t)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err := d.Search(cctx, c, []float32{1, 0}, nil, 1)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = d.Scan(cctx, c, nil, 1)
	assert.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, d.Upsert(cctx, c, []Point{pt("q", []float32{1, 0}, nil)}), context.Canceled)
	_, err = d.Ensure(cctx, c, 2)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPayloadBytesRoundTrip(t *testing.T) {
	d, c := seeded(t)
	big := make([]byte, 1<<20)
	for i := range big {
		big[i] = byte(i) // NUL and invalid UTF-8 included
	}
	require.NoError(t, d.Upsert(ctx, c, []Point{{ID: "big", Vector: []float32{1, 0}, Payload: big}}))
	hits, err := d.Scan(ctx, c, Cond{{Field: "missing", Values: []string{}}}, 1)
	require.NoError(t, err)
	assert.Empty(t, hits)
	all, err := d.Scan(ctx, c, nil, 10)
	require.NoError(t, err)
	for _, h := range all {
		if h.ID == "big" {
			assert.Equal(t, big, h.Payload)
			return
		}
	}
	t.Fatal("big payload not found")
}

// Two handles on one directory behave like two server processes: one
// writes batches while the other searches; the reader never sees a lock
// error and ends up seeing every committed point.
func TestTwoHandlesWriteWhileRead(t *testing.T) {
	dir := t.TempDir()
	w, r := New(dir), New(dir)
	defer w.Close()
	defer r.Close()
	_, err := w.Ensure(ctx, "c", 8)
	require.NoError(t, err)

	rnd := rand.New(rand.NewSource(1))
	vec := func() []float32 {
		v := make([]float32, 8)
		for i := range v {
			v[i] = rnd.Float32()
		}
		return v
	}
	batches := make([][]Point, 50)
	for b := range batches {
		for i := 0; i < 64; i++ {
			id := fmt.Sprintf("p%05d", b*64+i)
			batches[b] = append(batches[b], pt(id, vec(), map[string][]string{"kind": {"function"}}))
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var werr error
	go func() {
		defer wg.Done()
		for _, b := range batches {
			if err := w.Upsert(ctx, "c", b); err != nil {
				werr = err
				return
			}
		}
	}()
	q := make([]float32, 8)
	q[0] = 1
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	reads := 0
loop:
	for {
		select {
		case <-done:
			break loop
		default:
		}
		_, err := r.Search(ctx, "c", q, Cond{{Field: "kind", Values: []string{"function"}}}, 10)
		require.NoError(t, err)
		_, err = r.Scan(ctx, "c", nil, 100)
		require.NoError(t, err)
		reads++
	}
	require.NoError(t, werr)
	hits, err := r.Scan(ctx, "c", nil, 5000)
	require.NoError(t, err)
	assert.Len(t, hits, 3200)
	assert.Positive(t, reads)
}

// A drop by one handle is seen by the other as a missing collection.
func TestDropSeenByOtherHandle(t *testing.T) {
	dir := t.TempDir()
	a, b := New(dir), New(dir)
	defer a.Close()
	defer b.Close()
	_, err := a.Ensure(ctx, "c", 2)
	require.NoError(t, err)
	require.NoError(t, a.Upsert(ctx, "c", []Point{pt("x", []float32{1, 0}, nil)}))
	ok, err := b.Exists(ctx, "c")
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, a.Drop(ctx, "c"))
	ok, err = b.Exists(ctx, "c")
	require.NoError(t, err)
	assert.False(t, ok)
	_, err = b.Search(ctx, "c", []float32{1, 0}, nil, 1)
	assert.ErrorIs(t, err, ErrNotFound)
}

func BenchmarkSearch15k(b *testing.B) {
	d := New(b.TempDir())
	defer d.Close()
	_, err := d.Ensure(ctx, "c", 768)
	require.NoError(b, err)
	rnd := rand.New(rand.NewSource(1))
	var pts []Point
	for i := 0; i < 15000; i++ {
		v := make([]float32, 768)
		for j := range v {
			v[j] = rnd.Float32() - 0.5
		}
		kind := "function"
		if i%3 == 0 {
			kind = "method"
		}
		pts = append(pts, Point{ID: strconv.Itoa(i), Vector: v, Payload: make([]byte, 2000), Fields: map[string][]string{"kind": {kind}}})
		if len(pts) == 500 {
			require.NoError(b, d.Upsert(ctx, "c", pts))
			pts = nil
		}
	}
	q := make([]float32, 768)
	q[0] = 1
	b.Run("no filter", func(b *testing.B) {
		for b.Loop() {
			_, err := d.Search(ctx, "c", q, nil, 10)
			require.NoError(b, err)
		}
	})
	b.Run("kind filter", func(b *testing.B) {
		for b.Loop() {
			_, err := d.Search(ctx, "c", q, Cond{{Field: "kind", Values: []string{"method"}}}, 10)
			require.NoError(b, err)
		}
	})
}

// A collection directory deleted (or its file replaced) by someone else while
// a handle is open: the handle must not keep writing into the unlinked file.
// Reads see the collection gone and the next write lands in a new file on disk.
func TestReopensDeletedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to delete or replace a file another handle holds open, so the stale handle cannot arise")
	}
	dir := t.TempDir()
	a := New(dir)
	defer a.Close()
	_, err := a.Ensure(ctx, "c", 2)
	require.NoError(t, err)
	require.NoError(t, a.Upsert(ctx, "c", []Point{pt("x", []float32{1, 0}, nil)}))
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "c")))

	ok, err := a.Exists(ctx, "c")
	require.NoError(t, err)
	assert.False(t, ok, "deleted collection still seen through the cached handle")
	assert.Equal(t, uint64(1), a.Reopens("c"), "the reopen is reported so the caller resyncs")
	_, err = a.Ensure(ctx, "c", 2)
	require.NoError(t, err)
	require.NoError(t, a.Upsert(ctx, "c", []Point{pt("y", []float32{0, 1}, nil)}))
	b := New(dir) // a fresh process reads what is on disk
	defer b.Close()
	hs, err := b.Search(ctx, "c", []float32{0, 1}, nil, 5)
	require.NoError(t, err)
	assert.Equal(t, []string{"y"}, ids(hs))
}
