package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAndReadPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	err := WritePID(path, os.Getpid(), "2.1.54")
	require.NoError(t, err)

	info, err := ReadPID(path)
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), info.PID)
	assert.Equal(t, "2.1.54", info.Version)
	assert.NotEmpty(t, info.StartedAt)
}

func TestReadPID_NotExists(t *testing.T) {
	_, err := ReadPID("/nonexistent/path/daemon.pid")
	assert.Error(t, err)
}

func TestReadPID_InvalidContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	err := os.WriteFile(path, []byte("GARBAGE=data\n"), 0644)
	require.NoError(t, err)

	_, err = ReadPID(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no PID found")
}

func TestRemovePID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	err := WritePID(path, 12345, "1.0.0")
	require.NoError(t, err)

	err = RemovePID(path)
	assert.NoError(t, err)

	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestRemovePID_NotExists(t *testing.T) {
	err := RemovePID("/nonexistent/path/daemon.pid")
	assert.NoError(t, err) // should not error on missing file
}

func TestIsProcessAlive_CurrentProcess(t *testing.T) {
	assert.True(t, IsProcessAlive(os.Getpid()))
}

func TestIsProcessAlive_NonexistentPID(t *testing.T) {
	assert.False(t, IsProcessAlive(999999999))
}

func TestWritePID_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "daemon.pid")

	// Should fail — parent dir doesn't exist
	err := WritePID(path, 1, "1.0.0")
	assert.Error(t, err) // os.WriteFile doesn't create parent dirs
}
