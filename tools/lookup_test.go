package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// swapEmbedder delegates to the embedder set last, so a test indexes with a
// working one and then breaks it, keeping the model name the index was made with.
type swapEmbedder struct {
	cur atomic.Pointer[embed.Embedder]
}

func newSwap(em embed.Embedder) *swapEmbedder {
	s := &swapEmbedder{}
	s.set(em)
	return s
}

func (s *swapEmbedder) set(em embed.Embedder) { s.cur.Store(&em) }

func (s *swapEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return (*s.cur.Load()).Embed(ctx, texts)
}

func (s *swapEmbedder) Dimension(ctx context.Context) (int, error) {
	return (*s.cur.Load()).Dimension(ctx)
}

func (s *swapEmbedder) Model() string { return (*s.cur.Load()).Model() }

// fetchingEmbedder knows its vector size but cannot embed: the model file is
// being downloaded again.
type fetchingEmbedder struct{ *embed.Fake }

func (fetchingEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, fmt.Errorf("%w: %w", embed.ErrUnavailable, embed.ErrFetching)
}

// callWithin is call with a deadline: a tool that waits for the embedder fails
// the test instead of hanging it.
func (v *env) callWithin(t *testing.T, d time.Duration, name string, args map[string]any) Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	res, err := v.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, "%s did not answer in %s", name, d)
	require.NotEmpty(t, res.Content)
	text := res.Content[0].(*mcp.TextContent).Text
	var r Response
	require.False(t, res.IsError, text)
	require.NoError(t, json.Unmarshal([]byte(text), &r), text)
	return r
}

func (v *env) lookups() map[string]map[string]any {
	return map[string]map[string]any{
		"rag_find_usages":          {"workspace_root": v.root, "symbol": "m/pkg.Foo"},
		"rag_call_hierarchy":       {"workspace_root": v.root, "symbol": "m/pkg.Bar"},
		"rag_list_package_exports": {"workspace_root": v.root, "package": "m/pkg"},
	}
}

// Usages, call hierarchy and package exports read the index only: they answer
// from it whatever the embedder is doing.
func TestLookupToolsNeedNoEmbedder(t *testing.T) {
	for name, tc := range map[string]struct {
		em     func() embed.Embedder
		search string // status of rag_search with the same embedder; "" when it would wait
	}{
		"down":    {func() embed.Embedder { return downEmbedder{&embed.Fake{}} }, "dependency_down"},
		"lowmem":  {func() embed.Embedder { return lowMemEmbedder{&embed.Fake{}} }, "low_memory"},
		"loading": {func() embed.Embedder { return slowStartEmbedder{Fake: &embed.Fake{}, open: make(chan struct{})} }, ""},
	} {
		t.Run(name, func(t *testing.T) {
			sw := newSwap(&embed.Fake{})
			v := newEnvWith(t, sw, true, nil)
			sw.set(tc.em())
			for tool, args := range v.lookups() {
				r := v.callWithin(t, 2*time.Second, tool, args)
				assert.Equal(t, "ok", r.Status, "%s: %s", tool, r.Message)
				assert.NotEmpty(t, r.Data, tool)
				assert.NotContains(t, r.Message, "embedder", tool)
				assert.NotContains(t, r.Message, "memory", tool)
			}
			if tc.search != "" {
				r := v.callWithin(t, 2*time.Second, "rag_search", map[string]any{"workspace_root": v.root, "query": "Foo"})
				assert.Equal(t, tc.search, r.Status, r.Message)
			}
		})
	}
}

// Without an index the lookups say so, whatever the embedder does.
func TestLookupToolsNotIndexedWithEmbedderDown(t *testing.T) {
	v := newEnvWith(t, downEmbedder{&embed.Fake{}}, false, func(c *config.Config) { c.AutoIndex = false })
	for tool, args := range v.lookups() {
		r := v.callWithin(t, 2*time.Second, tool, args)
		assert.Equal(t, "not_indexed", r.Status, "%s: %s", tool, r.Message)
	}
}

// A model change makes the run probe the new model before it touches the index;
// while the probe waits, the lookups answer from the old rows.
func TestLookupToolsDuringModelChange(t *testing.T) {
	sw := newSwap(&embed.Fake{Name: "model-a"})
	v := newEnvWith(t, sw, true, nil)
	gate := slowStartEmbedder{Fake: &embed.Fake{Name: "model-b"}, open: make(chan struct{})}
	sw.set(gate)
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	r := v.callWithin(t, 2*time.Second, "rag_index_workspace", map[string]any{"workspace_root": v.root})
	assert.Equal(t, "indexing", r.Status, r.Message)
	require.Eventually(t, func() bool { return w.Status().Running }, 5*time.Second, 10*time.Millisecond)
	for tool, args := range v.lookups() {
		r := v.callWithin(t, 2*time.Second, tool, args)
		assert.Equal(t, "indexing", r.Status, "%s: %s", tool, r.Message)
		assert.NotEmpty(t, r.Data, tool)
	}
	close(gate.open)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)
}

// scrollFailStore fails every Scroll with err.
type scrollFailStore struct {
	store.Store
	err error
}

func (s scrollFailStore) Scroll(context.Context, string, store.Filter, int) ([]store.Hit, error) {
	return nil, s.err
}

// A failing store is named as the store by the lookup tools, which never
// started the embedder, while a failing embedder is named by rag_search.
func TestLookupToolsNameTheStore(t *testing.T) {
	v := newEnv(t, false, nil)
	e := engine.New(v.t.cfg, scrollFailStore{Store: store.NewMemory(), err: errors.New("disk on fire")}, &embed.Fake{}, nil)
	t.Cleanup(e.Close)
	v.t.e = e
	for tool, args := range v.lookups() {
		r := v.callWithin(t, 2*time.Second, tool, args)
		assert.Equal(t, "dependency_down", r.Status, tool)
		assert.Contains(t, r.Message, "index store unavailable", tool)
		assert.Contains(t, r.Message, "disk on fire", tool)
		assert.NotContains(t, r.Message, "embedder", tool)
	}
}

// While the runtime or the model downloads, rag_search answers at once and
// says to retry; the download and the index run it serves keep going.
func TestSearchWhileTheModelDownloads(t *testing.T) {
	em := &embed.Fake{Fetch: make(chan struct{})}
	v := newEnvWith(t, em, false, func(c *config.Config) { c.AutoIndex = false })
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	r := v.callWithin(t, 2*time.Second, "rag_index_workspace", map[string]any{"workspace_root": v.root})
	assert.Equal(t, "indexing", r.Status, r.Message)
	require.Eventually(t, func() bool { return w.Status().Running }, 5*time.Second, 10*time.Millisecond)

	for _, q := range []string{"Foo", "the function that calls foo"} {
		r = v.callWithin(t, 2*time.Second, "rag_search", map[string]any{"workspace_root": v.root, "query": q})
		assert.Equal(t, "dependency_down", r.Status, q)
		assert.Contains(t, r.Message, "being downloaded", q)
		assert.Contains(t, r.Message, "retry", q)
		assert.NotContains(t, r.Message, "see the embedder state", q)
	}
	assert.True(t, w.Status().Running, "the index run still waits for the download")

	close(em.Fetch)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)
	r = v.callWithin(t, 2*time.Second, "rag_search", map[string]any{"workspace_root": v.root, "query": "Foo"})
	assert.Equal(t, "ok", r.Status, r.Message)
}

// The model file can go missing after the size was cached: the query embed
// itself then reports the download.
func TestSearchQueryEmbedFetching(t *testing.T) {
	sw := newSwap(&embed.Fake{})
	v := newEnvWith(t, sw, true, nil)
	sw.set(fetchingEmbedder{&embed.Fake{}})
	for _, q := range []string{"Foo", "the function that calls foo"} {
		r := v.callWithin(t, 2*time.Second, "rag_search", map[string]any{"workspace_root": v.root, "query": q})
		assert.Equal(t, "dependency_down", r.Status, q)
		assert.Contains(t, r.Message, "being downloaded", q)
		assert.NotContains(t, r.Message, "see the embedder state", q)
	}
}
