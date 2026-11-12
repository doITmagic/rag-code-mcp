package parser_test

import (
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/gdscript"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/golang"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/javascript"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/php"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/python"
)

// A line of unclosed brackets makes error recovery wrap the whole file in
// one ERROR node: the declaration before it must still be a symbol (it used
// to be a file without symbols and without an error). Past the limit the
// file is refused with an error instead of costing hundreds of MB.
func TestBrokenFileKeepsDeclarationsOrFails(t *testing.T) {
	cases := map[string]string{
		"a.go":  "package a\n\nfunc first() {}\n\nvar x = f%s\n",
		"a.php": "<?php\nfunction first() {}\n$x = g%s;\n",
		"a.py":  "def first():\n    pass\n\n\nx = f%s\n",
		"a.js":  "function first(){}\nconst x = f%s\n",
		"a.gd":  "func first():\n\tpass\n\nvar x = f%s\n",
	}
	for name, tpl := range cases {
		for _, br := range []string{"(", "[", "{"} {
			src := strings.Replace(tpl, "%s", strings.Repeat(br, 200), 1)
			syms, err := parser.Analyze(parser.File{Path: "/ws/" + name, Root: "/ws", Source: []byte(src)})
			found := false
			for _, s := range syms {
				found = found || s.Name == "first"
			}
			if err != nil || !found {
				t.Errorf("%s with 200 unclosed %s: symbol first lost (%d symbols, err %v)", name, br, len(syms), err)
			}
			src = strings.Replace(tpl, "%s", strings.Repeat(br, 5000), 1)
			_, err = parser.Analyze(parser.File{Path: "/ws/" + name, Root: "/ws", Source: []byte(src)})
			// the parse runs first: on a slow run (-race) its timeout is the error
			if err == nil || !strings.Contains(err.Error(), "brackets") && !strings.Contains(err.Error(), "parse stopped") {
				t.Errorf("%s with 5000 unclosed %s: want a brackets or timeout error, got %v", name, br, err)
			}
		}
	}
}

// Unclosed brackets in the second method of a class shred the class: error
// recovery leaves no declaration node, so the file is an error instead of a
// file without symbols and without an error.
func TestBrokenClassMethodIsNoSilentEmptyFile(t *testing.T) {
	cases := map[string]string{
		"svc.ts": "export class Svc {\n  first(): number { return 1; }\n  broken() { const x = f" + strings.Repeat("(", 200) + "\n}\n",
		"svc.js": "class Svc {\n  first() { return 1; }\n  broken() { const x = f" + strings.Repeat("(", 200) + "\n}\n",
	}
	for name, src := range cases {
		syms, err := parser.Analyze(parser.File{Path: "/ws/" + name, Root: "/ws", Source: []byte(src)})
		if err == nil && len(syms) == 0 {
			t.Errorf("%s: no symbols and no error", name)
		}
	}
}
