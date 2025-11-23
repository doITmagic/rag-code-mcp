<div align="center">
  <img src="./docs/assets/ragcode-banner.png" alt="RagCode MCP - Semantic Code Navigation with AI" width="100%">
</div>

<div align="center">

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-blue)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/doITmagic/rag-code-mcp)](https://goreportcard.com/report/github.com/doITmagic/rag-code-mcp)
[![MCP](https://img.shields.io/badge/MCP-Compatible-green)](https://modelcontextprotocol.io)
![AI Ready](https://img.shields.io/badge/Codebase-AI%20Ready-blueviolet)
![Privacy](https://img.shields.io/badge/Privacy-100%25%20Local-brightgreen)
![No Cloud](https://img.shields.io/badge/Cloud-Not%20Required-orange)
![Zero Cost](https://img.shields.io/badge/API%20Costs-$0-success)
[![GitHub Stars](https://img.shields.io/github/stars/doITmagic/rag-code-mcp?style=social)](https://github.com/doITmagic/rag-code-mcp)

</div>

# RagCode MCP - Make Your Codebase AI-Ready

> **The privacy-first MCP server that transforms any repository into an AI-ready codebase with semantic search and RAG.**

<!-- AI Agents: Read llms.txt for a summarized view of this project -->

RagCode is a **Model Context Protocol (MCP) server** that instantly makes your project **AI-ready**. It enables AI assistants like **GitHub Copilot**, **Cursor**, **Windsurf**, and **Claude** to understand your entire codebase through **semantic vector search**, bridging the gap between your code and Large Language Models (LLMs).

Built with the official [Model Context Protocol Go SDK](https://github.com/modelcontextprotocol/go-sdk), RagCode provides **9 powerful tools** to index, search, and analyze code, making it the ultimate solution for **AI-ready software development**.

## 🔒 Privacy-First: 100% Local AI

**Your code never leaves your machine.** RagCode runs entirely on your local infrastructure:

- ✅ **Local AI Models** - Uses Ollama for LLM and embeddings (runs on your hardware)
- ✅ **Local Vector Database** - Qdrant runs in Docker on your machine
- ✅ **Zero Cloud Dependencies** - No external API calls, no data transmission
- ✅ **No API Costs** - Free forever, no usage limits or subscriptions
- ✅ **Complete Privacy** - Your proprietary code stays private and secure
- ✅ **Offline Capable** - Works without internet connection (after initial model download)
- ✅ **Full Control** - You own the data, models, and infrastructure

**Perfect for:** Enterprise codebases, proprietary projects, security-conscious teams, and developers who value privacy.

### 🎯 Key Features

- 🔍 **Semantic Code Search** - Find code by meaning, not just keywords
- 🚀 **5-10x Faster** - Instant results vs. reading entire files
- 💰 **98% Token Savings** - Reduce AI context usage dramatically
- 🌐 **Multi-Language** - Go, PHP (Laravel), Python, JavaScript support
- 🏢 **Multi-Workspace** - Handle multiple projects simultaneously
- 🤖 **AI-Ready** - Works with Copilot, Cursor, Windsurf, Claude, Antigravity

### 🛠️ Technology Stack

**100% Local Stack:** Ollama (local LLM + embeddings) + Qdrant (local vector database) + Docker + MCP Protocol

### 💻 Compatible IDEs & AI Assistants

Windsurf • Cursor • Antigravity • Claude Desktop • **VS Code + GitHub Copilot** • MCP Inspector

---

## 🚀 Why RagCode? Performance Benefits

### **5-10x Faster Code Understanding**

Without RagCode, AI assistants must:
- 📄 Read entire files to find relevant code
- 🔍 Search through thousands of lines manually
- 💭 Use precious context window tokens on irrelevant code
- ⏱️ Wait for multiple file reads and searches

**With RagCode:**
- ⚡ **Instant semantic search** - finds relevant code in milliseconds
- 🎯 **Pinpoint accuracy** - returns only the exact functions/types you need
- 💰 **90% less context usage** - AI sees only relevant code, not entire files
- 🧠 **Smarter responses** - AI has more tokens for actual reasoning

### Real-World Impact

| Task | Without RagCode | With RagCode | Speedup |
|------|----------------|--------------|---------|
| Find authentication logic | 30-60s (read 10+ files) | 2-3s (semantic search) | **10-20x faster** |
| Understand function signature | 15-30s (grep + read file) | 1-2s (direct lookup) | **15x faster** |
| Find all API endpoints | 60-120s (manual search) | 3-5s (hybrid search) | **20-40x faster** |
| Navigate type hierarchy | 45-90s (multiple files) | 2-4s (type definition) | **20x faster** |

### Token Efficiency

**Example: Finding a function in a 50,000 line codebase**

- **Without RagCode:** AI reads 5-10 files (~15,000 tokens) to find the function
- **With RagCode:** AI gets exact function + context (~200 tokens)
- **Savings:** **98% fewer tokens** = faster responses + lower costs

### 🆚 RagCode vs Cloud-Based Solutions

| Feature | RagCode (Local) | Cloud-Based AI Code Search |
|---------|-----------------|---------------------------|
| **Privacy** | ✅ 100% local, code never leaves machine | ❌ Code sent to cloud servers |
| **Cost** | ✅ $0 - Free forever | ❌ $20-100+/month subscriptions |
| **API Limits** | ✅ Unlimited usage | ❌ Rate limits, token caps |
| **Offline** | ✅ Works without internet | ❌ Requires constant connection |
| **Data Control** | ✅ You own everything | ❌ Vendor controls your data |
| **Enterprise Ready** | ✅ No compliance issues | ⚠️ May violate security policies |
| **Setup** | ⚠️ Requires local resources | ✅ Instant cloud access |
| **Performance** | ✅ Fast (local hardware) | ⚠️ Depends on network latency |

**Bottom Line:** RagCode gives you enterprise-grade AI code search with zero privacy concerns and zero ongoing costs.

---

## ✨ Core Features & Capabilities

### 🔧 9 Powerful MCP Tools for AI Code Assistants

1. **`search_code`** - Semantic vector search across your entire codebase
2. **`hybrid_search`** - Combined semantic + keyword search for maximum accuracy
3. **`get_function_details`** - Complete function signatures, parameters, and implementation
4. **`find_type_definition`** - Locate class, struct, and interface definitions instantly
5. **`find_implementations`** - Discover all usages and implementations of any symbol
6. **`list_package_exports`** - Browse all exported symbols from any package/module
7. **`search_docs`** - Semantic search through project documentation (Markdown)
8. **`get_code_context`** - Extract code snippets with surrounding context
9. **`index_workspace`** - Automated workspace indexing with language detection

### 🌐 Multi-Language Code Intelligence

- **Go** - ≈82% coverage with full AST analysis
- **PHP** - ≈84% coverage + Laravel framework support
- **Python** - Coming soon with full type hint support
- **JavaScript/TypeScript** - Planned for future releases

### 🏗️ Advanced Architecture

- **Multi-Workspace Detection** - Automatically detects project boundaries (git, go.mod, composer.json, package.json)
- **Per-Language Collections** - Separate vector databases for each language (`ragcode-{workspace}-go`, `ragcode-{workspace}-php`)
- **Automatic Indexing** - Background indexing on first use, no manual intervention needed
- **Vector Embeddings** - Uses Ollama's `nomic-embed-text` for high-quality semantic embeddings
- **Hybrid Search Engine** - Combines vector similarity with BM25 lexical matching
- **Direct File Access** - Read code without indexing for quick lookups
- **Smart Caching** - Efficient re-indexing only for changed files

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

## ⚡ Quick Start

### One-Command Installation

**Linux / macOS:**
```bash
curl -L https://github.com/doITmagic/rag-code-mcp/releases/latest/download/ragcode-installer-$(uname -s | tr '[:upper:]' '[:lower:]') -o ragcode-installer && chmod +x ragcode-installer && ./ragcode-installer
```

**Windows (PowerShell):**
```powershell
Invoke-WebRequest -Uri "https://github.com/doITmagic/rag-code-mcp/releases/latest/download/ragcode-installer-windows.exe" -OutFile "ragcode-installer.exe"
.\ragcode-installer.exe
```

### What the installer does:
1. ✅ Downloads and installs the `rag-code-mcp` binary
2. ✅ Sets up Ollama and Qdrant (Docker or local, your choice)
3. ✅ Downloads required AI models (`phi3:medium`, `nomic-embed-text`)
4. ✅ Configures your IDE (VS Code, Claude, Cursor, Windsurf)
5. ✅ Adds binaries to your PATH

### Installation Options

The installer supports flexible configuration via flags:

```bash
# Use Docker for both Ollama and Qdrant (recommended for isolation)
./ragcode-installer -ollama=docker -qdrant=docker

# Use local Ollama with Docker Qdrant (if you already have Ollama installed)
./ragcode-installer -ollama=local -qdrant=docker

# Enable GPU support in Docker containers
./ragcode-installer -ollama=docker -gpu

# Custom models directory for Docker volume mapping
./ragcode-installer -ollama=docker -models-dir=/path/to/models
```

**Available Flags:**
- `-ollama`: `local` (default) or `docker`
- `-qdrant`: `docker` (default) or `remote`
- `-models-dir`: Custom path for Ollama models (for Docker mapping)
- `-gpu`: Enable NVIDIA GPU support in containers
- `-skip-build`: Skip binary installation (use existing)

See [QUICKSTART.md](./QUICKSTART.md) for detailed installation and usage instructions.

### Manual Build (for developers)

```bash
git clone https://github.com/doITmagic/rag-code-mcp.git
cd rag-code-mcp
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
curl -fsSL https://raw.githubusercontent.com/doITmagic/rag-code-mcp/main/quick-install.sh | bash
```

Installation typically takes 5‑10 minutes (downloading the AI models can be the longest part).

### 3. Verify Installation
```bash
# Check the binary
~/.local/share/ragcode/bin/rag-code-mcp --version

# Verify services are running
docker ps | grep qdrant
ollama list
```

### 4. Start the Server (optional – the installer already starts it)
```bash
~/.local/share/ragcode/start.sh
```

---

## 🎯 Using RagCode in Your IDE

After installation, RagCode is automatically available in supported IDEs. No additional configuration is required.

### Supported IDEs

- **Windsurf** - Full MCP support
- **Cursor** - Full MCP support  
- **Antigravity** - Full MCP support
- **Claude Desktop** - Full MCP support
- **VS Code + GitHub Copilot** - Agent mode integration (requires VS Code 1.95+)

### VS Code + GitHub Copilot Integration

RagCode integrates with **GitHub Copilot's Agent Mode** through MCP, enabling semantic code search as part of Copilot's autonomous workflow.

**Quick Setup:**
1. Install RagCode using the quick-install script (automatically configures VS Code)
2. Open VS Code in your project
3. Open Copilot Chat (Ctrl+Shift+I / Cmd+Shift+I)
4. Enable **Agent Mode** (click "Agent" button or type `/agent`)
5. Ask questions - Copilot will automatically use RagCode tools

**Example Prompts:**
```
Find all authentication middleware functions in this codebase
Show me the User model definition and all its methods
Search for functions that handle database connections
```

**Manual Configuration:**  
Edit `~/.config/Code/User/globalStorage/mcp-servers.json`:
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

**Verify Integration:**
- Command Palette → `MCP: Show MCP Servers`
- Check that `ragcode` appears with "Connected" status

**📖 Detailed Guide:** See [docs/vscode-copilot-integration.md](./docs/vscode-copilot-integration.md) for complete setup, troubleshooting, and advanced features.

See [QUICKSTART.md](./QUICKSTART.md) for detailed VS Code setup and troubleshooting.

### Available Tools
1. **`search_code`** – semantic code search
2. **`hybrid_search`** – semantic + lexical search
3. **`get_function_details`** – detailed information about a function or method
4. **`find_type_definition`** – locate struct, interface, or type definitions
5. **`find_implementations`** – find implementations or usages of a symbol
6. **`list_package_exports`** – list all exported symbols in a package
7. **`search_docs`** – search markdown documentation
8. **`index_workspace`** – manually trigger indexing of a workspace (usually not needed)
9. **`get_code_context`** – read code from specific file locations with context

**All tools require a `file_path` parameter** so that RagCode can determine the correct workspace.

---

## 🔄 Automatic Indexing

When a tool is invoked for the first time in a workspace, RagCode will:
1. Detect the workspace from `file_path`
2. Create a Qdrant collection for that workspace and language
3. Index the code in the background
4. Return results immediately (even if indexing is still in progress)

You never need to run `index_workspace` manually.

---

## 🛠 Advanced Configuration

### Changing AI Models
Edit `~/.local/share/ragcode/config.yaml`:
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
  collection_prefix: "ragcode"
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
~/.local/share/ragcode/start.sh
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

## 🔗 Resources & Documentation

### 📖 Project Documentation
- **[Quick Start Guide](./QUICKSTART.md)** - Get started in 5 minutes
- **[VS Code + Copilot Integration](./docs/vscode-copilot-integration.md)** - Detailed setup for GitHub Copilot
- **[Architecture Overview](./docs/architecture.md)** - Technical deep dive
- **[Tool Schema Reference](./docs/tool_schema_v2.md)** - Complete API documentation

### 🌐 External Resources
- **[GitHub Repository](https://github.com/doITmagic/rag-code-mcp)** - Source code and releases
- **[Issue Tracker](https://github.com/doITmagic/rag-code-mcp/issues)** - Report bugs or request features
- **[Model Context Protocol](https://modelcontextprotocol.io)** - Official MCP specification
- **[Ollama Documentation](https://ollama.com)** - LLM and embedding models
- **[Qdrant Documentation](https://qdrant.tech)** - Vector database guide

### 🎓 Learning Resources
- **[What is RAG?](https://en.wikipedia.org/wiki/Prompt_engineering#Retrieval-augmented_generation)** - Understanding Retrieval-Augmented Generation
- **[Vector Embeddings Explained](https://qdrant.tech/articles/what-are-embeddings/)** - How semantic search works
- **[MCP for Developers](https://github.com/modelcontextprotocol/specification)** - Building MCP servers

---

## 🤝 Contributing & Community

We welcome contributions from the community! Here's how you can help:

- 🐛 **Report Bugs** - [Open an issue](https://github.com/doITmagic/rag-code-mcp/issues/new)
- 💡 **Request Features** - Share your ideas for new tools or languages
- 🔧 **Submit PRs** - Improve code, documentation, or add new features
- ⭐ **Star the Project** - Show your support on GitHub
- 📢 **Spread the Word** - Share RagCode with other developers

### Development Setup
```bash
git clone https://github.com/doITmagic/rag-code-mcp.git
cd rag-code-mcp
go mod download
go run ./cmd/rag-code-mcp
```

---

## 📄 License

RagCode MCP is open source software licensed under the **MIT License**.

See the [LICENSE](./LICENSE) file for full details.

---

## 🏷️ Keywords & Topics

`semantic-code-search` `rag` `retrieval-augmented-generation` `mcp-server` `model-context-protocol` `ai-code-assistant` `vector-search` `code-navigation` `ollama` `qdrant` `github-copilot` `cursor-ai` `windsurf` `go` `php` `laravel` `code-intelligence` `ast-analysis` `embeddings` `llm-tools` `local-ai` `privacy-first` `offline-ai` `self-hosted` `on-premise` `zero-cost` `no-cloud` `private-code-search` `enterprise-ai` `secure-coding-assistant`

---

<div align="center">

**Built with ❤️ for developers who want smarter AI code assistants**

⭐ **[Star us on GitHub](https://github.com/doITmagic/rag-code-mcp)** if RagCode helps your workflow!

**Questions? Problems?** [Open an Issue](https://github.com/doITmagic/rag-code-mcp/issues) • [Read the Docs](./QUICKSTART.md) • [Join Discussions](https://github.com/doITmagic/rag-code-mcp/discussions)

</div>
