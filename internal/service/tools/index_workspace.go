package tools

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// IndexWorkspaceTool implements the rag_index_workspace MCP tool.
type IndexWorkspaceTool struct {
	engine *engine.Engine
}

// NewIndexWorkspaceTool creates a new index workspace tool backed by the Engine.
func NewIndexWorkspaceTool(eng *engine.Engine) *IndexWorkspaceTool {
	return &IndexWorkspaceTool{
		engine: eng,
	}
}

func (t *IndexWorkspaceTool) Name() string { return "rag_index_workspace" }
func (t *IndexWorkspaceTool) Description() string {
	return "Indexes or reindexes a workspace for semantic code search. " +
		"Use this tool to manually trigger indexing if search results are stale or 'workspace not indexed'. " +
		"Analyzes all supported source files and stores vectors for semantic search. " +
		"The process runs in the background."
}

type IndexWorkspaceInput struct {
	FilePath           string `json:"file_path,omitempty"`
	Recreate           bool   `json:"recreate,omitempty"`
	IncludeRuntimeInfo bool   `json:"include_runtime_info,omitempty"`
}

func (t *IndexWorkspaceTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input IndexWorkspaceInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{
			"file_path":            input.FilePath,
			"recreate":             input.Recreate,
			"include_runtime_info": input.IncludeRuntimeInfo,
		}

		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_index_workspace failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}

		logger.Instance.Info("rag_index_workspace completed in %v", time.Since(start))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *IndexWorkspaceTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	filePath, _ := params["file_path"].(string)

	// Detect workspace context
	wctx, err := t.engine.DetectContext(ctx, filePath)
	if err != nil {
		response := ToolResponse{
			Status: "error",
			Error:  err.Error(),
		}
		body, _ := response.JSON()
		return body, err
	}

	recreate, _ := params["recreate"].(bool)

	// Trigger async indexing
	t.engine.StartIndexingAsync(wctx.Root, wctx.ID, nil, recreate)

	response := ToolResponse{
		Status:  "indexing_started",
		Message: fmt.Sprintf("🚀 Indexing started for workspace '%s'. The process is running in the background. You can use rag_search (or rag_search_code) immediately - results will appear as indexing progresses.", wctx.Root),
		Context: ContextMetadata{
			WorkspaceRoot:   wctx.Root,
			DetectionSource: wctx.DetectionSource,
		},
	}

	data := map[string]interface{}{}

	includeRuntimeInfo, _ := params["include_runtime_info"].(bool)
	if includeRuntimeInfo {
		// Expose only non-sensitive runtime information to avoid leaking
		// precise uptime or internal build metadata (commit hash/date).
		data["runtime"] = map[string]interface{}{
			"go_version": runtime.Version(),
			"build_info": map[string]string{
				"version": serverVersion,
			},
		}
	}

	// Include current indexing progress so the AI knows how many files are left
	if prog := BuildIndexingProgress(t.engine, wctx.ID, wctx.Root); prog != nil {
		data["indexing_progress"] = prog
	}

	response.Data = data

	// Warn if fallback was used
	response.SetFallbackWarning(wctx.DetectionSource == "registry_fallback")

	return response.JSON()
}
