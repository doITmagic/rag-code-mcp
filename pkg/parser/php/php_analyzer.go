package php

import (
	"context"
	"strings"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

func init() {
	pkgParser.Register(NewAnalyzer())
}

// Analyzer implements the parser.Analyzer interface for PHP.
type Analyzer struct {
	codeAnalyzer *CodeAnalyzer
}

// NewAnalyzer creates a new PHP analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		codeAnalyzer: NewCodeAnalyzer(),
	}
}

// Name returns "php".
func (a *Analyzer) Name() string {
	return "php"
}

// CanHandle returns true for .php files.
func (a *Analyzer) CanHandle(filePath string) bool {
	return strings.HasSuffix(filePath, ".php")
}

// Analyze extracts symbols from a file or directory.
func (a *Analyzer) Analyze(ctx context.Context, path string) (*pkgParser.Result, error) {
	chunks, err := a.codeAnalyzer.AnalyzePaths([]string{path})
	if err != nil {
		return nil, err
	}

	symbols := make([]pkgParser.Symbol, len(chunks))
	for i, chunk := range chunks {
		// PHP: methods can be private/protected — read visibility from metadata if available,
		// fall back to "not starting with _" convention.
		isPublic := true
		if vis, ok := chunk.Metadata["visibility"].(string); ok {
			isPublic = vis == "public" || vis == ""
		}
		symbols[i] = pkgParser.Symbol{
			Name:      chunk.Name,
			Type:      pkgParser.SymbolType(chunk.Type),
			Package:   chunk.Package,
			Content:   chunk.Code,
			Signature: chunk.Signature,
			Docstring: chunk.Docstring,
			StartLine: chunk.StartLine,
			EndLine:   chunk.EndLine,
			FilePath:  chunk.FilePath,
			Language:  "php",
			IsPublic:  isPublic,
			Relations: a.mapRelations(chunk.Relations),
			Metadata:  chunk.Metadata,
		}
	}

	return &pkgParser.Result{
		Symbols:  symbols,
		Language: "php",
	}, nil
}

func (a *Analyzer) mapRelations(phpRels []Relation) []pkgParser.Relation {
	if len(phpRels) == 0 {
		return nil
	}
	res := make([]pkgParser.Relation, len(phpRels))
	for i, r := range phpRels {
		res[i] = pkgParser.Relation{
			TargetName: r.TargetName,
			Type:       pkgParser.RelationType(r.Type),
		}
	}
	return res
}
