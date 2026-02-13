package tools

import (
	"context"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

// CodeSearcher provides code-only semantic search that excludes documentation chunks.
type CodeSearcher interface {
	SearchCodeOnly(ctx context.Context, query []float64, limit int) ([]memory.Document, error)
}
