package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/internal/service/indexer"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	Version = "1.1.21"
	Commit  = "none"
	Date    = "unknown"
)

// MCPTool defines the interface for tools (matches tools.MCPTool)
type MCPTool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}

// SearchCodeInput defines the typed input for the rag_search_code tool.
type SearchCodeInput struct {
	Query       string `json:"query"`
	Limit       int    `json:"limit,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	IncludeDocs bool   `json:"include_docs,omitempty"`
}

// SearchCodeOutput defines the typed output for the rag_search_code tool.
type SearchCodeOutput struct {
	Results string `json:"results"`
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	// Define flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	ollamaBaseURLFlag := flag.String("ollama-base-url", "", "Ollama base URL (overrides config/env)")
	ollamaModelFlag := flag.String("ollama-model", "", "Ollama chat model (overrides config/env)")
	ollamaEmbedFlag := flag.String("ollama-embed", "", "Ollama embedding model (overrides config/env)")
	qdrantURLFlag := flag.String("qdrant-url", "", "Qdrant URL (overrides config/env)")

	versionFlag := flag.Bool("version", false, "Print version information and exit")
	updateFlag := flag.Bool("update", false, "Check for updates and apply if available")
	healthFlag := flag.Bool("health", false, "Run health check and exit")

	// Workspace security flags - can be set in IDE MCP configuration
	allowedPathsFlag := flag.String("allowed-paths", "", "Comma-separated list of allowed workspace paths (e.g., ~/projects,~/work)")
	disableUpwardSearchFlag := flag.Bool("disable-upward-search", false, "Disable searching parent directories for workspace markers")
	autoCreateIDERulesFlag := flag.Bool("auto-create-ide-rules", true, "Automatically create rule files (.cursorrules, etc.) in workspace roots")

	flag.Usage = printUsage
	flag.Parse()

	// Initialize Logger from Env (early init)
	logger.InitLoggerFromEnv()

	// Handle version flag
	if *versionFlag {
		fmt.Printf("RagCode MCP Server\n")
		fmt.Printf("Version:    %s\n", Version)
		fmt.Printf("Commit:     %s\n", Commit)
		fmt.Printf("Build Date: %s\n", Date)
		os.Exit(0)
	}

	// Handle update flag
	if *updateFlag {
		handleUpdates()
		os.Exit(0)
	}

	// Resolve config path logic
	cfgPath := resolveConfigPath(*configPath)

	// Ensure config exists
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := config.EnsureConfigExists(cfgPath); err != nil {
			logger.Instance.Warn("Failed to create default config: %v", err)
		}
	}

	// Load Config
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Instance.Warn("Failed to load config file %s, using defaults: %v", cfgPath, err)
		cfg = config.DefaultConfig()
	}

	// Apply CLI overrides
	applyCLIFlagsToConfig(cfg, *ollamaBaseURLFlag, *ollamaModelFlag, *ollamaEmbedFlag, *qdrantURLFlag, *allowedPathsFlag, *disableUpwardSearchFlag, *autoCreateIDERulesFlag)

	// Update Logger config
	applyLoggingConfig(cfg.Logging)

	// Handle Health Check
	if *healthFlag {
		runHealthCheckAndExit(cfg)
	}

	// Startup Health Check
	if cfg.HealthCheck.EnableOnStartup {
		runStartupHealthCheck(cfg)
	}

	// --- SERVICE INITIALIZATION ---

	logger.Instance.Info("Initializing services...")

	// 1. LLM Provider
	llmCfg := cfg.LLM
	ollamaProvider, err := llm.NewOllamaLLMProvider(llmCfg)
	if err != nil {
		log.Fatalf("Failed to create Ollama provider: %v", err)
	}

	// 2. Vector Store (Qdrant)
	qcfg := storage.QdrantConfig{
		URL:    cfg.Storage.VectorDB.URL,
		APIKey: cfg.Storage.VectorDB.APIKey,
	}
	vectorStore, err := storage.NewQdrantClient(qcfg)
	if err != nil {
		log.Fatalf("Failed to create Qdrant client: %v", err)
	}
	defer vectorStore.Close()

	// 3. Application Services
	indexerService := indexer.NewService(ollamaProvider, vectorStore)
	searchService := search.NewService(ollamaProvider, vectorStore)

	// 4. Registry Path
	home, _ := os.UserHomeDir()
	registryPath := filepath.Join(home, ".ragcode", "registry.json")
	if err := os.MkdirAll(filepath.Dir(registryPath), 0755); err != nil {
		logger.Instance.Warn("Failed to create registry dir: %v", err)
	}

	// 5. THE ENGINE
	eng := engine.NewEngine(indexerService, searchService, registryPath, cfg)

	// --- MCP SERVER SETUP ---

	mcpInstructions := "RagCode MCP requires project context to function. " +
		"If the AI is working in a specific file, please ensure that the 'file_path' parameter " +
		"for any tool call contains the absolute path to that file."

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ragcode",
		Version: Version,
	}, &mcp.ServerOptions{
		Instructions: mcpInstructions,
		// TODO: Implement handlers for workspace folders using Engine
	})

	// --- TOOL REGISTRATION ---

	// Tool 1: Search Code (Typed)
	searchTool := tools.NewSearchLocalIndexTool(eng)
	searchTool.SetSearchLimit(cfg.RagCode.SearchLimit)
	registerSearchCodeToolTyped(server, searchTool)

	// Tool 2: Evaluate
	evaluateTool := tools.NewEvaluateRagCodeTool(eng, cfg)
	registerAgentTool(server, evaluateTool)

	// Tool 3: Update Tools
	checkUpdateTool := tools.NewCheckUpdateTool(Version)
	registerAgentTool(server, checkUpdateTool)

	applyUpdateTool := tools.NewApplyUpdateTool(Version)
	registerAgentTool(server, applyUpdateTool)

	// Tool 4: Background Updates (if needed)
	//triggerBackgroundUpdateCheck()

	logger.Instance.Info("All tools registered successfully")
	logger.Instance.Info("MCP RagCode Server started (stdio mode)")
	logger.Instance.Info("Embedding Model: %s", cfg.LLM.OllamaEmbed)

	// Run Server
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		if ctx.Err() != nil {
			logger.Instance.Warn("Server terminated due to signal: %v", ctx.Err())
		} else {
			logger.Instance.Error("Server terminated with error: %v", err)
		}
		os.Exit(1)
	}
}

// ... helpers ...

// registerAgentTool registers a standard MCP tool
func registerAgentTool(server *mcp.Server, tool MCPTool) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        tool.Name(),
		Description: tool.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args mcp.CallToolArguments) (*mcp.CallToolResult, map[string]interface{}, error) {
		resultString, err := tool.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("Tool %s failed: %v", tool.Name(), err)
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Text: resultString,
				},
			},
		}, nil, nil
	})
}

// registerSearchCodeToolTyped registers the search tool with typed input/output
func registerSearchCodeToolTyped(server *mcp.Server, tool *tools.SearchLocalIndexTool) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        tool.Name(),
		Description: tool.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchCodeInput) (*mcp.CallToolResult, SearchCodeOutput, error) {
		// Convert typed input to map for compatibility with Tool interface
		args := map[string]interface{}{
			"query": input.Query,
		}
		if input.Limit > 0 {
			args["limit"] = input.Limit
		}
		if input.FilePath != "" {
			args["file_path"] = input.FilePath
		}
		if input.IncludeDocs {
			args["include_docs"] = input.IncludeDocs
		}

		start := time.Now()
		logger.Instance.Info("🛠️ Executing tool '%s' with args: %v", tool.Name(), args)

		resultStr, err := tool.Execute(ctx, args)
		duration := time.Since(start)

		if err != nil {
			logger.Instance.Error("❌ Tool '%s' failed after %v: %v", tool.Name(), duration, err)
			return nil, SearchCodeOutput{}, err
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Text: resultStr,
				},
			},
		}, SearchCodeOutput{Results: resultStr}, nil
	})
}
