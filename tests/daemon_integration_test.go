package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/adapter"
	"github.com/doITmagic/rag-code-mcp/internal/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

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

// startTestDaemon starts a daemon with echo handler and returns the dynamically assigned port + cleanup func.
func startTestDaemon(t *testing.T) (port int, cancel context.CancelFunc) {
	t.Helper()

	var err error
	port, err = getFreePort()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})

	go func() {
		_ = daemon.ListenAndServe(ctx, daemon.ListenConfig{
			Port:       port,
			Version:    "1.0.0-test",
			OnReady:    func() { close(ready) },
			Handler:    echoMCPHandler(),
		})
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("test daemon did not start in 5s")
	}

	return port, cancel
}

func TestIntegration_DaemonStartAndToolsList(t *testing.T) {
	port, cancel := startTestDaemon(t)
	defer cancel()

	// Bridge a single request through the adapter
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"
	stdout := &bytes.Buffer{}

	err := adapter.RunBridge(context.Background(), port, strings.NewReader(input), stdout, "/test/workspace")
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
	port, cancel := startTestDaemon(t)
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
				input += `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"ping"}` + "\n"
			}

			stdout := &bytes.Buffer{}
			errors[idx] = adapter.RunBridge(context.Background(), port, strings.NewReader(input), stdout, "")
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

func TestIntegration_HealthEndpointReturnsVersion(t *testing.T) {
	port, cancel := startTestDaemon(t)
	defer cancel()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/health")
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
