package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrefixes(t *testing.T) {
	for _, c := range []struct{ model, query, doc string }{
		{"embeddinggemma-2:270m", "task: code retrieval | query: ", "title: none | text: "},
		{"embeddinggemma-2", "task: code retrieval | query: ", "title: none | text: "},
		{"hf.co/google/EmbeddingGemma-2:latest", "task: code retrieval | query: ", "title: none | text: "},
		{"qwen3-embedding:0.6b", qwen3Instruct, ""},
		{"mxbai-embed-large", "", ""},
		{"fake", "", ""},
	} {
		assert.Equal(t, c.query, QueryText(c.model, ""), c.model)
		assert.Equal(t, c.doc, DocText(c.model, ""), c.model)
	}
	assert.Equal(t, "task: code retrieval | query: find x", QueryText("embeddinggemma-2:270m", "find x"))
	assert.Equal(t, "title: none | text: func f()", DocText("embeddinggemma-2:270m", "func f()"))
}

func TestOllamaTooOldHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":""}`, http.StatusPreconditionFailed)
	}))
	defer srv.Close()
	_, err := NewOllama(srv.URL, "embeddinggemma-2:270m", nil).Embed(context.Background(), []string{"x"})
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Contains(t, err.Error(), "newer Ollama")
}
