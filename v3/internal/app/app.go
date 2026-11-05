// Package app wires config, embedder, store, engine and the MCP tools into
// one server. main serves it; tests drive it over in-memory transports.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/engine"
	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/doITmagic/rag-code-mcp/v3/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	// parser packages: keep in sync with v3/parser/*
	_ "github.com/doITmagic/rag-code-mcp/v3/parser/docs"
	_ "github.com/doITmagic/rag-code-mcp/v3/parser/gdscript"
	_ "github.com/doITmagic/rag-code-mcp/v3/parser/golang"
	_ "github.com/doITmagic/rag-code-mcp/v3/parser/javascript"
	_ "github.com/doITmagic/rag-code-mcp/v3/parser/php"
	_ "github.com/doITmagic/rag-code-mcp/v3/parser/python"
	_ "github.com/doITmagic/rag-code-mcp/v3/parser/web"
)

// Options configures New. Store and Embedder default to Qdrant and Ollama
// from Config; tests pass store.NewMemory() and &embed.Fake{}.
type Options struct {
	Config   config.Config
	Version  string
	Log      *slog.Logger
	Store    store.Store
	Embedder embed.Embedder
}

// App is the built server with the engine behind it.
type App struct {
	Server *mcp.Server
	Engine *engine.Engine
	store  store.Store
	log    *slog.Logger
}

// New builds the app. Nothing here dials Ollama or Qdrant.
func New(o Options) (*App, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Embedder == nil {
		o.Embedder = embed.NewOllama(o.Config.OllamaBaseURL, o.Config.OllamaEmbed, nil)
	}
	if o.Store == nil {
		q, err := store.NewQdrant(o.Config.QdrantURL, o.Config.QdrantAPIKey)
		if err != nil {
			return nil, fmt.Errorf("app: %w", err)
		}
		o.Store = q
	}
	if o.Log != nil {
		slog.SetDefault(o.Log) // the one process-wide logger; tools log through it
	}
	eng := engine.New(o.Config, o.Store, o.Embedder, o.Log)
	srv := mcp.NewServer(&mcp.Implementation{Name: "ragcode", Version: o.Version}, &mcp.ServerOptions{Logger: slog.New(sdkLog{o.Log.Handler()})})
	tools.Register(srv, eng, o.Config, o.Version, time.Now())
	return &App{Server: srv, Engine: eng, store: o.Store, log: o.Log}, nil
}

// sdkLog passes the go-sdk's records through, except that its
// "server run cancelled" ERROR, logged on every stop by signal, becomes INFO.
type sdkLog struct{ slog.Handler }

func (h sdkLog) Handle(ctx context.Context, r slog.Record) error {
	if r.Level == slog.LevelError && r.Message == "server run cancelled" {
		r.Level = slog.LevelInfo
		if !h.Enabled(ctx, r.Level) { // the logger checked Enabled for ERROR
			return nil
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h sdkLog) WithAttrs(as []slog.Attr) slog.Handler { return sdkLog{h.Handler.WithAttrs(as)} }
func (h sdkLog) WithGroup(n string) slog.Handler       { return sdkLog{h.Handler.WithGroup(n)} }

// Close stops the engine, giving it until ctx is done, then closes the store.
func (a *App) Close(ctx context.Context) {
	done := make(chan struct{})
	go func() { a.Engine.Close(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		a.log.Warn("app: engine did not stop in time", "err", ctx.Err())
	}
	if err := a.store.Close(); err != nil {
		a.log.Warn("app: store close", "err", err)
	}
}
