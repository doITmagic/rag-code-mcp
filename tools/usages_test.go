package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// htmlAnalyzer parses .fkh files like fakeAnalyzer, as language "html", whose
// relations the index lets point into code of any language.
type htmlAnalyzer struct{ fakeAnalyzer }

func (htmlAnalyzer) Language() string     { return "html" }
func (htmlAnalyzer) Extensions() []string { return []string{".fkh"} }
func (h htmlAnalyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	syms, err := h.fakeAnalyzer.Analyze(f)
	for i := range syms {
		syms[i].Language = "html"
	}
	return syms, err
}

func init() { parser.Register(htmlAnalyzer{}) }

// A symbol's usages and callers come from its own language: a call in another
// language naming the same qualified name (resolved by the index to that
// language's symbol) is neither a usage nor an incoming node, and an ambiguous
// call by name in another language is not in ambiguous_by_name. A page
// (html) calling it is still a usage: html and docs link into code on purpose.
func TestUsagesStayInTheirLanguage(t *testing.T) {
	v := newEnv(t, false, nil)
	for rel, content := range map[string]string{
		"pkg/target.fk":  "func Parse\n",
		"pkg/target.fk2": "func Parse\n",
		"app/caller.fk2": "func RunOtherLang\n  call m/pkg.Parse\n",
		"app/page.fkh":   "func Page\n  call m/pkg.Parse\n",
		"pkg/s.fk":       "func Save\n",
		"pkg/s1.fk2":     "func Save\n",
		"other/s2.fk2":   "func Save\n",
		"app/ambig.fk2":  "func AmbOtherLang\n  call Save\n",
	} {
		p := filepath.Join(v.root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool {
		s := w.Status()
		return s.Phase == "done" && !s.Running && !s.Queued
	}, 10*time.Second, 20*time.Millisecond)

	r, res := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Parse", "file_path": "pkg/target.fk"})
	require.False(t, res.IsError, r.Message)
	var u struct {
		Files []FileUsages `json:"files"`
		Total int          `json:"total"`
	}
	data(t, r, &u)
	require.Equal(t, 1, u.Total, "only the html page, not the fake2 caller")
	assert.Equal(t, "m/app.Page", u.Files[0].Usages[0].Caller)

	rh, res := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "Parse", "file_path": "pkg/target.fk", "direction": "incoming"})
	require.False(t, res.IsError, rh.Message)
	var h struct{ Incoming *Node }
	data(t, rh, &h)
	require.NotNil(t, h.Incoming)
	require.Len(t, h.Incoming.Children, 1)
	assert.Equal(t, "m/app.Page", h.Incoming.Children[0].QualifiedName)

	ra, res := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Save", "file_path": "pkg/s.fk"})
	require.False(t, res.IsError, ra.Message)
	var a struct {
		Ambiguous int `json:"ambiguous_by_name"`
	}
	data(t, ra, &a)
	assert.Zero(t, a.Ambiguous, "a fake2 call by name is ambiguous among fake2 symbols only")
}

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
