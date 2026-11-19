package engine

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingEmbedder counts the Dimension calls.
type countingEmbedder struct {
	*embed.Fake
	dims atomic.Int32
}

func (c *countingEmbedder) Dimension(ctx context.Context) (int, error) {
	c.dims.Add(1)
	return c.Fake.Dimension(ctx)
}

// scrollFailStore fails every Scroll with err.
type scrollFailStore struct {
	store.Store
	err error
}

func (s scrollFailStore) Scroll(context.Context, string, store.Filter, int) ([]store.Hit, error) {
	return nil, s.err
}

// Scroll needs no vector: a store failure comes back as it is, and the
// embedder, which may be downloading its model, is not asked for its size.
func TestScrollErrorDoesNotProbeTheEmbedder(t *testing.T) {
	cfg := testConfig(t)
	em := &countingEmbedder{Fake: &embed.Fake{}}
	boom := errors.New("disk on fire")
	e := New(cfg, scrollFailStore{Store: store.NewMemory(), err: boom}, em, nil)
	defer e.Close()
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	_, err = w.Scroll(context.Background(), store.Filter{}, 5)
	require.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ErrNotIndexed)
	assert.Zero(t, em.dims.Load(), "Scroll must not reach Dimension")
}

// A missing collection is ErrNotIndexed for Scroll, with the first run queued
// when auto_index is on, without the embedder being asked anything.
func TestScrollMissingCollectionNeedsNoEmbedder(t *testing.T) {
	cfg := testConfig(t)
	em := &countingEmbedder{Fake: &embed.Fake{Fetch: make(chan struct{})}} // never closed: any wait would hang
	e := New(cfg, store.NewMemory(), em, nil)
	defer e.Close()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "func A\n")
	w, err := e.Workspace(root)
	require.NoError(t, err)
	_, err = w.Scroll(context.Background(), store.Filter{}, 5)
	require.ErrorIs(t, err, ErrNotIndexed)
	assert.Zero(t, em.dims.Load())
	assert.Eventually(t, func() bool { s := w.Status(); return s.Queued || s.Running }, time.Second, 5*time.Millisecond, "the first run is queued")
}

// A search while the model is downloading answers at once, and says so; the
// index run of the same workspace keeps waiting through the download.
func TestSearchDuringFetchFailsAtOnce(t *testing.T) {
	cfg := testConfig(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "func A\n")
	em := &embed.Fake{Fetch: make(chan struct{})}
	e := New(cfg, store.NewMemory(), em, nil)
	defer e.Close()
	w, err := e.Workspace(root)
	require.NoError(t, err)
	w.Index(false) // an index run: no mark, it waits for the download
	require.Eventually(t, func() bool { return w.Status().Running }, 2*time.Second, 5*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := w.Search(context.Background(), "A", store.Filter{}, 5)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, embed.ErrUnavailable)
		require.ErrorIs(t, err, embed.ErrFetching)
		assert.NotErrorIs(t, err, ErrNotIndexed)
	case <-time.After(2 * time.Second):
		t.Fatal("Search waited for the download")
	}

	ready := make(chan string, 1)
	go func() {
		ok, msg := w.Ready(context.Background())
		assert.False(t, ok)
		ready <- msg
	}()
	select {
	case msg := <-ready:
		assert.Contains(t, msg, "downloaded")
		assert.Contains(t, msg, "retry")
		assert.NotContains(t, msg, "see rag_server_info", "it is not a failure to look into")
	case <-time.After(2 * time.Second):
		t.Fatal("Ready waited for the download")
	}

	assert.True(t, w.Status().Running, "the index run still waits")
	close(em.Fetch)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running && !s.Queued }, 5*time.Second, 10*time.Millisecond)
	hits, err := w.Search(context.Background(), "A", store.Filter{}, 5)
	require.NoError(t, err)
	assert.NotEmpty(t, hits)
	ok, msg := w.Ready(context.Background())
	assert.True(t, ok, msg)
}
