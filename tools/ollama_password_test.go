package tools

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The password of an ollama_base_url with basic auth stays out of every tool
// answer: rag_server_info's ollama.url and ollama.error (a non-200 status), and
// the dependency_down message of Workspace.Ready.
func TestOllamaPasswordNotInAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	u.User = url.UserPassword("raguser", "superSecretPass99")
	authURL := u.String()

	v := newEnvWith(t, downEmbedder{Fake: &embed.Fake{}}, false, func(c *config.Config) {
		c.AutoIndex = false
		c.OllamaBaseURL = authURL
	})

	t.Run("rag_server_info", func(t *testing.T) {
		r, res := v.call(t, "rag_server_info", map[string]any{})
		require.False(t, res.IsError)
		var info struct {
			Ollama Service `json:"ollama"`
		}
		data(t, r, &info)
		assert.NotContains(t, info.Ollama.URL, "superSecretPass99")
		assert.NotContains(t, info.Ollama.Error, "superSecretPass99")
	})

	t.Run("dependency_down", func(t *testing.T) {
		r, res := v.call(t, "rag_find_usages", map[string]any{
			"workspace_root": v.root,
			"symbol":         "Foo",
		})
		require.False(t, res.IsError)
		require.Equal(t, "dependency_down", r.Status)
		assert.NotContains(t, r.Message, "superSecretPass99")
	})
}
