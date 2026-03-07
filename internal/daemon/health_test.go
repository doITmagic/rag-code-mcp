package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler_ReturnsOK(t *testing.T) {
	handler := HealthHandler("2.1.54", time.Now())
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "2.1.54", resp.Version)
	assert.Greater(t, resp.PID, 0)
}

func TestHealthHandler_UptimeIncreases(t *testing.T) {
	startTime := time.Now().Add(-120 * time.Second)
	handler := HealthHandler("1.0.0", startTime)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var resp HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, resp.UptimeSeconds, 119) // allow 1s tolerance
}

func TestHealthHandler_VersionPropagated(t *testing.T) {
	handler := HealthHandler("3.0.0-beta", time.Now())
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var resp HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "3.0.0-beta", resp.Version)
}
