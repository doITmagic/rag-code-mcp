package javascript

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A raw NUL byte inside a regex or a template literal is valid JavaScript
// (listmonk's content.js uses it as a sentinel): every declaration around it
// is indexed.
func TestRawNULByteKeepsDeclarations(t *testing.T) {
	for name, src := range map[string]string{
		"regex":    "function f(s) { return s.replace(/\x00(\\d+)\x00/g, g) }\nfunction g() {}\n",
		"class":    "function first() {}\nclass Conv { strip(s) { return s.replace(/\x00/g, '') } }\nexport const strip = (s) => new Conv().strip(s)\n",
		"template": "function f(n) { return `\x00${n}\x00` }\nfunction g() { return f(1) }\n",
	} {
		syms := analyze(t, "src/"+name+".js", src)
		for _, want := range map[string][]string{
			"regex": {"f", "g"}, "class": {"first", "Conv", "Conv.strip", "strip"}, "template": {"f", "g"},
		}[name] {
			find(t, syms, "src/"+name+"."+want)
		}
	}
	g := find(t, analyze(t, "src/t.js", "function f(n) { return `\x00${n}` }\nfunction g() { return f(1) }\n"), "src/t.g")
	require.Len(t, g.Relations, 1)
	assert.Equal(t, 2, g.Relations[0].Line)
}
