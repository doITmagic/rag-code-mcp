package php

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"

	pkgParser "github.com/doITmagic/rag-code-mcp/v2/pkg/parser"
)

func init() {
	pkgParser.Register(NewAnalyzer())
}

// Analyzer implements the parser.Analyzer interface for PHP.
type Analyzer struct{}

// NewAnalyzer creates a new PHP analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{}
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
		Language: "php",
	}, nil
}

func (a *Analyzer) analyzeFile(path string) ([]pkgParser.Symbol, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	rootNode, err := parser.Parse(content, conf.Config{
		Version: &version.Version{Major: 8, Minor: 1},
	})
	if err != nil {
		return nil, fmt.Errorf("php parse error: %w", err)
	}

	collector := &symbolCollector{
		filePath:    path,
		fileContent: content,
	}

	traverser.NewTraverser(collector).Traverse(rootNode)
	symbols := collector.symbols
	
	// Post-processing for Laravel
	for i := range symbols {
		s := &symbols[i]
		if s.Type == pkgParser.Class {
			extends, _ := s.Metadata["extends"].(string)
			if extends != "" {
				if strings.Contains(extends, "Model") || strings.Contains(extends, "Authenticatable") {
					s.Metadata["laravel_type"] = "model"
					s.Metadata["framework"] = "laravel"
				} else if strings.Contains(extends, "Controller") {
					s.Metadata["laravel_type"] = "controller"
					s.Metadata["framework"] = "laravel"
				}
			}
		}
	}

	return symbols, nil
}
