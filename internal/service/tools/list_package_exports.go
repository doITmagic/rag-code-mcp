package tools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListPackageExportsTool struct {
	engine *engine.Engine
}

func NewListPackageExportsTool(eng *engine.Engine) *ListPackageExportsTool {
	return &ListPackageExportsTool{
		engine: eng,
	}
}

func (t *ListPackageExportsTool) Name() string { return "rag_list_package_exports" }
func (t *ListPackageExportsTool) Description() string {
	return "List all public functions, classes, and types in a package/module deterministically. Returns a structured list with symbol names, types, and signatures. Use to explore an unfamiliar package or find the right function to call. Works for Go packages, PHP namespaces, Python modules. IMPORTANT: Always provide the 'file_path' of the file you are currently working on for better context detection."
}

type ListPackageExportsInput struct {
	Package    string `json:"package"`
	FilePath   string `json:"file_path,omitempty"`
	SymbolType string `json:"symbol_type,omitempty"`
}

func (t *ListPackageExportsTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListPackageExportsInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{
			"package":     input.Package,
			"file_path":   input.FilePath,
			"symbol_type": input.SymbolType,
		}

		result, err := t.Execute(ctx, args)
		if err != nil {
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

// ExportedSymbol represents a symbol exported directly from DB payload
type ExportedSymbol struct {
	Name        string
	Type        string
	Signature   string
	Description string
	FilePath    string
	StartLine   int
	Package     string
}

func (t *ListPackageExportsTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	packageName, ok := args["package"].(string)
	if !ok || packageName == "" {
		resp := ToolResponse{Status: "error", Error: "package parameter is required"}
		return resp.JSON()
	}

	filterType, _ := args["symbol_type"].(string)
	filePath, _ := args["file_path"].(string)

	wctx, err := t.engine.DetectContext(ctx, filePath)
	if err != nil {
		resp := ToolResponse{Status: "error", Error: fmt.Sprintf("failed to detect workspace: %v", err)}
		return resp.JSON()
	}

	searchSvc := t.engine.GetSearchService()
	if searchSvc == nil {
		resp := ToolResponse{Status: "error", Error: "search service unavailable"}
		return resp.JSON()
	}

	// We'll iterate the known languages in the index
	langs := parser.SupportedLanguages()
	var allResults []storage.SearchResult

	for _, lang := range langs {
		colName := fmt.Sprintf("ragcode-%s-%s", wctx.ID, lang)
		exists, _ := searchSvc.CollectionExists(ctx, colName)
		if !exists {
			continue
		}

		filter := map[string]interface{}{
			"package": packageName,
		}

		res, err := searchSvc.ExactSearch(ctx, colName, filter, 1000)
		if err == nil {
			allResults = append(allResults, res...)
		}
	}

	if len(allResults) == 0 {
		resp := ToolResponse{
			Status:  "success",
			Message: fmt.Sprintf("No exported symbols found in package '%s'", packageName),
			Context: ContextMetadata{WorkspaceRoot: wctx.Root, DetectionSource: wctx.DetectionSource},
		}
		return resp.JSON()
	}

	exports := make(map[string][]ExportedSymbol)
	seenNames := make(map[string]bool)
	seenFiles := make(map[string]bool)
	baselineBytes := 0
	actualBytes := 0

	for _, result := range allResults {
		name, _ := result.Point.Payload["name"].(string)
		if !isExported(name) {
			continue
		}

		symType, _ := result.Point.Payload["type"].(string)
		if filterType != "" && symType != filterType {
			continue
		}

		key := fmt.Sprintf("%s:%s", symType, name)
		if seenNames[key] {
			continue
		}
		seenNames[key] = true

		var signature, docstring string
		signature, _ = result.Point.Payload["signature"].(string)
		docstring, _ = result.Point.Payload["docstring"].(string)

		descLine := strings.Split(docstring, "\n")[0]
		filePath, _ := result.Point.Payload["file_path"].(string)
		startLineVal, _ := result.Point.Payload["start_line"]
		startLine := 0
		switch v := startLineVal.(type) {
		case float64:
			startLine = int(v)
		case int:
			startLine = v
		}

		actualBytes += len(name) + len(signature) + len(descLine)
		if filePath != "" && !seenFiles[filePath] {
			seenFiles[filePath] = true
			if info, statErr := os.Stat(filePath); statErr == nil {
				baselineBytes += int(info.Size())
			}
		}

		sym := ExportedSymbol{
			Name:        name,
			Type:        symType,
			Signature:   signature,
			Description: descLine,
			FilePath:    filePath,
			StartLine:   startLine,
			Package:     packageName,
		}

		exports[symType] = append(exports[symType], sym)
	}

	if len(exports) == 0 {
		resp := ToolResponse{
			Status:  "success",
			Message: fmt.Sprintf("No exported symbols found in package '%s' (after filtering)", packageName),
			Context: ContextMetadata{WorkspaceRoot: wctx.Root, DetectionSource: wctx.DetectionSource},
		}
		return resp.JSON()
	}

	// Format response nicely
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

	resp := ToolResponse{
		Status:  "success",
		Message: "Found package exports\n\n" + response.String(),
		Data:    exports,
		Context: ContextMetadata{
			WorkspaceRoot:   wctx.Root,
			DetectionSource: wctx.DetectionSource,
			Telemetry:       telemetry.CalculateSavings(baselineBytes, actualBytes),
		},
	}

	return resp.JSON()
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	first := rune(name[0])
	return first >= 'A' && first <= 'Z'
}
