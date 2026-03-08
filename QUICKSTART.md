# RagCode MCP: The Vibe Coder Path

**Get semantic code search in your IDE in under 5 minutes.**

RagCode is an MCP server that enables autonomous AI assistants (Copilot, Cursor, Windsurf, Claude) to understand your codebase deeply through semantic search. This guide skips the technical details and gets you running immediately.

---

## 1. Verify Prerequisites
Ensure you have Docker installed and running on your machine. Everything else will be downloaded automatically.

## 2. Run the Installer
Run the 1-command installer for your OS. It will download the binary, configure your IDEs, pull the embedding models, and start the local vector database.

**Linux (amd64):**
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_linux_amd64.tar.gz | tar xz && ./rag-code-install -ollama=docker -qdrant=docker
```

**macOS (Apple Silicon):**
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_darwin_arm64.tar.gz | tar xz && ./rag-code-install -ollama=docker -qdrant=docker
```

**macOS (Intel):**
```bash
curl -fsSL https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_darwin_amd64.tar.gz | tar xz && ./rag-code-install -ollama=docker -qdrant=docker
```

**Windows (PowerShell):**
```powershell
Invoke-WebRequest -Uri "https://github.com/doITmagic/rag-code-mcp/releases/latest/download/rag-code-mcp_windows_amd64.zip" -OutFile "ragcode.zip"
Expand-Archive ragcode.zip -DestinationPath . -Force
.\rag-code-install.exe -ollama=docker -qdrant=docker
```
> Note: Windows requires Docker Desktop installed and running.

---

## 3. Your First Query (Zero Config)

RagCode is now available in your IDE (Windsurf, Cursor, Antigravity, Claude Desktop). 

1. **Open your project folder** in your AI IDE.
2. **Open the AI Chat** and type your first prompt:
   ```
   Please use RagCode to find all authentication functions in this codebase.
   ```
3. **Wait 2 seconds**. The AI will automatically call `rag_search`. If the codebase has never been indexed before, RagCode will use its **Fallback AST Search** instantly while Qdrant indexes your files in the background.

There is no step 4. You are done!

---

## Common Variations

**Using WSL on Windows**
If you run Docker via WSL, run the Linux command inside WSL. Then configure your Windows IDE manually (e.g., Windsurf at `%USERPROFILE%\.codeium\windsurf\mcp_config.json`):
```json
{
  "mcpServers": {
    "ragcode": {
      "command": "wsl.exe",
      "args": ["-e", "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp"],
      "env": {
        "OLLAMA_BASE_URL": "http://localhost:11434",
        "OLLAMA_EMBED": "qwen3-embedding:0.6b",
        "QDRANT_URL": "http://localhost:6333"
       }
    }
  }
}
```

**Using Local Ollama instead of Docker**
If you already run Ollama on macOS or Linux natively:
```bash
./rag-code-install -ollama=local -qdrant=docker
```

---

## What's Next?
* Need to use RagCode algorithmically from a script? Read [Headless API Usage](./docs/headless-usage.md).
* Want to contribute or build from source? Read the [Developer Path](./CONTRIBUTING.md).
