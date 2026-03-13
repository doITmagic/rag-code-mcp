package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/updater"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/healthcheck"
	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/internal/transport"
	"github.com/doITmagic/rag-code-mcp/internal/utils"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/html"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/javascript"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/php"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/php/laravel"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/php/wordpress"
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

	// Update the logging level dynamically based on the configuration file
	if cfg.Logging.Level != "" {
		logger.Instance.SetLevel(cfg.Logging.Level)
		logger.Instance.Debug("Logger level set dynamically from config to: %s", cfg.Logging.Level)
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
	if !cfg.AutoUpdate {
		tools.NewCheckUpdateTool(rcfg.Version, cfg).Register(mcpServer)
		tools.NewApplyUpdateTool(rcfg.Version).Register(mcpServer)
		logger.Instance.Info("Update tools registered (auto_update=false)")
	}

	logger.Instance.Info("MCP RagCode Daemon initialized (version=%s)", rcfg.Version)

	if cfg.AutoUpdate {
		go func() {
			// Give the daemon a few seconds to start up completely
			time.Sleep(10 * time.Second)

			logger.Instance.Info("AutoUpdate: check starting in background...")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			info, err := updater.CheckForUpdates(ctx, rcfg.Version, false)
			if err != nil {
				logger.Instance.Warn("AutoUpdate check failed: %v", err)
				return
			}
			if info == nil {
				logger.Instance.Info("AutoUpdate: No new updates available.")
				return
			}

			logger.Instance.Info("AutoUpdate: New version %s found! Downloading and applying...", info.LatestVersion)

			// Use the shared download+verify+apply helper.
			// On success this calls os.Exit(0) internally (handoff to installer).
			if err := updater.DownloadVerifyAndApply(ctx, info); err != nil {
				logger.Instance.Error("AutoUpdate apply failed: %v", err)
			}
		}()
	}

	// ── Streamable HTTP handler for MCP ──
	streamableHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})

	mcpMux := http.NewServeMux()
	mcpMux.Handle("/mcp", streamableHandler)

	// Profiling endpoints
	mcpMux.HandleFunc("/debug/pprof/", pprof.Index)
	mcpMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mcpMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mcpMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mcpMux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// Middleware: sticky workspace + response writer injection.
	//
	// 1. X-Workspace-Root (sticky): adapter learned workspace from a previous
	//    response header. Inject into context so tools skip resolver cascade.
	// 2. ResponseWriter: always injected into context so DetectContext can set
	//    X-Resolved-Workspace header in the response — the adapter reads it
	//    and caches it for subsequent requests.
	var resumeIndexingOnce sync.Once

	mcpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := transport.WithResponseWriter(r.Context(), w)
		if wsRoot := r.Header.Get("X-Workspace-Root"); wsRoot != "" {
			logger.Instance.Debug("[DAEMON] Request with sticky X-Workspace-Root=%s", wsRoot)
			ctx = transport.WithWorkspaceHint(ctx, wsRoot)
		} else {
			logger.Instance.Debug("[DAEMON] Request without X-Workspace-Root (first request or no workspace resolved yet)")
			resumeIndexingOnce.Do(func() {
				logger.Instance.Info("[DAEMON] Checking registry for incomplete indexing jobs...")
				go eng.ResumeIndexingOnConnect()
			})
		}
		r = r.WithContext(ctx)
		mcpMux.ServeHTTP(w, r)
	})

	// ── Paths ──
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	ragcodeDir := filepath.Join(homeDir, ".ragcode")
	if err := os.MkdirAll(ragcodeDir, 0o700); err != nil {
		return fmt.Errorf("cannot create ~/.ragcode: %w", err)
	}

	// ── Start Daemon Listeners ──
	logger.Instance.Info("--- DAEMON MODE --- version=%s pid=%d", rcfg.Version, os.Getpid())

	listenErr := ListenAndServe(context.Background(), ListenConfig{
		Port:    rcfg.HTTPPort,
		Version: rcfg.Version,
		Handler: mcpHandler,
		OnReady: func() {
			logger.Instance.Info("Daemon ready — port=%d", rcfg.HTTPPort)
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
