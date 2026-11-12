package web

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// onclick="toggleFilters()" in a page or Go template calls a browser global
// declared in a script file: a call to window.toggleFilters on the
// narrowest symbol around it.
func TestInlineHandlerCalls(t *testing.T) {
	src := `<html><body>
<div id="bar">
  <button onclick="toggleFilters()" {{if .X}}style="display:none"{{end}}>F</button>
  <button ONCLICK='if (ok) { resolveError({{.ID}}); } return false'>R</button>
</div>
<a href="#" onclick="event.preventDefault(); this.form.submit(); App.open(1)">x</a>
<input onchange = "save( this.value )" onkeyup="typeof f === 'function' && f()">
<p data-onclick="nope()" title="call()">t</p>
</body></html>
`
	syms := byQN(analyze(t, "/ws/t/page.html", src))
	assert.Equal(t, []rel{
		{"toggleFilters", "window.toggleFilters", 3},
		{"resolveError", "window.resolveError", 4},
	}, rels(syms["t/page.bar"], parser.RelCall))
	assert.Equal(t, []rel{
		{"save", "window.save", 7},
		{"f", "window.f", 7},
	}, rels(syms["t/page.page"], parser.RelCall))
}
