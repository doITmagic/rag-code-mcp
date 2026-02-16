package golang

import (
	"context"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"strings"

	pkgParser "github.com/doITmagic/rag-code-mcp/v2/pkg/parser"
)

func init() {
	pkgParser.Register(NewAnalyzer())
}

// Analyzer implements the parser.Analyzer interface for Go.
type Analyzer struct {
	fset *token.FileSet
}

// NewAnalyzer creates a new Go analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{fset: token.NewFileSet()}
}

// Name returns "go".
func (a *Analyzer) Name() string {
	return "go"
}

// CanHandle returns true for .go files that are not tests.
func (a *Analyzer) CanHandle(filePath string) bool {
	return strings.HasSuffix(filePath, ".go") && !strings.HasSuffix(filePath, "_test.go")
}

// Analyze extracts symbols from a file or a directory (package).
func (a *Analyzer) Analyze(ctx context.Context, path string) (*pkgParser.Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var symbols []pkgParser.Symbol
	if info.IsDir() {
		pkgSymbols, err := a.analyzePackage(path)
		if err != nil {
			return nil, err
		}
		symbols = pkgSymbols
	} else {
		fileSymbols, err := a.analyzeFile(path)
		if err != nil {
			return nil, err
		}
		symbols = fileSymbols
	}

	return &pkgParser.Result{
		Symbols:  symbols,
		Language: "go",
	}, nil
}

func (a *Analyzer) analyzeFile(filePath string) ([]pkgParser.Symbol, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	return a.extractSymbolsFromAST(fset, []*ast.File{f}), nil
}

func (a *Analyzer) analyzePackage(dir string) ([]pkgParser.Symbol, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return a.CanHandle(fi.Name())
	}, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var allSymbols []pkgParser.Symbol
	for _, pkg := range pkgs {
		var files []*ast.File
		for _, f := range pkg.Files {
			files = append(files, f)
		}

		docPkg := doc.New(pkg, "./", doc.Mode(0))
		symbols := a.extractSymbolsFromDoc(fset, docPkg, files)
		allSymbols = append(allSymbols, symbols...)
	}

	return allSymbols, nil
}
