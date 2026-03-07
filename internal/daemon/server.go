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
	SocketPath string       // Unix domain socket path (required)
	PIDPath    string       // PID file path (required)
	Version    string       // Server version string
	HTTPPort   int          // TCP port for optional HTTP listener (0 = disabled)
	Handler    http.Handler // MCP handler (must handle /mcp)
	OnReady    func()       // Called when daemon is ready to accept connections (optional)
}

// ListenAndServe starts the daemon listeners and blocks until ctx is cancelled
// or SIGTERM/SIGINT is received. Cleans up socket and PID file on exit.
//
// It sets up two listeners:
//  1. Unix domain socket at SocketPath (primary, for stdio adapters)
//  2. TCP HTTP on HTTPPort (optional, for curl/debug/external agents, localhost only)
//
// Both serve the same handler mux with /health and the provided MCP handler.
func ListenAndServe(ctx context.Context, cfg ListenConfig) error {
	startTime := time.Now()

	// Validate required config
	if cfg.SocketPath == "" {
		return fmt.Errorf("ListenAndServe: SocketPath is required")
	}
	if cfg.PIDPath == "" {
		return fmt.Errorf("ListenAndServe: PIDPath is required")
	}

	// Remove stale socket if it exists
	os.Remove(cfg.SocketPath)

	// Build mux: /health + user handler for everything else
	mux := http.NewServeMux()
	mux.Handle("/health", HealthHandler(cfg.Version, startTime))
	if cfg.Handler != nil {
		mux.Handle("/", cfg.Handler)
	}

	// --- Unix socket listener (primary) ---
	unixListener, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on unix socket %s: %w", cfg.SocketPath, err)
	}
	// Restrict socket access to owner only (security: prevents other local users from connecting)
	if chmodErr := os.Chmod(cfg.SocketPath, 0o600); chmodErr != nil {
		logger.Instance.Warn("Failed to chmod socket to 0600: %v", chmodErr)
	}

	// Ensure cleanup of socket file on exit
	defer func() {
		unixListener.Close()
		os.Remove(cfg.SocketPath)
	}()

	// Write PID file (fatal on failure — adapters rely on it for discovery/version checks)
	if err := WritePID(cfg.PIDPath, os.Getpid(), cfg.Version); err != nil {
		unixListener.Close()
		os.Remove(cfg.SocketPath)
		return fmt.Errorf("failed to write PID file %s: %w", cfg.PIDPath, err)
	}
	defer func() { _ = RemovePID(cfg.PIDPath) }()

	// --- Optional TCP HTTP listener (localhost only for security) ---
	var tcpListener net.Listener
	if cfg.HTTPPort > 0 {
		tcpListener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.HTTPPort))
		if err != nil {
			logger.Instance.Warn("HTTP port unavailable (non-fatal, Unix socket is primary): port=%d err=%v",
				cfg.HTTPPort, err)
			// Non-fatal — Unix socket is the primary channel
		}
	}

	// Start serving on Unix socket
	unixServer := &http.Server{Handler: mux}
	go func() {
		if serveErr := unixServer.Serve(unixListener); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Instance.Error("Unix socket server error: %v", serveErr)
		}
	}()

	// Start serving on TCP (if available)
	var tcpServer *http.Server
	if tcpListener != nil {
		tcpServer = &http.Server{Handler: mux}
		go func() {
			if serveErr := tcpServer.Serve(tcpListener); serveErr != nil && serveErr != http.ErrServerClosed {
				logger.Instance.Error("TCP server error: %v", serveErr)
			}
		}()
		logger.Instance.Info("HTTP server listening on 127.0.0.1:%d", cfg.HTTPPort)
	}

	// Signal readiness AFTER servers are actually serving
	if cfg.OnReady != nil {
		cfg.OnReady()
	}

	logger.Instance.Info("Daemon ready — socket=%s version=%s pid=%d", cfg.SocketPath, cfg.Version, os.Getpid())

	// Block until context cancellation or OS signal
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	logger.Instance.Info("Daemon shutting down...")

	// Graceful shutdown with 5s deadline
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if shutdownErr := unixServer.Shutdown(shutdownCtx); shutdownErr != nil {
		logger.Instance.Warn("Unix server shutdown error: %v", shutdownErr)
	}
	if tcpServer != nil {
		if shutdownErr := tcpServer.Shutdown(shutdownCtx); shutdownErr != nil {
			logger.Instance.Warn("TCP server shutdown error: %v", shutdownErr)
		}
	}

	return nil
}
