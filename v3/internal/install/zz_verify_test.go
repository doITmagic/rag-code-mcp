package install

// Independent verification probes.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// -ollama=local must still wait for Ollama and pull the model when docker is
// absent and -qdrant=docker is requested; only the container steps are skipped.
func TestVerifyLocalOllamaWithoutDocker(t *testing.T) {
	_, dockerLog, pulled := env(t)
	t.Setenv("PATH", t.TempDir()) // no docker, no codex
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Ollama: "local", Qdrant: "docker", Yes: true, SkipIDE: true, Out: &out, srcDir: src(t)}), out.String())
	assert.NoFileExists(t, dockerLog)
	assert.Equal(t, 1, len(*pulled), "local Ollama was skipped because docker is missing")
}

// A cancelled context must not hang Run (containers, polls and pulls bail out).
func TestVerifyCancelledCtxNoHang(t *testing.T) {
	env(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Ollama: "docker", Qdrant: "docker", Yes: true, SkipIDE: true, Out: &bytes.Buffer{}, srcDir: src(t)})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung with a cancelled context")
	}
}

// Unreachable Ollama with a short wait returns an error, no panic.
func TestVerifyWaitHTTPUnreachable(t *testing.T) {
	err := waitHTTP(context.Background(), "http://127.0.0.1:1/readyz", 300*time.Millisecond)
	assert.Error(t, err)
	err = pullModel(context.Background(), &bytes.Buffer{}, "http://127.0.0.1:1", "m")
	assert.Error(t, err)
	_ = os.Stdout
}

// Reinstall replaces the binary contents, leaves no temp file behind and
// writes .zshrc when SHELL is zsh.
func TestVerifyReinstallAndZsh(t *testing.T) {
	home, _, _ := env(t)
	t.Setenv("SHELL", "/usr/bin/zsh")
	s := src(t)
	opts := Options{Ollama: "skip", Qdrant: "skip", Yes: true, SkipIDE: true, Out: &bytes.Buffer{}, srcDir: s}
	require.NoError(t, Run(context.Background(), opts))
	require.NoError(t, os.WriteFile(filepath.Join(s, "rag-code-mcp"), []byte("v2"), 0o644))
	require.NoError(t, Run(context.Background(), opts))
	binDir := filepath.Join(home, ".ragcode", "bin")
	data, err := os.ReadFile(filepath.Join(binDir, "rag-code-mcp"))
	require.NoError(t, err)
	assert.Equal(t, "v2", string(data))
	entries, _ := os.ReadDir(binDir)
	assert.Len(t, entries, 1, "no temp files left in bin/")
	rc, _ := os.ReadFile(filepath.Join(home, ".zshrc"))
	assert.Equal(t, 1, strings.Count(string(rc), "# RagCode MCP"))
	assert.NoFileExists(t, filepath.Join(home, ".bashrc"))
}
