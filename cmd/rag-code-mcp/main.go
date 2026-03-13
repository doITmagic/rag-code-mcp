package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/Masterminds/semver/v3"
	"github.com/doITmagic/rag-code-mcp/internal/adapter"
	"github.com/doITmagic/rag-code-mcp/internal/daemon"
	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/uninstall"
)

var (
	Version = "2.1.79"
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
	httpPort := flag.Int("http-port", 39000, "Port for TCP daemon server (default 39000)")
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
		// Listens exclusively on local TCP port to guarantee singleton
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
		// ADAPTER MODE (default) — thin Stdio ↔ TCP bridge
		// Each IDE launches this mode; daemon is started automatically.
		// ═══════════════════════════════════════════════════════════════

		// Build daemon args from CLI flags so the adapter-started daemon
		// uses the same configuration as the adapter process.
		var daemonArgs []string
		if *configPath != "config.yaml" {
			daemonArgs = append(daemonArgs, "--config", *configPath)
		}
		if *httpPort != 39000 {
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

		runAdapter(Version, *httpPort, daemonArgs)
	}
}

// runAdapter is the thin stdio adapter that bridges IDE ↔ daemon.
// It ensures the daemon is running, handles version upgrades, and bridges stdin/stdout.
// daemonArgs are extra CLI flags forwarded to the daemon process.
func runAdapter(version string, httpPort int, daemonArgs []string) {
	// Check if daemon is already running
	running, existingVersion := adapter.IsDaemonRunning(httpPort)

	// Version upgrade check: if daemon is running an older version, restart it
	if running && needsUpgrade(existingVersion, version) {
		logger.Instance.Info("Daemon upgrade needed (%s → %s), restarting...", existingVersion, version)
		if err := adapter.StopDaemon(httpPort); err != nil {
			logger.Instance.Warn("Failed to stop old daemon: %v", err)
		}
		running = false
	}

	// Start daemon if not running
	if !running {
		logger.Instance.Info("Starting daemon on port %d...", httpPort)
		binaryPath, err := os.Executable()
		if err != nil {
			log.Fatalf("Cannot determine binary path: %v", err)
		}
		if err := adapter.StartDaemon(binaryPath, httpPort, daemonArgs...); err != nil {
			log.Fatalf("Failed to start daemon: %v", err)
		}
		logger.Instance.Info("Daemon started successfully")
	}

	// Bridge stdin ↔ daemon via local TCP port
	workspaceHint, _ := os.Getwd()
	logger.Instance.Info("Adapter bridging stdin ↔ daemon on port %d (workspace_hint=%s)", httpPort, workspaceHint)

	if err := adapter.RunBridge(context.Background(), httpPort, os.Stdin, os.Stdout, workspaceHint); err != nil {
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
