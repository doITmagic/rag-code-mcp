package javascript

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// Browser scripts wrap their code in an IIFE or a ready callback: the
// functions declared there were no symbols and their calls no usages.
func TestWrapperFunctionsAreSymbols(t *testing.T) {
	src := `(function () {
  'use strict';
  /** Inits pickers. */
  function initFlatpickr() { syncInputs(); setLawyerStatus(''); }
  const syncInputs = () => {};
  document.addEventListener('DOMContentLoaded', function () {
    function onReady() { initFlatpickr(); }
    $(function () { function deep() { onReady(); } });
  });
  window.resolveError = resolveError;
  function resolveError(id) {}
})();
!function ($) { function plugin() {} }(jQuery);
document.addEventListener('DOMContentLoaded', () => { function toggle() { plugin(); } });
`
	syms := analyze(t, "public/js/page.js", src)
	init := find(t, syms, "public/js/page.initFlatpickr")
	assert.Equal(t, parser.KindFunction, init.Kind)
	assert.Equal(t, "Inits pickers.", init.Doc)
	assert.Equal(t, 4, init.StartLine)
	assert.False(t, init.Exported)
	assert.Equal(t, "public/js/page.syncInputs", rel(t, init, parser.RelCall, "syncInputs").TargetQualifiedName)
	assert.Equal(t, "", rel(t, init, parser.RelCall, "setLawyerStatus").TargetQualifiedName, "a global of another file: by name in the index")
	assert.Equal(t, parser.KindFunction, find(t, syms, "public/js/page.syncInputs").Kind)
	assert.Equal(t, "public/js/page.initFlatpickr", rel(t, find(t, syms, "public/js/page.onReady"), parser.RelCall, "initFlatpickr").TargetQualifiedName)
	assert.Equal(t, "public/js/page.onReady", rel(t, find(t, syms, "public/js/page.deep"), parser.RelCall, "onReady").TargetQualifiedName)
	assert.True(t, find(t, syms, "public/js/page.resolveError").Exported, "window.resolveError = resolveError makes it a global")
	find(t, syms, "public/js/page.plugin")
	assert.Equal(t, "public/js/page.plugin", rel(t, find(t, syms, "public/js/page.toggle"), parser.RelCall, "plugin").TargetQualifiedName)
}

// A module's wrappers are test suites and hooks: their locals stay out.
func TestModuleWrappersNotDescended(t *testing.T) {
	src := `import { app } from './app';
describe('x', () => {
  type Actual = string;
  const App = () => app();
  function verify() {}
});
`
	for _, s := range analyze(t, "src/app.test.ts", src) {
		t.Errorf("unexpected symbol %s", s.QualifiedName)
	}
}

// `window.toggleFilters = function () {}` defines a global function.
func TestWindowAssignedFunction(t *testing.T) {
	src := `document.addEventListener('DOMContentLoaded', function () {
  window.toggleFilters = function () { sync(); };
  function sync() { window.toggleFilters(); }
});
window.count = 1;
`
	syms := analyze(t, "js/filter-toggle.js", src)
	tf := find(t, syms, "js/filter-toggle.toggleFilters")
	assert.Equal(t, parser.KindFunction, tf.Kind)
	assert.True(t, tf.Exported)
	assert.Equal(t, "js/filter-toggle.sync", rel(t, tf, parser.RelCall, "sync").TargetQualifiedName)
	assert.Equal(t, "js/filter-toggle.toggleFilters", rel(t, find(t, syms, "js/filter-toggle.sync"), parser.RelCall, "toggleFilters").TargetQualifiedName)
	assert.Equal(t, parser.KindVariable, find(t, syms, "js/filter-toggle.count").Kind)
}

// A test file without imports (Jest/Mocha globals) is still no browser
// script: functions inside describe/it/test/hook callbacks are no symbols,
// while an IIFE or a ready callback in the same file still counts.
func TestGlobalTestSuiteNotDescended(t *testing.T) {
	src := `describe('sum', () => {
  function renderCart() { return 1; }
  beforeEach(function () { function setupCart() {} });
  test('adds', () => { function inner() {} expect(renderCart()).toBe(1); });
});
describe.each([[1]])('each %i', () => { function eachHelper() {} });
it.only('x', () => { function onlyHelper() {} });
afterAll(() => { function teardown() {} });
context('c', function () { function ctxHelper() {} });
suite('s', function () { function suiteHelper() {} });
(function () { function realGlobal() {} })();
`
	syms := analyze(t, "src/sum.test.js", src)
	for _, s := range syms {
		if s.QualifiedName != "src/sum.test.realGlobal" {
			t.Errorf("unexpected symbol %s", s.QualifiedName)
		}
	}
	find(t, syms, "src/sum.test.realGlobal")
}

// Only what an inline html handler can reach is flagged global: a top-level
// function of a classic script or a window.f global, not a wrapper local, a
// method, nor a function of an ES module.
func TestBrowserGlobalsFlagged(t *testing.T) {
	classic := `function top() {}
var expr = function () {};
class Widget { open() {} }
(function () {
  function local() {}
  function shared() {}
  window.shared = shared;
  window.assigned = function () {};
})();
`
	module := `import { x } from './x';
export function exported() {}
function own() {}
window.own = own;
window.direct = function () {};
function renamed() {}
window.other = renamed; // the global is other, a symbol calling renamed: onclick="renamed()" reaches nothing
`
	global := map[string]bool{}
	for _, s := range append(analyze(t, "js/page.js", classic), analyze(t, "js/mod.js", module)...) {
		global[s.QualifiedName] = s.Metadata["global"] == "true"
	}
	assert.Equal(t, map[string]bool{
		"js/page.top": true, "js/page.expr": true, "js/page.Widget": false, "js/page.Widget.open": false,
		"js/page.local": false, "js/page.shared": true, "js/page.assigned": true,
		"js/mod.exported": false, "js/mod.own": true, "js/mod.direct": true, "js/mod.renamed": false, "js/mod.other": true,
	}, global)
}

// window.exposed = implementation: window.exposed() calls implementation, and
// the declared exposed is no longer what onclick="exposed()" reaches.
func TestBrowserWindowAliasTargetsAssignedFunction(t *testing.T) {
	syms := analyze(t, "browser.js", `function implementation() {}
function exposed() {}
window.exposed = implementation;
function caller() { window.exposed(); }
`)
	caller := find(t, syms, "browser.caller")
	r := rel(t, caller, parser.RelCall, "exposed")
	if r.TargetQualifiedName != "browser.implementation" {
		t.Fatalf("window.exposed() target = %q; want browser.implementation (the assigned function)", r.TargetQualifiedName)
	}
	assert.Empty(t, find(t, syms, "browser.exposed").Metadata["global"], "window.exposed is implementation now")
	assert.Equal(t, "true", find(t, syms, "browser.implementation").Metadata["global"])
}

// window.resolveError = handleResolve with no declared resolveError: the
// global is a symbol of its own that calls handleResolve.
func TestBrowserWindowAliasIsGlobalSymbol(t *testing.T) {
	syms := analyze(t, "public/errors.js", `function handleResolve(id) {
  return id;
}
window.resolveError = handleResolve;
`)
	g := find(t, syms, "public/errors.resolveError")
	assert.Equal(t, parser.KindFunction, g.Kind)
	assert.True(t, g.Exported)
	assert.Equal(t, "true", g.Metadata["global"])
	assert.Equal(t, 4, g.StartLine)
	assert.Equal(t, "public/errors.handleResolve", rel(t, g, parser.RelCall, "handleResolve").TargetQualifiedName)
}
