package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// IndexWorkspaceTool implements the rag_index_workspace MCP tool.
type IndexWorkspaceTool struct {
	engine *engine.Engine
	// pendingConfirmations stores nonces for validated workspace roots.
	// Key: workspace root path, Value: nonce string with expiry.
	pendingConfirmations sync.Map // map[string]confirmationEntry
}

type confirmationEntry struct {
	Nonce  string
	Expiry time.Time
}

func generateNonce() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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
	ConfirmationToken  string `json:"confirmation_token,omitempty"`
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
			"confirmation_token":   input.ConfirmationToken,
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

	// Detect workspace context using explicit workspace root path (routes through
	// req.WorkspaceRoot for correct resolver semantics: ReasonExplicitWorkspaceRoot)
	wctx, err := t.engine.DetectContextAsRoot(ctx, workspaceRoot)
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
		// Generate a confirmation nonce for this workspace root
		nonce := generateNonce()
		t.pendingConfirmations.Store(wctx.Root, confirmationEntry{
			Nonce:  nonce,
			Expiry: time.Now().Add(5 * time.Minute),
		})

		response := ToolResponse{
			Status:  "confirmation_required",
			Message: fmt.Sprintf("Workspace detected successfully at '%s'. \nVerify this is the correct top-level folder! \n\nCall the tool again passing \"confirm\": true and \"confirmation_token\": \"%s\" alongside the 'workspace_root' to begin indexing.", wctx.Root, nonce),
			Context: ContextMetadata{
				WorkspaceRoot:   wctx.Root,
				DetectionSource: wctx.DetectionSource,
			},
			Data: map[string]interface{}{
				"confirmation_token": nonce,
			},
		}
		return response.JSON()
	}

	// Confirm flow: validate the confirmation token
	token, _ := params["confirmation_token"].(string)
	if entry, ok := t.pendingConfirmations.LoadAndDelete(wctx.Root); ok {
		ce := entry.(confirmationEntry)
		if time.Now().After(ce.Expiry) {
			response := ToolResponse{
				Status: "error",
				Error:  "Confirmation token expired. Please run the validation step again (without confirm: true) to get a new token.",
			}
			return response.JSON()
		}
		if token != ce.Nonce {
			// Re-store the entry so the user can retry with the correct token
			t.pendingConfirmations.Store(wctx.Root, ce)
			response := ToolResponse{
				Status: "error",
				Error:  fmt.Sprintf("Invalid confirmation_token. Expected the token returned from the validation step. Please provide 'confirmation_token': '%s'.", ce.Nonce),
			}
			return response.JSON()
		}
	} else {
		// No pending validation — user sent confirm:true without prior validation
		response := ToolResponse{
			Status: "error",
			Error:  "No pending validation found. Please run the tool first without 'confirm: true' to validate the workspace root and receive a confirmation_token.",
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
