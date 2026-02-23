package tools

import (
	"encoding/json"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/telemetry"
)

// ToolResponse defines the standard JSON structure for all RagCode MCP tools.
type ToolResponse struct {
	Status  string          `json:"status"`            // "success", "error", "indexing_started", "no_results"
	Message string          `json:"message,omitempty"` // User-friendly status message
	Warning string          `json:"warning,omitempty"` // Non-fatal warnings (e.g. branch mismatch)
	Error   string          `json:"error,omitempty"`   // Error details for status="error"
	Context ContextMetadata `json:"context"`           // Metadata about the execution environment
	Data    interface{}     `json:"data,omitempty"`    // Tool-specific output data
}

// ContextMetadata provides info about which workspace and sources were used.
type ContextMetadata struct {
	WorkspaceRoot   string             `json:"workspace_root,omitempty"`
	DetectionSource string             `json:"detection_source,omitempty"` // "explicit_file_path", "registry_fallback", "cwd_detection"
	Language        string             `json:"language,omitempty"`
	Collection      string             `json:"collection,omitempty"`
	Telemetry       *telemetry.Savings `json:"telemetry,omitempty"`
}

// JSON returns the marshaled JSON string of the response.
func (r ToolResponse) JSON() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal response: %w", err)
	}
	return string(b), nil
}

// SetFallbackWarning sets a warning if the workspace was inferred.
func (r *ToolResponse) SetFallbackWarning(inferred bool) {
	if inferred {
		r.Warning = fmt.Sprintf("Workspace root was inferred from history (%s). Results may be inaccurate if you've changed projects.", r.Context.WorkspaceRoot)
	}
}

// ContextFromWorkspace builds a ContextMetadata from a resolved WorkspaceContext.
func ContextFromWorkspace(wctx *engine.WorkspaceContext) ContextMetadata {
	if wctx == nil {
		return ContextMetadata{}
	}
	return ContextMetadata{
		WorkspaceRoot:   wctx.Root,
		DetectionSource: wctx.DetectionSource,
	}
}
