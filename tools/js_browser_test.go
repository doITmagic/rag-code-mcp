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

// window.resolveError = handleResolve: onclick="resolveError(1)" reaches
// the global resolveError, which calls handleResolve.
func TestInlineHandlerReachesWindowAliasTarget(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"public/errors.js": `function handleResolve(id) {
  return id;
}
window.resolveError = handleResolve;
`,
		"templates/page.html": `<div id="list">
  <button onclick="resolveError(1)">Resolve</button>
</div>
`,
	})
	for name, file := range map[string]string{"resolveError": filepath.Join("templates", "page.html"), "handleResolve": filepath.Join("public", "errors.js")} {
		var out struct {
			Files []FileUsages `json:"files"`
		}
		r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": name})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		require.Len(t, out.Files, 1, name+": "+r.Message)
		assert.Equal(t, filepath.Join(v.root, file), out.Files[0].FilePath, name)
	}
}

// A <script setup> without top-level statements had no default symbol, so
// the <StatusCard /> of an importer was a usage of nothing.
func TestUsagesOfVueSetupComponent(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"src/StatusCard.vue": "<script setup>\nconst props = defineProps(['title']);\nfunction openCard() { return props.title; }\n</script>\n<template>\n  <button @click=\"openCard\">{{ title }}</button>\n</template>\n",
		"src/Dashboard.vue":  "<script setup>\nimport StatusCard from './StatusCard.vue';\n</script>\n<template>\n  <StatusCard title=\"ok\" />\n</template>\n",
	})
	var out struct {
		Files []FileUsages `json:"files"`
	}
	// by the component's name too: it names the template symbol, the usages are the default symbol's
	for _, q := range []string{"src/StatusCard.default", "StatusCard"} {
		r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": q})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		require.Len(t, out.Files, 1, q+": "+r.Message)
		assert.Equal(t, filepath.Join(v.root, "src", "Dashboard.vue"), out.Files[0].FilePath)
	}
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

// onclick="save()" calls the global window.save; a method of a class (no
// global, no window.save anywhere) is out of its reach.
func TestInlineHandlerNoClassMethod(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"src/cart.js": `export class Cart {
  save() { return 1; }
}
export function load() {}
`,
		"templates/page.html": `<div id="list">
  <button onclick="save()">Save</button>
  <button onclick="load()">Load</button>
</div>
`,
	})
	for _, name := range []string{"Cart.save", "load"} {
		var out struct {
			Total int `json:"total"`
		}
		r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": name})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		assert.Equal(t, 0, out.Total, name+": "+r.Message)
	}
}

// A recursive component renders itself by its file name, without an import:
// that tag is a usage too.
func TestUsagesOfRecursiveVueComponent(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"src/TreeNode.vue": "<script setup>\ndefineProps(['node']);\n</script>\n<template>\n  <li><TreeNode v-for=\"c in node.children\" :node=\"c\" /></li>\n</template>\n",
		"src/Tree.vue":     "<script setup>\nimport TreeNode from './TreeNode.vue';\n</script>\n<template>\n  <ul><TreeNode :node=\"root\" /></ul>\n</template>\n",
	})
	var out struct {
		Files []FileUsages `json:"files"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "TreeNode"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Len(t, out.Files, 2, r.Message)
}

// Two classic scripts declare the global toggle; a page calls it from
// onclick. The handlers bind to neither, and each toggle says so in
// ambiguous_by_name instead of "0 usages" alone. A module function of that
// name is not what a handler reaches: it reports none.
func TestInlineHandlerOfAmbiguousGlobalIsCounted(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"public/a.js":   "function toggle() {}\n",
		"public/b.js":   "function toggle() {}\n",
		"src/module.js": "export function toggle() {}\n",
		"templates/page.html": `<div id="list">
  <button onclick="toggle()">A</button>
  <button onclick="toggle()">B</button>
</div>
`,
	})
	for file, want := range map[string]int{"public/a.js": 2, "public/b.js": 2, "src/module.js": 0} {
		var out struct {
			Total     int `json:"total"`
			Ambiguous int `json:"ambiguous_by_name"`
		}
		r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "toggle", "file_path": file})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		assert.Equal(t, 0, out.Total, file+": "+r.Message)
		assert.Equal(t, want, out.Ambiguous, file+": "+r.Message)
	}
}
