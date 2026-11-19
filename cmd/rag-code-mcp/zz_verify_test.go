package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/internal/app"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func verifyApp(t *testing.T) *app.App {
	t.Helper()
	t.Setenv("RAGCODE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.WatchEnabled = false
	a, err := app.New(app.Options{Config: cfg, Version: "v", Store: store.NewMemory(), Embedder: &embed.Fake{}})
	require.NoError(t, err)
	return a
}

// serveHTTP must fail fast on an unusable address and stop on ctx cancel.
func TestVerifyServeHTTP(t *testing.T) {
	a := verifyApp(t)
	defer a.Close(context.Background())

	errc := make(chan error, 1)
	go func() { errc <- serveHTTP(context.Background(), "300.1.1.1:1", a, nil) }()
	select {
	case err := <-errc:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("serveHTTP hung on a bad address")
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { errc <- serveHTTP(ctx, addr, a, nil) }()
	var res *http.Response
	for i := 0; i < 50; i++ {
		if res, err = http.Get("http://" + addr + "/health"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
	// MCP endpoint answers (405/400 for a bare GET without a session is fine; it must not 404).
	res, err = http.Get("http://" + addr + "/")
	require.NoError(t, err)
	res.Body.Close()
	assert.NotEqual(t, 404, res.StatusCode)
	cancel()
	select {
	case err := <-errc:
		require.NoError(t, err)
	case <-time.After(closeTimeout + time.Second):
		t.Fatal("serveHTTP did not stop after cancel")
	}
}

// run must surface a config error without touching the network.
func TestVerifyRunBadConfig(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	dir := t.TempDir()
	require.NoError(t, writeFile(dir+"/config.yaml", "embed_runtime: gpu\n"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := run(ctx, dir+"/config.yaml", "", "")
	require.Error(t, err)
}
func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o644) }

// A typo in -log-level is an error, not a silent fall back to info.
func TestVerifyRunBadLogLevelFlag(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	dir := t.TempDir()
	require.NoError(t, writeFile(dir+"/config.yaml", "log_level: info\n"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := run(ctx, dir+"/config.yaml", "", "debgu")
	require.ErrorContains(t, err, "log_level")
}

// fakeDocker puts a docker on PATH that logs its arguments; ps lists the
// ragcode-ollama container while the file "container" exists, and every
// command fails while the file "down" does. The real docker is never run.
func fakeDocker(t *testing.T) (dir string, calls func() []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	dir = t.TempDir()
	log := filepath.Join(dir, "docker.log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n[ -f " + filepath.Join(dir, "down") + " ] && exit 1\ncase \"$1\" in ps) [ -f " + filepath.Join(dir, "container") + " ] && echo ragcode-ollama;; esac\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", dir)
	return dir, func() []string {
		data, _ := os.ReadFile(log)
		return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(data)), " ", "_"))
	}
}

// The server removes the container of the Ollama versions once per home:
// an auto-update replaces only the binary and never runs the installer.
func TestVerifyRemoveOllamaOnce(t *testing.T) {
	dir, calls := fakeDocker(t)
	home := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	marker := filepath.Join(home, "cache", "ollama-removed")
	ps := "ps_-a_--filter_name=^ragcode-ollama$_--format_{{.Names}}"

	// A daemon that does not answer: nothing is concluded, the next start asks again.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "down"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "container"), nil, 0o644))
	removeOllama(context.Background(), home, log)
	assert.Equal(t, []string{ps}, calls())
	assert.NoFileExists(t, marker)

	require.NoError(t, os.Remove(filepath.Join(dir, "down")))
	removeOllama(context.Background(), home, log)
	assert.Equal(t, []string{ps, ps, "rm_-f_ragcode-ollama", "volume_rm_ragcode-ollama"}, calls())
	assert.FileExists(t, marker)

	removeOllama(context.Background(), home, log)
	assert.Len(t, calls(), 4, "once per home")

	// No container: one question, then never again. No docker at all: asked at every start.
	require.NoError(t, os.Remove(filepath.Join(dir, "container")))
	other := t.TempDir()
	removeOllama(context.Background(), other, log)
	removeOllama(context.Background(), other, log)
	assert.Len(t, calls(), 5)
	t.Setenv("PATH", t.TempDir())
	none := t.TempDir()
	removeOllama(context.Background(), none, log)
	assert.NoFileExists(t, filepath.Join(none, "cache", "ollama-removed"))
}
