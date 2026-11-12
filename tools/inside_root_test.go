package tools

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// within, the comparison under insideRoot: with fold (Windows) a path whose
// drive letter or directories differ only in case from the root is inside it
// and comes back with the root's spelling, also when it does not exist (so
// EvalSymlinks left its case alone); without fold it is outside. A sibling
// sharing the root as a name prefix is never inside.
func TestWithinRootFoldsCase(t *testing.T) {
	root := filepath.FromSlash("/C/Users/Alice/proj")
	in := filepath.FromSlash("/c/users/alice/proj/src/mod")

	got, ok := within(root, in, true)
	assert.True(t, ok)
	assert.Equal(t, filepath.FromSlash("/C/Users/Alice/proj/src/mod"), got)

	got, ok = within(root, filepath.FromSlash("/c/users/alice/PROJ"), true)
	assert.True(t, ok)
	assert.Equal(t, root, got)

	_, ok = within(root, in, false)
	assert.False(t, ok, "case-sensitive file systems keep the case")

	for _, fold := range []bool{true, false} {
		_, ok = within(root, filepath.FromSlash("/C/Users/Alice/project/x"), fold)
		assert.False(t, ok, "a sibling with the root as name prefix")
		_, ok = within(root, filepath.FromSlash("/C/Users/Alice"), fold)
		assert.False(t, ok, "a parent of the root")
	}
}
