package parser_test

import (
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/css"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/generic"
	golang "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/html"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/javascript"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/python"
)

// Extensions are matched case-insensitively by every analyzer: Windows
// filesystems are, and F.PHP / N.JS were silently skipped. Analyzers are
// built directly because TestRegistry resets the global registry.
func TestCanHandleIgnoresExtensionCase(t *testing.T) {
	analyzers := []parser.Analyzer{
		golang.NewCodeAnalyzer(), php.NewAnalyzer(), python.NewAnalyzer(), javascript.NewCodeAnalyzer(),
		css.NewAnalyzer(), docs.NewAnalyzer(), html.NewAnalyzer(),
		generic.NewAnalyzer("generic", []string{".txt", ".env"}),
	}
	find := func(file string) string {
		for _, a := range analyzers {
			if a.CanHandle(file) {
				return a.Name()
			}
		}
		return ""
	}
	cases := map[string]string{
		"a.GO": "go", "b.PHP": "php", "c.PY": "python", "d.JS": "javascript", "e.TSX": "javascript",
		"f.HTML": "html", "g.CSS": "css", "h.MD": "docs", "i.TXT": "generic", "j.ENV": "generic",
		"k_test.GO": "go", "l.rb": "",
	}
	for file, want := range cases {
		if got := find(file); got != want {
			t.Errorf("%q handled by %q, want %q", file, got, want)
		}
	}
}
