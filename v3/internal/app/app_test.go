package app

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSmoke(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.WatchEnabled = false
	a, err := New(Options{Config: cfg, Version: "v3.0.0-test", Store: store.NewMemory(), Embedder: &embed.Fake{}})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() { a.Close(ctx) })

	ct, st := mcp.NewInMemoryTransports()
	_, err = a.Server.Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer cs.Close()

	ir := cs.InitializeResult()
	require.NotNil(t, ir)
	assert.Equal(t, "ragcode", ir.ServerInfo.Name)
	assert.Equal(t, "v3.0.0-test", ir.ServerInfo.Version)

	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	want := []string{"rag_search", "rag_find_usages", "rag_call_hierarchy", "rag_list_package_exports",
		"rag_read_file_context", "rag_index_workspace", "rag_server_info"}
	assert.ElementsMatch(t, want, names)
}

func TestNewDefaultsDoNotDial(t *testing.T) {
	cfg := config.Default()
	cfg.OllamaBaseURL, cfg.QdrantURL = "http://127.0.0.1:1", "http://127.0.0.1:1"
	a, err := New(Options{Config: cfg})
	require.NoError(t, err)
	require.NotNil(t, a.Server)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.Close(ctx)
}

// A stop by signal cancels Run's context; the SDK logs that as an ERROR,
// which made every normal shutdown look like a failure in ragcode.log.
func TestShutdownLogsNoError(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.WatchEnabled = false
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a, err := New(Options{Config: cfg, Version: "v3", Log: log, Store: store.NewMemory(), Embedder: &embed.Fake{}})
	require.NoError(t, err)
	t.Cleanup(func() { a.Close(context.Background()) })

	ct, st := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Server.Run(ctx, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	defer cs.Close()
	cancel()
	<-done
	assert.Contains(t, buf.String(), "server run cancelled")
	assert.NotContains(t, buf.String(), "level=ERROR")
}
