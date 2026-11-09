//go:build linux

package index

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cloned repo controls .git: a symlink to /dev/zero or a FIFO must not hang
// or exhaust memory.
func TestIsLinkedWorktreeSpecialFiles(t *testing.T) {
	zero := t.TempDir()
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(zero, ".git")))
	fifo := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(fifo, ".git"), 0o644))
	for _, dir := range []string{zero, fifo} {
		done := make(chan bool, 1)
		go func() { done <- IsLinkedWorktree(dir) }()
		select {
		case got := <-done:
			assert.False(t, got, dir)
		case <-time.After(2 * time.Second):
			t.Fatalf("IsLinkedWorktree(%s) hangs", dir)
		}
	}
}
