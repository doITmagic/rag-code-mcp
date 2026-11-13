//go:build windows

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

// TestHelperSleep is the process the test below starts. Its image name is
// unique to this test, so even the old name-based StopProcess cannot reach a
// real rag-code-mcp.exe on the machine.
func TestHelperSleep(t *testing.T) {
	if os.Getenv("RAGCODE_TEST_SLEEP") == "" {
		t.Skip("helper process")
	}
	time.Sleep(30 * time.Second)
}

// StopProcess stops the binary's own server, never a same-named exe started
// from another folder (the user's IDE server, another install).
func TestStopProcessOnlyStopsThisPath(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	src, err := os.ReadFile(self)
	require.NoError(t, err)
	start := func() (string, chan error) {
		bin := filepath.Join(t.TempDir(), "rag-code-stoptest.exe")
		require.NoError(t, os.WriteFile(bin, src, 0o755))
		c := exec.Command(bin, "-test.run=^TestHelperSleep$")
		c.Env = append(os.Environ(), "RAGCODE_TEST_SLEEP=1")
		require.NoError(t, c.Start())
		ch := make(chan error, 1)
		go func() { ch <- c.Wait() }()
		t.Cleanup(func() { _ = c.Process.Kill(); <-ch })
		return bin, ch
	}
	bin, serverDone := start()
	_, otherDone := start()
	time.Sleep(300 * time.Millisecond)

	StopProcess(context.Background(), bin)

	select {
	case err := <-serverDone:
		serverDone <- err // for the cleanup
	case <-time.After(10 * time.Second):
		t.Fatal("server still running")
	}
	select {
	case err := <-otherDone:
		otherDone <- err
		assert.Fail(t, "same-named exe from another folder was stopped", "%v", err)
	case <-time.After(500 * time.Millisecond):
	}
}
