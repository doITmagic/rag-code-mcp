package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
)

// PortIsOccupied returns true if the given TCP port is already listening.
func PortIsOccupied(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// QueryMasterVersion sends an MCP initialize request to the running master
// and returns its reported version string (e.g. "2.1.51").
// Returns "" on any error (timeout, parse failure, etc.).
func QueryMasterVersion(port int) string {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "version-check",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"clientInfo": map[string]string{
				"name":    "ragcode-proxy",
				"version": "1.0.0",
			},
			"capabilities": map[string]any{},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	// Parse the response — may be direct JSON or SSE-wrapped.
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}

	// Navigate: result.serverInfo.version
	res, ok := result["result"].(map[string]any)
	if !ok {
		return ""
	}
	serverInfo, ok := res["serverInfo"].(map[string]any)
	if !ok {
		return ""
	}
	version, _ := serverInfo["version"].(string)
	return version
}

// KillProcessOnPort finds the process listening on the given port and kills it.
// On Linux/macOS uses lsof + kill; on Windows uses netstat + taskkill.
// Waits up to 3 seconds for the port to become free.
func KillProcessOnPort(port int) {
	addr := fmt.Sprintf("%d", port)

	if runtime.GOOS == "windows" {
		// netstat -ano | findstr :3000 → extract PID → taskkill
		out, err := exec.Command("cmd", "/C", fmt.Sprintf("netstat -ano | findstr :%s", addr)).Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(strings.TrimSpace(line))
				if len(fields) >= 5 {
					pid := fields[len(fields)-1]
					_ = exec.Command("taskkill", "/F", "/PID", pid).Run()
				}
			}
		}
	} else {
		// lsof -ti :3000 → kill each PID
		out, err := exec.Command("lsof", "-ti", fmt.Sprintf(":%s", addr)).Output()
		if err == nil {
			for _, pid := range strings.Fields(string(out)) {
				if pid != "" {
					logger.Instance.Info("Killing old master process PID=%s on port %d", pid, port)
					_ = exec.Command("kill", pid).Run()
				}
			}
		}
	}

	// Wait for port to become free (max 3s)
	for i := 0; i < 6; i++ {
		if !PortIsOccupied(port) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Force kill if still occupied
	if runtime.GOOS != "windows" {
		out, err := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port)).Output()
		if err == nil {
			for _, pid := range strings.Fields(string(out)) {
				if pid != "" {
					logger.Instance.Warn("Force-killing stubborn process PID=%s on port %d", pid, port)
					_ = exec.Command("kill", "-9", pid).Run()
				}
			}
		}
	}

	time.Sleep(500 * time.Millisecond)
}
