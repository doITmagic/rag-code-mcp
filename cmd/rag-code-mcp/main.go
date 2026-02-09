package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/logging"
	"github.com/doITmagic/rag-code-mcp/internal/mcp"
	"github.com/doITmagic/rag-code-mcp/internal/ragcode"
	"github.com/doITmagic/rag-code-mcp/internal/skills"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
	"github.com/doITmagic/rag-code-mcp/internal/tools"
	"github.com/doITmagic/rag-code-mcp/internal/updater"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// Version is the server version, set at build time
var Version = "dev"

var (
	configPath           = flag.String("config", "config.yaml", "Path to configuration file")
	healthFlag           = flag.Bool("health", false, "Run health check and exit")
	versionFlag          = flag.Bool("version", false, "Print version information and exit")
	updateFlag           = flag.Bool("update", false, "Check for updates and apply if available")
	ollamaModelFlag      = flag.String("ollama-model", "", "Ollama chat model (overrides config/env)")
	ollamaEmbedFlag      = flag.String("ollama-embed", "", "Ollama embedding model (overrides config/env)")
	ollamaBaseURLFlag    = flag.String("ollama-base-url", "", "Ollama base URL (overrides config/env)")
	qdrantURLFlag        = flag.String("qdrant-url", "", "Qdrant URL (overrides config/env)")
	allowedPathsFlag     = flag.String("allowed-paths", "", "Comma-separated list of allowed workspace paths (e.g., ~/projects,~/work)")
	disableUpwardFlag    = flag.Bool("disable-upward-search", false, "Disable searching parent directories for workspace markers")
	autoCreateRulesFlag  = flag.Bool("auto-create-ide-rules", true, "Automatically create rule files (.cursorrules, etc.) in workspace roots")
)

var (
	logger *logging.Logger
)

func main() {
	flag.Parse()

	if *versionFlag {
		fmt.Printf("rag-code-mcp version %s\n", Version)
		return
	}

	if *updateFlag {
		doUpdate()
		return
	}

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Printf("[WARN] Failed to load config from %s, using defaults: %v", *configPath, err)
		cfg = config.DefaultConfig()
	}

	// Apply CLI overrides to config
	applyCLIOverrides(cfg)

	// Initialize specialized logger
	logger, err = logging.NewLogger(cfg, Version)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Close()

	if *healthFlag {
		runHealthCheck(cfg)
		return
	}

	// Initialize Ollama
	ollamaProvider := llm.NewOllamaProvider(cfg)

	// Initialize Qdrant
	qdrantClient, err := storage.NewQdrantClient(cfg.Storage.VectorDB)
	if err != nil {
		log.Fatalf("Failed to initialize Qdrant client: %v", err)
	}
	defer qdrantClient.Close()

	// Initialize Workspace Manager
	workspaceManager := workspace.NewManager(qdrantClient, ollamaProvider, cfg)

	// Trigger background update check
	triggerBackgroundUpdateCheck()

	// Create MCP server
	server := mcp.NewServer("rag-code-mcp", Version)

	// Register tools
	searchTool := tools.NewSearchLocalIndexTool(nil, ollamaProvider)
	searchTool.SetWorkspaceManager(workspaceManager)

	hybridTool := tools.NewHybridSearchTool(qdrantClient, ollamaProvider)
	hybridTool.SetWorkspaceManager(workspaceManager)

	searchDocsTool := tools.NewSearchDocsTool(nil, ollamaProvider)
	searchDocsTool.SetWorkspaceManager(workspaceManager)

	indexWorkspaceTool := tools.NewIndexWorkspaceTool(workspaceManager)
	evaluateTool := tools.NewEvaluateRagCodeTool(workspaceManager)

	listSkillsTool := tools.NewListSkillsTool()
	installSkillTool := tools.NewInstallSkillTool(workspaceManager)
	checkUpdateTool := tools.NewCheckUpdateTool(Version)
	applyUpdateTool := tools.NewApplyUpdateTool(Version)

	// Example: use typed ToolHandlerFor for rag_search_code
	registerSearchCodeToolTyped(server, searchTool, cfg)

	// Other tools still use the generic MCPTool handler
	registerAgentTool(server, tools.NewGetFunctionDetailsTool(), cfg)
	registerAgentTool(server, tools.NewFindImplementationsTool(), cfg)
	registerAgentTool(server, tools.NewFindTypeDefinitionTool(), cfg)
	registerAgentTool(server, tools.NewListPackageExportsTool(), cfg)
	registerAgentTool(server, tools.NewGetCodeContextTool(), cfg)
	registerAgentTool(server, searchDocsTool, cfg)
	registerAgentTool(server, hybridTool, cfg)
	registerAgentTool(server, indexWorkspaceTool, cfg)
	registerAgentTool(server, evaluateTool, cfg)
	registerAgentTool(server, listSkillsTool, cfg)
	registerAgentTool(server, installSkillTool, cfg)
	registerAgentTool(server, checkUpdateTool, cfg)
	registerAgentTool(server, applyUpdateTool, cfg)

	logger.Info("All tools registered successfully")

	if err := registerFileResources(server); err != nil {
		log.Fatalf("Failed to register resources: %v", err)
	}

	// Root management - automatically index workspaces provided by MCP host
	server.OnRootsChanged(func(roots []*mcp.Root) {
		rootPaths := extractFilePathsFromRoots(roots)
		if len(rootPaths) == 0 {
			return
		}

		logger.Info("🌱 Received %d workspace roots from IDE: %v", len(rootPaths), rootPaths)

		// Create ide rules for each root if enabled
		if cfg.Workspace.AutoCreateIDERules {
			for _, root := range rootPaths {
				ensureIDERules(cfg, root)
			}
		}

		if cfg.Workspace.AutoIndex {
			go func() {
				// Wait a bit for server to settle
				time.Sleep(2 * time.Second)
				for _, root := range rootPaths {
					logger.Info("🔍 Triggering auto-indexing for registered root: %s", root)
					params := map[string]any{"workspace_root": root}
					_, err := indexWorkspaceTool.Execute(context.Background(), params)
					if err != nil {
						logger.Warn("Failed to auto-index root %s: %v", root, err)
					}
				}
			}()
		}
	})

	// Start server
	logger.Info("Starting RagCode MCP Server %s...", Version)
	if err := mcp.Serve(server); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func doUpdate() {
	fmt.Printf("Checking for updates for version %s...\n", Version)
	info, err := updater.CheckForUpdates(Version, true)
	if err != nil {
		fmt.Printf("Error checking for updates: %v\n", err)
		os.Exit(1)
	}

	if info == nil {
		fmt.Println("You are already on the latest version.")
		return
	}

	fmt.Printf("New version available: %s\n", info.LatestVersion)
	tempFile := filepath.Join(os.TempDir(), "ragcode-update.tar.gz")
	if strings.Contains(info.AssetURL, ".zip") {
		tempFile = filepath.Join(os.TempDir(), "ragcode-update.zip")
	}

	fmt.Printf("Downloading update from %s...\n", info.AssetURL)
	if err := info.DownloadAndVerify(tempFile); err != nil {
		fmt.Printf("Download failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Applying update...")
	if err := updater.ApplyUpdate(tempFile); err != nil {
		fmt.Printf("Update failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Update successful! Please restart the server.")
}

func applyCLIOverrides(cfg *config.Config) {
	if *ollamaModelFlag != "" {
		cfg.LLM.OllamaModel = *ollamaModelFlag
	}
	if *ollamaEmbedFlag != "" {
		cfg.LLM.OllamaEmbed = *ollamaEmbedFlag
	}
	if *ollamaBaseURLFlag != "" {
		cfg.LLM.OllamaBaseURL = *ollamaBaseURLFlag
	}
	if *qdrantURLFlag != "" {
		cfg.Storage.VectorDB.URL = *qdrantURLFlag
	}
	if *allowedPathsFlag != "" {
		paths := strings.Split(*allowedPathsFlag, ",")
		for i := range paths {
			paths[i] = strings.TrimSpace(paths[i])
		}
		cfg.Workspace.AllowedWorkspacePaths = paths
	}
	if *disableUpwardFlag {
		cfg.Workspace.DisableUpwardSearch = true
	}
	if !*autoCreateRulesFlag {
		cfg.Workspace.AutoCreateIDERules = false
	}
}

func registerAgentTool(server *mcp.Server, tool interface {
	Name() string
	Description() string
	Execute(context.Context, map[string]interface{}) (string, error)
}, cfg *config.Config) {
	server.AddTool(tool.Name(), tool.Description(), func(ctx context.Context, args map[string]interface{}) (string, error) {
		return tool.Execute(ctx, args)
	})
}

// typed registration for search_code to allow better IDE integration
func registerSearchCodeToolTyped(server *mcp.Server, tool *tools.SearchLocalIndexTool, cfg *config.Config) {
	server.AddTool(tool.Name(), tool.Description(), func(ctx context.Context, args struct {
		Query        string  `json:\"query\" mcp:\"The search query combining lexical and semantic matching\"`
		FilePath     string  `json:\"file_path,omitempty\" mcp:\"Optional: file path to help detect workspace context\"`
		Limit        int     `json:\"limit,omitempty\" mcp:\"Maximum number of results to return (default: 5)\"`
		OutputFormat string  `json:\"output_format,omitempty\" mcp:\"Optional: Output format - 'json' (default) or 'markdown'\"`
	}) (string, error) {
		params := map[string]interface{}{
			\"query\":         args.Query,
			\"file_path\":     args.FilePath,
			\"limit\":         args.Limit,
			\"output_format\": args.OutputFormat,
		}
		return tool.Execute(ctx, params)
	})
}

func registerFileResources(server *mcp.Server) error {
	// Example resource: server logs
	server.AddResource("logs://mcp", "Server Logs", "Textual logs generated by the MCP server", "text/plain", func(ctx context.Context) (string, error) {
		logFile := logger.GetLogFilePath()
		content, err := os.ReadFile(logFile)
		if err != nil {
			return "", err
		}
		return string(content), nil
	})
	return nil
}

func ensureIDERules(cfg *config.Config, workspaceRoot string) {
	// 1. Create .ragcode folder if not exists
	ragDir := filepath.Join(workspaceRoot, ".ragcode")
	os.MkdirAll(ragDir, 0755)

	// 2. Prepare rule content
	ruleContent := `
# RagCode MCP - Semantic Code Navigation Rules

## Usage Guidelines
- Always provide 'file_path' to tools to ensure they detect the correct project context.
- Use 'rag_hybrid_search' if looking for exact variable names or error messages.
- If the tool says "workspace not indexed", use 'rag_index_workspace' once.
- **Skills System**: Use 'list_skills' to see available AI behaviors and 'install_skill' to enable them in this workspace (e.g., 'ragcode-priority', 'ragcode-update').
`

	// 3. Define target rule files
	ruleFiles := []string{".cursorrules", ".clinerules", ".clauderules", ".roomodes", ".windsurfrules"}

	for _, fileName := range ruleFiles {
		filePath := filepath.Join(workspaceRoot, fileName)

		// Logic: If file doesn't exist, create it with our rules.
		// If it exists, append our rules only if they are not already present.
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			os.WriteFile(filePath, []byte(ruleContent), 0644)
			logger.Info("✅ Created rule file: %s", filePath)
		} else {
			// Check if already contains our marker
			current, _ := os.ReadFile(filePath)
			if !strings.Contains(string(current), "RagCode MCP") {
				f, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0644)
				if err == nil {
					f.WriteString("\n" + ruleContent)
					f.Close()
					logger.Info("📝 Updated rule file: %s", filePath)
				}
			}
		}
	}
}

var lastUpdateCheck time.Time

func triggerBackgroundUpdateCheck() {
	// Only check if more than 1 hour passed since last check in THIS session
	// to avoid spamming go-routines, while updater.CheckForUpdates handles the 24h logic
	if time.Since(lastUpdateCheck) < 1*time.Hour {
		return
	}
	lastUpdateCheck = time.Now()

	go func() {
		info, _ := updater.CheckForUpdates(Version, false)
		if info != nil {
			logger.Info("🌟 New version available: %s. Run 'apply_update' to upgrade.", info.LatestVersion)
		}
	}()
}

// extractFilePathsFromRoots converts MCP roots to a slice of absolute file paths
func extractFilePathsFromRoots(roots []*mcp.Root) []string {
	var rootPaths []string
	for _, r := range roots {
		u, err := url.Parse(r.URI)
		if err != nil {
			logger.Warn("Failed to parse workspace root URI %s: %v", r.URI, err)
			continue
		}

		if u.Scheme == "file" {
			path := u.Path
			// On Windows, the path might be /C:/path or /c:/path; if so, trim the leading slash.
			if len(path) > 2 && path[0] == '/' && path[2] == ':' &&
				((path[1] >= 'A' && path[1] <= 'Z') || (path[1] >= 'a' && path[1] <= 'z')) {
				path = path[1:]
			}
			rootPaths = append(rootPaths, path)
		} else {
			logger.Warn("Workspace root URI scheme %q is not supported: %s. Only 'file://' roots are registered automatically for local indexing.", u.Scheme, r.URI)
		}
	}
	return rootPaths
}
