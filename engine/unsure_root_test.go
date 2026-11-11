package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/store"
)

// A valid directory that is not the top of one project is refused with the
// candidates to offer the user; a confirmed or already known root is not asked again.
func TestUnsureRootAsksInsteadOfIndexing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	cfg := testConfig(t)
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	top, _ := filepath.EvalSymlinks(t.TempDir())
	mk := func(rel string) string {
		p := filepath.Join(top, rel)
		require.NoError(t, os.MkdirAll(p, 0o755))
		return p
	}
	repo, sub := mk("many/repo"), mk("many/repo/cmd/api")
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	other := mk("many/group/other")
	require.NoError(t, os.WriteFile(filepath.Join(other, "go.mod"), []byte("module other\n"), 0o644))
	plain, plainSub := mk("plain"), mk("plain/helpers")

	var unsure *UnsureRootError
	_, err := e.Workspace(sub) // a subdirectory of a git repository
	require.ErrorAs(t, err, &unsure)
	assert.Equal(t, []string{repo}, unsure.Candidates)

	_, err = e.Workspace(filepath.Join(top, "many")) // no project file, two projects below
	require.ErrorAs(t, err, &unsure)
	assert.ElementsMatch(t, []string{repo, other}, unsure.Candidates)
	assert.Empty(t, readRoots(), "a refused root is not remembered")

	for _, ok := range []string{repo, other, plain} { // a repository top, a project file, a folder with no marker
		_, err := e.Workspace(ok)
		require.NoError(t, err, ok)
	}
	_, err = e.Workspace(plainSub) // inside a known root that has no marker of its own
	require.ErrorAs(t, err, &unsure)
	assert.Equal(t, []string{plain}, unsure.Candidates)

	require.NoError(t, e.ConfirmRoot(sub)) // the user wants exactly this directory
	_, err = e.Workspace(sub)
	require.NoError(t, err)
	e2 := New(cfg, store.NewMemory(), &embed.Fake{}, nil) // another server: the root is known, not asked again
	defer e2.Close()
	_, err = e2.Workspace(sub)
	require.NoError(t, err)
}
