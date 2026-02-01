# 🖥️ IDE Setup Guide

Manual configuration for RagCode MCP in various IDEs and AI assistants.

> **Note:** The `ragcode-installer` automatically configures most IDEs. Use this guide only if you need manual setup or installed an IDE after running the installer.

---

## 📍 Configuration File Locations

| IDE | Config File Path |
|-----|------------------|
| **Windsurf** | `~/.codeium/windsurf/mcp_config.json` |
| **Cursor** | `~/.cursor/mcp.config.json` |
| **Antigravity** | `~/.gemini/antigravity/mcp_config.json` |
| **Claude Desktop (Linux)** | `~/.config/Claude/mcp-servers.json` |
| **Claude Desktop (macOS)** | `~/Library/Application Support/Claude/mcp-servers.json` |
| **Claude Desktop (Windows)** | `%APPDATA%\Claude\mcp-servers.json` |
| **VS Code + Copilot** | `~/.config/Code/User/globalStorage/mcp-servers.json` |

---

## 🔧 Standard Configuration

Add this to your IDE's MCP configuration file (create the file if it doesn't exist):

### Linux / macOS

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

> **Important:** Replace `YOUR_USERNAME` with your actual system username.
> 
> On macOS, the path is typically `/Users/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp`

### Windows (Native)

```json
{
  "mcpServers": {
    "ragcode": {
      "command": "C:\\Users\\YOUR_USERNAME\\.local\\share\\ragcode\\bin\\rag-code-mcp.exe",
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

### Windows with WSL

If you run RagCode inside WSL but use Windows IDEs:

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

> **Note:** The `localhost` URLs work because WSL2 shares network ports with Windows.

---

## 🎯 IDE-Specific Instructions

### Windsurf

1. Open config file: `~/.codeium/windsurf/mcp_config.json`
2. Add the configuration above
3. Restart Windsurf
4. Check MCP status in Windsurf settings

### Cursor

1. Open config file: `~/.cursor/mcp.config.json`
2. Add the configuration above
3. Restart Cursor
4. Verify in Cursor's MCP panel

### VS Code + GitHub Copilot

**Requirements:**
- VS Code 1.95 or newer
- GitHub Copilot extension
- GitHub Copilot Chat extension

**Setup:**

1. Create/edit `~/.config/Code/User/globalStorage/mcp-servers.json`
2. Add the configuration above
3. Restart VS Code
4. Open Copilot Chat (Ctrl+Shift+I / Cmd+Shift+I)
5. Enable **Agent Mode** (click "Agent" button)

**Verify Integration:**
- Command Palette → `MCP: Show MCP Servers`
- Check that `ragcode` appears with "Connected" status

**Example Prompts:**
```
Find all authentication middleware functions in this codebase
Show me the User model definition and all its methods
Search for functions that handle database connections
```

📖 **Detailed Guide:** See [vscode-copilot-integration.md](./vscode-copilot-integration.md)

### Claude Desktop

**Linux:**
```bash
# Create config directory if needed
mkdir -p ~/.config/Claude

# Edit config
nano ~/.config/Claude/mcp-servers.json
```

**macOS:**
```bash
# Edit config
nano ~/Library/Application\ Support/Claude/mcp-servers.json
```

**Windows:**
- Open `%APPDATA%\Claude\mcp-servers.json` in a text editor

### Antigravity

1. Open config file: `~/.gemini/antigravity/mcp_config.json`
2. Add the configuration above
3. Restart Antigravity

---

## 🔄 Re-running the Installer

If you install a new IDE after initial setup, you can re-run the installer to auto-configure it:

```bash
~/.local/share/ragcode/bin/ragcode-installer -skip-build -ollama=local -qdrant=docker
```

The `-skip-build` flag skips binary compilation and only updates IDE configurations.

---

## ✅ Verifying Setup

After configuration, verify RagCode is working:

1. **Open your project** in the IDE
2. **Ask your AI assistant:**
   ```
   Use the search_code tool to find authentication functions
   ```
3. **Check for RagCode response** - you should see results from semantic search

### Troubleshooting Verification

If RagCode doesn't respond:

1. **Check config file syntax:**
   ```bash
   cat ~/.codeium/windsurf/mcp_config.json | jq .
   ```

2. **Check binary exists:**
   ```bash
   ls -la ~/.local/share/ragcode/bin/rag-code-mcp
   ```

3. **Check services running:**
   ```bash
   docker ps | grep ragcode
   ```

4. **Check logs:**
   ```bash
   tail -f ~/.local/share/ragcode/bin/mcp.log
   ```

---

## 🔗 Related Documentation

- **[Quick Start](../QUICKSTART.md)** - Installation guide
- **[Configuration](./CONFIGURATION.md)** - Settings and environment variables
- **[Troubleshooting](./TROUBLESHOOTING.md)** - Common problems and solutions
- **[VS Code + Copilot](./vscode-copilot-integration.md)** - Detailed Copilot setup
