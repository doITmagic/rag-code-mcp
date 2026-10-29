package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ApplyUpdate must return after handing off to the installer, so that
// rag_apply_update can still answer its client; the process exits afterwards.
func TestApplyUpdateReturnsBeforeExiting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub installer is a shell script")
	}
	marker, archive := stubInstallerArchive(t, 0)

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

// A failed installer must leave the daemon running: nothing was updated, and
// exiting anyway left the client with no server at all.
func TestApplyUpdateKeepsRunningWhenInstallerFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub installer is a shell script")
	}
	marker, archive := stubInstallerArchive(t, 1)

	exited := make(chan int, 1)
	exit = func(code int) { exited <- code }
	t.Cleanup(func() { exit = os.Exit })

	if err := ApplyUpdate(archive); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exited:
		t.Fatalf("process exited (code %d) although the installer failed", code)
	case <-time.After(3 * time.Second):
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("installer did not run")
	}
}

// stubInstallerArchive builds a release archive whose installer touches the
// returned marker and exits with code.
func stubInstallerArchive(t *testing.T, code int) (marker, archive string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "installer-ran")
	archive = filepath.Join(dir, "release.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	script := []byte(fmt.Sprintf("#!/bin/sh\ntouch '%s'\nexit %d\n", marker, code))
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
	return marker, archive
}

// Auto-update must leave a binary outside ~/.ragcode/bin alone: the installer
// only ever updates the installed one, so downloading and handing off would
// just stop this daemon (and repoint the IDEs) on every start.
func TestDownloadVerifyAndApplySkipsBinaryOutsideInstallDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	exit = func(int) { t.Error("process exited") }
	t.Cleanup(func() { exit = os.Exit })

	err := DownloadVerifyAndApply(context.Background(), &UpdateInfo{
		LatestVersion: "9.9.9",
		AssetURL:      srv.URL + "/rag-code-mcp_linux_amd64.tar.gz",
		ChecksumURL:   srv.URL + "/checksums.txt",
	})
	if err == nil || !strings.Contains(err.Error(), "not the installed") {
		t.Fatalf("err = %v, want the binary reported as not installed", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("downloaded the release %d time(s) for a binary the installer will not update", n)
	}
}
