package uninstall

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// On Windows a process is stopped only when its image is the binary itself,
// not another rag-code-mcp.exe from a different folder (an IDE's server).
func TestSameExe(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	bin := filepath.Join(dir, "rag-code-mcp.exe")
	require.NoError(t, os.WriteFile(bin, []byte("x"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(other, "rag-code-mcp.exe"), []byte("x"), 0o755))

	assert.True(t, sameExe(bin, bin))
	assert.True(t, sameExe(filepath.Join(dir, "x", "..", "RAG-CODE-MCP.EXE"), bin), "case and .. differ")
	assert.False(t, sameExe(filepath.Join(other, "rag-code-mcp.exe"), bin), "same name, other folder")
	assert.False(t, sameExe(filepath.Join(other, "missing.exe"), bin))
	assert.False(t, sameExe("", bin))
}
