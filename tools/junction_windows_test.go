package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An NTFS junction inside the root that points outside it is refused like a
// symlink (Go 1.23+ resolves junctions only with godebug winsymlink=0, set in go.mod).
func TestInsideRootRefusesJunctionOut(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	root, outside := filepath.Join(base, "ws"), filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "outside.go"), []byte("package main\n"), 0o644))
	out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "linkout"), outside).CombinedOutput()
	require.NoError(t, err, string(out))

	_, err = insideRoot(root, `linkout\outside.go`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the workspace root")
}
