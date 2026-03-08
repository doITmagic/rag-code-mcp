package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBridge_StickyWorkspace verifies that after the daemon responds with
// X-Resolved-Workspace header, the adapter sends X-Workspace-Root on
// all subsequent requests.
func TestBridge_StickyWorkspace(t *testing.T) {
	var mu sync.Mutex
	var receivedRoots []string
	callNum := 0

	sockPath := startFakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callNum++
		n := callNum
		receivedRoots = append(receivedRoots, r.Header.Get("X-Workspace-Root"))
		mu.Unlock()

		// First request: daemon resolves workspace and sends back X-Resolved-Workspace
		if n == 1 {
			w.Header().Set("X-Resolved-Workspace", "/home/user/project")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": n, "result": "ok"})
	})

	// Send 3 requests — workspaceHint is ignored
	input := `{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"c"}` + "\n"
	stdout := &bytes.Buffer{}

	err := RunBridge(context.Background(), sockPath, strings.NewReader(input), stdout, "/home/user")
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, receivedRoots, 3, "should have received 3 requests")

	// Request 1: adapter doesn't know workspace yet — no X-Workspace-Root
	assert.Empty(t, receivedRoots[0], "first request should NOT have Root (not learned yet)")

	// Request 2+3: adapter learned from header → sends X-Workspace-Root
	assert.Equal(t, "/home/user/project", receivedRoots[1], "second request should use Root from header")
	assert.Equal(t, "/home/user/project", receivedRoots[2], "third request should use Root from header")
}

// TestBridge_StickyWorkspace_NoHeader verifies that when daemon never sends
// X-Resolved-Workspace, X-Workspace-Root is never sent.
func TestBridge_StickyWorkspace_NoHeader(t *testing.T) {
	var mu sync.Mutex
	var receivedRoots []string

	sockPath := startFakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedRoots = append(receivedRoots, r.Header.Get("X-Workspace-Root"))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
	})

	input := `{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n"
	err := RunBridge(context.Background(), sockPath, strings.NewReader(input), io.Discard, "/my/workspace")
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, receivedRoots, 2)
	assert.Empty(t, receivedRoots[0], "no Root when daemon sends no header")
	assert.Empty(t, receivedRoots[1], "no Root when daemon sends no header")
}

// TestBridge_StickyWorkspace_IDEHintIgnored verifies that the IDE's
// workspaceHint is never forwarded as X-Workspace-Hint.
func TestBridge_StickyWorkspace_IDEHintIgnored(t *testing.T) {
	sockPath := startFakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("X-Workspace-Hint"), "IDE hint should never be forwarded")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
	})

	input := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	err := RunBridge(context.Background(), sockPath, strings.NewReader(input), io.Discard, "/home/user")
	require.NoError(t, err)
}
