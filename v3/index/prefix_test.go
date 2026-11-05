package index

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
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

func TestRunAddsModelDocPrefix(t *testing.T) {
	e := newEnv(t)
	em := &recEmbedder{Fake: embed.Fake{Name: "embeddinggemma-2:270m"}}
	e.opts.Embedder = em
	e.write(t, "a.go", "package a\nfunc A() {}\n")
	_, err := Run(context.Background(), e.opts)
	require.NoError(t, err)
	require.NotEmpty(t, em.texts)
	for _, s := range em.texts {
		if s != "dimension probe" {
			require.True(t, strings.HasPrefix(s, "title: none | text: "), s)
		}
	}
}
