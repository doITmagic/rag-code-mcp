package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/healthcheck"
	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/internal/utils"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/html"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/php"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/python"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RunConfig holds all configuration needed to start the daemon.
type RunConfig struct {
	Version    string
	Commit     string
	Date       string
	ConfigPath string
	HTTPPort   int

	// OllamaOverrides
	OllamaBaseURL string
	OllamaModel   string
	OllamaEmbed   string
	QdrantURL     string
}

// Run starts the full daemon process:
// 1. Loads config
// 2. Health checks (Ollama, Qdrant)
// 3. Initializes LLM provider + warmup
// 4. Initializes Qdrant store
// 5. Creates Engine (indexer, search, watchers)
// 6. Sets up MCP server with all tools
// 7. Listens on Unix socket (primary) + optional HTTP (secondary)
// 8. Blocks until SIGTERM/SIGINT
// 9. Graceful cleanup
func Run(rcfg RunConfig) error {
	// GOMAXPROCS — leave headroom for OS, IDE, other processes
	maxProcs := runtime.NumCPU() * 3 / 4
	if maxProcs < 2 {
		maxProcs = 2
	}
	runtime.GOMAXPROCS(maxProcs)
	logger.Instance.Info("GOMAXPROCS set to %d (NumCPU=%d)", maxProcs, runtime.NumCPU())

	// ── Config ──
	cfgPath := rcfg.ConfigPath
	if filepath.Base(cfgPath) == cfgPath {
		if exePath, err := os.Executable(); err == nil {
			cfgPath = filepath.Join(filepath.Dir(exePath), cfgPath)
		}
	}
	if err := config.EnsureConfigExists(cfgPath); err != nil {
		logger.Instance.Warn("Could not create default config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Instance.Warn("Failed to load config %s, using defaults: %v", cfgPath, err)
		cfg = config.DefaultConfig()
	}

	config.ApplyCLIOverrides(cfg, rcfg.OllamaBaseURL, rcfg.OllamaModel, rcfg.OllamaEmbed, rcfg.QdrantURL)
	utils.ApplyPathOverrides(cfg.Paths.LogsDir, cfg.Paths.Registry, cfg.Paths.SkillsCache, cfg.Paths.UpdateCache)

	if cfg.Paths.LogsDir != "" {
		logger.InitLoggerFromEnv()
	}

	// ── Health Checks ──
	if cfg.HealthCheck.EnableOnStartup {
		models := []string{cfg.LLM.OllamaEmbed}
		health := healthcheck.CheckAllWithModels(cfg.LLM.OllamaBaseURL, cfg.Storage.VectorDB.URL, models)
		for _, h := range health {
			if h.Status != "ok" {
				remediation := healthcheck.GetRemediation(health, models)
				return fmt.Errorf("health check failed for %s: %s\n%s", h.Service, h.Message, remediation)
			}
		}
		logger.Instance.Info("Health checks passed")
	} else {
		logger.Instance.Warn("Health checks skipped (health_check.enable_on_startup=false)")
	}

	// ── LLM Provider ──
	ollamaProvider, err := llm.NewOllamaLLMProvider(cfg.LLM)
	if err != nil {
		return fmt.Errorf("failed to create Ollama provider: %w", err)
	}
	provider := llm.NewRetryableProvider(ollamaProvider, 3, 30*time.Second)

	if err := ollamaProvider.Warmup(context.Background()); err != nil {
		logger.Instance.Warn("Ollama warmup failed (model may cold-start on first embed): %v", err)
	}
	logger.Instance.Info("LLM provider ready: embed=%s (retries=3, timeout=30s, keep_alive=30m)", cfg.LLM.OllamaEmbed)

	// ── Vector Store ──
	qdrantHost, qdrantPort := storage.ParseQdrantURL(cfg.Storage.VectorDB.URL)
	vectorStore, err := storage.NewQdrantStore(qdrantHost, qdrantPort, false, cfg.Storage.VectorDB.APIKey)
	if err != nil {
		return fmt.Errorf("failed to connect to Qdrant at %s:%d: %w", qdrantHost, qdrantPort, err)
	}
	logger.Instance.Info("Qdrant store ready: %s:%d", qdrantHost, qdrantPort)

	// ── Services ──
	indexerSvc := indexer.NewService(provider, vectorStore)
	searchSvc := search.NewService(provider, vectorStore)

	// ── Registry ──
	registryPath := utils.GetRegistryPath()
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		logger.Instance.Warn("Failed to create registry dir: %v", err)
	}

	eng := engine.NewEngine(indexerSvc, searchSvc, registryPath, cfg)
	logger.Instance.Info("Engine initialized, registry=%s", registryPath)

	tools.SetServerBuildInfo(rcfg.Version, rcfg.Commit, rcfg.Date)

	// ── MCP Server ──
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "ragcode",
		Version: rcfg.Version,
	}, &mcp.ServerOptions{
		Instructions: "RagCode MCP: always pass the absolute file_path of the current file to help detect the workspace.",
	})

	tools.NewSmartSearchTool(eng).Register(mcpServer)
	tools.NewFindUsagesTool(eng).Register(mcpServer)
	tools.NewListPackageExportsTool(eng).Register(mcpServer)
	tools.NewCallHierarchyTool(eng).Register(mcpServer)
	tools.NewReadFileContextTool(eng).Register(mcpServer)
	tools.NewIndexWorkspaceTool(eng).Register(mcpServer)
	tools.NewListSkillsTool(eng, cfg.Skills).Register(mcpServer)
	tools.NewInstallSkillTool(eng, cfg.Skills).Register(mcpServer)
	tools.NewEvaluateRagCodeTool(eng, cfg).Register(mcpServer)
	tools.NewCheckUpdateTool(rcfg.Version, cfg).Register(mcpServer)
	tools.NewApplyUpdateTool(rcfg.Version).Register(mcpServer)

	logger.Instance.Info("MCP RagCode Daemon initialized (version=%s)", rcfg.Version)

	// ── Streamable HTTP handler for MCP ──
	streamableHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})

	mcpMux := http.NewServeMux()
	mcpMux.Handle("/mcp", streamableHandler)

	// ── Paths ──
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	ragcodeDir := filepath.Join(homeDir, ".ragcode")
	if err := os.MkdirAll(ragcodeDir, 0o700); err != nil {
		return fmt.Errorf("cannot create ~/.ragcode: %w", err)
	}
	socketPath := filepath.Join(ragcodeDir, "daemon.sock")
	pidPath := filepath.Join(ragcodeDir, "daemon.pid")

	// ── Start Daemon Listeners ──
	logger.Instance.Info("--- DAEMON MODE --- version=%s pid=%d", rcfg.Version, os.Getpid())

	listenErr := ListenAndServe(context.Background(), ListenConfig{
		SocketPath: socketPath,
		PIDPath:    pidPath,
		Version:    rcfg.Version,
		HTTPPort:   rcfg.HTTPPort,
		Handler:    mcpMux,
		OnReady: func() {
			logger.Instance.Info("Daemon ready — socket=%s, http_port=%d", socketPath, rcfg.HTTPPort)
		},
	})

	// Cleanup
	logger.Instance.Info("Daemon shutting down...")
	eng.StopWatchers()

	if listenErr != nil {
		return fmt.Errorf("daemon listen error: %w", listenErr)
	}
	return nil
}
