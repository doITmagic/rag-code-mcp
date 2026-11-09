package store

import (
	"context"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sym(id, qn, name string, kind parser.Kind, file string, rels ...parser.Relation) parser.Symbol {
	return parser.Symbol{
		ID: id, QualifiedName: qn, Name: name, Kind: kind, FilePath: file,
		Module: "app", Language: "go", StartLine: 1, EndLine: 2, Relations: rels,
	}
}

func seed(t *testing.T, s Store, col string) {
	t.Helper()
	ctx := context.Background()
	_, err := s.EnsureCollection(ctx, col, 2)
	require.NoError(t, err)
	require.NoError(t, s.Upsert(ctx, col, []Item{
		{Vector: []float32{1, 0}, Symbol: sym("a", "app.User.find", "find", parser.KindMethod, "/w/models/user.go",
			parser.Relation{Kind: parser.RelCall, TargetName: "query", TargetQualifiedName: "app.db.query", TargetID: "c"})},
		{Vector: []float32{0, 1}, Symbol: sym("b", "app.Order.find", "find", parser.KindMethod, "/w/models/order.go")},
		{Vector: []float32{1, 1}, Symbol: sym("c", "app.db.query", "query", parser.KindFunction, "/w/db/query.go")},
	}))
}

func ids(hits []Hit) []string {
	if len(hits) == 0 {
		return nil
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Symbol.ID
	}
	return out
}

func TestMemoryFilter(t *testing.T) {
	m := NewMemory()
	seed(t, m, "c")
	ctx := context.Background()
	tests := []struct {
		name string
		f    Filter
		want []string
	}{
		{"empty", Filter{}, []string{"a", "b", "c"}},
		{"name", Filter{Name: "find"}, []string{"a", "b"}},
		{"qualified name exact", Filter{QualifiedName: "app.User.find"}, []string{"a"}},
		{"suffix segment", Filter{QualifiedSuffix: "User.find"}, []string{"a"}},
		{"suffix leaf", Filter{QualifiedSuffix: "find"}, []string{"a", "b"}},
		{"suffix php form", Filter{QualifiedSuffix: "User::find"}, []string{"a"}},
		{"suffix not on boundary", Filter{QualifiedSuffix: "ser.find"}, nil},
		{"file", Filter{FilePath: "/w/db/query.go"}, []string{"c"}},
		{"prefix", Filter{PathPrefix: "/w/models"}, []string{"a", "b"}},
		{"kinds any-of", Filter{Kinds: []parser.Kind{parser.KindFunction, parser.KindClass}}, []string{"c"}},
		{"and of fields", Filter{Name: "find", QualifiedSuffix: "Order.find"}, []string{"b"}},
		{"prefix is a directory, not a string prefix", Filter{PathPrefix: "/w/mod"}, nil},
		{"module and language", Filter{Module: "app", Language: "go"}, []string{"a", "b", "c"}},
		{"language miss", Filter{Language: "php"}, nil},
		{"target id", Filter{TargetIDs: []string{"c"}}, []string{"a"}},
		{"target qualified name", Filter{TargetQualifiedNames: []string{"app.db.query"}}, []string{"a"}},
		{"target name", Filter{TargetNames: []string{"query"}}, []string{"a"}},
		{"target miss", Filter{TargetNames: []string{"nothing"}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits, err := m.Scroll(ctx, "c", tt.f, 10)
			require.NoError(t, err)
			assert.Equal(t, tt.want, ids(hits))
		})
	}
}

func TestMemorySearch(t *testing.T) {
	m := NewMemory()
	seed(t, m, "c")
	ctx := context.Background()

	hits, err := m.Search(ctx, "c", []float32{1, 0}, Filter{}, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c", "b"}, ids(hits))
	assert.InDelta(t, 1, hits[0].Score, 1e-6)
	assert.InDelta(t, 0, hits[2].Score, 1e-6)

	// Equal scores order by ID.
	hits, err = m.Search(ctx, "c", []float32{1, 1}, Filter{Name: "find"}, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, ids(hits))

	hits, err = m.Search(ctx, "c", []float32{1, 0}, Filter{}, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, ids(hits))

	_, err = m.Search(ctx, "c", []float32{1, 0, 0}, Filter{}, 1)
	assert.Error(t, err)
	_, err = m.Search(ctx, "missing", []float32{1, 0}, Filter{}, 1)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMemoryCollections(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()

	ok, err := m.CollectionExists(ctx, "c")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.ErrorIs(t, m.DeleteCollection(ctx, "c"), ErrNotFound)
	assert.ErrorIs(t, m.Upsert(ctx, "c", nil), ErrNotFound)

	rec, err := m.EnsureCollection(ctx, "c", 2)
	require.NoError(t, err)
	assert.False(t, rec)
	require.NoError(t, m.Upsert(ctx, "c", []Item{{Vector: []float32{1, 0}, Symbol: sym("a", "a", "a", parser.KindFunction, "/f")}}))
	assert.Error(t, m.Upsert(ctx, "c", []Item{{Vector: []float32{1}, Symbol: sym("x", "x", "x", parser.KindFunction, "/f")}}))

	rec, err = m.EnsureCollection(ctx, "c", 2)
	require.NoError(t, err)
	assert.False(t, rec)
	n, _ := count(ctx, m, "c", Filter{})
	assert.Equal(t, 1, n)

	rec, err = m.EnsureCollection(ctx, "c", 3)
	require.NoError(t, err)
	assert.True(t, rec, "dimension change drops the points")
	n, _ = count(ctx, m, "c", Filter{})
	assert.Equal(t, 0, n)

	require.NoError(t, m.DeleteCollection(ctx, "c"))
	ok, _ = m.CollectionExists(ctx, "c")
	assert.False(t, ok)
}

func TestMemoryDelete(t *testing.T) {
	m := NewMemory()
	seed(t, m, "c")
	ctx := context.Background()

	require.NoError(t, m.DeleteByFile(ctx, "c", "/w/models/user.go"))
	hits, _ := m.Scroll(ctx, "c", Filter{}, 10)
	assert.Equal(t, []string{"b", "c"}, ids(hits))

	require.NoError(t, m.DeleteByFile(ctx, "c", "/w/models/order.go"))
	hits, _ = m.Scroll(ctx, "c", Filter{}, 10)
	assert.Equal(t, []string{"c"}, ids(hits))

	// Upsert overwrites by ID.
	require.NoError(t, m.Upsert(ctx, "c", []Item{{Vector: []float32{0, 1}, Symbol: sym("c", "app.db.query2", "query2", parser.KindFunction, "/w/db/query.go")}}))
	hits, _ = m.Scroll(ctx, "c", Filter{}, 10)
	require.Len(t, hits, 1)
	assert.Equal(t, "query2", hits[0].Symbol.Name)
}

func TestMemoryScrollCapAndCtx(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	_, _ = m.EnsureCollection(ctx, "c", 1)
	items := make([]Item, MaxScroll+5)
	for i := range items {
		items[i] = Item{Vector: []float32{1}, Symbol: sym(string(rune('a'+i%26))+string(rune('a'+i/26)), "q", "q", parser.KindFunction, "/f")}
	}
	require.NoError(t, m.Upsert(ctx, "c", items))
	hits, err := m.Scroll(ctx, "c", Filter{}, MaxScroll*2)
	require.NoError(t, err)
	assert.Len(t, hits, MaxScroll)
	hits, _ = m.Scroll(ctx, "c", Filter{}, 3)
	assert.Len(t, hits, 3)

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = m.Scroll(cctx, "c", Filter{}, 3)
	assert.ErrorIs(t, err, context.Canceled)
}
