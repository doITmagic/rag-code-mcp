package store

// Independent verification probes added by the reviewer. They are kept so the
// behaviours they pin down stay pinned.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func liveQdrant(t *testing.T) Store {
	t.Helper()
	u := os.Getenv("QDRANT_URL")
	if u == "" {
		t.Skip("QDRANT_URL not set")
	}
	q, err := NewQdrant(u, os.Getenv("QDRANT_API_KEY"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	return q
}

func eachStore(t *testing.T, fn func(t *testing.T, s Store)) {
	t.Run("memory", func(t *testing.T) { fn(t, NewMemory()) })
	t.Run("sqlite", func(t *testing.T) {
		s := NewSQLite(t.TempDir())
		t.Cleanup(func() { _ = s.Close() })
		fn(t, s)
	})
	t.Run("qdrant", func(t *testing.T) { fn(t, liveQdrant(t)) })
}

func newCol(t *testing.T, s Store, dim int) string {
	t.Helper()
	col := fmt.Sprintf("ragcode-verify-%d", time.Now().UnixNano())
	_, err := s.EnsureCollection(context.Background(), col, dim)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.DeleteCollection(context.Background(), col) })
	return col
}

// PathPrefix must mean the same thing on both implementations: a directory
// ancestor, not a raw string prefix.
func TestVerifyPathPrefixSemantics(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		col := newCol(t, s, 1)
		require.NoError(t, s.Upsert(ctx, col, []Item{
			{Vector: []float32{1}, Symbol: sym("in", "m.in", "in", parser.KindFunction, "/w/models/user.go")},
			{Vector: []float32{1}, Symbol: sym("sib", "m.sib", "sib", parser.KindFunction, "/w/modelsx/user.go")},
			{Vector: []float32{1}, Symbol: sym("deep", "m.deep", "deep", parser.KindFunction, "/w/models/sub/order.go")},
		}))
		tests := []struct {
			prefix string
			want   []string
		}{
			{"/w/models", []string{"deep", "in"}},
			{"/w/models/", []string{"deep", "in"}},
			{"/w", []string{"deep", "in", "sib"}},
			{"/w/models/user", nil}, // partial file name: not a directory
			{"/w/mod", nil},         // partial directory name
		}
		for _, tt := range tests {
			t.Run(tt.prefix, func(t *testing.T) {
				hits, err := s.Scroll(ctx, col, Filter{PathPrefix: tt.prefix}, 10)
				require.NoError(t, err)
				assert.ElementsMatch(t, tt.want, ids(hits))
			})
		}
	})
}

// A QualifiedSuffix that normalizes to "" must behave the same on both.
func TestVerifyQualifiedSuffixEmptyNormalized(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		col := newCol(t, s, 2)
		seed(t, s, col)
		n, err := count(ctx, s, col, Filter{QualifiedSuffix: "::"})
		require.NoError(t, err)
		assert.Equal(t, 0, n, "a suffix that normalizes to empty matches nothing (parser.MatchQualified)")
	})
}

// DeleteCollection of a missing collection: ErrNotFound on both.
func TestVerifyDeleteMissingCollection(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		err := s.DeleteCollection(context.Background(), fmt.Sprintf("ragcode-missing-%d", time.Now().UnixNano()))
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

// Search limit 0 and negative: same on both.
func TestVerifySearchLimitEdges(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		col := newCol(t, s, 2)
		seed(t, s, col)
		hits, err := s.Search(ctx, col, []float32{1, 0}, Filter{}, 0)
		require.NoError(t, err)
		assert.Empty(t, hits)
		hits, err = s.Search(ctx, col, []float32{1, 0}, Filter{}, -1)
		require.NoError(t, err)
		assert.Empty(t, hits, "negative limit")
		_, err = s.Search(ctx, col, []float32{1, 0, 0}, Filter{}, 5)
		assert.Error(t, err, "wrong dimension is an error, not a panic")
		_, err = s.Search(ctx, col, nil, Filter{}, 5)
		assert.Error(t, err, "nil vector is an error, not a panic")
	})
}

// Scroll pages past 256 and stops at the limit.
func TestVerifyScrollPaging(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		col := newCol(t, s, 1)
		items := make([]Item, 600)
		for i := range items {
			items[i] = Item{Vector: []float32{1}, Symbol: sym(fmt.Sprintf("s%04d", i), "q", "q", parser.KindFunction, "/f")}
		}
		require.NoError(t, s.Upsert(ctx, col, items))
		hits, err := s.Scroll(ctx, col, Filter{}, MaxScroll)
		require.NoError(t, err)
		assert.Len(t, hits, 600)
		hits, err = s.Scroll(ctx, col, Filter{}, 300)
		require.NoError(t, err)
		assert.Len(t, hits, 300)
		hits, err = s.Scroll(ctx, col, Filter{}, 0)
		require.NoError(t, err)
		assert.Empty(t, hits)
		n, err := count(ctx, s, col, Filter{})
		require.NoError(t, err)
		assert.Equal(t, 600, n)
	})
}

// Payload with awkward strings must round-trip or fail cleanly, never panic,
// and must be marshalable to the wire (protobuf rejects invalid UTF-8).
func TestVerifyPayloadAwkwardStrings(t *testing.T) {
	syms := []parser.Symbol{
		{},
		{ID: "x", QualifiedName: "\xff.find", Name: "\xff", Kind: parser.KindFunction, FilePath: "/w/\xff.go", Language: "go",
			Relations: []parser.Relation{{Kind: parser.RelCall, TargetName: "\xff", TargetQualifiedName: "a.\xff", TargetID: "\xff"}},
			Metadata:  map[string]string{"\xff": "\xff"}},
		{ID: "y", QualifiedName: "...", Name: "", FilePath: "", Language: "go"},
		{ID: "z", QualifiedName: "a..b", FilePath: "rel/x.go"},
	}
	for i, s := range syms {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			p, err := toPayload(&s)
			require.NoError(t, err)
			_, err = proto.Marshal(&qdrant.PointStruct{Id: qdrant.NewIDUUID(pointID(s.ID)), Vectors: qdrant.NewVectorsDense([]float32{1}), Payload: p})
			assert.NoError(t, err, "payload must be wire-marshalable")
			got := fromPayload(p)
			assert.Equal(t, s.ID, got.ID)
		})
	}
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		col := newCol(t, s, 1)
		for i := range syms {
			err := s.Upsert(ctx, col, []Item{{Vector: []float32{1}, Symbol: syms[i]}})
			assert.NoError(t, err, "symbol %d", i)
		}
	})
}

// Memory: every method honours a cancelled ctx and is race-free.
func TestVerifyMemoryCtxAndRace(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	col := newCol(t, m, 2)
	seed(t, m, col)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err := m.EnsureCollection(cctx, col, 2)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = m.CollectionExists(cctx, col)
	assert.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, m.DeleteCollection(cctx, col), context.Canceled)
	assert.ErrorIs(t, m.Upsert(cctx, col, nil), context.Canceled)
	assert.ErrorIs(t, m.DeleteByFile(cctx, col, "/f"), context.Canceled)
	_, err = m.Search(cctx, col, []float32{1, 0}, Filter{}, 1)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = m.Scroll(cctx, col, Filter{}, 1)
	assert.ErrorIs(t, err, context.Canceled)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := fmt.Sprintf("g%d-%d", g, i)
				_ = m.Upsert(ctx, col, []Item{{Vector: []float32{1, float32(i)}, Symbol: sym(id, "q."+id, id, parser.KindFunction, "/w/"+id+".go")}})
				_, _ = m.Search(ctx, col, []float32{1, 1}, Filter{Name: id}, 3)
				_, _ = m.Scroll(ctx, col, Filter{PathPrefix: "/w"}, 10)
				_ = m.DeleteByFile(ctx, col, "/w/"+id+".go")
			}
		}(g)
	}
	wg.Wait()
}

// Unreachable endpoint: errors, never hangs past the ctx or the 30 s default.
func TestVerifyQdrantUnreachable(t *testing.T) {
	q, err := NewQdrant("http://127.0.0.1:1", "k")
	require.NoError(t, err)
	defer q.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err = q.Scroll(ctx, "c", Filter{}, 10)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	assert.Less(t, time.Since(start), 5*time.Second)

	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	start = time.Now()
	_, err = q.Scroll(cctx, "c", Filter{}, 10)
	assert.Error(t, err)
	assert.Less(t, time.Since(start), time.Second)
	_, err = q.EnsureCollection(cctx, "c", 2)
	assert.Error(t, err)
	assert.Error(t, q.Upsert(cctx, "c", []Item{{Vector: []float32{1}, Symbol: sym("a", "a", "a", parser.KindFunction, "/f")}}))
}

// Live Qdrant only: missing-collection mapping and index presence.
func TestVerifyQdrantLive(t *testing.T) {
	s := liveQdrant(t)
	ctx := context.Background()
	missing := fmt.Sprintf("ragcode-missing-%d", time.Now().UnixNano())
	_, err := s.Scroll(ctx, missing, Filter{}, 1)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.Search(ctx, missing, []float32{1}, Filter{}, 1)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, s.Upsert(ctx, missing, []Item{{Vector: []float32{1}, Symbol: sym("a", "a", "a", parser.KindFunction, "/f")}}), ErrNotFound)
	assert.ErrorIs(t, s.DeleteByFile(ctx, missing, "/f"), ErrNotFound)

	col := newCol(t, s, 2)
	q := s.(*Qdrant)
	// Indexes are created without Wait, so they show up within a moment.
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := q.c.GetCollectionInfo(ctx, col)
		require.NoError(t, err)
		missingIdx := 0
		for _, k := range indexedKeys {
			if _, ok := info.GetPayloadSchema()[k]; !ok {
				missingIdx++
			}
		}
		if missingIdx == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d payload indexes missing", missingIdx)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// A second EnsureCollection with the same dim keeps the collection.
	rec, err := s.EnsureCollection(ctx, col, 2)
	require.NoError(t, err)
	assert.False(t, rec)
}

// Live Qdrant only: a 256-point scroll page of realistic symbol bodies must
// fit the client's gRPC receive limit (grpc-go defaults to 4 MB per message),
// and a large Search result page as well.
func TestVerifyLargeContentPages(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		col := newCol(t, s, 1)
		body := strings.Repeat("x", 20<<10) // 20 KiB: a long function or doc chunk
		items := make([]Item, scrollPage)
		for i := range items {
			sy := sym(fmt.Sprintf("big%03d", i), "q", "q", parser.KindFunction, "/f")
			sy.Content = body
			items[i] = Item{Vector: []float32{1}, Symbol: sy}
		}
		require.NoError(t, s.Upsert(ctx, col, items))
		hits, err := s.Scroll(ctx, col, Filter{}, scrollPage)
		require.NoError(t, err, "scroll page over 4 MB")
		assert.Len(t, hits, scrollPage)
		hits, err = s.Search(ctx, col, []float32{1}, Filter{}, scrollPage)
		require.NoError(t, err, "search page over 4 MB")
		assert.Len(t, hits, scrollPage)
	})
}

// QualifiedSuffix parity: both stores must agree with parser.MatchQualified
// on mixed-separator names and queries.
func TestVerifyQualifiedSuffixParity(t *testing.T) {
	names := []string{`App\Models\User::find`, "github.com/x/pkg.Bar", "a.b.c", "User.find", "find", "a..b", "x->y.z"}
	queries := []string{"User.find", "find", "User::find", `Models\User::find`, "pkg.Bar", "Bar", "x/pkg.Bar", "c", "b.c", ".c", "b", "a..b", ".b", "y.z", "z", "::", "ser.find", "User", "Models"}
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		col := newCol(t, s, 1)
		items := make([]Item, len(names))
		for i, n := range names {
			items[i] = Item{Vector: []float32{1}, Symbol: sym(fmt.Sprintf("n%d", i), n, "n", parser.KindFunction, "/f")}
		}
		require.NoError(t, s.Upsert(ctx, col, items))
		for _, q := range queries {
			var want []string
			for i, n := range names {
				if parser.MatchQualified(n, q) {
					want = append(want, fmt.Sprintf("n%d", i))
				}
			}
			hits, err := s.Scroll(ctx, col, Filter{QualifiedSuffix: q}, 100)
			require.NoError(t, err, q)
			assert.ElementsMatch(t, want, ids(hits), "query %q", q)
		}
	})
}

// An empty file path must not be a wildcard: both stores must agree and
// neither may wipe the collection on an empty argument.
func TestVerifyDeleteEmptyArgIsNotWildcard(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		col := newCol(t, s, 2)
		seed(t, s, col)
		_ = s.DeleteByFile(ctx, col, "")
		n, err := count(ctx, s, col, Filter{})
		require.NoError(t, err)
		assert.Equal(t, 3, n, "DeleteByFile(\"\") must not delete everything")
	})
}

// Second verification pass: parity probes on overwrite, any-of kinds,
// relative paths, full round-trip and concurrent use of one Qdrant client.
func TestVerifyParityEdges(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		col := newCol(t, s, 2)
		full := parser.Symbol{
			ID: "full", QualifiedName: "app.Repo.Save", Name: "Save", Kind: parser.KindMethod, Module: "app", Owner: "Repo",
			Signature: "Save(ctx) error", Doc: "Saves.", Content: "func (r *Repo) Save() {}", FilePath: "/w/repo/repo.go",
			StartLine: 3, EndLine: 9, Language: "go", Exported: true,
			Relations: []parser.Relation{{Kind: parser.RelCall, TargetName: "Exec", TargetQualifiedName: "db.Exec", TargetID: "t9", Resolution: "import", Line: 5}},
			Metadata:  map[string]string{"framework": "none"},
		}
		require.NoError(t, s.Upsert(ctx, col, []Item{
			{Vector: []float32{1, 0}, Symbol: full},
			{Vector: []float32{0, 1}, Symbol: sym("rel", "x.y", "y", parser.KindClass, "x/y.go")},
			{Vector: []float32{0, 1}, Symbol: sym("iface", "app.I", "I", parser.KindInterface, "/w/repo/iface.go")},
			{Vector: []float32{1, 1}, Symbol: sym("v", "app.v", "v", parser.KindVariable, "/w/repo/sub/v.go")},
		}))

		// Overwrite by ID: one point, newest fields.
		require.NoError(t, s.Upsert(ctx, col, []Item{{Vector: []float32{1, 0}, Symbol: sym("v", "app.v2", "v2", parser.KindConstant, "/w/repo/sub/v.go")}}))
		n, err := count(ctx, s, col, Filter{})
		require.NoError(t, err)
		assert.Equal(t, 4, n)
		hits, err := s.Scroll(ctx, col, Filter{FilePath: "/w/repo/sub/v.go"}, 10)
		require.NoError(t, err)
		require.Len(t, hits, 1)
		assert.Equal(t, "v2", hits[0].Symbol.Name)
		assert.Equal(t, parser.KindConstant, hits[0].Symbol.Kind)

		// Full round trip through Search, with a relation filter.
		hits, err = s.Search(ctx, col, []float32{1, 0}, Filter{TargetName: "Exec", Kinds: []parser.Kind{parser.KindMethod, parser.KindFunction}}, 10)
		require.NoError(t, err)
		require.Len(t, hits, 1)
		assert.InDelta(t, 1, hits[0].Score, 1e-5)
		assert.Equal(t, full, hits[0].Symbol)

		tests := []struct {
			name string
			f    Filter
			want []string
		}{
			{"kinds any-of", Filter{Kinds: []parser.Kind{parser.KindClass, parser.KindInterface}}, []string{"iface", "rel"}},
			{"kinds+language+prefix", Filter{Kinds: []parser.Kind{parser.KindInterface, parser.KindConstant}, Language: "go", PathPrefix: "/w/repo"}, []string{"iface", "v"}},
			{"relative dir", Filter{PathPrefix: "x"}, []string{"rel"}},
			{"relative dot", Filter{PathPrefix: "."}, []string{"rel"}},
			{"absolute does not match relative", Filter{PathPrefix: "/x"}, nil},
			{"suffix exact full", Filter{QualifiedSuffix: "app.Repo.Save"}, []string{"full"}},
			{"suffix with slash", Filter{QualifiedSuffix: "Repo/Save"}, []string{"full"}},
			{"target qn", Filter{TargetQualifiedName: "db.Exec", TargetID: "t9"}, []string{"full"}},
			{"target id miss", Filter{TargetQualifiedName: "db.Exec", TargetID: "nope"}, nil},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				hits, err := s.Scroll(ctx, col, tt.f, 10)
				require.NoError(t, err)
				assert.ElementsMatch(t, tt.want, ids(hits))
			})
		}

		// Search limit above the match count returns everything, filtered.
		hits, err = s.Search(ctx, col, []float32{1, 1}, Filter{PathPrefix: "/w/repo"}, 50)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"full", "iface", "v"}, ids(hits))

		// Deleting one file keeps its directory sibling.
		require.NoError(t, s.DeleteByFile(ctx, col, "/w/repo/repo.go"))
		hits, err = s.Scroll(ctx, col, Filter{PathPrefix: "/w/repo"}, 10)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"iface", "v"}, ids(hits))
	})
}

// timeout adds the 30 s default only when ctx has no deadline.
func TestVerifyTimeoutDefault(t *testing.T) {
	ctx, cancel := timeout(context.Background())
	defer cancel()
	d, ok := ctx.Deadline()
	require.True(t, ok)
	assert.InDelta(t, defaultTimeout.Seconds(), time.Until(d).Seconds(), 1)

	own, ocancel := context.WithTimeout(context.Background(), time.Minute)
	defer ocancel()
	ctx2, cancel2 := timeout(own)
	defer cancel2()
	d2, _ := ctx2.Deadline()
	od, _ := own.Deadline()
	assert.Equal(t, od, d2)
}

// Live Qdrant only: one client shared by goroutines, a short ctx deadline on
// a multi-page scroll errors instead of hanging, and a second store on the
// same URL sees the same data (no client-side state).
func TestVerifyQdrantConcurrentAndDeadline(t *testing.T) {
	s := liveQdrant(t)
	ctx := context.Background()
	col := newCol(t, s, 2)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				id := fmt.Sprintf("g%d-%d", g, i)
				if err := s.Upsert(ctx, col, []Item{{Vector: []float32{1, float32(i)}, Symbol: sym(id, "q."+id, id, parser.KindFunction, "/w/"+id+".go")}}); err != nil {
					errs <- err
				}
				if _, err := s.Search(ctx, col, []float32{1, 1}, Filter{Name: id}, 3); err != nil {
					errs <- err
				}
				if _, err := s.Scroll(ctx, col, Filter{PathPrefix: "/w"}, 10); err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}
	n, err := count(ctx, s, col, Filter{})
	require.NoError(t, err)
	assert.Equal(t, 40, n)

	items := make([]Item, 600)
	for i := range items {
		items[i] = Item{Vector: []float32{1, 0}, Symbol: sym(fmt.Sprintf("p%04d", i), "q", "q", parser.KindFunction, "/p")}
	}
	require.NoError(t, s.Upsert(ctx, col, items))
	dctx, dcancel := context.WithTimeout(ctx, time.Microsecond)
	defer dcancel()
	start := time.Now()
	_, err = s.Scroll(dctx, col, Filter{}, MaxScroll)
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)

	s2, err := NewQdrant(os.Getenv("QDRANT_URL"), os.Getenv("QDRANT_API_KEY"))
	require.NoError(t, err)
	defer s2.Close()
	n, err = count(ctx, s2, col, Filter{})
	require.NoError(t, err)
	assert.Equal(t, 640, n)
}
