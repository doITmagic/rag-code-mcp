package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
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
//  2. TCP HTTP on HTTPPort (optional, for curl/debug/external agents)
//
// Both serve the same handler mux with /health and the provided MCP handler.
func ListenAndServe(ctx context.Context, cfg ListenConfig) error {
	startTime := time.Now()

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

	// Ensure cleanup of socket file on exit
	defer func() {
		unixListener.Close()
		os.Remove(cfg.SocketPath)
	}()

	// Write PID file
	if err := WritePID(cfg.PIDPath, os.Getpid(), cfg.Version); err != nil {
		slog.Warn("Failed to write PID file", "error", err)
	}
	defer func() { _ = RemovePID(cfg.PIDPath) }()

	// --- Optional TCP HTTP listener ---
	var tcpListener net.Listener
	if cfg.HTTPPort > 0 {
		tcpListener, err = net.Listen("tcp", fmt.Sprintf(":%d", cfg.HTTPPort))
		if err != nil {
			slog.Warn("HTTP port unavailable (non-fatal, Unix socket is primary)",
				"port", cfg.HTTPPort, "error", err)
			// Non-fatal — Unix socket is the primary channel
		}
	}

	// Signal readiness
	if cfg.OnReady != nil {
		cfg.OnReady()
	}

	// Start serving on Unix socket
	unixServer := &http.Server{Handler: mux}
	go func() {
		if serveErr := unixServer.Serve(unixListener); serveErr != nil && serveErr != http.ErrServerClosed {
			slog.Error("Unix socket server error", "error", serveErr)
		}
	}()

	// Start serving on TCP (if available)
	var tcpServer *http.Server
	if tcpListener != nil {
		tcpServer = &http.Server{Handler: mux}
		go func() {
			if serveErr := tcpServer.Serve(tcpListener); serveErr != nil && serveErr != http.ErrServerClosed {
				slog.Error("TCP server error", "error", serveErr)
			}
		}()
		slog.Info("HTTP server listening", "port", cfg.HTTPPort)
	}

	slog.Info("Daemon ready", "socket", cfg.SocketPath, "version", cfg.Version, "pid", os.Getpid())

	// Block until context cancellation or OS signal
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	slog.Info("Daemon shutting down...")

	// Graceful shutdown with 5s deadline
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if shutdownErr := unixServer.Shutdown(shutdownCtx); shutdownErr != nil {
		slog.Warn("Unix server shutdown error", "error", shutdownErr)
	}
	if tcpServer != nil {
		if shutdownErr := tcpServer.Shutdown(shutdownCtx); shutdownErr != nil {
			slog.Warn("TCP server shutdown error", "error", shutdownErr)
		}
	}

	return nil
}
