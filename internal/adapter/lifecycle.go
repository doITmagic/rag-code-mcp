package adapter

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
// Uses a lock file to prevent multiple concurrent adapters from racing
// to start multiple daemons simultaneously.
// extraArgs are additional CLI flags (e.g. --config, --http-port) forwarded to the daemon.
// Waits up to 10 seconds for the daemon to become healthy on the socket.
func StartDaemon(binaryPath, socketPath string, extraArgs ...string) error {
	// Acquire lock to prevent concurrent duplicate starts
	lockPath := filepath.Join(filepath.Dir(socketPath), "daemon.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			// Another adapter is starting the daemon — wait for it
			return waitForDaemon(socketPath)
		}
		return fmt.Errorf("failed to acquire daemon lock: %w", err)
	}
	defer func() {
		lockFile.Close()
		os.Remove(lockPath)
	}()

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

	return waitForDaemon(socketPath)
}

// waitForDaemon polls the daemon health endpoint until ready (max 10s).
func waitForDaemon(socketPath string) error {
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

// StopDaemon sends a termination signal to the daemon process identified by the PID file.
// On Unix, sends SIGTERM first, then SIGKILL after 5s. On Windows, uses Kill().
func StopDaemon(pidPath string) error {
	info, err := daemon.ReadPID(pidPath)
	if err != nil {
		return fmt.Errorf("read PID file: %w", err)
	}

	process, err := os.FindProcess(info.PID)
	if err != nil {
		return fmt.Errorf("find process %d: %w", info.PID, err)
	}

	// Send graceful stop signal (SIGTERM on Unix, Kill on Windows)
	if runtime.GOOS == "windows" {
		return process.Kill()
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
