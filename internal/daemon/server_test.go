package daemon_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
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
	assert.Contains(t, err.Error(), "address already in use")
}
