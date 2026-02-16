package python

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	pkgParser "github.com/doITmagic/rag-code-mcp/v2/pkg/parser"
)

func init() {
	pkgParser.Register(NewAnalyzer())
}

// Analyzer implements the parser.Analyzer interface for Python.
type Analyzer struct {
	includeTests bool
}

// NewAnalyzer creates a new Python analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		includeTests: false,
	}
}

// Name returns "python".
func (a *Analyzer) Name() string {
	return "python"
}

// CanHandle returns true for .py files.
func (a *Analyzer) CanHandle(filePath string) bool {
	return strings.HasSuffix(filePath, ".py")
}

// Analyze extracts symbols from a file or directory.
func (a *Analyzer) Analyze(ctx context.Context, path string) (*pkgParser.Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var symbols []pkgParser.Symbol
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				// Skip common directories
				base := d.Name()
				if base == ".git" || base == "__pycache__" || base == ".venv" ||
					base == "venv" || base == "env" || base == ".env" ||
					base == "node_modules" || strings.HasPrefix(base, ".") {
					return filepath.SkipDir
				}
				return nil
			}

			if !a.CanHandle(p) {
				return nil
			}

			// Skip test files unless enabled
			if !a.includeTests {
				if strings.HasPrefix(d.Name(), "test_") || strings.HasSuffix(d.Name(), "_test.py") {
					return nil
				}
			}

			fileSymbols, _ := a.analyzeFile(p)
			symbols = append(symbols, fileSymbols...)
			return nil
		})
	} else {
		symbols, err = a.analyzeFile(path)
	}

	if err != nil {
		return nil, err
	}

	return &pkgParser.Result{
		Symbols:  symbols,
		Language: "python",
	}, nil
}

func (a *Analyzer) analyzeFile(path string) ([]pkgParser.Symbol, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	extractor := &extractor{
		filePath:    path,
		fileContent: content,
	}

	return extractor.extract(), nil
}
