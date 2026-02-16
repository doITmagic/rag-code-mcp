package html

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/PuerkitoBio/goquery"
	pkgParser "github.com/doITmagic/rag-code-mcp/v2/pkg/parser"
)

func init() {
	pkgParser.Register(NewAnalyzer())
}

// Analyzer implements the parser.Analyzer interface for HTML.
type Analyzer struct{}

// NewAnalyzer creates a new HTML analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{}
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
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var results []pkgParser.Symbol
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !a.CanHandle(p) {
				return nil
			}
			fileSymbols, _ := a.analyzeFile(p)
			results = append(results, fileSymbols...)
			return nil
		})
	} else {
		results, err = a.analyzeFile(path)
	}

	if err != nil {
		return nil, err
	}

	return &pkgParser.Result{
		Symbols:  results,
		Language: "html",
	}, nil
}

func (a *Analyzer) analyzeFile(path string) ([]pkgParser.Symbol, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	title := strings.TrimSpace(doc.Find("title").First().Text())
	
	var symbols []pkgParser.Symbol

	// Section extraction based on headings
	doc.Find("h1, h2, h3, h4, h5, h6").Each(func(i int, s *goquery.Selection) {
		tag := s.Nodes[0].Data
		level := headingLevel(tag)
		headingText := strings.TrimSpace(s.Text())
		
		// Find next heading or end of parent
		var bodyParts []string
		for next := s.Next(); next.Length() > 0 && headingLevel(next.Nodes[0].Data) == 0; next = next.Next() {
			bodyParts = append(bodyParts, normalizeWhitespace(next.Text()))
		}
		
		bodyText := strings.Join(bodyParts, "\n")
		codeBlocks := extractCodeBlocks(s)

		sym := pkgParser.Symbol{
			Name:      headingText,
			Type:      pkgParser.Type, // Sections are mapped to Type
			FilePath:  path,
			Language:  "html",
			Docstring: bodyText,
			Content:   buildSectionCode(headingText, bodyText, codeBlocks),
			Metadata: map[string]any{
				"html_tag":      tag,
				"heading_level": level,
			},
		}
		
		if title != "" {
			sym.Metadata["page_title"] = title
		}
		if id, ok := s.Attr("id"); ok && strings.TrimSpace(id) != "" {
			sym.Metadata["html_id"] = strings.TrimSpace(id)
		}
		if class, ok := s.Attr("class"); ok && strings.TrimSpace(class) != "" {
			sym.Metadata["html_class"] = strings.TrimSpace(class)
		}

		symbols = append(symbols, sym)
	})

	return symbols, nil
}

func headingLevel(tag string) int {
	if len(tag) == 2 && tag[0] == 'h' {
		switch tag[1] {
		case '1': return 1
		case '2': return 2
		case '3': return 3
		case '4': return 4
		case '5': return 5
		case '6': return 6
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
