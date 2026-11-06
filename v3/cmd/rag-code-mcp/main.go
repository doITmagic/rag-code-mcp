// Command rag-code-mcp is the RagCode MCP server: stdio by default, or
// Streamable HTTP with --http. Nothing but the stdio transport writes to
// stdout; logs go to $RAGCODE_HOME/logs/ragcode.log.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/internal/app"
	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/internal/uninstall"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is set at build time: -ldflags "-X main.Version=v3.x.y".
var Version = "dev"

const closeTimeout = 10 * time.Second

func versionLine() string { return "rag-code-mcp " + Version }

func main() {
	cfgPath := flag.String("config", config.DefaultPath(), "configuration file")
	httpAddr := flag.String("http", "", "serve Streamable HTTP on this address instead of stdio (e.g. 127.0.0.1:39000)")
	logLevel := flag.String("log-level", "", "debug, info, warn or error (overrides the config)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	doUninstall := flag.Bool("uninstall", false, "remove RagCode MCP from this machine and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(versionLine())
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *doUninstall {
		if err := uninstall.Run(ctx, uninstall.Options{}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(ctx, *cfgPath, *httpAddr, *logLevel); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// setMemoryLimit caps the heap at 1 GiB unless GOMEMLIMIT is set: parallel
// tree-sitter parsing otherwise left 1.3-5 GB RSS for minutes during indexing.
func setMemoryLimit() {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(1 << 30)
	}
}

func run(ctx context.Context, cfgPath, httpAddr, logLevel string) error {
	setMemoryLimit()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if logLevel != "" {
		cfg.LogLevel = logLevel
	}
	logPath := filepath.Join(config.HomeDir(), "logs", "ragcode.log")
	var out io.Writer = os.Stderr
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		if f, err := config.OpenLogFile(logPath, cfg.LogMaxSizeMB); err == nil {
			defer f.Close()
			out = f
		} else {
			fmt.Fprintln(os.Stderr, "logging to stderr:", err)
		}
	}
	log := config.NewLogger(cfg.LogLevel, out).With("pid", os.Getpid()) // every server process writes the same file

	a, err := app.New(app.Options{Config: cfg, Version: Version, Log: log})
	if err != nil {
		return err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		a.Close(cctx)
	}()
	log.Info("rag-code-mcp starting", "version", Version, "http", httpAddr)

	if httpAddr == "" {
		go a.Engine.Resume(ctx)
		err = a.Server.Run(ctx, &mcp.StdioTransport{})
	} else {
		err = serveHTTP(ctx, httpAddr, a, log)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error("server stopped", "err", err)
		return err
	}
	log.Info("rag-code-mcp stopped")
	return nil
}

// httpHandler serves the MCP endpoint at / and GET /health.
func httpHandler(server *mcp.Server, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"version": Version, "pid": os.Getpid()})
	})
	mux.Handle("/", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, Logger: log}))
	return mux
}

// serveHTTP listens on addr until ctx is done; Resume starts once the
// listener is up.
func serveHTTP(ctx context.Context, addr string, a *app.App, log *slog.Logger) error {
	srv := &http.Server{Addr: addr, Handler: httpHandler(a.Server, log), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	go a.Engine.Resume(ctx)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}
