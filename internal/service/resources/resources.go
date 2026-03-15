package resources

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Register registers all MCP resources provided by this package on the given mcp.Server.
func Register(mcpServer *mcp.Server, eng *engine.Engine) {
	mcpServer.AddResource(&mcp.Resource{
		URI:         "ragcode://status/indexing",
		Name:        "Indexing Status",
		Description: "Returns the current indexing status and progress for the workspace.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// Attempt to resolve context
		wctx, err := eng.DetectContext(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("failed to detect workspace: %w", err)
		}

		status := eng.GetIndexStatus(wctx.Root)
		if status == nil {
			return nil, fmt.Errorf("index status not found for workspace %s", wctx.Root)
		}

		blob, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal index status: %w", err)
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      request.Params.URI,
					Text:     string(blob),
					MIMEType: "application/json",
				},
			},
		}, nil
	})
}
