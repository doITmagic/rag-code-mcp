package config

import (
	"time"
)

// Config represents the global application configuration
type Config struct {
	// LLM configuration
	LLM LLMConfig `yaml:"llm"`

	// Memory configuration
	Memory MemoryConfig `yaml:"memory"`

	// Storage configuration
	Storage StorageConfig `yaml:"storage"`

	// Server configuration
	Server ServerConfig `yaml:"server"`

	// Logging configuration
	Logging LoggingConfig `yaml:"logging"`

	// CodeRAG configuration
	CodeRAG CodeRAGConfig `yaml:"code_rag"`

	// Docs configuration (Markdown documentation indexing)
	Docs DocsConfig `yaml:"docs"`

	// APIDocs configuration (legacy optional extra collection). There is no
	// dedicated APIChunk/APIIndexer pipeline anymore; this collection can be
	// used as an additional LongTermMemory for pre-indexed structured content.
	APIDocs APIDocsConfig `yaml:"api_docs"`

	// Workspace configuration (multi-workspace support)
	Workspace WorkspaceConfig `yaml:"workspace"`
}

// LLMConfig contains LLM provider settings
type LLMConfig struct {
	// Provider type: "ollama" (local Ollama), "llamafile" (local GGUF), "huggingface" (HF API)
	Provider string `yaml:"provider"`

	// Embedding provider: if set, use different provider for embeddings
	// Options: "ollama", "llamafile", "huggingface", "" (empty = use same as Provider)
	EmbedProvider string `yaml:"embed_provider"`

	// Ollama settings
	OllamaBaseURL string `yaml:"ollama_base_url"` // Default: http://localhost:11434
	OllamaModel   string `yaml:"ollama_model"`    // e.g., phi3:medium, granite3.1-dense:8b
	OllamaEmbed   string `yaml:"ollama_embed"`    // e.g., nomic-embed-text

	// Llamafile settings (local GGUF models via llama.cpp server)
	LlamafileBaseURL string `yaml:"llamafile_base_url"` // Default: http://localhost:8080
	LlamafileModel   string `yaml:"llamafile_model"`    // Model name or path
	LlamafileEmbed   string `yaml:"llamafile_embed"`    // Embedding model

	// HuggingFace settings (cloud API)
	HuggingFaceAPIKey     string `yaml:"huggingface_api_key"`     // Optional: HF API token
	HuggingFaceModel      string `yaml:"huggingface_model"`       // e.g., gpt2, zephyr-7b-beta
	HuggingFaceEmbedModel string `yaml:"huggingface_embed_model"` // e.g., sentence-transformers/all-MiniLM-L6-v2
	HuggingFaceProvider   string `yaml:"huggingface_provider"`    // Optional: inference provider (e.g., hyperbolic)

	// Global settings (apply to all providers)
	FallbackProvider string        `yaml:"fallback_provider"` // Fallback provider if primary fails
	Temperature      float64       `yaml:"temperature"`
	MaxTokens        int           `yaml:"max_tokens"`
	Timeout          time.Duration `yaml:"timeout"`
	MaxRetries       int           `yaml:"max_retries"`

	// Deprecated (kept for backward compatibility)
	BaseURL    string `yaml:"base_url"`    // Legacy: use OllamaBaseURL
	APIKey     string `yaml:"api_key"`     // Legacy: use HuggingFaceAPIKey
	Model      string `yaml:"model"`       // Legacy: use provider-specific model
	EmbedModel string `yaml:"embed_model"` // Legacy: use provider-specific embed model
}

// MemoryConfig contains memory engine settings
type MemoryConfig struct {
	ShortTermSize  int  `yaml:"short_term_size"`
	EnableLongTerm bool `yaml:"enable_long_term"`
}

// StorageConfig contains storage backend settings
type StorageConfig struct {
	VectorDB VectorDBConfig `yaml:"vector_db"`
	Redis    RedisConfig    `yaml:"redis"`
	SQLite   SQLiteConfig   `yaml:"sqlite"`
}

// VectorDBConfig contains vector database settings
type VectorDBConfig struct {
	Provider   string `yaml:"provider"` // qdrant, chromadb
	URL        string `yaml:"url"`
	APIKey     string `yaml:"api_key"`
	Collection string `yaml:"collection"`
}

// RedisConfig contains Redis settings
type RedisConfig struct {
	Enabled  bool   `yaml:"enabled"`
	URL      string `yaml:"url"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// SQLiteConfig contains SQLite settings
type SQLiteConfig struct {
	Path string `yaml:"path"`
}

// ServerConfig contains HTTP server settings
type ServerConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	EnableWebSocket bool   `yaml:"enable_websocket"`
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // json, text
	Output string `yaml:"output"` // stdout, file
	Path   string `yaml:"path"`
}

// CodeRAGConfig contains configuration for codebase indexing at startup
type CodeRAGConfig struct {
	Enabled        bool     `yaml:"enabled"`          // enable Code RAG features
	IndexOnStartup bool     `yaml:"index_on_startup"` // run indexer when server starts
	Paths          []string `yaml:"paths"`            // directories to index
	Collection     string   `yaml:"collection"`       // Qdrant collection for code index
	Model          string   `yaml:"model"`            // optional: embedding model override
	Include        []string `yaml:"include"`          // glob include patterns
	Exclude        []string `yaml:"exclude"`          // glob exclude patterns
}

// DocsConfig contains configuration for Markdown documentation indexing
type DocsConfig struct {
	// Qdrant collection for docs index (e.g., do-ai-docs)
	Collection string `yaml:"collection"`

	// Root-level README and docs directory paths
	ReadmePath string   `yaml:"readme_path"`
	DocsPaths  []string `yaml:"docs_paths"`
}

// APIDocsConfig contains configuration for API documentation indexing
type APIDocsConfig struct {
	// Qdrant collection for API docs index (e.g., do-ai-api-docs)
	Collection string `yaml:"collection"`
}

// WorkspaceConfig contains configuration for multi-workspace support
type WorkspaceConfig struct {
	// Enabled controls whether multi-workspace mode is active
	// When true, collections are created per-workspace automatically
	// When false, uses traditional single-collection mode
	Enabled bool `yaml:"enabled"`

	// AutoIndex controls whether indexing is triggered automatically
	// when a new workspace is detected
	AutoIndex bool `yaml:"auto_index"`

	// MaxWorkspaces limits the number of workspaces that can be indexed
	// Set to 0 for unlimited (default: 10)
	MaxWorkspaces int `yaml:"max_workspaces"`

	// DetectionMarkers are files/directories used to identify workspace roots
	// Default: [".git", "go.mod", "package.json", "Cargo.toml", "pyproject.toml"]
	DetectionMarkers []string `yaml:"detection_markers"`

	// ExcludePatterns are glob patterns for paths to exclude from workspace detection
	// Default: ["node_modules", ".git", "vendor", "target"]
	ExcludePatterns []string `yaml:"exclude_patterns"`

	// CollectionPrefix is prepended to all workspace collection names
	// Format: {prefix}-{workspaceID}
	// Default: "coderag"
	CollectionPrefix string `yaml:"collection_prefix"`

	// IndexPatterns override code_rag include/exclude patterns per workspace
	// If empty, uses global code_rag patterns
	IndexInclude []string `yaml:"index_include"`
	IndexExclude []string `yaml:"index_exclude"`
}
