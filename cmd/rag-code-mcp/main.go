package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/healthcheck"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
	"github.com/doITmagic/rag-code-mcp/internal/tools"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
	// Build trigger
)

// Simple logger using log level from env
type simpleLogger struct {
	logFile *os.File
}

func (l *simpleLogger) shouldLog(msgLevel string) bool {
	levels := map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}
	logLevel := strings.ToLower(os.Getenv("MCP_LOG_LEVEL"))
	if logLevel == "" {
		logLevel = "info"
	}
	return levels[msgLevel] >= levels[logLevel]
}

func (l *simpleLogger) Info(format string, args ...interface{}) {
	if l.shouldLog("info") {
		fmt.Fprintf(os.Stderr, "[INFO] "+format+"\n", args...)
		if l.logFile != nil {
			fmt.Fprintf(l.logFile, "[INFO] "+format+"\n", args...)
		}
	}
}

func (l *simpleLogger) Error(format string, args ...interface{}) {
	if l.shouldLog("error") {
		fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", args...)
		if l.logFile != nil {
			fmt.Fprintf(l.logFile, "[ERROR] "+format+"\n", args...)
		}
	}
}

func (l *simpleLogger) Warn(format string, args ...interface{}) {
	if l.shouldLog("warn") {
		fmt.Fprintf(os.Stderr, "[WARN] "+format+"\n", args...)
	}
}

var logger = &simpleLogger{}

func initLoggerFromEnv() {
	// Set default log output to stderr to avoid interfering with MCP stdio protocol
	log.SetOutput(os.Stderr)
	
	path := os.Getenv("MCP_LOG_FILE")
	if path == "" {
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to open log file %s: %v\n", path, err)
		return
	}

	logger.logFile = f
}

type MCPTool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}

// SearchCodeInput defines the typed input for the search_code tool.
type SearchCodeInput struct {
	Query    string `json:"query"`
	Limit    int    `json:"limit,omitempty"`
	FilePath string `json:"file_path,omitempty"`
}

// SearchCodeOutput defines the typed output for the search_code tool.
type SearchCodeOutput struct {
	Results string `json:"results"`
}

// ensureConfigExists creates a default config.yaml if it doesn't exist
func ensureConfigExists(configPath string) error {
	// Check if config file already exists
	if _, err := os.Stat(configPath); err == nil {
		return nil // File exists, nothing to do
	}

	log.Printf("📝 Config file not found, creating default configuration at: %s", configPath)

	// Create default config content
	defaultConfigYAML := `# RagCode MCP Server Configuration
# Auto-generated on first run

llm:
  provider: ollama
  ollama_base_url: http://localhost:11434
  ollama_model: phi3:medium
  ollama_embed: nomic-embed-text
  temperature: 0.7
  max_tokens: 1024
  timeout: 60s
  max_retries: 3

storage:
  vector_db:
    url: http://localhost:6333
    api_key: ""

# Multi-workspace configuration (auto-creates collections per workspace+language)
workspace:
  enabled: true
  auto_index: true
  max_workspaces: 10
  detection_markers:
    - .git
    - go.mod
    - package.json
    - Cargo.toml
    - pyproject.toml
    - setup.py
    - requirements.txt
    - composer.json
    - pom.xml
    - build.gradle
    - Gemfile
    - Package.swift
  exclude_patterns:
    - node_modules
    - .git
    - vendor
    - target
    - build
    - dist
    - .venv
  collection_prefix: ragcode
  index_include: []
  index_exclude: []
`

	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
	}

	// Write config file
	if err := os.WriteFile(configPath, []byte(defaultConfigYAML), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	log.Printf("✓ Created default configuration file: %s", configPath)
	log.Printf("  You can edit this file to customize your settings")

	return nil
}

func main() {
	// Define flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	ollamaBaseURLFlag := flag.String("ollama-base-url", "", "Ollama base URL (overrides config/env)")
	ollamaModelFlag := flag.String("ollama-model", "", "Ollama chat model (overrides config/env)")
	ollamaEmbedFlag := flag.String("ollama-embed", "", "Ollama embedding model (overrides config/env)")
	qdrantURLFlag := flag.String("qdrant-url", "", "Qdrant URL (overrides config/env)")
	versionFlag := flag.Bool("version", false, "Print version information and exit")
	healthFlag := flag.Bool("health", false, "Run health check and exit")

	// Custom usage message
	flag.Usage = printUsage

	flag.Parse()

	initLoggerFromEnv()

	// Handle version flag
	if *versionFlag {
		fmt.Printf("RagCode MCP Server\n")
		fmt.Printf("Version:    %s\n", Version)
		fmt.Printf("Commit:     %s\n", Commit)
		fmt.Printf("Build Date: %s\n", Date)
		os.Exit(0)
	}

	// Auto-create config.yaml if it doesn't exist
	if err := ensureConfigExists(*configPath); err != nil {
		logger.Warn("Failed to create default config: %v", err)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Warn("Failed to load config file %s, using defaults: %v", *configPath, err)
		cfg = config.DefaultConfig()
	}

	// Apply CLI overrides (highest precedence)
	if *ollamaBaseURLFlag != "" {
		cfg.LLM.OllamaBaseURL = *ollamaBaseURLFlag
	}
	if *ollamaModelFlag != "" {
		cfg.LLM.OllamaModel = *ollamaModelFlag
	}
	if *ollamaEmbedFlag != "" {
		cfg.LLM.OllamaEmbed = *ollamaEmbedFlag
	}
	if *qdrantURLFlag != "" {
		cfg.Storage.VectorDB.URL = *qdrantURLFlag
	}

	// Set defaults
	if cfg.LLM.OllamaBaseURL == "" {
		cfg.LLM.OllamaBaseURL = "http://localhost:11434"
	}
	if cfg.Storage.VectorDB.URL == "" {
		cfg.Storage.VectorDB.URL = "http://localhost:6333"
	}

	// Handle health check flag
	if *healthFlag {
		results := healthcheck.CheckAll(cfg.LLM.OllamaBaseURL, cfg.Storage.VectorDB.URL)
		fmt.Fprint(os.Stderr, healthcheck.FormatResults(results))

		allHealthy := true
		for _, result := range results {
			if result.Status != "ok" {
				allHealthy = false
				break
			}
		}

		if !allHealthy {
			fmt.Fprintln(os.Stderr, healthcheck.GetRemediation(results))
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Run health check on startup (non-fatal)
	logger.Info("Checking dependencies...")
	results := healthcheck.CheckAll(cfg.LLM.OllamaBaseURL, cfg.Storage.VectorDB.URL)

	hasErrors := false
	for _, result := range results {
		if result.Status == "ok" {
			logger.Info("✓ %s: %s", result.Service, result.Message)
		} else {
			logger.Error("✗ %s: %s", result.Service, result.Message)
			hasErrors = true
		}
	}

	if hasErrors {
		fmt.Fprintln(os.Stderr, healthcheck.GetRemediation(results))
		log.Fatal("Dependency check failed. Please fix the issues above and try again.")
	}

	embeddingModel := "nomic-embed-text"
	if cfg.LLM.OllamaEmbed != "" {
		embeddingModel = cfg.LLM.OllamaEmbed
	}

	llmCfg := cfg.LLM
	if llmCfg.OllamaBaseURL == "" {
		llmCfg.OllamaBaseURL = "http://localhost:11434"
	}
	llmCfg.OllamaEmbed = embeddingModel
	llmCfg.Provider = "ollama"

	ollamaProvider, err := llm.NewOllamaLLMProvider(llmCfg)
	if err != nil {
		log.Fatalf("Failed to create Ollama provider: %v", err)
	}

	// Create base Qdrant config (no collection - multi-workspace manages collections)
	qcfg := storage.QdrantConfig{
		URL:    cfg.Storage.VectorDB.URL,
		APIKey: cfg.Storage.VectorDB.APIKey,
	}

	// Create WorkspaceManager for multi-workspace support
	qdrantClientForWorkspace, err := storage.NewQdrantClient(qcfg)
	if err != nil {
		log.Fatalf("Failed to create Qdrant client for workspace manager: %v", err)
	}
	defer qdrantClientForWorkspace.Close()

	workspaceManager := workspace.NewManager(
		qdrantClientForWorkspace,
		ollamaProvider,
		cfg,
	)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ragcode",
		Version: "1.0.0",
	}, nil)

	// All tools use workspace manager - no single collections
	searchTool := tools.NewSearchLocalIndexTool(nil, ollamaProvider)
	searchTool.SetWorkspaceManager(workspaceManager)

	getFunctionTool := tools.NewGetFunctionDetailsTool(nil, ollamaProvider)
	getFunctionTool.SetWorkspaceManager(workspaceManager)

	findTypeTool := tools.NewFindTypeDefinitionTool(nil, ollamaProvider)
	findTypeTool.SetWorkspaceManager(workspaceManager)

	getContextTool := tools.NewGetCodeContextTool()
	// getContextTool doesn't need workspace manager (reads files directly)

	listExportsTool := tools.NewListPackageExportsTool(nil, ollamaProvider)
	listExportsTool.SetWorkspaceManager(workspaceManager)

	findImplTool := tools.NewFindImplementationsTool(nil, ollamaProvider)
	findImplTool.SetWorkspaceManager(workspaceManager)

	hybridTool := tools.NewHybridSearchTool(nil, ollamaProvider)
	hybridTool.SetWorkspaceManager(workspaceManager)

	searchDocsTool := tools.NewSearchDocsTool(nil, ollamaProvider)
	searchDocsTool.SetWorkspaceManager(workspaceManager)

	indexWorkspaceTool := tools.NewIndexWorkspaceTool(workspaceManager)

	// Example: use typed ToolHandlerFor for search_code
	registerSearchCodeToolTyped(server, searchTool)

	// Other tools still use the generic MCPTool handler
	registerAgentTool(server, getFunctionTool)
	registerAgentTool(server, findTypeTool)
	registerAgentTool(server, getContextTool)
	registerAgentTool(server, listExportsTool)
	registerAgentTool(server, findImplTool)
	registerAgentTool(server, searchDocsTool)
	registerAgentTool(server, hybridTool)
	registerAgentTool(server, indexWorkspaceTool)

	if err := registerFileResources(server); err != nil {
		log.Fatalf("Failed to register resources: %v", err)
	}

	logger.Info("MCP RagCode Server started (stdio mode) - Multi-workspace enabled")
	logger.Info("Embedding Model: %s", embeddingModel)
	logger.Info("Workspaces: auto-detected, collections created per workspace+language")

	// Use a context that cancels on OS signals for graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server terminated: %v", err)
	}
}

// registerSearchCodeToolTyped registers the search_code tool using the typed
// ToolHandlerFor API from the MCP Go SDK.
func registerSearchCodeToolTyped(server *mcp.Server, tool *tools.SearchLocalIndexTool) {
	mcp.AddTool[SearchCodeInput, SearchCodeOutput](server, &mcp.Tool{
		Name:        tool.Name(),
		Description: tool.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchCodeInput) (*mcp.CallToolResult, SearchCodeOutput, error) {
		args := map[string]interface{}{
			"query": input.Query,
		}
		if input.Limit > 0 {
			args["limit"] = input.Limit
		}
		if input.FilePath != "" {
			args["file_path"] = input.FilePath
		}

		result, err := tool.Execute(ctx, args)
		if err != nil {
			return nil, SearchCodeOutput{}, err
		}

		return nil, SearchCodeOutput{Results: result}, nil
	})
}

func registerAgentTool(server *mcp.Server, tool MCPTool) {
	schema := getToolSchema(tool.Name())
	server.AddTool(&mcp.Tool{
		Name:        tool.Name(),
		Description: tool.Description(),
		InputSchema: schema,
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]interface{}{}
		if req.Params != nil && req.Params.Arguments != nil {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
		}
		result, err := tool.Execute(ctx, args)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				},
			}, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: result},
			},
		}, nil
	})
}

func registerFileResources(server *mcp.Server) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	for _, res := range buildDefaultResources(cwd) {
		resource := res
		handler := func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			data, err := os.ReadFile(resource.path)
			if err != nil {
				if os.IsNotExist(err) {
					return nil, mcp.ResourceNotFoundError(req.Params.URI)
				}
				return nil, err
			}
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{
						URI:      resource.URI,
						MIMEType: resource.MIMEType,
						Text:     string(data),
					},
				},
			}, nil
		}
		server.AddResource(&mcp.Resource{
			URI:         resource.URI,
			Name:        resource.Name,
			Title:       resource.Title,
			Description: resource.Description,
			MIMEType:    resource.MIMEType,
		}, handler)
	}
	return nil
}

type fileResourceInfo struct {
	URI         string
	Name        string
	Title       string
	Description string
	MIMEType    string
	path        string
}

func buildDefaultResources(baseDir string) []fileResourceInfo {
	var resources []fileResourceInfo

	// Directories to exclude from scanning
	excludeDirs := map[string]bool{
		"vendor":       true,
		"node_modules": true,
		".git":         true,
		"bin":          true,
		"qdrant_data":  true,
		"docs-backup":  true,
		".venv":        true,
		"__pycache__":  true,
	}

	// Walk the directory tree to find all .md and .yaml files
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files with errors
		}

		// Skip directories that should be excluded
		if info.IsDir() {
			relPath, _ := filepath.Rel(baseDir, path)
			for _, part := range strings.Split(relPath, string(filepath.Separator)) {
				if excludeDirs[part] {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Check if file has a supported extension
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".yaml" && ext != ".yml" {
			return nil
		}

		// Determine MIME type based on extension
		mimeType := "text/markdown"
		if ext == ".yaml" || ext == ".yml" {
			mimeType = "application/x-yaml"
		}

		// Generate resource name from relative path
		relPath, _ := filepath.Rel(baseDir, path)
		name := strings.ReplaceAll(relPath, string(filepath.Separator), "-")
		name = strings.TrimSuffix(name, filepath.Ext(name))

		// Generate title from filename
		baseName := filepath.Base(path)
		title := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		title = strings.ReplaceAll(title, "-", " ")
		title = strings.ReplaceAll(title, "_", " ")
		// Capitalize first letter of each word
		words := strings.Fields(title)
		for i, word := range words {
			if len(word) > 0 {
				words[i] = strings.ToUpper(word[:1]) + word[1:]
			}
		}
		title = strings.Join(words, " ")

		// Generate description based on file type
		description := "Project documentation"
		if ext == ".yaml" || ext == ".yml" {
			description = "Configuration file"
		} else if strings.Contains(strings.ToLower(baseName), "readme") {
			description = "README documentation"
		} else if strings.Contains(strings.ToLower(relPath), "docs") {
			description = "Technical documentation"
		}

		resources = append(resources, fileResourceInfo{
			URI:         fileURI(path),
			Name:        name,
			Title:       title,
			Description: description,
			MIMEType:    mimeType,
			path:        path,
		})

		return nil
	})

	if err != nil {
		logger.Warn("Error scanning for resources: %v", err)
	}

	return resources
}

func fileURI(absPath string) string {
	// Ensure we produce a properly escaped file:// URI
	path := filepath.ToSlash(absPath)
	if !filepath.IsAbs(absPath) {
		abs := filepath.Join("/", path)
		path = filepath.ToSlash(abs)
	}
	u := &url.URL{Scheme: "file", Path: path}
	return u.String()
}

func getToolSchema(toolName string) map[string]interface{} {
	switch toolName {
	case "search_code":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The search query to find relevant code",
				},
				"file_path": map[string]interface{}{
					"type":        "string",
					"description": "Optional: file path to help detect workspace context",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Maximum number of results to return (default: 5)",
				},
			},
			"required": []string{"query"},
		}

	case "get_function_details":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"function_name": map[string]interface{}{
					"type":        "string",
					"description": "The name of the function or method to look up",
				},
				"file_path": map[string]interface{}{
					"type":        "string",
					"description": "Optional: file path to help detect workspace context",
				},
				"package": map[string]interface{}{
					"type":        "string",
					"description": "Optional: filter by package path (e.g., 'internal/agents')",
				},
			},
			"required": []string{"function_name"},
		}

	case "find_type_definition":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"type_name": map[string]interface{}{
					"type":        "string",
					"description": "The name of the type (struct or interface) to look up",
				},
				"file_path": map[string]interface{}{
					"type":        "string",
					"description": "Optional: file path to help detect workspace context",
				},
				"package": map[string]interface{}{
					"type":        "string",
					"description": "Optional: filter by package path (e.g., 'internal/ragcode')",
				},
			},
			"required": []string{"type_name"},
		}

	case "get_code_context":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"file_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the source file (relative or absolute)",
				},
				"start_line": map[string]interface{}{
					"type":        "number",
					"description": "Starting line number (1-indexed)",
				},
				"end_line": map[string]interface{}{
					"type":        "number",
					"description": "Ending line number (1-indexed)",
				},
				"context_lines": map[string]interface{}{
					"type":        "number",
					"description": "Number of context lines to show before/after (default: 5)",
				},
			},
			"required": []string{"file_path", "start_line", "end_line"},
		}

	case "list_package_exports":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"package": map[string]interface{}{
					"type":        "string",
					"description": "The package path to list exports from (e.g., 'internal/agents', 'ragcode')",
				},
				"file_path": map[string]interface{}{
					"type":        "string",
					"description": "Optional: file path to help detect workspace context",
				},
				"symbol_type": map[string]interface{}{
					"type":        "string",
					"description": "Optional: filter by symbol type (function, method, type, const, var)",
				},
			},
			"required": []string{"package"},
		}

	case "find_implementations":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"symbol_name": map[string]interface{}{
					"type":        "string",
					"description": "The name of the function, method, or interface to find usages of",
				},
				"file_path": map[string]interface{}{
					"type":        "string",
					"description": "Optional: file path to help detect workspace context",
				},
				"package": map[string]interface{}{
					"type":        "string",
					"description": "Optional: filter results by package path",
				},
			},
			"required": []string{"symbol_name"},
		}

	case "search_docs":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The search query to find relevant documentation",
				},
				"file_path": map[string]interface{}{
					"type":        "string",
					"description": "Optional: file path to help detect workspace context",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Maximum number of results to return (default: 5)",
				},
			},
			"required": []string{"query"},
		}

	case "index_workspace":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"file_path": map[string]interface{}{
					"type":        "string",
					"description": "A file path within the workspace to index (used to detect workspace root)",
				},
				"language": map[string]interface{}{
					"type":        "string",
					"description": "Optional: specific language to index (e.g., 'go', 'python', 'php'). If not provided, all detected languages will be indexed.",
				},
			},
			"required": []string{"file_path"},
		}

	default:
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `RagCode MCP Server - Semantic code navigation for Go codebases

USAGE:
    rag-code-mcp [OPTIONS]

EXAMPLES:
    # Start with default configuration
    rag-code-mcp

    # Use custom config file
    rag-code-mcp -config my-config.yaml

    # Override Ollama and Qdrant URLs
    rag-code-mcp -ollama-base-url http://remote:11434 -qdrant-url http://remote:6333

    # Check version
    rag-code-mcp -version

    # Run health check only
    rag-code-mcp -health

OPTIONS:
`)
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
CONFIGURATION PRECEDENCE:
    CLI flags > Environment variables > config.yaml > defaults

ENVIRONMENT VARIABLES:
    OLLAMA_BASE_URL              Ollama server URL (default: http://localhost:11434)
    OLLAMA_MODEL                 Chat model name (default: phi3:medium)
    OLLAMA_EMBED                 Embedding model name (default: nomic-embed-text)
    QDRANT_URL                   Qdrant server URL (default: http://localhost:6333)
    QDRANT_COLLECTION            Collection name for code index (legacy mode only)
    QDRANT_API_KEY               Qdrant API key (optional)

    Multi-Workspace Mode (Recommended):
    WORKSPACE_COLLECTION_PREFIX  Prefix for auto-generated collections (default: ragcode)
    WORKSPACE_ENABLED            Enable multi-workspace mode (default: true)
    WORKSPACE_AUTO_INDEX         Auto-index when workspace detected (default: true)
    WORKSPACE_MAX_WORKSPACES     Max concurrent workspace indexing (default: 10)

    Documentation (Optional):
    DOCS_COLLECTION              Qdrant collection for markdown docs (default: do-ai-docs)
    API_DOCS_COLLECTION          Qdrant collection for API docs (default: do-ai-api-docs)

    Logging:
    MCP_LOG_LEVEL                Log level: debug, info, warn, error (default: info)

For more information, visit: https://github.com/doITmagic/rag-code-mcp
`)
}
