package uninstall

// Independent verification probes.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Garbage roots.json and a cancelled context: no panic, no hang, home removed.
func TestVerifyRobust(t *testing.T) {
	f := setup(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.home, ".ragcode", "roots.json"), []byte("\x00{broken"), 0o644))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{Yes: true, Docker: true, Out: &bytes.Buffer{}}) }()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung")
	}
	assert.NoDirExists(t, filepath.Join(f.home, ".ragcode"))
	assert.DirExists(t, filepath.Join(f.root, ".ragcode"), "garbage roots.json means no roots")
}

// Closed stdin when an answer is needed fails Run instead of looping.
func TestVerifyEOFStdin(t *testing.T) {
	f := setup(t)
	err := Run(context.Background(), Options{In: bytes.NewReader(nil), Out: &bytes.Buffer{}})
	assert.Error(t, err)
	assert.DirExists(t, filepath.Join(f.root, ".ragcode"))
}

// RAGCODE_HOME override: that dir is removed and its bin/ PATH line stripped;
// ~/.ragcode is left alone.
func TestVerifyRagcodeHomeOverride(t *testing.T) {
	f := setup(t)
	custom := t.TempDir()
	t.Setenv("RAGCODE_HOME", custom)
	require.NoError(t, os.MkdirAll(filepath.Join(custom, "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.home, ".zshrc"), []byte("# RagCode MCP\nexport PATH=\""+filepath.Join(custom, "bin")+":$PATH\"\nx=1\n"), 0o644))
	require.NoError(t, Run(context.Background(), Options{Yes: true, Out: &bytes.Buffer{}}))
	assert.NoDirExists(t, custom)
	assert.DirExists(t, filepath.Join(f.home, ".ragcode"), "default home untouched when overridden")
	rc, _ := os.ReadFile(filepath.Join(f.home, ".zshrc"))
	assert.Equal(t, "x=1\n", string(rc))
}

// RAGCODE_HOME set to the user's home (or /) must never be deleted recursively.
func TestRunRefusesHomeAsRagcodeHome(t *testing.T) {
	f := setup(t)
	keep := filepath.Join(f.home, "notes.txt")
	require.NoError(t, os.WriteFile(keep, []byte("mine"), 0o644))
	t.Setenv("RAGCODE_HOME", f.home)
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, Out: &out}))
	assert.FileExists(t, keep)
	assert.Contains(t, out.String(), "not removing")
}
