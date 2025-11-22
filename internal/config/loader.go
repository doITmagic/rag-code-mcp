package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Load reads and parses the configuration file
func Load(path string) (*Config, error) {
	// Read configuration file
	data, err := os.ReadFile(path)
	if err != nil {
		// Return default config if file doesn't exist
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Apply environment variable overrides
	applyEnvOverrides(&cfg)

	// Validate configuration
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		LLM: LLMConfig{
			Provider:         "ollama",
			OllamaBaseURL:    "http://localhost:11434",
			OllamaModel:      "llama3",
			OllamaEmbed:      "nomic-embed-text",
			LlamafileBaseURL: "http://localhost:8080",
			Temperature:      0.7,
			MaxTokens:        2048,
			Timeout:          60 * time.Second,
			MaxRetries:       3,
			// Legacy fields for backward compatibility
			BaseURL:    "http://localhost:11434",
			Model:      "llama3",
			EmbedModel: "nomic-embed-text",
		},
		Memory: MemoryConfig{
			ShortTermSize:  10,
			EnableLongTerm: false,
		},
		Storage: StorageConfig{
			VectorDB: VectorDBConfig{
				Provider:   "qdrant",
				URL:        "http://localhost:6333",
				Collection: "do-ai",
			},
			Redis: RedisConfig{
				Enabled: false,
				URL:     "localhost:6379",
				DB:      0,
			},
			SQLite: SQLiteConfig{
				Path: "./data/do-ai.db",
			},
		},
		Server: ServerConfig{
			Host:            "0.0.0.0",
			Port:            8080,
			EnableWebSocket: true,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
			Output: "stdout",
		},
		CodeRAG: CodeRAGConfig{
			Enabled:        false,
			IndexOnStartup: false,
			Paths:          []string{"./internal", "./cmd"},
			Collection:     "do-ai-code",
			Model:          "",
			Include:        []string{"**/*.go"},
			Exclude:        []string{"**/*_test.go", "vendor/**", ".git/**", "testdata/**"},
		},
		Docs: DocsConfig{
			Collection: "do-ai-docs",
			ReadmePath: "./README.md",
			DocsPaths:  []string{"./docs"},
		},
		APIDocs: APIDocsConfig{
			Collection: "do-ai-api-docs",
		},
		Workspace: WorkspaceConfig{
			Enabled:          true,
			AutoIndex:        true,
			MaxWorkspaces:    10,
			DetectionMarkers: []string{".git", "go.mod", "package.json", "Cargo.toml", "pyproject.toml", "pom.xml"},
			ExcludePatterns:  []string{"node_modules", ".git", "vendor", "target", "build", "dist", ".venv"},
			CollectionPrefix: "coderag",
			IndexInclude:     []string{}, // Empty means use global code_rag.include
			IndexExclude:     []string{}, // Empty means use global code_rag.exclude
		},
	}
}

// applyEnvOverrides applies environment variable overrides to the configuration
func applyEnvOverrides(cfg *Config) {
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		cfg.LLM.APIKey = apiKey
	}
	if baseURL := os.Getenv("LLM_BASE_URL"); baseURL != "" {
		cfg.LLM.BaseURL = baseURL
	}

	// Docs configuration overrides
	if docsColl := os.Getenv("DOCS_COLLECTION"); docsColl != "" {
		cfg.Docs.Collection = docsColl
	}
	if readmePath := os.Getenv("DOCS_README_PATH"); readmePath != "" {
		cfg.Docs.ReadmePath = readmePath
	}
	if docsPaths := os.Getenv("DOCS_PATHS"); docsPaths != "" {
		parts := strings.Split(docsPaths, ",")
		cfg.Docs.DocsPaths = cfg.Docs.DocsPaths[:0]
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				cfg.Docs.DocsPaths = append(cfg.Docs.DocsPaths, p)
			}
		}
	}

	if apiColl := os.Getenv("API_DOCS_COLLECTION"); apiColl != "" {
		cfg.APIDocs.Collection = apiColl
	}

	// LLM configuration overrides
	if provider := os.Getenv("LLM_PROVIDER"); provider != "" {
		cfg.LLM.Provider = provider
	}
	if baseURL := os.Getenv("OLLAMA_BASE_URL"); baseURL != "" {
		cfg.LLM.OllamaBaseURL = baseURL
	}
	if model := os.Getenv("OLLAMA_MODEL"); model != "" {
		cfg.LLM.OllamaModel = model
	}
	if embed := os.Getenv("OLLAMA_EMBED"); embed != "" {
		cfg.LLM.OllamaEmbed = embed
	}

	// Vector DB (Qdrant) configuration overrides
	if url := os.Getenv("QDRANT_URL"); url != "" {
		cfg.Storage.VectorDB.URL = url
	}
	if apiKey := os.Getenv("QDRANT_API_KEY"); apiKey != "" {
		cfg.Storage.VectorDB.APIKey = apiKey
	}
	if coll := os.Getenv("QDRANT_COLLECTION"); coll != "" {
		cfg.Storage.VectorDB.Collection = coll
	}

	// CodeRAG configuration overrides
	if codeColl := os.Getenv("CODE_RAG_COLLECTION"); codeColl != "" {
		cfg.CodeRAG.Collection = codeColl
	}
	if codeModel := os.Getenv("CODE_RAG_MODEL"); codeModel != "" {
		cfg.CodeRAG.Model = codeModel
	}
	if enabled := os.Getenv("CODE_RAG_ENABLED"); enabled != "" {
		if v, err := strconv.ParseBool(enabled); err == nil {
			cfg.CodeRAG.Enabled = v
		}
	}
	if indexOnStartup := os.Getenv("CODE_RAG_INDEX_ON_STARTUP"); indexOnStartup != "" {
		if v, err := strconv.ParseBool(indexOnStartup); err == nil {
			cfg.CodeRAG.IndexOnStartup = v
		}
	}

	// Workspace configuration overrides
	if wsEnabled := os.Getenv("WORKSPACE_ENABLED"); wsEnabled != "" {
		if v, err := strconv.ParseBool(wsEnabled); err == nil {
			cfg.Workspace.Enabled = v
		}
	}
	if wsAutoIndex := os.Getenv("WORKSPACE_AUTO_INDEX"); wsAutoIndex != "" {
		if v, err := strconv.ParseBool(wsAutoIndex); err == nil {
			cfg.Workspace.AutoIndex = v
		}
	}
	if wsMax := os.Getenv("WORKSPACE_MAX_WORKSPACES"); wsMax != "" {
		if v, err := strconv.Atoi(wsMax); err == nil {
			cfg.Workspace.MaxWorkspaces = v
		}
	}
	if wsPrefix := os.Getenv("WORKSPACE_COLLECTION_PREFIX"); wsPrefix != "" {
		cfg.Workspace.CollectionPrefix = wsPrefix
	}
}

// validate checks if the configuration is valid
func validate(cfg *Config) error {
	// Default to ollama if provider is not set
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = "ollama"
	}

	// Only ollama is supported in this version of the project
	if cfg.LLM.Provider != "ollama" {
		return fmt.Errorf("llm.provider must be 'ollama'")
	}

	// Validate ollama model configuration
	if cfg.LLM.OllamaModel == "" && cfg.LLM.Model == "" {
		return fmt.Errorf("llm.ollama_model (or legacy llm.model) is required for ollama provider")
	}

	return nil
}
