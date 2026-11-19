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
	assert.NoError(t, l.checkMemory(1), "0 turns the check off")
	l.MinFreeMB = 100
	assert.ErrorIs(t, l.checkMemory(1), ErrLowMemory)
	freeMB = func() (int, bool) { return 0, false }
	assert.NoError(t, l.checkMemory(1), "an unreadable free memory does not block")
	if _, ok := availableMB(); runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		assert.True(t, ok, "this platform reads its free memory")
	}
}

// A warm-up asks for a multiple of MinFreeMB; the message names the amount.
func TestCheckMemoryFactor(t *testing.T) {
	old := freeMB
	freeMB = func() (int, bool) { return 1000, true }
	t.Cleanup(func() { freeMB = old })
	l := NewLlama("m", nil, "")
	l.MinFreeMB = 750
	assert.NoError(t, l.checkMemory(1))
	err := l.checkMemory(2)
	assert.ErrorIs(t, err, ErrLowMemory)
	assert.Contains(t, err.Error(), "at least 1500 MB needed")
}
