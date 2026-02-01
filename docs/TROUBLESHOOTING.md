# 🐛 RagCode Troubleshooting Guide

Solutions for common issues with RagCode MCP server.

---

## Quick Fixes

| Problem | Quick Solution |
|---------|----------------|
| "Could not connect to Qdrant" | `docker start ragcode-qdrant` |
| "Ollama model not found" | `ollama pull phi3:medium && ollama pull nomic-embed-text` |
| IDE doesn't see RagCode | Re-run `./ragcode-installer -skip-build` |
| Indexing stuck | Check logs: `tail -f ~/.local/share/ragcode/bin/mcp.log` |

---

## Detailed Solutions

### "Workspace '/home' is not indexed yet"

**Cause:** The `file_path` parameter is missing or points outside a recognized project.

**Solution:** Provide a valid `file_path` inside your project:
```json
{ 
  "query": "search query", 
  "file_path": "/path/to/your/project/file.go" 
}
```

**Why this happens:** RagCode uses `file_path` to detect which workspace you're working in. Without it, it defaults to `/home` which is not a valid project.

---

### "Could not connect to Qdrant"

**Cause:** Docker is not running or the Qdrant container is stopped.

**Solution:**
```bash
# Start Docker (Linux)
sudo systemctl start docker

# Start Qdrant container
docker start ragcode-qdrant

# Or restart everything
~/.local/share/ragcode/start.sh
```

**Verify:**
```bash
docker ps | grep qdrant
# Should show: ragcode-qdrant ... Up ...
```

---

### "Ollama model not found"

**Cause:** Required AI models have not been downloaded.

**Solution:**
```bash
# Download embedding model
ollama pull nomic-embed-text

# Download LLM model
ollama pull phi3:medium

# Verify
ollama list
```

**If using Docker Ollama:**
```bash
docker exec ragcode-ollama ollama pull nomic-embed-text
docker exec ragcode-ollama ollama pull phi3:medium
```

---

### Indexing is Too Slow

**Causes:**
- Large workspace with many files
- Heavy LLM model
- Insufficient RAM/CPU

**Solutions:**

1. **Use a smaller model:**
   ```yaml
   # In config.yaml
   llm:
     model: "phi3:mini"  # Instead of phi3:medium
   ```

2. **Exclude large directories:**
   ```yaml
   workspace:
     exclude_patterns:
       - "vendor"
       - "node_modules"
       - ".git"
       - "dist"
       - "build"
       - "*.min.js"
       - "*.bundle.js"
   ```

3. **Wait for background indexing:**
   - Indexing runs in the background
   - First query may be slow, subsequent queries are fast
   - Check progress: `tail -f ~/.local/share/ragcode/bin/mcp.log`

---

### IDE Doesn't Detect RagCode

**Cause:** MCP configuration file is missing or incorrect.

**Solution 1: Re-run installer**
```bash
~/.local/share/ragcode/bin/ragcode-installer -skip-build -ollama=local -qdrant=docker
```

**Solution 2: Manual configuration**

Check your IDE's config file exists and has correct content:

| IDE | Config Path |
|-----|-------------|
| Windsurf | `~/.codeium/windsurf/mcp_config.json` |
| Cursor | `~/.cursor/mcp.config.json` |
| VS Code | `~/.config/Code/User/globalStorage/mcp-servers.json` |
| Claude Desktop | `~/.config/Claude/mcp-servers.json` |

See [IDE-SETUP.md](./IDE-SETUP.md) for complete configuration examples.

---

### "Empty embedding returned"

**Cause:** Ollama embedding model is not responding correctly.

**Solution:**
```bash
# Restart Ollama
docker restart ragcode-ollama
# or
systemctl restart ollama

# Test embedding model
curl http://localhost:11434/api/embeddings -d '{
  "model": "nomic-embed-text",
  "prompt": "test"
}'
```

---

### High Memory Usage

**Cause:** Large models or multiple workspaces indexed.

**Solutions:**

1. **Use smaller models:**
   - `phi3:mini` instead of `phi3:medium`
   - `all-minilm` instead of `nomic-embed-text`

2. **Limit concurrent operations:**
   - Index one workspace at a time
   - Close unused IDE windows

3. **Increase swap (Linux):**
   ```bash
   sudo fallocate -l 8G /swapfile
   sudo chmod 600 /swapfile
   sudo mkswap /swapfile
   sudo swapon /swapfile
   ```

---

### Docker Permission Denied

**Cause:** User not in docker group.

**Solution (Linux):**
```bash
sudo usermod -aG docker $USER
# Log out and log back in
```

**Solution (macOS):**
- Ensure Docker Desktop is running
- Check Docker Desktop settings → Resources

---

### Search Returns No Results

**Causes:**
- Workspace not indexed yet
- Wrong language filter
- Query too specific

**Solutions:**

1. **Check if indexed:**
   ```bash
   # Look for collection in Qdrant
   curl http://localhost:6333/collections | jq
   ```

2. **Force re-index:**
   - Use `index_workspace` tool with `file_path` parameter

3. **Try broader query:**
   - Instead of exact function name, describe what it does
   - Use `search_code` before `hybrid_search`

---

### Windows-Specific Issues

#### WSL Path Issues
**Problem:** Windows IDE can't find WSL binary.

**Solution:** Use `wsl.exe` wrapper in config:
```json
{
  "mcpServers": {
    "ragcode": {
      "command": "wsl.exe",
      "args": ["-e", "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp"]
    }
  }
}
```

#### Docker Desktop Not Running
**Problem:** "Cannot connect to Docker daemon"

**Solution:**
1. Start Docker Desktop application
2. Wait for it to fully initialize (check system tray icon)
3. Run installer again

---

## 📊 Diagnostic Commands

```bash
# Check RagCode version
~/.local/share/ragcode/bin/rag-code-mcp --version

# Health check
~/.local/share/ragcode/bin/rag-code-mcp --health

# Check Docker containers
docker ps | grep ragcode

# Check Ollama models
ollama list

# Check Qdrant collections
curl http://localhost:6333/collections | jq

# View logs
tail -100 ~/.local/share/ragcode/bin/mcp.log

# Test Ollama connection
curl http://localhost:11434/api/tags

# Test Qdrant connection
curl http://localhost:6333/collections
```

---

## 🔗 Getting Help

If your issue isn't listed here:

1. **Check logs:** `tail -f ~/.local/share/ragcode/bin/mcp.log`
2. **Search issues:** [GitHub Issues](https://github.com/doITmagic/rag-code-mcp/issues)
3. **Open new issue:** Include logs, OS, and steps to reproduce

---

## 🔗 Related Documentation

- **[Quick Start](../QUICKSTART.md)** - Installation guide
- **[Configuration](./CONFIGURATION.md)** - Settings and options
- **[IDE Setup](./IDE-SETUP.md)** - Manual IDE configuration
