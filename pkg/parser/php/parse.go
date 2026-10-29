package php

import (
	"bytes"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/parser"
)

// Parse wraps the VKCOM parser. Its scanner (v0.8.2) never terminates, or
// panics, when inline HTML ends with '<' followed only by line breaks, e.g.
// "x<" or "?>\n<\n" in a file being edited; memory then grows until the
// process is killed. A trailing space avoids that state without changing
// any symbol positions.
func Parse(content []byte, cfg conf.Config) (ast.Vertex, error) {
	if bytes.HasSuffix(bytes.TrimRight(content, "\r\n"), []byte("<")) {
		content = append(content[:len(content):len(content)], ' ')
	}
	return parser.Parse(content, cfg)
}
