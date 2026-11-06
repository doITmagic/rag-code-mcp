package index

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two holders of index.lock exclude each other on every platform.
func TestLockExcludes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "index.lock")
	unlock, err := lock(p)
	require.NoError(t, err)
	_, err = lock(p)
	assert.ErrorIs(t, err, ErrLocked)
	unlock()
	unlock, err = lock(p)
	require.NoError(t, err)
	unlock()
}
