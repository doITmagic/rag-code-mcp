package index

import (
	"fmt"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A non-fresh run (resume, big pull) whose files call many symbols already
// in the store must resolve every one of them, not only the first 500.
func TestRunResolvesBeyondFiveHundredTargets(t *testing.T) {
	e := newEnv(t)
	var defs, calls []string
	for i := 0; i < 600; i++ {
		defs = append(defs, fmt.Sprintf("func T%d", i))
		calls = append(calls, fmt.Sprintf("func F%d -> b.T%d", i, i))
	}
	e.write(t, "b.go", strings.Join(defs, "\n"))
	_, err := e.run(t, nil)
	require.NoError(t, err)

	a := e.write(t, "a.go", strings.Join(calls, "\n"))
	_, err = e.run(t, nil)
	require.NoError(t, err)
	unresolved := 0
	for _, s := range e.symbols(t, a) {
		if s.Relations[0].TargetID == "" {
			unresolved++
		}
	}
	assert.Equal(t, 0, unresolved)

	// A later run without changes does not repair them.
	_, err = e.run(t, nil)
	require.NoError(t, err)
	unresolved = 0
	for _, s := range e.symbols(t, a) {
		if s.Relations[0].TargetID == "" {
			unresolved++
		}
	}
	assert.Equal(t, 0, unresolved, "after an unchanged rerun")
}

// A JS declaration both named and default-exported is qualified
// "<module>.default", while `import { sleep }` names "<module>.sleep": the
// top-level symbol of that module with that name is the target.
func TestResolveByModuleAndName(t *testing.T) {
	parser.Register(lineAnalyzer{"javascript", ".js"})
	e := newEnv(t)
	b := e.write(t, "b.js", "func sleep as default")
	a := e.write(t, "a.js", "func F -> b.sleep")
	e.write(t, "g.go", "func H -> b.sleep") // Go never takes this path
	_, err := e.run(t, nil)
	require.NoError(t, err)
	target := e.symbols(t, b)["sleep"].ID
	require.NotEmpty(t, target)
	assert.Equal(t, target, e.symbols(t, a)["F"].Relations[0].TargetID, "full run")

	c := e.write(t, "c.js", "func G -> b.sleep")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, target, e.symbols(t, c)["G"].Relations[0].TargetID, "from the store")
	assert.Empty(t, e.symbols(t, e.root+"/g.go")["H"].Relations[0].TargetID)
}

// onclick="toggle()" in a page names the browser global window.toggle: the
// JavaScript global function of that name, by name, from html only; a
// method or a module function of that name is out of its reach.
func TestResolveHTMLHandlerToJavaScriptGlobal(t *testing.T) {
	parser.Register(lineAnalyzer{"javascript", ".js"})
	parser.Register(lineAnalyzer{"html", ".fkh"})
	e := newEnv(t)
	js := e.write(t, "filter.js", "func toggle as global\nvar shown")
	e.write(t, "mod.js", "func toggle\nmethod Cart.toggle")
	e.write(t, "t.go", "func toggle")
	h := e.write(t, "page.fkh", "func page -> window.toggle\nfunc other -> window.shown")
	a := e.write(t, "a.js", "func F -> window.toggle")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	target := e.symbols(t, js)["toggle"].ID
	require.NotEmpty(t, target)
	r := e.symbols(t, h)["page"].Relations[0]
	assert.Equal(t, target, r.TargetID, "full run")
	assert.Equal(t, parser.ResolutionUnique, r.Resolution)
	assert.Empty(t, e.symbols(t, h)["other"].Relations[0].TargetID, "a call by name never binds a variable")
	assert.Empty(t, e.symbols(t, a)["F"].Relations[0].TargetID, "only html handlers")

	h2 := e.write(t, "page2.fkh", "func page2 -> window.toggle")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, target, e.symbols(t, h2)["page2"].Relations[0].TargetID, "from the store")
}
