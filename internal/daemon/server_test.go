package daemon_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"testing"
	"time"

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

func TestListenAndServe_Success(t *testing.T) {
	port, err := getFreePort()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})
	go func() {
		_ = daemon.ListenAndServe(ctx, daemon.ListenConfig{
			Port:    port,
			Version: "test-1.0",
			OnReady: func() { close(ready) },
		})
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not start in time")
	}

	// Verify health endpoint works
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var health daemon.HealthResponse
	err = json.NewDecoder(resp.Body).Decode(&health)
	require.NoError(t, err)
	assert.Equal(t, "test-1.0", health.Version)
}

// Routes beside /mcp (pprof in debug mode) must get the same DNS-rebinding
// protection the SDK gives /mcp: a foreign Host header is refused.
func TestListenAndServe_RejectsForeignHost(t *testing.T) {
	port, err := getFreePort()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})
	go func() {
		_ = daemon.ListenAndServe(ctx, daemon.ListenConfig{
			Port:    port,
			Version: "test-1.0",
			Handler: http.HandlerFunc(pprof.Cmdline),
			OnReady: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not start in time")
	}

	for host, want := range map[string]int{
		"evil.example.com":                       http.StatusForbidden,
		"evil.example.com:" + strconv.Itoa(port): http.StatusForbidden,
		"127.0.0.1:" + strconv.Itoa(port):        http.StatusOK,
		"localhost:" + strconv.Itoa(port):        http.StatusOK,
	} {
		req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/debug/pprof/cmdline", nil)
		require.NoError(t, err)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, want, resp.StatusCode, "Host %s", host)
	}
}

func TestListenAndServe_PortConflict(t *testing.T) {
	port, err := getFreePort()
	require.NoError(t, err)

	// Block the port first
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	require.NoError(t, err)
	defer l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = daemon.ListenAndServe(ctx, daemon.ListenConfig{
		Port:    port,
		Version: "test-1.0",
		OnReady: func() {}, // Should not be called
	})

	// Expect address in use error
	require.Error(t, err)
	// The OS wording differs (Linux "address already in use", Windows "Only one
	// usage of each socket address"); check the daemon's own message instead.
	assert.Contains(t, err.Error(), "failed to bind TCP port")
}
