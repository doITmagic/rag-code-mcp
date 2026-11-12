package tools

import (
	"path/filepath"
	"testing"

	_ "github.com/doITmagic/rag-code-mcp/v2/parser/javascript"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A server-rendered page: functions inside an IIFE, called from another
// script without imports and from an onclick handler of a template, had no
// symbol at all ("no indexed symbol named resolveError").
func TestUsagesOfBrowserGlobals(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"public/js/errors.js": `(function () {
  function resolveError(id) { post(id); }
  function post(id) {}
  window.resolveError = resolveError;
})();
`,
		"public/js/other.js": `document.addEventListener('DOMContentLoaded', function () {
  function retry() { resolveError(1); }
});
`,
		"templates/errors/index.html": `<div id="list">
  <button onclick="resolveError({{.ID}})">Resolve</button>
</div>
`,
	})
	var out struct {
		Files []FileUsages `json:"files"`
		Total int          `json:"total"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "resolveError"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	files := []string{}
	for _, f := range out.Files {
		files = append(files, f.FilePath)
	}
	assert.ElementsMatch(t, []string{filepath.Join(v.root, "public", "js", "other.js"), filepath.Join(v.root, "templates", "errors", "index.html")}, files, r.Message)
	assert.Equal(t, 2, out.Total, r.Message)
}

// A Jest test file relying on the global describe/test (no imports): the
// helper inside the suite is no indexed symbol.
func TestJestGlobalSuiteHelperNoSymbol(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"src/sum.test.js": `describe('sum', () => {
  function renderCart() { return 1; }
  test('adds', () => { expect(renderCart()).toBe(1); });
});
`,
	})
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "renderCart"})
	assert.Contains(t, r.Message, `no indexed symbol named "renderCart"`)
}
