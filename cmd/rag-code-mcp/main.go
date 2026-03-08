package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Masterminds/semver/v3"
	"github.com/doITmagic/rag-code-mcp/internal/adapter"
	"github.com/doITmagic/rag-code-mcp/internal/daemon"
	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/uninstall"
)

var (
	Version = "2.1.63"
	Commit  = "none"
	Date    = "24.10.2025"
)

func main() {
	// Flags
	daemonFlag := flag.Bool("daemon", false, "Run as background daemon (internal use)")
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	ollamaURLFlag := flag.String("ollama-base-url", "", "Ollama base URL override")
	ollamaModel := flag.String("ollama-model", "", "Ollama chat model override")
	ollamaEmbed := flag.String("ollama-embed", "", "Ollama embedding model override")
	qdrantURLFlag := flag.String("qdrant-url", "", "Qdrant URL override")
	httpPort := flag.Int("http-port", 3000, "Port for optional HTTP server (default 3000, set -1 to disable)")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	uninstallFlag := flag.Bool("uninstall", false, "Uninstall RagCode MCP from this system")
	flag.Parse()

	if *uninstallFlag {
		uninstall.RunUninstall()
		os.Exit(0)
	}

	if *versionFlag {
		fmt.Printf("RagCode MCP  version=%s  commit=%s  date=%s\n", Version, Commit, Date)
		os.Exit(0)
	}

	logger.InitLoggerFromEnv()

	if *daemonFlag {
		// ═══════════════════════════════════════════════════════════════
		// DAEMON MODE — the heavy process: Qdrant, Ollama, Engine, MCP
		// Listens on Unix socket + optional HTTP
		// ═══════════════════════════════════════════════════════════════
		if err := daemon.Run(daemon.RunConfig{
			Version:       Version,
			Commit:        Commit,
			Date:          Date,
			ConfigPath:    *configPath,
			HTTPPort:      *httpPort,
			OllamaBaseURL: *ollamaURLFlag,
			OllamaModel:   *ollamaModel,
			OllamaEmbed:   *ollamaEmbed,
			QdrantURL:     *qdrantURLFlag,
		}); err != nil {
			log.Fatalf("Daemon error: %v", err)
		}
	} else {
		// ═══════════════════════════════════════════════════════════════
		// ADAPTER MODE (default) — thin Stdio ↔ Unix socket bridge
		// Each IDE launches this mode; daemon is started automatically.
		// ═══════════════════════════════════════════════════════════════

		// Build daemon args from CLI flags so the adapter-started daemon
		// uses the same configuration as the adapter process.
		var daemonArgs []string
		if *configPath != "config.yaml" {
			daemonArgs = append(daemonArgs, "--config", *configPath)
		}
		if *httpPort != 3000 {
			daemonArgs = append(daemonArgs, "--http-port", strconv.Itoa(*httpPort))
		}
		if *ollamaURLFlag != "" {
			daemonArgs = append(daemonArgs, "--ollama-base-url", *ollamaURLFlag)
		}
		if *ollamaModel != "" {
			daemonArgs = append(daemonArgs, "--ollama-model", *ollamaModel)
		}
		if *ollamaEmbed != "" {
			daemonArgs = append(daemonArgs, "--ollama-embed", *ollamaEmbed)
		}
		if *qdrantURLFlag != "" {
			daemonArgs = append(daemonArgs, "--qdrant-url", *qdrantURLFlag)
		}

		runAdapter(Version, daemonArgs)
	}
}

// runAdapter is the thin stdio adapter that bridges IDE ↔ daemon.
// It ensures the daemon is running, handles version upgrades, and bridges stdin/stdout.
// daemonArgs are extra CLI flags forwarded to the daemon process.
func runAdapter(version string, daemonArgs []string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Cannot determine home directory: %v", err)
	}
	ragcodeDir := filepath.Join(homeDir, ".ragcode")
	if err := os.MkdirAll(ragcodeDir, 0o700); err != nil {
		log.Fatalf("Cannot create ~/.ragcode: %v", err)
	}

	pidPath := filepath.Join(ragcodeDir, "daemon.pid")
	sockPath := filepath.Join(ragcodeDir, "daemon.sock")

	// Check if daemon is already running
	running, existingVersion := adapter.IsDaemonRunning(pidPath, sockPath)

	// Version upgrade check: if daemon is running an older version, restart it
	if running && needsUpgrade(existingVersion, version) {
		logger.Instance.Info("Daemon upgrade needed (%s → %s), restarting...", existingVersion, version)
		if err := adapter.StopDaemon(pidPath); err != nil {
			logger.Instance.Warn("Failed to stop old daemon: %v", err)
		}
		adapter.CleanupStaleFiles(pidPath, sockPath)
		running = false
	}

	// Start daemon if not running
	if !running {
		logger.Instance.Info("Starting daemon...")
		binaryPath, err := os.Executable()
		if err != nil {
			log.Fatalf("Cannot determine binary path: %v", err)
		}
		if err := adapter.StartDaemon(binaryPath, sockPath, daemonArgs...); err != nil {
			log.Fatalf("Failed to start daemon: %v", err)
		}
		logger.Instance.Info("Daemon started successfully")
	}

	// Bridge stdin ↔ daemon via Unix socket
	workspaceHint, _ := os.Getwd()
	logger.Instance.Info("Adapter bridging stdin ↔ daemon (workspace_hint=%s)", workspaceHint)

	if err := adapter.RunBridge(context.Background(), sockPath, os.Stdin, os.Stdout, workspaceHint); err != nil {
		logger.Instance.Error("Adapter bridge error: %v", err)
		os.Exit(1)
	}
}

// needsUpgrade checks if the current version is newer than the daemon's version.
func needsUpgrade(daemonVersion, currentVersion string) bool {
	current, errC := semver.NewVersion(currentVersion)
	existing, errD := semver.NewVersion(daemonVersion)
	if errC != nil || errD != nil {
		return false // can't compare, don't upgrade
	}
	return current.GreaterThan(existing)
}
