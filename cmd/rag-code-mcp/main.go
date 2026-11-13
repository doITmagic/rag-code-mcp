// Command rag-code-mcp is the RagCode MCP server: stdio by default, or
// Streamable HTTP with --http. Nothing but the stdio transport writes to
// stdout; logs go to $RAGCODE_HOME/logs/ragcode.log.
package main

import (
	"cmp"
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
	"sync"
	"syscall"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/internal/app"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/internal/uninstall"
	"github.com/doITmagic/rag-code-mcp/v2/internal/update"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is set at build time: -ldflags "-X main.Version=v2.x.y" (goreleaser: {{.Tag}}).
var Version = "dev"

const closeTimeout = 10 * time.Second

func versionLine() string { return "rag-code-mcp " + Version }

func main() {
	cfgPath := flag.String("config", config.DefaultPath(), "configuration file")
	httpAddr := flag.String("http", "", "serve Streamable HTTP on this address instead of stdio (e.g. 127.0.0.1:39000)")
	logLevel := flag.String("log-level", "", "debug, info, warn or error (overrides the config)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	doUninstall := flag.Bool("uninstall", false, "remove RagCode MCP from this machine and exit")
	doUpdate := flag.Bool("update", false, "install a newer release over the installed binary and exit")
	flag.Parse()
	// Parsing stops at the first non-flag argument; flags after it would be ignored silently.
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q (flags only; see -h)\n", flag.Arg(0))
		os.Exit(2)
	}

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
	if *doUpdate {
		if err := updateNow(ctx, *cfgPath); err != nil {
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

// updateNow is -update: install a newer release from update_source (GitHub
// when empty) over the installed binary, without the daily cache.
func updateNow(ctx context.Context, cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	src := cmp.Or(cfg.UpdateSource, update.GitHubLatest)
	v, err := update.Run(ctx, src, uninstall.Binary(config.HomeDir()), Version)
	switch {
	case err != nil:
		return err
	case v == "":
		fmt.Println("no newer release than", Version, "at", config.Redacted(src))
	default:
		fmt.Printf("updated %s -> %s; restart the MCP clients to use it\n", Version, v)
	}
	return nil
}

// autoUpdate installs a newer release when this process is the installed
// binary (not a dev build, Homebrew or a copy elsewhere). GitHub is asked at
// most once a day through the update.json cache.
func autoUpdate(ctx context.Context, source string, log *slog.Logger) {
	target := uninstall.Binary(config.HomeDir())
	if exe, err := os.Executable(); err != nil || !sameFile(exe, target) {
		log.Debug("update: not the installed binary, skipped", "exe", exe, "installed", target)
		return
	}
	if source == "" {
		source = update.GitHubLatest
		if l := update.Latest(ctx, http.DefaultClient, source); !update.Less(Version, l) {
			return
		}
	}
	v, err := update.Run(ctx, source, target, Version)
	switch {
	case err != nil && ctx.Err() == nil:
		log.Warn("update: failed", "source", config.Redacted(source), "err", err)
	case v != "":
		log.Info("update: installed; restart the MCP clients to use it", "from", Version, "to", v)
	}
}

func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
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
		if err := cfg.Validate(); err != nil {
			return err
		}
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
	// The engine stops when its cancelled run returns: no clock, so a slow
	// disk is not cut off with its state half written.
	defer a.Close(context.Background())
	log.Info("rag-code-mcp starting", "version", Version, "http", httpAddr)
	if exe, err := os.Executable(); err == nil {
		if bin := uninstall.Binary(config.HomeDir()); sameFile(exe, bin) {
			update.Remember(bin) // tool answers ask for a restart once it is replaced
		}
	}
	if cfg.AutoUpdate {
		go autoUpdate(ctx, cfg.UpdateSource, log)
	}

	// The SDK waits for in-flight calls before it returns; closing the engine
	// first ends a call stuck in a watcher walk (fsnotify refuses new watches).
	defer context.AfterFunc(ctx, a.Engine.Close)()
	if httpAddr == "" {
		go a.Engine.Resume(ctx)
		closeEngine := sync.OnceFunc(func() { go a.Engine.Close() })
		err = a.Server.Run(ctx, &mcp.IOTransport{Reader: onEOF{os.Stdin, closeEngine}, Writer: nopWriteCloser{os.Stdout}})
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

// onEOF runs f when the client closes stdin.
type onEOF struct {
	io.ReadCloser
	f func()
}

func (r onEOF) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) {
		r.f()
	}
	return n, err
}

// nopWriteCloser keeps stdout open, as mcp.StdioTransport does.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

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
