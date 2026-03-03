package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/internal/service/internalutil"
	"github.com/doITmagic/rag-code-mcp/pkg/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type FindUsagesTool struct {
	engine *engine.Engine
}

func NewFindUsagesTool(eng *engine.Engine) *FindUsagesTool {
	return &FindUsagesTool{
		engine: eng,
	}
}

func (t *FindUsagesTool) Name() string { return "rag_find_usages" }
func (t *FindUsagesTool) Description() string {
	return "Fast and deterministic tool to find where a specific class, function, or type is used across the codebase based on the Code Graph (AST Relations). Returns the exact code chunks, files, and line numbers. MANDATORY: You must provide the 'file_path' of the current file to allow the tool to detect the correct workspace and context.\nExample: { \"symbol_name\": \"MyClass\", \"file_path\": \"/path/to/project/main.go\" }"
}

type FindUsagesInput struct {
	SymbolName string `json:"symbol_name"`
	FilePath   string `json:"file_path,omitempty"`
}

func (t *FindUsagesTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FindUsagesInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{
			"symbol_name": input.SymbolName,
			"file_path":   input.FilePath,
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

// UsageResult represents a found usage of a symbol
type UsageResult struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Signature   string   `json:"signature"`
	FilePath    string   `json:"file_path"`
	StartLine   int      `json:"start_line"`
	Package     string   `json:"package"`
	Snippet     string   `json:"snippet"`
	MatchReason []string `json:"match_reason"` // Which relations matched
}

func (t *FindUsagesTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	symbolName, ok := args["symbol_name"].(string)
	if !ok || symbolName == "" {
		resp := ToolResponse{Status: "error", Error: "symbol_name parameter is required"}
		return resp.JSON()
	}

	filePath, _ := args["file_path"].(string)

	wctx, err := t.engine.DetectContext(ctx, filePath)
	if err != nil {
		resp := ToolResponse{Status: "error", Error: fmt.Sprintf("failed to detect workspace: %v", err)}
		return resp.JSON()
	}

	// Fan-out to all language collections in parallel — zero embedding
	filter := map[string]interface{}{
		"relations[].target_name": symbolName,
	}

	idx := t.engine.GetIndexProgress(wctx.ID)
	allResults, err := t.engine.ExactSearchPolyglot(ctx, wctx.ID, filter, 100)
	if err != nil {
		var noCollections *engine.ErrNoCollectionsFound
		if errors.As(err, &noCollections) {
			resp := ToolResponse{
				Status:  "indexing_required",
				Message: fmt.Sprintf("⏳ Workspace '%s' is not indexed yet. Indexing is required for complete results.", wctx.Root),
				Context: ContextFromWorkspaceWithProgress(wctx, t.engine),
			}
			if idx != nil {
				resp.Status = "indexing_in_progress"
				resp.Data = map[string]any{"indexing": idx}
			}
			return resp.JSON()
		}

		resp := ToolResponse{Status: "error", Error: fmt.Sprintf("usage search failed: %v", err)}
		return resp.JSON()
	}

	if len(allResults) == 0 {
		resp := ToolResponse{
			Status:  "success",
			Message: fmt.Sprintf("No usages found for symbol '%s' based on Code Graph relations.", symbolName),
			Context: ContextFromWorkspaceWithProgress(wctx, t.engine),
		}
		return resp.JSON()
	}

	var usages []UsageResult
	seenIDs := make(map[string]bool)
	seenFiles := make(map[string]bool)
	baselineBytes := 0
	actualBytes := 0

	for _, result := range allResults {
		if seenIDs[result.Point.ID] {
			continue
		}
		seenIDs[result.Point.ID] = true

		name, _ := result.Point.Payload["name"].(string)
		symType, _ := result.Point.Payload["type"].(string)
		pkg, _ := result.Point.Payload["package"].(string)

		signature, _ := result.Point.Payload["signature"].(string)
		code, _ := result.Point.Payload["content"].(string)

		filePath, _ := result.Point.Payload["file_path"].(string)
		startLineVal := result.Point.Payload["start_line"]
		startLine := 0
		switch v := startLineVal.(type) {
		case float64:
			startLine = int(v)
		case int:
			startLine = v
		}

		// Extract exactly which relation(s) matched to show reasoning
		var matchedRelations []string
		if relationsRaw, hasRel := result.Point.Payload["relations"]; hasRel {
			if relList, ok := relationsRaw.([]interface{}); ok {
				for _, relItem := range relList {
					if rMap, ok := relItem.(map[string]interface{}); ok {
						if target, _ := rMap["target_name"].(string); target == symbolName {
							relType, _ := rMap["type"].(string)
							matchedRelations = append(matchedRelations, relType)
						}
					}
				}
			}
		}

		actualBytes += len(code)
		if filePath != "" && !seenFiles[filePath] {
			seenFiles[filePath] = true
			if info, statErr := os.Stat(filePath); statErr == nil {
				baselineBytes += int(info.Size())
			}
		}

		usage := UsageResult{
			Name:        name,
			Type:        symType,
			Signature:   signature,
			FilePath:    filePath,
			StartLine:   startLine,
			Package:     pkg,
			Snippet:     code,
			MatchReason: matchedRelations,
		}
		usages = append(usages, usage)
	}

	if len(usages) == 0 {
		resp := ToolResponse{
			Status:  "success",
			Message: fmt.Sprintf("No explicit usages found for symbol '%s'", symbolName),
			Context: ContextFromWorkspaceWithProgress(wctx, t.engine),
		}
		return resp.JSON()
	}

	// Sort results by file path and start line
	sort.Slice(usages, func(i, j int) bool {
		if usages[i].FilePath == usages[j].FilePath {
			return usages[i].StartLine < usages[j].StartLine
		}
		return usages[i].FilePath < usages[j].FilePath
	})

	// Format a readable markdown response
	var response strings.Builder
	response.WriteString(fmt.Sprintf("# Usages of `%s`\n\n", symbolName))
	response.WriteString(fmt.Sprintf("**Total usages found:** %d\n\n", len(usages)))

	for _, u := range usages {
		response.WriteString(fmt.Sprintf("## In %s `%s`\n", u.Type, u.Name))
		response.WriteString(fmt.Sprintf("📍 `%s:%d`\n", u.FilePath, u.StartLine))

		if len(u.MatchReason) > 0 {
			response.WriteString(fmt.Sprintf("**Relation Types:** %s\n", strings.Join(u.MatchReason, ", ")))
		}

		if u.Snippet != "" {
			// Find language for code block wrapping based on file extension
			lang := internalutil.LanguageFromPath(u.FilePath)
			response.WriteString(fmt.Sprintf("\n```%s\n%s\n```\n\n", lang, u.Snippet))
		}
	}

	// Add markdown payload as well under a human readable format
	// actually, the standard pattern for tools in rag-code-mcp:
	// To return string so the AI can read it, we change ToolResponse struct or just return the JSON string.
	// We'll construct a generic Map to marshal it all manually just like rag_search_code.

	resp := ToolResponse{
		Status:  "success",
		Message: "Found symbol usages\n\n" + response.String(),
		Data:    usages,
		Context: ContextMetadata{
			WorkspaceRoot:    wctx.Root,
			DetectionSource:  wctx.DetectionSource,
			Telemetry:        telemetry.CalculateSavings(baselineBytes, actualBytes),
			IndexingProgress: BuildIndexingProgress(t.engine, wctx.ID),
		},
	}
	return resp.JSON()
}
