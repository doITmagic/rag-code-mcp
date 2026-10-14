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
	var globalDecls []string
	globalStartLine := 0
	braceDepth := 0
	startLine := 0
	currentLine := 1
	inComment := false
	inLineComment := false
	var inQuote byte

	baseName := filepath.Base(path)

	for i := 0; i < len(text); i++ {
		c := text[i]

		if c == '\n' {
			currentLine++
			if inLineComment {
				inLineComment = false
				continue
			}
		}

		if inLineComment {
			continue
		}

		// Handle block comments when not inside a string literal
		if inQuote == 0 {
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
		}
		if inComment {
			continue
		}

		// Handle SCSS/LESS // line comments when not inside a string or url(http://...)
		if inQuote == 0 && c == '/' && i+1 < len(text) && text[i+1] == '/' {
			if i == 0 || text[i-1] != ':' {
				inLineComment = true
				i++ // skip second '/'
				continue
			}
		}

		// Track string literals so braces/semicolons/comments inside quotes are not misinterpreted
		if inQuote != 0 {
			if c == '\\' && i+1 < len(text) {
				if braceDepth == 0 {
					selectorBuilder.WriteByte(c)
					selectorBuilder.WriteByte(text[i+1])
				} else {
					contentBuilder.WriteByte(c)
					contentBuilder.WriteByte(text[i+1])
				}
				if text[i+1] == '\n' {
					currentLine++
				}
				i++
				continue
			}
			if c == inQuote {
				inQuote = 0
			}
			if braceDepth == 0 {
				selectorBuilder.WriteByte(c)
			} else {
				contentBuilder.WriteByte(c)
			}
			continue
		} else if c == '"' || c == '\'' {
			inQuote = c
			if braceDepth == 0 {
				if selectorBuilder.Len() == 0 {
					startLine = currentLine
				}
				selectorBuilder.WriteByte(c)
			} else {
				contentBuilder.WriteByte(c)
			}
			continue
		}

		if c == '{' {
			contentBuilder.WriteByte(c)
			braceDepth++
			continue
		}

		if c == '}' {
			if braceDepth <= 0 {
				braceDepth = 0
				selectorBuilder.Reset()
				contentBuilder.Reset()
				startLine = 0
				continue
			}
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
					if startLine == 0 {
						startLine = currentLine
					}
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
				startLine = 0
			}
			continue
		}

		if braceDepth == 0 {
			if c == ';' {
				decl := strings.TrimSpace(selectorBuilder.String())
				if decl != "" {
					if globalStartLine == 0 {
						if startLine > 0 {
							globalStartLine = startLine
						} else {
							globalStartLine = currentLine
						}
					}
					globalDecls = append(globalDecls, decl+";")
				}
				selectorBuilder.Reset()
				startLine = 0
				continue
			}
			if selectorBuilder.Len() == 0 {
				if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
					continue
				}
				startLine = currentLine
			}
			selectorBuilder.WriteByte(c)
		} else {
			contentBuilder.WriteByte(c)
		}
	}

	// Any leftover content (like variables at the root without braces)
	leftover := strings.TrimSpace(selectorBuilder.String())
	if len(symbols) == 0 {
		var parts []string
		if len(globalDecls) > 0 {
			parts = append(parts, strings.Join(globalDecls, "\n"))
		}
		if leftover != "" {
			parts = append(parts, leftover)
		}
		combined := strings.TrimSpace(strings.Join(parts, "\n"))
		if combined != "" {
			if len(combined) > 8192 {
				combined = combined[:8192] + "\n...[TRUNCATED]"
			}
			firstLine := startLine
			if globalStartLine > 0 {
				firstLine = globalStartLine
			}
			if firstLine == 0 {
				firstLine = 1
			}
			symbols = append(symbols, parser.Symbol{
				Name:      baseName,
				Type:      "style_rule",
				FilePath:  path,
				Language:  "css",
				Content:   combined,
				Signature: "global",
				StartLine: firstLine,
				EndLine:   currentLine,
				IsPublic:  true,
			})
		}
	}

	return &parser.Result{
		Symbols:  symbols,
		Language: "css",
	}, nil
}
