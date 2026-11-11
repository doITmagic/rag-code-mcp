package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitInit(t *testing.T, root, ignore string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	require.NoError(t, exec.Command("git", "-C", root, "init", "-q").Run())
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte(ignore), 0o644))
}

// F6: the walk counts git-ignored files against grepFiles and git drops
// them only afterwards, so a tree with more ignored analyzable files than the
// cap (seoraport: 9,207 in audituri/ and depozit/) is searched only up to the
// cap, in lexical order, and the cut is not reported.
func TestGrepIgnoredFilesUseUpTheCap(t *testing.T) {
	v := newEnv(t, false, nil)
	gitInit(t, v.root, "audituri/\n")
	dir := filepath.Join(v.root, "audituri")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for i := range grepFiles + 1 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("r%05d.fk", i)), []byte("func R\n"), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "pkg/z.fk"), []byte("func Z\n  uses seoField\n"), 0o644))
	hits, cut := v.t.grep(context.Background(), v.root, v.root, "seoField")
	require.Len(t, hits, 1, "the only real file holding the word was never read (cut=%v)", cut)
	assert.False(t, cut)
}

// F6: an unreadable directory anywhere ends the walk (fs.SkipAll on any
// error) and the lexically later files are never searched, again unreported.
func TestGrepUnreadableDirStopsSearch(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions and a non-root user")
	}
	v := newEnv(t, false, nil)
	locked := filepath.Join(v.root, "db-data")
	require.NoError(t, os.MkdirAll(locked, 0o755))
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "pkg/z.fk"), []byte("func Z\n  uses lockField\n"), 0o644))
	hits, cut := v.t.grep(context.Background(), v.root, v.root, "lockField")
	require.Len(t, hits, 1, "pkg/z.fk comes after db-data and was never read (cut=%v)", cut)
}

// F6: with an index in place the text search still walks the whole tree and
