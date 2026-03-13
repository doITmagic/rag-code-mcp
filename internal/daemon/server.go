package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
)

// ListenConfig configures the daemon's network listeners and lifecycle.
type ListenConfig struct {
	Port    int          // TCP port for localhost listener
	Version string       // Server version string
	Handler http.Handler // MCP handler (must handle /mcp)
	OnReady func()       // Called when daemon is ready to accept connections (optional)
}

// ListenAndServe starts the daemon listeners and blocks until ctx is cancelled
// or SIGTERM/SIGINT is received. It binds exclusively to a local TCP port to
// guarantee it is a singleton, avoiding file locking issues.
func ListenAndServe(ctx context.Context, cfg ListenConfig) error {
	startTime := time.Now()

	// Validate required config
	if cfg.Port <= 0 {
		return fmt.Errorf("ListenAndServe: valid Port is required")
	}

	// Build mux: /health + user handler for everything else
	mux := http.NewServeMux()
	mux.Handle("/health", HealthHandler(cfg.Version, startTime))
	if cfg.Handler != nil {
		mux.Handle("/", cfg.Handler)
	}

	// Bind to local TCP port (guarantees Singleton)
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	tcpListener, err := net.Listen("tcp", addr)
	if err != nil {
		// If address is in use, another instance is already running
		return fmt.Errorf("failed to bind TCP port %s (address in use?): %w", addr, err)
	}

	tcpServer := &http.Server{Handler: mux}
	go func() {
		if serveErr := tcpServer.Serve(tcpListener); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Instance.Error("TCP server error: %v", serveErr)
		}
	}()
	logger.Instance.Info("HTTP server listening on %s", addr)

	// Signal readiness AFTER servers are actually serving
	if cfg.OnReady != nil {
		cfg.OnReady()
	}

	logger.Instance.Info("Daemon ready — address=%s version=%s pid=%d", addr, cfg.Version, os.Getpid())

	// Block until context cancellation or OS signal
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	logger.Instance.Info("Daemon shutting down...")

	// Graceful shutdown with 5s deadline
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if shutdownErr := tcpServer.Shutdown(shutdownCtx); shutdownErr != nil {
		logger.Instance.Warn("TCP server shutdown error: %v", shutdownErr)
	}

	return nil
}
