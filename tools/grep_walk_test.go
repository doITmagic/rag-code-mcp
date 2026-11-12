package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// A word typed without diacritics finds the text written with them, and the
// other way round ("Salveaza" against 40 "Salvează" lines found nothing).
// The line comes back as it is in the file; a longer word is not a match.
func TestGrepIgnoresDiacritics(t *testing.T) {
	v := newEnv(t, false, nil)
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "pkg/d.fk"),
		[]byte("func D\n  label Salvează\n  label Salveaza\n  label Salveazăă\n  label Șterge țară\n"), 0o644))
	for _, word := range []string{"Salveaza", "Salvează"} {
		hits, cut := v.t.grep(context.Background(), v.root, v.root, word)
		require.Len(t, hits, 2, word)
		assert.Equal(t, "label Salvează", hits[0].Text, word)
		assert.Equal(t, 2, hits[0].Line, word)
		assert.Equal(t, 3, hits[1].Line, word)
		assert.False(t, cut)
	}
	hits, _ := v.t.grep(context.Background(), v.root, v.root, "Sterge")
	require.Len(t, hits, 1)
	assert.Equal(t, "label Șterge țară", hits[0].Text)
	hits, _ = v.t.grep(context.Background(), v.root, v.root, "tara")
	assert.Len(t, hits, 1)
}

// A qualified name with a wrong owner ("Base.Data" when the method is
// BackendController.Data) lists the symbols of that last name first, marked
// "name", and says so, instead of only unrelated semantic neighbours.
func TestSearchWrongOwnerListsSymbolsOfThatName(t *testing.T) {
	v := newEnv(t, true, nil)
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Wrong.Foo", "limit": 3})
	require.Equal(t, "ok", r.Status)
	assert.Contains(t, r.Message, `no symbol named "Wrong.Foo"; 1 symbols are named "Foo"`)
	var out struct {
		Results []SearchHit `json:"results"`
	}
	data(t, r, &out)
	require.NotEmpty(t, out.Results)
	assert.Equal(t, "Foo", out.Results[0].Name)
	assert.Equal(t, "name", out.Results[0].Match)
	// A right qualified name stays an exact match.
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "pkg.Foo", "limit": 3})
	data(t, r, &out)
	require.NotEmpty(t, out.Results)
	assert.Equal(t, "exact", out.Results[0].Match)
	assert.Empty(t, r.Message)
}

// A text file with a NUL byte past the first 8 KiB (a sentinel in a regex) is
// text to the index, so the text search reads it too.
func TestGrepReadsFileWithLateNULByte(t *testing.T) {
	v := newEnv(t, false, nil)
	body := "func N\n" + strings.Repeat("  label padding padding padding\n", 400) + "  label late\x00sentinel Zebra\n"
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "pkg/n.fk"), []byte(body), 0o644))
	hits, _ := v.t.grep(context.Background(), v.root, v.root, "Zebra")
	require.Len(t, hits, 1)
	assert.Equal(t, 402, hits[0].Line)
}
