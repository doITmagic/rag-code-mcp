package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func checkSourceFilesOwnHome(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	ragHome := filepath.Join(v.root, "ragcode_home")
	t.Setenv("RAGCODE_HOME", ragHome)
	require.NoError(t, os.MkdirAll(ragHome, 0o755))
	leaked := filepath.Join(ragHome, "home_data.fk")
	require.NoError(t, os.WriteFile(leaked, []byte("func LeakedFromOwnHome\n"), 0o644))

	walked, err := index.Walk(v.root, v.t.cfg.ExcludePatterns)
	require.NoError(t, err)
	assert.NotContains(t, walked, leaked, "index.Walk skips RAGCODE_HOME")

	r, _ := v.call(t, "rag_search", map[string]any{
		"workspace_root": v.root,
		"query":          "LeakedFromOwnHome",
	})
	var out struct{ Results []SearchHit }
	data(t, r, &out)
	assert.Empty(t, out.Results, "sourceFiles must skip RAGCODE_HOME like index.Walk")
}

func checkSourceFilesPathPrefixBypass(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	require.NoError(t, os.MkdirAll(filepath.Join(v.root, "node_modules", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "node_modules", "pkg", "dep.fk"), []byte("func LeakedNodeModules\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(v.root, ".hidden"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, ".hidden", "sec.fk"), []byte("func LeakedHiddenDir\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(v.root, "subwt"), 0o755))
	gitdir := t.TempDir() // a linked worktree's gitdir has a commondir
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "subwt", ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "subwt", "wt.fk"), []byte("func LeakedWorktree\n"), 0o644))

	cases := map[string]string{
		"node_modules/pkg": "LeakedNodeModules",
		".hidden":          "LeakedHiddenDir",
		"subwt":            "LeakedWorktree",
	}
	for prefix, sym := range cases {
		r, _ := v.call(t, "rag_search", map[string]any{
			"workspace_root": v.root,
			"query":          sym,
			"path_prefix":    prefix,
		})
		var out struct{ Results []SearchHit }
		data(t, r, &out)
		assert.Empty(t, out.Results, "path_prefix %q must not bypass skip rules", prefix)
	}
}

// sourceFiles (the not-indexed fallback and the text search) prunes what
// index.Walk prunes: a non-hidden RAGCODE_HOME inside the root, and a
// path_prefix at or inside an excluded, hidden or linked-worktree directory.
func TestSourceFilesSkipRules(t *testing.T) {
	t.Run("ragcode_home_inside_workspace", checkSourceFilesOwnHome)
	t.Run("path_prefix_bypasses_excluded_hidden_and_worktree", checkSourceFilesPathPrefixBypass)
}
