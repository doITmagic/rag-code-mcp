// Package transport provides shared context keys and helpers for the
// daemon ↔ adapter transport layer. It has zero internal dependencies,
// so both daemon and engine can import it without cycles.
package transport

import (
	"context"
	"net/http"
)

// contextKey is an unexported type to prevent collisions with keys from other packages.
type contextKey string

const (
	// workspaceHintKey is the context key for the X-Workspace-Hint header value.
	workspaceHintKey contextKey = "workspace_hint"
	// responseWriterKey is the context key for the http.ResponseWriter.
	responseWriterKey contextKey = "response_writer"
)

// WithWorkspaceHint returns a new context carrying the workspace hint (IDE's CWD).
func WithWorkspaceHint(ctx context.Context, hint string) context.Context {
	return context.WithValue(ctx, workspaceHintKey, hint)
}

// GetWorkspaceHint extracts the workspace hint from the context.
// Returns empty string if not present.
func GetWorkspaceHint(ctx context.Context) string {
	if hint, ok := ctx.Value(workspaceHintKey).(string); ok {
		return hint
	}
	return ""
}

// WithResponseWriter returns a new context carrying the http.ResponseWriter.
// This allows tools/engine to set response headers (e.g. X-Resolved-Workspace)
// during request processing, before the MCP SDK writes the body.
func WithResponseWriter(ctx context.Context, w http.ResponseWriter) context.Context {
	return context.WithValue(ctx, responseWriterKey, w)
}

// SetResponseHeader sets a header on the http.ResponseWriter stored in context.
// No-op if context has no ResponseWriter. Must be called before the body is written.
func SetResponseHeader(ctx context.Context, key, value string) {
	if w, ok := ctx.Value(responseWriterKey).(http.ResponseWriter); ok {
		w.Header().Set(key, value)
	}
}
