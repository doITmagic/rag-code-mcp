package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/doITmagic/rag-code-mcp/v2/store"
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

// truncated and the advice to raise limit are true: callers in another
// language naming the same qualified name do not fill the scroll and mark one
// real usage truncated, and when callers in deleted files fill the store's
// scroll, raising limit would show nothing more, so the message says to grep.
func TestFindUsagesTruncatedOnlyWhenMoreExist(t *testing.T) {
	t.Run("other language callers", func(t *testing.T) {
		v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
		var other strings.Builder
		other.WriteString("package m/other\n")
		for i := range store.MaxScroll {
			fmt.Fprintf(&other, "func Foreign%d\n  call m/shared.Target\n", i)
		}
		v.addFiles(t, map[string]string{
			"shared/live.fk":  "package m/shared\nfunc Target\nfunc RealCaller\n  call m/shared.Target\n",
			"other/noise.fk2": other.String(),
		})
		r, res := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "m/shared.Target"})
		require.False(t, res.IsError, r.Message)
		var out struct {
			Total     int  `json:"total"`
			Truncated bool `json:"truncated"`
		}
		data(t, r, &out)
		assert.Equal(t, 1, out.Total, r.Message)
		assert.False(t, out.Truncated, r.Message)
	})

	t.Run("deleted callers fill the scroll", func(t *testing.T) {
		v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
		var gone strings.Builder
		for i := range store.MaxScroll + 5 {
			fmt.Fprintf(&gone, "func Gone%d\n  call m/live.Target\n", i)
		}
		v.addFiles(t, map[string]string{
			"live/t.fk":    "func Target\nfunc AliveCaller\n  call m/live.Target\n",
			"gone/dead.fk": gone.String(),
		})
		require.NoError(t, os.Remove(filepath.Join(v.root, "gone", "dead.fk")))
		r, res := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "m/live.Target"})
		require.False(t, res.IsError, r.Message)
		assert.NotContains(t, r.Message, "raise limit")
		assert.Contains(t, r.Message, "grep the name for the rest")
	})
}

// A candidate ID from an ambiguous answer selects the same merged getter/setter
// symbol as its qualified name, with the relations of both twins.
func TestCandidateIDKeepsTwinRelations(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"src/a.ts": `export class Cart {
    get entry() { return read(); }
    set entry(value) { write(value); }
}
function read() { return 1; }
function write(value) {}
`,
		"src/b.ts": "export class Other { get entry() { return 2; } }\n",
	})
	var h struct {
		Outgoing *Node `json:"outgoing"`
	}
	r, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "src/a.Cart.entry", "direction": "outgoing", "depth": 1})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &h)
	require.Len(t, h.Outgoing.Children, 2, "control: selecting by qualified name merges the getter and setter")
	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "entry", "direction": "outgoing", "depth": 1})
	require.Equal(t, "ambiguous", r.Status, r.Message)
	var amb struct {
		Candidates []Candidate `json:"candidates"`
	}
	data(t, r, &amb)
	var id string
	for _, c := range amb.Candidates {
		if c.QualifiedName == "src/a.Cart.entry" {
			id = c.ID
		}
	}
	require.NotEmpty(t, id)
	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": id, "direction": "outgoing", "depth": 1})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &h)
	var names []string
	for _, child := range h.Outgoing.Children {
		names = append(names, child.QualifiedName)
	}
	assert.ElementsMatch(t, []string{"src/a.read", "src/a.write"}, names, "a candidate id must select the same merged symbol as its qualified name and file")
}
