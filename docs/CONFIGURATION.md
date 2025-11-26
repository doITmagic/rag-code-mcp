# ⚙️ RagCode Configuration Guide

Complete configuration reference for RagCode MCP server.

---

## 📁 Installation Directory

RagCode installs to `~/.local/share/ragcode/` with the following structure:

```
~/.local/share/ragcode/
├── bin/
│   ├── rag-code-mcp      # Main MCP server binary
│   ├── index-all         # CLI indexing tool
│   └── mcp.log           # Server logs
└── config.yaml           # Main configuration file
```

---

## 📄 Configuration File

Edit `~/.local/share/ragcode/config.yaml` to customize RagCode:

```yaml
llm:
  provider: "ollama"
  base_url: "http://localhost:11434"
  model: "phi3:medium"        # LLM for code analysis
  embed_model: "nomic-embed-text"  # Embedding model

storage:
  vector_db:
    url: "http://localhost:6333"
    collection_prefix: "ragcode"

workspace:
  auto_index: true
  exclude_patterns:
    - "vendor"
    - "node_modules"
    - ".git"
    - "dist"
    - "build"

logging:
  level: "info"           # debug, info, warn, error
  path: "~/.local/share/ragcode/bin/mcp.log"
```

---

## 🤖 Recommended Models

### LLM Models (for code analysis)
| Model | Size | Speed | Quality | Use Case |
|-------|------|-------|---------|----------|
| `phi3:medium` | 7.9 GB | Fast | Good | **Recommended default** |
| `phi3:mini` | 2.3 GB | Very Fast | Basic | Low-resource systems |
| `llama3.1:8b` | 4.7 GB | Fast | Very Good | Better reasoning |
| `qwen2.5:7b` | 4.4 GB | Fast | Very Good | Multi-language |
| `codellama:7b` | 3.8 GB | Fast | Good | Code-specific |

### Embedding Models
| Model | Size | Dimensions | Use Case |
|-------|------|------------|----------|
| `nomic-embed-text` | 274 MB | 768 | **Recommended default** |
| `all-minilm` | 45 MB | 384 | Faster, lower quality |
| `mxbai-embed-large` | 670 MB | 1024 | Higher quality |

---

## 🌍 Environment Variables

Environment variables override `config.yaml` settings. Set these in your IDE's MCP configuration:

| Variable | Default | Description |
|----------|---------|-------------|
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama server URL |
| `OLLAMA_MODEL` | `phi3:medium` | LLM model for code analysis |
| `OLLAMA_EMBED` | `nomic-embed-text` | Embedding model |
| `QDRANT_URL` | `http://localhost:6333` | Qdrant vector database URL |
| `MCP_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |

### Example IDE Configuration

```json
{
  "mcpServers": {
    "ragcode": {
      "command": "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp",
      "args": [],
      "env": {
        "OLLAMA_BASE_URL": "http://localhost:11434",
        "OLLAMA_MODEL": "phi3:medium",
        "OLLAMA_EMBED": "nomic-embed-text",
        "QDRANT_URL": "http://localhost:6333"
      }
    }
  }
}
```

---

## 📊 Logs and Monitoring

### Log File Location
- **Main log:** `~/.local/share/ragcode/bin/mcp.log`

### Watch Logs in Real-Time
```bash
tail -f ~/.local/share/ragcode/bin/mcp.log
```

### Docker Container Logs
```bash
docker logs ragcode-ollama
docker logs ragcode-qdrant
```

### Log Levels
- `debug` - Verbose output, useful for development
- `info` - Normal operation (default)
- `warn` - Warnings only
- `error` - Errors only

---

## 🔧 Installer Options

The `ragcode-installer` supports various configurations:

| Flag | Values | Description |
|------|--------|-------------|
| `-ollama` | `docker`, `local` | Where to run Ollama |
| `-qdrant` | `docker`, `remote` | Where to run Qdrant |
| `-gpu` | (flag) | Enable GPU acceleration for Ollama |
| `-models-dir` | path | Mount local Ollama models directory |
| `-skip-build` | (flag) | Skip binary compilation |

### Common Scenarios

```bash
# Everything in Docker (default, recommended)
./ragcode-installer -ollama=docker -qdrant=docker

# Local Ollama + Docker Qdrant
./ragcode-installer -ollama=local -qdrant=docker

# Docker with GPU acceleration
./ragcode-installer -ollama=docker -qdrant=docker -gpu

# Reuse existing Ollama models
./ragcode-installer -ollama=docker -models-dir=$HOME/.ollama

# Re-configure IDEs only (no rebuild)
./ragcode-installer -skip-build
```

---

## 📦 System Requirements

### Minimum Requirements

| Component | Requirement | Notes |
|-----------|-------------|-------|
| **CPU** | 4 cores | For running Ollama models |
| **RAM** | 16 GB | 8 GB for `phi3:medium`, 4 GB for `nomic-embed-text`, 4 GB system |
| **Disk** | 10 GB free | ~8 GB for models + 2 GB for data |
| **OS** | Linux, macOS, Windows | Docker required for Qdrant |

### Recommended Requirements

| Component | Requirement | Notes |
|-----------|-------------|-------|
| **CPU** | 8+ cores | Better performance for concurrent operations |
| **RAM** | 32 GB | Allows comfortable multi‑workspace indexing |
| **GPU** | NVIDIA GPU with 8 GB+ VRAM | Significantly speeds up Ollama inference (optional) |
| **Disk** | 20 GB free SSD | Faster indexing and search |

### Model Sizes
- `nomic-embed-text`: ~274 MB
- `phi3:medium`: ~7.9 GB
- **Total**: ~8.2 GB for models

---

## 🔄 Incremental Indexing

RagCode features **smart incremental indexing** that only processes changed files.

### How It Works
- Tracks file modification times and sizes in `.ragcode/state.json`
- Compares current state with saved state on each run
- Only indexes new or modified files
- Automatically removes outdated chunks from deleted/modified files

### Performance
- **First run:** Indexes all files (e.g., 77 files in ~20 seconds)
- **No changes:** Completes instantly with "No code changes detected"
- **Single file change:** Re-indexes only that file (e.g., 1 file in ~1 second)

For technical details, see [incremental_indexing.md](./incremental_indexing.md).

---

## 🔗 Related Documentation

- **[Quick Start](../QUICKSTART.md)** - Get started in 5 minutes
- **[IDE Setup](./IDE-SETUP.md)** - Manual IDE configuration
- **[Troubleshooting](./TROUBLESHOOTING.md)** - Common problems and solutions
- **[Architecture](./architecture.md)** - Technical deep dive
