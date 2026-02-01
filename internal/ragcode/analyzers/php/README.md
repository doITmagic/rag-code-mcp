# PHP Code Analyzer

Code analyzer for extracting symbols, structure, and relationships from PHP files. Includes full support for the Laravel framework. Indexes code for semantic search in Qdrant.

## Status: ✅ PRODUCTION READY

---

## 🎯 What This Analyzer Does

The PHP analyzer parses `.php` files and extracts:
1. **Symbols** - classes, methods, functions, interfaces, traits, constants
2. **Relationships** - inheritance, implementations, Eloquent relations
3. **Metadata** - PHPDoc, visibility, types, Laravel-specific
4. **Framework** - Eloquent models, Controllers, Routes (Laravel)

---

## 📊 Data Flow

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   .php Files    │────▶│   PHP Analyzer   │────▶│   CodeChunks    │
│  (source code)  │     │  (VKCOM parser)  │     │  (structured)   │
└─────────────────┘     └──────────────────┘     └────────┬────────┘
                                                          │
                        ┌──────────────────┐              │
                        │ Laravel Analyzer │◀─────────────┤
                        │ (Eloquent, etc.) │              │
                        └──────────────────┘              ▼
                                                 ┌─────────────────┐
                                                 │     Qdrant      │
                                                 │  (vector store) │
                                                 └─────────────────┘
```

---

## 🔍 What We Index

### 1. Classes (`type: "class"`)

```php
<?php
namespace App\Models;

/**
 * Represents a user in the system.
 */
class User extends Model implements Authenticatable
{
    use SoftDeletes, Notifiable;
    
    protected $fillable = ['name', 'email'];
    protected $casts = ['email_verified_at' => 'datetime'];
}
```

**Extracted information:**
| Field | Value | Description |
|-------|-------|-------------|
| `name` | `"User"` | Class name |
| `namespace` | `"App\\Models"` | Namespace |
| `full_name` | `"App\\Models\\User"` | Fully qualified name |
| `extends` | `"Model"` | Parent class |
| `implements` | `["Authenticatable"]` | Implemented interfaces |
| `traits` | `["SoftDeletes", "Notifiable"]` | Used traits |
| `is_abstract` | `false` | If abstract |
| `is_final` | `false` | If final |
| `docstring` | `"Represents a user..."` | PHPDoc |

### 2. Methods (`type: "method"`)

```php
/**
 * Returns the user's orders.
 * 
 * @param int $limit Maximum number of orders
 * @return Collection<Order>
 */
public function getOrders(int $limit = 10): Collection
{
    return $this->orders()->limit($limit)->get();
}
```

**Extracted information:**
| Field | Value | Description |
|-------|-------|-------------|
| `name` | `"getOrders"` | Method name |
| `visibility` | `"public"` | Visibility |
| `is_static` | `false` | If static |
| `is_abstract` | `false` | If abstract |
| `parameters` | `[{name: "limit", type: "int", default: "10"}]` | Parameters |
| `return_type` | `"Collection"` | Return type |
| `phpdoc.params` | `[{name: "limit", type: "int", desc: "..."}]` | PHPDoc params |
| `phpdoc.return` | `{type: "Collection<Order>", desc: ""}` | PHPDoc return |

### 3. Interfaces (`type: "interface"`)

```php
interface PaymentGateway extends Gateway
{
    public function charge(float $amount): bool;
    public function refund(string $transactionId): bool;
}
```

### 4. Traits (`type: "trait"`)

```php
trait Auditable
{
    public function getCreatedBy(): ?User { ... }
    public function logActivity(string $action): void { ... }
}
```

### 5. Global Functions (`type: "function"`)

```php
/**
 * Helper for price formatting.
 */
function format_price(float $amount, string $currency = 'USD'): string
{
    return number_format($amount, 2) . ' ' . $currency;
}
```

---

## 🔗 Laravel Framework Support

### Eloquent Models

```php
class Order extends Model
{
    protected $fillable = ['user_id', 'total', 'status'];
    protected $casts = ['total' => 'decimal:2'];
    
    public function user(): BelongsTo
    {
        return $this->belongsTo(User::class);
    }
    
    public function items(): HasMany
    {
        return $this->hasMany(OrderItem::class);
    }
    
    public function scopeCompleted($query)
    {
        return $query->where('status', 'completed');
    }
    
    public function getTotalFormattedAttribute(): string
    {
        return number_format($this->total, 2) . ' USD';
    }
}
```

**Extracted Laravel metadata:**
```json
{
  "is_eloquent_model": true,
  "fillable": ["user_id", "total", "status"],
  "casts": {"total": "decimal:2"},
  "relations": [
    {"name": "user", "type": "belongsTo", "related": "User"},
    {"name": "items", "type": "hasMany", "related": "OrderItem"}
  ],
  "scopes": ["completed"],
  "accessors": ["total_formatted"]
}
```

### Controllers

```php
class OrderController extends Controller
{
    public function index(): View { ... }
    public function store(OrderRequest $request): RedirectResponse { ... }
    public function show(Order $order): View { ... }
}
```

**Controller metadata:**
```json
{
  "is_controller": true,
  "is_resource_controller": true,
  "actions": ["index", "store", "show"],
  "http_methods": {
    "index": "GET",
    "store": "POST",
    "show": "GET"
  }
}
```

### Routes

```php
Route::get('/orders', [OrderController::class, 'index'])->name('orders.index');
Route::resource('users', UserController::class);
```

---

## 🏗️ File Structure

```
php/
├── types.go              # PHP types: ClassInfo, MethodInfo, etc.
├── analyzer.go           # PathAnalyzer implementation (21KB)
├── api_analyzer.go       # APIAnalyzer for documentation
├── phpdoc.go             # PHPDoc parser
├── analyzer_test.go      # 10 CodeAnalyzer tests
├── api_analyzer_test.go  # 4 APIAnalyzer tests
├── parser_test.go        # 5 parser tests
├── README.md             # This documentation
└── laravel/              # Laravel module
    ├── types.go          # Laravel-specific types
    ├── analyzer.go       # Laravel coordinator
    ├── adapter.go        # PathAnalyzer adapter
    ├── eloquent.go       # Eloquent Models analyzer
    ├── controller.go     # Controllers analyzer
    ├── routes.go         # Routes analyzer
    └── README.md         # Laravel documentation
```

---

## 💻 Usage

```go
import "github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php/laravel"

// For Laravel projects (recommended)
analyzer := laravel.NewAdapter()

// Analyze directories/files
chunks, err := analyzer.AnalyzePaths([]string{"./app"})

for _, chunk := range chunks {
    fmt.Printf("[%s] %s\n", chunk.Type, chunk.Name)
    if relations, ok := chunk.Metadata["relations"]; ok {
        fmt.Printf("  Relations: %v\n", relations)
    }
}
```

---

## 🔌 Integration

### Language Manager

The PHP/Laravel analyzer is automatically selected for:
- `php` - generic PHP projects
- `laravel` - Laravel projects
- `php-laravel` - Laravel alternative

### Workspace Detection

| File/Directory | Project Type |
|----------------|--------------|
| `artisan` | Laravel |
| `composer.json` | PHP |
| `app/Models/` | Laravel |
| `routes/web.php` | Laravel |

---

## 📋 CodeChunk Types

| Type | Description | Example |
|------|-------------|---------|
| `class` | PHP class | `class User extends Model` |
| `method` | Class method | `public function save()` |
| `function` | Global function | `function helper()` |
| `interface` | Interface | `interface Payable` |
| `trait` | Trait | `trait Auditable` |
| `const` | Class constant | `const STATUS_ACTIVE = 1` |
| `property` | Property | `protected $fillable` |

---

## 🏷️ Complete Metadata

### Class Metadata
```json
{
  "namespace": "App\\Models",
  "extends": "Model",
  "implements": ["Authenticatable"],
  "traits": ["SoftDeletes"],
  "is_abstract": false,
  "is_final": false,
  "is_eloquent_model": true,
  "fillable": ["name", "email"],
  "relations": [...]
}
```

### Method Metadata
```json
{
  "class_name": "UserController",
  "visibility": "public",
  "is_static": false,
  "is_abstract": false,
  "is_final": false,
  "parameters": [...],
  "return_type": "View",
  "phpdoc": {
    "description": "...",
    "params": [...],
    "return": {...}
  }
}
```

---

## 🧪 Testing

```bash
# All PHP tests (19 tests)
go test ./internal/ragcode/analyzers/php/...

# Laravel only (21 tests)
go test ./internal/ragcode/analyzers/php/laravel/...

# With coverage
go test -cover ./internal/ragcode/analyzers/php/...
```

**Results:**
- ✅ 19/19 PHP tests PASS
- ✅ 21/21 Laravel tests PASS
- ✅ Coverage: 83.6%

---

## 📦 Dependencies

- **VKCOM/php-parser** v0.8.2 - PHP parser with PHP 8.0-8.2 support

---

## ⚠️ Limitations

| Limitation | Description |
|------------|-------------|
| **No Runtime** | Static analysis, doesn't execute code |
| **Single-file** | Each file is analyzed independently |
| **No Autoload** | Doesn't resolve Composer autoload |

---

## 🔮 Future Improvements

- [ ] Route groups with middleware
- [ ] Migration analyzer
- [ ] Symfony framework support
- [ ] WordPress support
- [ ] Cross-file type resolution

---

## Implemented Features

### Laravel-Specific Features ✅

1. **Eloquent Models** (COMPLETE):
   - ✅ Model detection (`extends Model`)
   - ✅ Property extraction: `$fillable`, `$guarded`, `$casts`, `$table`, `$primaryKey`
   - ✅ **All Relations**: `hasOne`, `hasMany`, `belongsTo`, `belongsToMany`, `hasManyThrough`, `morphTo`, `morphMany`, `morphToMany`, `morphedByMany`
   - ✅ Foreign key & local key extraction
   - ✅ Fully-qualified name resolution with imports
   - ✅ **Scopes**: `scopeMethodName()` detection
   - ✅ **Accessors/Mutators**: `getXxxAttribute()`, `setXxxAttribute()`
   - ✅ SoftDeletes trait detection

2. **Controllers** (COMPLETE):
   - ✅ Controller detection (`extends Controller`)
   - ✅ Resource controller identification (7 RESTful actions)
   - ✅ API controller detection
   - ✅ HTTP method inference from action names
   - ✅ Parameter extraction

3. **Routes** (COMPLETE):
   - ✅ Route file parsing (`routes/web.php`, `routes/api.php`)
   - ✅ `Route::get()`, `Route::post()`, etc.
   - ✅ `Route::match()` support
   - ✅ `Route::resource()` expansion
   - ✅ Controller@action binding
   - ✅ Array syntax `[Controller::class, 'action']`

### Core PHP Features ✅

1. **Namespace Support**
   - Multi-namespace per-file
   - Fully qualified names

2. **Class Analysis**
   - Class declarations with extends/implements
   - Method extraction (visibility, static, abstract, final)
   - Property extraction (visibility, static, readonly, typed)
   - Class constants (visibility, value extraction)
   - Parameter and return type support
   - **PHPDoc extraction** (description, @param, @return)

3. **Interface Support**
   - Interface declarations
   - Method signatures
   - Multiple interface extends
   - **PHPDoc documentation**

4. **Trait Support**
   - Trait declarations
   - Methods and properties
   - **PHPDoc documentation**

5. **Function Analysis**
   - Global functions
   - Namespaced functions
   - Parameters and return types
   - **PHPDoc documentation**

6. **PHPDoc Parsing**
   - Description extraction
   - @param tags (type, name, description)
   - @return tags (type, description)
   - @throws, @var, @deprecated, @see, @example tags
   - Type hint merging with PHPDoc

---

## Code Metrics

- **Total Lines**: ~1,800
- **Core Implementation**: 
  - `analyzer.go` (21KB, 814 lines)
  - `api_analyzer.go` (7.4KB, 293 lines)
  - `phpdoc.go` (5.3KB, 217 lines)
- **Helper Functions**: 25+
- **Test Coverage**: 83.6%
- **Tests**: 19 (5 parser + 10 analyzer + 4 API)
- **Integration Tests**: 6 (language manager + workspace detector)

---

## Architecture

Follows the same pattern as `golang` analyzer with modular framework support:

```
php/
├── types.go              - Internal type definitions
├── analyzer.go           - PathAnalyzer implementation
├── api_analyzer.go       - APIAnalyzer implementation
├── phpdoc.go             - PHPDoc parser (PHP-specific helper)
├── analyzer_test.go      - CodeAnalyzer tests
├── api_analyzer_test.go  - APIAnalyzer tests
├── parser_test.go        - Parser validation tests
└── laravel/              - Laravel framework module (separate package)
    ├── types.go          - Laravel-specific types (Eloquent, Controllers, Routes)
    ├── analyzer.go       - Laravel framework analyzer coordinator
    ├── eloquent.go       - Eloquent Model analyzer
    ├── controller.go     - Controller analyzer
    └── README.md         - Laravel module documentation
```

### Framework Modules

Framework-specific analyzers are separated into their own packages:
- **laravel/** - Laravel framework support (Eloquent, Controllers, Routes)
- **symfony/** - Symfony framework support (planned)
- **wordpress/** - WordPress support (planned)

This modular design allows:
- Clean separation of concerns
- Independent testing of framework features
- Easy addition of new frameworks
- Reusable base PHP analyzer
