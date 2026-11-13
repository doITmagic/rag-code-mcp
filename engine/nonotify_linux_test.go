package engine

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStatfs makes every path report file system type typ (or err).
func fakeStatfs(t *testing.T, typ uint32, err error) {
	t.Helper()
	orig := statfsType
	statfsType = func(string) (uint32, error) { return typ, err }
	t.Cleanup(func() { statfsType = orig })
}

// Only 9p (the Windows drives WSL2 mounts under /mnt) is named.
func TestNoNotifyFS(t *testing.T) {
	assert.Equal(t, "", noNotify(t.TempDir()), "the real file system of a temp dir")
	for typ, want := range map[uint32]string{
		0x01021997: "9p", // WSL2 drvfs
		0xFF534D42: "",   // cifs
		0x6969:     "",   // nfs
		0xEF53:     "",   // ext4
		0x01021994: "",   // tmpfs
		0xf15f:     "",   // ecryptfs
		0x65735546: "",   // fuse
	} {
		fakeStatfs(t, typ, nil)
		assert.Equal(t, want, noNotify("/x"), "type %#x", typ)
	}
	fakeStatfs(t, 0x01021997, errors.New("statfs failed"))
	assert.Equal(t, "", noNotify("/x"), "an error names nothing")
}

// F3 (Z13, WSL2 /mnt/c): a watched root on a file system without change
// events logs one WARN per root per process and reports it in Status, so the
// tools can tell the model that edits need rag_index_workspace.
func TestWatcherWarnsOnceOnNoNotifyFS(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	fakeStatfs(t, 0x01021997, nil)
	var logBuf safeBuf
	e := New(cfg, store.NewMemory(), &embed.Fake{}, slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer e.Close()
	e.run = (&runRecorder{}).run
	root := t.TempDir()
	var w *Workspace
	for range 3 { // every tool call opens the workspace
		var err error
		w, err = e.Workspace(root)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, strings.Count(logBuf.String(), "level=WARN"), logBuf.String())
	assert.Contains(t, logBuf.String(), "fs=9p")
	assert.Contains(t, w.Status().Unwatched, "9p")

	cfg.WatchEnabled = false // nothing is watched: nothing to warn about
	var quiet safeBuf
	e2 := New(cfg, store.NewMemory(), &embed.Fake{}, slog.New(slog.NewTextHandler(&quiet, nil)))
	defer e2.Close()
	w2, err := e2.Workspace(t.TempDir())
	require.NoError(t, err)
	assert.NotContains(t, quiet.String(), "level=WARN")
	assert.Equal(t, "", w2.Status().Unwatched)
}

// F4 (WSL, Z13): on 9p the watcher walks nothing; inotify reports no change
// there and the walk over a whole /mnt/c drive took minutes.
func TestWatcherSkipsTreeOnNoNotifyFS(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	fakeStatfs(t, 0x01021997, nil)
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = (&runRecorder{}).run
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0o755))
	w, err := e.Workspace(root)
	require.NoError(t, err)
	e.mu.Lock()
	defer e.mu.Unlock()
	require.NotNil(t, w.watch, "still the leader")
	assert.Empty(t, w.watch.dirs)
}
