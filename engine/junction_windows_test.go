package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A root that is an NTFS junction, or lies below one, resolves to the real
// directory (godebug winsymlink=0 in go.mod), instead of "does not exist" or a
// root the walk cannot enter.
func TestValidateRootResolvesJunction(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	real := filepath.Join(base, "ws", "proj")
	require.NoError(t, os.MkdirAll(real, 0o755))
	for link, target := range map[string]string{"jroot": real, "jp": filepath.Join(base, "ws")} {
		out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(base, link), target).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	cfg := testConfig(t)
	for _, root := range []string{filepath.Join(base, "jroot"), filepath.Join(base, "jp", "proj")} {
		got, err := validateRoot(cfg, root)
		require.NoError(t, err, root)
		assert.True(t, samePath(real, got), root+" -> "+got)
	}
}
