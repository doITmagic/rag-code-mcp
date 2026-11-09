package app

import (
	"context"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/require"
)

// Close with an already-expired ctx must return promptly and nil Log must be tolerated.
func TestVerifyCloseExpiredCtx(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	cfg := config.Default()
	a, err := New(Options{Config: cfg, Store: store.NewMemory(), Embedder: &embed.Fake{}})
	require.NoError(t, err)
	root := t.TempDir()
	_, err = a.Engine.Workspace(root)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { a.Close(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung")
	}
}

// Resume with a cancelled ctx returns immediately.
func TestVerifyResumeCancelled(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	a, err := New(Options{Config: config.Default(), Store: store.NewMemory(), Embedder: &embed.Fake{}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { a.Engine.Resume(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Resume hung")
	}
	a.Close(context.Background())
}
