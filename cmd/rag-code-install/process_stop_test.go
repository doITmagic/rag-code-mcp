package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Reinstalling while an IDE still runs the installed binary as its stdio
// adapter must replace the binary, not fail with "text file busy".
func TestCopyFileReplacesRunningBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a running executable cannot be replaced on Windows")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not found")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "rag-code-mcp")
	if err := copyFile(sleep, dst); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(dst, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	src := filepath.Join(dir, "new")
	if err := os.WriteFile(src, []byte("new binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("binary in use not replaced: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "new binary" {
		t.Fatalf("dst = %q", got)
	}
}

// The renamed-in binary must be executable at once: an IDE starting its
// adapter before the installer's chmod would otherwise fail with EACCES.
func TestCopyFileInstallsExecutableBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no exec bit on Windows")
	}
	src, err := exec.LookPath("true")
	if err != nil {
		t.Skip("true not found")
	}
	dst := filepath.Join(t.TempDir(), "rag-code-mcp")
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(dst).Run(); err != nil {
		t.Fatalf("installed binary not runnable right after copy: %v", err)
	}
}

func TestWaitForFileRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag-code-mcp.exe")
	if err := os.WriteFile(path, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !waitForFileRelease(path, time.Second) {
		t.Fatal("writable executable was reported as locked")
	}
	if !waitForFileRelease(path+".missing", time.Second) {
		t.Fatal("missing executable should be considered released")
	}
}
