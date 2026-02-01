package html

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

// CodeAnalyzer implements codetypes.PathAnalyzer for HTML documents.
type CodeAnalyzer struct{}

// NewCodeAnalyzer creates a new HTML analyzer instance.
func NewCodeAnalyzer() *CodeAnalyzer {
	return &CodeAnalyzer{}
}

// AnalyzePaths walks provided paths and extracts sections from HTML files.
func (a *CodeAnalyzer) AnalyzePaths(paths []string) ([]codetypes.CodeChunk, error) {
	var chunks []codetypes.CodeChunk

	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("html analyzer: stat %s: %w", root, err)
		}

		if info.IsDir() {
			err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if shouldSkipDir(path, root) {
						return filepath.SkipDir
					}
					return nil
				}
				if !isHTMLFile(d.Name()) {
					return nil
				}
				fileChunks, ferr := a.analyzeFile(path)
				if ferr != nil {
					return ferr
				}
				chunks = append(chunks, fileChunks...)
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}

		if !isHTMLFile(root) {
			continue
		}
		fileChunks, err := a.analyzeFile(root)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, fileChunks...)
	}

	return chunks, nil
}

func (a *CodeAnalyzer) analyzeFile(path string) ([]codetypes.CodeChunk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("html analyzer: read %s: %w", path, err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("html analyzer: parse %s: %w", path, err)
	}

	title := strings.TrimSpace(doc.Find("title").First().Text())
	sections := buildSections(doc, path, title)
	if len(sections) > 0 {
		return sections, nil
	}

	// Fallback: treat entire body as a single chunk.
	body := doc.Find("body")
	if body.Length() == 0 {
		body = doc.Selection
	}

	bodyText := normalizeWhitespace(body.Text())
	if bodyText == "" {
		return nil, nil
	}

	name := title
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}

	chunk := codetypes.CodeChunk{
		Type:      "file",
		Name:      name,
		Language:  "html",
		FilePath:  path,
		Signature: title,
		Docstring: bodyText,
		Code:      bodyText,
	}
	if title != "" {
		chunk.Metadata = map[string]any{"page_title": title}
	}
	return []codetypes.CodeChunk{chunk}, nil
}

func buildSections(doc *goquery.Document, path, pageTitle string) []codetypes.CodeChunk {
	headingSelector := "h1,h2,h3,h4,h5,h6"
	body := doc.Find("body")
	if body.Length() == 0 {
		body = doc.Selection
	}

	var chunks []codetypes.CodeChunk
	body.Find(headingSelector).Each(func(i int, sel *goquery.Selection) {
		title := strings.TrimSpace(sel.Text())
		if title == "" {
			title = fmt.Sprintf("Section %d", i+1)
		}

		level := headingLevel(goquery.NodeName(sel))
		if level == 0 {
			level = 1
		}

		content := sel.NextUntil(headingSelector)
		bodyText := normalizeWhitespace(content.Text())
		codeBlocks := extractCodeBlocks(content)

		if bodyText == "" && len(codeBlocks) == 0 {
			return
		}

		metadata := map[string]any{
			"heading_level": level,
		}
		if pageTitle != "" {
			metadata["page_title"] = pageTitle
		}
		if id, ok := sel.Attr("id"); ok && strings.TrimSpace(id) != "" {
			metadata["html_id"] = strings.TrimSpace(id)
		}
		if class, ok := sel.Attr("class"); ok && strings.TrimSpace(class) != "" {
			metadata["class"] = strings.TrimSpace(class)
		}
		if len(codeBlocks) > 0 {
			metadata["code_blocks"] = codeBlocks
		}

		chunk := codetypes.CodeChunk{
			Type:      "section",
			Name:      title,
			Language:  "html",
			FilePath:  path,
			Signature: fmt.Sprintf("<h%d>%s</h%d>", level, title, level),
			Docstring: bodyText,
			Code:      buildSectionCode(title, bodyText, codeBlocks),
			Metadata:  metadata,
		}
		chunks = append(chunks, chunk)
	})

	return chunks
}

func shouldSkipDir(path, root string) bool {
	if path == root {
		return false
	}
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return true
	}
	switch base {
	case "node_modules", "vendor", "dist", "build", "public", "tmp":
		return true
	default:
		return false
	}
}

func isHTMLFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm")
}

func headingLevel(tag string) int {
	if len(tag) == 2 && tag[0] == 'h' {
		switch tag[1] {
		case '1':
			return 1
		case '2':
			return 2
		case '3':
			return 3
		case '4':
			return 4
		case '5':
			return 5
		case '6':
			return 6
		}
	}
	return 0
}

func normalizeWhitespace(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

func extractCodeBlocks(sel *goquery.Selection) []string {
	var blocks []string
	sel.Find("pre,code").Each(func(_ int, s *goquery.Selection) {
		block := normalizeWhitespace(s.Text())
		if block != "" {
			blocks = append(blocks, block)
		}
	})
	return blocks
}

func buildSectionCode(title, body string, codeBlocks []string) string {
	var parts []string
	if title != "" {
		parts = append(parts, title)
	}
	if body != "" {
		parts = append(parts, body)
	}
	if len(codeBlocks) > 0 {
		parts = append(parts, strings.Join(codeBlocks, "\n\n"))
	}
	return strings.Join(parts, "\n\n")
}
