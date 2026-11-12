package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/index"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"2.0.0-rc1", "v2.0.0", true},
		{"v2.0.0", "v2.0.0-rc1", false},
		{"v2.0.0", "v2.0.0", false},
		{"2.0.0", "v2.0.1", true},
		{"v2.0.0-rc1", "v1.1.21", false},
		{"v2.0.0-rc1", "v2.0.0-rc2", false},
		{"v2.0.0-rc1+b6f25e7", "v2.0.0", true},
		{"v2.0.0+b6f25e7", "v2.0.1", true},
		{"dev", "v9.0.0", false},
	} {
		assert.Equal(t, c.want, Less(c.a, c.b), "%s < %s", c.a, c.b)
	}
}

// makeRelease writes a .tar.gz holding a fake rag-code-mcp that prints version,
// and its checksums.txt, into dir.
func makeRelease(t *testing.T, dir, version string) {
	t.Helper()
	script := []byte("#!/bin/sh\necho rag-code-mcp " + version + "\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"rag-code-mcp": script, "README.md": []byte("x")} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	require.NoError(t, os.WriteFile(filepath.Join(dir, asset), buf.Bytes(), 0o644))
	sum := sha256.Sum256(buf.Bytes())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.txt"),
		fmt.Appendf(nil, "%s  %s\n", hex.EncodeToString(sum[:]), asset), 0o644))
}

func installed(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "rag-code-mcp")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o755))
	return target
}

func skipWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake release binary is a shell script")
	}
}

func TestInstallFromDirectory(t *testing.T) {
	skipWindows(t)
	src, target := t.TempDir(), installed(t)
	makeRelease(t, src, "v2.1.0")
	v, err := Install(context.Background(), src, target, "v2.0.0-rc1+b6f25e7")
	require.NoError(t, err)
	assert.Equal(t, "v2.1.0", v)
	body, _ := os.ReadFile(target)
	assert.Contains(t, string(body), "rag-code-mcp v2.1.0")
}

func TestInstallRefusesOlderOrSame(t *testing.T) {
	skipWindows(t)
	src, target := t.TempDir(), installed(t)
	makeRelease(t, src, "v2.0.0")
	for _, cur := range []string{"v2.0.0", "v2.1.0", "dev"} {
		v, err := Install(context.Background(), src, target, cur)
		require.NoError(t, err)
		assert.Empty(t, v, cur)
	}
	body, _ := os.ReadFile(target)
	assert.Equal(t, "old", string(body))
}

func TestInstallRejectsBadChecksum(t *testing.T) {
	skipWindows(t)
	src, target := t.TempDir(), installed(t)
	makeRelease(t, src, "v2.1.0")
	require.NoError(t, os.WriteFile(filepath.Join(src, "checksums.txt"),
		[]byte("0000  "+AssetName(runtime.GOOS, runtime.GOARCH)+"\n"), 0o644))
	_, err := Install(context.Background(), src, target, "v2.0.0")
	require.ErrorContains(t, err, "does not match")
	require.NoError(t, os.WriteFile(filepath.Join(src, "checksums.txt"), []byte("abc  other.tar.gz\n"), 0o644))
	_, err = Install(context.Background(), src, target, "v2.0.0")
	require.ErrorContains(t, err, "no line for")
	body, _ := os.ReadFile(target)
	assert.Equal(t, "old", string(body))
}

func TestInstallFromGitHubStyleRelease(t *testing.T) {
	skipWindows(t)
	files := t.TempDir()
	makeRelease(t, files, "v2.1.0")
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	tag := "v2.1.0"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":"%s/%s"},{"name":"checksums.txt","browser_download_url":"%s/checksums.txt"}]}`,
				tag, asset, srv.URL, asset, srv.URL)
			return
		}
		http.ServeFile(w, r, filepath.Join(files, filepath.Base(r.URL.Path)))
	}))
	defer srv.Close()

	target := installed(t)
	v, err := Install(context.Background(), srv.URL+"/latest", target, "v2.0.0")
	require.NoError(t, err)
	assert.Equal(t, "v2.1.0", v)

	// An older tag is not downloaded at all.
	tag = "v1.1.21"
	target = installed(t)
	v, err = Install(context.Background(), srv.URL+"/latest", target, "v2.0.0")
	require.NoError(t, err)
	assert.Empty(t, v)
}

func TestRunSkipsWhileLocked(t *testing.T) {
	skipWindows(t)
	home := t.TempDir()
	t.Setenv("RAGCODE_HOME", home)
	src, target := t.TempDir(), installed(t)
	makeRelease(t, src, "v2.1.0")
	require.NoError(t, os.MkdirAll(filepath.Join(home, "cache"), 0o755))
	unlock, err := index.LockFile(filepath.Join(home, "cache", "update.lock"))
	require.NoError(t, err)
	v, err := Run(context.Background(), src, target, "v2.0.0")
	require.NoError(t, err)
	assert.Empty(t, v, "another process is updating")
	unlock()
	v, err = Run(context.Background(), src, target, "v2.0.0")
	require.NoError(t, err)
	assert.Equal(t, "v2.1.0", v)
}

// The Windows archive is a zip; extract finds the .exe at its root.
func TestExtractZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"README.md": "x", "rag-code-mcp.exe": "MZbinary"} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte(body))
	}
	require.NoError(t, zw.Close())
	got, err := extract(buf.Bytes(), "rag-code-mcp_windows_amd64.zip", "rag-code-mcp.exe")
	require.NoError(t, err)
	assert.Equal(t, "MZbinary", string(got))
	_, err = extract(buf.Bytes(), "rag-code-mcp_windows_amd64.zip", "missing.exe")
	require.ErrorContains(t, err, "not found")
}

// On Windows the running binary is renamed to .old before the new one moves in.
func TestReplaceKeepsOldOnWindowsOnly(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "new"), filepath.Join(dir, "rag-code-mcp")
	require.NoError(t, os.WriteFile(src, []byte("new"), 0o755))
	require.NoError(t, os.WriteFile(dst, []byte("old"), 0o755))
	require.NoError(t, Replace(src, dst))
	body, _ := os.ReadFile(dst)
	assert.Equal(t, "new", string(body))
	_, err := os.Stat(dst + ".old")
	assert.Equal(t, runtime.GOOS == "windows", err == nil)
}

// Once the remembered binary is replaced (renamed over, as Install does),
// Replaced is true; a missing path clears the record.
func TestReplaced(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "rag-code-mcp")
	require.NoError(t, os.WriteFile(bin, []byte("old"), 0o755))
	Remember(bin)
	assert.False(t, Replaced())
	src := filepath.Join(dir, "new")
	require.NoError(t, os.WriteFile(src, []byte("new"), 0o755))
	require.NoError(t, Replace(src, bin))
	assert.True(t, Replaced())
	Remember(filepath.Join(dir, "missing"))
	assert.False(t, Replaced())
}

// A hyphen in build metadata does not make a stable version a pre-release.
func TestBuildMetadataHyphenIsNotPrerelease(t *testing.T) {
	skipWindows(t)
	source, target := t.TempDir(), installed(t)
	makeRelease(t, source, "v2.0.0")
	v, err := Install(context.Background(), source, target, "v2.0.0+build-123")
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Fatalf("installed %s over an equal stable version: build metadata was treated as a prerelease", v)
	}
}
