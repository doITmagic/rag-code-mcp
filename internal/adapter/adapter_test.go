package adapter_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
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
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0", "result": ` + string(body) + `}`))
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

	err = adapter.RunBridge(ctx, port, stdin, &stdout, "/test/workspace", nil)
	require.NoError(t, err)

	// Validate results
	assert.Equal(t, 2, hitCount)
	outContent := stdout.String()
	assert.Contains(t, outContent, `{"jsonrpc":"2.0","id":1,"method":"foo"}`)
	assert.Contains(t, outContent, `{"jsonrpc":"2.0","id":2,"method":"bar"}`)
}

func TestRunBridgeRefreshesResolvedWorkspace(t *testing.T) {
	steps := []struct {
		requestRoot  string
		responseRoot string
		status       int
	}{
		{"", "/project-a", http.StatusOK},
		{"/project-a", "/project-b", http.StatusOK},
		{"/project-b", "", http.StatusOK},
		{"/project-b", "/failed-project", http.StatusInternalServerError},
		{"/project-b", "", http.StatusOK},
	}
	hitCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hitCount >= len(steps) {
			t.Error("unexpected extra request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		step := steps[hitCount]
		hitCount++
		assert.Equal(t, step.requestRoot, r.Header.Get("X-Workspace-Root"), "request %d", hitCount)
		if step.responseRoot != "" {
			w.Header().Set("X-Resolved-Workspace", step.responseRoot)
		}
		w.WriteHeader(step.status)
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer server.Close()

	stdin := strings.NewReader(strings.Repeat("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\"}\n", len(steps)))
	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := adapter.RunBridge(ctx, server.Listener.Addr().(*net.TCPAddr).Port, stdin, &stdout, "/untrusted-cwd", nil)
	require.NoError(t, err)
	assert.Equal(t, len(steps), hitCount)
	assert.Contains(t, stdout.String(), "daemon returned HTTP 500")
}

// A daemon that died mid-session is started again instead of failing every
// later request with "daemon unreachable".
func TestRunBridgeRestartsDeadDaemon(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = io.WriteString(w, `{"status":"ok"}`)
			return
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	})
	dead := httptest.NewServer(handler)
	port := dead.Listener.Addr().(*net.TCPAddr).Port
	dead.Close()

	restarts := 0
	restart := func() error {
		restarts++
		ln, err := net.Listen("tcp", dead.Listener.Addr().String())
		if err != nil {
			return err
		}
		srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: handler}}
		srv.Start()
		t.Cleanup(srv.Close)
		return nil
	}

	stdin := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := adapter.RunBridge(ctx, port, stdin, &stdout, "", restart)
	require.NoError(t, err)
	assert.Equal(t, 1, restarts)
	assert.Equal(t, `{"jsonrpc":"2.0","id":1,"result":{}}`+"\n", stdout.String())
}

// One oversized request must get a JSON-RPC error, not end the whole session.
func TestRunBridgeRejectsOversizedLineAndContinues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{}}`)
	}))
	defer server.Close()

	huge := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"x":"` + strings.Repeat("a", 10*1024*1024) + `"}}`
	stdin := strings.NewReader(huge + "\n" + `{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n")
	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := adapter.RunBridge(ctx, server.Listener.Addr().(*net.TCPAddr).Port, stdin, &stdout, "", nil)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], `"code":-32000`)
	// The client matches the error to its pending request by id.
	assert.Contains(t, lines[0], `"id":1,`)
	assert.Equal(t, `{"jsonrpc":"2.0","id":2,"result":{}}`, lines[1])
}

// A notification gets 202 with an empty body from the daemon; the adapter must
// not turn it into a blank stdout line, which line-based MCP clients reject.
func TestRunBridgeSkipsEmptyNotificationReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"id"`)) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer server.Close()

	stdin := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := adapter.RunBridge(ctx, server.Listener.Addr().(*net.TCPAddr).Port, stdin, &stdout, "", nil)
	require.NoError(t, err)
	assert.Equal(t, `{"jsonrpc":"2.0","id":1,"result":{}}`+"\n", stdout.String())
}

// A request the daemon received before dying must not be sent again to the
// restarted daemon: it may be what killed it, or not be safe to run twice.
func TestRunBridgeDoesNotResendDeliveredRequest(t *testing.T) {
	var delivered atomic.Int32
	handler := func(die func(w http.ResponseWriter)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				_, _ = io.WriteString(w, `{"status":"ok"}`)
				return
			}
			delivered.Add(1)
			die(w)
		}
	}
	var dying *httptest.Server
	dying = httptest.NewServer(handler(func(w http.ResponseWriter) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		dying.Listener.Close()
		conn.Close()
	}))
	defer dying.Close()
	addr := dying.Listener.Addr().String()

	restarts := 0
	restart := func() error {
		restarts++
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: handler(func(w http.ResponseWriter) {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		})}}
		srv.Start()
		t.Cleanup(srv.Close)
		return nil
	}

	stdin := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}` + "\n")
	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := adapter.RunBridge(ctx, dying.Listener.Addr().(*net.TCPAddr).Port, stdin, &stdout, "", restart)
	require.NoError(t, err)
	assert.Equal(t, int32(1), delivered.Load())
	assert.Equal(t, 0, restarts)
	assert.Contains(t, stdout.String(), `"id":1`)
	assert.Contains(t, stdout.String(), "daemon unreachable")
}
