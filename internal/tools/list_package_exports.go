package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// ListPackageExportsTool lists all exported symbols from a package
type ListPackageExportsTool struct {
	longTermMemory   memory.LongTermMemory
	embedder         llm.Provider
	workspaceManager *workspace.Manager
}

// NewListPackageExportsTool creates a new package exports listing tool
func NewListPackageExportsTool(ltm memory.LongTermMemory, embedder llm.Provider) *ListPackageExportsTool {
	return &ListPackageExportsTool{
		longTermMemory: ltm,
		embedder:       embedder,
	}
}

// SetWorkspaceManager sets the workspace manager for workspace-aware searching
func (t *ListPackageExportsTool) SetWorkspaceManager(wm *workspace.Manager) {
	t.workspaceManager = wm
}

func (t *ListPackageExportsTool) Name() string {
	return "list_package_exports"
}

func (t *ListPackageExportsTool) Description() string {
	return "List all public functions, classes, and types in a package/module. Returns a structured list with symbol names, types, and signatures. Use to explore an unfamiliar package or find the right function to call. Works for Go packages, PHP namespaces, Python modules."
}

func (t *ListPackageExportsTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	packageName, ok := args["package"].(string)
	if !ok || packageName == "" {
		return "", fmt.Errorf("package is required")
	}

	// Optional: filter by type (function, type, const, etc.)
	filterType := ""
	if ft, ok := args["symbol_type"].(string); ok {
		filterType = ft
	}

	// Optional output format: markdown (default) or json
	outputFormat := "markdown"
	if of, ok := args["output_format"].(string); ok && of != "" {
		outputFormat = strings.ToLower(of)
	}

	// file_path is required for workspace detection
	filePath := extractFilePathFromParams(args)
	if filePath == "" {
		return "", fmt.Errorf("file_path parameter is required for list_package_exports. Please provide a file path from your workspace")
	}

	// Try workspace detection if workspace manager is available
	var searchMemory memory.LongTermMemory
	var workspaceInfo *workspace.Info
	var collectionName string

	if t.workspaceManager != nil {
		wi, err := t.workspaceManager.DetectWorkspace(args)
		if err == nil && wi != nil {
			workspaceInfo = wi

			// Detect language from file path or use first detected language
			language := inferLanguageFromPath(filePath)
			if language == "" && len(wi.Languages) > 0 {
				language = wi.Languages[0]
			}
			if language == "" {
				language = wi.ProjectType
			}

			collectionName = wi.CollectionNameForLanguage(language)
			mem, err := t.workspaceManager.GetMemoryForWorkspaceLanguage(ctx, wi, language)
			if err == nil && mem != nil {
				// Check if indexing is in progress
				indexKey := wi.ID + "-" + language
				if t.workspaceManager.IsIndexing(indexKey) {
					return fmt.Sprintf("⏳ Workspace '%s' language '%s' is currently being indexed in the background.\n"+
						"Please try again in a few moments.\n"+
						"Workspace: %s\n"+
						"Language: %s\n"+
						"Collection: %s",
						wi.Root, language, wi.Root, language, collectionName), nil
				}

				// Check if collection exists before proceeding
				if msg, err := CheckCollectionStatus(ctx, mem, collectionName, wi.Root); err != nil || msg != "" {
					if err != nil {
						return "", err
					}
					return msg, nil
				}

				searchMemory = mem
			}
		}
	}

	// If this looks like a PHP/Laravel workspace, prefer using the PHP analyzer directly
	if workspaceInfo != nil && isPHPLikeProject(workspaceInfo.ProjectType) {
		return listPHPExports(ctx, workspaceInfo, packageName, filterType, outputFormat)
	}

	// Use workspace-specific memory or fall back to default
	if searchMemory == nil {
		searchMemory = t.longTermMemory
	}

	if searchMemory == nil {
		return "", fmt.Errorf("no long-term memory configured")
	}

	// Search for package contents in the vector index
	query := fmt.Sprintf("package %s exports", packageName)
	queryEmbedding, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Get more results to cover all package symbols
	// Prefer SearchCodeOnly to exclude markdown documentation
	type CodeSearcher interface {
		SearchCodeOnly(ctx context.Context, query []float64, limit int) ([]memory.Document, error)
	}

	var results []memory.Document
	if codeSearcher, ok := searchMemory.(CodeSearcher); ok {
		results, err = codeSearcher.SearchCodeOnly(ctx, queryEmbedding, 100)
	} else {
		results, err = searchMemory.Search(ctx, queryEmbedding, 100)
	}
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	// Check if workspace search returned no results (might be empty collection)
	if len(results) == 0 && workspaceInfo != nil && collectionName != "" {
		if msg, err := CheckSearchResults(0, collectionName, workspaceInfo.Root); err != nil || msg != "" {
			if err != nil {
				return "", err
			}
			return msg, nil
		}
	}

	// Group by type
	exports := make(map[string][]ExportedSymbol)
	seenNames := make(map[string]bool)

	for _, result := range results {
		var chunk codetypes.CodeChunk
		if err := json.Unmarshal([]byte(result.Content), &chunk); err != nil {
			continue
		}

		// Filter by package name
		if !strings.Contains(chunk.Package, packageName) {
			continue
		}

		// Check if exported (starts with uppercase)
		if len(chunk.Name) == 0 || !isExported(chunk.Name) {
			continue
		}

		// Apply type filter if specified
		if filterType != "" && chunk.Type != filterType {
			continue
		}

		// Avoid duplicates
		key := fmt.Sprintf("%s:%s", chunk.Type, chunk.Name)
		if seenNames[key] {
			continue
		}
		seenNames[key] = true

		symbol := ExportedSymbol{
			Name:        chunk.Name,
			Type:        chunk.Type,
			Signature:   chunk.Signature,
			Description: strings.Split(chunk.Docstring, "\n")[0], // First line only
			FilePath:    chunk.FilePath,
			StartLine:   chunk.StartLine,
			Package:     chunk.Package,
			Language:    chunk.Language,
		}

		exports[chunk.Type] = append(exports[chunk.Type], symbol)
	}

	if len(exports) == 0 {
		return fmt.Sprintf("No exported symbols found in package '%s'", packageName), nil
	}

	// JSON output for Go and other non-PHP languages
	if strings.ToLower(outputFormat) == "json" {
		var descriptors []codetypes.SymbolDescriptor
		types := make([]string, 0, len(exports))
		for t := range exports {
			types = append(types, t)
		}
		sort.Strings(types)

		for _, symbolType := range types {
			symbols := exports[symbolType]
			sort.Slice(symbols, func(i, j int) bool {
				return symbols[i].Name < symbols[j].Name
			})
			for _, sym := range symbols {
				desc := codetypes.SymbolDescriptor{
					Language:    sym.Language,
					Kind:        sym.Type,
					Name:        sym.Name,
					Namespace:   sym.Package,
					Package:     sym.Package,
					Signature:   sym.Signature,
					Description: sym.Description,
					Location: codetypes.SymbolLocation{
						FilePath:  sym.FilePath,
						StartLine: sym.StartLine,
					},
				}
				descriptors = append(descriptors, desc)
			}
		}

		data, err := json.MarshalIndent(descriptors, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal Go package exports: %w", err)
		}
		return string(data), nil
	}

	// Build response
	var response strings.Builder
	response.WriteString(fmt.Sprintf("# Package: %s\n\n", packageName))

	totalCount := 0
	for _, symbols := range exports {
		totalCount += len(symbols)
	}
	response.WriteString(fmt.Sprintf("**Total exported symbols:** %d\n\n", totalCount))

	// Sort types for consistent output
	types := make([]string, 0, len(exports))
	for t := range exports {
		types = append(types, t)
	}
	sort.Strings(types)

	// Display by type
	for _, symbolType := range types {
		symbols := exports[symbolType]
		sort.Slice(symbols, func(i, j int) bool {
			return symbols[i].Name < symbols[j].Name
		})

		response.WriteString(fmt.Sprintf("## %s (%d)\n\n", cases.Title(language.English).String(symbolType), len(symbols)))

		for _, sym := range symbols {
			response.WriteString(fmt.Sprintf("### `%s`\n", sym.Name))
			if sym.Signature != "" {
				response.WriteString(fmt.Sprintf("**Signature:** `%s`\n\n", sym.Signature))
			}
			if sym.Description != "" {
				response.WriteString(fmt.Sprintf("%s\n\n", sym.Description))
			}
			response.WriteString(fmt.Sprintf("📍 `%s:%d`\n\n", sym.FilePath, sym.StartLine))
		}
	}

	return response.String(), nil
}

type ExportedSymbol struct {
	Name        string
	Type        string
	Signature   string
	Description string
	FilePath    string
	StartLine   int
	Package     string
	Language    string
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	first := rune(name[0])
	return first >= 'A' && first <= 'Z'
}

// isPHPLikeProject returns true for php / php-laravel / laravel project types.
func isPHPLikeProject(projectType string) bool {
	pt := strings.ToLower(strings.TrimSpace(projectType))
	return pt == "php" || pt == "php-laravel" || pt == "laravel"
}

// listPHPExports uses the PHP analyzer directly to list exported symbols (classes, functions, constants)
// for a given namespace/package, avoiding reliance on vector search ranking.
//
// outputFormat can be "markdown" (default) or "json". The JSON form returns a
// list of codetypes.SymbolDescriptor values encoded as JSON.
func listPHPExports(ctx context.Context, info *workspace.Info, packageName string, filterType, outputFormat string) (string, error) {
	analyzer := php.NewCodeAnalyzer()
	// Analyze the entire workspace root; PHP analyzer will respect vendor/public exclusions
	chunks, err := analyzer.AnalyzePaths([]string{info.Root})
	if err != nil {
		return "", fmt.Errorf("PHP analysis failed for workspace '%s': %w", info.Root, err)
	}

	// Group by type
	exports := make(map[string][]ExportedSymbol)
	seenNames := make(map[string]bool)

	for _, ch := range chunks {
		// Filter by namespace/package (exact or prefix match)
		if ch.Package == "" {
			continue
		}
		if ch.Package != packageName && !strings.HasPrefix(ch.Package, packageName+"\\") {
			continue
		}

		// Only consider exported symbols (leading uppercase), consistent with Go path
		if !isExported(ch.Name) {
			continue
		}

		// Apply type filter if specified
		if filterType != "" && ch.Type != filterType {
			continue
		}

		key := fmt.Sprintf("%s:%s", ch.Type, ch.Name)
		if seenNames[key] {
			continue
		}
		seenNames[key] = true

		symbol := ExportedSymbol{
			Name:        ch.Name,
			Type:        ch.Type,
			Signature:   ch.Signature,
			Description: strings.Split(ch.Docstring, "\n")[0],
			FilePath:    ch.FilePath,
			StartLine:   ch.StartLine,
			Package:     ch.Package,
			Language:    ch.Language,
		}
		exports[ch.Type] = append(exports[ch.Type], symbol)
	}

	if len(exports) == 0 {
		return fmt.Sprintf("No exported symbols found in package '%s'", packageName), nil
	}

	// JSON output
	format := strings.ToLower(outputFormat)
	if format == "json" {
		// Flatten grouped exports into a stable, sorted list of SymbolDescriptor
		var descriptors []codetypes.SymbolDescriptor
		types := make([]string, 0, len(exports))
		for t := range exports {
			types = append(types, t)
		}
		sort.Strings(types)

		for _, symbolType := range types {
			symbols := exports[symbolType]
			sort.Slice(symbols, func(i, j int) bool {
				return symbols[i].Name < symbols[j].Name
			})
			for _, sym := range symbols {
				desc := codetypes.SymbolDescriptor{
					Language:    sym.Language,
					Kind:        sym.Type,
					Name:        sym.Name,
					Namespace:   sym.Package,
					Package:     sym.Package,
					Signature:   sym.Signature,
					Description: sym.Description,
					Location: codetypes.SymbolLocation{
						FilePath:  sym.FilePath,
						StartLine: sym.StartLine,
					},
				}
				descriptors = append(descriptors, desc)
			}
		}

		data, err := json.MarshalIndent(descriptors, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal PHP package exports: %w", err)
		}
		return string(data), nil
	}

	// Build response identical to the default path
	var response strings.Builder
	response.WriteString(fmt.Sprintf("# Package: %s\n\n", packageName))

	totalCount := 0
	for _, symbols := range exports {
		totalCount += len(symbols)
	}
	response.WriteString(fmt.Sprintf("**Total exported symbols:** %d\n\n", totalCount))

	types := make([]string, 0, len(exports))
	for t := range exports {
		types = append(types, t)
	}
	sort.Strings(types)

	for _, symbolType := range types {
		symbols := exports[symbolType]
		sort.Slice(symbols, func(i, j int) bool {
			return symbols[i].Name < symbols[j].Name
		})

		response.WriteString(fmt.Sprintf("## %s (%d)\n\n", cases.Title(language.English).String(symbolType), len(symbols)))

		for _, sym := range symbols {
			response.WriteString(fmt.Sprintf("### `%s`\n", sym.Name))
			if sym.Signature != "" {
				response.WriteString(fmt.Sprintf("**Signature:** `%s`\n\n", sym.Signature))
			}
			if sym.Description != "" {
				response.WriteString(fmt.Sprintf("%s\n\n", sym.Description))
			}
			response.WriteString(fmt.Sprintf("📍 `%s:%d`\n\n", sym.FilePath, sym.StartLine))
		}
	}

	return response.String(), nil
}
