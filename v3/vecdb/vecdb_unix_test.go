//go:build unix

package vecdb

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureReadOnlyDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores permissions")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	d := New(dir)
	defer d.Close()
	_, err := d.Ensure(ctx, "c", 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), dir)
	assert.False(t, errors.Is(err, ErrNotFound))
}
