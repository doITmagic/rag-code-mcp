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
// X-Resolved-Workspace, the adapter switches from X-Workspace-Hint to
// X-Workspace-Root on subsequent requests.
func TestBridge_StickyWorkspace(t *testing.T) {
	var mu sync.Mutex
	var headers []struct {
		hint string
		root string
	}
	callNum := 0

	sockPath := startFakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callNum++
		n := callNum
		headers = append(headers, struct {
			hint string
			root string
		}{
			hint: r.Header.Get("X-Workspace-Hint"),
			root: r.Header.Get("X-Workspace-Root"),
		})
		mu.Unlock()

		// First request: daemon resolves workspace and sends back X-Resolved-Workspace
		if n == 1 {
			w.Header().Set("X-Resolved-Workspace", "/home/user/project")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": n, "result": "ok"})
	})

	// Send 3 requests with a workspace hint
	input := `{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"c"}` + "\n"
	stdout := &bytes.Buffer{}

	err := RunBridge(context.Background(), sockPath, strings.NewReader(input), stdout, "/home/user/project")
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, headers, 3, "should have received 3 requests")

	// Request 1: adapter doesn't know workspace yet → sends X-Workspace-Hint
	assert.Equal(t, "/home/user/project", headers[0].hint, "first request should use Hint")
	assert.Empty(t, headers[0].root, "first request should NOT have Root")

	// Request 2+3: adapter learned workspace → sends X-Workspace-Root
	assert.Empty(t, headers[1].hint, "second request should NOT send Hint")
	assert.Equal(t, "/home/user/project", headers[1].root, "second request should use Root")

	assert.Empty(t, headers[2].hint, "third request should NOT send Hint")
	assert.Equal(t, "/home/user/project", headers[2].root, "third request should use Root")
}

// TestBridge_StickyWorkspace_NoHeaderNoSwitch verifies that if the daemon
// does NOT respond with X-Resolved-Workspace, the adapter keeps sending
// X-Workspace-Hint on every request.
func TestBridge_StickyWorkspace_NoHeaderNoSwitch(t *testing.T) {
	var mu sync.Mutex
	var hints []string

	sockPath := startFakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hints = append(hints, r.Header.Get("X-Workspace-Hint"))
		mu.Unlock()
		// No X-Resolved-Workspace header
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
	})

	input := `{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n"
	err := RunBridge(context.Background(), sockPath, strings.NewReader(input), io.Discard, "/my/workspace")
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, hints, 2)
	assert.Equal(t, "/my/workspace", hints[0], "should keep sending hint on first request")
	assert.Equal(t, "/my/workspace", hints[1], "should keep sending hint when no resolved header")
}

// TestBridge_StickyWorkspace_NoHintNoHeader verifies that when no workspace
// hint is provided at all, neither X-Workspace-Hint nor X-Workspace-Root
// are sent in requests.
func TestBridge_StickyWorkspace_NoHintNoHeader(t *testing.T) {
	sockPath := startFakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("X-Workspace-Hint"), "should not have Hint")
		assert.Empty(t, r.Header.Get("X-Workspace-Root"), "should not have Root")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
	})

	input := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	err := RunBridge(context.Background(), sockPath, strings.NewReader(input), io.Discard, "")
	require.NoError(t, err)
}
