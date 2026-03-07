// Package transport provides shared context keys and helpers for the
// daemon ↔ adapter transport layer. It has zero internal dependencies,
// so both daemon and engine can import it without cycles.
package transport

import "context"

// contextKey is an unexported type to prevent collisions with keys from other packages.
type contextKey string

// workspaceHintKey is the context key for the X-Workspace-Hint header value.
const workspaceHintKey contextKey = "workspace_hint"

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
