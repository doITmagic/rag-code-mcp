package config

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultConfigBytes []byte

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

	// Auto-migrate file-based config BEFORE env overrides to see if we should save
	if migrateEmbeddingModel(&cfg) {
		log.Printf("🔄 Auto-migrating configuration file '%s' to stable embedding models...", path)
		if err := Save(path, &cfg); err != nil {
			log.Printf("⚠️  Failed to persist migrated configuration to '%s': %v", path, err)
		} else {
			log.Printf("✅ Configuration file successfully updated.")
		}
	}

	// Apply environment variable overrides
	applyEnvOverrides(&cfg)

	// Auto-migrate again after env overrides (in memory) to ensure any deprecated models
	// from environment variables are also updated
	migrateEmbeddingModel(&cfg)

	// Validate configuration
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// Save writes the configuration to a file
func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// DefaultConfig returns a default configuration structure
func DefaultConfig() *Config {
	// Parse the embedded default YAML to ensure struct defaults match file defaults
	var cfg Config
	if err := yaml.Unmarshal(defaultConfigBytes, &cfg); err != nil {
		// Fallback to hardcoded defaults if parsing fails (should not happen)
		log.Printf("[WARN] Failed to parse embedded default config: %v", err)
		return &Config{
			LLM: LLMConfig{
				Provider:      "ollama",
				OllamaBaseURL: "http://localhost:11434",
				OllamaModel:   "phi3:medium",
				OllamaEmbed:   "mxbai-embed-large",
				MaxTokens:     1024,
				Timeout:       60 * time.Second,
			},
			Storage: StorageConfig{
				VectorDB: VectorDBConfig{URL: "http://localhost:6333"},
			},
			Logging: LoggingConfig{Level: "info"},
		}
	}
	return &cfg
}

// EnsureConfigExists creates a default config.yaml if it doesn't exist
func EnsureConfigExists(configPath string) error {
	// Check if config file already exists
	if _, err := os.Stat(configPath); err == nil {
		return nil // File exists, nothing to do
	}

	log.Printf("📝 Config file not found, creating default configuration at: %s", configPath)

	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
	}

	// Write embedded config content
	if err := os.WriteFile(configPath, defaultConfigBytes, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	log.Printf("✓ Created default configuration file: %s", configPath)
	return nil
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

	// RagCode configuration overrides
	if codeColl := os.Getenv("CODE_RAG_COLLECTION"); codeColl != "" {
		cfg.RagCode.Collection = codeColl
	}
	if codeModel := os.Getenv("CODE_RAG_MODEL"); codeModel != "" {
		cfg.RagCode.Model = codeModel
	}
	if enabled := os.Getenv("CODE_RAG_ENABLED"); enabled != "" {
		if v, err := strconv.ParseBool(enabled); err == nil {
			cfg.RagCode.Enabled = v
		}
	}
	if indexOnStartup := os.Getenv("CODE_RAG_INDEX_ON_STARTUP"); indexOnStartup != "" {
		if v, err := strconv.ParseBool(indexOnStartup); err == nil {
			cfg.RagCode.IndexOnStartup = v
		}
	}
	if searchLimit := os.Getenv("SEARCH_LIMIT"); searchLimit != "" {
		if v, err := strconv.Atoi(searchLimit); err == nil {
			cfg.RagCode.SearchLimit = v
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
	if wsIDERules := os.Getenv("WORKSPACE_AUTO_CREATE_IDE_RULES"); wsIDERules != "" {
		if v, err := strconv.ParseBool(wsIDERules); err == nil {
			cfg.Workspace.AutoCreateIDERules = v
		}
	}

	// HealthCheck configuration overrides
	if healthOnStartup := os.Getenv("HEALTH_CHECK_ON_STARTUP"); healthOnStartup != "" {
		if v, err := strconv.ParseBool(healthOnStartup); err == nil {
			cfg.HealthCheck.EnableOnStartup = v
		}
	}
}

// migrateEmbeddingModel automatically migrates from old unstable embedding model
func migrateEmbeddingModel(cfg *Config) bool {
	migrated := false

	// List of deprecated/unstable models that should be migrated
	deprecatedModels := []string{"nomic-embed-text"}
	newStableModel := "mxbai-embed-large"

	// Check if current model is deprecated
	for _, deprecated := range deprecatedModels {
		if cfg.LLM.OllamaEmbed == deprecated {
			log.Printf("⚠️  MIGRATION: Detected deprecated embedding model '%s'", deprecated)
			log.Printf("   Automatically upgrading to stable model '%s'", newStableModel)
			log.Printf("   Note: Existing indexed data will need to be re-indexed.")
			log.Printf("   Use 'rag_index_workspace' tool with 'recreate: true' to rebuild indexes.")

			cfg.LLM.OllamaEmbed = newStableModel
			migrated = true
			break
		}

		// Also check legacy field
		if cfg.LLM.EmbedModel == deprecated {
			cfg.LLM.EmbedModel = newStableModel
			migrated = true
		}
	}

	return migrated
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

	// Ensure log max size
	if cfg.Logging.MaxSizeMB <= 0 {
		cfg.Logging.MaxSizeMB = 10
	}

	return nil
}
