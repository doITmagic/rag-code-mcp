package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
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
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/html"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/php"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/python"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	Version = "2.1.9"
	Commit  = "none"
	Date    = "24.10.2025"
)

func main() {
	// Flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	ollamaURLFlag := flag.String("ollama-base-url", "", "Ollama base URL override")
	ollamaModel := flag.String("ollama-model", "", "Ollama chat model override")
	ollamaEmbed := flag.String("ollama-embed", "", "Ollama embedding model override")
	qdrantURLFlag := flag.String("qdrant-url", "", "Qdrant URL override")
	httpPort := flag.Int("http-port", 3000, "Port for SSE server (default 3000, set -1 to disable)")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	logger.InitLoggerFromEnv()

	// Limit Go scheduler threads to 75% of CPUs — leaves headroom for the OS,
	// IDE, and other processes. Prevents freezes when indexing multiple workspaces.
	maxProcs := runtime.NumCPU() * 3 / 4
	if maxProcs < 2 {
		maxProcs = 2
	}
	runtime.GOMAXPROCS(maxProcs)
	logger.Instance.Info("GOMAXPROCS set to %d (NumCPU=%d)", maxProcs, runtime.NumCPU())

	if *versionFlag {
		fmt.Printf("RagCode MCP  version=%s  commit=%s  date=%s\n", Version, Commit, Date)
		os.Exit(0)
	}

	// Config
	cfgPath := *configPath
	if err := config.EnsureConfigExists(cfgPath); err != nil {
		logger.Instance.Warn("Could not create default config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Instance.Warn("Failed to load config %s, using defaults: %v", cfgPath, err)
		cfg = config.DefaultConfig()
	}

	// Apply CLI overrides
	config.ApplyCLIOverrides(cfg, *ollamaURLFlag, *ollamaModel, *ollamaEmbed, *qdrantURLFlag)

	// Kill any existing processes on the HTTP port before health checks
	if *httpPort > 0 {
		if err := utils.KillProcessesOnPort(*httpPort); err != nil {
			logger.Instance.Warn("Failed to kill processes on port %d: %v", *httpPort, err)
		}
	}

	// Health checks (only embedding model is strictly required for core RAG)
	if cfg.HealthCheck.EnableOnStartup {
		models := []string{cfg.LLM.OllamaEmbed}

		health := healthcheck.CheckAllWithModels(cfg.LLM.OllamaBaseURL, cfg.Storage.VectorDB.URL, models)
		for _, h := range health {
			if h.Status != "ok" {
				log.Fatalf("Health check failed for %s: %s\n%s", h.Service, h.Message, healthcheck.GetRemediation(health, models))
			}
		}
		logger.Instance.Info("Health checks passed")
	} else {
		logger.Instance.Warn("Health checks skipped (health_check.enable_on_startup=false)")
	}

	// LLM Provider
	ollamaProvider, err := llm.NewOllamaLLMProvider(cfg.LLM)
	if err != nil {
		log.Fatalf("Failed to create Ollama provider: %v", err)
	}
	logger.Instance.Info("LLM provider ready: chat=%s embed=%s", cfg.LLM.OllamaModel, cfg.LLM.OllamaEmbed)

	// Vector Store
	qdrantHost, qdrantPort := storage.ParseQdrantURL(cfg.Storage.VectorDB.URL)
	vectorStore, err := storage.NewQdrantStore(qdrantHost, qdrantPort, false, cfg.Storage.VectorDB.APIKey)
	if err != nil {
		log.Fatalf("Failed to connect to Qdrant at %s:%d: %v", qdrantHost, qdrantPort, err)
	}
	logger.Instance.Info("Qdrant store ready: %s:%d", qdrantHost, qdrantPort)

	// Services
	indexerSvc := indexer.NewService(ollamaProvider, vectorStore)
	searchSvc := search.NewService(ollamaProvider, vectorStore)

	// Registry
	registryPath := utils.GetRegistryPath()
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		logger.Instance.Warn("Failed to create registry dir: %v", err)
	}

	eng := engine.NewEngine(indexerSvc, searchSvc, registryPath, cfg)
	logger.Instance.Info("Engine initialized, registry=%s", registryPath)

	tools.SetServerBuildInfo(Version, Commit, Date)

	// MCP Server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ragcode",
		Version: Version,
	}, &mcp.ServerOptions{
		Instructions: "RagCode MCP: always pass the absolute file_path of the current file to help detect the workspace.",
	})

	// Register Tools
	tools.NewSearchLocalIndexTool(eng).Register(server)
	tools.NewFindUsagesTool(eng).Register(server)
	tools.NewListPackageExportsTool(eng).Register(server)
	tools.NewCallHierarchyTool(eng).Register(server)
	tools.NewReadFileContextTool(eng).Register(server)
	tools.NewIndexWorkspaceTool(eng).Register(server)
	tools.NewListSkillsTool(eng).Register(server)
	tools.NewInstallSkillTool(eng).Register(server)
	tools.NewEvaluateRagCodeTool(eng, cfg).Register(server)
	tools.NewCheckUpdateTool(Version, cfg).Register(server)
	tools.NewApplyUpdateTool(Version).Register(server)

	logger.Instance.Info("MCP RagCode Server initialized")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// SSE Server
	var httpServer *http.Server
	if *httpPort > 0 {
		sseHandler := mcp.NewSSEHandler(func(r *http.Request) *mcp.Server {
			return server
		}, nil)

		mux := http.NewServeMux()
		mux.Handle("/sse", sseHandler)
		mux.Handle("/messages", sseHandler)

		httpServer = &http.Server{
			Addr:    fmt.Sprintf(":%d", *httpPort),
			Handler: mux,
		}

		go func() {
			logger.Instance.Info("Starting SSE server on http://localhost:%d/sse", *httpPort)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Instance.Error("HTTP server failed: %v", err)
				cancel()
			}
		}()
	}

	// Stdio Server (always runs, in parallel with SSE if http-port is set)
	logger.Instance.Info("Starting Stdio server")
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		if ctx.Err() == nil {
			logger.Instance.Error("Stdio server error: %v", err)
			os.Exit(1)
		}
	}

	// Cleanup
	logger.Instance.Info("Shutting down...")
	eng.StopWatchers()
	if httpServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Instance.Error("HTTP shutdown error: %v", err)
		}
	}
}
