package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"time"
)

// HealthResponse is the JSON payload returned by the health endpoint.
type HealthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	UptimeSeconds int    `json:"uptime_seconds"`
	PID           int    `json:"pid"`
}

// HealthHandler returns an http.HandlerFunc that reports daemon health.
// Used by adapters to verify the daemon is alive and check its version.
func HealthHandler(version string, startTime time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := HealthResponse{
			Status:        "ok",
			Version:       version,
			UptimeSeconds: int(time.Since(startTime).Seconds()),
			PID:           os.Getpid(),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
