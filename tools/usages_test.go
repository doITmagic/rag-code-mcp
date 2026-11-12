package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTruncatedUsagesEnv(t *testing.T) *env {
	t.Helper()
	v := newEnv(t, false, func(c *config.Config) {
		c.WatchEnabled = false
		c.AutoIndex = false
	})
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "pkg", "target.fk"), []byte("func TargetSym\n"), 0o644))
	for i := 1; i <= 5; i++ {
		src := fmt.Sprintf("func Caller%d\n  call m/pkg.TargetSym\n", i)
		p := filepath.Join(v.root, "pkg", fmt.Sprintf("c%d.fk", i))
		require.NoError(t, os.WriteFile(p, []byte(src), 0o644))
	}
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool {
		s := w.Status()
		return s.Phase == "done" && !s.Running && !s.Queued
	}, 10*time.Second, 20*time.Millisecond)
	return v
}

// limit counts live referencing symbols: with 2 of the first 3 caller files
// deleted on disk, limit=2 still returns 2 of the 3 live callers, truncated.
func TestFindUsagesLimitCountsLiveFiles(t *testing.T) {
	v := setupTruncatedUsagesEnv(t)

	type usagesData struct {
		Files     []FileUsages `json:"files"`
		Total     int          `json:"total"`
		Truncated bool         `json:"truncated"`
	}
	r1, res := v.call(t, "rag_find_usages", map[string]any{
		"workspace_root": v.root, "symbol": "TargetSym", "limit": 2,
	})
	require.False(t, res.IsError, r1.Message)
	var first usagesData
	data(t, r1, &first)
	require.Len(t, first.Files, 2)
	require.True(t, first.Truncated)

	// Delete those 2 files on disk; 3 live callers still exist on disk and in the index.
	for _, f := range first.Files {
		require.NoError(t, os.Remove(f.FilePath))
	}

	r2, res := v.call(t, "rag_find_usages", map[string]any{
		"workspace_root": v.root, "symbol": "TargetSym", "limit": 2,
	})
	require.False(t, res.IsError, r2.Message)
	var second usagesData
	data(t, r2, &second)
	assert.Equal(t, 2, second.Total, "3 live callers exist so limit=2 must return 2")
	assert.True(t, second.Truncated, "3 live callers exist with limit=2 so truncated must be true")
}
