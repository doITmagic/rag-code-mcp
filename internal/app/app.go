// Package app wires config, embedder, store, engine and the MCP tools into
// one server. main serves it; tests drive it over in-memory transports.
package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/internal/llama"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/doITmagic/rag-code-mcp/v2/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	// parser packages: keep in sync with parser/*
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/docs"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/gdscript"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/golang"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/javascript"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/php"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/python"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/web"
)

// Options configures New. Store defaults to SQLite under HomeDir()/index and
// Embedder to a llama-server this process runs (internal/llama); tests pass
// store.NewMemory() and &embed.Fake{}.
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
	embed  embed.Embedder
	log    *slog.Logger
}

// New builds the app. Nothing here starts the embedder or opens an index file.
func New(o Options) (*App, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Embedder == nil {
		log := o.Log
		rt := llama.FromConfig(o.Config, func(s string) { log.Info("embedder: " + s) })
		em := embed.NewLlama(rt.ModelLabel(), rt, filepath.Join(config.HomeDir(), "run"))
		em.Threads = o.Config.EmbedThreads
		em.MinFreeMB = o.Config.MinFreeMemoryMB
		em.MaxTokens = o.Config.EmbedMaxTokens
		o.Embedder = em
	}
	if o.Store == nil {
		o.Store = store.NewSQLite(filepath.Join(config.HomeDir(), "index"))
	}
	if o.Log != nil {
		slog.SetDefault(o.Log) // the one process-wide logger; tools log through it
	}
	eng := engine.New(o.Config, o.Store, o.Embedder, o.Log)
	srv := mcp.NewServer(&mcp.Implementation{Name: "ragcode", Version: o.Version}, &mcp.ServerOptions{Logger: slog.New(sdkLog{o.Log.Handler()})})
	srv.AddReceivingMiddleware(logClient(o.Log))
	tools.Register(srv, eng, o.Config, o.Version, time.Now())
	return &App{Server: srv, Engine: eng, store: o.Store, embed: o.Embedder, log: o.Log}, nil
}

// logClient names each client once per process: several clients share one
// log file. ClientInfo comes from initialize, or per request on 2026-07-28.
// It is keyed by the client, not the session: stateless HTTP makes a session
// per request, and keeping those would grow without bound.
func logClient(log *slog.Logger) mcp.Middleware {
	var named sync.Map // client name, version, protocol -> true
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if r, ok := req.(interface {
				ClientInfo() *mcp.Implementation
				ProtocolVersion() string
			}); ok {
				if ci := r.ClientInfo(); ci != nil {
					if _, seen := named.LoadOrStore([3]string{ci.Name, ci.Version, r.ProtocolVersion()}, true); !seen {
						log.Info("client connected", "client", ci.Name, "client_version", ci.Version, "protocol", r.ProtocolVersion())
					}
				}
			}
			return next(ctx, method, req)
		}
	}
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

// Close stops the engine, giving it until ctx is done, then stops the
// embedder's server and closes the store.
func (a *App) Close(ctx context.Context) {
	done := make(chan struct{})
	go func() { a.Engine.Close(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		a.log.Warn("app: engine did not stop in time", "err", ctx.Err())
	}
	if c, ok := a.embed.(io.Closer); ok {
		_ = c.Close()
	}
	if err := a.store.Close(); err != nil {
		a.log.Warn("app: store close", "err", err)
	}
}
