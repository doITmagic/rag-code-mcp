package tools

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A client that sends capabilities {} (no "roots") and never answers
// server-to-client requests must get the missing-root error at once for an
// empty workspace_root, and the server must not send roots/list at all.
func TestEmptyRootClientWithoutRootsCapability(t *testing.T) {
	v := newEnv(t, false, nil)
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	v.t.register(server)
	ct, st := mcp.NewInMemoryTransports()
	_, err := server.Connect(context.Background(), st, nil)
	require.NoError(t, err)

	var asked atomic.Int32
	c := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"},
		&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}}) // no roots capability
	c.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "roots/list" {
				asked.Add(1)
				<-ctx.Done() // never answer, like the Z13 client
				return nil, ctx.Err()
			}
			return next(ctx, method, req)
		}
	})
	cs, err := c.Connect(context.Background(), ct, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })

	for name, args := range map[string]map[string]any{
		"rag_search":            {"workspace_root": "", "query": "x", "limit": 1},
		"rag_read_file_context": {"workspace_root": "", "file_path": "pkg/a.fk"},
		"rag_index_workspace":   {"workspace_root": ""},
	} {
		// Test-local deadline: today the call hangs on roots/list until it.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		cancel()
		require.NoError(t, err, "%s: no answer before the test deadline (server waits on roots/list)", name)
		assert.True(t, res.IsError, name)
		assert.Contains(t, res.Content[0].(*mcp.TextContent).Text, "workspace_root is missing", name)
	}
	assert.Zero(t, asked.Load(), "roots/list was sent to a client that did not declare roots")
}
