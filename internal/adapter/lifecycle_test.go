package adapter_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

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
