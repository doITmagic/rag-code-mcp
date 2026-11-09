package app

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSmoke(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.WatchEnabled = false
	a, err := New(Options{Config: cfg, Version: "v2.0.0-test", Store: store.NewMemory(), Embedder: &embed.Fake{}})
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
	assert.Equal(t, "v2.0.0-test", ir.ServerInfo.Version)

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
	t.Setenv("RAGCODE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.OllamaBaseURL = "http://127.0.0.1:1"
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
	a, err := New(Options{Config: cfg, Version: "v2", Log: log, Store: store.NewMemory(), Embedder: &embed.Fake{}})
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

// Without a Store option the server indexes into one SQLite file per root
// under RAGCODE_HOME/index.
func TestAppDefaultStoreIsSQLite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RAGCODE_HOME", home)
	cfg := config.Default()
	cfg.WatchEnabled = false
	a, err := New(Options{Config: cfg, Embedder: &embed.Fake{}})
	require.NoError(t, err)
	t.Cleanup(func() { a.Close(context.Background()) })
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.md"), []byte("# Title\n\nSome text.\n"), 0o644))
	w, err := a.Engine.Workspace(root)
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running }, 10*time.Second, 20*time.Millisecond)
	assert.FileExists(t, filepath.Join(home, "index", store.CollectionName(root), "index.db"))
}

// Several clients share one log file, so each connection names its client,
// on the initialize handshake and on the 2026-07-28 per-request metadata.
func TestLogsConnectedClient(t *testing.T) {
	for _, proto := range []string{"2025-11-25", ""} {
		t.Run("protocol="+proto, func(t *testing.T) {
			t.Setenv("RAGCODE_HOME", t.TempDir())
			cfg := config.Default()
			cfg.WatchEnabled = false
			var buf syncBuf
			log := slog.New(slog.NewTextHandler(&buf, nil))
			a, err := New(Options{Config: cfg, Version: "v2", Log: log, Store: store.NewMemory(), Embedder: &embed.Fake{}})
			require.NoError(t, err)
			t.Cleanup(func() { a.Close(context.Background()) })

			ct, st := mcp.NewInMemoryTransports()
			_, err = a.Server.Connect(context.Background(), st, nil)
			require.NoError(t, err)
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "2.1"}, nil).Connect(context.Background(), ct, &mcp.ClientSessionOptions{ProtocolVersion: proto})
			require.NoError(t, err)
			defer cs.Close()
			_, err = cs.ListTools(context.Background(), nil)
			require.NoError(t, err)
			_, err = cs.ListTools(context.Background(), nil)
			require.NoError(t, err)
			assert.Eventually(t, func() bool {
				return strings.Contains(buf.String(), `msg="client connected" client=claude-code client_version=2.1`)
			}, 2*time.Second, 10*time.Millisecond, buf.String())
			assert.Equal(t, 1, strings.Count(buf.String(), `msg="client connected"`), "once per session")
		})
	}
}

// Stateless HTTP makes a session per request: the client is still named once,
// and nothing is kept per request.
func TestLogsConnectedClientOnceOverStatelessHTTP(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.WatchEnabled = false
	var buf syncBuf
	a, err := New(Options{Config: cfg, Version: "v2", Log: slog.New(slog.NewTextHandler(&buf, nil)), Store: store.NewMemory(), Embedder: &embed.Fake{}})
	require.NoError(t, err)
	t.Cleanup(func() { a.Close(context.Background()) })
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return a.Server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer srv.Close()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "codex", Version: "1"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	require.NoError(t, err)
	defer cs.Close()
	for range 3 {
		_, err = cs.ListTools(context.Background(), nil)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, strings.Count(buf.String(), `msg="client connected"`), buf.String())
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
