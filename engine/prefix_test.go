package engine

import (
	"context"
	"sync"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/require"
)

type recEmbedder struct {
	embed.Fake
	mu    sync.Mutex
	texts []string
}

func (r *recEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	r.mu.Lock()
	r.texts = append(r.texts, texts...)
	r.mu.Unlock()
	return r.Fake.Embed(ctx, texts)
}

func TestSearchAddsModelQueryPrefix(t *testing.T) {
	cfg := testConfig(t)
	cfg.AutoIndex = false
	em := &recEmbedder{Fake: embed.Fake{Name: "embeddinggemma-2:270m"}}
	e := New(cfg, store.NewMemory(), em, nil)
	defer e.Close()
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)
	_, _ = w.Search(context.Background(), "find the parser", store.Filter{}, 3)
	em.mu.Lock()
	defer em.mu.Unlock()
	require.Contains(t, em.texts, "task: code retrieval | query: find the parser")
}
