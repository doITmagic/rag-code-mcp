# Architecture

This document describes the internal architecture of RagCode MCP Server after the multi-language restructuring.

## Overview

RagCode MCP is structured to support multiple programming languages through a pluggable analyzer architecture. The codebase is organized to separate language-agnostic components from language-specific analyzers.

## Directory Structure

```
internal/
├── codetypes/              # Universal types and interfaces (language-agnostic)
│   ├── types.go           # CodeChunk (canonical), PathAnalyzer (legacy APIChunk/APIAnalyzer kept only for compatibility)
│   └── symbol_schema.go   # Symbol schema definitions
│
├── ragcode/               # Core indexing and language management
│   ├── indexer.go         # Indexing logic using PathAnalyzer (CodeChunk-only)
│   ├── language_manager.go # Factory for selecting language analyzers (by project type)
│   ├── ragcode_test.go    # Integration tests
│   ├── laravel_integration_test.go # Laravel integration tests
│   └── analyzers/         # Language-specific analyzers
│       ├── golang/        # Go language analyzer (fully implemented)
│       │   ├── analyzer.go           # PathAnalyzer implementation → CodeChunk
│       │   ├── api_analyzer.go       # API documentation analyzer
│       │   ├── types.go              # Go-specific types (FunctionInfo, TypeInfo, etc.)
│       │   └── analyzer_test.go      # Unit tests
│       ├── php/           # PHP analyzer (including Laravel support)
│       │   ├── analyzer.go           # Main PHP analyzer
│       │   ├── api_analyzer.go       # PHP API analyzer
│       │   ├── phpdoc.go             # PHPDoc parsing
│       │   ├── types.go              # PHP-specific types
│       │   └── laravel/   # Laravel-specific analyzers
│       │       ├── analyzer.go       # Laravel analyzer coordinator
│       │       ├── eloquent.go       # Eloquent model analyzer
│       │       ├── controller.go     # Controller analyzer
│       │       ├── routes.go         # Route analyzer
│       │       ├── adapter.go        # Adapter for integration
│       │       └── ast_helper.go     # AST utilities
│       ├── html/          # HTML analyzer
│       │   └── analyzer.go
│       └── python/        # Python analyzer (placeholder)
│           └── README.md
│
├── workspace/             # Multi-workspace detection and management
│   ├── manager.go         # Workspace manager (per-language collections)
│   ├── detector.go        # Workspace root detection
│   ├── language_detection.go # Language detection from markers
│   ├── multi_search.go    # Cross-workspace search logic
│   ├── cache.go           # Workspace cache
│   ├── types.go           # Workspace types and structs
│   ├── README.md          # Workspace documentation
│   └── *_test.go          # Comprehensive test suite (manager_multilang_test.go, etc.)
│
├── tools/                 # MCP tool implementations (9 tools)
│   ├── search_local_index.go
│   ├── hybrid_search.go
│   ├── get_function_details.go
│   ├── find_type_definition.go
│   ├── get_code_context.go
│   ├── list_package_exports.go
│   ├── find_implementations.go
│   ├── search_docs.go
│   ├── index_workspace.go    # Manual indexing tool
│   ├── workspace_helpers.go  # Helper functions for tools
│   ├── utils.go
│   └── *_test.go             # Tool tests
│
├── storage/               # Vector database (Qdrant) integration
│   ├── qdrant.go          # Qdrant client wrapper
│   ├── qdrant_memory.go   # LongTermMemory implementation
│   ├── qdrant_memory_test.go
│   └── (Redis, SQLite configs - optional backends)
│
├── memory/                # Memory management (short-term, long-term)
│   ├── state.go           # Memory state interface
│   ├── shortterm.go       # Short-term memory implementation
│   ├── longterm.go        # Long-term memory interface
│   └── (Storage implementations)
│
├── llm/                   # LLM provider (Ollama, HuggingFace, etc.)
│   ├── provider.go        # LLM provider interface
│   ├── ollama.go          # Ollama implementation
│   └── provider_test.go   # Tests
│
├── config/                # Configuration management
│   ├── config.go          # Config structs (8 sections: LLM, Storage, etc.)
│   ├── loader.go          # YAML + ENV parsing
│   └── config_test.go     # Tests
│
├── healthcheck/           # Health check utilities
│   └── healthcheck.go     # Dependency checks (Ollama, Qdrant, etc.)
│
├── utils/                 # Utility functions
│   └── retry.go           # Retry logic
│
└── codetypes/             # (See above)
```

## Multi-Language & Multi-Workspace Architecture

### Overview

RagCode MCP supports **polyglot workspaces** (containing multiple programming languages) by creating **separate Qdrant collections per language per workspace**. This ensures clean separation of code by language, better search quality, and improved scalability.

### Collection Naming Strategy

**Format:**
```
{prefix}-{workspaceID}-{language}
```

**Examples:**
```
ragcode-a1b2c3d4e5f6-go
ragcode-a1b2c3d4e5f6-python
ragcode-a1b2c3d4e5f6-javascript
ragcode-a1b2c3d4e5f6-php
```

**Default Prefix:** `ragcode` (configurable via `workspace.collection_prefix` in `config.yaml`)

### Language Detection Strategy

Language detection uses **file markers** to identify programming languages present in a workspace:

| Marker File         | Detected Language |
|---------------------|-------------------|
| `go.mod`            | `go`              |
| `package.json`      | `javascript`      |
| `pyproject.toml`    | `python`          |
| `setup.py`          | `python`          |
| `requirements.txt`  | `python`          |
| `composer.json`     | `php`             |
| `Cargo.toml`        | `rust`            |
| `pom.xml`           | `java`            |
| `build.gradle`      | `java`            |
| `Gemfile`           | `ruby`            |
| `Package.swift`     | `swift`           |
| `.git`              | workspace root    |

### Multi-Language Workspace Example

Consider a monorepo with multiple languages:

```
myproject/
├── .git
├── go.mod                  # Triggers Go detection
├── main.go                 # → Indexed into ragcode-xxx-go
├── api_server.go
├── scripts/
│   ├── pyproject.toml      # Triggers Python detection
│   ├── train.py            # → Indexed into ragcode-xxx-python
│   └── ml_utils.py
└── web/
    ├── package.json        # Triggers JavaScript detection
    ├── app.js              # → Indexed into ragcode-xxx-javascript
    └── utils.ts
```

**Results in 3 collections:**
- `ragcode-{workspaceID}-go` - Contains all Go code
- `ragcode-{workspaceID}-python` - Contains all Python code
- `ragcode-{workspaceID}-javascript` - Contains all JavaScript/TypeScript code

### Indexing Strategy

When indexing a workspace:

1. **Detect all languages** present in the workspace from markers
2. **For each detected language**:
   - Create collection if it doesn't exist: `{prefix}-{workspaceID}-{language}`
   - Select appropriate analyzer (Go, PHP, Python, etc.)
   - Filter files by language extension (`**/*.go`, `**/*.py`, etc.)
   - Index using language-specific analyzer
   - Store all chunks with `Language` field set to the language identifier

**File Filtering Examples:**

| Language     | Include Patterns       | Exclude Patterns          |
|--------------|------------------------|---------------------------|
| Go           | `**/*.go`              | `**/*_test.go`, `vendor/` |
| Python       | `**/*.py`              | `**/__pycache__/`, `**/.venv/` |
| JavaScript   | `**/*.js`, `**/*.ts`   | `**/node_modules/`, `**/dist/` |
| PHP          | `**/*.php`             | `**/vendor/`, `**/cache/` |

### Query Strategy

#### Language-Specific Search

When a query is received via MCP tools with file context:

1. **Detect file context** from query parameters (e.g., `file_path`)
2. **Infer language** from file extension or workspace markers
3. **Search in language-specific collection**: `{prefix}-{workspaceID}-{language}`

**Example:** Query with Go file context
```json
{
  "file_path": "/workspace/main.go",
  "query": "handler function"
}
```
→ Automatically searches in `ragcode-{workspaceID}-go`

#### Cross-Language Search

For semantic searches across all code:

1. **Query all language collections** in the workspace
2. **Merge and rank results** by relevance score
3. **Return unified results** with language metadata for context

**Example:** Semantic search without file context
```json
{
  "query": "authentication middleware",
  "workspace_id": "backend"
}
```
→ Searches in:
- `ragcode-backend-go`
- `ragcode-backend-python`
- `ragcode-backend-javascript`
→ Returns combined results with language labels

### Workspace Info API

The `Workspace.Info` struct tracks detected languages:

```go
type Info struct {
    Root             string    `json:"root"`
    ID               string    `json:"id"`
    ProjectType      string    `json:"project_type,omitempty"`
    Languages        []string  `json:"languages,omitempty"` // Detected languages
    Markers          []string  `json:"markers,omitempty"`   // Detection markers found
    DetectedAt       time.Time `json:"detected_at,omitempty"`
    CollectionPrefix string    `json:"collection_prefix,omitempty"`
}

// CollectionNameForLanguage returns the collection name for a specific language
func (w *Info) CollectionNameForLanguage(language string) string {
    return w.CollectionPrefix + "-" + w.ID + "-" + language
}
```

### Migration from Single-Collection Mode

**Legacy Format (Deprecated):**
```
ragcode-{workspaceID}  →  [Mixed Go + Python + JavaScript code]
```

**New Format:**
```
ragcode-{workspaceID}-go          →  [Go code only]
ragcode-{workspaceID}-python      →  [Python code only]
ragcode-{workspaceID}-javascript  →  [JavaScript code only]
```

To migrate:
1. **Delete old collection** (optional): `ragcode-{workspaceID}`
2. **Re-run indexing**: Automatically creates language-specific collections
3. **Update queries**: Use `CollectionNameForLanguage(language)` instead of single collection

### Benefits of Multi-Language Architecture

1. **Better Organization** - Clear separation of code by language
2. **Improved Search Quality** - Language-specific chunking and embeddings
3. **Scalability** - Independent indexing per language, supports parallel processing
4. **Debugging** - Easy to identify and fix language-specific indexing issues
5. **Extensibility** - Add new languages without affecting existing ones

---

## Core Components

### 1. Universal Types (`internal/codetypes`)

**Purpose:** Define language-agnostic types and interfaces used across all analyzers.

**Key Types:**
- `CodeChunk` - Represents a code symbol (function, method, type, etc.)
- `APIChunk` - Represents API documentation for a symbol
- `PathAnalyzer` - Interface for code analysis
- `APIAnalyzer` - Interface for API documentation extraction

**Design Principle:** These types are enhanced with LSP-inspired fields (Language, URI, SelectionRange, Detail, AccessModifier, Tags, Children) to support rich code navigation.

### 2. Language Manager (`internal/ragcode/language_manager.go`)

**Purpose:** Factory pattern for selecting the appropriate analyzer based on project type or language.

**Key Functions:**
```go
func (m *AnalyzerManager) CodeAnalyzerForProjectType(projectType string) codetypes.PathAnalyzer
func (m *AnalyzerManager) APIAnalyzerForProjectType(projectType string) codetypes.APIAnalyzer
```

**Supported Languages:**
- `LanguageGo` (Go) - fully implemented
- `LanguagePHP` (PHP) - placeholder
- `LanguagePython` (Python) - placeholder

### 4. Workspace Manager (`internal/workspace/manager.go`)

**Purpose:** Core component for multi-workspace and multi-language support. Manages automatic workspace detection, per-language collections, and multi-workspace indexing.

**Key Capabilities:**
- Automatic workspace detection using markers (`.git`, `go.mod`, `package.json`, etc.)
- Per-workspace, per-language collection creation: `{prefix}-{workspaceID}-{language}`
- Language detection from file markers
- Workspace cache for performance
- Multi-workspace simultaneous indexing with concurrency limits

**Key Methods:**
```go
func (m *Manager) GetMemoryForWorkspaceLanguage(workspaceID, language string) (memory.LongTermMemory, error)
func (m *Manager) DetectWorkspace(params map[string]interface{}) (*Info, error)
func (m *Manager) GetAllWorkspaces() []Info
```

**Example:**
For a monorepo with Go + Python code:
```
├── backend/                      → workspace "backend"
│   ├── .git/
│   ├── go.mod                   → language: "go"
│   └── Collections: ragcode-backend-go
├── frontend/                     → workspace "frontend"
│   ├── package.json             → language: "javascript"
│   └── Collections: ragcode-frontend-javascript
└── scripts/                      → workspace "scripts"
    ├── requirements.txt         → language: "python"
    └── Collections: ragcode-scripts-python
```

### 5. Workspace Detector (`internal/workspace/detector.go`)

**Purpose:** Detects workspace roots from file paths and manages workspace information caching.

**Key Features:**
- Find workspace root by looking for detection markers
- Cache workspace information for fast lookups
- Extract workspace metadata (root, ID, detected markers)

### 6. Language Detection (`internal/workspace/language_detection.go`)

**Purpose:** Identifies programming language from workspace detection markers.

**Supported Languages (11+):**
- Go: `go.mod`
- JavaScript/TypeScript/Node.js: `package.json`
- Python: `pyproject.toml`, `setup.py`, `requirements.txt`
- Rust: `Cargo.toml`
- PHP: `composer.json`
- Java: `pom.xml`, `build.gradle`
- Ruby: `Gemfile`
- Swift: `Package.swift`
- C#: `*.csproj`
- Others: `.git` alone indicates workspace root

### 5. Indexer (`internal/ragcode/indexer.go`)

**Purpose:** Indexes code chunks into vector database using embeddings.

**Dependencies:**
- Accepts `codetypes.PathAnalyzer` or `codetypes.APIAnalyzer`
- Uses `llm.Provider` for embeddings
- Stores in `memory.LongTermMemory` (Qdrant)

**Workflow:**
```
paths → analyzer.AnalyzePaths() → []CodeChunk → embeddings → Qdrant
```

### 6. Go Analyzer (`internal/ragcode/analyzers/golang`)

**Purpose:** Implements PathAnalyzer and APIAnalyzer for Go language using `go/ast`, `go/doc`, and `go/parser`.

**Components:**
- `analyzer.go` - Implements `AnalyzePaths()` for code chunk extraction
- `api_analyzer.go` - Implements `AnalyzeAPIPaths()` for API documentation
- `types.go` - Go-specific internal types (PackageInfo, FunctionInfo, TypeInfo, etc.)

**Key Features:**
- Extracts functions, methods, types, interfaces
- Populates `Language: "go"` for all chunks
- Supports docstring extraction
- Line-accurate positioning (StartLine, EndLine, SelectionRange)

**Test Coverage:** 82.1% (13 unit tests)

### 7. Storage: Qdrant Integration (`internal/storage`)

**Purpose:** Vector database integration for storing and retrieving embeddings.

**Components:**
- `qdrant.go` - Qdrant client wrapper with collection management
- `qdrant_memory.go` - LongTermMemory implementation using Qdrant

**Features:**
- Automatic collection creation
- Per-workspace, per-language collections
- Vector similarity search
- Filtering and text search integration

### 8. Tools: 8 MCP Tools (`internal/tools`)

**Purpose:** Implements semantic code navigation and search tools for IDE integration.

**Tools:**
1. `search_local_index.go` - Semantic search across indexed codebase
2. `hybrid_search.go` - Combined semantic + lexical search
3. `get_function_details.go` - Retrieve function signatures and documentation
4. `find_type_definition.go` - Locate type and interface definitions
5. `get_code_context.go` - Direct file access without indexing
6. `list_package_exports.go` - List exported symbols
7. `find_implementations.go` - Find interface implementations
8. `search_docs.go` - Search markdown documentation

**All tools support:**
- Workspace-specific queries
- Language-specific filtering
- Multi-language workspaces

## Adding a New Language Analyzer

To add support for a new language (e.g., PHP, Python):

### Step 1: Create Analyzer Package

```bash
mkdir -p internal/ragcode/analyzers/<language>
```

### Step 2: Implement PathAnalyzer

Create `analyzer.go`:

```go
package <language>

import "github.com/doITmagic/rag-code-mcp/internal/codetypes"

type CodeAnalyzer struct {
    // language-specific fields
}

func NewCodeAnalyzer() *CodeAnalyzer {
    return &CodeAnalyzer{}
}

func (ca *CodeAnalyzer) AnalyzePaths(paths []string) ([]codetypes.CodeChunk, error) {
    // Parse files and extract symbols
    // Set Language field to appropriate value (e.g., "php", "python")
    // Return chunks
}
```

### Step 3: Implement APIAnalyzer

Create `api_analyzer.go`:

```go
package <language>

import "github.com/doITmagic/rag-code-mcp/internal/codetypes"

type APIAnalyzerImpl struct {
    analyzer *CodeAnalyzer
}

func NewAPIAnalyzer(analyzer *CodeAnalyzer) *APIAnalyzerImpl {
    return &APIAnalyzerImpl{analyzer: analyzer}
}

func (a *APIAnalyzerImpl) AnalyzeAPIPaths(paths []string) ([]codetypes.APIChunk, error) {
    // Extract API documentation
    // Set Language field
    // Return API chunks
}
```

### Step 4: Register in Language Manager

Update `internal/ragcode/language_manager.go`:

```go
import "github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/<language>"

const (
    Language<Name> Language = "<language>"
)

func (m *AnalyzerManager) CodeAnalyzerForProjectType(projectType string) codetypes.PathAnalyzer {
    lang := normalizeProjectType(projectType)
    switch lang {
    case Language<Name>:
        return <language>.NewCodeAnalyzer()
    // ...
    }
}
```

### Step 5: Add Tests

Create `analyzer_test.go` and `api_analyzer_test.go` following the pattern in `golang/` tests.

### Step 6: Update Documentation

Update this file and main README.md to list the new language as supported.

## Key Design Decisions

### 1. Separate `codetypes` Package

**Rationale:** Prevents import cycles. Analyzers import `codetypes`, not `ragcode`.

**Benefits:**
- Clean dependency graph: `golang` → `codetypes`, `ragcode` → `codetypes`, `ragcode` → `golang`
- Shared types accessible from all packages
- Easy to add new languages without circular dependencies

### 2. Language Field in All Chunks

**Rationale:** Support multi-language workspaces and language-specific queries.

**Implementation:** Each analyzer must set `Language` field (e.g., "go", "php", "python") in all returned chunks.

### 3. LSP-Inspired Metadata

**Rationale:** Enable rich IDE-like features (navigation, hover, completion).

**Fields Added:**
- `URI` - Full document URI for protocol compliance
- `SelectionRange` - Precise symbol name location for "Go to Definition"
- `Detail` - Short description for hover tooltips
- `AccessModifier` - public/private/protected for filtering
- `Tags` - deprecated/experimental/internal for UI badges
- `Children` - Nested symbols for hierarchy display

### 4. Factory Pattern (Language Manager)

**Rationale:** Single point of entry for analyzer selection, easy to extend.

**Benefits:**
- Centralized language detection logic
- Consistent interface for all languages
- Easy to add language variants (e.g., "php-laravel")

## Testing Strategy

### Unit Tests
- Test each analyzer independently with temporary test files
- Verify Language field is set correctly
- Check metadata accuracy (line numbers, signatures)
- Test edge cases (empty dirs, non-existent paths, interfaces)

### Integration Tests
- Test full indexing pipeline (analyzer → embeddings → Qdrant)
- Verify search results match expectations
- Test workspace detection and multi-workspace scenarios

### Coverage Goals
- Analyzers: >80% coverage
- Core packages: >70% coverage
- Tools: >60% coverage

## Performance Considerations

### Indexing
- Batch embedding calls to reduce latency
- Use goroutines for parallel file parsing
- Cache parsed ASTs when possible

### Search
- Hybrid search combines vector + lexical for better results
- Limit results to top-k to reduce memory usage
- Use Qdrant's filtering for language-specific queries

## Multi-Language Configuration

### config.yaml

```yaml
workspace:
  enabled: true                    # Enable multi-workspace mode
  auto_index: true                 # Auto-index detected workspaces
  collection_prefix: ragcode       # Collection naming prefix
  
  # Language detection markers - file presence indicates language
  detection_markers:
    - .git                         # Generic workspace root
    - go.mod                       # Go projects
    - package.json                 # JavaScript/Node.js
    - pyproject.toml               # Python (modern)
    - setup.py                     # Python (legacy)
    - requirements.txt             # Python (pip)
    - composer.json                # PHP
    - Cargo.toml                   # Rust
    - pom.xml                      # Java (Maven)
    - build.gradle                 # Java (Gradle)
    - Gemfile                      # Ruby
    - Package.swift                # Swift
```

### Environment Variables (Advanced)

For advanced users (not recommended for typical use):

- `WORKSPACE_ENABLED` - Enable/disable multi-workspace mode (default: true)
- `WORKSPACE_AUTO_INDEX` - Auto-index detected workspaces (default: true)
- `WORKSPACE_COLLECTION_PREFIX` - Collection naming prefix (default: "ragcode")
- `WORKSPACE_MAX_WORKSPACES` - Maximum concurrent workspaces to index (default: 10)

**Note:** These variables are auto-managed by the system. Use defaults unless you have specific requirements.

## Future Enhancements

### Planned
- [x] PHP analyzer implementation (PHP + Laravel analyzer, ~84% coverage, PAS 1–10 complete, production ready)
- [ ] Python analyzer implementation (placeholder ready)
- [ ] TypeScript/JavaScript analyzer
- [ ] Cross-language symbol references
- [ ] Multi-workspace search across all languages
- [ ] Language-specific embedding models

### Under Consideration
- [ ] Incremental indexing (watch mode)
- [ ] Symbol relationship graph (calls, implements, extends)
- [ ] Code metrics and quality analysis
- [ ] Custom analyzer plugins via Go plugins

## Current Implementation Status

**Multi-Language Support:** ✅ Fully implemented architecture
- **Go**: ✅ Fully implemented with 82% test coverage (13 unit tests)
- **PHP**: ✅ Fully implemented with 83.6% test coverage (19 unit tests)
  - **Laravel Framework**: ✅ Advanced framework support (14 integration tests)
- **Python**: 🔄 Placeholder - ready for implementation
- **Other languages**: Waiting for community contributions

**Multi-Workspace Support:** ✅ Fully implemented
- Automatic detection from 11+ language markers
- Per-workspace, per-language collections
- Concurrent multi-workspace indexing
- Comprehensive test suite (15+ integration tests)

**MCP Tools:** ✅ 8 tools fully implemented
- All tools support multi-workspace and multi-language queries
- Workspace-aware collection selection

## PHP & Laravel Support

### Overview

The PHP analyzer provides comprehensive support for PHP 8.0+ codebases with advanced Laravel framework integration.

### PHP Base Analyzer (`php/`)

**Features:**
- ✅ Namespace and package detection
- ✅ Class extraction (properties, methods, constants)
- ✅ Interface extraction
- ✅ Trait extraction with usage detection
- ✅ Function extraction (global and methods)
- ✅ PHPDoc parsing for descriptions and types
- ✅ Visibility modifiers (public, protected, private)
- ✅ Type hints and return types
- ✅ AST-based analysis using VKCOM/php-parser

**Test Coverage:** 83.6% (19 unit tests)

### Laravel Framework Support (`php/laravel/`)

**Architecture:**
```
php/laravel/
├── types.go              # Laravel-specific types
├── analyzer.go           # Main coordinator
├── eloquent.go           # Eloquent model analyzer
├── controller.go         # Controller analyzer
├── ast_helper.go         # AST extraction utilities
├── *_test.go             # Comprehensive test suite
└── README.md             # Documentation
```

**Features:**

**1. Eloquent Model Analysis:**
- ✅ Model detection (extends `Illuminate\Database\Eloquent\Model`)
- ✅ Property extraction: `$table`, `$primaryKey`, `$fillable`, `$guarded`, `$casts`, `$hidden`, `$visible`, `$appends`
- ✅ Trait detection: `SoftDeletes`, `HasFactory`, custom traits
- ✅ Relationship extraction: `hasMany`, `hasOne`, `belongsTo`, `belongsToMany`, `morphMany`, etc.
- ✅ Query scopes: `scopeActive`, `scopePublished`, etc.
- ✅ Accessors/Mutators: `getFullNameAttribute`, `setPasswordAttribute`
- ✅ AST-based property parsing (handles `Post::class` syntax)

**2. Controller Analysis:**
- ✅ Resource controller detection (7 CRUD methods: index, create, store, show, edit, update, destroy)
- ✅ API controller detection (namespace `App\Http\Controllers\Api`)
- ✅ Action extraction with HTTP method inference
- ✅ Parameter extraction (with `$` prefix normalization)
- ✅ Custom action detection (non-CRUD methods)

**3. AST Helpers:**
- ✅ Property extraction: arrays, maps, strings from class properties
- ✅ Method call extraction: detects relation methods in model methods
- ✅ PHP variable name handling: automatic `$` prefix trimming
- ✅ `Class::class` constant fetch support

**Laravel Detection:**
The system automatically detects Laravel projects by checking for:
- Namespaces starting with `App\Models`, `App\Http\Controllers`
- Classes extending `Model`, `Controller`
- `Illuminate\` framework classes

**Test Coverage:**
- 14 Laravel-specific tests (100% passing)
- 4 AST helper tests
- 3 Eloquent analyzer tests
- 4 Controller analyzer tests
- 3 Integration tests

**Example Output:**

```go
// EloquentModel
{
  ClassName: "User",
  Namespace: "App\\Models",
  Table: "users",
  Fillable: ["name", "email", "password"],
  SoftDeletes: true,
  Relations: [
    {Name: "posts", Type: "hasMany", RelatedModel: "Post"},
    {Name: "profile", Type: "hasOne", RelatedModel: "Profile"}
  ],
  Scopes: [{Name: "active", MethodName: "scopeActive"}],
  Attributes: [{Name: "full_name", MethodName: "getFullNameAttribute"}]
}

// Controller
{
  ClassName: "PostController",
  Namespace: "App\\Http\\Controllers",
  IsResource: true,
  IsApi: false,
  Actions: [
    {Name: "index", HttpMethods: ["GET"]},
    {Name: "store", HttpMethods: ["POST"], Parameters: ["request"]},
    {Name: "destroy", HttpMethods: ["DELETE"], Parameters: ["post"]}
  ]
}
```

**Usage:**

```go
// Detect Laravel project
analyzer := php.NewCodeAnalyzer()
analyzer.AnalyzeFile("app/Models/User.php")
if analyzer.IsLaravelProject() {
    // Get packages and analyze with Laravel
    packages := analyzer.GetPackages()
    laravelAnalyzer := laravel.NewAnalyzer(packages[0])
    info := laravelAnalyzer.Analyze()
    
    // info.Models contains Eloquent models
    // info.Controllers contains controllers
}
```

## Contributing

When contributing code:

1. Follow the existing package structure
2. Implement both `PathAnalyzer` and `APIAnalyzer` for new languages
3. Add comprehensive tests (>80% coverage)
4. Update this architecture document
5. Set `Language` field correctly in all chunks
6. Use `codetypes` for shared types, not package-local definitions
