package adapter_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/adapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunBridge(t *testing.T) {
	// Create an HTTP test server to mock the daemon
	hitCount := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/mcp", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		hitCount++
		w.Header().Set("Content-Type", "application/json")
		// Echo it to prove it hit the server
		w.Write([]byte(`{"jsonrpc":"2.0", "result": ` + string(body) + `}`)) 
	}))
	server.Start()
	defer server.Close()

	// Extract port from httptest server listener
	parts := strings.Split(server.Listener.Addr().String(), ":")
	portStr := parts[len(parts)-1]

	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	// Setup stdin with a couple of JSON-RPC requests separated by newline
	stdinData := `{"jsonrpc":"2.0","id":1,"method":"foo"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"bar"}` + "\n"
	stdin := strings.NewReader(stdinData)
	var stdout bytes.Buffer

	// Run bridge
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = adapter.RunBridge(ctx, port, stdin, &stdout, "/test/workspace")
	require.NoError(t, err)

	// Validate results
	assert.Equal(t, 2, hitCount)
	outContent := stdout.String()
	assert.Contains(t, outContent, `{"jsonrpc":"2.0","id":1,"method":"foo"}`)
	assert.Contains(t, outContent, `{"jsonrpc":"2.0","id":2,"method":"bar"}`)
}
