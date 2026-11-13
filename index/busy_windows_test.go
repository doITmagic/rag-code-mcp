package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// A file another process opened with no sharing reads as busy; a missing one does not.
func TestFileBusySharingViolation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lk.go")
	require.NoError(t, os.WriteFile(p, []byte("package lk\n"), 0o644))
	name, err := windows.UTF16PtrFromString(p)
	require.NoError(t, err)
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	_, err = os.ReadFile(p)
	assert.True(t, fileBusy(err), "%v", err)
	require.NoError(t, windows.CloseHandle(h))
	_, err = os.ReadFile(p)
	require.NoError(t, err)
	_, err = os.ReadFile(p + ".missing")
	assert.False(t, fileBusy(err))
}
