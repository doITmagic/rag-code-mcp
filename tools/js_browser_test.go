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
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "src/StatusCard.default"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	require.Len(t, out.Files, 1, r.Message)
	assert.Equal(t, filepath.Join(v.root, "src", "Dashboard.vue"), out.Files[0].FilePath)
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
