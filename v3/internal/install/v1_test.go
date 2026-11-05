package install

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunRemovesV1(t *testing.T) {
	home, dockerLog, _ := env(t)
	v1Bin := filepath.Join(home, ".local", "share", "ragcode", "bin")
	require.NoError(t, os.MkdirAll(v1Bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(v1Bin, "rag-code-mcp"), []byte("v1"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".local", "share", "qdrant", "collections"), 0o755))
	rc := "alias ll='ls -l'\n\nexport PATH=\"" + v1Bin + ":$PATH\"\nexport EDITOR=vim\n"
	require.NoError(t, os.WriteFile(filepath.Join(home, ".bashrc"), []byte(rc), 0o644))
	cursor := filepath.Join(home, ".cursor", "mcp.config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(cursor), 0o755))
	require.NoError(t, os.WriteFile(cursor, []byte(`{"mcpServers":{"ragcode":{"command":"`+v1Bin+`/rag-code-mcp"},"github":{"command":"gh"}}}`), 0o644))

	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Ollama: "skip", Qdrant: "skip", Yes: true, SkipIDE: true, Out: &out, srcDir: src(t)}), out.String())

	assert.NoDirExists(t, filepath.Join(home, ".local", "share", "ragcode"))
	assert.NoDirExists(t, filepath.Join(home, ".local", "share", "qdrant"))
	data, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	assert.NotContains(t, string(data), v1Bin)
	assert.Contains(t, string(data), "alias ll='ls -l'\n\nexport EDITOR=vim\n")
	data, _ = os.ReadFile(cursor)
	assert.NotContains(t, string(data), "ragcode")
	assert.Contains(t, string(data), "github")
	log, _ := os.ReadFile(dockerLog)
	assert.Contains(t, string(log), "rm -f ragcode-qdrant ragcode-ollama")
	assert.FileExists(t, filepath.Join(home, ".ragcode", "bin", "rag-code-mcp"))
}

// Answering no to "Replace?" must leave everything as it was, v1 included.
func TestRunCancelKeepsV1(t *testing.T) {
	home, _, _ := env(t)
	v1Dir := filepath.Join(home, ".local", "share", "ragcode")
	require.NoError(t, os.MkdirAll(v1Dir, 0o755))
	good := src(t)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ragcode", "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ragcode", "bin", "rag-code-mcp"), []byte("v3"), 0o755))
	err := Run(context.Background(), Options{Ollama: "skip", Qdrant: "skip", SkipIDE: true, In: strings.NewReader("n\n"), Out: &bytes.Buffer{}, srcDir: good})
	require.ErrorContains(t, err, "cancelled")
	assert.DirExists(t, v1Dir)
}
