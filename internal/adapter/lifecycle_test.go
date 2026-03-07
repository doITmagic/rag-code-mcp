package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDaemonRunning_NoPIDFile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	sockPath := filepath.Join(dir, "daemon.sock")

	running, version := IsDaemonRunning(pidPath, sockPath)
	assert.False(t, running)
	assert.Empty(t, version)
}

func TestIsDaemonRunning_StalePID(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	sockPath := filepath.Join(dir, "daemon.sock")

	// Write a PID that doesn't exist
	err := daemon.WritePID(pidPath, 999999999, "1.0.0")
	require.NoError(t, err)

	running, _ := IsDaemonRunning(pidPath, sockPath)
	assert.False(t, running)

	// PID file should be cleaned up
	_, err = os.Stat(pidPath)
	assert.True(t, os.IsNotExist(err), "stale PID file should be removed")
}

func TestIsDaemonRunning_ProcessAliveButNoSocket(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	sockPath := filepath.Join(dir, "daemon.sock")

	// Write current PID (alive) but no socket file
	err := daemon.WritePID(pidPath, os.Getpid(), "1.0.0")
	require.NoError(t, err)

	running, _ := IsDaemonRunning(pidPath, sockPath)
	assert.False(t, running, "should be false without a reachable socket")

	// Both files should be cleaned up
	_, err = os.Stat(pidPath)
	assert.True(t, os.IsNotExist(err))
}

func TestCleanupStaleFiles(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	sockPath := filepath.Join(dir, "daemon.sock")

	require.NoError(t, os.WriteFile(pidPath, []byte("stale"), 0644))
	require.NoError(t, os.WriteFile(sockPath, []byte("stale"), 0644))

	CleanupStaleFiles(pidPath, sockPath)

	_, err1 := os.Stat(pidPath)
	_, err2 := os.Stat(sockPath)
	assert.True(t, os.IsNotExist(err1))
	assert.True(t, os.IsNotExist(err2))
}

func TestCleanupStaleFiles_NoFiles(t *testing.T) {
	// Should not panic on non-existent files
	CleanupStaleFiles("/nonexistent/pid", "/nonexistent/sock")
}
