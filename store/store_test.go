package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreContract runs the same behaviour checks on every Store.
func TestStoreContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) { storeContract(t, NewMemory()) })
	t.Run("sqlite", func(t *testing.T) { s := NewSQLite(t.TempDir()); defer s.Close(); storeContract(t, s) })
}

func storeContract(t *testing.T, s Store) {
	ctx := context.Background()
	col := fmt.Sprintf("ragcode-test-%d", time.Now().UnixNano())
	defer func() { _ = s.DeleteCollection(ctx, col) }()

	ok, err := s.CollectionExists(ctx, col)
	require.NoError(t, err)
	assert.False(t, ok)

	seed(t, s, col)
	ok, err = s.CollectionExists(ctx, col)
	require.NoError(t, err)
	assert.True(t, ok)

	// Same dimension keeps data, another dimension recreates.
	rec, err := s.EnsureCollection(ctx, col, 2)
	require.NoError(t, err)
	assert.False(t, rec)
	n, err := count(ctx, s, col, Filter{})
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	tests := []struct {
		name string
		f    Filter
		want []string
	}{
		{"suffix", Filter{QualifiedSuffix: "User::find"}, []string{"a"}},
		{"leaf suffix", Filter{QualifiedSuffix: "find"}, []string{"a", "b"}},
		{"name and prefix", Filter{Name: "find", PathPrefix: "/w/models"}, []string{"a", "b"}},
		{"kinds", Filter{Kinds: []parser.Kind{parser.KindFunction}}, []string{"c"}},
		{"target id", Filter{TargetIDs: []string{"c"}}, []string{"a"}},
		{"target qualified name", Filter{TargetQualifiedNames: []string{"app.db.query"}}, []string{"a"}},
		{"target name", Filter{TargetNames: []string{"query"}}, []string{"a"}},
		{"target owner", Filter{TargetOwners: []string{"app.db"}}, []string{"a"}},
		{"module+language", Filter{Module: "app", Language: "go"}, []string{"a", "b", "c"}},
		{"miss", Filter{Name: "Nobody"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits, err := s.Scroll(ctx, col, tt.f, 10)
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.want, ids(hits))
		})
	}

	hits, err := s.Search(ctx, col, []float32{1, 0}, Filter{}, 2)
	require.NoError(t, err)
	require.Len(t, hits, 2)
	assert.Equal(t, "a", hits[0].Symbol.ID)
	assert.InDelta(t, 1, hits[0].Score, 1e-5)
	assert.Equal(t, "c", hits[1].Symbol.ID)
	assert.Equal(t, "app.User.find", hits[0].Symbol.QualifiedName)
	assert.Equal(t, "c", hits[0].Symbol.Relations[0].TargetID, "relations survive the round trip")

	hits, err = s.Search(ctx, col, []float32{1, 0}, Filter{Kinds: []parser.Kind{parser.KindFunction}}, 5)
	require.NoError(t, err)
	assert.Equal(t, []string{"c"}, ids(hits))

	require.NoError(t, s.DeleteByFile(ctx, col, "/w/models/user.go"))
	n, _ = count(ctx, s, col, Filter{})
	assert.Equal(t, 2, n)
	require.NoError(t, s.DeleteByFile(ctx, col, "/w/models/order.go"))
	hits, _ = s.Scroll(ctx, col, Filter{}, 10)
	assert.Equal(t, []string{"c"}, ids(hits))

	rec, err = s.EnsureCollection(ctx, col, 3)
	require.NoError(t, err)
	assert.True(t, rec)
	n, _ = count(ctx, s, col, Filter{})
	assert.Equal(t, 0, n)

	require.NoError(t, s.DeleteCollection(ctx, col))
	_, err = s.Scroll(ctx, col, Filter{}, 1)
	assert.ErrorIs(t, err, ErrNotFound)
}

// count is len(Scroll): exact only below MaxScroll, which every test stays under.
func count(ctx context.Context, s Store, col string, f Filter) (int, error) {
	hits, err := s.Scroll(ctx, col, f, MaxScroll)
	return len(hits), err
}

func TestQnSuffixesAndDirParts(t *testing.T) {
	assert.Equal(t, []string{"a.b.c", "b.c", "c"}, qnSuffixes("a.b.c"))
	assert.Equal(t, []string{"c"}, qnSuffixes("c"))
	assert.Nil(t, qnSuffixes(""))
	// Directories use the OS separator, as FilePath and the cleaned PathPrefix do.
	assert.Equal(t, []string{filepath.FromSlash("/w/models"), filepath.FromSlash("/w"), filepath.FromSlash("/")}, dirParts("/w/models/user.go"))
	assert.Equal(t, []string{filepath.FromSlash("/")}, dirParts("/user.go"))
	assert.Equal(t, []string{"x", "."}, dirParts("x/y.go"))
}
