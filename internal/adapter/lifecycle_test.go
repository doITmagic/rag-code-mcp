package adapter_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/adapter"
	"github.com/doITmagic/rag-code-mcp/internal/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDaemonRunning_Success(t *testing.T) {
	// Mock a healthy daemon response
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/health", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusOK)
		resp := daemon.HealthResponse{
			Status:  "ok",
			Version: "1.2.3",
			PID:     1234,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	server.Start()
	defer server.Close()

	parts := strings.Split(server.Listener.Addr().String(), ":")
	port, err := strconv.Atoi(parts[len(parts)-1])
	require.NoError(t, err)

	running, version := adapter.IsDaemonRunning(port)
	assert.True(t, running)
	assert.Equal(t, "1.2.3", version)
}

func TestIsDaemonRunning_Failure(t *testing.T) {
	// Attempt reaching a port that is definitively not open/handling HTTP
	running, version := adapter.IsDaemonRunning(12345)
	assert.False(t, running)
	assert.Empty(t, version)
}

// A daemon that exits at once (e.g. a dependency is down) must be reported
// right away, not after the full readiness timeout.
func TestStartDaemonReportsEarlyExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the daemon binary")
	}
	bin := filepath.Join(t.TempDir(), "daemon.sh")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o700))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	start := time.Now()
	err = adapter.StartDaemon(bin, port)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited")
	assert.Less(t, time.Since(start), 10*time.Second)
}

// A daemon that cannot bind its port because another socket holds it (an
// outgoing connection got it as its ephemeral port) must be reported as
// such, not blamed on Ollama or Qdrant.
func TestStartDaemonReportsPortInUse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the daemon binary")
	}
	bin := filepath.Join(t.TempDir(), "daemon.sh")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o700))
	peer, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer peer.Close()
	conn, err := net.Dial("tcp", peer.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	port := conn.LocalAddr().(*net.TCPAddr).Port

	err = adapter.StartDaemon(bin, port)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port "+strconv.Itoa(port)+" is in use")
}
