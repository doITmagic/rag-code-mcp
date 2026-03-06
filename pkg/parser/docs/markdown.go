package docs

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"gitlab.com/golang-commonmark/markdown"
)

type MarkdownParser struct {
	md *markdown.Markdown
}

func NewMarkdownParser() *MarkdownParser {
	return &MarkdownParser{
		md: markdown.New(markdown.HTML(true)),
	}
}

func (p *MarkdownParser) Parse(source []byte, filePath string) ([]parser.Symbol, error) {
	tokens := p.md.Parse(source)

	var symbols []parser.Symbol
	var currentHeading string

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]

		switch t := tok.(type) {
		case *markdown.HeadingOpen:
			// The next token is usually the inline containing the text for the heading
			headingText := ""
			if i+1 < len(tokens) {
				if inline, ok := tokens[i+1].(*markdown.Inline); ok {
					headingText = inline.Content
				}
			}
			hashes := strings.Repeat("#", t.HLevel)
			currentHeading = fmt.Sprintf("%s %s", hashes, headingText)

		case *markdown.ParagraphOpen:
			// The next token is the inline text content
			paraText := ""
			if i+1 < len(tokens) {
				if inline, ok := tokens[i+1].(*markdown.Inline); ok {
					paraText = inline.Content
				}
			}

			if paraText != "" {
				startLine, endLine := p.getLines(t.Map)
				symbols = append(symbols, parser.Symbol{
					Name:      filepath.Base(filePath),
					Type:      "documentation",
					Package:   "",
					Content:   paraText,
					Signature: currentHeading,
					Docstring: "",
					StartLine: startLine,
					EndLine:   endLine,
					FilePath:  filePath,
					Language:  "markdown",
					IsPublic:  true,
					Metadata: map[string]interface{}{
						"chunk_type":      "markdown",
						"section_heading": currentHeading,
					},
				})
			}

		case *markdown.Fence:
			lang := t.Params
			content := t.Content
			startLine, endLine := p.getLines(t.Map)

			sig := currentHeading
			if sig == "" {
				if lang != "" {
					sig = "Code Block (" + lang + ")"
				} else {
					sig = "Code Block"
				}
			}

			fullContent := fmt.Sprintf("```%s\n%s```", lang, strings.TrimRight(content, "\n"))

			symbols = append(symbols, parser.Symbol{
				Name:      filepath.Base(filePath),
				Type:      "code_block",
				Package:   "",
				Content:   fullContent,
				Signature: sig,
				Docstring: "",
				StartLine: startLine,
				EndLine:   endLine,
				FilePath:  filePath,
				Language:  "markdown",
				IsPublic:  true,
				Metadata: map[string]interface{}{
					"chunk_type":      "markdown",
					"code_language":   lang,
					"section_heading": currentHeading,
				},
			})
		}
	}

	return symbols, nil
}

func (p *MarkdownParser) getLines(maps [2]int) (int, int) {
	// Source maps are 0-indexed, we convert to 1-indexed for MCP standards
	return maps[0] + 1, maps[1] + 1
}
