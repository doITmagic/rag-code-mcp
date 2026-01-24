# Explicit Workspace Root Parameter

## Overview

As suggested by @doITmagic, the `index_workspace` tool now supports an **explicit `workspace_root` parameter** that allows the AI (Copilot/Cursor/Windsurf) to specify the project root directly for indexing, completely bypassing automatic workspace detection.

This provides:
- ✅ **Better security** - No automatic directory traversal during indexing
- ✅ **Explicit control** - AI knows exactly which project to index
- ✅ **Faster operation** - Skips detection logic
- ✅ **No permission prompts** - Never triggers macOS Home folder access

## Why This Approach?

**Original question**: "Why doesn't the AI just give us the workspace root path directly?"

**Answer**: You're absolutely right! The AI (Copilot/Cursor/Windsurf) knows the project root. For indexing operations, it makes more sense for the AI to tell us explicitly which project to index, rather than us trying to guess by searching upward through directories.

### For Indexing (index_workspace)

**Before (Automatic Detection)**:
```json
{
  "tool": "index_workspace",
  "file_path": "/home/user/projects/myapp/src/handlers/auth.go"
}
```
Tool searches UP from `auth.go` → finds `.git` in `/home/user/projects/myapp/` → indexes that project

**After (Explicit Root)** - RECOMMENDED:
```json
{
  "tool": "index_workspace",
  "workspace_root": "/home/user/projects/myapp"
}
```
Tool uses `/home/user/projects/myapp` directly → No searching needed!

### For Search Operations (search_code, etc.)

Search operations DON'T need `workspace_root` because:
1. The workspace is already indexed with a unique ID
2. Tool detects workspace from `file_path` and looks up in cache
3. Searching in already-indexed collections is instant

**Example**:
```json
{
  "tool": "search_code",
  "query": "authentication",
  "file_path": "/home/user/projects/myapp/src/auth.go"
}
```
- Tool detects workspace from `file_path`
- Looks up cached workspace ID
- Searches in appropriate collection
- Fast and secure!

## Usage: index_workspace Tool

### Schema

```json
{
  "tool": "index_workspace",
  "params": {
    "workspace_root": "string (optional)",  // NEW: Explicit project root
    "file_path": "string (optional)",        // Fallback: Any file in project
    "language": "string (optional)"          // Specific language to index
  }
}
```

**Required**: At least ONE of `workspace_root` OR `file_path` must be provided.

### Example 1: Index with Explicit Root (Recommended)

```json
{
  "tool": "index_workspace",
  "params": {
    "workspace_root": "/home/user/projects/myapp"
  }
}
```

✅ No directory traversal  
✅ No permission prompts  
✅ Fast and explicit  

### Example 2: Index with File Path (Fallback)

```json
{
  "tool": "index_workspace",
  "params": {
    "file_path": "/home/user/projects/myapp/main.go"
  }
}
```

⚠️ Searches upward to find workspace root  
⚠️ May trigger permission prompts on macOS if misconfigured  

### Example 3: Index Specific Language

```json
{
  "tool": "index_workspace",
  "params": {
    "workspace_root": "/home/user/projects/myapp",
    "language": "go"
  }
}
```

Only indexes Go code in the project.

## Security Benefits

### 1. No Home Directory Traversal

**Problem**: Automatic detection could trigger traversal of Home directory
```
File: ~/Desktop/script.py
Detection: Searches up → reaches ~ → triggers macOS permission prompt
```

**Solution**: With explicit root, this never happens
```json
{
  "workspace_root": "/home/user/projects/myapp"
}
```
Only `myapp` directory is accessed during indexing.

### 2. Works with CLI Security Flags

Combine with `-allowed-paths` for maximum security:

```bash
rag-code-mcp -allowed-paths "/home/user/projects,/home/user/work"
```

Then AI can only index workspaces within allowed paths:
```json
{
  "workspace_root": "/home/user/projects/backend"  // ✅ Allowed
}
```
```json
{
  "workspace_root": "/home/user/Desktop"  // ❌ Rejected
}
```

### 3. Explicit is Better Than Implicit

The Zen of Python applies here:
- **Explicit is better than implicit**
- **Simple is better than complex**

Letting AI provide the workspace root explicitly is:
- More secure (no surprises)
- More reliable (no guessing)
- Easier to understand (clear intent)

## Implementation Details

### Priority Order in index_workspace

When `index_workspace` is called:

1. **Check for `workspace_root`** (highest priority)
   - If present, use it directly
   - Still validates path (security checks, marker validation)
   - Skips all automatic detection

2. **Check for `file_path`** (fallback)
   - If no `workspace_root`, use `file_path`
   - Triggers automatic detection (upward search)
   - Subject to all security restrictions

3. **Error if neither provided**
   - Tool requires at least one parameter

### Validation

Even with explicit `workspace_root`, the tool still validates:

✅ Path exists and is accessible  
✅ Within `allowed_paths` (if configured)  
✅ Not `/`, `/tmp`, or Home directory (unless has markers)  
✅ Has valid workspace markers (`.git`, `go.mod`, etc.)  

### Caching

After indexing, workspace info is cached:
- Key: workspace root path
- Value: workspace Info (ID, languages, collections)
- Duration: Permanent (until server restart)

Search operations use this cache to avoid re-detection.

## AI Integration Recommendations

### For AI/LLM Providers

When building MCP integrations for `index_workspace`:

1. **Always provide `workspace_root`** when known
2. **Detect project root** from IDE/editor context
3. **Use IDE APIs** to get project root reliably

### Example Pseudo-code

```python
class MCPClient:
    def index_workspace(self):
        # Get project root from IDE
        project_root = self.get_project_root_from_ide()
        
        # Call index_workspace with explicit root
        return self.mcp_server.execute('index_workspace', {
            'workspace_root': project_root
        })
    
    def get_project_root_from_ide(self):
        # Use IDE/editor API to get project root
        # e.g., VSCode: workspace.workspaceFolders[0].uri.fsPath
        # e.g., JetBrains: project.basePath
        # e.g., Vim: getcwd() with project detection
        return get_project_root_from_ide_api()
```

### VS Code Extension Example

```typescript
const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

const result = await mcpClient.callTool('index_workspace', {
    workspace_root: workspaceRoot  // Explicit root from VS Code
});
```

### Cursor/Windsurf Example

```typescript
const projectRoot = getCurrentProjectRoot();

await mcp.execute({
    tool: 'index_workspace',
    params: {
        workspace_root: projectRoot  // Explicit root from IDE
    }
});
```

## Why Only for index_workspace?

**Q**: Why not add `workspace_root` to search tools (`search_code`, `get_function_details`, etc.)?

**A**: Search operations don't need it because:

1. **Workspace already indexed** - Each workspace has a unique ID
2. **Fast lookup** - Tool detects workspace from `file_path` and uses cached ID
3. **Simpler API** - Less parameters for AI to manage
4. **No filesystem access** - Search only queries vector database

The filesystem traversal risk ONLY exists during **indexing**, not during **searching**.

## Comparison: Indexing vs Searching

| Operation | Needs workspace_root? | Filesystem Access? | Why? |
|-----------|----------------------|-------------------|------|
| **index_workspace** | ✅ Yes (recommended) | ✅ Yes (walks entire project) | Must scan all files to index |
| **search_code** | ❌ No | ❌ No (only DB query) | Searches already-indexed data |
| **get_function_details** | ❌ No | ❌ No (only DB query) | Looks up in indexed collection |
| **find_type_definition** | ❌ No | ❌ No (only DB query) | Searches indexed types |

## Backward Compatibility

The `workspace_root` parameter is **optional**. Existing behavior is preserved:

- `index_workspace` without `workspace_root` → automatic detection (as before)
- `index_workspace` with `workspace_root` → explicit root (new behavior)
- No breaking changes to existing integrations

## Testing

### Test with explicit root:

```bash
# Create test project
mkdir -p /tmp/test-project
cd /tmp/test-project
git init
echo "package main" > main.go

# Test with explicit root
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "tool": "index_workspace",
    "params": {
      "workspace_root": "/tmp/test-project"
    }
  }'
```

### Test validation:

```bash
# Should reject invalid path
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "tool": "index_workspace",
    "params": {
      "workspace_root": "/nonexistent/path"
    }
  }'
```

## Benefits Summary

| Aspect | Automatic Detection | Explicit Root |
|--------|-------------------|---------------|
| **Security** | ⚠️ May traverse upward | ✅ No traversal |
| **Speed** | 🐢 Searches directories | ⚡ Instant |
| **Accuracy** | 🎲 Best guess | 🎯 Exact |
| **Permissions** | ⚠️ May trigger prompts | ✅ No prompts |
| **Control** | 🤷 Automatic | ✅ Explicit |

## Conclusion

The `workspace_root` parameter for `index_workspace` addresses the core security concern:

> "I don't see why the tool needs access to this information and the scope of its access should be limited to the project its running on."

**Solution**: Let the AI explicitly tell us which project to INDEX, rather than trying to figure it out ourselves by traversing the filesystem.

For search operations, the workspace is already known from the index, so no additional parameter is needed.

This is:
- More secure (no unnecessary traversal during indexing)
- More explicit (clear intent for indexing)
- More efficient (no detection overhead during indexing)
- Better UX (no permission prompts)

## See Also

- [IDE Security Configuration](./IDE-SECURITY-CONFIG.md) - CLI flags for additional security
- [Configuration Guide](./CONFIGURATION.md) - Server configuration options
- [Tool Schema Reference](./tool_schema_v2.md) - Complete API documentation

This provides:
- ✅ **Better security** - No automatic directory traversal
- ✅ **Explicit control** - AI knows exactly which project to work with
- ✅ **Faster operation** - Skips detection logic
- ✅ **No permission prompts** - Never triggers macOS Home folder access

## Why This Approach?

**Original question**: "Why doesn't the AI just give us the workspace root path directly?"

**Answer**: You're absolutely right! The AI (Copilot/Cursor/Windsurf) knows the project root. It makes more sense for the AI to tell us explicitly rather than us trying to guess by searching upward through directories.

### Before (Automatic Detection)
```json
{
  "tool": "search_code",
  "query": "authentication handler",
  "file_path": "/home/user/projects/myapp/src/handlers/auth.go"
}
```
Tool searches UP from `auth.go` → finds `.git` in `/home/user/projects/myapp/` → uses that as root

### After (Explicit Root)
```json
{
  "tool": "search_code",
  "query": "authentication handler",
  "workspace_root": "/home/user/projects/myapp"
}
```
Tool uses `/home/user/projects/myapp` directly → No searching needed!

## Parameter Support

All workspace-aware tools now support `workspace_root`:

| Tool | Supports workspace_root | Description |
|------|------------------------|-------------|
| `search_code` | ✅ Yes | Semantic code search |
| `hybrid_search` | ✅ Yes | Combined lexical + semantic search |
| `get_function_details` | ✅ Yes | Get function/method details |
| `find_type_definition` | ✅ Yes | Find class/type definitions |
| `list_package_exports` | ✅ Yes | List package exports |
| `find_implementations` | ✅ Yes | Find usages/implementations |
| `search_docs` | ✅ Yes | Search documentation |
| `index_workspace` | ✅ Yes | Manual workspace indexing |
| `get_code_context` | ❌ No | Reads file directly, doesn't need workspace |

## Usage Examples

### Example 1: Search Code with Explicit Root

**Without workspace_root** (automatic detection):
```json
{
  "tool": "search_code",
  "params": {
    "query": "user authentication",
    "file_path": "/home/user/projects/backend/src/auth/handler.go"
  }
}
```
- Searches up from `handler.go`
- Looks for `.git`, `go.mod`, etc.
- May traverse multiple parent directories

**With workspace_root** (explicit):
```json
{
  "tool": "search_code",
  "params": {
    "query": "user authentication",
    "workspace_root": "/home/user/projects/backend"
  }
}
```
- Uses `/home/user/projects/backend` directly
- No upward search
- Faster and more secure

### Example 2: Index Workspace Explicitly

**Automatic detection**:
```json
{
  "tool": "index_workspace",
  "params": {
    "file_path": "/home/user/projects/myapp/main.go"
  }
}
```

**Explicit root** (recommended):
```json
{
  "tool": "index_workspace",
  "params": {
    "workspace_root": "/home/user/projects/myapp"
  }
}
```

### Example 3: Multiple Tool Calls with Same Root

When AI makes multiple queries about the same project:

```json
// First call - explicit root
{
  "tool": "search_code",
  "params": {
    "query": "database connection",
    "workspace_root": "/home/user/projects/backend"
  }
}

// Second call - reuse same root
{
  "tool": "get_function_details",
  "params": {
    "function_name": "ConnectDB",
    "workspace_root": "/home/user/projects/backend"
  }
}

// Third call - still same root
{
  "tool": "find_implementations",
  "params": {
    "symbol_name": "DatabaseInterface",
    "workspace_root": "/home/user/projects/backend"
  }
}
```

No directory traversal needed for any of these calls!

## Security Benefits

### 1. No Home Directory Traversal

**Problem**: Automatic detection could trigger traversal of Home directory
```
File: ~/Desktop/script.py
Detection: Searches up → reaches ~ → triggers macOS permission prompt
```

**Solution**: With explicit root, this never happens
```json
{
  "workspace_root": "/home/user/projects/myapp"
}
```
Only `myapp` directory is accessed.

### 2. Works with CLI Security Flags

Combine with `-allowed-paths` for maximum security:

```bash
rag-code-mcp -allowed-paths "/home/user/projects,/home/user/work"
```

Then AI can only use explicit roots within allowed paths:
```json
{
  "workspace_root": "/home/user/projects/backend"  // ✅ Allowed
}
```
```json
{
  "workspace_root": "/home/user/Desktop"  // ❌ Rejected
}
```

### 3. Bypass Upward Search

Even with `-disable-upward-search`, explicit roots work:

```bash
rag-code-mcp -disable-upward-search
```

```json
{
  "workspace_root": "/home/user/projects/myapp"  // ✅ Works fine
}
```

No upward search needed because root is explicit!

## Implementation Details

### Priority Order

When `DetectWorkspace()` is called with params:

1. **Check for `workspace_root`** (highest priority)
   - If present, use it directly
   - Still validates path (security checks, marker validation)
   - Skips all automatic detection

2. **Check for `file_path`** (fallback)
   - If no `workspace_root`, use `file_path`
   - Triggers automatic detection (upward search)
   - Subject to all security restrictions

3. **Use CWD** (last resort)
   - If neither provided, use current working directory
   - Only if `require_explicit_path` is not set

### Validation

Even with explicit `workspace_root`, the tool still validates:

✅ Path exists and is accessible  
✅ Within `allowed_paths` (if configured)  
✅ Not `/`, `/tmp`, or Home directory (unless has markers)  
✅ Has valid workspace markers (`.git`, `go.mod`, etc.)  

### Caching

Explicit workspace roots are cached:
- Key: absolute path of workspace root
- Value: workspace Info structure
- Duration: 5 minutes (configurable)

## AI Integration Recommendations

### For AI/LLM Providers

When building MCP integrations, we recommend:

1. **Always provide `workspace_root`** when known
2. **Detect project root** from IDE/editor context
3. **Cache workspace root** for the current session
4. **Reuse across tool calls** for same project

### Example Pseudo-code

```python
class MCPClient:
    def __init__(self):
        self.current_workspace = self.detect_project_root()
    
    def call_tool(self, tool_name, params):
        # Always add workspace_root if we know it
        if self.current_workspace:
            params['workspace_root'] = self.current_workspace
        
        return self.mcp_server.execute(tool_name, params)
    
    def detect_project_root(self):
        # Use IDE/editor API to get project root
        # e.g., VSCode: workspace.workspaceFolders[0].uri.fsPath
        # e.g., JetBrains: project.basePath
        return get_project_root_from_ide()
```

### VS Code Extension Example

```typescript
const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

const result = await mcpClient.callTool('search_code', {
    query: 'authentication',
    workspace_root: workspaceRoot  // Explicit root from VS Code
});
```

### Cursor/Windsurf Example

```typescript
const projectRoot = getCurrentProjectRoot();

await mcp.execute({
    tool: 'index_workspace',
    params: {
        workspace_root: projectRoot  // Explicit root from IDE
    }
});
```

## Backward Compatibility

The `workspace_root` parameter is **optional**. Existing behavior is preserved:

- Tools without `workspace_root` → automatic detection (as before)
- Tools with `workspace_root` → explicit root (new behavior)
- No breaking changes to existing integrations

## Testing

### Test with explicit root:

```bash
# Create test project
mkdir -p /tmp/test-project
cd /tmp/test-project
git init
echo "package main" > main.go

# Test with explicit root
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "tool": "index_workspace",
    "params": {
      "workspace_root": "/tmp/test-project"
    }
  }'
```

### Test validation:

```bash
# Should reject invalid path
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "tool": "search_code",
    "params": {
      "query": "test",
      "workspace_root": "/nonexistent/path"
    }
  }'
```

## Benefits Summary

| Aspect | Automatic Detection | Explicit Root |
|--------|-------------------|---------------|
| **Security** | ⚠️ May traverse upward | ✅ No traversal |
| **Speed** | 🐢 Searches directories | ⚡ Instant |
| **Accuracy** | 🎲 Best guess | 🎯 Exact |
| **Permissions** | ⚠️ May trigger prompts | ✅ No prompts |
| **Control** | 🤷 Automatic | ✅ Explicit |

## Conclusion

The `workspace_root` parameter addresses the core security concern raised in the original issue:

> "I don't see why the tool needs access to this information and the scope of its access should be limited to the project its running on."

**Solution**: Let the AI explicitly tell us which project to work with, rather than trying to figure it out ourselves by traversing the filesystem.

This is:
- More secure (no unnecessary traversal)
- More explicit (clear intent)
- More efficient (no detection overhead)
- Better UX (no permission prompts)

## See Also

- [IDE Security Configuration](./IDE-SECURITY-CONFIG.md) - CLI flags for additional security
- [Configuration Guide](./CONFIGURATION.md) - Server configuration options
- [Tool Schema Reference](./tool_schema_v2.md) - Complete API documentation
