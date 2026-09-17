package python

import (
	"context"
	"strings"
	"sync"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

func init() {
	pkgParser.Register(NewAnalyzer())
}

// Analyzer implements the parser.Analyzer interface for Python.
type Analyzer struct {
	ca *CodeAnalyzer
	// ponytail: one lock matches the shared collector; make collection local only if parallel parsing becomes useful.
	mu sync.Mutex
}

// NewAnalyzer creates a new Python analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		ca: NewCodeAnalyzer(),
	}
}

// Name returns "python".
func (a *Analyzer) Name() string {
	return "python"
}

// CanHandle returns true for .py files.
func (a *Analyzer) CanHandle(filePath string) bool {
	return strings.HasSuffix(strings.ToLower(filePath), ".py")
}

func (a *Analyzer) ReleaseResources() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ca.ReleaseResources()
}

// Analyze extracts symbols from a file or directory.
func (a *Analyzer) Analyze(ctx context.Context, path string) (*pkgParser.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chunks, err := a.ca.AnalyzePaths([]string{path})
	if err != nil {
		return nil, err
	}

	var symbols []pkgParser.Symbol
	for _, ch := range chunks {
		symbols = append(symbols, pkgParser.Symbol{
			Name:      ch.Name,
			Type:      pkgParser.SymbolType(ch.Type),
			Package:   ch.Package,
			Content:   ch.Code,
			Signature: ch.Signature,
			Docstring: ch.Docstring,
			StartLine: ch.StartLine,
			EndLine:   ch.EndLine,
			FilePath:  ch.FilePath,
			Language:  ch.Language,
			IsPublic:  len(ch.Name) > 0 && !strings.HasPrefix(ch.Name, "_"),
			Relations: ch.Relations,
			Metadata:  ch.Metadata,
		})
	}

	return &pkgParser.Result{
		Symbols:  symbols,
		Language: "python",
	}, nil
}
