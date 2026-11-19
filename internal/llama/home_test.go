package llama

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
)

// A relative RAGCODE_HOME gives absolute paths: llama-server is started with
// its own directory as working directory and would not find a relative one.
func TestFromConfigRelativeHomeIsAbsolute(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("RAGCODE_HOME", "rel")

	o := FromConfig(config.Default(), nil)
	want, err := filepath.Abs("rel")
	require.NoError(t, err)
	assert.Equal(t, want, o.Home)
	assert.True(t, filepath.IsAbs(o.ServerPath()), o.ServerPath())
	assert.True(t, filepath.IsAbs(o.ModelPath()), o.ModelPath())
	assert.True(t, strings.HasPrefix(o.ServerPath(), want+string(filepath.Separator)), "server stays under the home")
	assert.True(t, strings.HasPrefix(o.ModelPath(), want+string(filepath.Separator)), "model stays under the home")
}

func TestFromConfigKeepsAbsoluteServerAndModel(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("RAGCODE_HOME", "rel")
	server := filepath.Join(t.TempDir(), "llama-server")
	model := filepath.Join(t.TempDir(), "m.gguf")

	cfg := config.Default()
	cfg.LlamaServer, cfg.EmbedModel = server, model
	o := FromConfig(cfg, nil)
	assert.Equal(t, server, o.ServerPath())
	assert.Equal(t, model, o.ModelPath())
}
