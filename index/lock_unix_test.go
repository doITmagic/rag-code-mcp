//go:build unix

package index

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunLocked(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, os.MkdirAll(Dir(e.root), 0o755))
	f, err := os.OpenFile(filepath.Join(Dir(e.root), "index.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX))

	st, err := e.run(t, nil)
	assert.ErrorIs(t, err, ErrLocked)
	assert.Equal(t, "failed", st.Phase)
	_, ok := ReadStatus(e.root)
	assert.False(t, ok, "a locked run must not touch the other run's status")

	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_UN))
	_, err = e.run(t, nil)
	assert.NoError(t, err)
}
