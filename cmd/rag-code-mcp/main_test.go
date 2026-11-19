package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionLine(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	for _, v := range []string{"dev", "v2.1.0"} {
		Version = v
		assert.Equal(t, "rag-code-mcp "+v, versionLine())
	}
}

func TestHealth(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	ts := httptest.NewServer(httpHandler(srv, nil))
	defer ts.Close()
	res, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var body struct {
		Version string `json:"version"`
		PID     int    `json:"pid"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, Version, body.Version)
	assert.Equal(t, os.Getpid(), body.PID)
}

// F4 (WSL, Z13): the end of stdin closes the engine at once; the SDK waits for
// in-flight calls, and one stuck in a watcher walk kept the process alive.
func TestStdinEOFRunsHook(t *testing.T) {
	n := 0
	r := onEOF{io.NopCloser(strings.NewReader("x")), sync.OnceFunc(func() { n++ })}
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, "x", string(b))
	_, _ = r.Read(make([]byte, 1))
	assert.Equal(t, 1, n)
}

// run starts the warm-up when embed_warm_start is set, and only then. The
// runtime and model are files that are not there, so nothing is loaded or
// downloaded: the warm-up ends at once and says so in the debug log.
func TestRunWarmStart(t *testing.T) {
	for _, warm := range []bool{true, false} {
		t.Run(map[bool]string{true: "on", false: "off"}[warm], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("RAGCODE_HOME", home)
			cfgPath := filepath.Join(home, "config.yaml")
			require.NoError(t, os.WriteFile(cfgPath, []byte("log_level: debug\nauto_update: false\nwatch_enabled: false\nembed_warm_start: "+map[bool]string{true: "true", false: "false"}[warm]+
				"\nembed_model: "+filepath.Join(home, "none", "m.gguf")+"\nllama_server: "+filepath.Join(home, "none", "llama-server")+"\n"), 0o644))
			ctx, cancel := context.WithCancel(context.Background())
			errc := make(chan error, 1)
			go func() { errc <- run(ctx, cfgPath, "127.0.0.1:0", "") }()
			logPath := filepath.Join(home, "logs", "ragcode.log")
			logged := func() bool {
				data, _ := os.ReadFile(logPath)
				return strings.Contains(string(data), "warm-up finished")
			}
			if warm {
				require.Eventually(t, logged, 5*time.Second, 20*time.Millisecond)
			} else {
				time.Sleep(300 * time.Millisecond)
				assert.False(t, logged())
			}
			cancel()
			require.NoError(t, <-errc)
			assert.NoDirExists(t, filepath.Join(home, "llama"), "nothing downloaded")
		})
	}
}
