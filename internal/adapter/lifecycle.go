package adapter

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/daemon"
)

// IsDaemonRunning checks if a healthy daemon is reachable.
// Returns (true, version) if the daemon is alive and responding.
// Cleans up stale PID/socket files if daemon is dead.
func IsDaemonRunning(pidPath, socketPath string) (bool, string) {
	info, err := daemon.ReadPID(pidPath)
	if err != nil {
		return false, ""
	}

	if !daemon.IsProcessAlive(info.PID) {
		CleanupStaleFiles(pidPath, socketPath)
		return false, ""
	}

	// Try connecting to socket
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		CleanupStaleFiles(pidPath, socketPath)
		return false, ""
	}
	conn.Close()

	return true, info.Version
}

// CleanupStaleFiles removes leftover PID and socket files from a dead daemon.
func CleanupStaleFiles(pidPath, socketPath string) {
	os.Remove(pidPath)
	os.Remove(socketPath)
}

// StartDaemon launches the daemon as a detached background process.
// Waits up to 10 seconds for the daemon to become healthy on the socket.
func StartDaemon(binaryPath, socketPath string) error {
	cmd := exec.Command(binaryPath, "--daemon")
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

	// Poll socket until daemon is ready (max 10s, 500ms intervals)
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}

	for i := 0; i < 20; i++ {
		resp, err := client.Get("http://daemon/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("daemon did not become ready within 10s (socket: %s)", socketPath)
}

// StopDaemon sends SIGTERM to the daemon process identified by the PID file.
func StopDaemon(pidPath string) error {
	info, err := daemon.ReadPID(pidPath)
	if err != nil {
		return fmt.Errorf("read PID file: %w", err)
	}

	process, err := os.FindProcess(info.PID)
	if err != nil {
		return fmt.Errorf("find process %d: %w", info.PID, err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to %d: %w", info.PID, err)
	}

	// Wait for process to die (max 5s)
	for i := 0; i < 10; i++ {
		if !daemon.IsProcessAlive(info.PID) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Force kill if still alive
	return process.Signal(syscall.SIGKILL)
}
