//go:build unix

package index

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file another process holds locked (faked: unreadable, classified busy) is
// a failed file listed in Busy, with a Warning that its symbols may be stale;
// the next run that reads it clears both.
func TestRunReportsBusyFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	isBusy = func(err error) bool { return errors.Is(err, fs.ErrPermission) }
	t.Cleanup(func() { isBusy = fileBusy })
	e := newEnv(t)
	b := e.write(t, "b.go", "func B\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	e.write(t, "b.go", "func B2\n")
	require.NoError(t, os.Chmod(b, 0))
	files := func(o *Options) { o.Files = []string{b} }
	st, err := e.run(t, files)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Failed)
	assert.Equal(t, []string{b}, st.Busy)
	assert.Contains(t, st.Warning, "locked by another process")
	assert.Equal(t, 1, e.count(t, store.Filter{FilePath: b}), "the old points stay")
	disk, _ := ReadStatus(e.root)
	assert.Contains(t, disk.Warning, "locked by another process", "the answers read it from disk")

	require.NoError(t, os.Chmod(b, 0o644))
	st, err = e.run(t, files)
	require.NoError(t, err)
	assert.Zero(t, st.Failed)
	assert.Empty(t, st.Busy)
	assert.Empty(t, st.Warning)
}
