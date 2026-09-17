package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
