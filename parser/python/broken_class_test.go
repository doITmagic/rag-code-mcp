package python

import (
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
)

// A class whose last method is cut off mid-edit: its methods may be lost
// (then the file is an error, not an empty file), but none may come out as a
// module-level function.
func TestBrokenClassMethodIsNoFunction(t *testing.T) {
	src := "class A:\n    def m(self):\n        pass\n    def n(self):\n        x = (\n\nclass B:\n    def k(self): pass\n"
	syms, err := analyzer{}.Analyze(parser.File{Path: "/ws/a.py", Root: "/ws", Source: []byte(src), Language: "python"})
	assert.True(t, err != nil || len(syms) > 0, "no symbols and no error")
	for _, s := range syms {
		if owner, ok := map[string]string{"m": "A", "n": "A", "k": "B"}[s.Name]; ok {
			assert.Equal(t, owner, s.Owner, "%s is a method of %s, got %s %s", s.Name, owner, s.Kind, s.QualifiedName)
		}
	}
}
