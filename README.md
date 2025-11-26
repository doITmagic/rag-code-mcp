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
- 🌐 **Multi-Language** - Go, PHP (Laravel), Python with full support
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

### 🧠 RagCode vs Generic RAG Systems

Most RAG systems treat code like plain text - they split files into arbitrary chunks of N tokens without understanding the code structure. **RagCode is different.**

| Aspect | Generic RAG | RagCode |
|--------|-------------|---------|
| **Chunking** | Arbitrary text splits (512 tokens) | Semantic units (functions, classes, methods) |
| **Context** | Random text fragments | Complete code entities with full context |
| **Metadata** | Filename only | Name, type, parameters, return type, dependencies, line numbers |
| **Relationships** | None | Knows `UserController` uses `UserService`, inheritance chains |
| **Search** | Semantic similarity only | Exact name match + semantic similarity combined |
| **Language Awareness** | Treats all text the same | AST parsing per language (Go, PHP, Python) |
| **Results** | May return partial functions | Always returns complete, runnable code units |

**Why this matters for AI assistants:**

```
❌ Generic RAG returns:
"...the user authentication logic checks the token
and validates against the database. If valid, it..."
→ AI sees fragment, must guess the rest

✅ RagCode returns:
func AuthenticateUser(token string) (*User, error) {
    // Complete function with all context
    user, err := db.FindByToken(token)
    if err != nil { return nil, ErrInvalidToken }
    return user, nil
}
File: auth/middleware.go, Lines: 45-52
Called by: LoginHandler, RefreshToken
Depends on: db.FindByToken, ErrInvalidToken
→ AI sees complete picture, responds accurately
```

**The result:** AI assistants using RagCode give more accurate answers because they see complete, contextual code - not random text fragments.

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

- **Go** - Full AST analysis with functions, types, interfaces, methods
- **PHP** - Full support + Laravel framework (Eloquent models, routes, controllers)
- **Python** - Full support with classes, functions, decorators, type hints, mixins, metaclasses
- **JavaScript/TypeScript** - Planned for future releases (tree-sitter based)

### 🏗️ Advanced Architecture

- **Multi-Workspace Detection** - Automatically detects project boundaries (git, go.mod, composer.json, package.json, pyproject.toml, requirements.txt)
- **Per-Language Collections** - Separate vector databases for each language (`ragcode-{workspace}-go`, `ragcode-{workspace}-php`, `ragcode-{workspace}-python`)
- **Automatic Indexing** - Background indexing on first use, no manual intervention needed
- **Incremental Indexing** - Smart re-indexing that only processes changed files, saving time and resources
- **Exact + Semantic Search** - Combines exact name matching with semantic similarity for best results
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

**Linux (amd64):**
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_linux_amd64.tar.gz | tar xz && ./ragcode-installer -ollama=docker -qdrant=docker
```

That's it! One command downloads, extracts, and runs the installer.

**macOS (Apple Silicon):**
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_darwin_arm64.tar.gz | tar xz && ./ragcode-installer -ollama=docker -qdrant=docker
```

**macOS (Intel):**
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_darwin_amd64.tar.gz | tar xz && ./ragcode-installer -ollama=docker -qdrant=docker
```

**Windows (PowerShell):**
```powershell
# Download and extract
Invoke-WebRequest -Uri "https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_windows_amd64.zip" -OutFile "ragcode.zip"
Expand-Archive ragcode.zip -DestinationPath . -Force

# Run installer (requires Docker Desktop running)
.\ragcode-installer.exe -ollama=docker -qdrant=docker
```
> ⚠️ Windows requires [Docker Desktop](https://www.docker.com/products/docker-desktop/) to be installed and running.

**Windows with WSL (alternative):**

If you prefer to run RagCode inside WSL while using Windows IDEs (Windsurf, Cursor, VS Code):

```bash
# Inside WSL terminal
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_linux_amd64.tar.gz | tar xz && ./ragcode-installer -ollama=docker -qdrant=docker
```

Then manually configure your Windows IDE to use the WSL binary. Example for Windsurf (`%USERPROFILE%\.codeium\windsurf\mcp_config.json`):

```json
{
  "mcpServers": {
    "ragcode": {
      "command": "wsl.exe",
      "args": ["-e", "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp"],
      "env": {
        "OLLAMA_BASE_URL": "http://localhost:11434",
        "OLLAMA_MODEL": "phi3:medium",
        "OLLAMA_EMBED": "nomic-embed-text",
        "QDRANT_URL": "http://localhost:6333"
      },
      "disabled": false
    }
  }
}
```

> 💡 Replace `YOUR_USERNAME` with your WSL username. The `localhost` URLs work because WSL2 shares network ports with Windows.

### What the installer does:
1. ✅ Downloads and installs the `rag-code-mcp` binary
2. ✅ Sets up Ollama and Qdrant (Docker or local, your choice)
3. ✅ Downloads required AI models (`phi3:medium`, `nomic-embed-text`)
4. ✅ Configures your IDE (VS Code, Claude, Cursor, Windsurf)
5. ✅ Adds binaries to your PATH

### Zero-Config Usage

Once installed, **you don't need to configure anything**.

1. Open your project in your IDE (VS Code, Cursor, Windsurf).
2. Ask your AI assistant a question about your code (e.g., *"How does the authentication system work?"*).
3. **That's it!** RagCode automatically detects your workspace, creates the index in the background, and answers your question.
   - First query might take a moment while indexing starts.
   - Subsequent queries are instant.
   - File changes are automatically detected and re-indexed incrementally.

### Installation Options

The installer runs **Ollama and Qdrant in Docker by default**, but you can quickly mix and match components:

| Scenario | When to use | Example command |
| --- | --- | --- |
| Everything in Docker (default) | Quick setup, no local software needed | `./ragcode-installer -ollama=docker -qdrant=docker` |
| Local Ollama + Docker Qdrant | You already have Ollama installed/optimized locally | `./ragcode-installer -ollama=local -qdrant=docker` |
| Existing remote services | Running Qdrant/Ollama in separate infrastructure | `./ragcode-installer -ollama=local -qdrant=remote --skip-build` |
| Docker + GPU | Run Ollama container with GPU acceleration | `./ragcode-installer -ollama=docker -qdrant=docker -gpu` |
| Docker with custom models folder | Reuse locally downloaded Ollama models | `./ragcode-installer -ollama=docker -models-dir=$HOME/.ollama` |

**Key flags:**
- `-ollama`: `docker` (default) or `local`
- `-qdrant`: `docker` (default) or `remote`
- `-models-dir`: mount your local directory as `/root/.ollama`
- `-gpu`: adds `--gpus=all` to the Ollama container
- `-skip-build`: skip rebuild if binaries already exist

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

**Docker is required** (for Qdrant, and optionally for Ollama):

```bash
# Ubuntu/Debian
sudo apt update && sudo apt install docker.io
sudo systemctl start docker
sudo usermod -aG docker $USER   # log out / log in again

# macOS
brew install docker
```

### 2. Run the Installer

**Option A: Everything in Docker (recommended, no extra installs needed)**
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_linux_amd64.tar.gz | tar xz && ./ragcode-installer -ollama=docker -qdrant=docker
```

**Option B: Use local Ollama (if you already have Ollama installed)**
```bash
# First, install Ollama locally (skip if already installed)
curl -fsSL https://ollama.com/install.sh | sh

# Then run installer with local Ollama
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_linux_amd64.tar.gz | tar xz && ./ragcode-installer -ollama=local -qdrant=docker
```

Installation takes 5‑10 minutes (downloading the Ollama models is the long pole).

### 3. Verify Installation
```bash
# Check the binary
~/.local/share/ragcode/bin/rag-code-mcp --version

# Verify services are running
docker ps | grep qdrant
ollama list
```

### 4. Health Check (services start automatically)
```bash
~/.local/share/ragcode/bin/rag-code-mcp --health
docker ps | grep ragcode-qdrant
docker ps | grep ragcode-ollama
```

### 5. Logs and Troubleshooting

- Main log file: `~/.local/share/ragcode/bin/mcp.log`
- Watch in real-time: `tail -f ~/.local/share/ragcode/bin/mcp.log`
- For service issues, also check Docker logs (`docker logs ragcode-ollama`, `docker logs ragcode-qdrant`).

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
1. Install RagCode with `ragcode-installer` (it configures VS Code automatically)
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

### Manual IDE Integration

If you install a new IDE **after** running `ragcode-installer`, you'll need to configure it manually. Below are the configuration file paths and JSON snippets for each supported IDE.

#### Configuration File Locations

| IDE | Config File Path |
| --- | --- |
| **Windsurf** | `~/.codeium/windsurf/mcp_config.json` |
| **Cursor** | `~/.cursor/mcp.config.json` |
| **Antigravity** | `~/.gemini/antigravity/mcp_config.json` |
| **Claude Desktop (Linux)** | `~/.config/Claude/mcp-servers.json` |
| **Claude Desktop (macOS)** | `~/Library/Application Support/Claude/mcp-servers.json` |
| **Claude Desktop (Windows)** | `%APPDATA%\Claude\mcp-servers.json` |
| **VS Code + Copilot** | `~/.config/Code/User/globalStorage/mcp-servers.json` |

#### JSON Configuration Snippet

Add the following to your IDE's MCP configuration file (create the file if it doesn't exist):

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

> **Important:** Replace `YOUR_USERNAME` with your actual system username. On macOS, the binary path is typically `/Users/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp`.

#### Re-running the Installer for New IDEs

Alternatively, you can re-run the installer to auto-configure newly installed IDEs:

```bash
# Re-run installer (it will detect and configure new IDEs)
~/.local/share/ragcode/bin/ragcode-installer -skip-build -ollama=local -qdrant=docker
```

The `-skip-build` flag skips binary compilation and only updates IDE configurations.

---

### Available Tools

| Tool | What it does | When to use |
| --- | --- | --- |
| `search_code` | Semantic search by MEANING. Returns complete source code with file:line. **Go, PHP, Python, HTML.** | **USE FIRST** for exploration. Better than hybrid_search for general queries. |
| `hybrid_search` | Keyword + semantic for **EXACT matches**. Returns code with file:line + metadata. **Go, PHP, Python, HTML.** | **ONLY** when you need exact identifiers (variable names, error messages). Use search_code first. |
| `get_function_details` | Returns **COMPLETE** function source: signature, params, return types, body. **Go, PHP, Python.** | When you know the exact function name and need full implementation. |
| `find_type_definition` | Returns complete type source with fields, methods, inheritance. **Go, PHP, Python.** | To understand a data model or see what methods a type has. |
| `find_implementations` | Lists all callers/usages with code snippets + file:line. **Go, PHP, Python.** | **Before refactoring** to understand impact, or to find usage examples. |
| `list_package_exports` | Returns structured list: symbol names, types, signatures. **Go, PHP, Python.** | To explore unfamiliar packages or find the right function to call. |
| `search_docs` | Returns doc snippets with file paths. **Markdown only.** | For setup, architecture, examples. **Not for code** - use search_code instead. |
| `get_code_context` | Returns code snippet with configurable context lines. **Any text file.** | When you have file:line (from search/errors) and need surrounding code. |
| `index_workspace` | Reindex codebase. **USUALLY AUTOMATIC.** **Go, PHP, Python, HTML.** | Only if "workspace not indexed" error or after git pull/branch switch. |

**All tools require a `file_path` parameter** so that RagCode can determine the correct workspace.

---

## 🔄 Automatic Indexing

When a tool (e.g., `search_code`, `get_function_details`, etc.) is invoked for the first time in a workspace, RagCode will:
1. Detect the workspace from `file_path`
2. Create a Qdrant collection for that workspace and language
3. Index the code in the background
4. Return results immediately (even if indexing is still in progress)

👉 **You never need to run `index_workspace` manually** – MCP tools automatically trigger indexing and incremental re-indexing in the background.

### ⚡ Incremental Indexing

RagCode features **smart incremental indexing** that dramatically reduces re-indexing time by only processing files that have changed.

**How it works:**
- Tracks file modification times and sizes in `.ragcode/state.json`
- On subsequent indexing runs, compares current state with saved state
- Only indexes new or modified files
- Automatically removes outdated chunks from deleted/modified files

**Performance Benefits:**
- **First run:** Indexes all files (e.g., 77 files in ~20 seconds)
- **No changes:** Completes instantly with "No code changes detected"
- **Single file change:** Re-indexes only that file (e.g., 1 file in ~1 second)

**Manual CLI (optional):** If you prefer to automate indexing from CI/CD scripts or run a full reindex on demand, you can use `./bin/index-all -paths ...`. The command follows the same incremental mechanism, but most users don't need to run it manually.

**Note:** Incremental indexing applies to source code files. Markdown documentation is fully re-indexed on every run (optimization planned).

For technical details, see [docs/incremental_indexing.md](./docs/incremental_indexing.md).

---

## 🛠 Advanced Configuration

### Installation Directory

RagCode installs to `~/.local/share/ragcode/` with the following structure:

```
~/.local/share/ragcode/
├── bin/
│   ├── rag-code-mcp      # Main MCP server binary
│   ├── index-all         # CLI indexing tool
│   └── mcp.log           # Server logs
└── config.yaml           # Main configuration file
```

### Configuration File

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

**Recommended models:**
- **LLM:** `phi3:medium`, `llama3.1:8b`, `qwen2.5:7b`
- **Embeddings:** `nomic-embed-text`, `all-minilm`

### Environment Variables

Environment variables override `config.yaml` settings. These are typically set in your IDE's MCP configuration:

| Variable | Default | Description |
| --- | --- | --- |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama server URL |
| `OLLAMA_MODEL` | `phi3:medium` | LLM model for code analysis |
| `OLLAMA_EMBED` | `nomic-embed-text` | Embedding model |
| `QDRANT_URL` | `http://localhost:6333` | Qdrant vector database URL |
| `MCP_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |

### Logs and Monitoring

- **Log file:** `~/.local/share/ragcode/bin/mcp.log`
- **Watch logs in real-time:**
  ```bash
  tail -f ~/.local/share/ragcode/bin/mcp.log
  ```
- **Docker container logs:**
  ```bash
  docker logs ragcode-ollama
  docker logs ragcode-qdrant
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

`semantic-code-search` `rag` `retrieval-augmented-generation` `mcp-server` `model-context-protocol` `ai-code-assistant` `vector-search` `code-navigation` `ollama` `qdrant` `github-copilot` `cursor-ai` `windsurf` `go` `php` `laravel` `python` `django` `flask` `fastapi` `code-intelligence` `ast-analysis` `embeddings` `llm-tools` `local-ai` `privacy-first` `offline-ai` `self-hosted` `on-premise` `zero-cost` `no-cloud` `private-code-search` `enterprise-ai` `secure-coding-assistant`

---

<div align="center">

**Built with ❤️ for developers who want smarter AI code assistants**

⭐ **[Star us on GitHub](https://github.com/doITmagic/rag-code-mcp)** if RagCode helps your workflow!

**Questions? Problems?** [Open an Issue](https://github.com/doITmagic/rag-code-mcp/issues) • [Read the Docs](./QUICKSTART.md) • [Join Discussions](https://github.com/doITmagic/rag-code-mcp/discussions)

</div>
