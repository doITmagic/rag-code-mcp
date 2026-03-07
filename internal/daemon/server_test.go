package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenAndServe_UnixSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	pidPath := filepath.Join(dir, "test.pid")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})

	go func() {
		_ = ListenAndServe(ctx, ListenConfig{
			SocketPath: sockPath,
			PIDPath:    pidPath,
			Version:    "1.0.0-test",
			HTTPPort:   0, // disabled
			OnReady:    func() { close(ready) },
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}),
		})
	}()

	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not become ready in 3s")
	}

	// Connect via Unix socket and hit /health
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
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var health HealthResponse
	body, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(body, &health)
	require.NoError(t, err)
	assert.Equal(t, "ok", health.Status)
	assert.Equal(t, "1.0.0-test", health.Version)
	assert.Equal(t, os.Getpid(), health.PID)
}

func TestListenAndServe_PIDFileWritten(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	pidPath := filepath.Join(dir, "test.pid")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})

	go func() {
		_ = ListenAndServe(ctx, ListenConfig{
			SocketPath: sockPath,
			PIDPath:    pidPath,
			Version:    "2.0.0",
			HTTPPort:   0,
			OnReady:    func() { close(ready) },
		})
	}()

	<-ready

	// Verify PID file
	info, err := ReadPID(pidPath)
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), info.PID)
	assert.Equal(t, "2.0.0", info.Version)

	// Cancel and wait for cleanup
	cancel()
	time.Sleep(200 * time.Millisecond)

	// Verify socket file was cleaned up
	_, err = os.Stat(sockPath)
	assert.True(t, os.IsNotExist(err), "socket file should be removed after shutdown")
}

func TestListenAndServe_MCPHandler(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	pidPath := filepath.Join(dir, "test.pid")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})

	// Custom handler simulating MCP
	mcpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	go func() {
		_ = ListenAndServe(ctx, ListenConfig{
			SocketPath: sockPath,
			PIDPath:    pidPath,
			Version:    "1.0.0",
			HTTPPort:   0,
			OnReady:    func() { close(ready) },
			Handler:    mcpHandler,
		})
	}()

	<-ready

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}

	// Send MCP-like JSON-RPC request
	resp, err := client.Post("http://daemon/mcp",
		"application/json",
		io.NopCloser(
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`),
		),
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]any
	body, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(body, &result)
	require.NoError(t, err)
	assert.Equal(t, float64(1), result["id"])
	assert.NotNil(t, result["result"])
}

func TestListenAndServe_StaleSocketCleanup(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	pidPath := filepath.Join(dir, "test.pid")

	// Create a stale socket file
	require.NoError(t, os.WriteFile(sockPath, []byte("stale"), 0644))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})

	go func() {
		_ = ListenAndServe(ctx, ListenConfig{
			SocketPath: sockPath,
			PIDPath:    pidPath,
			Version:    "1.0.0",
			HTTPPort:   0,
			OnReady:    func() { close(ready) },
		})
	}()

	select {
	case <-ready:
		// Success — daemon started despite stale file
	case <-time.After(3 * time.Second):
		t.Fatal("daemon should have cleaned up stale socket and started")
	}
}
