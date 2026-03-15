package prompts

import (
	"context"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Register registers all MCP prompts provided by this package on the given mcp.Server.
func Register(mcpServer *mcp.Server, eng *engine.Engine) {
	mcpServer.AddPrompt(&mcp.Prompt{
		Name:        "System Diagnostics",
		Description: "Analyze the current indexing status and troubleshoot common issues based on the workspace resources.",
		Title:       "Analyze RagCode Indexing System",
	}, func(ctx context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		// Idem cu Resources: detectie automata prin ctx, fara logica proprie.
		wctx, err := eng.DetectContext(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("failed to detect workspace: %w", err)
		}

		promptText := fmt.Sprintf(`Please analyze the current indexing status of the RagCode MCP server for the workspace located at: %s

You can inspect the indexing status by reading the resource directly via URI: "ragcode://status/indexing" or using your available tools.
Then, inform me if there are any issues such as stuck processes or parsing errors.`, wctx.Root)

		return &mcp.GetPromptResult{
			Description: "Prompt to diagnose indexing and execution issues automatically.",
			Messages: []*mcp.PromptMessage{
				{
					Role: mcp.Role("user"),
					Content: &mcp.TextContent{
						Text: promptText,
					},
				},
			},
		}, nil
	})
}
