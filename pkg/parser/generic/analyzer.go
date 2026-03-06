package generic

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

func init() {
	// Register for file types that don't have specialized AST parsers.
	// NOTE: .ts, .jsx, .tsx, .vue are intentionally excluded — they are
	// handled by the dedicated javascript package (pkg/parser/javascript).
	exts := []string{".txt", ".csv", ".log", ".env", ".gitignore", ".dockerignore", ".bash"}
	pkgParser.Register(NewAnalyzer("generic", exts))
}

// Analyzer implements the parser.Analyzer interface for generic line-based chunking.
type Analyzer struct {
	name       string
	extensions []string
	chunkSize  int
}

// NewAnalyzer creates a new generic analyzer.
func NewAnalyzer(name string, extensions []string) *Analyzer {
	return &Analyzer{
		name:       name,
		extensions: extensions,
		chunkSize:  100,
	}
}

// Name returns the analyzer name.
func (a *Analyzer) Name() string {
	return a.name
}

// CanHandle returns true if the extension matches.
func (a *Analyzer) CanHandle(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	for _, e := range a.extensions {
		if e == ext {
			return true
		}
	}
	return false
}

// Analyze extracts basic segments from a file.
func (a *Analyzer) Analyze(ctx context.Context, path string) (*pkgParser.Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var symbols []pkgParser.Symbol
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !a.CanHandle(p) {
				return nil
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
		Language: "generic",
	}, nil
}

func (a *Analyzer) analyzeFile(path string) ([]pkgParser.Symbol, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var symbols []pkgParser.Symbol
	var currentLines []string
	startLine := 1
	lineNum := 0

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineNum++
		currentLines = append(currentLines, scanner.Text())

		if len(currentLines) >= a.chunkSize {
			symbols = append(symbols, a.createSymbol(path, currentLines, startLine, lineNum))
			currentLines = nil
			startLine = lineNum + 1
		}
	}

	if len(currentLines) > 0 {
		symbols = append(symbols, a.createSymbol(path, currentLines, startLine, lineNum))
	}

	return symbols, nil
}

func (a *Analyzer) createSymbol(path string, lines []string, start, end int) pkgParser.Symbol {
	content := strings.Join(lines, "\n")
	return pkgParser.Symbol{
		Name:      filepath.Base(path),
		Type:      pkgParser.Type, // Generic chunk
		Content:   content,
		StartLine: start,
		EndLine:   end,
		FilePath:  path,
		Language:  "generic",
		Metadata: map[string]any{
			"extension": filepath.Ext(path),
		},
	}
}
