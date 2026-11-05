package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionLine(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	for _, v := range []string{"dev", "v3.1.0"} {
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
