package parser_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/docs"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/golang"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/javascript"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/php"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/python"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/web"
)

// Brackets inside a string, a comment or text are no nesting: a valid file
// with 1001 of them keeps its symbols.
func TestLiteralBracketsAreNotNesting(t *testing.T) {
	root := t.TempDir()
	brackets := strings.Repeat("[", 1001)
	cases := map[string]string{
		"literal.go":   "package p\nconst Text = \"" + brackets + "\"\nfunc Keep() {}\n",
		"literal.js":   "const text = \"" + brackets + "\"; function keep() {}\n",
		"literal.php":  "<?php\nfunction keep() { return '" + brackets + "'; }\n",
		"literal.py":   "text = '" + brackets + "'\ndef keep():\n    pass\n",
		"literal.html": "<main id=\"keep\"><p>" + brackets + "</p></main>\n",
		"literal.json": "{\"keep\":\"" + brackets + "\"}\n",
		"literal.md":   "# Keep\n\n```text\n" + brackets + "\n```\n",
		"comment.go":   "package p\n// " + brackets + "\nfunc Keep() {}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			syms, err := parser.Analyze(parser.File{Path: filepath.Join(root, name), Root: root, Source: []byte(src)})
			if err != nil {
				t.Fatalf("valid file (%d bytes) rejected: %v", len(src), err)
			}
			if len(syms) == 0 {
				t.Fatal("valid file lost all symbols")
			}
		})
	}
}
