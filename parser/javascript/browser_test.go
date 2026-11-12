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
