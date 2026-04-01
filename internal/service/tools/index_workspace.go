package tools

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
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
		"Instead of abstract paths, you MUST explicitly provide the absolute 'workspace_root' directory. " +
		"The tool first validates the root and returns early. Then, you submit a second call with 'confirm': true " +
		"to actually lock-in the registry root and start the background indexing."
}

type IndexWorkspaceInput struct {
	WorkspaceRoot      string `json:"workspace_root,omitempty"`
	Confirm            bool   `json:"confirm,omitempty"`
	Recreate           bool   `json:"recreate,omitempty"`
	IncludeRuntimeInfo bool   `json:"include_runtime_info,omitempty"`
}

func (t *IndexWorkspaceTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input IndexWorkspaceInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{
			"workspace_root":       input.WorkspaceRoot,
			"confirm":              input.Confirm,
			"recreate":             input.Recreate,
			"include_runtime_info": input.IncludeRuntimeInfo,
		}

		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_index_workspace failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			if result != "" {
				res.Content = []mcp.Content{&mcp.TextContent{Text: result}}
			}
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
	workspaceRoot, ok := params["workspace_root"].(string)
	if !ok || workspaceRoot == "" {
		response := ToolResponse{
			Status: "error",
			Error:  "workspace_root parameter is strictly required to explicitly pinpoint the directory to index.",
		}
		return response.JSON()
	}

	confirm, _ := params["confirm"].(bool)

	// Detect workspace context using explicit root validation 
	wctx, err := t.engine.DetectContext(ctx, workspaceRoot)
	if err != nil {
		candidates := t.engine.FindAlternativeCandidates(workspaceRoot)
		msg := err.Error()
		if len(candidates) > 0 {
			msg = fmt.Sprintf("Validation failed: %s.\n\nSuggested local roots (Tiers applied):\n- %s", err.Error(), strings.Join(candidates, "\n- "))
		}
		
		response := ToolResponse{
			Status: "error",
			Error:  msg,
		}
		return response.JSON()
	}

	if !confirm {
		response := ToolResponse{
			Status:  "confirmation_required",
			Message: fmt.Sprintf("Workspace detected successfully at '%s'. \nVerify this is the correct top-level folder! \n\nCall the tool again passing \"confirm\": true alongside the 'workspace_root' to begin indexing.", wctx.Root),
			Context: ContextMetadata{
				WorkspaceRoot:   wctx.Root,
				DetectionSource: wctx.DetectionSource,
			},
		}
		return response.JSON()
	}

	recreate, _ := params["recreate"].(bool)

	// Trigger async indexing
	t.engine.StartIndexingAsync(wctx.Root, wctx.ID, nil, recreate)

	response := ToolResponse{
		Status:  "indexing_started",
		Message: fmt.Sprintf("🚀 Indexing started for validated workspace '%s'. The nested constraints were processed and cleanup checks triggered.", wctx.Root),
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

	// Include current indexing status
	if s := indexer.LoadIndexStatus(wctx.Root); s != nil {
		data["indexing_progress"] = s
	}

	response.Data = data

	// Warn if fallback was used
	response.SetFallbackWarning(wctx.DetectionSource == "registry_fallback")

	return response.JSON()
}
