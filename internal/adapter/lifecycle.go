package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/daemon"
)

// IsDaemonRunning checks if a healthy daemon is reachable on the given TCP port.
// Returns (true, version) if the daemon is alive and responding.
func IsDaemonRunning(port int) (bool, string) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, ""
	}

	var health daemon.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return false, ""
	}

	return true, health.Version
}

// CleanupStaleFiles is kept for compatibility but does nothing since we use TCP ports.
func CleanupStaleFiles(pidPath, socketPath string) {
}

// StartDaemon launches the daemon as a detached background process.
// extraArgs are additional CLI flags (e.g. --config, --http-port) forwarded to the daemon.
// Waits up to 10 seconds for the daemon to become healthy on the port.
func StartDaemon(binaryPath string, port int, extraArgs ...string) error {
	args := append([]string{"--daemon"}, extraArgs...)
	cmd := exec.Command(binaryPath, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Don't wait for the daemon process — it runs independently
	go func() { _ = cmd.Wait() }()

	return waitForDaemon(port)
}

// waitForDaemon polls the daemon health endpoint until ready (max 10s).
func waitForDaemon(port int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := &http.Client{Timeout: 2 * time.Second}

	for i := 0; i < 20; i++ {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("daemon did not become ready within 10s (port: %d)", port)
}

// StopDaemon sends a termination signal to the daemon process via its TCP health endpoint PID.
func StopDaemon(port int) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return nil // already dead or unreachable
	}
	defer resp.Body.Close()

	var health daemon.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return fmt.Errorf("failed to decode daemon health: %w", err)
	}

	process, err := os.FindProcess(health.PID)
	if err != nil {
		return fmt.Errorf("find process %d: %w", health.PID, err)
	}

	if runtime.GOOS == "windows" {
		return process.Kill()
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to %d: %w", health.PID, err)
	}

	// Wait for process to die (max 5s)
	for i := 0; i < 10; i++ {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Force kill if still alive
	return process.Signal(syscall.SIGKILL)
}
