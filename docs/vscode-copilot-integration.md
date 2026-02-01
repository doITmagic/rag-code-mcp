# VS Code + GitHub Copilot Integration Guide

This guide explains how to integrate RagCode MCP with **Visual Studio Code** and **GitHub Copilot** to enable semantic code search capabilities in Copilot's agent mode.

---

## Overview

RagCode integrates with GitHub Copilot through the **Model Context Protocol (MCP)**, which is a standardized way for AI models to interact with external tools and services. When configured, Copilot can automatically use RagCode's semantic search tools as part of its autonomous coding workflow.

### Key Benefits

- 🔍 **Semantic Code Search** - Find code by meaning, not just keywords
- 🎯 **Context-Aware** - Copilot understands your entire codebase structure
- ⚡ **Automatic Tool Selection** - Copilot chooses the right RagCode tool based on your question
- 🚀 **Enhanced Productivity** - Get accurate answers about your codebase faster

---

## Prerequisites

Before setting up RagCode with VS Code + Copilot, ensure you have:

1. **VS Code** version **1.95 or higher**
   - Check version: Help → About
   - Update if needed: Help → Check for Updates

2. **GitHub Copilot Subscription**
   - Individual, Business, or Enterprise plan
   - Copilot extension installed and activated

3. **RagCode Installed**
   - Use the quick-install script (recommended)
   - Or build from source

4. **Required Services Running**
   - Docker (for Qdrant vector database)
   - Ollama (for AI models)

---

## Installation

### Automatic Setup (Recommended)

The RagCode quick-install script automatically configures VS Code:

```bash
curl -fsSL https://raw.githubusercontent.com/doITmagic/rag-code-mcp/main/quick-install.sh | bash
```

This creates the MCP configuration file at:
```
~/.config/Code/User/globalStorage/mcp-servers.json
```

### Manual Setup

If you need to configure manually or customize the setup:

1. **Create the MCP configuration directory:**
   ```bash
   mkdir -p ~/.config/Code/User/globalStorage
   ```

2. **Create or edit `mcp-servers.json`:**
   ```bash
   nano ~/.config/Code/User/globalStorage/mcp-servers.json
   ```

3. **Add the RagCode configuration:**
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

   **Important:** Replace `YOUR_USERNAME` with your actual Linux username.

4. **Restart VS Code** to load the new configuration.

---

## Verification

### Check MCP Server Status

1. Open **Command Palette** (Ctrl+Shift+P / Cmd+Shift+P)
2. Type: `MCP: Show MCP Servers`
3. Verify that `ragcode` appears in the list
4. Status should show **"Connected"**

### Check Available Tools

In the MCP Servers view, expand the `ragcode` server to see available tools:
- `search_code`
- `hybrid_search`
- `get_function_details`
- `find_type_definition`
- `find_implementations`
- `list_package_exports`
- `search_docs`
- `get_code_context`
- `index_workspace`

---

## Usage

### Enabling Agent Mode

RagCode tools work with **GitHub Copilot's Agent Mode**:

1. Open **Copilot Chat** (Ctrl+Shift+I / Cmd+Shift+I)
2. Click the **"Agent"** button in the chat interface
   - Or type `/agent` in the chat
3. Agent mode is now active (indicated by an icon/badge)

### Asking Questions

Once in agent mode, Copilot will automatically use RagCode tools when appropriate:

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
What are all the API endpoints related to user management?
```

```
Find where the ProcessPayment function is called
```

### Explicit Tool Invocation

You can explicitly reference RagCode tools using the `#` symbol:

```
#ragcode search for payment processing functions
```

```
#ragcode find the UserController type definition
```

```
#ragcode list all exports from the auth package
```

### Example Workflow

1. **Open your project in VS Code**
   ```bash
   cd ~/projects/my-app
   code .
   ```

2. **Index the workspace** (first time only)
   - Open Copilot Chat
   - Ask: "Please index this workspace using RagCode"
   - Wait for confirmation (1-5 minutes depending on project size)

3. **Start asking questions**
   - "Find all HTTP handlers in this project"
   - "Show me the database schema models"
   - "Where is the authentication logic implemented?"

---

## Configuration Options

### Custom Ollama Models

Edit the MCP configuration to use different models:

```json
{
  "mcpServers": {
    "ragcode": {
      "command": "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp",
      "args": [],
      "env": {
        "OLLAMA_BASE_URL": "http://localhost:11434",
        "OLLAMA_MODEL": "llama3.1:8b",          // ← Changed
        "OLLAMA_EMBED": "all-minilm",           // ← Changed
        "QDRANT_URL": "http://localhost:6333"
      }
    }
  }
}
```

**Recommended Models:**
- **LLM:** `phi3:medium`, `llama3.1:8b`, `qwen2.5:7b`, `phi3:mini`
- **Embeddings:** `nomic-embed-text`, `all-minilm`

### Remote Ollama/Qdrant

If running Ollama or Qdrant on a different machine:

```json
{
  "mcpServers": {
    "ragcode": {
      "command": "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp",
      "args": [],
      "env": {
        "OLLAMA_BASE_URL": "http://192.168.1.100:11434",  // ← Remote Ollama
        "OLLAMA_MODEL": "phi3:medium",
        "OLLAMA_EMBED": "nomic-embed-text",
        "QDRANT_URL": "http://192.168.1.101:6333"         // ← Remote Qdrant
      }
    }
  }
}
```

### Logging and Debugging

Enable detailed logging for troubleshooting:

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
        "QDRANT_URL": "http://localhost:6333",
        "MCP_LOG_LEVEL": "debug",                         // ← Enable debug logs
        "MCP_LOG_FILE": "/tmp/ragcode-mcp.log"           // ← Log file path
      }
    }
  }
}
```

View logs:
```bash
tail -f /tmp/ragcode-mcp.log
```

---

## Troubleshooting

### MCP Server Not Showing

**Problem:** RagCode doesn't appear in the MCP Servers list.

**Solutions:**
1. Verify config file exists:
   ```bash
   cat ~/.config/Code/User/globalStorage/mcp-servers.json
   ```

2. Check VS Code version (must be 1.95+):
   - Help → About

3. Restart VS Code completely:
   - Close all windows
   - Reopen VS Code

4. Check for JSON syntax errors in config file

### Connection Failed

**Problem:** MCP server shows "Disconnected" or "Error" status.

**Solutions:**
1. Verify binary exists and is executable:
   ```bash
   ls -la ~/.local/share/ragcode/bin/rag-code-mcp
   chmod +x ~/.local/share/ragcode/bin/rag-code-mcp
   ```

2. Check Qdrant is running:
   ```bash
   docker ps | grep qdrant
   ```
   If not running:
   ```bash
   ~/.local/share/ragcode/start.sh
   ```

3. Check Ollama is running:
   ```bash
   ollama list
   ```
   If not running:
   ```bash
   ollama serve
   ```

4. Test RagCode manually:
   ```bash
   ~/.local/share/ragcode/bin/rag-code-mcp --version
   ```

### Tools Not Working

**Problem:** Copilot doesn't use RagCode tools or tools fail.

**Solutions:**
1. **Ensure you're in Agent Mode:**
   - Look for the "Agent" indicator in Copilot Chat
   - Click "Agent" button or type `/agent`

2. **Index your workspace first:**
   - Ask Copilot: "Please index this workspace using RagCode"
   - Wait for confirmation

3. **Check MCP logs:**
   ```bash
   # Add logging to config (see Configuration Options above)
   tail -f /tmp/ragcode-mcp.log
   ```

4. **Verify services are running:**
   ```bash
   docker ps | grep qdrant
   ollama list
   ```

5. **Try explicit tool invocation:**
   ```
   #ragcode search for authentication functions
   ```

### Workspace Not Indexed

**Problem:** Error message "Workspace '/path' is not indexed yet"

**Solutions:**
1. **Index the workspace:**
   - In Copilot Chat: "Please index this workspace using RagCode"
   - Or manually: Ask Copilot to call `index_workspace` with a file path from your project

2. **Verify file_path is correct:**
   - Tools require a `file_path` parameter from your project
   - Copilot should automatically provide this

3. **Check indexing status:**
   - Indexing runs in background
   - Wait 1-5 minutes for large projects
   - You can start searching immediately (results improve as indexing progresses)

### Slow Performance

**Problem:** RagCode tools are slow to respond.

**Solutions:**
1. **Use a smaller/faster model:**
   ```json
   "OLLAMA_MODEL": "phi3:mini"  // Instead of phi3:medium
   ```

2. **Exclude large directories:**
   Edit `~/.local/share/ragcode/config.yaml`:
   ```yaml
   workspace:
     exclude_patterns:
       - "node_modules"
       - "vendor"
       - "dist"
       - "build"
       - ".git"
   ```

3. **Check system resources:**
   - Ollama requires significant RAM (8GB+ recommended)
   - Consider using GPU acceleration if available

---

## Advanced Features

### Multi-Workspace Support

RagCode automatically handles multiple projects:

1. Each project gets its own Qdrant collection
2. Collections are named: `ragcode-{workspace-id}-{language}`
3. RagCode auto-detects workspace from file paths
4. No manual switching needed

**Example:**
```bash
# Project 1
cd ~/projects/backend-api
code .
# Ask Copilot to index → creates ragcode-abc123-go

# Project 2
cd ~/projects/frontend-app
code .
# Ask Copilot to index → creates ragcode-def456-javascript
```

### Language-Specific Collections

RagCode creates separate collections per language:
- `ragcode-{workspace}-go`
- `ragcode-{workspace}-php`
- `ragcode-{workspace}-python`
- `ragcode-{workspace}-javascript`

This improves search accuracy and performance.

### Custom Instructions

You can create `.github/copilot-instructions.md` in your project to guide Copilot's use of RagCode:

```markdown
# Copilot Instructions

When searching for code:
- Always use RagCode semantic search first
- Prefer `hybrid_search` for finding specific function names
- Use `get_function_details` to understand implementation details
- Index the workspace on first use
```

---

## Comparison with Other IDEs

| Feature | VS Code + Copilot | Windsurf/Cursor | Antigravity |
|---------|-------------------|-----------------|-------------|
| MCP Support | ✅ Agent Mode | ✅ Native | ✅ Native |
| Auto Tool Selection | ✅ | ✅ | ✅ |
| Explicit Tool Invocation | ✅ (#ragcode) | ✅ | ✅ |
| Configuration | JSON file | JSON file | JSON file |
| Setup Complexity | Medium | Easy | Easy |

**VS Code Advantages:**
- Familiar environment for many developers
- Extensive extension ecosystem
- Integrated debugging and Git tools
- Free and open source

**Considerations:**
- Requires Copilot subscription
- Agent mode is newer (may have rough edges)
- MCP support requires VS Code 1.95+

---

## Resources

- **RagCode GitHub:** https://github.com/doITmagic/rag-code-mcp
- **VS Code MCP Docs:** https://code.visualstudio.com/docs/copilot/copilot-extensibility-overview
- **GitHub Copilot Docs:** https://docs.github.com/copilot
- **Model Context Protocol:** https://modelcontextprotocol.io
- **Ollama:** https://ollama.com
- **Qdrant:** https://qdrant.tech

---

## Support

If you encounter issues:

1. **Check the troubleshooting section** above
2. **Review MCP logs** (enable debug logging)
3. **Open an issue:** https://github.com/doITmagic/rag-code-mcp/issues
4. **Include:**
   - VS Code version
   - RagCode version
   - Error messages
   - MCP configuration
   - Log excerpts

---

**Happy coding with RagCode + VS Code + Copilot! 🚀**
