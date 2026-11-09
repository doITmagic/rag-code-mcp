//go:build !windows

package uninstall

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// StopProcess stops the server binary, not every process whose command line
// merely mentions its path (a shell, tail -f, Claude Desktop's wrapper).
func TestStopProcessOnlyStopsTheBinary(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	require.NoError(t, err)
	src, err := os.ReadFile(sleep)
	require.NoError(t, err)
	bin := filepath.Join(t.TempDir(), "rag-code-mcp")
	require.NoError(t, os.WriteFile(bin, src, 0o755))

	server := exec.Command(bin, "30")
	require.NoError(t, server.Start())
	bystander := exec.Command("sh", "-c", "sleep 30; : "+bin)
	require.NoError(t, bystander.Start())
	t.Cleanup(func() { _ = bystander.Process.Kill(); _ = server.Process.Kill() })
	exited := func(c *exec.Cmd) chan error { ch := make(chan error, 1); go func() { ch <- c.Wait() }(); return ch }
	serverDone, bystanderDone := exited(server), exited(bystander)
	time.Sleep(100 * time.Millisecond)

	StopProcess(context.Background(), bin)

	select {
	case <-serverDone:
	case <-time.After(10 * time.Second):
		t.Fatal("server still running")
	}
	select {
	case err := <-bystanderDone:
		assert.Fail(t, "bystander was stopped", "%v", err)
	case <-time.After(300 * time.Millisecond):
	}
}
