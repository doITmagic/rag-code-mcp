package tools

import (
	"encoding/json"
	"fmt"
	"time"

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

// IndexingProgressSummary is a compact view of the current indexing job.
type IndexingProgressSummary struct {
	State     string                      `json:"state"`               // starting|running|completed|failed
	Elapsed   string                      `json:"elapsed"`             // e.g. "1m23s"
	Languages map[string]LangProgressItem `json:"languages,omitempty"` // per-language stats
}

// LangProgressItem holds progress stats for a single language.
type LangProgressItem struct {
	DoneFiles  int `json:"done_files"`
	TotalFiles int `json:"total_files"`
	Percent    int `json:"percent"`
}

// ContextMetadata provides info about which workspace and sources were used.
type ContextMetadata struct {
	WorkspaceRoot    string                   `json:"workspace_root,omitempty"`
	DetectionSource  string                   `json:"detection_source,omitempty"` // "explicit_file_path", "registry_fallback", "cwd_detection"
	Language         string                   `json:"language,omitempty"`
	Collection       string                   `json:"collection,omitempty"`
	Telemetry        *telemetry.Savings       `json:"telemetry,omitempty"`
	IndexingProgress *IndexingProgressSummary `json:"indexing_progress,omitempty"` // present when indexing is in progress or just completed
}

// BuildIndexingProgress reads the live progress for a workspace and returns a summary.
// Returns nil if no indexing job has been tracked for this workspace.
func BuildIndexingProgress(eng *engine.Engine, workspaceID string) *IndexingProgressSummary {
	if eng == nil {
		return nil
	}
	prog := eng.GetIndexProgress(workspaceID)
	if prog == nil {
		return nil
	}
	elapsed := time.Since(prog.StartedAt).Round(time.Second).String()
	if prog.CompletedAt != nil {
		elapsed = prog.CompletedAt.Sub(prog.StartedAt).Round(time.Second).String()
	}
	langs := make(map[string]LangProgressItem, len(prog.Languages))
	for lang, lp := range prog.Languages {
		langs[lang] = LangProgressItem{
			DoneFiles:  lp.DoneFiles,
			TotalFiles: lp.TotalFiles,
			Percent:    lp.Percent,
		}
	}
	return &IndexingProgressSummary{
		State:     prog.State,
		Elapsed:   elapsed,
		Languages: langs,
	}
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

// ContextFromWorkspaceWithProgress builds ContextMetadata and attaches live indexing progress.
func ContextFromWorkspaceWithProgress(wctx *engine.WorkspaceContext, eng *engine.Engine) ContextMetadata {
	ctx := ContextFromWorkspace(wctx)
	ctx.IndexingProgress = BuildIndexingProgress(eng, wctx.ID)
	return ctx
}
