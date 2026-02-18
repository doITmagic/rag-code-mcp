# IDE Security Configuration for RagCode MCP

This guide shows how to configure workspace security settings directly in your IDE's MCP configuration, without needing to edit config files.

## Problem

The RagCode MCP tool is installed in `~/.local/share/ragcode/` where users don't have easy access to edit `config.yaml`. However, users CAN easily edit their IDE's MCP configuration files.

## Solution

Use command-line flags in your IDE's MCP configuration to control workspace security settings.

---

## Security Flags Available

### 1. `-allowed-paths` 
**Restricts workspaces to specific directories**

```bash
-allowed-paths "~/projects,~/work,/opt/code"
```

- Only paths within these directories are allowed as workspaces
- Prevents accidental scanning of Home directory, Desktop, Documents, etc.
- Multiple paths separated by commas
- Tilde (`~`) is automatically expanded to Home directory

**Use when:** You want to limit tool to specific project folders only.

---

### 2. `-disable-upward-search`
**Disables automatic parent directory search**

```bash
-disable-upward-search
```

- Tool will ONLY check the exact directory provided for workspace markers
- Won't walk up through parent directories looking for `.git`, `go.mod`, etc.
- Provides strictest control

**Use when:** You want explicit control and no automatic traversal.

---

## IDE Configuration Examples

### Cursor (`~/.cursor/mcp.config.json`)

#### Secure: Restrict to ~/projects only
```json
{
  "mcpServers": {
    "ragcode": {
      "command": "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp",
      "args": ["-allowed-paths", "~/projects"]
    }
  }
}
```

#### Maximum Security: Restrict + Disable Search
```json
{
  "mcpServers": {
    "ragcode": {
      "command": "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp",
      "args": [
        "-allowed-paths", "~/projects,~/work",
        "-disable-upward-search"
      ]
    }
  }
}
```


---

### Windsurf (`~/.codeium/windsurf/mcp_config.json`)

```json
{
  "mcpServers": {
    "ragcode": {
      "command": "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp",
      "args": ["-allowed-paths", "~/projects,~/code"]
    }
  }
}
```

---

### VS Code + Copilot (`~/.config/Code/User/globalStorage/mcp-servers.json`)

```json
{
  "mcpServers": {
    "ragcode": {
      "command": "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp",
      "args": [
        "-allowed-paths", "~/projects",
        "-disable-upward-search"
      ]
    }
  }
}
```

---

### Claude Desktop (macOS: `~/Library/Application Support/Claude/mcp-servers.json`)

```json
{
  "mcpServers": {
    "ragcode": {
      "command": "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp",
      "args": ["-allowed-paths", "~/projects,~/work"]
    }
  }
}
```

---

### Windows with WSL

```json
{
  "mcpServers": {
    "ragcode": {
      "command": "wsl.exe",
      "args": [
        "-e",
        "/home/YOUR_USERNAME/.local/share/ragcode/bin/rag-code-mcp",
        "-allowed-paths",
        "~/projects,~/work"
      ]
    }
  }
}
```

---

## Testing Your Configuration

### 1. Test that the tool starts
```bash
~/.local/share/ragcode/bin/rag-code-mcp -allowed-paths "~/projects" -version
```

### 2. Test with a valid path
Create a test project in your allowed path:
```bash
mkdir -p ~/projects/test-project
cd ~/projects/test-project
git init
echo "package main" > main.go
```

### 3. Test with an invalid path
Try from a disallowed location (should fail):
```bash
cd ~/Desktop
# IDE queries here should be rejected
```

---

## Behavior Examples

### Scenario 1: Allowed paths set to `~/projects`

✅ **ALLOWED:**
- `~/projects/myapp/main.go` → Works
- `~/projects/backend/src/handler.go` → Works
- `~/projects/frontend/app.js` → Works

❌ **REJECTED:**
- `~/Desktop/script.py` → "Not within allowed workspace paths"
- `~/Documents/code/app.go` → "Not within allowed workspace paths"
- `~/Downloads/project/main.go` → "Not within allowed workspace paths"

---

### Scenario 2: Upward search disabled

✅ **ALLOWED:**
- File in `~/projects/myapp/main.go` with `.git` in `~/projects/myapp/` → Works

❌ **REJECTED:**
- File in `~/projects/myapp/src/handler.go` but `.git` in `~/projects/myapp/` → Fails
  (because it won't search up to find `.git`)

**Solution:** Always run from the project root when upward search is disabled.

---

### Scenario 3: Explicit path required

✅ **ALLOWED:**
- Tool call with `file_path: "~/projects/myapp/main.go"` → Works

❌ **REJECTED:**
- Tool call without `file_path` parameter → "No file_path parameter provided"

---

## Recommended Configurations

### For Personal Use (Balanced Security)
```json
{
  "args": ["-allowed-paths", "~/projects,~/code,~/work"]
}
```

### For Enterprise/Shared Machines (High Security)
```json
{
  "args": [
    "-allowed-paths", "~/projects",
    "-disable-upward-search"
  ]
}
```

### For Development/Testing (Default Behavior)
```json
{
  "args": []
}
```
No args = default behavior, maximum flexibility

---

## Troubleshooting

### Error: "path is not within allowed workspace paths"

**Cause:** File is outside your configured allowed paths

**Solution:** 
1. Add the directory to `-allowed-paths`: `-allowed-paths "~/projects,~/newdir"`
2. Or move your project to an allowed directory

---

### Error: "no workspace markers found"

**Cause:** Your project doesn't have workspace markers and upward search is disabled

**Solution:**
1. Initialize your project: `git init` or `go mod init` or `npm init`
2. Or remove `-disable-upward-search` flag

---

## Advanced: Multiple Allowed Paths

You can specify multiple directories:

```json
{
  "args": [
    "-allowed-paths",
    "~/projects,~/work,~/opensource,/opt/company-code"
  ]
}
```

All of these will be allowed:
- `~/projects/app1/`
- `~/work/project-x/`
- `~/opensource/contributions/`
- `/opt/company-code/internal/`

---

## Why This Approach?

### ✅ Advantages
1. **Easy to configure** - IDE config files are user-accessible
2. **No file editing** - No need to find/edit `~/.local/share/ragcode/config.yaml`
3. **IDE-specific** - Different settings for different IDEs
4. **Version control** - Can commit IDE configs to team repositories
5. **Transparent** - Settings are visible in IDE configuration

### 🎯 Best Practice
Configure security settings in your IDE's MCP configuration rather than modifying the installed config file.

---

## See Also

- [Main IDE Setup Guide](./IDE-SETUP.md)
- [Configuration Guide](./CONFIGURATION.md)
- [Troubleshooting](./TROUBLESHOOTING.md)
