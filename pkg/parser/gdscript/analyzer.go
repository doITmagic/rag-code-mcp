package gdscript

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

func init() {
	pkgParser.Register(NewAnalyzer())
}

// Analyzer implements the parser.Analyzer interface for Godot projects:
// GDScript files (.gd) and text scene files (.tscn).
type Analyzer struct {
	mu      sync.Mutex
	parsers map[string]*gotreesitter.Parser
}

// NewAnalyzer creates a new GDScript analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{parsers: make(map[string]*gotreesitter.Parser)}
}

// Name returns "gdscript".
func (a *Analyzer) Name() string {
	return "gdscript"
}

// CanHandle returns true for .gd and .tscn files.
func (a *Analyzer) CanHandle(filePath string) bool {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".gd", ".tscn":
		return true
	}
	return false
}

// ReleaseResources drops cached tree-sitter parsers so the GC can reclaim arena memory.
func (a *Analyzer) ReleaseResources() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.parsers = make(map[string]*gotreesitter.Parser)
}

// Analyze extracts symbols from a file, or from every handled file under a directory.
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
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if d.IsDir() {
				if p != path && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !a.CanHandle(p) {
				return nil
			}
			syms, err := a.analyzeFile(p)
			if err != nil {
				return err
			}
			symbols = append(symbols, syms...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		symbols, err = a.analyzeFile(path)
		if err != nil {
			return nil, err
		}
	}

	return &pkgParser.Result{
		Symbols:  symbols,
		Language: "gdscript",
	}, nil
}

func (a *Analyzer) analyzeFile(path string) ([]pkgParser.Symbol, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(path), ".tscn") {
		return a.parseScene(source, path)
	}
	return a.parseScript(source, path)
}

// parse runs the tree-sitter grammar registered for path's extension and
// hands the root node to fn while the tree is still alive.
func (a *Analyzer) parse(source []byte, path string, fn func(root *gotreesitter.Node, lang *gotreesitter.Language)) error {
	entry := grammars.DetectLanguage(path)
	if entry == nil {
		return fmt.Errorf("no tree-sitter grammar for %s", path)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	parser, ok := a.parsers[entry.Name]
	if !ok {
		parser = gotreesitter.NewParser(entry.Language())
		a.parsers[entry.Name] = parser
	}
	tree, err := parser.Parse(source)
	if err != nil {
		return err
	}
	defer tree.Release()
	fn(tree.RootNode(), entry.Language())
	return nil
}

// fileStem returns the file name without directory and extension.
func fileStem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
