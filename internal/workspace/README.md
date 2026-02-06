# Workspace Package

The `workspace` package provides intelligent workspace detection, identification, and persistence for multi-workspace MCP environments. It ensures that AI agents always have the correct context, even when multiple projects are open or when tool calls lack explicit paths.

## Key Components

- **Manager**: The central orchestrator that coordinates detection, caching, and persistence.
- **Detector**: Performs the heavy lifting of walking up directory trees to find project markers.
- **Registry**: Manages the global persistence of known workspaces (~/.config/ragcode/workspaces.json).
- **Cache**: In-memory storage for high-performance repeated lookups in the same session.

## Features

- **Multi-Level Detection**: Robust fallback system (Explicit -> File -> CWD -> Registry -> Session).
- **Persistence**: Remembers where you worked last, even after restarting the MCP server.
- **Process Isolation**: Uses Current Working Directory (CWD) to distinguish between multiple IDE windows.
- **Stability**: Generates consistent 12-character IDs based on project root paths.
- **Throttling**: Efficiently saves state to disk using a debounce mechanism (500ms).

## Workspace Detection Logic (The 4-Priority System)

When a tool is called, the `Manager` determines the active workspace using this priority order:

1.  **Priority 1: Explicit Root**: If `workspace_root` is provided in the configuration or tool parameters, it is used immediately.
2.  **Priority 2: Automatic from Path**: If a `file_path` is provided, the `Detector` walks up the tree to find project markers (like `.git`, `go.mod`, `package.json`).
3.  **Priority 2.5: CWD Fallback**: If no `file_path` is given, it checks the **Process CWD**. This allows different IDE windows (running on different ports/processes) to maintain their own contexts automatically.
4.  **Priority 3: Global Registry**: If the current process has no context, it looks at the **Global Registry** to find the last active workspace used on the machine.
5.  **Priority 4: Session Fallback**: As a last resort, it uses the first workspace registered in the current MCP session.

## Usage

### Using the Manager

The `Manager` is the recommended way to interact with this package:

```go
import "github.com/doITmagic/rag-code-mcp/internal/workspace"

// Initialize with a registry path (optional)
mgr := workspace.NewManager(storageClient, llmProvider, config)

// In a tool execution:
params := map[string]interface{}{"file_path": "/path/to/file.go"}
info, err := mgr.DetectWorkspace(params)
if err != nil {
    return nil, err
}

fmt.Println("Active Collection:", info.CollectionName())
```

### Persistence (Registry)

The registry saves workspace metadata to disk, enabling "memory" across restarts.

```go
// Default path: ~/.config/ragcode/workspaces.json
reg, _ := workspace.NewRegistry("")

// Registry remembers languages and last used timestamps
last := reg.GetLastUsed()
if last != nil {
    fmt.Println("Welcome back to:", last.Root)
}
```

## Configuration

In `config.yaml`, you can customize the workspace behavior:

```yaml
workspace:
  enabled: true
  collection_prefix: "custom-prefix" # Applied to all detected collections
  exclude_patterns:
    - "/node_modules/"
    - "/vendor/"
```

## API Reference

### Manager

#### `DetectWorkspace(params map[string]interface{}) (*Info, error)`
The high-level method to always get a valid workspace context. It handles all fallback logic and caching.

### Registry

#### `RegisterOrUpdate(info *Info) error`
Adds a workspace to the global registry. Updates the `Root` if it changed and refreshes `LastUsed`. Includes a 500ms debounce to protect disk I/O.

#### `GetLastUsed() *RegistryEntry`
Returns the most recently active workspace from the registry.

## Performance

- **Memory Lookup**: < 0.001ms
- **FS Detection**: ~0.1ms
- **Disk Save**: Throttled (debounced) to prevent overhead during rapid tool calls.

## See Also

- [Tool Schema Reference](../../docs/tool_schema_v2.md) - How tools use workspace detection.
- [Architecture Overview](../../docs/architecture.md) - How Manager fits into the system.
