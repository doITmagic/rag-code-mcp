package css

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
)

func init() {
	parser.Register(NewAnalyzer())
}

// Analyzer implementeaza procesarea pe bucati (chunk-based) a fisierelor CSS/SCSS/LESS.
// Fara sa depinda de Tree-sitter, nu face OOM nici macar la bundle-uri gigantice.
type Analyzer struct{}

func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

func (a *Analyzer) Name() string {
	return "css"
}

func (a *Analyzer) CanHandle(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".css" || ext == ".scss" || ext == ".less" || ext == ".sass"
}

func (a *Analyzer) Analyze(ctx context.Context, path string) (*parser.Result, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read css: %w", err)
	}

	text := string(content)
	if strings.TrimSpace(text) == "" {
		return &parser.Result{Language: "css"}, nil
	}

	var symbols []parser.Symbol

	var selectorBuilder strings.Builder
	var contentBuilder strings.Builder
	braceDepth := 0
	startLine := 1
	currentLine := 1
	inComment := false

	baseName := filepath.Base(path)

	for i := 0; i < len(text); i++ {
		c := text[i]

		if c == '\n' {
			currentLine++
		}

		// Skip over slash-star comments
		if !inComment && c == '/' && i+1 < len(text) && text[i+1] == '*' {
			inComment = true
			i++ // skip '*'
			continue
		}
		if inComment && c == '*' && i+1 < len(text) && text[i+1] == '/' {
			inComment = false
			i++ // skip '/'
			continue
		}
		if inComment {
			continue
		}

		if c == '{' {
			if braceDepth == 0 {
				contentBuilder.WriteByte(c)
			} else {
				contentBuilder.WriteByte(c)
			}
			braceDepth++
			continue
		}

		if c == '}' {
			braceDepth--
			contentBuilder.WriteByte(c)

			if braceDepth == 0 {
				selector := strings.TrimSpace(selectorBuilder.String())
				if len(selector) > 200 {
					selector = selector[:197] + "..."
				}

				blockContent := strings.TrimSpace(contentBuilder.String())
				if len(blockContent) > 8192 {
					blockContent = blockContent[:8192] + "\n...[TRUNCATED]"
				}

				if selector != "" {
					symbols = append(symbols, parser.Symbol{
						Name:      baseName,
						Type:      "style_rule",
						FilePath:  path,
						Language:  "css",
						Content:   selector + " " + blockContent,
						Signature: selector,
						StartLine: startLine,
						EndLine:   currentLine,
						IsPublic:  true,
						Metadata: map[string]interface{}{
							"selector": selector,
						},
					})
				}

				selectorBuilder.Reset()
				contentBuilder.Reset()
				startLine = currentLine
			}
			continue
		}

		if braceDepth == 0 {
			selectorBuilder.WriteByte(c)
		} else {
			contentBuilder.WriteByte(c)
		}
	}

	// Any leftover content (like variables at the root without braces)
	leftover := strings.TrimSpace(selectorBuilder.String())
	if leftover != "" && len(symbols) == 0 {
		if len(leftover) > 8192 {
			leftover = leftover[:8192] + "\n...[TRUNCATED]"
		}
		symbols = append(symbols, parser.Symbol{
			Name:      baseName,
			Type:      "style_rule",
			FilePath:  path,
			Language:  "css",
			Content:   leftover,
			Signature: "global",
			StartLine: startLine,
			EndLine:   currentLine,
			IsPublic:  true,
		})
	}

	return &parser.Result{
		Symbols:  symbols,
		Language: "css",
	}, nil
}
