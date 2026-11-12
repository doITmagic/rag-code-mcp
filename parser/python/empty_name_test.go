package python

import (
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// Malformed Python where error recovery produces an assignment-like node
// without a name (found by fuzzing) must not yield a nameless symbol.
func TestMalformedAssignmentHasNoNamelessSymbol(t *testing.T) {
	src := "``T:s:l\n:a"
	f := parser.File{Path: "/ws/api.py", Root: "/ws", Source: []byte(src), Language: "python"}
	syms, err := analyzer{}.Analyze(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Name == "" {
			t.Errorf("symbol with empty name: kind=%s qn=%q lines %d-%d content=%q", s.Kind, s.QualifiedName, s.StartLine, s.EndLine, s.Content)
		}
	}
}
