# Config Package

The `config` package handles the application's configuration lifecycle, including loading settings from YAML files, applying environment variable overrides, and providing default values for a stable "out-of-the-box" experience.

## Features

- **YAML Support**: Loads complex configurations from a `config.yaml` file.
- **Environment Overrides**: Automatically maps environment variables (e.g., `RAG_LLM_PROVIDER`) to config fields.
- **Validation**: Ensures mandatory fields (like model names or database URLs) are present and valid.

## Key Types

### `Config`
The root configuration structure, containing sub-sections for:
- `LLM`: Model names, URLs, and provider settings.
- `Storage`: Vector database (Qdrant) connection details.
- `Workspace`: Indexing patterns, exclusion rules, and auto-indexing flags.
- `Logging`: File paths and verbosity levels.

## Usage

```go
import "github.com/doITmagic/rag-code-mcp/internal/config"

// Load will look for config.yaml in the execution directory
// and apply environment overrides.
cfg, err := config.Load()
if err != nil {
    log.Fatalf("Failed to load config: %v", err)
}

fmt.Printf("Using LLM Provider: %s\n", cfg.LLM.Provider)
```

## Environment Priority

The configuration is resolved in the following order (highest to lowest priority):
1. **Environment Variables** (e.g., `RAG_LLM_MODEL`)
2. **YAML file** (`config.yaml`)
3. **Hardcoded Defaults**
