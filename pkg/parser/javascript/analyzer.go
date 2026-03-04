package javascript

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/javascript/vue"
)

func init() {
	pkgParser.Register(NewCodeAnalyzer())
}

// CodeAnalyzer implements parser.Analyzer for JavaScript/TypeScript
type CodeAnalyzer struct{}

// NewCodeAnalyzer creates a new JS/TS code analyzer
func NewCodeAnalyzer() *CodeAnalyzer {
	return &CodeAnalyzer{}
}

// Name returns the analyzer name
func (ca *CodeAnalyzer) Name() string {
	return "javascript"
}

// jsExtensions lists all supported file extensions
var jsExtensions = map[string]bool{
	".js":  true,
	".jsx": true,
	".ts":  true,
	".tsx": true,
	".mjs": true,
	".cjs": true,
	".vue": true, // Vue Single File Components
}

// CanHandle returns true for JS/TS files
func (ca *CodeAnalyzer) CanHandle(filePath string) bool {
	ext := filepath.Ext(filePath)
	return jsExtensions[ext]
}

// Analyze extracts symbols from a file or directory
func (ca *CodeAnalyzer) Analyze(ctx context.Context, path string) (*pkgParser.Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var allInfo []fileAnalysis

	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				base := filepath.Base(p)
				if base == "node_modules" || base == ".git" || base == "dist" ||
					base == "build" || base == ".next" || base == "coverage" {
					return filepath.SkipDir
				}
				return nil
			}
			if !ca.CanHandle(p) {
				return nil
			}
			// Skip test/spec files
			name := d.Name()
			if strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") ||
				strings.Contains(name, "__test__") || strings.Contains(name, "__tests__") {
				return nil
			}

			fa, err := ca.analyzeFile(p)
			if err != nil {
				return nil // skip file on error
			}
			allInfo = append(allInfo, *fa)
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		fa, err := ca.analyzeFile(path)
		if err != nil {
			return nil, err
		}
		allInfo = append(allInfo, *fa)
	}

	// Convert to symbols
	var symbols []pkgParser.Symbol
	for _, fa := range allInfo {
		symbols = append(symbols, ca.convertToSymbols(fa)...)
	}

	return &pkgParser.Result{
		Symbols:  symbols,
		Language: "javascript",
	}, nil
}

// fileAnalysis holds the analysis results for a single file
type fileAnalysis struct {
	FilePath   string
	Language   string // js, jsx, ts, tsx
	Functions  []JSFunction
	Classes    []JSClass
	Interfaces []TSInterface
	Types      []TSTypeAlias
	Enums      []TSEnum
	Imports    []JSImport
	Exports    []JSExport
}

// analyzeFile performs full analysis of a single JS/TS file
// Uses tree-sitter (pure Go, zero CGO) as primary engine, with regex fallback
func (ca *CodeAnalyzer) analyzeFile(filePath string) (*fileAnalysis, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// Route .vue files to the Vue SFC sub-analyzer
	if filepath.Ext(filePath) == ".vue" {
		return ca.analyzeVueFile(filePath, content)
	}

	// Try tree-sitter first (accurate AST parsing)
	tsParser := NewTreeSitterParser()
	fa, err := tsParser.ParseFile(content, filePath)
	if err == nil && fa != nil && (len(fa.Functions) > 0 || len(fa.Classes) > 0 ||
		len(fa.Interfaces) > 0 || len(fa.Types) > 0 || len(fa.Enums) > 0) {
		// Tree-sitter succeeded — also extract imports/exports if not already done
		source := string(content)
		if len(fa.Imports) == 0 {
			fa.Imports = ExtractImports(source)
		}
		if len(fa.Exports) == 0 {
			fa.Exports = ExtractExports(source)
		}
		return fa, nil
	}

	// Fallback to regex-based extraction
	source := string(content)
	SetSourceCache(source)

	ext := filepath.Ext(filePath)
	lang := "javascript"
	if ext == ".ts" || ext == ".tsx" {
		lang = "typescript"
	}

	fa = &fileAnalysis{
		FilePath:  filePath,
		Language:  lang,
		Functions: ExtractFunctions(source, filePath),
		Classes:   ExtractClasses(source, filePath),
		Imports:   ExtractImports(source),
		Exports:   ExtractExports(source),
	}

	// TypeScript-specific extraction
	if lang == "typescript" || ext == ".tsx" {
		fa.Interfaces = ExtractTSInterfaces(source, filePath)
		fa.Types = ExtractTSTypeAliases(source, filePath)
		fa.Enums = ExtractTSEnums(source, filePath)
	}

	return fa, nil
}

// analyzeVueFile delegates to the Vue SFC sub-analyzer and converts results to fileAnalysis
func (ca *CodeAnalyzer) analyzeVueFile(filePath string, content []byte) (*fileAnalysis, error) {
	vueAnalyzer := vue.NewAnalyzer()
	vueInfo := vueAnalyzer.Analyze(string(content), filePath)
	if vueInfo == nil {
		return &fileAnalysis{FilePath: filePath, Language: "vue"}, nil
	}

	fa := &fileAnalysis{
		FilePath: filePath,
		Language: "vue",
	}

	// Map Vue composables to JS functions
	for _, comp := range vueInfo.Composables {
		fa.Functions = append(fa.Functions, JSFunction{
			Name:       comp.Name,
			FilePath:   filePath,
			IsExported: true,
		})
	}

	// Map Vue components as class-like symbols
	for _, comp := range vueInfo.Components {
		fa.Classes = append(fa.Classes, JSClass{
			Name:       comp.Name,
			FilePath:   filePath,
			IsExported: comp.IsExported,
			Docstring:  fmt.Sprintf("Vue component (%s)", comp.Type),
		})
	}

	return fa, nil
}

// convertToSymbols converts file analysis to parser symbols
func (ca *CodeAnalyzer) convertToSymbols(fa fileAnalysis) []pkgParser.Symbol {
	var symbols []pkgParser.Symbol

	// Functions
	for _, fn := range fa.Functions {
		sig := buildFunctionSignature(fn)
		sym := pkgParser.Symbol{
			Name:      fn.Name,
			Type:      pkgParser.Function,
			Content:   fn.Code,
			Signature: sig,
			Docstring: fn.Docstring,
			StartLine: fn.StartLine,
			EndLine:   fn.EndLine,
			FilePath:  fn.FilePath,
			Language:  fa.Language,
			IsPublic:  fn.IsExported,
			Metadata: map[string]any{
				"is_async":    fn.IsAsync,
				"is_arrow":    fn.IsArrow,
				"is_exported": fn.IsExported,
				"is_default":  fn.IsDefault,
			},
		}

		// Add import relations
		for _, imp := range fa.Imports {
			sym.Relations = append(sym.Relations, pkgParser.Relation{
				TargetName: imp.Source,
				Type:       pkgParser.RelDependency,
			})
		}

		symbols = append(symbols, sym)
	}

	// Classes
	for _, cls := range fa.Classes {
		sig := buildClassSignature(cls)
		sym := pkgParser.Symbol{
			Name:      cls.Name,
			Type:      pkgParser.Class,
			Content:   cls.Code,
			Signature: sig,
			Docstring: cls.Docstring,
			StartLine: cls.StartLine,
			EndLine:   cls.EndLine,
			FilePath:  cls.FilePath,
			Language:  fa.Language,
			IsPublic:  cls.IsExported,
			Metadata: map[string]any{
				"extends":     cls.Extends,
				"implements":  cls.Implements,
				"is_abstract": cls.IsAbstract,
				"methods":     len(cls.Methods),
			},
		}

		if cls.Extends != "" {
			sym.Relations = append(sym.Relations, pkgParser.Relation{
				TargetName: cls.Extends,
				Type:       pkgParser.RelInheritance,
			})
		}
		for _, impl := range cls.Implements {
			sym.Relations = append(sym.Relations, pkgParser.Relation{
				TargetName: impl,
				Type:       pkgParser.RelImplements,
			})
		}

		symbols = append(symbols, sym)

		// Class methods as separate symbols
		for _, method := range cls.Methods {
			methodSig := fmt.Sprintf("%s.%s(%s)", cls.Name, method.Name, strings.Join(method.Params, ", "))
			symbols = append(symbols, pkgParser.Symbol{
				Name:      fmt.Sprintf("%s.%s", cls.Name, method.Name),
				Type:      pkgParser.Method,
				Signature: methodSig,
				Docstring: method.Docstring,
				StartLine: method.StartLine,
				EndLine:   method.EndLine,
				FilePath:  cls.FilePath,
				Language:  fa.Language,
				IsPublic:  !method.IsPrivate,
				Metadata: map[string]any{
					"is_async":   method.IsAsync,
					"is_static":  method.IsStatic,
					"visibility": method.Visibility,
					"class":      cls.Name,
				},
			})
		}
	}

	// Interfaces (TS)
	for _, iface := range fa.Interfaces {
		sig := fmt.Sprintf("interface %s", iface.Name)
		if len(iface.Extends) > 0 {
			sig += " extends " + strings.Join(iface.Extends, ", ")
		}

		symbols = append(symbols, pkgParser.Symbol{
			Name:      iface.Name,
			Type:      pkgParser.Interface,
			Signature: sig,
			Docstring: iface.Docstring,
			StartLine: iface.StartLine,
			EndLine:   iface.EndLine,
			FilePath:  iface.FilePath,
			Language:  fa.Language,
			IsPublic:  iface.IsExported,
			Metadata: map[string]any{
				"properties": len(iface.Properties),
				"extends":    iface.Extends,
			},
		})
	}

	// Type aliases (TS)
	for _, t := range fa.Types {
		symbols = append(symbols, pkgParser.Symbol{
			Name:      t.Name,
			Type:      pkgParser.Type,
			Signature: fmt.Sprintf("type %s = %s", t.Name, t.Definition),
			StartLine: t.StartLine,
			EndLine:   t.EndLine,
			FilePath:  t.FilePath,
			Language:  fa.Language,
			IsPublic:  t.IsExported,
		})
	}

	// Enums (TS)
	for _, e := range fa.Enums {
		prefix := "enum"
		if e.IsConst {
			prefix = "const enum"
		}
		symbols = append(symbols, pkgParser.Symbol{
			Name:      e.Name,
			Type:      pkgParser.Type,
			Signature: fmt.Sprintf("%s %s { %s }", prefix, e.Name, strings.Join(e.Members, ", ")),
			StartLine: e.StartLine,
			EndLine:   e.EndLine,
			FilePath:  e.FilePath,
			Language:  fa.Language,
			IsPublic:  e.IsExported,
			Metadata: map[string]any{
				"is_enum":  true,
				"is_const": e.IsConst,
				"members":  e.Members,
			},
		})
	}

	return symbols
}

// buildFunctionSignature creates a readable function signature
func buildFunctionSignature(fn JSFunction) string {
	var parts []string
	if fn.IsExported {
		parts = append(parts, "export")
	}
	if fn.IsDefault {
		parts = append(parts, "default")
	}
	if fn.IsAsync {
		parts = append(parts, "async")
	}
	if fn.IsArrow {
		parts = append(parts, fmt.Sprintf("const %s = (%s) =>", fn.Name, strings.Join(fn.Params, ", ")))
	} else {
		parts = append(parts, fmt.Sprintf("function %s(%s)", fn.Name, strings.Join(fn.Params, ", ")))
	}
	if fn.ReturnType != "" {
		parts = append(parts, ": "+fn.ReturnType)
	}
	return strings.Join(parts, " ")
}

// buildClassSignature creates a readable class signature
func buildClassSignature(cls JSClass) string {
	var parts []string
	if cls.IsExported {
		parts = append(parts, "export")
	}
	if cls.IsAbstract {
		parts = append(parts, "abstract")
	}
	parts = append(parts, "class "+cls.Name)
	if cls.Extends != "" {
		parts = append(parts, "extends "+cls.Extends)
	}
	if len(cls.Implements) > 0 {
		parts = append(parts, "implements "+strings.Join(cls.Implements, ", "))
	}
	return strings.Join(parts, " ")
}
