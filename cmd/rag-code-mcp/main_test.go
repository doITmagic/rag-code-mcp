package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

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
