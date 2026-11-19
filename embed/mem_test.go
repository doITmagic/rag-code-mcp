package embed

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// MinFreeMB 0 turns the memory check off; the check with a low value is in
// TestLlamaLowMemory.
func TestCheckMemoryOff(t *testing.T) {
	old := freeMB
	freeMB = func() (int, bool) { return 1, true }
	t.Cleanup(func() { freeMB = old })
	l := NewLlama("m", nil, "")
	assert.NoError(t, l.checkMemory(), "0 turns the check off")
	l.MinFreeMB = 100
	assert.ErrorIs(t, l.checkMemory(), ErrLowMemory)
	freeMB = func() (int, bool) { return 0, false }
	assert.NoError(t, l.checkMemory(), "an unreadable free memory does not block")
	if _, ok := availableMB(); runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		assert.True(t, ok, "this platform reads its free memory")
	}
}
