package main

import (
	"context"
	"net"
	"net/http"
	"os"
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
	require.NoError(t, writeFile(dir+"/config.yaml", "ollama_base_url: ':::'\n"))
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
