# 🚀 RagCode MCP - Quick Start

**Get semantic code search in your IDE in under 5 minutes.**

RagCode is an MCP server that enables AI assistants (Copilot, Cursor, Windsurf, Claude) to understand your codebase through semantic search. Runs 100% locally.

---

## 📦 Install

### Linux
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_linux_amd64.tar.gz | tar xz && ./ragcode-installer -ollama=docker -qdrant=docker
```

### macOS (Apple Silicon)
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_darwin_arm64.tar.gz | tar xz && ./ragcode-installer -ollama=docker -qdrant=docker
```

### macOS (Intel)
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_darwin_amd64.tar.gz | tar xz && ./ragcode-installer -ollama=docker -qdrant=docker
```

### Windows (PowerShell)
```powershell
Invoke-WebRequest -Uri "https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_windows_amd64.zip" -OutFile "ragcode.zip"
Expand-Archive ragcode.zip -DestinationPath . -Force
.\ragcode-installer.exe -ollama=docker -qdrant=docker
```

### Windows with WSL (alternative)

If you run Docker via WSL and have IDEs on Windows:

```bash
# Inside WSL terminal
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_linux_amd64.tar.gz | tar xz && ./ragcode-installer -ollama=docker -qdrant=docker
```

Then configure your Windows IDE manually (e.g., Windsurf at `%USERPROFILE%\.codeium\windsurf\mcp_config.json`):

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

> **Prerequisite:** Docker must be installed and running.

---

## ✅ Verify Installation

```bash
# Check binary
~/.local/share/ragcode/bin/rag-code-mcp --version

# Check services
docker ps | grep ragcode
```

---

## 🎯 Start Using

1. **Open your project** in Windsurf, Cursor, or VS Code
2. **Ask your AI assistant:**
   ```
   Find all authentication functions in this codebase
   ```
3. **RagCode automatically indexes** your project on first use

That's it! The AI will use RagCode's semantic search to find relevant code.

---

## 🔧 Common Options

```bash
# Use local Ollama instead of Docker
./ragcode-installer -ollama=local -qdrant=docker

# Enable GPU acceleration
./ragcode-installer -ollama=docker -qdrant=docker -gpu

# Re-configure IDEs without rebuilding
./ragcode-installer -skip-build
```

---

## 📚 Next Steps

- **[README.md](./README.md)** - Full documentation, all features, configuration
- **[docs/vscode-copilot-integration.md](./docs/vscode-copilot-integration.md)** - VS Code + Copilot setup
- **[docs/architecture.md](./docs/architecture.md)** - Technical details

---

## 🐛 Quick Troubleshooting

| Problem | Solution |
|---------|----------|
| "Could not connect to Qdrant" | Run `docker start ragcode-qdrant` |
| "Ollama model not found" | Run `ollama pull phi3:medium && ollama pull nomic-embed-text` |
| IDE doesn't see RagCode | Re-run `./ragcode-installer -skip-build` |

For more help, see [README.md#troubleshooting](./README.md#-troubleshooting) or open an [Issue](https://github.com/doITmagic/rag-code-mcp/issues).

---

**Questions?** Open an Issue on [GitHub](https://github.com/doITmagic/rag-code-mcp/issues) 🚀
