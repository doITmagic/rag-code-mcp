package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBridge_ForwardsRequestAndResponse(t *testing.T) {
	sockPath := startFakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result":  map[string]any{"tools": []string{"rag_search"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"
	stdout := &bytes.Buffer{}

	err := RunBridge(context.Background(), sockPath, strings.NewReader(input), stdout, "")
	require.NoError(t, err)

	var resp map[string]any
	err = json.Unmarshal(stdout.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, float64(1), resp["id"])
	assert.NotNil(t, resp["result"])
}

func TestBridge_SendsWorkspaceHintHeader(t *testing.T) {
	var receivedHint string

	sockPath := startFakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		receivedHint = r.Header.Get("X-Workspace-Hint")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
	})

	input := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	err := RunBridge(context.Background(), sockPath, strings.NewReader(input), io.Discard, "/home/user/project")
	require.NoError(t, err)

	assert.Equal(t, "/home/user/project", receivedHint)
}

func TestBridge_SkipsEmptyLines(t *testing.T) {
	callCount := 0
	sockPath := startFakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
	})

	input := "\n\n" + `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n\n  \n"
	err := RunBridge(context.Background(), sockPath, strings.NewReader(input), io.Discard, "")
	require.NoError(t, err)

	assert.Equal(t, 1, callCount, "only one real JSON line should be forwarded")
}

func TestBridge_MultipleRequests(t *testing.T) {
	sockPath := startFakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": "ok"})
	})

	input := `{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"c"}` + "\n"
	stdout := &bytes.Buffer{}

	err := RunBridge(context.Background(), sockPath, strings.NewReader(input), stdout, "")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	assert.Len(t, lines, 3, "should get 3 responses for 3 requests")
}

func TestBridge_DaemonUnreachable(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"test"}` + "\n"
	stdout := &bytes.Buffer{}

	// Use a non-existent socket path
	err := RunBridge(context.Background(), "/tmp/nonexistent.sock", strings.NewReader(input), stdout, "")
	require.NoError(t, err) // bridge itself should not error — it writes JSON-RPC error to stdout

	var resp map[string]any
	err = json.Unmarshal(stdout.Bytes(), &resp)
	require.NoError(t, err)
	assert.NotNil(t, resp["error"], "should receive a JSON-RPC error")
	assert.Equal(t, float64(1), resp["id"], "error should preserve the request id")
}

// --- Helper ---

func startFakeDaemon(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	dir := t.TempDir()
	sockPath := dir + "/test.sock"

	listener, err := net.Listen("unix", sockPath)
	require.NoError(t, err)

	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { srv.Close() })

	return sockPath
}
