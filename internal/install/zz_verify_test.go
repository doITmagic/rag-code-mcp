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

// A cancelled context must not hang Run (containers, polls and pulls bail out).
func TestVerifyCancelledCtxNoHang(t *testing.T) {
	env(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{SkipDownload: true, Yes: true, SkipIDE: true, Out: &bytes.Buffer{}, srcDir: src(t)})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung with a cancelled context")
	}
}

// Reinstall replaces the binary contents, leaves no temp file behind and
// writes .zshrc when SHELL is zsh.
func TestVerifyReinstallAndZsh(t *testing.T) {
	home, _ := env(t)
	t.Setenv("SHELL", "/usr/bin/zsh")
	s := src(t)
	opts := Options{SkipDownload: true, Yes: true, SkipIDE: true, Out: &bytes.Buffer{}, srcDir: s}
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
