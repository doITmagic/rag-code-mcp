# CodeRAG MCP Server

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-blue)](https://go.dev/)
[![MCP](https://img.shields.io/badge/MCP-Compatible-green)](https://modelcontextprotocol.io)

**Semantic code navigation server using Retrieval‑Augmented Generation (RAG) with multi‑language support.**

Built with the official [Model Context Protocol Go SDK](https://github.com/modelcontextprotocol/go-sdk), CodeRAG provides intelligent code search and navigation tools over vector‑indexed codebases.

**Stack:** Ollama (LLM + embeddings) + Qdrant (vector database) + Docker + MCP  
**Clients:** Windsurf, Cursor, Antigravity, Claude Desktop, VS Code + Copilot, MCP Inspector

---

## 🚀 Why CodeRAG? Performance Benefits

### **5-10x Faster Code Understanding**

Without CodeRAG, AI assistants must:
- 📄 Read entire files to find relevant code
- 🔍 Search through thousands of lines manually
- 💭 Use precious context window tokens on irrelevant code
- ⏱️ Wait for multiple file reads and searches

**With CodeRAG:**
- ⚡ **Instant semantic search** - finds relevant code in milliseconds
- 🎯 **Pinpoint accuracy** - returns only the exact functions/types you need
- 💰 **90% less context usage** - AI sees only relevant code, not entire files
- 🧠 **Smarter responses** - AI has more tokens for actual reasoning

### Real-World Impact

| Task | Without CodeRAG | With CodeRAG | Speedup |
|------|----------------|--------------|---------|
| Find authentication logic | 30-60s (read 10+ files) | 2-3s (semantic search) | **10-20x faster** |
| Understand function signature | 15-30s (grep + read file) | 1-2s (direct lookup) | **15x faster** |
| Find all API endpoints | 60-120s (manual search) | 3-5s (hybrid search) | **20-40x faster** |
| Navigate type hierarchy | 45-90s (multiple files) | 2-4s (type definition) | **20x faster** |

### Token Efficiency

**Example: Finding a function in a 50,000 line codebase**

- **Without CodeRAG:** AI reads 5-10 files (~15,000 tokens) to find the function
- **With CodeRAG:** AI gets exact function + context (~200 tokens)
- **Savings:** **98% fewer tokens** = faster responses + lower costs

---

## ✨ Features

- **9 MCP Tools** – semantic search, hybrid search, function details, type definitions, workspace indexing, and more
- **Multi‑Language Support** – Go (≈82% coverage), PHP (≈84% coverage) + Laravel framework, Python (planned)
- **Multi‑Workspace Detection** – automatic workspace detection and per‑workspace collections
- **Per‑Language Collections** – separate Qdrant collections for each language (e.g., `coderag-{workspace}-go`)
- **Hybrid Search** – combines semantic (vector) and lexical (keyword) search for better relevance
- **Direct File Access** – read code context without indexing

---

## 📦 System Requirements

### Minimum Requirements

| Component | Requirement | Notes |
|-----------|-------------|-------|
| **CPU**   | 4 cores     | For running Ollama models |
| **RAM**   | 16 GB       | 8 GB for `phi3:medium`, 4 GB for `nomic-embed-text`, 4 GB system |
| **Disk**  | 10 GB free  | ~8 GB for models + 2 GB for data |
| **OS**    | Linux, macOS, Windows | Docker required for Qdrant |

### Recommended Requirements

| Component | Requirement | Notes |
|-----------|-------------|-------|
| **CPU**   | 8+ cores    | Better performance for concurrent operations |
| **RAM**   | 32 GB       | Allows comfortable multi‑workspace indexing |
| **GPU**   | NVIDIA GPU with 8 GB+ VRAM | Significantly speeds up Ollama inference (optional) |
| **Disk**  | 20 GB free SSD | Faster indexing and search |

### Model Sizes

- `nomic-embed-text`: ~274 MB (embeddings model)
- `phi3:medium`: ~7.9 GB (LLM for code analysis)
- **Total**: ~8.2 GB for models

---

## ⚡ Quick Start (One‑Command Installer)

```bash
curl -fsSL https://raw.githubusercontent.com/doITmagic/coderag-mcp/main/quick-install.sh | bash
```

The installer will:
1. ✅ Download the latest release from GitHub (or build locally if the download fails)
2. ✅ Install binaries into `~/.local/share/coderag/bin`
3. ✅ Add `coderag-mcp` to your `PATH`
4. ✅ Configure Windsurf, Cursor, and Antigravity automatically (writes `mcp_config.json`)
5. ✅ **Start Docker** if it is not already running
6. ✅ **Start the Qdrant container** (vector database)
7. ✅ **Start Ollama** with `ollama serve` if it is not already running
8. ✅ **Download required AI models** (`nomic-embed-text` and `phi3:medium`)
9. ✅ Launch the MCP server in the background

### Customization Options

You can customize the installation using environment variables:

```bash
# Use development branch
curl -fsSL https://raw.githubusercontent.com/doITmagic/coderag-mcp/develop/quick-install.sh | BRANCH=develop bash

# Custom Ollama model
curl -fsSL https://raw.githubusercontent.com/doITmagic/coderag-mcp/main/quick-install.sh | OLLAMA_MODEL=llama3.1:8b bash

# Combine multiple options
curl -fsSL https://raw.githubusercontent.com/doITmagic/coderag-mcp/develop/quick-install.sh | BRANCH=develop OLLAMA_MODEL=phi3:mini bash
```

**Available environment variables:**
- `BRANCH` – Git branch to install from (default: `main`)
- `OLLAMA_MODEL` – LLM model name (default: `phi3:medium`)
- `OLLAMA_EMBED` – Embedding model (default: `nomic-embed-text`)
- `OLLAMA_BASE_URL` – Ollama server URL (default: `http://localhost:11434`)
- `QDRANT_URL` – Qdrant server URL (default: `http://localhost:6333`)

See [QUICKSTART.md](./QUICKSTART.md) for detailed installation and usage instructions.

### Manual Build (for developers)

```bash
git clone https://github.com/doITmagic/coderag-mcp.git
cd coderag-mcp
go run ./cmd/install
```

---

## 📋 Step‑by‑Step Setup

### 1. Install Prerequisites

#### Docker (for Qdrant)
```bash
# Ubuntu/Debian
sudo apt update && sudo apt install docker.io
sudo systemctl start docker
sudo usermod -aG docker $USER   # log out / log in again

# macOS
brew install docker
```

#### Ollama (for AI models)
```bash
# Linux
curl -fsSL https://ollama.com/install.sh | sh

# macOS
brew install ollama
```

### 2. Run the Installer
```bash
curl -fsSL https://raw.githubusercontent.com/doITmagic/coderag-mcp/main/quick-install.sh | bash
```

Installation typically takes 5‑10 minutes (downloading the AI models can be the longest part).

### 3. Verify Installation
```bash
# Check the binary
~/.local/share/coderag/bin/coderag-mcp --version

# Verify services are running
docker ps | grep qdrant
ollama list
```

### 4. Start the Server (optional – the installer already starts it)
```bash
~/.local/share/coderag/start.sh
```

---

## 🎯 Using CodeRAG in Windsurf / Cursor

After installation, CodeRAG is automatically available in supported IDEs. No additional configuration is required.

### Available Tools
1. **`search_code`** – semantic code search
2. **`hybrid_search`** – semantic + lexical search
3. **`get_function_details`** – detailed information about a function or method
4. **`find_type_definition`** – locate struct, interface, or type definitions
5. **`find_implementations`** – find implementations or usages of a symbol
6. **`list_package_exports`** – list all exported symbols in a package
7. **`search_docs`** – search markdown documentation
8. **`index_workspace`** – manually trigger indexing of a workspace (usually not needed)

**All tools require a `file_path` parameter** so that CodeRAG can determine the correct workspace.

---

## 🔄 Automatic Indexing

When a tool is invoked for the first time in a workspace, CodeRAG will:
1. Detect the workspace from `file_path`
2. Create a Qdrant collection for that workspace and language
3. Index the code in the background
4. Return results immediately (even if indexing is still in progress)

You never need to run `index_workspace` manually.

---

## 🛠 Advanced Configuration

### Changing AI Models
Edit `~/.local/share/coderag/config.yaml`:
```yaml
llm:
  provider: "ollama"
  base_url: "http://localhost:11434"
  model: "phi3:medium"        # change to another model if desired
  embed_model: "nomic-embed-text"
```
Recommended models:
- **LLM:** `phi3:medium`, `llama3.1:8b`, `qwen2.5:7b`
- **Embeddings:** `nomic-embed-text`, `all-minilm`

### Qdrant Configuration
```yaml
qdrant:
  url: "http://localhost:6333"
  collection_prefix: "coderag"
```

### Excluding Directories
```yaml
workspace:
  exclude_patterns:
    - "vendor"
    - "node_modules"
    - ".git"
    - "dist"
    - "build"
```

---

## 🐛 Troubleshooting

### "Workspace '/home' is not indexed yet"
**Cause:** `file_path` is missing or points outside a recognized project.
**Fix:** Provide a valid `file_path` inside your project, e.g.:
```json
{ "query": "search query", "file_path": "/path/to/your/project/file.go" }
```

### "Could not connect to Qdrant"
**Cause:** Docker is not running or the Qdrant container is stopped.
**Fix:**
```bash
sudo systemctl start docker   # Linux
# Then start Qdrant (the installer does this automatically)
~/.local/share/coderag/start.sh
```

### "Ollama model not found"
**Cause:** Required models have not been downloaded.
**Fix:**
```bash
ollama pull nomic-embed-text
ollama pull phi3:medium
```

### Indexing is too slow
**Cause:** Large workspace or a heavy model.
**Fix:**
- Use a smaller model (`phi3:mini`)
- Exclude large directories in `config.yaml`
- Wait – indexing runs in the background.

---

## 📚 Example Requests
```json
{ "query": "user authentication login", "file_path": "/home/user/myproject/auth/handler.go" }
```
```json
{ "type_name": "UserController", "file_path": "/home/user/laravel-app/app/Http/Controllers/UserController.php" }
```
```json
{ "query": "API endpoints documentation", "file_path": "/home/user/myproject/docs/API.md" }
```

---

## 🔗 Useful Links
- **GitHub:** https://github.com/doITmagic/coderag-mcp
- **Issues:** https://github.com/doITmagic/coderag-mcp/issues
- **Ollama Docs:** https://ollama.com
- **Qdrant Docs:** https://qdrant.tech

---

## 🤝 Contributing
Contributions are welcome! Open a PR or an Issue on GitHub.

---

## 📄 License
MIT License – see the `LICENSE` file for details.

---

**Questions? Problems?** Open an Issue on GitHub! 🚀
