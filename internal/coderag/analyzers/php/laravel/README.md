# Laravel Framework Analyzer

Laravel-specific analyzer module for detecting and extracting framework features.

## Overview

This module provides framework-aware analysis on top of the base PHP analyzer. It detects Laravel-specific patterns and extracts ORM relationships, controller actions, routes, and other framework features.

## Architecture

```
laravel/
├── types.go        - Laravel-specific type definitions
├── analyzer.go     - Main Laravel analyzer coordinator
├── eloquent.go     - Eloquent Model analyzer
├── controller.go   - Controller analyzer
└── routes.go       - Route file parser (TODO)
```

## Features

### ✅ Implemented

1. **Eloquent Model Detection**
   - Detects classes extending `Illuminate\Database\Eloquent\Model`
   - Extracts model properties:
     - `$fillable` - mass assignable attributes
     - `$guarded` - protected attributes
     - `$casts` - attribute type casting
     - `$hidden` - hidden attributes for serialization
     - `$table` - custom table name
     - `$primaryKey` - custom primary key
   - Detects SoftDeletes trait usage
   - Extracts query scopes (`scopeMethodName`)
   - Detects accessors/mutators (`getXxxAttribute`, `setXxxAttribute`)

2. **Relationship Detection**
   - `hasOne()` - one-to-one
   - `hasMany()` - one-to-many
   - `belongsTo()` - inverse one-to-many
   - `belongsToMany()` - many-to-many
   - `hasManyThrough()` - has-many-through
   - Polymorphic relations (TODO)

3. **Controller Analysis**
   - Detects classes in `App\Http\Controllers` namespace
   - Identifies resource controllers (index, create, store, show, edit, update, destroy)
   - Distinguishes API controllers
   - Extracts controller actions with:
     - HTTP method inference
     - Parameter extraction
     - Return type detection
   - Middleware detection (TODO: requires method body parsing)

### 🚧 In Progress

4. **Route Analysis** (TODO)
   - Parse `routes/web.php` and `routes/api.php`
   - Extract route definitions:
     - HTTP method (GET, POST, PUT, DELETE, etc.)
     - URI pattern
     - Controller@action binding
     - Route names
     - Middleware
   - Map routes to controller actions

5. **Migration Analysis** (TODO)
   - Parse migration files
   - Extract table schemas
   - Track database structure evolution

### ❌ Planned

6. **Service Provider Analysis**
   - Detect service bindings
   - Extract IoC container registrations
   - Map contracts to implementations

7. **Middleware Analysis**
   - Parse middleware classes
   - Track middleware stacks
   - Analyze middleware groups

## Usage

### Basic Analysis

```go
import (
    "github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/php"
    "github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/php/laravel"
)

// First, analyze PHP code
phpAnalyzer := php.NewCodeAnalyzer()
packageInfo, err := phpAnalyzer.AnalyzePath("app/Models")

// Then apply Laravel-specific analysis
laravelAnalyzer := laravel.NewAnalyzer(packageInfo)
laravelInfo := laravelAnalyzer.Analyze()

// Access Laravel-specific features
for _, model := range laravelInfo.Models {
    fmt.Printf("Model: %s\n", model.ClassName)
    fmt.Printf("  Table: %s\n", model.Table)
    fmt.Printf("  Fillable: %v\n", model.Fillable)
    
    for _, relation := range model.Relations {
        fmt.Printf("  Relation: %s -> %s (%s)\n", 
            relation.Name, relation.RelatedModel, relation.Type)
    }
}
```

### Integration with PHP Analyzer

```go
// The Laravel analyzer works as a decorator over PHP analyzer
phpAnalyzer := php.NewCodeAnalyzer()
pkgInfo, _ := phpAnalyzer.AnalyzePath("app/")

// Detect if it's a Laravel project
isLaravel := detectLaravelProject(pkgInfo)

if isLaravel {
    laravelAnalyzer := laravel.NewAnalyzer(pkgInfo)
    laravelInfo := laravelAnalyzer.Analyze()
    
    // Use Laravel-specific information for better RAG chunking
    chunks := createLaravelAwareChunks(pkgInfo, laravelInfo)
}
```

## Design Patterns

### 1. Layered Analysis

```
Base PHP AST → PHP Analyzer → Laravel Analyzer → RAG Chunks
```

- **Base PHP AST**: VKCOM/php-parser provides syntax tree
- **PHP Analyzer**: Extracts classes, methods, properties (generic PHP)
- **Laravel Analyzer**: Detects framework patterns (Eloquent, Controllers, Routes)
- **RAG Chunks**: Smart chunking based on Laravel semantics

### 2. Framework Detection

The analyzer uses multiple signals to detect Laravel:
- Class inheritance (`extends Model`, `extends Controller`)
- Namespace patterns (`App\Models\`, `App\Http\Controllers\`)
- Directory structure (`app/Models/`, `routes/`)
- Trait usage (`SoftDeletes`, `Notifiable`)

### 3. Semantic Chunking

Instead of arbitrary character limits, chunks are created based on:
- **Model** = Class + Properties + Relations + Scopes
- **Controller** = Class + Actions + Middleware + Related Routes
- **Route Group** = Related routes with shared middleware/prefix

## Limitations

### Current Limitations

1. **Property Array Extraction**: Requires AST parsing of class property initializers
   - `$fillable = ['name', 'email']` → Need to parse array literals
   - **Workaround**: Parse raw source code for simple cases

2. **Method Body Analysis**: Relationship detection requires method body parsing
   - `return $this->hasMany(Post::class)` → Need to parse method calls
   - **Workaround**: Use PHPDoc annotations when available

3. **Route Files**: Routes are procedural, not class-based
   - Requires special parser for `Route::get()` calls
   - **Workaround**: Simple regex patterns for common cases

### Future Improvements

1. **AST-based Property Extraction**
   - Traverse PropertyList nodes
   - Parse array initialization expressions
   - Handle complex array structures

2. **Method Call Graph**
   - Build call graph from method bodies
   - Detect relationship chains
   - Trace service container resolutions

3. **Route-Controller Mapping**
   - Parse route files using AST
   - Match routes to controllers
   - Build complete request flow graph

## Testing

```bash
# Run Laravel analyzer tests
go test ./internal/coderag/analyzers/php/laravel/

# Test with real Laravel project
go test -v -run TestLaravelProjectAnalysis
```

## Examples

See `laravel/examples/` for sample Laravel code and expected analysis output.

## Contributing

When adding new Laravel features:
1. Add types to `types.go`
2. Create analyzer in separate file (e.g., `middleware.go`)
3. Integrate into `analyzer.go`
4. Add tests with real Laravel code samples
5. Update this README

## References

- [Laravel Documentation](https://laravel.com/docs)
- [Eloquent ORM](https://laravel.com/docs/eloquent)
- [Laravel Routing](https://laravel.com/docs/routing)
- [Service Container](https://laravel.com/docs/container)
