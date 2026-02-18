package parser

import (
	"context"
	"sync"
)

var (
	mu        sync.RWMutex
	analyzers = make(map[string]Analyzer)
)

// Register adds an analyzer to the global registry.
func Register(a Analyzer) {
	mu.Lock()
	defer mu.Unlock()
	analyzers[a.Name()] = a
}

// GetByName returns an analyzer by its name (e.g., "go").
func GetByName(name string) Analyzer {
	mu.RLock()
	defer mu.RUnlock()
	return analyzers[name]
}

// GetByFile returns an analyzer that can handle the given file path.
func GetByFile(filePath string) Analyzer {
	mu.RLock()
	defer mu.RUnlock()
	for _, a := range analyzers {
		if a.CanHandle(filePath) {
			return a
		}
	}
	return nil
}

// SymbolType identifies the kind of code symbol.
type SymbolType string

const (
	Function  SymbolType = "function"
	Method    SymbolType = "method"
	Class     SymbolType = "class"
	Interface SymbolType = "interface"
	Type      SymbolType = "type"
	Const     SymbolType = "const"
	Var       SymbolType = "var"
)

// Symbol represents a generic code entity (function, class, etc.)
type Symbol struct {
	Name      string
	Type      SymbolType
	Package   string
	Content   string
	Signature string
	Docstring string
	StartLine int
	EndLine   int
	FilePath  string
	Language  string
	Metadata  map[string]any
}

// Result is what a parser returns for a given file or directory.
type Result struct {
	Symbols  []Symbol
	Language string
}

// Analyzer is the interface for language-specific parsers.
type Analyzer interface {
	// Name returns the analyzer name (e.g., "golang", "python").
	Name() string

	// CanHandle returns true if the analyzer supports the given file extension.
	CanHandle(filePath string) bool

	// Analyze extracts symbols from a file or directory.
	Analyze(ctx context.Context, path string) (*Result, error)
}
