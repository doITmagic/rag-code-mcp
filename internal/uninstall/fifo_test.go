//go:build unix

package uninstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A FIFO at an instruction file in a known root is skipped instead of
// blocking Run forever in the read; the other pointers are still removed.
func TestRunSkipsFIFOPointerFile(t *testing.T) {
	f := setup(t)
	fifo := filepath.Join(f.root, "AGENTS.md")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))

	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), Options{Yes: true, Out: &bytes.Buffer{}}) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		// The blocked goroutine is abandoned; the test binary exits anyway.
		t.Fatal("uninstall.Run blocked on the FIFO")
	}
	claude, _ := os.ReadFile(filepath.Join(f.root, "CLAUDE.md"))
	assert.Equal(t, "# Project\n", string(claude))
}
