package php

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

// FrameworkEnricher defines an interface for adding framework-specific parsing (e.g., Laravel, WordPress).
type FrameworkEnricher interface {
	IsApplicable(ca *CodeAnalyzer, paths []string) bool
	Enrich(ca *CodeAnalyzer, packages []*PackageInfo, paths []string, chunks []CodeChunk) []CodeChunk
}

var enrichers []FrameworkEnricher

// RegisterEnricher adds a framework-specific enricher to the PHP parser.
func RegisterEnricher(e FrameworkEnricher) {
	enrichers = append(enrichers, e)
}

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
	paths := []string{path}
	chunks, err := a.codeAnalyzer.AnalyzePaths(paths)
	if err != nil {
		return nil, err
	}

	// Fetch packages analyzed by the core PHP parser
	packages := a.codeAnalyzer.GetPackages()

	// Run all registered framework enrichers
	for _, enricher := range enrichers {
		if enricher.IsApplicable(a.codeAnalyzer, paths) {
			chunks = enricher.Enrich(a.codeAnalyzer, packages, paths, chunks)
		}
	}

	// If no symbols found and the file is in a routes/ directory,
	// try extracting Route::* calls as symbols (Laravel convention).
	if len(chunks) == 0 && isRouteFile(path) {
		routeChunks := a.codeAnalyzer.ExtractRouteChunks(path)
		chunks = append(chunks, routeChunks...)
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
			Relations: chunk.Relations,
			Metadata:  chunk.Metadata,
		}
	}

	return &pkgParser.Result{
		Symbols:  symbols,
		Language: "php",
	}, nil
}

// isRouteFile checks if a PHP file is in a routes/ directory (Laravel convention).
func isRouteFile(path string) bool {
	dir := filepath.Dir(path)
	return filepath.Base(dir) == "routes"
}

// RouteInfo holds extracted route data (kept in php package to avoid import cycles).
type RouteInfo struct {
	Method      string
	URI         string
	Controller  string
	Action      string
	FilePath    string
	Line        int
	Description string
}

// ExtractRouteChunks parses a PHP route file and returns CodeChunks for Route::* calls.
// This uses regex-based extraction to avoid import cycles with the laravel sub-package.
func (ca *CodeAnalyzer) ExtractRouteChunks(filePath string) []CodeChunk {
	content, err := readFileContent(filePath)
	if err != nil || len(content) == 0 {
		return nil
	}

	var chunks []CodeChunk
	lines := strings.Split(string(content), "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match Route::get(...), Route::post(...), etc.
		if !strings.HasPrefix(trimmed, "Route::") {
			continue
		}

		method, uri, controller, action := parseRouteLine(trimmed)
		if method == "" || uri == "" {
			continue
		}

		chunk := CodeChunk{
			Name:      fmt.Sprintf("%s %s", method, uri),
			Type:      "route",
			Language:  "php",
			FilePath:  filePath,
			StartLine: i + 1,
			EndLine:   i + 1,
			Signature: fmt.Sprintf("Route::%s('%s', ...)", strings.ToLower(method), uri),
			Metadata: map[string]any{
				"method":     method,
				"uri":        uri,
				"controller": controller,
				"action":     action,
				"framework":  "laravel",
			},
		}
		if controller != "" && action != "" {
			chunk.Docstring = fmt.Sprintf("Route %s %s -> %s@%s", method, uri, controller, action)
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}

// parseRouteLine extracts method, URI, controller, and action from a Route::* line.
func parseRouteLine(line string) (method, uri, controller, action string) {
	// Match: Route::get('uri', 'Controller@action') or Route::get('uri', [...])
	if !strings.HasPrefix(line, "Route::") {
		return
	}

	// Extract method name
	rest := line[len("Route::"):]
	parenIdx := strings.Index(rest, "(")
	if parenIdx < 0 {
		return
	}
	method = strings.ToUpper(rest[:parenIdx])

	// Only handle standard HTTP methods
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "ANY", "MATCH", "RESOURCE":
		// ok
	default:
		return "", "", "", ""
	}

	// Extract first string argument (URI)
	argsStr := rest[parenIdx+1:]
	uri = extractQuotedString(argsStr)

	// Try to extract controller@action
	// Look for 'Controller@action' pattern
	atIdx := strings.Index(argsStr, "@")
	if atIdx > 0 {
		// Find the quoted string containing @
		for _, q := range []byte{'\'', '"'} {
			startQ := strings.IndexByte(argsStr[strings.Index(argsStr, string(q))+1:], q)
			_ = startQ
		}
		// Simple approach: find 'Something@method'
		parts := extractControllerAction(argsStr)
		if len(parts) == 2 {
			controller = parts[0]
			action = parts[1]
		}
	}

	return
}

// extractQuotedString extracts the first single or double quoted string.
func extractQuotedString(s string) string {
	for _, q := range []byte{'\'', '"'} {
		start := strings.IndexByte(s, q)
		if start < 0 {
			continue
		}
		end := strings.IndexByte(s[start+1:], q)
		if end < 0 {
			continue
		}
		return s[start+1 : start+1+end]
	}
	return ""
}

// extractControllerAction finds 'Controller@action' pattern in args string.
func extractControllerAction(s string) []string {
	// Find quotes containing @
	for _, q := range []byte{'\'', '"'} {
		idx := 0
		for idx < len(s) {
			start := strings.IndexByte(s[idx:], q)
			if start < 0 {
				break
			}
			start += idx
			end := strings.IndexByte(s[start+1:], q)
			if end < 0 {
				break
			}
			quoted := s[start+1 : start+1+end]
			if strings.Contains(quoted, "@") {
				return strings.SplitN(quoted, "@", 2)
			}
			idx = start + 1 + end + 1
		}
	}
	return nil
}

// readFileContent reads a file and returns its content as bytes.
func readFileContent(filePath string) ([]byte, error) {
	return os.ReadFile(filePath)
}
