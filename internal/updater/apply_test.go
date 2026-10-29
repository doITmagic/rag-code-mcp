package updater

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// ApplyUpdate must return after handing off to the installer, so that
// rag_apply_update can still answer its client; the process exits afterwards.
func TestApplyUpdateReturnsBeforeExiting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub installer is a shell script")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "installer-ran")
	archive := filepath.Join(dir, "release.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	script := []byte("#!/bin/sh\ntouch '" + marker + "'\n")
	if err := tw.WriteHeader(&tar.Header{Name: "rag-code-install", Mode: 0o755, Size: int64(len(script))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(script); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	exited := make(chan int, 1)
	exit = func(code int) { exited <- code }
	t.Cleanup(func() { exit = os.Exit })

	if err := ApplyUpdate(archive); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exited:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("process never exits after the handoff")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("installer did not run")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
