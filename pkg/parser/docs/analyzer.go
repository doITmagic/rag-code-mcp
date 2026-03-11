package docs

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

type Analyzer struct {
	mdParser *MarkdownParser
	tsParser *TreeSitterParser
}

func NewAnalyzer() *Analyzer {
	return &Analyzer{
		mdParser: NewMarkdownParser(),
		tsParser: NewTreeSitterParser(),
	}
}

func (a *Analyzer) Name() string {
	return "docs"
}

func (a *Analyzer) CanHandle(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	// Markdown
	case ".md", ".markdown":
		return true
	// Tree-sitter supported structured / config / markup
	case ".yaml", ".yml", ".json", ".xml", ".toml", ".rst":
		return true
	default:
		return false
	}
}

func (a *Analyzer) Analyze(ctx context.Context, path string) (*parser.Result, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}

	if len(strings.TrimSpace(string(content))) == 0 {
		return &parser.Result{Language: "docs"}, nil
	}

	ext := strings.ToLower(filepath.Ext(path))
	var symbols []parser.Symbol

	if ext == ".md" || ext == ".markdown" {
		symbols, err = a.mdParser.Parse(content, path)
	} else {
		// Try treesitter for yaml, json, xml, toml, rst
		symbols, err = a.tsParser.Parse(content, path, ext)
	}

	if err != nil {
		return nil, fmt.Errorf("parse error %s: %w", path, err)
	}

	return &parser.Result{
		Symbols:  symbols,
		Language: "docs", // Common language category
	}, nil
}
