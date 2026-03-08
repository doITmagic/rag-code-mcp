package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/adapter"
	"github.com/doITmagic/rag-code-mcp/internal/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoMCPHandler is a simple handler that echoes back the method name.
func echoMCPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]any{
				"method": req["method"],
				"echo":   true,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	return mux
}

// startTestDaemon starts a daemon with echo handler and returns paths + cleanup func.
func startTestDaemon(t *testing.T) (sockPath string, pidPath string, cancel context.CancelFunc) {
	t.Helper()
	dir := t.TempDir()
	sockPath = filepath.Join(dir, "daemon.sock")
	pidPath = filepath.Join(dir, "daemon.pid")

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})

	go func() {
		_ = daemon.ListenAndServe(ctx, daemon.ListenConfig{
			SocketPath: sockPath,
			PIDPath:    pidPath,
			Version:    "1.0.0-test",
			HTTPPort:   0,
			OnReady:    func() { close(ready) },
			Handler:    echoMCPHandler(),
		})
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("test daemon did not start in 5s")
	}

	return sockPath, pidPath, cancel
}

func TestIntegration_DaemonStartAndToolsList(t *testing.T) {
	sockPath, _, cancel := startTestDaemon(t)
	defer cancel()

	// Bridge a single request through the adapter
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"
	stdout := &bytes.Buffer{}

	err := adapter.RunBridge(context.Background(), sockPath,
		strings.NewReader(input), stdout, "/test/workspace")
	require.NoError(t, err)

	var resp map[string]any
	err = json.Unmarshal(stdout.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, float64(1), resp["id"])

	result, ok := resp["result"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "tools/list", result["method"])
	assert.Equal(t, true, result["echo"])
}

func TestIntegration_MultipleAdaptersConcurrent(t *testing.T) {
	sockPath, _, cancel := startTestDaemon(t)
	defer cancel()

	// Run 5 adapters concurrently, each sending 3 requests
	var wg sync.WaitGroup
	results := make([]string, 5)
	errors := make([]error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			input := ""
			for j := 1; j <= 3; j++ {
				id := idx*10 + j
				input += fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"ping"}`, id) + "\n"
			}

			stdout := &bytes.Buffer{}
			errors[idx] = adapter.RunBridge(context.Background(), sockPath,
				strings.NewReader(input), stdout, "")
			results[idx] = stdout.String()
		}(i)
	}

	wg.Wait()

	for i := 0; i < 5; i++ {
		assert.NoError(t, errors[i], "adapter %d should not error", i)
		lines := strings.Split(strings.TrimSpace(results[i]), "\n")
		assert.Len(t, lines, 3, "adapter %d should get 3 responses", i)
	}
}

func TestIntegration_DaemonSurvivesAdapterEOF(t *testing.T) {
	sockPath, _, cancel := startTestDaemon(t)
	defer cancel()

	// First adapter connects and disconnects
	input1 := `{"jsonrpc":"2.0","id":1,"method":"first"}` + "\n"
	err := adapter.RunBridge(context.Background(), sockPath,
		strings.NewReader(input1), io.Discard, "")
	require.NoError(t, err)

	// Daemon should still be alive — second adapter should work
	input2 := `{"jsonrpc":"2.0","id":2,"method":"second"}` + "\n"
	stdout := &bytes.Buffer{}
	err = adapter.RunBridge(context.Background(), sockPath,
		strings.NewReader(input2), stdout, "")
	require.NoError(t, err)

	var resp map[string]any
	err = json.Unmarshal(stdout.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, float64(2), resp["id"], "second adapter should get response")
}

func TestIntegration_HealthEndpointReturnsVersion(t *testing.T) {
	sockPath, _, cancel := startTestDaemon(t)
	defer cancel()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}

	resp, err := client.Get("http://daemon/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	var health daemon.HealthResponse
	body, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(body, &health)
	require.NoError(t, err)

	assert.Equal(t, "ok", health.Status)
	assert.Equal(t, "1.0.0-test", health.Version)
	assert.Greater(t, health.PID, 0)
	assert.GreaterOrEqual(t, health.UptimeSeconds, 0)
}

func TestIntegration_IDEHintNotForwarded(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	defer listener.Close()

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// IDE hint should never be forwarded as a header
		assert.Empty(t, r.Header.Get("X-Workspace-Hint"), "IDE hint must not be forwarded")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
	})}
	go func() { _ = srv.Serve(listener) }()
	defer srv.Close()

	input := `{"jsonrpc":"2.0","id":1,"method":"test"}` + "\n"
	err = adapter.RunBridge(context.Background(), sockPath,
		strings.NewReader(input), io.Discard, "/home/user/my-project")
	require.NoError(t, err)
}
