package html

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/PuerkitoBio/goquery"
	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

func init() {
	pkgParser.Register(NewAnalyzer())
}

// Analyzer implements the pkgParser.Analyzer interface for HTML.
type Analyzer struct {
	ca *CodeAnalyzer
}

// NewAnalyzer creates a new HTML analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		ca: NewCodeAnalyzer(),
	}
}

// Name returns "html".
func (a *Analyzer) Name() string {
	return "html"
}

// CanHandle returns true for .html and .htm files.
func (a *Analyzer) CanHandle(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".html" || ext == ".htm"
}

// Analyze extracts symbols (sections) from a file or directory.
func (a *Analyzer) Analyze(ctx context.Context, path string) (*pkgParser.Result, error) {
	chunks, err := a.ca.AnalyzePaths([]string{path})
	if err != nil {
		return nil, err
	}

	var symbols []pkgParser.Symbol
	for _, ch := range chunks {
		symbols = append(symbols, pkgParser.Symbol{
			Name:      ch.Name,
			Type:      pkgParser.SymbolType(ch.Type),
			FilePath:  ch.FilePath,
			Language:  "html",
			Docstring: ch.Docstring,
			Content:   ch.Content,
			Signature: ch.Signature,
			Metadata:  ch.Metadata,
		})
	}

	return &pkgParser.Result{
		Symbols:  symbols,
		Language: "html",
	}, nil
}

// CodeAnalyzer handles the heavy lifting of HTML analysis.
type CodeAnalyzer struct{}

func NewCodeAnalyzer() *CodeAnalyzer {
	return &CodeAnalyzer{}
}

func (ca *CodeAnalyzer) AnalyzePaths(paths []string) ([]CodeChunk, error) {
	var chunks []CodeChunk

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
					if ca.shouldSkipDir(path, root) {
						return filepath.SkipDir
					}
					return nil
				}
				if !ca.isHTMLFile(d.Name()) {
					return nil
				}
				fileChunks, ferr := ca.analyzeFile(path)
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

		if !ca.isHTMLFile(root) {
			continue
		}
		fileChunks, err := ca.analyzeFile(root)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, fileChunks...)
	}

	return chunks, nil
}

func (ca *CodeAnalyzer) analyzeFile(path string) ([]CodeChunk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("html analyzer: read %s: %w", path, err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("html analyzer: parse %s: %w", path, err)
	}

	title := strings.TrimSpace(doc.Find("title").First().Text())
	sections := ca.buildSections(doc, path, title)
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

	chunk := CodeChunk{
		Type:      "type", // Mapped to section/type
		Name:      name,
		Language:  "html",
		FilePath:  path,
		Signature: title,
		Docstring: bodyText,
		Content:   bodyText,
	}
	if title != "" {
		chunk.Metadata = map[string]any{"page_title": title}
	}
	return []CodeChunk{chunk}, nil
}

func (ca *CodeAnalyzer) buildSections(doc *goquery.Document, path, pageTitle string) []CodeChunk {
	headingSelector := "h1,h2,h3,h4,h5,h6"
	body := doc.Find("body")
	if body.Length() == 0 {
		body = doc.Selection
	}

	var chunks []CodeChunk
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

		chunk := CodeChunk{
			Type:      "type", // Map sections to type for consistency
			Name:      title,
			Language:  "html",
			FilePath:  path,
			Signature: fmt.Sprintf("<h%d>%s</h%d>", level, title, level),
			Docstring: bodyText,
			Content:   buildSectionCode(title, bodyText, codeBlocks),
			Metadata:  metadata,
		}
		chunks = append(chunks, chunk)
	})

	return chunks
}

func (ca *CodeAnalyzer) shouldSkipDir(path, root string) bool {
	if path == root {
		return false
	}
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return true
	}
	switch base {
	case "node_modules", "vendor", "dist", "build":
		return true
	default:
		return false
	}
}

func (ca *CodeAnalyzer) isHTMLFile(name string) bool {
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
