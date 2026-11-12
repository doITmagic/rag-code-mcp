//go:build unix

package engine

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A FIFO at an instruction file is refused instead of blocking WriteIDERules
// forever in the read, and the other files still get the pointer.
func TestWriteIDERulesRefusesFIFO(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "CLAUDE.md")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# Agents")

	done := make(chan error, 1)
	go func() { done <- WriteIDERules(root) }()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "not a regular file")
	case <-time.After(5 * time.Second):
		// The blocked goroutine is abandoned; the test binary exits anyway.
		t.Fatal("WriteIDERules blocked on the FIFO")
	}
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Agents\n"+IDEPointer+"\n", string(agents))
}
