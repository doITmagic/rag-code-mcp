package html

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

// cssRuleRe matches a CSS selector followed by an opening brace.
// It captures everything before the '{' as the selector.
var cssRuleRe = regexp.MustCompile(`^([^{/]+)\{`)

// analyzeCSSRegex extracts CSS selectors using simple line scanning.
// This replaces the tree-sitter approach which caused GLR memory explosion.
func (a *Analyzer) analyzeCSSRegex(path string) (*pkgParser.Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("css read %s: %w", path, err)
	}
	defer f.Close()

	baseName := filepath.Base(path)
	var symbols []pkgParser.Symbol
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024) // handle long lines

	lineNum := 0
	braceDepth := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments.
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		// Track brace depth so we only capture top-level selectors.
		openBraces := strings.Count(trimmed, "{")
		closeBraces := strings.Count(trimmed, "}")

		if braceDepth == 0 && openBraces > 0 {
			m := cssRuleRe.FindStringSubmatch(trimmed)
			if m != nil {
				selector := strings.TrimSpace(m[1])
				if selector != "" && len(selector) < 500 {
					// Content is the whole rule — but we cap it.
					content := trimmed
					if len(content) > 4096 {
						content = content[:4096] + "\n...[TRUNCATED]"
					}

					symbols = append(symbols, pkgParser.Symbol{
						Name:      selector,
						Type:      "css_rule",
						FilePath:  path,
						Language:  "html",
						Content:   content,
						Signature: selector,
						StartLine: lineNum,
						EndLine:   lineNum,
						IsPublic:  true,
						Metadata: map[string]interface{}{
							"selector":  selector,
							"node_type": "rule_set",
							"file":      baseName,
						},
					})
				}
			}
		}

		braceDepth += openBraces - closeBraces
		if braceDepth < 0 {
			braceDepth = 0
		}
	}

	return &pkgParser.Result{
		Symbols:  symbols,
		Language: "html",
	}, nil
}
