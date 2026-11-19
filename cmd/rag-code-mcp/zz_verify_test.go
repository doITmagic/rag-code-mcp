package main

import (
	"bytes"
	"context"
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

// fakeDocker puts a docker on PATH that keeps a ragcode-ollama container and
// volume as files next to it: "container" (listed by ps and inspect), "image"
// and "mounts" (what inspect reports), "volume" (listed by volume ls). Every
// command fails while "down" exists; "vanish" makes inspect find no container
// although ps listed one; "rmfail" and "volfail" make `rm -f` and `volume rm`
// fail; "rmhang" makes `rm -f` create "rmstarted" and wait. The real docker is never run.
func fakeDocker(t *testing.T) (dir string, calls func() []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	dir = t.TempDir()
	script := `#!/bin/sh
D=` + dir + `
echo "$*" >> $D/docker.log
[ -f $D/down ] && exit 1
case "$1 $2 $3" in
"ps -a "*) [ -f $D/container ] && echo ragcode-ollama; exit 0;;
"container inspect "*)
  if [ ! -f $D/container ] || [ -f $D/vanish ]; then rm -f $D/container; echo "Error: No such container: ragcode-ollama"; exit 1; fi
  echo "$(cat $D/image)|$(cat $D/mounts)"; exit 0;;
"rm -f ragcode-ollama") if [ -f $D/rmhang ]; then touch $D/rmstarted; exec sleep 30; fi; if [ -f $D/rmfail ]; then echo "cannot remove"; exit 1; fi; rm -f $D/container; exit 0;;
"volume ls "*) [ -f $D/volume ] && echo ragcode-ollama; exit 0;;
"volume rm ragcode-ollama") if [ -f $D/volfail ]; then echo "volume is in use"; exit 1; fi; rm -f $D/volume; exit 0;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, func() []string {
		data, _ := os.ReadFile(filepath.Join(dir, "docker.log"))
		var out []string
		for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			switch {
			case l == "":
			case strings.HasPrefix(l, "ps "):
				out = append(out, "ps")
			case strings.HasPrefix(l, "container inspect "):
				out = append(out, "inspect")
			case strings.HasPrefix(l, "volume ls "):
				out = append(out, "volume ls")
			default:
				out = append(out, l)
			}
		}
		return out
	}
}

// control creates or removes a control file of fakeDocker.
func control(t *testing.T, dir, name string, on bool, content ...string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if !on {
		_ = os.Remove(p)
		return
	}
	require.NoError(t, os.WriteFile(p, []byte(strings.Join(content, "")), 0o644))
}

// ollamaContainer makes the fake docker list a v2 container with its volume.
func ollamaContainer(t *testing.T, dir string) {
	control(t, dir, "container", true)
	control(t, dir, "image", true, "ollama/ollama")
	control(t, dir, "mounts", true, "volume=ragcode-ollama=/root/.ollama;")
	control(t, dir, "volume", true)
}

// removeOllamaLogged runs removeOllama and returns what it logged.
func removeOllamaLogged(ctx context.Context, home string) string {
	var buf bytes.Buffer
	removeOllama(ctx, home, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return buf.String()
}

// The server removes the container of the Ollama versions once per home:
// an auto-update replaces only the binary and never runs the installer.
func TestVerifyRemoveOllamaOnce(t *testing.T) {
	dir, calls := fakeDocker(t)
	home := t.TempDir()
	marker := filepath.Join(home, "cache", "ollama-removed")
	pending := filepath.Join(home, "cache", "ollama-volume-pending")
	ctx := context.Background()

	// A daemon that does not answer: nothing is concluded, the next start asks again.
	ollamaContainer(t, dir)
	control(t, dir, "down", true)
	removeOllamaLogged(ctx, home)
	assert.Equal(t, []string{"ps"}, calls())
	assert.NoFileExists(t, marker)
	assert.NoFileExists(t, pending)

	control(t, dir, "down", false)
	logged := removeOllamaLogged(ctx, home)
	assert.Equal(t, []string{"ps", "ps", "inspect", "rm -f ragcode-ollama", "volume rm ragcode-ollama"}, calls())
	assert.FileExists(t, marker)
	assert.NoFileExists(t, pending, "the volume is gone")
	assert.Contains(t, logged, "removed the ragcode-ollama container;")
	assert.Contains(t, logged, "removed the ragcode-ollama volume")

	removeOllamaLogged(ctx, home)
	assert.Len(t, calls(), 5, "once per home")

	// No container: one question, then never again; an orphan volume is not proven to be ours.
	control(t, dir, "volume", true)
	other := t.TempDir()
	removeOllamaLogged(ctx, other)
	removeOllamaLogged(ctx, other)
	assert.Equal(t, "ps", calls()[5])
	assert.Len(t, calls(), 6)
	assert.FileExists(t, filepath.Join(dir, "volume"))
	assert.FileExists(t, filepath.Join(other, "cache", "ollama-removed"))

	// No docker at all: asked at every start.
	t.Setenv("PATH", t.TempDir())
	none := t.TempDir()
	removeOllamaLogged(ctx, none)
	assert.NoFileExists(t, filepath.Join(none, "cache", "ollama-removed"))
}

// A volume that cannot be removed leaves no marker: the next start retries
// the volume alone, on the proof of the pending file.
func TestVerifyRemoveOllamaVolumeRetried(t *testing.T) {
	dir, calls := fakeDocker(t)
	home := t.TempDir()
	marker := filepath.Join(home, "cache", "ollama-removed")
	pending := filepath.Join(home, "cache", "ollama-volume-pending")
	ollamaContainer(t, dir)
	control(t, dir, "volfail", true)

	logged := removeOllamaLogged(context.Background(), home)
	assert.Contains(t, logged, "level=WARN")
	assert.Contains(t, logged, "volume is in use")
	assert.NoFileExists(t, marker)
	assert.FileExists(t, pending)

	control(t, dir, "volfail", false)
	before := len(calls())
	logged = removeOllamaLogged(context.Background(), home)
	assert.Equal(t, []string{"ps", "volume rm ragcode-ollama"}, calls()[before:], "only the volume is retried")
	assert.Contains(t, logged, "removed the ragcode-ollama volume")
	assert.FileExists(t, marker)
	assert.NoFileExists(t, pending)
	assert.NoFileExists(t, filepath.Join(dir, "volume"))
}

// The process died after the container was removed and before the volume was:
// the pending file written before rm -f drives the retry.
func TestVerifyRemoveOllamaDiedBeforeVolume(t *testing.T) {
	dir, calls := fakeDocker(t)
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "cache"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "cache", "ollama-volume-pending"), nil, 0o644))
	control(t, dir, "volume", true)
	removeOllamaLogged(context.Background(), home)
	assert.Equal(t, []string{"ps", "volume rm ragcode-ollama"}, calls())
	assert.FileExists(t, filepath.Join(home, "cache", "ollama-removed"))
	assert.NoFileExists(t, filepath.Join(home, "cache", "ollama-volume-pending"))
}

// The pending file exists before the container goes, so a refused rm -f
// leaves it and no marker, and the next start identifies the container again.
func TestVerifyRemoveOllamaPendingBeforeRm(t *testing.T) {
	dir, _ := fakeDocker(t)
	home := t.TempDir()
	ollamaContainer(t, dir)
	control(t, dir, "rmfail", true)
	logged := removeOllamaLogged(context.Background(), home)
	assert.Contains(t, logged, "level=WARN")
	assert.Contains(t, logged, "cannot remove")
	assert.FileExists(t, filepath.Join(home, "cache", "ollama-volume-pending"))
	assert.NoFileExists(t, filepath.Join(home, "cache", "ollama-removed"))
}

// A container named ragcode-ollama that is not RagCode's stays, with one
// warning, and the marker keeps the server from asking again.
func TestVerifyRemoveOllamaForeign(t *testing.T) {
	dir, calls := fakeDocker(t)
	home := t.TempDir()
	control(t, dir, "container", true)
	control(t, dir, "image", true, "nginx")
	control(t, dir, "mounts", true, "volume=ragcode-ollama=/data;")
	control(t, dir, "volume", true)
	logged := removeOllamaLogged(context.Background(), home)
	assert.Equal(t, 1, strings.Count(logged, "level=WARN"))
	assert.Equal(t, []string{"ps", "inspect"}, calls())
	assert.FileExists(t, filepath.Join(home, "cache", "ollama-removed"))
	assert.NoFileExists(t, filepath.Join(home, "cache", "ollama-volume-pending"))
	assert.FileExists(t, filepath.Join(dir, "container"))
	assert.FileExists(t, filepath.Join(dir, "volume"))
	removeOllamaLogged(context.Background(), home)
	assert.Len(t, calls(), 2)
}

// The container vanishes between ps and inspect: it is absent, not an error.
func TestVerifyRemoveOllamaVanished(t *testing.T) {
	dir, calls := fakeDocker(t)
	home := t.TempDir()
	ollamaContainer(t, dir)
	control(t, dir, "vanish", true)
	logged := removeOllamaLogged(context.Background(), home)
	assert.NotContains(t, logged, "level=WARN")
	assert.Equal(t, []string{"ps", "inspect"}, calls())
	assert.FileExists(t, filepath.Join(home, "cache", "ollama-removed"))
}

// A cancelled context (the server stopping) is not worth a warning and writes no marker.
func TestVerifyRemoveOllamaCancelled(t *testing.T) {
	dir, _ := fakeDocker(t)
	home := t.TempDir()
	ollamaContainer(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logged := removeOllamaLogged(ctx, home)
	assert.NotContains(t, logged, "level=WARN")
	assert.NoFileExists(t, filepath.Join(home, "cache", "ollama-removed"))
}

// The server stops after docker was asked to remove the container: the removal
// that fails for that reason is not a warning either, and there is no marker;
// the pending file stays, so the next start finishes the work.
func TestVerifyRemoveOllamaCancelledDuringRm(t *testing.T) {
	dir, _ := fakeDocker(t)
	home := t.TempDir()
	ollamaContainer(t, dir)
	control(t, dir, "rmhang", true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(filepath.Join(dir, "rmstarted")); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done(): // the test ended: no poll left behind
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	logged := removeOllamaLogged(ctx, home)
	require.Error(t, ctx.Err(), "the context was cancelled while docker rm ran")
	assert.NotContains(t, logged, "level=WARN")
	assert.NoFileExists(t, filepath.Join(home, "cache", "ollama-removed"))
	assert.FileExists(t, filepath.Join(home, "cache", "ollama-volume-pending"))
}
