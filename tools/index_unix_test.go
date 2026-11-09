//go:build unix

package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/stretchr/testify/require"
)

// A root whose status has no ended_at is only "interrupted" when nobody holds
// its lock; while a run is in progress the current phase is reported instead.
func TestKnownRootsRunningIsNotInterrupted(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	root := t.TempDir()
	dir := index.Dir(root)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	roots, _ := json.Marshal([]string{root})
	require.NoError(t, os.WriteFile(filepath.Join(config.HomeDir(), "roots.json"), roots, 0o644))
	status, _ := json.Marshal(index.Status{Root: root, StartedAt: time.Now(), Phase: "parsing", Done: 3})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index_status.json"), status, 0o644))

	f, err := os.OpenFile(filepath.Join(dir, "index.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))

	got := knownRoots(nil)
	require.Len(t, got, 1)
	require.Equal(t, "parsing", got[0].State)
	require.Equal(t, 3, got[0].Files)

	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_UN))
	require.Equal(t, "interrupted", knownRoots(nil)[0].State)
}
