package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bumpStateFormat(t *testing.T, root string) {
	t.Helper()
	statePath := filepath.Join(index.Dir(root), "state.json")
	rawState, err := os.ReadFile(statePath)
	require.NoError(t, err)
	var stateMap map[string]any
	require.NoError(t, json.Unmarshal(rawState, &stateMap))
	stateMap["format"] = index.Format + 1
	upgradedState, err := json.Marshal(stateMap)
	require.NoError(t, err)
	require.NoError(t, index.WriteAtomic(statePath, upgradedState))
}

func startLeaderWorkspace(t *testing.T, root string, st store.Store) *Workspace {
	t.Helper()
	cfg := testConfig(t)
	cfg.WatchEnabled = true
	cfg.AutoIndex = true
	cfg.WatchDebounce = 20 * time.Millisecond
	oldEng := New(cfg, st, &embed.Fake{}, nil)
	t.Cleanup(oldEng.Close)
	ws, err := oldEng.Workspace(root)
	require.NoError(t, err)
	ws.Index(false)
	require.Eventually(t, func() bool {
		s := ws.Status()
		return s.Phase == "done" && !s.Running && !s.Queued
	}, 5*time.Second, 20*time.Millisecond)
	return ws
}

// An older server that leads a root and meets an index of a newer Format
// writes nothing, not even index_status.json, and gives up watch.lock for
// good, so a server of the newer version takes the watcher over and indexes
// the edits.
func TestNewerFormatLeavesStatusAndWatcherToNewerServer(t *testing.T) {
	prevPoll := takeoverPoll
	takeoverPoll = 25 * time.Millisecond
	t.Cleanup(func() { takeoverPoll = prevPoll })

	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	aPath := filepath.Join(root, "a.go")
	require.NoError(t, os.WriteFile(aPath, []byte("func One\n"), 0o644))

	st := store.NewMemory()
	oldWS := startLeaderWorkspace(t, root, st)
	bumpStateFormat(t, root)

	var newRuns atomic.Int32
	newEng := New(oldWS.e.cfg, st, &embed.Fake{}, nil)
	newEng.run = func(_ context.Context, _ index.Options) (index.Status, error) {
		newRuns.Add(1)
		return index.Status{Phase: "done"}, nil
	}
	t.Cleanup(newEng.Close)
	_, err = newEng.Workspace(root)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(aPath, []byte("func One\nfunc Two\n"), 0o644))
	require.Eventually(t, func() bool {
		s := oldWS.Status()
		return s.Phase == "failed" && !s.Running
	}, 5*time.Second, 20*time.Millisecond)

	diskStatus, ok := index.ReadStatus(root)
	require.True(t, ok)
	assert.Equal(t, "done", diskStatus.Phase, "old server hitting ErrNewerFormat must not overwrite index_status.json with failed")

	require.NoError(t, os.WriteFile(filepath.Join(root, "b.go"), []byte("func Three\n"), 0o644))
	time.Sleep(200 * time.Millisecond)
	assert.Greater(t, newRuns.Load(), int32(0), "old server kept watch.lock after ErrNewerFormat, starving upgraded follower")
}
