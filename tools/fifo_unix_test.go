//go:build unix

package tools

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fallback keeps the index skip rules: no symlink out of the root, no
// fifo (it would block the read), no secret file.
func TestSearchFallbackSkipRules(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	outside := filepath.Join(t.TempDir(), "s.fk")
	require.NoError(t, os.WriteFile(outside, []byte("func Secret\n"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(v.root, "link.fk")))
	require.NoError(t, syscall.Mkfifo(filepath.Join(v.root, "x.fk"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, ".env.fk"), []byte("func Secret\n"), 0o644))
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Secret"})
	var out struct{ Results []SearchHit }
	data(t, r, &out)
	assert.Empty(t, out.Results)
}
