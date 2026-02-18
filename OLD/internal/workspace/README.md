# Workspace Package

The `workspace` package provides workspace detection and identification for multi-workspace MCP support.

## Features

- **Auto-detection**: Automatically detect workspace root from file paths
- **Multi-marker support**: Detects various project types (.git, go.mod, package.json, etc.)
- **Stable IDs**: Generates consistent workspace identifiers using SHA256 hashing
- **Caching**: Built-in cache to avoid redundant detection
- **Extensible**: Customizable markers and exclusion patterns

## Usage

### Basic Detection

```go
import "github.com/doITmagic/rag-code-mcp/internal/workspace"

// Create detector
detector := workspace.NewDetector()

// Detect from file path
info, err := detector.DetectFromPath("/home/user/projects/my-app/src/main.go")
if err != nil {
    log.Fatal(err)
}

fmt.Println("Workspace:", info.Root)
fmt.Println("ID:", info.ID)
fmt.Println("Type:", info.ProjectType)
fmt.Println("Collection:", info.CollectionName())
```

### Detection from MCP Parameters

```go
// MCP tool receives parameters
params := map[string]interface{}{
    "file_path": "/home/user/project/internal/handler.go",
    "query": "search term",
}

// Detect workspace from params
info, err := detector.DetectFromParams(params)
if err != nil {
    log.Fatal(err)
}

// Use workspace-specific collection
collectionName := info.CollectionName() // "ragcode-a3f4b8c9d2e1"
```

### Using Cache

```go
// Create cache with 5 minute TTL
cache := workspace.NewCache(5 * time.Minute)

// Check cache before detection
if cached := cache.Get(filePath); cached != nil {
    return cached
}

// Detect and cache
info, err := detector.DetectFromPath(filePath)
cache.Set(filePath, info)
```

### Custom Configuration

```go
detector := workspace.NewDetector()

// Customize markers
detector.SetMarkers([]string{
    ".git",
    "package.json",
    "deno.json",      // Custom marker
    "requirements.txt", // Python projects
})

// Customize exclusions
detector.SetExcludePatterns([]string{
    "/node_modules/",
    "/dist/",
    "/.next/",
    "/target/",
})
```

## API Reference

### Types

#### `Info`
Represents detected workspace information.

```go
type Info struct {
    Root        string    // Absolute path to workspace root
    ID          string    // Unique workspace identifier (12-char hash)
    ProjectType string    // Detected project type (go, nodejs, python, etc.)
    Markers     []string  // Found workspace markers
    DetectedAt  time.Time // Detection timestamp
}

func (i *Info) CollectionName() string // Returns "ragcode-{ID}"
```

#### `Metadata`
Workspace metadata for storage in Qdrant.

```go
type Metadata struct {
    WorkspaceID  string    // Workspace unique ID
    RootPath     string    // Workspace root path
    LastIndexed  time.Time // Last indexing time
    FileCount    int       // Number of indexed files
    ChunkCount   int       // Number of indexed chunks
    Status       string    // "indexed", "indexing", "failed", "pending"
    ProjectType  string    // Project type
    Markers      []string  // Workspace markers
    ErrorMessage string    // Error if status is "failed"
}
```

### Detector

#### `NewDetector() *Detector`
Creates detector with default markers.

Default markers (in priority order):
- `.git` - Git repository
- `go.mod` - Go project
- `package.json` - Node.js project
- `Cargo.toml` - Rust project
- `pyproject.toml` - Python project (modern)
- `setup.py` - Python project (legacy)
- `pom.xml` - Maven project
- `build.gradle` - Gradle project
- `.project` - Generic project
- `.vscode` - VS Code workspace

#### `DetectFromPath(filePath string) (*Info, error)`
Detects workspace from a file path. Walks up directory tree looking for workspace markers.

Returns fallback workspace (file's directory) if no markers found.

#### `DetectFromParams(params map[string]interface{}) (*Info, error)`
Detects workspace from MCP tool parameters. Looks for file paths in common parameter names:
- `file_path`, `filePath`
- `path`, `file`
- `source_file`, `target_file`
- `directory`, `dir`

Falls back to current working directory if no paths found.

#### `SetMarkers(markers []string)`
Sets custom workspace markers.

#### `SetExcludePatterns(patterns []string)`
Sets path patterns to exclude from detection.

### Cache

#### `NewCache(ttl time.Duration) *Cache`
Creates cache with specified TTL.

#### `Get(key string) *Info`
Retrieves cached workspace info. Returns nil if not found or expired.

#### `Set(key string, info *Info)`
Stores workspace info in cache.

#### `Clear()`
Removes all entries from cache.

#### `CleanExpired() int`
Removes expired entries. Returns number of entries removed.

#### `Size() int`
Returns number of cached entries.

## Workspace ID Generation

Workspace IDs are generated using SHA256 hash of the absolute workspace path:

```go
func generateWorkspaceID(rootPath string) string {
    h := sha256.Sum256([]byte(rootPath))
    return hex.EncodeToString(h[:])[:12] // First 12 chars
}
```

**Properties:**
- ✅ **Stable**: Same path always generates same ID
- ✅ **Unique**: Different paths generate different IDs
- ✅ **Collision-resistant**: 12-char hex = 48 bits (281 trillion combinations)
- ✅ **Readable**: Short enough for logs and debugging

**Examples:**
```
/home/user/projects/do-ai       → a3f4b8c9d2e1
/home/user/projects/other-app   → 5e6f7g8h9i0j
/opt/workspace/backend          → 1a2b3c4d5e6f
```

## Project Type Detection

Automatically infers project type from markers:

| Marker | Project Type |
|--------|--------------|
| `go.mod` | `go` |
| `package.json` | `nodejs` |
| `Cargo.toml` | `rust` |
| `pyproject.toml`, `setup.py` | `python` |
| `pom.xml` | `maven` |
| `build.gradle` | `gradle` |
| `.git` | `git` |
| No markers | `unknown` |

For Laravel projects, the detector uses the combination of `composer.json` + `artisan` and normalizes the project type to a PHP/Laravel variant (e.g. `laravel`, `php-laravel`), so that the `language_manager` can automatically select the PHP + Laravel analyzer.

## Integration Examples

### MCP Tool Integration

```go
type SearchTool struct {
    detector *workspace.Detector
    cache    *workspace.Cache
}

func (t *SearchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
    // Detect workspace from params
    info, err := t.detector.DetectFromParams(params)
    if err != nil {
        return "", err
    }
    
    // Get workspace-specific collection
    collection := info.CollectionName()
    
    // Use collection for search
    results, err := t.searchInCollection(ctx, collection, params)
    return results, err
}
```

### Background Cleanup

```go
// Periodic cache cleanup
func startCacheCleanup(cache *workspace.Cache) {
    ticker := time.NewTicker(10 * time.Minute)
    go func() {
        defer ticker.Stop()
        for range ticker.C {
            removed := cache.CleanExpired()
            log.Printf("Cleaned %d expired workspace cache entries", removed)
        }
    }()
}
```

## Testing

Run tests:
```bash
go test ./internal/workspace/...
```

Run with coverage:
```bash
go test -cover ./internal/workspace/...
```

Run benchmarks:
```bash
go test -bench=. ./internal/workspace/...
```

## Performance

- **Detection**: ~0.1ms (with filesystem access)
- **Cache hit**: ~0.001ms (memory lookup)
- **ID generation**: ~0.01ms (SHA256 hash)

Cache significantly improves performance for repeated calls with same paths.

## See Also

- [Multi-workspace Design](../../docs/multi-workspace-design.md) - Architecture overview
- [Migration Guide](../../MIGRATION.md) - Upgrading to multi-workspace
