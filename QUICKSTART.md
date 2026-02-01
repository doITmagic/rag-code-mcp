# 🚀 RagCode MCP - Quick Start Guide

**Semantic code navigation using RAG (Retrieval-Augmented Generation)**

---

## 📦 What is RagCode?

RagCode is an MCP (Model Context Protocol) server that allows you to navigate and understand code using semantic search. It works with **Windsurf**, **Cursor**, **Antigravity**, **Claude Desktop**, and other MCP-compatible IDEs to provide:

- 🔍 **Semantic Search** in your codebase (not just text matching)
- 📚 **Contextual Understanding** of code (functions, classes, relationships)
- 🎯 **Multi-workspace** - works on multiple projects simultaneously
- 🌐 **Multi-language** - support for Go, PHP (Laravel), JavaScript, Python

---

## ⚡ Quick Install (1 Command)

### Option 1: Install Script (Recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/doITmagic/rag-code-mcp/main/quick-install.sh | bash
```

The installer will:
1. ✅ Download the latest release from GitHub (or build locally if download fails)
2. ✅ Install binaries to `~/.local/share/ragcode/bin`
3. ✅ Add `rag-code-mcp` to PATH (in `.bashrc` or `.zshrc`)
4. ✅ Configures Windsurf, Cursor, Antigravity, and VS Code automatically (in `mcp_config.json`)
5. ✅ **Starts Docker** (if not already running)
6. ✅ **Starts Qdrant container** (vector database)
7. ✅ **Starts Ollama** with `ollama serve` (if not already running)
8. ✅ **Downloads required AI models**:
   - `nomic-embed-text` (~274 MB) - for embeddings
   - `phi3:medium` (~7.9 GB) - for LLM
9. ✅ Starts MCP server in background

**Environment Variables (Optional):**

You can customize the installation by setting environment variables before running the script:

```bash
# Use development branch instead of main
curl -fsSL https://raw.githubusercontent.com/doITmagic/rag-code-mcp/develop/quick-install.sh | BRANCH=develop bash

# Custom Ollama model
curl -fsSL https://raw.githubusercontent.com/doITmagic/rag-code-mcp/main/quick-install.sh | OLLAMA_MODEL=llama3.1:8b bash

# Custom embedding model
curl -fsSL https://raw.githubusercontent.com/doITmagic/rag-code-mcp/main/quick-install.sh | OLLAMA_EMBED=all-minilm bash

# Custom Ollama URL (if running remotely)
curl -fsSL https://raw.githubusercontent.com/doITmagic/rag-code-mcp/main/quick-install.sh | OLLAMA_BASE_URL=http://192.168.1.100:11434 bash

# Custom Qdrant URL
curl -fsSL https://raw.githubusercontent.com/doITmagic/rag-code-mcp/main/quick-install.sh | QDRANT_URL=http://192.168.1.100:6333 bash

# Combine multiple variables
curl -fsSL https://raw.githubusercontent.com/doITmagic/rag-code-mcp/develop/quick-install.sh | BRANCH=develop OLLAMA_MODEL=phi3:mini bash
```

**Available Environment Variables:**
- `BRANCH` - Git branch to install from (default: `main`)
- `OLLAMA_MODEL` - LLM model name (default: `phi3:medium`)
- `OLLAMA_EMBED` - Embedding model (default: `nomic-embed-text`)
- `OLLAMA_BASE_URL` - Ollama server URL (default: `http://localhost:11434`)
- `QDRANT_URL` - Qdrant server URL (default: `http://localhost:6333`)

### Option 2: Local Build (For Developers)

```bash
git clone https://github.com/doITmagic/rag-code-mcp.git
cd rag-code-mcp
go run ./cmd/install
```

---

## 🔧 System Requirements

### Mandatory:
- **Docker** - for Qdrant (vector database)
- **Ollama** - for LLM and embeddings
- **Go 1.21+** - only for local build

### Optional:
- **Windsurf**, **Cursor**, **Antigravity**, **Claude Desktop**, or other MCP compatible IDEs

---

## 📋 Step-by-Step Setup

### 1. Install Dependencies

#### Docker (for Qdrant)
```bash
# Ubuntu/Debian
sudo apt update && sudo apt install docker.io
sudo systemctl start docker
sudo usermod -aG docker $USER  # Logout/login after

# macOS
brew install docker
```

#### Ollama (for AI)
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

**Installation time:** ~5-10 minutes (downloads ~4GB of AI models)

### 3. Verify Installation

```bash
# Verify binaries are installed
~/.local/share/ragcode/bin/rag-code-mcp --version

# Verify services are running
docker ps | grep qdrant
ollama list
```

### 4. Start Server (Optional - starts automatically)

```bash
~/.local/share/ragcode/start.sh
```

---

## 💡 First Time Setup - Index Your Workspace

After installation, you need to index each project you want to work with. This is a **one-time setup per project**.

### Quick Start Prompt for Your AI Assistant

Open your project in Windsurf or Cursor and paste this prompt to the AI:

```
Please use the RagCode MCP tool 'index_workspace' to index this project 
for semantic code search. Provide the file_path parameter pointing to any 
file in this workspace. Once indexing completes, I'll be able to use 
search_code, get_function_details, and other tools to help you navigate 
and understand the codebase.

Note: Indexing runs in the background and may take a few minutes depending 
on project size. You can start using search immediately - results will 
improve as indexing progresses.
```

### What Happens During Indexing?

1. 🔍 **Workspace Detection** - RagCode detects your project root (looks for `.git`, `go.mod`, `composer.json`, etc.)
2. 📊 **Language Detection** - Identifies programming languages in your project
3. 🗂️ **Collection Creation** - Creates a Qdrant collection: `ragcode-{workspace-id}-{language}`
4. 📝 **Code Analysis** - Extracts functions, classes, types, and their relationships
5. 🧠 **Embedding Generation** - Creates semantic embeddings using Ollama
6. 💾 **Vector Storage** - Stores embeddings in Qdrant for fast retrieval

### Example Workflow

```bash
# 1. Open your project in Windsurf/Cursor
cd ~/projects/my-awesome-app

# 2. Ask AI to index (using the prompt above)
# AI will call: index_workspace with file_path="/path/to/my-awesome-app/main.go"

# 3. Wait for confirmation (usually 1-5 minutes)
# ✓ Indexing started for workspace '/path/to/my-awesome-app'
# Languages: go
# Collections will be created: ragcode-abc123-go

# 4. Start using semantic search!
# Ask: "Find all authentication middleware functions"
# Ask: "Show me the User model definition"
# Ask: "What functions call the database connection?"
```

### Multi-Project Support

**Repeat the indexing process for each project:**

```bash
# Project 1
cd ~/projects/backend-api
# Ask AI to index this workspace

# Project 2  
cd ~/projects/frontend-app
# Ask AI to index this workspace

# Project 3
cd ~/projects/mobile-app
# Ask AI to index this workspace
```

Each project gets its own collection in Qdrant, and RagCode automatically switches between them based on which file you're working with.

---

## 🎯 How to Use RagCode?

### In Your MCP-Compatible IDE (Windsurf, Cursor, Antigravity, etc.)

After installation, RagCode is automatically available in the IDE. **No manual action required!**

### In VS Code with GitHub Copilot

RagCode integrates with **GitHub Copilot's Agent Mode** in VS Code through the Model Context Protocol (MCP). This allows Copilot to use RagCode's semantic search capabilities as part of its autonomous coding workflow.

#### Prerequisites
- **VS Code** with **GitHub Copilot** subscription
- RagCode installed (via quick-install script above)
- VS Code version **1.95+** (for MCP support)

#### Setup

The quick-install script automatically configures RagCode for VS Code by creating:
```
~/.config/Code/User/globalStorage/mcp-servers.json
```

**Manual Configuration (if needed):**

Create or edit `~/.config/Code/User/globalStorage/mcp-servers.json`:

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

**Note:** Replace `YOUR_USERNAME` with your actual username.

#### Using RagCode with Copilot Agent Mode

1. **Open VS Code** in your project directory
2. **Open Copilot Chat** (Ctrl+Shift+I or Cmd+Shift+I)
3. **Enable Agent Mode** by clicking the "Agent" button or typing `/agent`
4. **Use RagCode tools** - Copilot will automatically invoke them based on your prompts

**Example Prompts:**

```
Find all authentication middleware functions in this codebase
```

```
Show me the User model definition and all its methods
```

```
Search for functions that handle database connections
```

```
Find all API endpoints related to user management
```

Copilot will automatically use RagCode's `search_code`, `get_function_details`, `find_type_definition`, and other tools to answer your questions.

#### Explicit Tool Usage

You can also explicitly reference RagCode tools using the `#` symbol:

```
#ragcode search for payment processing functions
```

```
#ragcode find the UserController type definition
```

#### Verifying MCP Integration

1. Open **Command Palette** (Ctrl+Shift+P / Cmd+Shift+P)
2. Type: `MCP: Show MCP Servers`
3. Verify that `ragcode` appears in the list
4. Check status shows "Connected"

#### Troubleshooting VS Code Integration

**MCP server not showing:**
- Verify config file exists: `~/.config/Code/User/globalStorage/mcp-servers.json`
- Restart VS Code
- Check VS Code version (requires 1.95+)

**Tools not working:**
- Ensure Qdrant and Ollama are running: `docker ps | grep qdrant`
- Check MCP server logs in VS Code Output panel (select "MCP" from dropdown)
- Verify binary path is correct in config

**Copilot not using tools:**
- Make sure you're in **Agent Mode** (not regular chat)
- Try explicitly mentioning `#ragcode` in your prompt
- Ensure workspace is indexed (ask Copilot to index first)

**📖 For more details:** See [docs/vscode-copilot-integration.md](../docs/vscode-copilot-integration.md) for:
- Advanced configuration options
- Custom Ollama models
- Remote Ollama/Qdrant setup
- Detailed troubleshooting
- Multi-workspace workflows
- Performance optimization tips

#### Available Tools:

1. **`search_code`** - Semantic code search
   ```json
   {
     "query": "authentication middleware",
     "file_path": "/path/to/your/project/file.go"
   }
   ```

2. **`hybrid_search`** - Hybrid search (semantic + lexical)
   ```json
   {
     "query": "user login function",
     "file_path": "/path/to/your/project/file.php"
   }
   ```

3. **`get_function_details`** - Complete details about a function
   ```json
   {
     "function_name": "HandleLogin",
     "file_path": "/path/to/your/project/auth.go"
   }
   ```

4. **`find_type_definition`** - Find type/class definition
   ```json
   {
     "type_name": "User",
     "file_path": "/path/to/your/project/models/user.php"
   }
   ```

5. **`find_implementations`** - Find where a function is used
   ```json
   {
     "symbol_name": "ProcessPayment",
     "file_path": "/path/to/your/project/payment.go"
   }
   ```

6. **`list_package_exports`** - List all exports of a package
   ```json
   {
     "package": "github.com/myapp/auth",
     "file_path": "/path/to/your/project/auth/handler.go"
   }
   ```

7. **`search_docs`** - Search in documentation (Markdown)
   ```json
   {
     "query": "API authentication",
     "file_path": "/path/to/your/project/README.md"
   }
   ```

8. **`index_workspace`** - Manually index a workspace
   ```json
   {
     "file_path": "/path/to/your/project/main.go"
   }
   ```

### 📌 **IMPORTANT:** All tools require `file_path`!

RagCode automatically detects the workspace from `file_path`. Ensure you provide a valid path from your project.

---

## 🔄 Automatic Indexing

**RagCode automatically indexes the workspace on first use!**

When you call a tool (e.g., `search_code`) for the first time in a workspace:
1. ✅ Detects workspace from `file_path`
2. ✅ Creates a Qdrant collection for that workspace + language
3. ✅ Indexes code in background
4. ✅ Returns results (even if indexing is not complete)

**You do not need to run `index_workspace` manually** - it happens automatically!

---

## 🛠️ Advanced Configuration

### Change AI Models

Edit `~/.local/share/ragcode/config.yaml`:

```yaml
llm:
  provider: "ollama"
  base_url: "http://localhost:11434"
  model: "phi3:medium"        # Change to another model
  embed_model: "nomic-embed-text"  # Change embedding model
```

Recommended models:
- **LLM:** `phi3:medium`, `llama3.1:8b`, `qwen2.5:7b`
- **Embeddings:** `nomic-embed-text`, `all-minilm`

### Configure Qdrant

```yaml
qdrant:
  url: "http://localhost:6333"
  collection_prefix: "ragcode"
```

### Exclude Directories

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

### Error: "Workspace '/home' is not indexed yet"

**Cause:** You did not provide `file_path` or the path is not in a valid project.

**Solution:**
```json
{
  "query": "search query",
  "file_path": "/path/to/your/actual/project/file.go"  // ← Add this!
}
```

### Error: "Could not connect to Qdrant"

**Cause:** Docker is not running or Qdrant is stopped.

**Solution:**
```bash
# Start Docker
sudo systemctl start docker

# Start Qdrant
~/.local/share/ragcode/start.sh
```

### Error: "Ollama model not found"

**Cause:** AI models are not downloaded.

**Solution:**
```bash
ollama pull phi3:medium
ollama pull nomic-embed-text
```

### Indexing is too slow

**Cause:** Large workspace or slow AI model.

**Solution:**
- Use a smaller model: `phi3:mini` instead of `phi3:medium`
- Exclude large directories in `config.yaml`
- Wait - indexing runs in background

---

## 📚 Usage Examples

### Example 1: Search authentication functions

```json
{
  "query": "user authentication login",
  "file_path": "/home/user/myproject/auth/handler.go"
}
```

### Example 2: Find all methods of a Laravel class

```json
{
  "type_name": "UserController",
  "file_path": "/home/user/laravel-app/app/Http/Controllers/UserController.php"
}
```

### Example 3: Search in documentation

```json
{
  "query": "API endpoints documentation",
  "file_path": "/home/user/myproject/docs/API.md"
}
```

---

## 🔗 Useful Links

- **GitHub:** https://github.com/doITmagic/rag-code-mcp
- **Issues:** https://github.com/doITmagic/rag-code-mcp/issues
- **Ollama Documentation:** https://ollama.com
- **Qdrant Documentation:** https://qdrant.tech

---

## 🤝 Contributions

Contributions are welcome! Open a PR or Issue on GitHub.

---

## 📄 License

MIT License - see `LICENSE` for details.

---

**Questions? Problems?** Open an Issue on GitHub! 🚀
