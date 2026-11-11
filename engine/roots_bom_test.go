package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/store"
)

// Windows PowerShell 5 "Set-Content -Encoding utf8" writes a UTF-8 BOM. The
// roots in such a roots.json must be read, and registering a new root must
// keep them instead of rewriting the file with only the new one.
func TestRootsJSONWithBOMIsReadAndKept(t *testing.T) {
	cfg := testConfig(t)
	var old []string
	for range 3 {
		old = append(old, t.TempDir())
	}
	data, err := json.MarshalIndent(old, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(rootsPath()), 0o755))
	require.NoError(t, os.WriteFile(rootsPath(), append([]byte("\xef\xbb\xbf"), data...), 0o644))

	assert.ElementsMatch(t, old, readRoots(), "readRoots must strip a leading UTF-8 BOM")

	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	root := t.TempDir()
	_, err = e.Workspace(root)
	require.NoError(t, err)
	assert.ElementsMatch(t, append(old, root), readRoots(), "registerRoot dropped the roots of a BOM-prefixed roots.json")
}

// Fails today: the junk is overwritten and no roots.json.bad exists. Passes with the fix.
// An invalid roots.json is kept as roots.json.bad, byte for byte, before the
// new root is written.
func TestRootsJSONInvalidIsKeptAsBad(t *testing.T) {
	cfg := testConfig(t)
	junk := []byte(`["/a", "/b",]`)
	require.NoError(t, os.MkdirAll(filepath.Dir(rootsPath()), 0o755))
	require.NoError(t, os.WriteFile(rootsPath(), junk, 0o644))
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	root := t.TempDir()
	_, err := e.Workspace(root)
	require.NoError(t, err)
	assert.Equal(t, []string{root}, readRoots())
	bad, err := os.ReadFile(rootsPath() + ".bad")
	require.NoError(t, err)
	assert.Equal(t, junk, bad)
}
