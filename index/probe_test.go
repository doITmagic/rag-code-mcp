package index

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countEmbedder counts the calls that start the real embedder (llama-server):
// the vector size probe and the embedding itself.
type countEmbedder struct {
	embed.Fake
	dims, embeds atomic.Int32
}

func (c *countEmbedder) Dimension(ctx context.Context) (int, error) {
	c.dims.Add(1)
	return c.Fake.Dimension(ctx)
}

func (c *countEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	c.embeds.Add(1)
	return c.Fake.Embed(ctx, texts)
}

// noSizeStore cannot tell the vector size of a collection.
type noSizeStore struct{ store.Store }

func (noSizeStore) VectorSize(context.Context, string) (int, error) {
	return 0, errors.New("size unreadable")
}

// indexed runs the first, full run and returns the env, so a test can start a
// second run as a restarted server would: a fresh embedder on the same index.
func indexed(t *testing.T) (*env, string) {
	t.Helper()
	e := newEnv(t)
	a := e.write(t, "a.go", "func A\n")
	e.write(t, "b.go", "func B\n")
	first := &countEmbedder{}
	_, err := e.run(t, func(o *Options) { o.Embedder = first })
	require.NoError(t, err)
	assert.EqualValues(t, 1, first.dims.Load(), "an empty state has nothing to take the size from: probed")
	assert.NotZero(t, first.embeds.Load())
	return e, a
}

// A server start on an indexed, unchanged workspace must not load the model:
// no vector size probe and nothing to embed.
func TestRunUnchangedDoesNotStartEmbedder(t *testing.T) {
	e, a := indexed(t)

	full := &countEmbedder{}
	st, err := e.run(t, func(o *Options) { o.Embedder = full })
	require.NoError(t, err)
	assert.Zero(t, st.Total)
	assert.Zero(t, full.dims.Load(), "full run: the size is the stored collection's")
	assert.Zero(t, full.embeds.Load())

	files := &countEmbedder{}
	st, err = e.run(t, func(o *Options) { o.Embedder = files; o.Files = []string{a} })
	require.NoError(t, err)
	assert.Zero(t, st.Total)
	assert.Zero(t, files.dims.Load(), "files run on an unchanged file")
	assert.Zero(t, files.embeds.Load())
}

// A run with a file to embed asks for no size first: the embedder starts at
// its first Embed, and the stored size is the one its vectors are checked against.
func TestRunChangedFileEmbedsWithoutProbe(t *testing.T) {
	e, a := indexed(t)
	e.write(t, "a.go", "func A\nfunc A2\n")

	em := &countEmbedder{}
	st, err := e.run(t, func(o *Options) { o.Embedder = em })
	require.NoError(t, err)
	assert.Equal(t, 1, st.Total)
	assert.Zero(t, em.dims.Load())
	assert.NotZero(t, em.embeds.Load())
	assert.Len(t, e.symbols(t, a), 2)
}

// Whatever drops the index probes first, so an embedder that cannot serve
// leaves the old index as it was, and one that can is asked before anything goes.
func TestRunChangeProbesBeforeDropping(t *testing.T) {
	cases := map[string]struct {
		prepare  func(*testing.T, *env)
		model    string // "" keeps the model of the first run
		tokens   int
		recreate bool
	}{
		"model": {model: "other"},
		"format": {prepare: func(t *testing.T, e *env) {
			st, err := LoadState(e.root)
			require.NoError(t, err)
			st.Format = Format - 1
			require.NoError(t, st.Save())
		}},
		"embed limit": {tokens: 500},
		"recreate":    {recreate: true},
		"missing collection": {prepare: func(t *testing.T, e *env) {
			require.NoError(t, e.store.DeleteCollection(context.Background(), "c"))
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e, _ := indexed(t)
			if c.prepare != nil {
				c.prepare(t, e)
			}
			setup := func(o *Options, em embed.Embedder) {
				o.Embedder, o.EmbedTokens, o.Recreate = em, c.tokens, c.recreate
			}
			model := c.model
			if model == "" {
				model = "fake"
			}
			before, err := LoadState(e.root)
			require.NoError(t, err)

			_, err = e.run(t, func(o *Options) { setup(o, deadEmbedder{namedEmbedder{&embed.Fake{}, model}}) })
			require.ErrorIs(t, err, embed.ErrUnavailable)
			after, err := LoadState(e.root)
			require.NoError(t, err)
			assert.Equal(t, before.Model, after.Model)
			assert.Equal(t, before.Format, after.Format)
			assert.Equal(t, before.EmbedTokens, after.EmbedTokens)
			assert.Equal(t, before.Files, after.Files)
			if name != "missing collection" {
				assert.Equal(t, 2, e.count(t, store.Filter{}), "points of the old index kept")
			}

			em := &countEmbedder{Fake: embed.Fake{Name: model}}
			st, err := e.run(t, func(o *Options) { setup(o, em) })
			require.NoError(t, err)
			assert.EqualValues(t, 1, em.dims.Load(), "probed once, before anything is dropped")
			assert.Equal(t, 2, st.Total, "everything embedded again")
			assert.Equal(t, 2, e.count(t, store.Filter{}))
		})
	}
}

// A store that cannot tell the vector size leaves the probe as the way to get it.
func TestRunStoredSizeUnreadableProbes(t *testing.T) {
	e, _ := indexed(t)
	em := &countEmbedder{}
	st, err := e.run(t, func(o *Options) { o.Embedder, o.Store = em, noSizeStore{e.store} })
	require.NoError(t, err)
	assert.Zero(t, st.Total)
	assert.EqualValues(t, 1, em.dims.Load())
	assert.Zero(t, em.embeds.Load())
}

// A collection that exists but holds nothing (a run cut short) is filled again,
// and the size its vectors will have is asked of the embedder before that.
func TestRunEmptyCollectionProbes(t *testing.T) {
	e, _ := indexed(t)
	ctx := context.Background()
	require.NoError(t, e.store.DeleteCollection(ctx, "c"))
	_, err := e.store.EnsureCollection(ctx, "c", 3)
	require.NoError(t, err)
	em := &countEmbedder{}
	st, err := e.run(t, func(o *Options) { o.Embedder = em })
	require.NoError(t, err)
	assert.Equal(t, 2, st.Total)
	assert.EqualValues(t, 1, em.dims.Load())
	assert.Equal(t, 2, e.count(t, store.Filter{}))
}

// The stored size is taken only from an index that holds a model and files: a
// state.json emptied or unreadable beside points of another size, or one that
// names no model, must leave the probe to say what size the vectors will have.
func TestRunStoredSizeNeedsModelAndFiles(t *testing.T) {
	cases := map[string]func(*State){
		"no files": func(s *State) { s.Files = nil },
		"no model": func(s *State) { s.Model = "" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e, _ := indexed(t)
			st, err := LoadState(e.root)
			require.NoError(t, err)
			change(st)
			require.NoError(t, st.Save())

			em := &countEmbedder{}
			_, err = e.run(t, func(o *Options) { o.Embedder = em })
			require.NoError(t, err)
			assert.EqualValues(t, 1, em.dims.Load(), "probed: the stored size is not trusted")
		})
	}
}

// existsStore answers CollectionExists as set, while VectorSize still gives the
// stored size.
type existsStore struct {
	store.Store
	exists bool
	err    error
}

func (s existsStore) CollectionExists(context.Context, string) (bool, error) { return s.exists, s.err }

// A store that cannot say the collection is there (an error, or not there)
// leaves the probe as the way to get the size, even when VectorSize answers.
func TestRunCollectionNotConfirmedProbes(t *testing.T) {
	cases := map[string]existsStore{
		"error":   {err: errors.New("exists unreadable")},
		"missing": {exists: false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e, _ := indexed(t)
			c.Store = e.store
			_, err := e.run(t, func(o *Options) { o.Embedder, o.Store = deadEmbedder{namedEmbedder{&embed.Fake{}, "fake"}}, c })
			require.Error(t, err)
			assert.ErrorIs(t, err, embed.ErrUnavailable, "the embedder was asked")
		})
	}
}
