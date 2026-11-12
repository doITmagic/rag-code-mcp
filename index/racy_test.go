package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setClock fixes the time the indexer reads as now. Whole seconds: ecryptfs
// drops sub-second mtimes set by Chtimes.
func setClock(t *testing.T, at time.Time) {
	t.Helper()
	old := clock
	clock = func() time.Time { return at }
	t.Cleanup(func() { clock = old })
}

// replace writes content (same size as before) to p and restores mtime.
func replace(t *testing.T, p, content string, mtime time.Time) {
	t.Helper()
	info, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, info.Size(), int64(len(content)), "the replacement keeps the size")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	require.NoError(t, os.Chtimes(p, mtime, mtime))
}

func entry(t *testing.T, root, p string) Entry {
	t.Helper()
	st, err := LoadState(root)
	require.NoError(t, err)
	return st.Files[p]
}

// A file indexed within two seconds of its mtime and then replaced by other
// bytes of the same size under the same mtime (tar -x, cp -p, rsync -a, or a
// filesystem with coarse timestamps) is re-indexed: its entry carries a
// content hash, checked while the mtime is that close to the read.
func TestRacyFileReplacedSameStatIsReindexed(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for name, files := range map[string]bool{"full": false, "files": true} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			setClock(t, now)
			p := e.write(t, "a.go", "func AA\n")
			require.NoError(t, os.Chtimes(p, now, now)) // written the second it is indexed
			_, err := e.run(t, nil)
			require.NoError(t, err)
			require.NotEmpty(t, entry(t, e.root, p).Hash, "a racy file carries a hash")

			replace(t, p, "func BB\n", now)
			st, err := e.run(t, func(o *Options) {
				if files {
					o.Files = []string{p}
				}
			})
			require.NoError(t, err)
			assert.Equal(t, 1, st.Total)
			assert.Equal(t, []string{"BB"}, keys(e.symbols(t, p)))
		})
	}
}

// A file whose mtime is safely older than its read carries no hash, and is
// never opened again while its mtime and size hold: a same-stat replacement
// with an old mtime is not seen (git behaves the same). Proven by bytes that
// became binary: had the walk or the scan read them, the file would leave
// the index or be re-parsed.
func TestOldFileReplacedSameStatIsNotOpened(t *testing.T) {
	e := newEnv(t)
	now := time.Now().Truncate(time.Second)
	setClock(t, now)
	old := now.Add(-time.Hour)
	p := e.write(t, "a.go", "func AA\n")
	require.NoError(t, os.Chtimes(p, old, old))
	_, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Empty(t, entry(t, e.root, p).Hash)

	replace(t, p, "fun\x00 BB\n", old)
	st, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Zero(t, st.Total)
	assert.Zero(t, st.Deleted)
	assert.Equal(t, []string{"AA"}, keys(e.symbols(t, p)))
}

// A racy file left untouched is read again while its mtime is inside the
// window; once it is older than the window, its hash is dropped and later
// runs no longer open it.
func TestRacyFileHashDroppedOnceOld(t *testing.T) {
	e := newEnv(t)
	now := time.Now().Truncate(time.Second)
	setClock(t, now)
	p := e.write(t, "a.go", "func AA\n")
	require.NoError(t, os.Chtimes(p, now, now))
	_, err := e.run(t, nil)
	require.NoError(t, err)
	hash := entry(t, e.root, p).Hash
	require.NotEmpty(t, hash)

	st, err := e.run(t, nil) // still inside the window: checked, kept
	require.NoError(t, err)
	assert.Zero(t, st.Total)
	assert.Equal(t, hash, entry(t, e.root, p).Hash)

	setClock(t, now.Add(3*time.Second))
	st, err = e.run(t, nil) // past the window: checked once more, hash dropped
	require.NoError(t, err)
	assert.Zero(t, st.Total)
	assert.Empty(t, entry(t, e.root, p).Hash, "the dropped hash is persisted")

	replace(t, p, "fun\x00 BB\n", now)
	st, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Zero(t, st.Total, "not opened again")
	assert.Zero(t, st.Deleted)
	assert.Equal(t, []string{"AA"}, keys(e.symbols(t, p)))
}

// A racy file whose bytes became binary under the same stat is opened by the
// walk's binary check too, not taken as known text: it leaves the index.
func TestRacyFileTurnedBinaryLeaves(t *testing.T) {
	e := newEnv(t)
	now := time.Now().Truncate(time.Second)
	setClock(t, now)
	p := e.write(t, "a.go", "func AA\n")
	require.NoError(t, os.Chtimes(p, now, now))
	_, err := e.run(t, nil)
	require.NoError(t, err)
	replace(t, p, "fun\x00 BB\n", now)
	st, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Deleted)
	assert.Empty(t, e.all(t, p))
}

// A state.json written before hashes existed (entries without "hash") loads
// and runs as before: nothing is re-indexed.
func TestStateWithoutHashesLoads(t *testing.T) {
	e := newEnv(t)
	p := e.write(t, "a.go", "func AA\n")
	info, err := os.Stat(p)
	require.NoError(t, err)
	_, err = e.run(t, nil)
	require.NoError(t, err)
	st, err := LoadState(e.root)
	require.NoError(t, err)
	ids := st.Files[p].IDs
	data := fmt.Sprintf(`{"model":%q,"format":%d,"files":{%q:{"mtime":%d,"size":%d,"ids":[%q]}}}`,
		st.Model, Format, p, info.ModTime().UnixNano(), info.Size(), ids[0])
	require.NoError(t, os.WriteFile(filepath.Join(Dir(e.root), "state.json"), []byte(data), 0o644))
	got, err := LoadState(e.root)
	require.NoError(t, err)
	assert.Equal(t, Entry{ModTime: info.ModTime().UnixNano(), Size: info.Size(), IDs: ids}, got.Files[p])
	run, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Zero(t, run.Total)
	assert.Equal(t, []string{"AA"}, keys(e.symbols(t, p)))
}
